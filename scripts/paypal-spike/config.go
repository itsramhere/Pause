package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const sandboxURL = "https://api-m.sandbox.paypal.com"

type config struct {
	Root, AppEnv, AppURL, PayPalURL, ClientID, ClientSecret string
	MerchantID, MerchantLabel, WebhookID, EvidenceDir       string
	AIProvider, AIModel, AIKey                              string
	AITimeout                                               time.Duration
	AIMaxTokens, DeadlineMinutes                            int
}

// Small literal .env reader: no interpolation, command expansion, or logging values.
// Existing process environment takes precedence, including explicitly empty values.
func loadEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("cannot read environment file")
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for line := 1; s.Scan(); line++ {
		v := strings.TrimSpace(strings.TrimPrefix(s.Text(), "\ufeff"))
		if v == "" || strings.HasPrefix(v, "#") {
			continue
		}
		key, value, ok := strings.Cut(v, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || !envName.MatchString(key) {
			return fmt.Errorf("invalid .env syntax at line %d", line)
		}
		if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
			if len(value) < 2 || value[len(value)-1] != value[0] {
				return fmt.Errorf("invalid .env quoting at line %d", line)
			}
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return errors.New("cannot set environment variable")
			}
		}
	}
	return s.Err()
}

func getConfig(root string) (config, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return config{}, errors.New("invalid repository root")
	}
	if err := loadEnv(filepath.Join(root, ".env")); err != nil {
		return config{}, err
	}
	get := func(k, fallback string) string {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
		return fallback
	}
	c := config{Root: root, AppEnv: get("APP_ENV", "demo"), AppURL: get("APP_BASE_URL", "http://127.0.0.1:8787"), PayPalURL: get("PAYPAL_BASE_URL", sandboxURL),
		ClientID: get("PAYPAL_CLIENT_ID", ""), ClientSecret: get("PAYPAL_CLIENT_SECRET", ""), MerchantID: get("PAYPAL_MERCHANT_ID", ""), MerchantLabel: get("PAYPAL_MERCHANT_LABEL", "Pause Sandbox Store"), WebhookID: get("PAYPAL_WEBHOOK_ID", ""),
		AIProvider: get("AI_PROVIDER", "openai"), AIModel: get("AI_MODEL", ""), AIKey: get("AI_API_KEY", ""), EvidenceDir: get("SPIKE_EVIDENCE_DIR", "planning/evidence/P00")}
	for _, item := range []struct {
		k                  string
		fallback, min, max int
		target             *int
	}{
		{"SPIKE_DEADLINE_MINUTES", 30, 1, 30, &c.DeadlineMinutes},
	} {
		v, e := strconv.Atoi(get(item.k, strconv.Itoa(item.fallback)))
		if e != nil || v < item.min || v > item.max {
			return c, fmt.Errorf("%s is outside its allowed integer range", item.k)
		}
		*item.target = v
	}
	if !filepath.IsAbs(c.EvidenceDir) {
		c.EvidenceDir = filepath.Join(root, c.EvidenceDir)
	}
	return c, c.validate()
}

// Legacy model settings must not prevent payment/configuration checks from
// running. Only the explicitly requested legacy command validates them.
func (c config) legacyAIConfig() (config, error) {
	for _, item := range []struct {
		name               string
		fallback, min, max int
		target             *int
	}{
		{"AI_TIMEOUT_SECONDS", 30, 1, 60, nil},
		{"AI_MAX_OUTPUT_TOKENS", 800, 200, 2000, &c.AIMaxTokens},
	} {
		raw := strings.TrimSpace(os.Getenv(item.name))
		if raw == "" {
			raw = strconv.Itoa(item.fallback)
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < item.min || value > item.max {
			return c, fmt.Errorf("%s is outside its allowed integer range", item.name)
		}
		if item.target == nil {
			c.AITimeout = time.Duration(value) * time.Second
		} else {
			*item.target = value
		}
	}
	return c, nil
}

func (c config) validate() error {
	if c.AppEnv != "demo" && c.AppEnv != "test" && c.AppEnv != "development" {
		return errors.New("APP_ENV must be demo, test, or development for this local spike")
	}
	// Exact comparison rejects live hosts, URL userinfo, alternate ports, paths,
	// lookalike suffixes, queries, fragments, and redirects before any request.
	if c.PayPalURL != sandboxURL {
		return errors.New("PAYPAL_BASE_URL must be https://api-m.sandbox.paypal.com; live and unknown hosts are forbidden")
	}
	u, err := url.Parse(c.AppURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Hostname() != "127.0.0.1" {
		return errors.New("APP_BASE_URL must be http://127.0.0.1:<port> for the local spike")
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		return errors.New("APP_BASE_URL requires an explicit port")
	}
	if c.MerchantID != "" && !providerID.MatchString(c.MerchantID) {
		return errors.New("PAYPAL_MERCHANT_ID must be a sandbox merchant account ID")
	}
	return nil
}

func (c config) requirePayPal() error {
	var missing []string
	for _, item := range []struct{ k, v string }{{"PAYPAL_CLIENT_ID", c.ClientID}, {"PAYPAL_CLIENT_SECRET", c.ClientSecret}, {"PAYPAL_MERCHANT_ID", c.MerchantID}} {
		if item.v == "" {
			missing = append(missing, item.k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (c config) inventory() map[string]any {
	names := []string{"PAYPAL_CLIENT_ID", "PAYPAL_CLIENT_SECRET", "PAYPAL_MERCHANT_ID", "PAYPAL_MERCHANT_LABEL", "PAYPAL_WEBHOOK_ID", "PAYPAL_BASE_URL", "DATABASE_URL", "SESSION_KEY", "MESSAGE_ENCRYPTION_KEY", "ML_SERVICE_URL", "ML_SERVICE_TOKEN", "CLASSIFIER_MANIFEST_PATH", "ENCODER_MANIFEST_PATH", "ELASTICSEARCH_URL", "ELASTICSEARCH_API_KEY", "ELASTICSEARCH_CA_CERT", "ELASTICSEARCH_PLAYBOOK_ALIAS", "ELASTICSEARCH_HISTORY_ALIAS", "ELASTICSEARCH_TIMEOUT_SECONDS", "AI_PROVIDER", "AI_MODEL", "AI_API_KEY", "APP_BASE_URL", "APP_ENV"}
	entries := map[string]string{}
	for _, k := range names {
		if strings.TrimSpace(os.Getenv(k)) == "" {
			entries[k] = "missing"
		} else {
			entries[k] = "configured"
		}
	}
	return map[string]any{"environment": c.AppEnv, "paypal_host": "sandbox only", "entries": entries, "note": "Presence is not proof of account access. Defaults do not count as configured secrets."}
}
