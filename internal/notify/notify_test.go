package notify

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ramst/pause/internal/config"
)

func TestHeaderInjection(t *testing.T) {
	for _, injection := range []string{"\r", "\n", "\r\nBcc: stolen@pause.dev"} {
		for _, field := range []string{"from", "to", "subject"} {
			from, msg := "sender@pause.dev", Message{To: "recipient@pause.dev", Subject: "Notice", Body: "body"}
			switch field {
			case "from":
				from += injection
			case "to":
				msg.To += injection
			case "subject":
				msg.Subject += injection
			}
			_, _, _, err := wireMessage(from, msg)
			if err == nil || err.Error() != "MAIL_REJECTED" {
				t.Fatal("injection accepted or leaked")
			}
		}
	}
}

func TestWireHeaders(t *testing.T) {
	subject := "Did you ask Renée for $40.00?"
	raw, _, _, err := wireMessage("Pause <sender@pause.dev>", Message{To: "recipient@pause.dev", Subject: subject, Body: "First\n.Second\r\nThird"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Content-Type", "Content-Transfer-Encoding"} {
		if parsed.Header.Get(header) == "" {
			t.Fatal("missing", header)
		}
	}
	if !strings.HasPrefix(parsed.Header.Get("Subject"), "=?UTF-8?q?") {
		t.Fatal("subject not Q encoded")
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || decoded != subject {
		t.Fatal("subject round trip")
	}
	if _, err = mail.ParseDate(parsed.Header.Get("Date")); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ReplaceAll(raw, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("bare LF")
	}
	body, _ := io.ReadAll(parsed.Body)
	if string(body) != "First\r\n.Second\r\nThird\r\n" {
		t.Fatal("body normalization")
	}
}

// A small real TCP SMTP catcher; captures the on-wire DATA including dot stuffing.
func catcher(t *testing.T) (string, <-chan string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	captured := make(chan string, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		r, w := bufio.NewReader(conn), bufio.NewWriter(conn)
		reply := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
		reply("220 catcher")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				reply("250-catcher\r\n250 8BITMIME")
			case strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				reply("250 accepted")
			case strings.HasPrefix(line, "DATA"):
				reply("354 continue")
				var data strings.Builder
				for {
					v, e := r.ReadString('\n')
					if e != nil {
						return
					}
					if v == ".\r\n" {
						break
					}
					data.WriteString(v)
				}
				captured <- data.String()
				reply("250 accepted")
			case strings.HasPrefix(line, "QUIT"):
				reply("221 bye")
				return
			default:
				reply("500 unexpected")
			}
		}
	}()
	return l.Addr().String(), captured
}

func TestLoopbackPlainSMTP(t *testing.T) {
	addr, captured := catcher(t)
	host, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	m := &SMTPMailer{Host: host, Port: n, From: "sender@pause.dev"}
	if err := m.Send(context.Background(), Message{To: "recipient@pause.dev", Subject: "A notice", Body: "Exact body\n.line"}); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-captured:
		parsed, err := mail.ReadMessage(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(parsed.Body)
		if string(body) != "Exact body\r\n..line\r\n" || parsed.Header.Get("To") != "<recipient@pause.dev>" {
			t.Fatal("unexpected DATA")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no DATA")
	}
}

func TestRemoteWithoutSTARTTLSRefused(t *testing.T) {
	addr, _ := catcher(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c, err := smtp.NewClient(conn, "smtp.remote.invalid")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = requireSTARTTLS(c, "smtp.remote.invalid", &tls.Config{MinVersion: tls.VersionTLS12})
	if err == nil || err.Error() != "MAIL_TLS" {
		t.Fatal("plain remote SMTP accepted")
	}
}

func TestSafeErrorsAndLog(t *testing.T) {
	for _, input := range []error{&textproto.Error{Code: 451, Msg: "recipient@pause.dev secret server response"}, &textproto.Error{Code: 550, Msg: "recipient@pause.dev secret"}, context.DeadlineExceeded, errors.New("recipient@pause.dev")} {
		err := failure(input, "MAIL_REJECTED")
		if strings.Contains(err.Error(), "@") || strings.Contains(err.Error(), "secret") {
			t.Fatal("error leaked")
		}
	}
	var out bytes.Buffer
	m := &LogMailer{Logger: slog.New(slog.NewJSONHandler(&out, nil))}
	if err := m.Send(context.Background(), Message{To: "private@pause.dev", Subject: "notice", Body: "private body"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "notification_logged") || !strings.Contains(out.String(), "pause.dev") || strings.Contains(out.String(), "private") || strings.Contains(out.String(), "notice\"") {
		t.Fatal("unsafe log")
	}
	if New(config.Config{MailMode: "off"}) != nil {
		t.Fatal("off enabled")
	}
}
