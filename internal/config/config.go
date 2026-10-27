package config

import (
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment, Listen, Origin, DatabaseURL, WebDir              string
	MessageKey                                                    []byte
	MerchantID, MerchantLabel                                     string
	MaxAmount                                                     int64
	MessageRetention, OperationRetention, SessionTTL, DecisionTTL time.Duration
	MLURL, MLToken, ClassifierManifest, EncoderManifest           string
	ElasticURL, ElasticKey, PlaybookAlias, HistoryAlias           string
	ElasticCA                                                     string
	SecureCookies                                                 bool
	PayPalURL, PayPalClientID, PayPalSecret, PayPalWebhookID      string
}

// Load never includes values or credentials in a configuration error.
func Load() (Config, error) { return From(os.Getenv) }
func From(get func(string) string) (Config, error) {
	c := Config{Environment: value(get, "APP_ENV", "development"), Listen: value(get, "HTTP_ADDR", "127.0.0.1:8080"), Origin: value(get, "APP_BASE_URL", "http://127.0.0.1:5173"), DatabaseURL: get("DATABASE_URL"), WebDir: value(get, "WEB_DIST_DIR", "web/dist"), MerchantID: get("PAYPAL_MERCHANT_ID"), MerchantLabel: value(get, "PAYPAL_MERCHANT_LABEL", "Pause Sandbox Store"), MaxAmount: 500000, MessageRetention: 7 * 24 * time.Hour, OperationRetention: 24 * time.Hour, SessionTTL: 8 * time.Hour, DecisionTTL: 30 * time.Minute, MLURL: get("ML_SERVICE_URL"), MLToken: get("ML_SERVICE_TOKEN"), ClassifierManifest: value(get, "CLASSIFIER_MANIFEST_PATH", "models/classifier.manifest.json"), EncoderManifest: value(get, "ENCODER_MANIFEST_PATH", "models/encoder.manifest.json"), ElasticURL: get("ELASTICSEARCH_URL"), ElasticKey: get("ELASTICSEARCH_API_KEY"), PlaybookAlias: value(get, "ELASTICSEARCH_PLAYBOOK_ALIAS", "pause-playbooks"), HistoryAlias: value(get, "ELASTICSEARCH_HISTORY_ALIAS", "pause-request-history")}
	if c.Environment != "development" && c.Environment != "test" && c.Environment != "demo" && c.Environment != "production" {
		return c, errors.New("APP_ENV is invalid")
	}
	c.ElasticCA = get("ELASTICSEARCH_CA_CERT")
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
	for _, item := range []struct{ name, raw string }{{"ML_SERVICE_URL", c.MLURL}, {"ELASTICSEARCH_URL", c.ElasticURL}} {
		if item.raw != "" {
			u, e := url.Parse(item.raw)
			if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname()) && c.Environment != "production")) {
				return c, errors.New(item.name + " requires a credential-free HTTPS URL or local development URL")
			}
		}
	}
	if c.MLURL != "" && len(c.MLToken) < 32 {
		return c, errors.New("ML_SERVICE_TOKEN must contain at least 32 characters")
	}
	if c.PlaybookAlias != "pause-playbooks" || c.HistoryAlias != "pause-request-history" {
		return c, errors.New("search aliases must match the approved logical indexes")
	}
	return c, nil
}
func value(get func(string) string, k, d string) string {
	if s := get(k); s != "" {
		return s
	}
	return d
}
func loopback(h string) bool { return h == "127.0.0.1" || h == "localhost" || h == "::1" }
