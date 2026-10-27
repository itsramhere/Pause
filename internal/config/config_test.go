package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func validEnv() map[string]string {
	return map[string]string{"APP_ENV": "test", "DATABASE_URL": "postgres://pause:private-password@127.0.0.1:55432/pause", "MESSAGE_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32)), "PAYPAL_MERCHANT_ID": "TEST_ONLY"}
}
func TestConfigurationValidationAndNoLLMDependency(t *testing.T) {
	env := validEnv()
	get := func(k string) string { return env[k] }
	c, e := From(get)
	if e != nil {
		t.Fatal(e)
	}
	if c.MessageRetention.Hours() != 168 || c.MaxAmount != 500000 || c.SecureCookies {
		t.Fatal("unexpected defaults")
	}
	for _, pair := range [][2]string{{"DATABASE_URL", ""}, {"DATABASE_URL", "postgres://secret-credential"}, {"MESSAGE_ENCRYPTION_KEY", "private-message-key"}, {"APP_BASE_URL", "http://public.example"}, {"APP_BASE_URL", "https://example.test/path"}, {"MAX_AMOUNT_MINOR", "900000"}, {"MESSAGE_RETENTION_HOURS", "169"}, {"ML_SERVICE_URL", "http://inference.example"}, {"ELASTICSEARCH_HISTORY_ALIAS", "client-selected"}} {
		env = validEnv()
		env[pair[0]] = pair[1]
		_, e = From(get)
		if e == nil {
			t.Fatal("accepted invalid config", pair[0])
		}
		if strings.Contains(e.Error(), "secret-credential") || strings.Contains(e.Error(), "private-message-key") {
			t.Fatal("configuration leaked a value")
		}
	}
	env = validEnv()
	env["APP_ENV"] = "production"
	env["APP_BASE_URL"] = "http://127.0.0.1:8080"
	if _, e = From(get); e == nil {
		t.Fatal("production HTTP accepted")
	}
	env["APP_BASE_URL"] = "https://pause.example"
	c, e = From(get)
	if e != nil || !c.SecureCookies {
		t.Fatal("production cookie config", e)
	}
}
