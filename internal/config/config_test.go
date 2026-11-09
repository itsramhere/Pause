package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func validEnv() map[string]string {
	return map[string]string{"APP_ENV": "test", "DATABASE_URL": "postgres://pause:private-password@127.0.0.1:55432/pause", "MESSAGE_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32)), "PAYPAL_MERCHANT_ID": "TEST_ONLY"}
}
func TestConfigurationValidation(t *testing.T) {
	env := validEnv()
	get := func(k string) string { return env[k] }
	c, e := From(get)
	if e != nil {
		t.Fatal(e)
	}
	if c.MessageRetention.Hours() != 168 || c.MaxAmount != 500000 || c.SecureCookies {
		t.Fatal("unexpected defaults")
	}
	if c.GeminiModel != "gemini-3.5-flash-lite" || c.GeminiBaseURL != "https://generativelanguage.googleapis.com" || c.GeminiAPIKey != "" || c.GeminiThinkingLevel != "minimal" {
		t.Fatal("unexpected Gemini defaults")
	}
	for _, pair := range [][2]string{{"DATABASE_URL", ""}, {"DATABASE_URL", "postgres://secret-credential"}, {"MESSAGE_ENCRYPTION_KEY", "private-message-key"}, {"APP_BASE_URL", "http://public.example"}, {"APP_BASE_URL", "https://example.test/path"}, {"MAX_AMOUNT_MINOR", "900000"}, {"MESSAGE_RETENTION_HOURS", "169"}, {"ML_SERVICE_URL", "http://inference.example"}, {"ELASTICSEARCH_HISTORY_ALIAS", "client-selected"}, {"GEMINI_BASE_URL", "http://gemini.example"}, {"GEMINI_BASE_URL", "https://user:pass@gemini.example"}} {
		env = validEnv()
		env[pair[0]] = pair[1]
		_, e = From(get)
		if e == nil {
			t.Fatal("accepted invalid config", pair[0])
		}
		if strings.Contains(e.Error(), "secret-credential") || strings.Contains(e.Error(), "private-message-key") || strings.Contains(e.Error(), "user:pass") {
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

func TestGeminiConfiguration(t *testing.T) {
	env := validEnv()
	get := func(k string) string { return env[k] }
	c, e := From(get)
	if e != nil {
		t.Fatal(e)
	}
	// An empty key is the supported off state; no other Gemini setting may
	// turn the feature on or fail configuration by itself.
	if c.GeminiAPIKey != "" || c.GeminiModel != "gemini-3.5-flash-lite" {
		t.Fatal("Gemini must default to disabled with the documented model")
	}
	env["GEMINI_API_KEY"] = "test-only-key"
	env["GEMINI_MODEL"] = "gemini-custom-model"
	c, e = From(get)
	if e != nil || c.GeminiAPIKey == "" || c.GeminiModel != "gemini-custom-model" {
		t.Fatal("explicit Gemini settings rejected", e)
	}
	env["APP_ENV"] = "production"
	env["APP_BASE_URL"] = "https://pause.example"
	env["GEMINI_BASE_URL"] = "http://127.0.0.1:9999"
	if _, e = From(get); e == nil {
		t.Fatal("production accepted plain-HTTP Gemini endpoint")
	}
}

func TestLLMOnlyConfiguration(t *testing.T) {
	env := map[string]string{"GEMINI_API_KEY": "test-only-key"}
	get := func(k string) string {
		switch k {
		case "GEMINI_API_KEY", "GEMINI_MODEL", "GEMINI_BASE_URL", "GEMINI_THINKING_LEVEL", "APP_ENV", "DEPENDENCY_PRIVATE_HTTP_HOSTS":
		default:
			t.Fatal("LLM config read an application setting", k)
		}
		return env[k]
	}
	c, err := LLMFrom(get)
	if err != nil || c.APIKey != "test-only-key" || c.Model != "gemini-3.5-flash-lite" || c.ThinkingLevel != "minimal" || c.BaseURL != "https://generativelanguage.googleapis.com" {
		t.Fatal("unexpected LLM-only defaults", err)
	}
	for _, level := range []string{"minimal", "low", "medium", "high", "invalid-private-value", "LOW", " minimal"} {
		env["GEMINI_THINKING_LEVEL"] = level
		_, llmErr := LLMFrom(get)
		fullEnv := validEnv()
		fullEnv["GEMINI_THINKING_LEVEL"] = level
		full, fullErr := From(func(k string) string { return fullEnv[k] })
		valid := level == "minimal" || level == "low" || level == "medium" || level == "high"
		if valid {
			if llmErr != nil || fullErr != nil || full.GeminiThinkingLevel != level {
				t.Fatal("valid thinking level rejected", llmErr, fullErr)
			}
		} else if llmErr == nil || fullErr == nil || llmErr.Error() != "GEMINI_THINKING_LEVEL is invalid" || fullErr.Error() != llmErr.Error() {
			t.Fatal("invalid thinking level not bounded", llmErr, fullErr)
		}
	}
}

func TestGeminiURLValidationParity(t *testing.T) {
	for _, tc := range []struct {
		url, environment, hosts string
		valid                   bool
	}{
		{"https://gemini.example", "test", "", true},
		{"http://127.0.0.1:9999", "test", "", true},
		{"http://127.0.0.1:9999", "production", "", false},
		{"http://inference:8090", "production", "inference", true},
		{"http://inference:8090", "production", "", false},
		{"http://public.example", "test", "", false},
		{"https://user:private-value@gemini.example", "test", "", false},
		{"https://gemini.example?key=private-value", "test", "", false},
		{"https://gemini.example#fragment", "test", "", false},
		{"https://gemini.example/%zz", "test", "", false},
		{"https://gemini.example", "test", "public.example", false},
	} {
		env := validEnv()
		env["GEMINI_BASE_URL"], env["APP_ENV"], env["DEPENDENCY_PRIVATE_HTTP_HOSTS"] = tc.url, tc.environment, tc.hosts
		if tc.environment == "production" {
			env["APP_BASE_URL"] = "https://pause.example"
		}
		get := func(k string) string { return env[k] }
		_, fullErr := From(get)
		_, llmErr := LLMFrom(get)
		if (fullErr == nil) != tc.valid || (llmErr == nil) != tc.valid {
			t.Fatal("URL validation disagrees", fullErr, llmErr)
		}
		if !tc.valid && (fullErr.Error() != llmErr.Error() || strings.Contains(llmErr.Error(), "private-value")) {
			t.Fatal("URL validation errors differ or expose values", fullErr, llmErr)
		}
	}
}

func TestDemoConfigurationIsExplicitAndNonProduction(t *testing.T) {
	env := validEnv()
	get := func(k string) string { return env[k] }
	c, e := From(get)
	if e != nil || c.DemoEnabled {
		t.Fatal("demo must default off", e)
	}
	env["DEMO_ENABLED"] = "1"
	c, e = From(get)
	if e != nil || !c.DemoEnabled {
		t.Fatal("explicit demo", e)
	}
	env["APP_ENV"] = "production"
	env["APP_BASE_URL"] = "https://pause.example"
	if _, e = From(get); e == nil {
		t.Fatal("production scenario launcher accepted")
	}
}

func TestP06PrivateNetworkAndProxyConfiguration(t *testing.T) {
	env := validEnv()
	get := func(k string) string { return env[k] }
	env["APP_ENV"] = "production"
	env["APP_BASE_URL"] = "https://pause.example"
	env["ML_SERVICE_URL"] = "http://inference:8090"
	env["ML_SERVICE_TOKEN"] = strings.Repeat("x", 32)
	if _, err := From(get); err == nil {
		t.Fatal("implicit HTTP trust")
	}
	env["DEPENDENCY_PRIVATE_HTTP_HOSTS"] = "inference"
	env["TRUSTED_PROXY_CIDRS"] = "10.42.0.0/24"
	if _, err := From(get); err != nil {
		t.Fatal(err)
	}
	env["TRUSTED_PROXY_CIDRS"] = "0.0.0.0/0"
	if _, err := From(get); err == nil {
		t.Fatal("unbounded proxy trust")
	}
}
