package config

import (
	"encoding/base64"
	"errors"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	MailMode, SMTPHost, SMTPUsername, SMTPPassword, MailFrom, MailRedirectTo string
	SMTPPort                                                                 int
	Environment, Listen, Origin, DatabaseURL, WebDir                         string
	MessageKey                                                               []byte
	MerchantID, MerchantLabel                                                string
	MaxAmount                                                                int64
	PatternRequested, PatternMinimum, PatternMultiplier                      int64
	MessageRetention, OperationRetention, SessionTTL, DecisionTTL            time.Duration
	MLURL, MLToken, ClassifierManifest, EncoderManifest                      string
	ElasticURL, ElasticKey, PlaybookAlias, HistoryAlias                      string
	ElasticCA                                                                string
	GeminiAPIKey, GeminiModel, GeminiBaseURL, GeminiThinkingLevel            string
	SecureCookies                                                            bool
	DemoEnabled                                                              bool
	TrustedProxies                                                           []netip.Prefix
	PrivateHTTPHosts                                                         []string
	PayPalURL, PayPalClientID, PayPalSecret, PayPalWebhookID                 string
}

// Load never includes values or credentials in a configuration error.
func Load() (Config, error) { return From(os.Getenv) }
func From(get func(string) string) (Config, error) {
	c := Config{Environment: value(get, "APP_ENV", "development"), Listen: listenAddr(get), Origin: value(get, "APP_BASE_URL", "http://127.0.0.1:5173"), DatabaseURL: get("DATABASE_URL"), WebDir: value(get, "WEB_DIST_DIR", "web/dist"), MerchantID: get("PAYPAL_MERCHANT_ID"), MerchantLabel: value(get, "PAYPAL_MERCHANT_LABEL", "Pause Sandbox Store"), MaxAmount: 500000, MessageRetention: 7 * 24 * time.Hour, OperationRetention: 24 * time.Hour, SessionTTL: 8 * time.Hour, DecisionTTL: 30 * time.Minute, MLURL: get("ML_SERVICE_URL"), MLToken: get("ML_SERVICE_TOKEN"), ClassifierManifest: value(get, "CLASSIFIER_MANIFEST_PATH", "models/classifier.manifest.json"), EncoderManifest: value(get, "ENCODER_MANIFEST_PATH", "models/encoder.manifest.json"), ElasticURL: get("ELASTICSEARCH_URL"), ElasticKey: get("ELASTICSEARCH_API_KEY"), PlaybookAlias: value(get, "ELASTICSEARCH_PLAYBOOK_ALIAS", "pause-playbooks"), HistoryAlias: value(get, "ELASTICSEARCH_HISTORY_ALIAS", "pause-request-history")}
	gemini := llmSettings(get)
	c.GeminiAPIKey, c.GeminiModel, c.GeminiBaseURL, c.GeminiThinkingLevel = gemini.APIKey, gemini.Model, gemini.BaseURL, gemini.ThinkingLevel
	if c.Environment != "development" && c.Environment != "test" && c.Environment != "demo" && c.Environment != "production" {
		return c, errors.New("APP_ENV is invalid")
	}
	c.ElasticCA = get("ELASTICSEARCH_CA_CERT")
	for _, raw := range strings.Split(get("TRUSTED_PROXY_CIDRS"), ",") {
		if raw = strings.TrimSpace(raw); raw != "" {
			p, e := netip.ParsePrefix(raw)
			if e != nil || p.Bits() == 0 {
				return c, errors.New("TRUSTED_PROXY_CIDRS requires explicit bounded CIDRs")
			}
			c.TrustedProxies = append(c.TrustedProxies, p.Masked())
		}
	}
	var err error
	if err = mailSettings(&c, get); err != nil {
		return c, err
	}
	c.PrivateHTTPHosts, err = privateHTTPHosts(get("DEPENDENCY_PRIVATE_HTTP_HOSTS"))
	if err != nil {
		return c, err
	}
	c.DemoEnabled = get("DEMO_ENABLED") == "1"
	if c.DemoEnabled && c.Environment == "production" {
		return c, errors.New("demo launch is disabled in production")
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	c.PayPalURL = value(get, "PAYPAL_BASE_URL", "https://api-m.sandbox.paypal.com")
	c.PayPalClientID, c.PayPalSecret, c.PayPalWebhookID = get("PAYPAL_CLIENT_ID"), get("PAYPAL_CLIENT_SECRET"), get("PAYPAL_WEBHOOK_ID")
	if c.PayPalURL != "https://api-m.sandbox.paypal.com" || (c.PayPalClientID == "") != (c.PayPalSecret == "") || c.PayPalWebhookID == "WEBHOOK_ID" {
		return c, errors.New("PayPal requires sandbox URL, paired credentials and a real app webhook ID")
	}
	d, err := url.Parse(c.DatabaseURL)
	if err != nil || (d.Scheme != "postgres" && d.Scheme != "postgresql") || d.Host == "" || d.Path == "" || d.Path == "/" {
		return c, errors.New("DATABASE_URL is invalid")
	}
	o, err := url.Parse(c.Origin)
	if err != nil || o.Host == "" || o.User != nil || o.RawQuery != "" || o.Fragment != "" || (o.Path != "" && o.Path != "/") || (o.Scheme != "http" && o.Scheme != "https") {
		return c, errors.New("APP_BASE_URL must be an origin")
	}
	c.Origin = strings.TrimSuffix(c.Origin, "/")
	c.SecureCookies = o.Scheme == "https"
	if !c.SecureCookies && (!loopback(o.Hostname()) || c.Environment == "production") {
		return c, errors.New("APP_BASE_URL requires HTTPS outside local development")
	}
	c.MessageKey, err = base64.StdEncoding.DecodeString(get("MESSAGE_ENCRYPTION_KEY"))
	if err != nil || len(c.MessageKey) != 32 {
		return c, errors.New("MESSAGE_ENCRYPTION_KEY must be a base64 encoded 32-byte key")
	}
	if c.MerchantID == "" && c.Environment == "demo" {
		c.MerchantID = "PauseDemoMerchant"
	}
	if c.MerchantID == "" || len(c.MerchantID) > 128 {
		return c, errors.New("PAYPAL_MERCHANT_ID is required and bounded")
	}
	if get("MAX_AMOUNT_MINOR") != "" {
		c.MaxAmount, err = strconv.ParseInt(get("MAX_AMOUNT_MINOR"), 10, 64)
		if err != nil || c.MaxAmount < 1 || c.MaxAmount > 500000 {
			return c, errors.New("MAX_AMOUNT_MINOR must be between 1 and 500000")
		}
	}
	for _, item := range []struct {
		name     string
		target   *int64
		fallback int64
	}{{"PATTERN_REQUESTED_THRESHOLD_MINOR", &c.PatternRequested, 100000}, {"PATTERN_ESCALATION_MINIMUM_MINOR", &c.PatternMinimum, 10000}, {"PATTERN_ESCALATION_MULTIPLIER", &c.PatternMultiplier, 3}} {
		*item.target = item.fallback
		if raw := get(item.name); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || n < 1 || n > 1000000000 {
				return c, errors.New(item.name + " is out of range")
			}
			*item.target = n
		}
	}
	for _, item := range []struct {
		name   string
		target *time.Duration
		max    int
	}{{"MESSAGE_RETENTION_HOURS", &c.MessageRetention, 168}, {"OPERATION_RETENTION_HOURS", &c.OperationRetention, 168}} {
		if s := get(item.name); s != "" {
			n, e := strconv.Atoi(s)
			if e != nil || n < 1 || n > item.max {
				return c, errors.New(item.name + " is out of range")
			}
			*item.target = time.Duration(n) * time.Hour
		}
	}
	for _, item := range []struct{ name, raw string }{{"ML_SERVICE_URL", c.MLURL}, {"ELASTICSEARCH_URL", c.ElasticURL}, {"GEMINI_BASE_URL", c.GeminiBaseURL}} {
		if err := dependencyURL(item.name, item.raw, c.Environment, c.PrivateHTTPHosts); err != nil {
			return c, err
		}
	}
	if err := validateThinkingLevel(c.GeminiThinkingLevel); err != nil {
		return c, err
	}
	if c.MLURL != "" && len(c.MLToken) < 32 {
		return c, errors.New("ML_SERVICE_TOKEN must contain at least 32 characters")
	}
	if c.PlaybookAlias != "pause-playbooks" || c.HistoryAlias != "pause-request-history" {
		return c, errors.New("search aliases must match the approved logical indexes")
	}
	return c, nil
}

func mailSettings(c *Config, get func(string) string) error {
	c.MailMode = value(get, "MAIL_MODE", "off")
	c.SMTPHost, c.SMTPUsername, c.SMTPPassword = get("SMTP_HOST"), get("SMTP_USERNAME"), get("SMTP_PASSWORD")
	c.MailFrom, c.MailRedirectTo = get("MAIL_FROM"), get("MAIL_REDIRECT_TO")
	port, err := strconv.Atoi(value(get, "SMTP_PORT", "587"))
	if err != nil || port < 1 || port > 65535 {
		return errors.New("SMTP_PORT is invalid")
	}
	c.SMTPPort = port
	if c.MailMode != "off" && c.MailMode != "log" && c.MailMode != "smtp" {
		return errors.New("MAIL_MODE is invalid")
	}
	if c.MailMode == "smtp" && (strings.TrimSpace(c.SMTPHost) == "" || c.MailFrom == "") {
		return errors.New("SMTP_HOST and MAIL_FROM are required for smtp")
	}
	for _, field := range []struct{ name, value string }{{"MAIL_FROM", c.MailFrom}, {"MAIL_REDIRECT_TO", c.MailRedirectTo}} {
		if field.value != "" {
			if _, err := mail.ParseAddress(field.value); err != nil || strings.ContainsAny(field.value, "\r\n") {
				return errors.New(field.name + " is invalid")
			}
		}
	}
	if c.Environment == "production" && c.MailRedirectTo != "" {
		return errors.New("MAIL_REDIRECT_TO is disabled in production")
	}
	return nil
}
func value(get func(string) string, k, d string) string {
	if s := get(k); s != "" {
		return s
	}
	return d
}
func loopback(h string) bool { return h == "127.0.0.1" || h == "localhost" || h == "::1" }

func listenAddr(get func(string) string) string {
	if addr := get("HTTP_ADDR"); addr != "" {
		return addr
	}
	port := value(get, "PORT", "10000")
	if strings.Contains(port, ":") {
		return port
	}
	return "0.0.0.0:" + port
}

// LLMConfig contains only the settings needed for advisory signal extraction.
type LLMConfig struct {
	APIKey, Model, BaseURL, ThinkingLevel string
}

func LoadLLM() (LLMConfig, error) { return LLMFrom(os.Getenv) }

func llmSettings(get func(string) string) LLMConfig {
	return LLMConfig{
		APIKey:        get("GEMINI_API_KEY"),
		Model:         value(get, "GEMINI_MODEL", "gemini-3.5-flash-lite"),
		BaseURL:       value(get, "GEMINI_BASE_URL", "https://generativelanguage.googleapis.com"),
		ThinkingLevel: value(get, "GEMINI_THINKING_LEVEL", "minimal"),
	}
}

// LLMFrom validates Gemini settings without requiring application dependencies.
func LLMFrom(get func(string) string) (LLMConfig, error) {
	c := llmSettings(get)
	environment := value(get, "APP_ENV", "development")
	if environment != "development" && environment != "test" && environment != "demo" && environment != "production" {
		return c, errors.New("APP_ENV is invalid")
	}
	hosts, err := privateHTTPHosts(get("DEPENDENCY_PRIVATE_HTTP_HOSTS"))
	if err != nil {
		return c, err
	}
	if err := dependencyURL("GEMINI_BASE_URL", c.BaseURL, environment, hosts); err != nil {
		return c, err
	}
	return c, validateThinkingLevel(c.ThinkingLevel)
}

func validateThinkingLevel(level string) error {
	switch level {
	case "minimal", "low", "medium", "high":
		return nil
	default:
		return errors.New("GEMINI_THINKING_LEVEL is invalid")
	}
}

func privateHTTPHosts(raw string) ([]string, error) {
	var hosts []string
	for _, h := range strings.Split(raw, ",") {
		if h = strings.TrimSpace(h); h != "" {
			if strings.ContainsAny(h, "/:@* ") || strings.Contains(h, ".") {
				return nil, errors.New("private HTTP hosts must be explicit internal service names")
			}
			hosts = append(hosts, h)
		}
	}
	return hosts, nil
}

func dependencyURL(name, raw, environment string, privateHosts []string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New(name + " is invalid")
	}
	private := false
	for _, h := range privateHosts {
		if h == u.Hostname() {
			private = true
		}
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (private || loopback(u.Hostname()) && environment != "production"))) {
		return errors.New(name + " requires a credential-free HTTPS URL or local development URL")
	}
	return nil
}
