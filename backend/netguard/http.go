// Package netguard bounds HTTP access to user-configured service origins.
package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func Origin(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	address, err := url.Parse(raw)
	if err != nil || address.Scheme != "https" || address.Hostname() == "" ||
		address.User != nil || address.RawQuery != "" || address.ForceQuery ||
		address.Fragment != "" || address.Path != "" ||
		strings.ContainsAny(address.Host, "%\\\r\n\x00") {
		return "", errors.New("Git service requires an HTTPS origin without a path, credentials or query")
	}

	address.Host = strings.ToLower(address.Host)
	return address.String(), nil
}

// Each dial resolves and validates the complete DNS answer, then connects to a
// validated IP directly. No second lookup can turn a public address into an
// internal target. Proxy environment variables and redirects are not used.
func HTTPClient(allowPrivate bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 20 * time.Second
	transport.DialContext = guardedDial(allowPrivate, net.DefaultResolver.LookupNetIP, (&net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext)

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type lookupFunc func(context.Context, string, string) ([]netip.Addr, error)
type dialFunc func(context.Context, string, string) (net.Conn, error)

func guardedDial(allowPrivate bool, lookup lookupFunc, dial dialFunc) dialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid service address")
		}

		addresses, err := lookup(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("service address could not be resolved")
		}

		for _, address := range addresses {
			if !allowedAddress(address, allowPrivate) {
				return nil, errors.New("service address is not permitted by its network setting")
			}
		}

		for _, address := range addresses {
			connection, err := dial(ctx, network, net.JoinHostPort(address.Unmap().String(), port))
			if err == nil {
				return connection, nil
			}

			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}

		return nil, errors.New("service connection failed")
	}
}

func allowedAddress(address netip.Addr, allowPrivate bool) bool {
	address = address.Unmap()
	if !address.IsValid() || address.Zone() != "" || address.IsUnspecified() ||
		address.IsMulticast() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() {
		return false
	}

	if address.IsLoopback() {
		return allowPrivate
	}

	for _, prefix := range nonServiceNetworks {
		if prefix.Contains(address) {
			return false
		}
	}

	carrierNetwork := netip.MustParsePrefix("100.64.0.0/10")
	if address.IsPrivate() || carrierNetwork.Contains(address) {
		return allowPrivate
	}

	return address.IsGlobalUnicast()
}

// Reserved/transition addresses must not turn into private destinations after
// the dial (for example through NAT64 or 6to4). Known non-link-local cloud
// metadata endpoints stay blocked even with explicit private-network consent.
var nonServiceNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100.100.100.200/32"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}
