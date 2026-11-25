package oidc

import (
	"strings"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	settings := map[string]string{
		"ZENTRALE_OIDC_ISSUER":        "https://idp.test/tenant",
		"ZENTRALE_OIDC_CLIENT_ID":     testClientID,
		"ZENTRALE_OIDC_CLIENT_SECRET": testClientSecret,
		"ZENTRALE_OIDC_REDIRECT_URL":  testRedirect,
	}
	for key := range settings {
		t.Setenv(key, "")
	}
	if config, enabled, err := ConfigFromEnv(); config != (Config{}) || enabled || err != nil {
		t.Fatalf("empty configuration returned %#v, %t, %v", config, enabled, err)
	}

	for key, value := range settings {
		t.Setenv(key, value)
	}
	want := Config{Issuer: settings["ZENTRALE_OIDC_ISSUER"], ClientID: testClientID, ClientSecret: testClientSecret, RedirectURL: testRedirect}
	if config, enabled, err := ConfigFromEnv(); config != want || !enabled || err != nil {
		t.Fatalf("complete configuration returned enabled=%t, err=%v", enabled, err)
	}

	for key := range settings {
		t.Run("missing "+key, func(t *testing.T) {
			t.Setenv(key, "")
			_, enabled, err := ConfigFromEnv()
			if !enabled || err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("partial configuration returned enabled=%t, err=%v", enabled, err)
			}
		})
	}
}

func TestConfigURLs(t *testing.T) {
	tests := []struct {
		name     string
		issuer   string
		redirect string
		valid    bool
	}{
		{"production", "https://idp.test/tenant", testRedirect, true},
		{"loopback development", "http://127.0.0.1:1234", "http://[::1]:8080/callback", true},
		{"cleartext issuer", "http://idp.test", testRedirect, false},
		{"cleartext callback", "https://idp.test", "http://zentrale.test/callback", false},
		{"issuer query", "https://idp.test/?realm=tenant", testRedirect, false},
		{"callback query", "https://idp.test", testRedirect + "?next=/", false},
		{"callback fragment", "https://idp.test", testRedirect + "#", false},
		{"relative callback", "https://idp.test", "/callback", false},
		{"missing callback path", "https://idp.test", "https://zentrale.test", false},
		{"userinfo", "https://user:pass@idp.test", testRedirect, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := Config{Issuer: test.issuer, ClientID: testClientID, ClientSecret: testClientSecret, RedirectURL: test.redirect}
			_, _, err := config.validate()
			if (err == nil) != test.valid {
				t.Fatalf("valid = %t, error = %v", test.valid, err)
			}
		})
	}
}
