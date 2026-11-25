// Package oidc implements Zentrale's OpenID Connect authorization code flow.
package oidc

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Config contains the provider and registered relying-party settings.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// ConfigFromEnv reports whether OpenID Connect is configured. An incomplete
// or invalid configuration is an error, not a signal to fall back to local login.
func ConfigFromEnv() (Config, bool, error) {
	config := Config{
		Issuer:       os.Getenv("ZENTRALE_OIDC_ISSUER"),
		ClientID:     os.Getenv("ZENTRALE_OIDC_CLIENT_ID"),
		ClientSecret: os.Getenv("ZENTRALE_OIDC_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("ZENTRALE_OIDC_REDIRECT_URL"),
	}
	if config == (Config{}) {
		return Config{}, false, nil
	}

	if _, _, err := config.validate(); err != nil {
		return Config{}, true, err
	}
	return config, true, nil
}

func (c Config) validate() (*url.URL, *url.URL, error) {
	for _, field := range []struct{ name, value string }{
		{"ZENTRALE_OIDC_ISSUER", c.Issuer},
		{"ZENTRALE_OIDC_CLIENT_ID", c.ClientID},
		{"ZENTRALE_OIDC_CLIENT_SECRET", c.ClientSecret},
		{"ZENTRALE_OIDC_REDIRECT_URL", c.RedirectURL},
	} {
		if strings.TrimSpace(field.value) == "" {
			return nil, nil, fmt.Errorf("OIDC configuration: %s is required", field.name)
		}
	}

	issuer, err := parseWebURL(c.Issuer)
	if err != nil {
		return nil, nil, fmt.Errorf("ZENTRALE_OIDC_ISSUER: %w", err)
	}
	if issuer.RawQuery != "" || issuer.ForceQuery {
		return nil, nil, errors.New("ZENTRALE_OIDC_ISSUER must not contain a query")
	}

	redirect, err := parseWebURL(c.RedirectURL)
	if err != nil {
		return nil, nil, fmt.Errorf("ZENTRALE_OIDC_REDIRECT_URL: %w", err)
	}
	if redirect.Path == "" || redirect.RawQuery != "" || redirect.ForceQuery {
		return nil, nil, errors.New("ZENTRALE_OIDC_REDIRECT_URL must have a callback path and no query")
	}

	return issuer, redirect, nil
}

func parseWebURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil ||
		strings.Contains(raw, "#") || strings.TrimSpace(raw) != raw {
		return nil, errors.New("expected an absolute URL without userinfo or fragment")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return nil, errors.New("HTTPS is required except on loopback addresses")
	}
	return u, nil
}

func isLoopback(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}
