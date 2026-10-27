package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var assets embed.FS

type localServer struct {
	spike  *spike
	mu     sync.Mutex
	csrf   string
	appURL *url.URL
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *localServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "scope": "local feasibility spike"})
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"paypal_client_id": s.spike.config.ClientID, "actual_merchant_id": s.spike.config.MerchantID, "actual_merchant_label": s.spike.config.MerchantLabel, "currency": "USD", "intent": "AUTHORIZE", "csrf_token": s.csrf, "scenarios": scenarios})
	})
	mux.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, s.spike.store.data)
	})
	mux.HandleFunc("POST /api/runs", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Scenario string `json:"scenario"`
		}
		if err := decodeInput(w, r, &input); err != nil {
			writeError(w, 400, err)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		run, err := s.spike.create(r.Context(), input.Scenario)
		if err != nil {
			writeJSON(w, 409, map[string]any{"error": err.Error(), "run": run})
			return
		}
		writeJSON(w, 201, run)
	})
	mux.HandleFunc("POST /api/runs/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			OrderID string `json:"order_id"`
		}
		if err := decodeInput(w, r, &input); err != nil {
			writeError(w, 400, err)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		run := s.spike.store.data.Runs[r.PathValue("id")]
		if run == nil {
			writeError(w, 404, errors.New("unknown run"))
			return
		}
		if err := s.spike.action(r.Context(), run, r.PathValue("action"), input.OrderID); err != nil {
			writeJSON(w, 409, map[string]any{"error": err.Error(), "run": run})
			return
		}
		writeJSON(w, 200, run)
	})
	mux.HandleFunc("POST /api/webhooks/paypal", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
		if err != nil {
			writeError(w, 400, errors.New("webhook body too large or unreadable"))
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err = s.spike.paypal.verifyWebhook(r.Context(), r.Header, body); err != nil {
			writeError(w, 401, errors.New("webhook signature not verified"))
			return
		}
		var event struct {
			ID       string `json:"id"`
			Type     string `json:"event_type"`
			Resource struct {
				ID string `json:"id"`
			} `json:"resource"`
		}
		if json.Unmarshal(body, &event) != nil || !safeCode.MatchString(event.ID) || !safeCode.MatchString(event.Type) {
			writeError(w, 400, errors.New("webhook event fields invalid"))
			return
		}
		if err = s.spike.store.receipt(map[string]any{"source": "real-paypal-app-webhook", "verified": true, "recorded_at": s.spike.now().UTC(), "event_id": event.ID, "event_type": event.Type, "resource_id": code(event.Resource.ID), "note": "P00 delivery evidence only; no payment state mutation. Durable event deduplication and reconciliation are P02."}); err != nil {
			writeError(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := map[string]string{"/": "web/index.html", "/app.js": "web/app.js", "/style.css": "web/style.css"}[r.URL.Path]
		if name == "" {
			http.NotFound(w, r)
			return
		}
		b, err := assets.ReadFile(name)
		if err != nil {
			http.Error(w, "asset unavailable", 500)
			return
		}
		if strings.HasSuffix(name, ".js") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		} else if strings.HasSuffix(name, ".css") {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		_, _ = w.Write(b)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		// A loopback listener alone does not prevent DNS rebinding or cross-origin POSTs.
		if r.Host != s.appURL.Host {
			writeError(w, 403, errors.New("unexpected local host"))
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.URL.Path != "/api/webhooks/paypal" {
			if r.Header.Get("Origin") != s.appURL.String() || r.Header.Get("X-CSRF-Token") != s.csrf || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeError(w, 403, errors.New("same-origin JSON and CSRF token required"))
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func decodeInput(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return errors.New("invalid JSON request")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func serve(c config) error {
	if err := c.requirePayPal(); err != nil {
		return err
	}
	u, _ := url.Parse(c.AppURL)
	// Bind before opening the spike to prevent two operators using the same local address.
	listener, err := net.Listen("tcp", u.Host)
	if err != nil {
		return errors.New("local spike address is unavailable; stop any existing instance")
	}
	defer listener.Close()
	if err := os.MkdirAll(c.EvidenceDir, 0700); err != nil {
		return errors.New("cannot create evidence directory")
	}
	lockPath := filepath.Join(c.EvidenceDir, "server.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("evidence directory is locked; stop the existing server or inspect a stale server.lock after a crash")
	}
	defer os.Remove(lockPath)
	if _, err = fmt.Fprintf(lock, "pid=%d\n", os.Getpid()); err != nil {
		_ = lock.Close()
		return errors.New("cannot write server lock")
	}
	if err = lock.Close(); err != nil {
		return errors.New("cannot close server lock")
	}
	st, err := openStore(c.EvidenceDir)
	if err != nil {
		return err
	}
	s := &localServer{spike: &spike{config: c, store: st, paypal: newPayPal(c), now: time.Now, evidenceSource: "real-paypal-sandbox"}, csrf: newID(), appURL: u}
	server := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Printf("Local sandbox feasibility spike: %s\nEvidence: planning/evidence/P00 (or configured local directory)\n", c.AppURL)
	if err = server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return errors.New("local HTTP server failed")
	}
	return nil
}
