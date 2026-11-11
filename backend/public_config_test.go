package main

import (
	"flag"
	"testing"
)

func TestPublicServerConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, origin, secure  string
		args                  []string
		wantOrigin            string
		wantSecure, wantError bool
	}{
		{name: "defaults"},
		{name: "Coolify environment", origin: "https://zentrale.example", secure: "true", wantOrigin: "https://zentrale.example", wantSecure: true},
		{name: "normalize origin", origin: " https://ZENTRALE.example/ ", secure: "1", wantOrigin: "https://zentrale.example", wantSecure: true},
		{name: "explicit false", secure: "false"},
		{name: "flags override environment", origin: "https://old.example", secure: "true", args: []string{"-origin", "http://localhost:8080", "-secure-cookie=false"}, wantOrigin: "http://localhost:8080"},
		{name: "invalid boolean fails closed", secure: "yes", wantError: true},
		{name: "origin path", origin: "https://example.test/app", wantError: true},
		{name: "origin credentials", origin: "https://user:password@example.test", wantError: true},
		{name: "origin query", origin: "https://example.test?", wantError: true},
		{name: "origin fragment", origin: "https://example.test#", wantError: true},
		{name: "origin scheme", origin: "wss://example.test", wantError: true},
		{name: "origin host missing", origin: "https:///app", wantError: true},
		{name: "multiple origins", origin: "https://one.test,https://two.test", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ZENTRALE_ORIGIN", test.origin)
			t.Setenv("ZENTRALE_SECURE_COOKIE", test.secure)
			flags := flag.NewFlagSet(test.name, flag.ContinueOnError)
			options, err := publicServerFlags(flags)
			if err == nil {
				err = flags.Parse(test.args)
			}
			if err == nil {
				err = options.validate()
			}
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError = %v", err, test.wantError)
			}
			if err == nil && (options.Origin != test.wantOrigin || options.SecureCookie != test.wantSecure) {
				t.Fatalf("got %+v, want origin %q, secure %v", options, test.wantOrigin, test.wantSecure)
			}
		})
	}
}
