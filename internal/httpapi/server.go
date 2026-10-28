package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ramst/pause/internal/app"
	"github.com/ramst/pause/internal/auth"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/migrations"
)

const SessionCookie = "pause_session"
const CSRFCookie = "pause_csrf"

type contextKey int

const (
	sessionKey contextKey = iota
	correlationKey
)

type Server struct {
	App  *app.Service
	Auth *auth.Service
	Log  *slog.Logger
}
type envelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(s.observe)
	r.Use(s.security)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "alive"}) })
	r.Get("/readyz", s.ready)
	r.Post("/api/webhooks/paypal", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			s.failure(w, r, domain.Fail("BODY_TOO_LARGE", 413))
			return
		}
		e = s.App.IngestWebhook(r.Context(), r.Header, raw)
		s.respond(w, r, map[string]string{"status": "STORED"}, e, 200)
	})
	r.Route("/api", func(r chi.Router) {
		r.Post("/auth/login", s.login)
		r.Group(func(r chi.Router) {
			r.Use(s.authenticate)
			r.Get("/session", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]any{"actor": session(r).Actor}) })
			r.Post("/auth/logout", s.logout)
			r.Get("/configuration", func(w http.ResponseWriter, r *http.Request) {
				c := s.App.Config
				payment := "UNAVAILABLE"
				if s.App.Provider != nil {
					payment = "SANDBOX_AUTHORIZATION_ONLY"
				}
				write(w, 200, map[string]any{"merchant_id": c.MerchantID, "merchant_label": c.MerchantLabel, "currency": "USD", "max_amount_minor": c.MaxAmount, "message_retention_hours": int(c.MessageRetention.Hours()), "payment_status": payment, "paypal_client_id": c.PayPalClientID, "investigation_status": "UNAVAILABLE"})
			})
			r.With(s.requireRole(domain.Senior)).Get("/contacts", func(w http.ResponseWriter, r *http.Request) {
				out, e := s.App.Contacts(r.Context(), session(r).Actor)
				s.respond(w, r, out, e, 200)
			})
			r.With(s.requireRole(domain.Senior, domain.Reviewer)).Get("/intents", func(w http.ResponseWriter, r *http.Request) {
				out, e := s.App.List(r.Context(), session(r).Actor)
				s.respond(w, r, out, e, 200)
			})
			r.With(s.requireRole(domain.Senior)).Post("/intents", func(w http.ResponseWriter, r *http.Request) {
				var in app.Intake
				if !s.decode(w, r, &in) {
					return
				}
				out, replay, e := s.App.Create(r.Context(), session(r).Actor, in, r.Header.Get("Idempotency-Key"), correlation(r))
				status := 201
				if replay {
					status = 200
				}
				s.respond(w, r, out, e, status)
			})
			r.Get("/intents/{id}", func(w http.ResponseWriter, r *http.Request) {
				out, e := s.App.Read(r.Context(), session(r).Actor, chi.URLParam(r, "id"))
				s.respond(w, r, out, e, 200)
			})
			for path, kind := range map[string]string{"paypal-order": "CREATE_ORDER", "authorize": "AUTHORIZE"} {
				r.With(s.requireRole(domain.Senior)).Post("/intents/{id}/"+path, func(w http.ResponseWriter, r *http.Request) {
					var in app.PaymentAction
					if !s.decode(w, r, &in) {
						return
					}
					out, e := s.App.ReserveCheckout(r.Context(), session(r).Actor, chi.URLParam(r, "id"), kind, r.Header.Get("Idempotency-Key"), in, correlation(r))
					s.respond(w, r, out, e, 202)
				})
			}
			r.With(s.requireRole(domain.Senior, domain.Reviewer)).Post("/intents/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					ExpectedVersion int `json:"expected_version"`
				}
				if !s.decode(w, r, &in) {
					return
				}
				out, e := s.App.Cancel(r.Context(), session(r).Actor, chi.URLParam(r, "id"), in.ExpectedVersion, correlation(r))
				s.respond(w, r, out, e, 202)
			})
			r.With(s.requireRole(domain.Senior, domain.Reviewer)).Get("/intents/{id}/payment-diagnostics", func(w http.ResponseWriter, r *http.Request) {
				out, e := s.App.Diagnostics(r.Context(), session(r).Actor, chi.URLParam(r, "id"))
				s.respond(w, r, out, e, 200)
			})
			r.With(s.requireRole(domain.Senior, domain.Reviewer)).Get("/intents/{id}/provider-state", func(w http.ResponseWriter, r *http.Request) {
				out, e := s.App.ProviderDiagnostic(r.Context(), session(r).Actor, chi.URLParam(r, "id"))
				if e != nil {
					var fault *domain.Fault
					if errors.As(e, &fault) {
						s.failure(w, r, e)
						return
					}
					s.failure(w, r, domain.Fail("PROVIDER_READ_UNAVAILABLE", 503))
					return
				}
				s.respond(w, r, out, nil, 200)
			})
			r.Get("/intents/{id}/message", func(w http.ResponseWriter, r *http.Request) {
				out, e := s.App.Message(r.Context(), session(r).Actor, chi.URLParam(r, "id"))
				s.respond(w, r, map[string]string{"message": out}, e, 200)
			})
			r.With(s.requireRole(domain.Senior)).Post("/intents/{id}/corrections", func(w http.ResponseWriter, r *http.Request) {
				var in app.Correction
				if !s.decode(w, r, &in) {
					return
				}
				out, e := s.App.Correct(r.Context(), session(r).Actor, chi.URLParam(r, "id"), in, correlation(r))
				s.respond(w, r, out, e, 200)
			})
			r.With(s.requireRole(domain.Senior)).Put("/intents/{id}/consent", func(w http.ResponseWriter, r *http.Request) {
				var in app.ConsentChange
				if !s.decode(w, r, &in) {
					return
				}
				out, e := s.App.ChangeConsent(r.Context(), session(r).Actor, chi.URLParam(r, "id"), in, correlation(r))
				s.respond(w, r, out, e, 200)
			})
			r.With(s.requireRole(domain.Senior)).Post("/intents/{id}/abandon", func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					ExpectedVersion int `json:"expected_version"`
				}
				if !s.decode(w, r, &in) {
					return
				}
				e := (app.Lifecycle{Service: s.App}).Abandon(r.Context(), session(r).Actor, chi.URLParam(r, "id"), in.ExpectedVersion, correlation(r))
				s.respond(w, r, map[string]string{"status": "ABANDONED"}, e, 200)
			})
			r.With(s.requireRole(domain.Subject)).Get("/tasks", func(w http.ResponseWriter, r *http.Request) {
				out, e := s.App.Tasks(r.Context(), session(r).Actor)
				s.respond(w, r, out, e, 200)
			})
			r.With(s.requireRole(domain.Subject)).Post("/verifications/{id}/respond", func(w http.ResponseWriter, r *http.Request) {
				var in app.Answer
				if !s.decode(w, r, &in) {
					return
				}
				e := s.App.RespondVerification(r.Context(), session(r).Actor, chi.URLParam(r, "id"), in, correlation(r))
				s.respond(w, r, map[string]string{"status": "RECORDED"}, e, 200)
			})
			r.With(s.requireRole(domain.Senior, domain.Reviewer)).Post("/intents/{id}/request-verification", func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Version int `json:"expected_version"`
				}
				if !s.decode(w, r, &in) {
					return
				}
				e := s.App.RequestVerification(r.Context(), session(r).Actor, chi.URLParam(r, "id"), in.Version, correlation(r))
				s.respond(w, r, map[string]string{"status": "REQUESTED"}, e, 200)
			})
			r.With(s.requireRole(domain.Senior, domain.Reviewer)).Post("/intents/{id}/analyze", func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Version int `json:"expected_version"`
				}
				if !s.decode(w, r, &in) {
					return
				}
				out, e := s.App.Analyze(r.Context(), session(r).Actor, chi.URLParam(r, "id"), in.Version)
				s.respond(w, r, out, e, 200)
			})
			r.With(s.requireRole(domain.Senior, domain.Reviewer)).Get("/intents/{id}/evidence", func(w http.ResponseWriter, r *http.Request) {
				out, e := s.App.Evidence(r.Context(), session(r).Actor, chi.URLParam(r, "id"))
				s.respond(w, r, out, e, 200)
			})
			r.With(s.requireRole(domain.Reviewer)).Post("/intents/{id}/review", func(w http.ResponseWriter, r *http.Request) {
				var in app.ReviewDecision
				if !s.decode(w, r, &in) {
					return
				}
				e := s.App.Review(r.Context(), session(r).Actor, chi.URLParam(r, "id"), in, correlation(r))
				s.respond(w, r, map[string]string{"status": "RESERVED"}, e, 202)
			})
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { s.failure(w, r, domain.Fail("NOT_FOUND", 404)) })
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) { s.failure(w, r, domain.Fail("METHOD_NOT_ALLOWED", 405)) })
	})
	r.Get("/*", s.static)
	return r
}
func write(w http.ResponseWriter, status int, out any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}
func (s *Server) respond(w http.ResponseWriter, r *http.Request, out any, e error, status int) {
	if e != nil {
		s.failure(w, r, e)
		return
	}
	write(w, status, out)
}
func (s *Server) failure(w http.ResponseWriter, r *http.Request, e error) {
	code, status := "INTERNAL_ERROR", 500
	var f *domain.Fault
	if errors.As(e, &f) {
		code, status = f.Code, f.Status
	}
	if status == 500 {
		s.Log.Error("request_failed", "request_id", correlation(r), "reason_code", code)
	}
	out := envelope{}
	out.Error.Code = code
	out.Error.Message = message(code)
	out.Error.RequestID = correlation(r)
	write(w, status, out)
}
func message(code string) string {
	switch code {
	case "INVALID_CREDENTIALS":
		return "Email or password is incorrect."
	case "UNAUTHENTICATED":
		return "Please sign in."
	case "LOGIN_THROTTLED":
		return "Too many sign-in attempts. Try again in 15 minutes."
	case "NOT_FOUND":
		return "This record is unavailable."
	case "VERSION_CONFLICT", "CONSENT_VERSION_CONFLICT":
		return "This record changed. Reload and try again."
	case "HISTORY_REVIEW_REQUIRED", "EVIDENCE_CONFLICT", "RELEASE_EVIDENCE_REQUIRED":
		return "The request or its evidence changed. Run message checks, refresh the evidence, and review it again."
	case "PRIVATE_SOURCES_UNAVAILABLE":
		return "Private history search is unavailable. Retry the checks before approving. Cancellation and subject responses remain available."
	case "VERSION_BOUND":
		return "This version is already bound to checkout."
	case "INVALID_AMOUNT":
		return "Enter a positive USD amount with at most two decimal places, within the prototype limit."
	case "INTERNAL_ERROR":
		return "The request could not be completed."
	default:
		return "The request was rejected. Check your inputs and permissions."
	}
}
func (s *Server) decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.ContentLength > 16*1024 {
		s.failure(w, r, domain.Fail("BODY_TOO_LARGE", 413))
		return false
	}
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		s.failure(w, r, domain.Fail("JSON_REQUIRED", 415))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	e := d.Decode(out)
	if e == nil {
		if e = d.Decode(new(any)); e == io.EOF {
			return true
		}
	}
	var large *http.MaxBytesError
	if errors.As(e, &large) {
		s.failure(w, r, domain.Fail("BODY_TOO_LARGE", 413))
	} else {
		s.failure(w, r, domain.Fail("INVALID_JSON", 400))
	}
	return false
}
func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://www.paypal.com https://www.paypalobjects.com; style-src 'self' 'unsafe-inline'; connect-src 'self' https://*.paypal.com https://*.paypalobjects.com; img-src 'self' data: https://*.paypal.com https://*.paypalobjects.com; frame-src https://*.paypal.com; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && r.URL.Path != "/api/webhooks/paypal" {
			if r.Header.Get("Origin") != s.App.Config.Origin || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				s.failure(w, r, domain.Fail("ORIGIN_REJECTED", 403))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie(SessionCookie)
		if e != nil {
			s.failure(w, r, domain.Fail("UNAUTHENTICATED", 401))
			return
		}
		sess, e := s.Auth.Resolve(r.Context(), c.Value)
		if e != nil {
			s.failure(w, r, e)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			token := r.Header.Get("X-CSRF-Token")
			hash := domain.Hash([]byte(token))
			if len(token) != 43 || subtle.ConstantTimeCompare([]byte(hash), []byte(sess.CSRFHash)) != 1 {
				s.failure(w, r, domain.Fail("CSRF_REJECTED", 403))
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	})
}

// Route role checks are an early boundary; domain services independently
// recheck active membership and object ownership/assignment against PostgreSQL.
func (s *Server) requireRole(roles ...domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, role := range roles {
				if session(r).Actor.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			s.failure(w, r, domain.Fail("FORBIDDEN", 403))
		})
	}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	ip, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		ip = r.RemoteAddr
	}
	old := ""
	if c, e := r.Cookie(SessionCookie); e == nil {
		old = c.Value
	}
	out, e := s.Auth.Login(r.Context(), in.Email, in.Password, ip, old, correlation(r))
	if e != nil {
		s.failure(w, r, e)
		return
	}
	s.cookie(w, SessionCookie, out.Token, true, int(s.App.Config.SessionTTL.Seconds()))
	s.cookie(w, CSRFCookie, out.CSRFToken, false, int(s.App.Config.SessionTTL.Seconds()))
	write(w, 200, out)
}
func (s *Server) cookie(w http.ResponseWriter, name, value string, httpOnly bool, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: httpOnly, Secure: s.App.Config.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !s.decode(w, r, &in) {
		return
	}
	c, _ := r.Cookie(SessionCookie)
	if e := s.Auth.Logout(r.Context(), c.Value, correlation(r)); e != nil {
		s.failure(w, r, e)
		return
	}
	s.cookie(w, SessionCookie, "", true, -1)
	s.cookie(w, CSRFCookie, "", false, -1)
	write(w, 200, map[string]string{"status": "SIGNED_OUT"})
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	n, e := migrations.Current(ctx, s.App.DB.Pool)
	if e != nil || n != migrations.Version {
		write(w, 503, map[string]any{"status": "unready", "reason_code": "DATABASE_OR_SCHEMA_UNAVAILABLE"})
		return
	}
	write(w, 200, map[string]any{"status": "ready", "migration_version": n})
}
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.failure(w, r, domain.Fail("NOT_FOUND", 404))
		return
	}
	root, e := filepath.Abs(s.App.Config.WebDir)
	if e != nil {
		s.failure(w, r, domain.Fail("WEB_NOT_BUILT", 503))
		return
	}
	p := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
	rel, e := filepath.Rel(root, p)
	if e != nil || strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, ".") {
		p = filepath.Join(root, "index.html")
	}
	if stat, e := os.Stat(p); e != nil || stat.IsDir() {
		p = filepath.Join(root, "index.html")
	}
	if _, e = os.Stat(p); e != nil {
		s.failure(w, r, domain.Fail("WEB_NOT_BUILT", 503))
		return
	}
	http.ServeFile(w, r, p)
}
func session(r *http.Request) auth.Session { return r.Context().Value(sessionKey).(auth.Session) }
func correlation(r *http.Request) string {
	s, _ := r.Context().Value(correlationKey).(string)
	return s
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(n int) {
	if w.status == 0 {
		w.status = n
		w.ResponseWriter.WriteHeader(n)
	}
}
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(context.WithValue(r.Context(), correlationKey, id))
		out := &responseWriter{ResponseWriter: w}
		start := time.Now()
		defer func() {
			if recover() != nil {
				s.failure(out, r, domain.Fail("INTERNAL_ERROR", 500))
			}
			route := "unmatched"
			if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
				route = rc.RoutePattern()
			}
			s.Log.Info("http_request", "request_id", id, "method", r.Method, "route", route, "status", out.status, "duration_ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(out, r)
	})
}
