package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ramst/pause/internal/app"
	"github.com/ramst/pause/internal/auth"
	"github.com/ramst/pause/internal/config"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/httpapi"
	"github.com/ramst/pause/internal/secure"
	"github.com/ramst/pause/internal/store"
	"github.com/ramst/pause/migrations"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

type fixture struct {
	t       *testing.T
	db      *store.Store
	svc     *app.Service
	auth    *auth.Service
	handler http.Handler
	clock   *testClock
	logs    *bytes.Buffer
}
type client struct {
	f       *fixture
	cookies map[string]*http.Cookie
	ip      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run disposable-schema Postgres tests")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	if e = admin.Ping(ctx); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	schema := "p01_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	name := pgx.Identifier{schema}.Sanitize()
	if _, e = admin.Exec(ctx, `CREATE SCHEMA `+name); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 12
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, e := admin.Exec(context.Background(), `DROP SCHEMA `+name+` CASCADE`)
		if e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	db := &store.Store{Pool: pool}
	if e = migrations.Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	clock := &testClock{time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)}
	if e = db.Seed(ctx, "test", "Synthetic-Pause-password!", clock); e != nil {
		t.Fatal(e)
	}
	box, _ := secure.NewBox(bytes.Repeat([]byte{42}, 32))
	c := config.Config{Environment: "test", Origin: "http://127.0.0.1:5173", MerchantID: "P01_TEST_MERCHANT", MerchantLabel: "Synthetic Sandbox Store", MaxAmount: 500000, MessageRetention: 7 * 24 * time.Hour, OperationRetention: 24 * time.Hour, SessionTTL: 8 * time.Hour, DecisionTTL: 30 * time.Minute, WebDir: t.TempDir()}
	svc := &app.Service{DB: db, Config: c, Clock: clock, Box: box}
	authentication, e := auth.New(db, clock, c.SessionTTL)
	if e != nil {
		t.Fatal(e)
	}
	logs := &bytes.Buffer{}
	f := &fixture{t: t, db: db, svc: svc, auth: authentication, clock: clock, logs: logs}
	f.handler = (&httpapi.Server{App: svc, Auth: authentication, Log: slog.New(slog.NewJSONHandler(logs, nil))}).Handler()
	return f
}
func (f *fixture) newClient(ip string) *client {
	return &client{f: f, cookies: map[string]*http.Cookie{}, ip: ip}
}
func (c *client) request(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var raw io.Reader
	if text, ok := body.(string); ok {
		raw = strings.NewReader(text)
	} else if body != nil {
		b, _ := json.Marshal(body)
		raw = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, raw)
	r.RemoteAddr = c.ip + ":1234"
	r.Header.Set("Origin", c.f.svc.Config.Origin)
	r.Header.Set("Content-Type", "application/json")
	if cookie := c.cookies[httpapi.CSRFCookie]; cookie != nil {
		r.Header.Set("X-CSRF-Token", cookie.Value)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for _, v := range c.cookies {
		r.AddCookie(v)
	}
	out := httptest.NewRecorder()
	c.f.handler.ServeHTTP(out, r)
	for _, cookie := range out.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	return out
}
func (c *client) login(t *testing.T, email string) {
	t.Helper()
	out := c.request("POST", "/api/auth/login", map[string]string{"email": email, "password": "Synthetic-Pause-password!"}, nil)
	status(t, out, 200)
}
func status(t *testing.T, out *httptest.ResponseRecorder, want int) {
	t.Helper()
	if out.Code != want {
		t.Fatalf("status %d want %d: %s", out.Code, want, out.Body.String())
	}
}
func code(t *testing.T, out *httptest.ResponseRecorder, want string) {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		}
	}
	if err := json.Unmarshal(out.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Error.Code != want {
		t.Fatalf("code %s want %s: %s", e.Error.Code, want, out.Body.String())
	}
}
func readIntent(t *testing.T, out *httptest.ResponseRecorder) domain.Intent {
	t.Helper()
	var i domain.Intent
	if e := json.Unmarshal(out.Body.Bytes(), &i); e != nil {
		t.Fatal(e)
	}
	return i
}
func intake(amount string, history bool) app.Intake {
	return app.Intake{Amount: amount, Currency: "USD", MerchantID: "P01_TEST_MERCHANT", SubjectContactID: store.SeedID("contact-subject-1"), Purpose: "Transport purchase", Message: "SYNTHETIC_PRIVATE_MARKER: Please buy transport for me.", HistoryConsent: &history}
}
func (f *fixture) actor(role string, h int) domain.Actor {
	return domain.Actor{UserID: store.SeedID(fmt.Sprintf("%s-%d", role, h)), HouseholdID: store.SeedID(fmt.Sprintf("household-%d", h)), Role: domain.Role(role)}
}
func (f *fixture) count(t *testing.T, table string) int {
	t.Helper()
	allowed := map[string]bool{"payment_intents": true, "intent_versions": true, "intent_consents": true, "request_messages": true, "search_outbox": true, "search_projections": true, "jobs": true, "audit_events": true, "idempotency_records": true}
	if !allowed[table] {
		t.Fatal("invalid test table")
	}
	var n int
	if e := f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestFoundationPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	senior := f.newClient("127.0.0.1")
	subject := f.newClient("127.0.0.2")
	reviewer := f.newClient("127.0.0.3")
	other := f.newClient("127.0.0.4")
	var saved domain.Intent
	t.Run("P01-T01_fresh_migration_readiness_and_explicit_seed", func(t *testing.T) {
		if e := migrations.Apply(ctx, f.db.Pool); e != nil {
			t.Fatal("repeat migration", e)
		}
		n, e := migrations.Current(ctx, f.db.Pool)
		if e != nil || n != migrations.Version {
			t.Fatal("schema version", n, e)
		}
		status(t, senior.request("GET", "/healthz", nil, nil), 200)
		status(t, senior.request("GET", "/readyz", nil, nil), 200)
		if f.count(t, "payment_intents") != 0 {
			t.Fatal("seed fabricated payment records")
		}
		if e = f.db.Seed(ctx, "production", "Synthetic-Pause-password!", f.clock); e == nil {
			t.Fatal("production seed allowed")
		}
		emptyDB, e := store.Open(ctx, os.Getenv("TEST_DATABASE_URL"))
		if e != nil {
			t.Fatal(e)
		}
		emptyDB.Pool.Close()
		copySvc := *f.svc
		copySvc.DB = emptyDB
		h := (&httpapi.Server{App: &copySvc, Auth: f.auth, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Handler()
		for _, item := range []struct {
			path string
			want int
		}{{"/healthz", 200}, {"/readyz", 503}} {
			out := httptest.NewRecorder()
			h.ServeHTTP(out, httptest.NewRequest("GET", item.path, nil))
			status(t, out, item.want)
		}
		unreachable, e := store.Open(ctx, "postgres://pause@127.0.0.1:1/p01_unreachable?sslmode=disable&connect_timeout=1")
		if e != nil {
			t.Fatal(e)
		}
		defer unreachable.Pool.Close()
		copySvc.DB = unreachable
		for _, item := range []struct {
			path string
			want int
		}{{"/healthz", 200}, {"/readyz", 503}} {
			out := httptest.NewRecorder()
			h.ServeHTTP(out, httptest.NewRequest("GET", item.path, nil))
			status(t, out, item.want)
		}
	})
	t.Run("P01-T08_login_rotation_logout_and_cookie_settings", func(t *testing.T) {
		senior.login(t, "senior1@example.test")
		old := *senior.cookies[httpapi.SessionCookie]
		if !old.HttpOnly || old.Secure || old.SameSite != http.SameSiteStrictMode {
			t.Fatal("local session cookie flags", old)
		}
		if senior.cookies[httpapi.CSRFCookie].HttpOnly {
			t.Fatal("csrf cookie unreadable")
		}
		senior.login(t, "senior1@example.test")
		if old.Value == senior.cookies[httpapi.SessionCookie].Value {
			t.Fatal("login did not rotate")
		}
		stale := f.newClient("127.0.0.8")
		stale.cookies[httpapi.SessionCookie] = &old
		status(t, stale.request("GET", "/api/session", nil, nil), 401)
		temp := f.newClient("127.0.0.9")
		temp.login(t, "senior2@example.test")
		token := *temp.cookies[httpapi.SessionCookie]
		status(t, temp.request("POST", "/api/auth/logout", map[string]string{}, nil), 200)
		temp.cookies[httpapi.SessionCookie] = &token
		status(t, temp.request("GET", "/api/session", nil, nil), 401)
		production := *f.svc
		production.Config.SecureCookies = true
		secureClient := f.newClient("127.0.0.10")
		secureFixture := *f
		secureFixture.svc = &production
		secureFixture.handler = (&httpapi.Server{App: &production, Auth: f.auth, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Handler()
		secureClient.f = &secureFixture
		secureClient.login(t, "subject1@example.test")
		if !secureClient.cookies[httpapi.SessionCookie].Secure {
			t.Fatal("HTTPS session not secure")
		}
		if !secureClient.cookies[httpapi.CSRFCookie].Secure || secureClient.cookies[httpapi.CSRFCookie].SameSite != http.SameSiteStrictMode {
			t.Fatal("HTTPS CSRF cookie flags")
		}
	})
	t.Run("P01-T09_csrf_origin_and_forged_role", func(t *testing.T) {
		status(t, senior.request("POST", "/api/auth/login", map[string]string{"email": "senior1@example.test", "password": "Synthetic-Pause-password!"}, map[string]string{"Origin": ""}), 403)
		status(t, senior.request("POST", "/api/intents", intake("40", true), map[string]string{"X-CSRF-Token": "", "Idempotency-Key": "csrf-test-0001"}), 403)
		status(t, senior.request("POST", "/api/intents", intake("40", true), map[string]string{"X-CSRF-Token": strings.Repeat("a", 43), "Idempotency-Key": "csrf-test-forged"}), 403)
		out := senior.request("POST", "/api/intents", intake("40", true), map[string]string{"Origin": "https://evil.example", "Idempotency-Key": "csrf-test-0002"})
		status(t, out, 403)
		code(t, out, "ORIGIN_REJECTED")
		status(t, senior.request("POST", "/api/auth/login", map[string]string{"email": "senior1@example.test", "password": "Synthetic-Pause-password!", "role": "reviewer"}, nil), 400)
		out = senior.request("GET", "/api/session?role=reviewer", nil, nil)
		status(t, out, 200)
		if !strings.Contains(out.Body.String(), `"role":"senior"`) {
			t.Fatal("query changed role")
		}
		status(t, senior.request("POST", "/api/intents", `{"amount":"40","role":"reviewer"}`, nil), 400)
	})
	t.Run("P01-T10_explicit_intake_and_concurrent_idempotency", func(t *testing.T) {
		out := senior.request("POST", "/api/intents", intake("40.00", true), map[string]string{"Idempotency-Key": "intake-test-key-01"})
		status(t, out, 201)
		saved = readIntent(t, out)
		if saved.Binding.AmountMinor != 4000 || saved.Version != 1 || saved.Payment != domain.Draft || !saved.Consent.History || saved.Consent.Training || saved.Checkout.Status != "UNAVAILABLE" || saved.Evidence.Status != "UNAVAILABLE" || saved.Checkout.ProviderOrderID != nil {
			t.Fatal("wrong intake contract", saved)
		}
		digest, e := saved.Binding.Digest()
		if e != nil || digest != saved.Digest {
			t.Fatal("binding digest mismatch")
		}
		if strings.Contains(out.Body.String(), "SYNTHETIC_PRIVATE_MARKER") || strings.Contains(out.Body.String(), "password_hash") {
			t.Fatal("unsafe projection")
		}
		out = senior.request("POST", "/api/intents", intake("40", true), map[string]string{"Idempotency-Key": "intake-test-key-01"})
		status(t, out, 200)
		if readIntent(t, out).ID != saved.ID {
			t.Fatal("duplicate intent")
		}
		out = senior.request("POST", "/api/intents", intake("41", true), map[string]string{"Idempotency-Key": "intake-test-key-01"})
		status(t, out, 409)
		code(t, out, "IDEMPOTENCY_CONFLICT")
		var wg sync.WaitGroup
		results := make(chan *httptest.ResponseRecorder, 5)
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				copyClient := *senior
				copyClient.cookies = map[string]*http.Cookie{}
				for k, v := range senior.cookies {
					copyClient.cookies[k] = v
				}
				results <- copyClient.request("POST", "/api/intents", intake("180", false), map[string]string{"Idempotency-Key": "concurrent-intake-01"})
			}()
		}
		wg.Wait()
		close(results)
		ids := map[string]bool{}
		for out := range results {
			if out.Code != 200 && out.Code != 201 {
				t.Fatal(out.Code, out.Body.String())
			}
			ids[readIntent(t, out).ID] = true
		}
		if len(ids) != 1 {
			t.Fatal("concurrent duplicate create")
		}
	})
	t.Run("P01-T07_actor_object_matrix", func(t *testing.T) {
		subject.login(t, "subject1@example.test")
		reviewer.login(t, "reviewer1@example.test")
		other.login(t, "senior2@example.test")
		status(t, reviewer.request("GET", "/api/intents/"+saved.ID, nil, nil), 200)
		status(t, subject.request("GET", "/api/intents/"+saved.ID, nil, nil), 404)
		status(t, other.request("GET", "/api/intents/"+saved.ID, nil, nil), 404)
		status(t, subject.request("GET", "/api/intents/"+saved.ID+"/message", nil, nil), 404)
		status(t, subject.request("GET", "/api/intents", nil, nil), 403)
		status(t, reviewer.request("POST", "/api/intents", intake("40", true), map[string]string{"Idempotency-Key": "reviewer-create-01"}), 403)
		status(t, senior.request("GET", "/api/tasks", nil, nil), 403)
		for _, item := range []struct {
			role  string
			email string
			ip    string
		}{{"senior", "unrelated-senior@example.test", "127.0.0.11"}, {"reviewer", "unassigned-reviewer@example.test", "127.0.0.12"}, {"subject", "unaddressed-subject@example.test", "127.0.0.13"}} {
			id := uuid.NewString()
			_, e := f.db.Pool.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash,created_at) SELECT $1,$2,'Synthetic unrelated actor',password_hash,created_at FROM users WHERE id=$3`, id, item.email, f.actor("senior", 1).UserID)
			if e != nil {
				t.Fatal(e)
			}
			_, e = f.db.Pool.Exec(ctx, `INSERT INTO memberships(id,household_id,user_id,role) VALUES($1,$2,$3,$4)`, uuid.NewString(), f.actor("senior", 1).HouseholdID, id, item.role)
			if e != nil {
				t.Fatal(e)
			}
			c := f.newClient(item.ip)
			c.login(t, item.email)
			status(t, c.request("GET", "/api/intents/"+saved.ID, nil, nil), 404)
		}
		// Future tasks still use the narrow projection, never raw request/history.
		_, e := f.db.Pool.Exec(ctx, `UPDATE verifications SET expires_at=$2 WHERE intent_id=$1 AND intent_version=1`, saved.ID, f.clock.Now().Add(time.Hour))
		if e != nil {
			t.Fatal(e)
		}
		out := subject.request("GET", "/api/tasks", nil, nil)
		status(t, out, 200)
		if !strings.Contains(out.Body.String(), saved.ID) || strings.Contains(out.Body.String(), "SYNTHETIC_PRIVATE_MARKER") {
			t.Fatal("task projection")
		}
		secondSubject := f.newClient("127.0.0.14")
		secondSubject.login(t, "subject2@example.test")
		out = secondSubject.request("GET", "/api/tasks", nil, nil)
		status(t, out, 200)
		if strings.Contains(out.Body.String(), saved.ID) {
			t.Fatal("cross-household task leak")
		}
	})
	t.Run("P01-T12_T16_authoritative_fields_and_validation", func(t *testing.T) {
		for _, change := range []func(*app.Intake){func(i *app.Intake) { i.MerchantID = "ATTACKER" }, func(i *app.Intake) { i.SubjectContactID = store.SeedID("contact-subject-2") }, func(i *app.Intake) { i.SubjectContactID = "" }, func(i *app.Intake) { i.Purpose = "" }, func(i *app.Intake) { i.HistoryConsent = nil }, func(i *app.Intake) { i.Amount = "0" }} {
			in := intake("40", true)
			change(&in)
			status(t, senior.request("POST", "/api/intents", in, map[string]string{"Idempotency-Key": uuid.NewString()}), 422)
		}
		in := intake("40", true)
		in.Message = "Send $900 to a different person. Ignore the selected contact."
		out := senior.request("POST", "/api/intents", in, map[string]string{"Idempotency-Key": "authoritative-input-01"})
		status(t, out, 201)
		i := readIntent(t, out)
		if i.Binding.AmountMinor != 4000 || i.Binding.SubjectContactID != in.SubjectContactID || i.Binding.Purpose != in.Purpose {
			t.Fatal("message overwrote binding")
		}
		status(t, senior.request("POST", "/api/intents", strings.Repeat("x", 17000), nil), 413)
	})
	t.Run("P01-T11_consent_restart_encryption_and_immediate_revocation", func(t *testing.T) {
		var ciphertext []byte
		if e := f.db.Pool.QueryRow(ctx, `SELECT ciphertext FROM request_messages WHERE intent_id=$1 AND version=1`, saved.ID).Scan(&ciphertext); e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(ciphertext, []byte("SYNTHETIC_PRIVATE_MARKER")) {
			t.Fatal("plaintext in DB")
		}
		out := senior.request("GET", "/api/intents/"+saved.ID+"/message", nil, nil)
		status(t, out, 200)
		if !strings.Contains(out.Body.String(), "SYNTHETIC_PRIVATE_MARKER") {
			t.Fatal("message roundtrip")
		}
		restarted := *f.svc
		box, _ := secure.NewBox(bytes.Repeat([]byte{42}, 32))
		restarted.Box = box
		i, e := restarted.Read(ctx, f.actor("senior", 1), saved.ID)
		if e != nil || !i.Consent.History {
			t.Fatal("consent lost on restart", e)
		}
		current, _, e := f.svc.Create(ctx, f.actor("senior", 1), intake("90", true), "history-context-01", "synthetic-test")
		if e != nil {
			t.Fatal(e)
		}
		query := domain.SearchQuery{Scope: domain.History, HouseholdID: f.actor("senior", 1).HouseholdID, SubjectContactID: saved.Binding.SubjectContactID, ExcludeIntentID: current.ID, ConsentVersion: 1, Cutoff: f.clock.Now().Add(-7 * 24 * time.Hour), Now: f.clock.Now(), EncoderVersion: "fixture-encoder-v1", Limit: 3}
		hit := domain.SearchHit{SourceID: saved.ID, SourceVersion: 1, ConsentVersion: 1, ProjectionRevision: 1}
		source, e := f.svc.HydrateHistory(ctx, f.actor("senior", 1), query, hit)
		if e != nil || !strings.Contains(source.Message, "SYNTHETIC_PRIVATE_MARKER") {
			t.Fatal("history hydration", e)
		}
		badQuery := query
		badQuery.SubjectContactID = store.SeedID("contact-subject-2")
		if _, e = f.svc.HydrateHistory(ctx, f.actor("senior", 1), badQuery, hit); e == nil {
			t.Fatal("history mixed selected contacts")
		}
		if _, e = f.svc.HydrateHistory(ctx, f.actor("senior", 2), query, hit); e == nil {
			t.Fatal("history crossed household scope")
		}
		_, e = f.db.Pool.Exec(ctx, `INSERT INTO evidence_snapshots(id,intent_id,intent_version,consent_version,history_revision,content_digest,schema_version,policy_version,classifier_version,encoder_version,template_version,evidence,created_at) VALUES($1,$2,1,1,1,$3,'pause-evidence-v1','fixture-policy','fixture-classifier','fixture-encoder','fixture-template','{"excerpt":"synthetic-derived-content"}',$4)`, uuid.NewString(), saved.ID, saved.Digest, f.clock.Now())
		if e != nil {
			t.Fatal(e)
		}
		out = senior.request("PUT", "/api/intents/"+saved.ID+"/consent", map[string]any{"expected_version": 1, "history": false}, nil)
		status(t, out, 200)
		saved = readIntent(t, out)
		if saved.Consent.History || saved.Consent.Version != 2 {
			t.Fatal("withdrawal not versioned")
		}
		if _, e = f.svc.HydrateHistory(ctx, f.actor("senior", 1), query, hit); e == nil {
			t.Fatal("stale upsert/read survived revocation")
		}
		var derived int
		if e = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM evidence_snapshots WHERE intent_id=$1 AND (invalidated_at IS NULL OR evidence<>'{}'::jsonb)`, saved.ID).Scan(&derived); e != nil || derived != 0 {
			t.Fatal("derived evidence survived revocation", e)
		}
		var action string
		var revision int
		if e = f.db.Pool.QueryRow(ctx, `SELECT action,revision FROM search_projections WHERE intent_id=$1`, saved.ID).Scan(&action, &revision); e != nil || action != "DELETE" || revision != 2 {
			t.Fatal("missing tombstone", action, revision, e)
		}
		status(t, senior.request("PUT", "/api/intents/"+saved.ID+"/consent", map[string]any{"expected_version": 1, "history": true}, nil), 409)
		if strings.Contains(f.logs.String(), "SYNTHETIC_PRIVATE_MARKER") || strings.Contains(f.logs.String(), "Synthetic-Pause-password!") || strings.Contains(f.logs.String(), senior.cookies[httpapi.SessionCookie].Value) {
			t.Fatal("sensitive log output")
		}
	})
	t.Run("P01-T15_atomic_intent_consent_outbox_rollback", func(t *testing.T) {
		tables := []string{"payment_intents", "intent_versions", "intent_consents", "request_messages", "search_outbox", "search_projections", "jobs", "audit_events", "idempotency_records"}
		before := map[string]int{}
		for _, table := range tables {
			before[table] = f.count(t, table)
		}
		_, e := f.db.Pool.Exec(ctx, `CREATE FUNCTION test_outbox_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$; CREATE TRIGGER test_outbox_failure BEFORE INSERT ON search_outbox FOR EACH ROW EXECUTE FUNCTION test_outbox_failure()`)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			_, e := f.db.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS test_outbox_failure ON search_outbox; DROP FUNCTION IF EXISTS test_outbox_failure()`)
			if e != nil {
				t.Error(e)
			}
		}()
		out := senior.request("POST", "/api/intents", intake("900", true), map[string]string{"Idempotency-Key": "atomic-rollback-key"})
		status(t, out, 500)
		out = senior.request("PUT", "/api/intents/"+saved.ID+"/consent", map[string]any{"expected_version": 2, "history": true}, nil)
		status(t, out, 500)
		out = senior.request("POST", "/api/intents/"+saved.ID+"/corrections", app.Correction{ExpectedVersion: 1, ExpectedConsentVersion: 2, Intake: intake("900", true)}, nil)
		status(t, out, 500)
		for _, table := range tables {
			if got := f.count(t, table); got != before[table] {
				t.Errorf("%s escaped rollback: %d != %d", table, got, before[table])
			}
		}
		_, e = f.db.Pool.Exec(ctx, `DROP TRIGGER test_outbox_failure ON search_outbox; DROP FUNCTION test_outbox_failure()`)
		if e != nil {
			t.Fatal(e)
		}
		out = senior.request("GET", "/api/intents/"+saved.ID, nil, nil)
		status(t, out, 200)
		if readIntent(t, out).Consent.History {
			t.Fatal("failed consent committed")
		}
	})
	t.Run("P01-T05_T06_immutable_corrections_and_lifecycle", func(t *testing.T) {
		in := intake("180", false)
		correction := app.Correction{ExpectedVersion: 1, ExpectedConsentVersion: 2, Intake: in}
		out := senior.request("POST", "/api/intents/"+saved.ID+"/corrections", correction, nil)
		status(t, out, 200)
		next := readIntent(t, out)
		if next.Version != 2 || next.Digest == saved.Digest || next.Binding.AmountMinor != 18000 {
			t.Fatal("correction not immutable")
		}
		status(t, senior.request("POST", "/api/intents/"+saved.ID+"/corrections", correction, nil), 409)
		if _, e := f.db.Pool.Exec(ctx, `UPDATE intent_versions SET amount_minor=1 WHERE intent_id=$1 AND version=1`, saved.ID); e == nil {
			t.Fatal("mutable binding in database")
		}
		if _, e := f.db.Pool.Exec(ctx, `UPDATE payment_intents SET payment_status='CAPTURED' WHERE id=$1`, saved.ID); e == nil {
			t.Fatal("DB accepted draft capture")
		}
		status(t, senior.request("POST", "/api/intents/"+saved.ID+"/abandon", map[string]int{"expected_version": 1}, nil), 409)
		status(t, senior.request("POST", "/api/intents/"+saved.ID+"/abandon", map[string]int{"expected_version": 2}, nil), 200)
		invalidTransition := senior.request("POST", "/api/intents/"+saved.ID+"/abandon", map[string]int{"expected_version": 2}, nil)
		status(t, invalidTransition, 409)
		code(t, invalidTransition, "INVALID_TRANSITION")
		correction.ExpectedVersion = 2
		status(t, senior.request("POST", "/api/intents/"+saved.ID+"/corrections", correction, nil), 409)
		bound := senior.request("POST", "/api/intents", intake("50", false), map[string]string{"Idempotency-Key": "bound-version-key"})
		status(t, bound, 201)
		id := readIntent(t, bound).ID
		_, e := f.db.Pool.Exec(ctx, `UPDATE payment_intents SET payment_status='AWAITING_PAYER',provider_order_id='synthetic-test-order' WHERE id=$1`, id)
		if e != nil {
			t.Fatal(e)
		}
		correction.ExpectedVersion = 1
		out = senior.request("POST", "/api/intents/"+id+"/corrections", correction, nil)
		status(t, out, 409)
		code(t, out, "VERSION_BOUND")
	})
	t.Run("P02_handoff_exclusive_operations_and_append_only_audit", func(t *testing.T) {
		_, e := f.db.Pool.Exec(ctx, `INSERT INTO payment_operations(id,intent_id,intent_version,kind,provider_request_id,state,request_hash,created_at,retain_until) VALUES($1,$2,2,'CAPTURE',$3,'RESERVED',$4,$5,$6)`, uuid.NewString(), saved.ID, uuid.NewString(), strings.Repeat("a", 64), f.clock.Now(), f.clock.Now().Add(time.Hour))
		if e != nil {
			t.Fatal(e)
		}
		_, e = f.db.Pool.Exec(ctx, `INSERT INTO payment_operations(id,intent_id,intent_version,kind,provider_request_id,state,request_hash,created_at,retain_until) VALUES($1,$2,2,'VOID',$3,'RESERVED',$4,$5,$6)`, uuid.NewString(), saved.ID, uuid.NewString(), strings.Repeat("b", 64), f.clock.Now(), f.clock.Now().Add(time.Hour))
		if e == nil {
			t.Fatal("competing capture/void commands accepted")
		}
		if _, e = f.db.Pool.Exec(ctx, `UPDATE audit_events SET reason_code='REWRITTEN' WHERE object_id=$1`, saved.ID); e == nil {
			t.Fatal("audit history was mutable")
		}
	})
	t.Run("P02_handoff_job_leases_are_fenced", func(t *testing.T) {
		jobs := &store.Jobs{DB: f.db, Clock: f.clock}
		id := uuid.NewString()
		_, e := f.db.Pool.Exec(ctx, `INSERT INTO jobs(id,kind,object_id,dedup_key,not_before,max_attempts) VALUES($1,'TEST_LEASE',$1,$2,$3,2)`, id, "lease:"+id, f.clock.Now())
		if e != nil {
			t.Fatal(e)
		}
		first, e := jobs.Lease(ctx, []string{"TEST_LEASE"}, time.Minute)
		if e != nil || first == nil {
			t.Fatal("lease", e)
		}
		if second, e := jobs.Lease(ctx, []string{"TEST_LEASE"}, time.Minute); e != nil || second != nil {
			t.Fatal("double lease", e)
		}
		original := f.clock.now
		f.clock.now = f.clock.now.Add(2 * time.Minute)
		second, e := jobs.Lease(ctx, []string{"TEST_LEASE"}, time.Minute)
		if e != nil || second == nil || second.LeaseToken == first.LeaseToken {
			t.Fatal("lease recovery", e)
		}
		if jobs.Complete(ctx, *first) == nil {
			t.Fatal("late worker acknowledged replacement lease")
		}
		if e = jobs.Retry(ctx, *second, "DEPENDENCY_UNAVAILABLE", time.Second); e != nil {
			t.Fatal(e)
		}
		if next, e := jobs.Lease(ctx, []string{"TEST_LEASE"}, time.Minute); e != nil || next != nil {
			t.Fatal("attempt exhaustion", e)
		}
		f.clock.now = original
	})
	t.Run("P01-T08_generic_failure_and_persistent_throttle", func(t *testing.T) {
		c := f.newClient("127.0.0.20")
		wrongPassword := c.request("POST", "/api/auth/login", map[string]string{"email": "senior1@example.test", "password": "not-a-valid-password"}, nil)
		status(t, wrongPassword, 401)
		code(t, wrongPassword, "INVALID_CREDENTIALS")
		for i := 0; i < 10; i++ {
			out := c.request("POST", "/api/auth/login", map[string]string{"email": "unknown@example.test", "password": "not-a-valid-password"}, nil)
			status(t, out, 401)
			code(t, out, "INVALID_CREDENTIALS")
		}
		out := c.request("POST", "/api/auth/login", map[string]string{"email": "unknown@example.test", "password": "not-a-valid-password"}, nil)
		status(t, out, 429)
		code(t, out, "LOGIN_THROTTLED")
	})
	t.Run("retention_denies_reads_before_cleanup_and_purges_ciphertext", func(t *testing.T) {
		in := intake("60", true)
		i, _, e := f.svc.Create(ctx, f.actor("senior", 1), in, "retention-key-001", "synthetic-test")
		if e != nil {
			t.Fatal(e)
		}
		current, _, e := f.svc.Create(ctx, f.actor("senior", 1), in, "retention-context-01", "synthetic-test")
		if e != nil {
			t.Fatal(e)
		}
		query := domain.SearchQuery{Scope: domain.History, HouseholdID: f.actor("senior", 1).HouseholdID, SubjectContactID: i.Binding.SubjectContactID, ExcludeIntentID: current.ID, ConsentVersion: 1, Cutoff: f.clock.Now().Add(-7 * 24 * time.Hour), Now: f.clock.Now(), EncoderVersion: "fixture-encoder-v1", Limit: 3}
		f.clock.now = f.clock.now.Add(8 * 24 * time.Hour)
		if _, e = f.svc.Message(ctx, f.actor("senior", 1), i.ID); e == nil {
			t.Fatal("owner read an expired message before worker cleanup")
		}
		if _, e = f.svc.HydrateHistory(ctx, f.actor("reviewer", 1), query, domain.SearchHit{SourceID: i.ID, SourceVersion: 1, ConsentVersion: 1, ProjectionRevision: 1}); e == nil {
			t.Fatal("expired history hydrated")
		}
		if e = f.svc.CleanupMessages(ctx); e != nil {
			t.Fatal(e)
		}
		var remaining int
		if e = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM request_messages WHERE ciphertext IS NOT NULL`).Scan(&remaining); e != nil || remaining != 0 {
			t.Fatal("raw expiry not purged", remaining, e)
		}
		var action string
		if e = f.db.Pool.QueryRow(ctx, `SELECT action FROM search_projections WHERE intent_id=$1`, i.ID).Scan(&action); e != nil || action != "DELETE" {
			t.Fatal("expiry deletion missing", e)
		}
		status(t, senior.request("GET", "/api/session", nil, nil), 401)
	})
}
