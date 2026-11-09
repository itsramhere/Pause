package config

import (
	"strings"
	"testing"
)

func TestMailConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]string
		valid  bool
	}{
		{"default", nil, true},
		{"log", map[string]string{"MAIL_MODE": "log"}, true},
		{"smtp", map[string]string{"MAIL_MODE": "smtp", "SMTP_HOST": "localhost", "MAIL_FROM": "Pause <sender@pause.dev>"}, true},
		{"unknown", map[string]string{"MAIL_MODE": "private-value"}, false},
		{"no host", map[string]string{"MAIL_MODE": "smtp", "MAIL_FROM": "sender@pause.dev"}, false},
		{"no from", map[string]string{"MAIL_MODE": "smtp", "SMTP_HOST": "localhost"}, false},
		{"zero port", map[string]string{"SMTP_PORT": "0"}, false},
		{"large port", map[string]string{"SMTP_PORT": "65536"}, false},
		{"invalid port", map[string]string{"SMTP_PORT": "private-value"}, false},
		{"min port", map[string]string{"SMTP_PORT": "1"}, true},
		{"max port", map[string]string{"SMTP_PORT": "65535"}, true},
		{"invalid from", map[string]string{"MAIL_FROM": "private-value"}, false},
		{"many from", map[string]string{"MAIL_FROM": "a@pause.dev,b@pause.dev"}, false},
		{"invalid redirect", map[string]string{"MAIL_REDIRECT_TO": "private-value"}, false},
		{"many redirects", map[string]string{"MAIL_REDIRECT_TO": "a@pause.dev,b@pause.dev"}, false},
		{"injection", map[string]string{"MAIL_FROM": "a@pause.dev\r\nBcc: b@pause.dev"}, false},
		{"redirect", map[string]string{"MAIL_REDIRECT_TO": "judge@pause.dev"}, true},
		{"production redirect", map[string]string{"APP_ENV": "production", "APP_BASE_URL": "https://pause.dev", "MAIL_REDIRECT_TO": "judge@pause.dev"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			for k, v := range tc.values {
				env[k] = v
			}
			c, err := From(func(k string) string { return env[k] })
			if (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
			if err != nil && (strings.Contains(err.Error(), "private-value") || strings.Contains(err.Error(), "@pause.dev")) {
				t.Fatal("value leaked")
			}
			if tc.name == "default" && (c.MailMode != "off" || c.SMTPPort != 587) {
				t.Fatal("defaults")
			}
		})
	}
}
