// Package notify delivers advisory notices. Errors contain safe codes only.
package notify

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/ramst/pause/internal/config"
)

type Message struct{ To, Subject, Body string }
type Mailer interface {
	Send(context.Context, Message) error
}
type Error struct {
	Code      string
	Temporary bool
}

func (e *Error) Error() string { return e.Code }

func New(cfg config.Config) Mailer {
	switch cfg.MailMode {
	case "log":
		return &LogMailer{Logger: slog.Default()}
	case "smtp":
		return &SMTPMailer{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.MailFrom}
	default:
		return nil
	}
}

type LogMailer struct{ Logger *slog.Logger }

func (m *LogMailer) Send(_ context.Context, msg Message) error {
	if strings.ContainsAny(msg.To+msg.Subject, "\r\n") {
		return &Error{Code: "MAIL_REJECTED"}
	}
	a, err := mail.ParseAddress(msg.To)
	if err != nil {
		return &Error{Code: "MAIL_REJECTED"}
	}
	domain := a.Address[strings.LastIndex(a.Address, "@")+1:]
	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("notification_logged", "domain", domain, "subject_bytes", len(msg.Subject))
	return nil
}

type SMTPMailer struct {
	Host                     string
	Port                     int
	Username, Password, From string
}

func wireMessage(from string, msg Message) ([]byte, string, string, error) {
	if strings.ContainsAny(from+msg.To+msg.Subject, "\r\n") {
		return nil, "", "", &Error{Code: "MAIL_REJECTED"}
	}
	f, err := mail.ParseAddress(from)
	if err != nil {
		return nil, "", "", &Error{Code: "MAIL_REJECTED"}
	}
	t, err := mail.ParseAddress(msg.To)
	if err != nil {
		return nil, "", "", &Error{Code: "MAIL_REJECTED"}
	}
	subject := msg.Subject
	for _, r := range subject {
		if r > 127 {
			subject = mime.QEncoding.Encode("UTF-8", subject)
			break
		}
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return nil, "", "", &Error{Code: "MAIL_REJECTED"}
	}
	body := strings.ReplaceAll(strings.ReplaceAll(msg.Body, "\r\n", "\n"), "\r", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	raw := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@pause.local>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", f.String(), t.String(), subject, time.Now().Format(time.RFC1123Z), hex.EncodeToString(id[:]), body)
	return []byte(raw), f.Address, t.Address, nil
}

func failure(err error, code string) error {
	var n net.Error
	if errors.As(err, &n) && n.Timeout() {
		return &Error{Code: "MAIL_TIMEOUT", Temporary: true}
	}
	var response *textproto.Error
	if errors.As(err, &response) {
		return &Error{Code: code, Temporary: response.Code >= 400 && response.Code < 500}
	}
	return &Error{Code: code, Temporary: code == "MAIL_DIAL" || code == "MAIL_TIMEOUT"}
}

func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	raw, from, to, err := wireMessage(m.From, msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	dialer := &net.Dialer{Timeout: 15 * time.Second, Deadline: deadline}
	address := net.JoinHostPort(m.Host, strconv.Itoa(m.Port))
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return failure(err, "MAIL_DIAL")
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)
	socket := conn
	stop := context.AfterFunc(ctx, func() { _ = socket.SetDeadline(time.Now()) })
	defer stop()
	tlsConfig := &tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12}
	if m.Port == 465 {
		secure := tls.Client(conn, tlsConfig)
		if err = secure.HandshakeContext(ctx); err != nil {
			return failure(err, "MAIL_TLS")
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return failure(err, "MAIL_DIAL")
	}
	defer client.Close()
	if m.Port != 465 {
		if err = requireSTARTTLS(client, m.Host, tlsConfig); err != nil {
			return err
		}
	}
	if m.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", m.Username, m.Password, m.Host)); err != nil {
			return failure(err, "MAIL_AUTH")
		}
	}
	if err = client.Mail(from); err != nil {
		return failure(err, "MAIL_REJECTED")
	}
	if err = client.Rcpt(to); err != nil {
		return failure(err, "MAIL_REJECTED")
	}
	w, err := client.Data()
	if err != nil {
		return failure(err, "MAIL_REJECTED")
	}
	if _, err = w.Write(raw); err != nil {
		return failure(err, "MAIL_REJECTED")
	}
	if err = w.Close(); err != nil {
		return failure(err, "MAIL_REJECTED")
	}
	// DATA acceptance is delivery success; a failed QUIT must not duplicate mail.
	_ = client.Quit()
	return nil
}

func requireSTARTTLS(client *smtp.Client, host string, cfg *tls.Config) error {
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(cfg); err != nil {
			return failure(err, "MAIL_TLS")
		}
	} else if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return &Error{Code: "MAIL_TLS"}
	}
	return nil
}
