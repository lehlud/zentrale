package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type publicServerOptions struct {
	Origin       string
	SecureCookie bool
}

// Deployment configuration is trusted; request forwarding headers are not.
// Environment defaults support platforms such as Coolify without replacing the
// image's entrypoint. Explicit command-line flags take precedence.
func publicServerFlags(flags *flag.FlagSet) (*publicServerOptions, error) {
	options := &publicServerOptions{Origin: strings.TrimSpace(os.Getenv("ZENTRALE_ORIGIN"))}
	if raw := strings.TrimSpace(os.Getenv("ZENTRALE_SECURE_COOKIE")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("ZENTRALE_SECURE_COOKIE must be a boolean: %w", err)
		}
		options.SecureCookie = value
	}
	flags.StringVar(&options.Origin, "origin", options.Origin, "public origin, e.g. https://zentrale.example (env ZENTRALE_ORIGIN)")
	flags.BoolVar(&options.SecureCookie, "secure-cookie", options.SecureCookie, "require HTTPS for session cookies (env ZENTRALE_SECURE_COOKIE)")
	return options, nil
}

func (options *publicServerOptions) validate() error {
	if options.Origin == "" {
		return nil
	}
	parsed, err := url.Parse(options.Origin)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
		parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(options.Origin, "#") {
		return fmt.Errorf("-origin / ZENTRALE_ORIGIN must be a single HTTP(S) origin without credentials, path, query or fragment")
	}
	options.Origin = parsed.Scheme + "://" + strings.ToLower(parsed.Host)
	return nil
}
