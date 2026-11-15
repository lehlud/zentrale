package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestPrivateNetworkConsentDoesNotAllowMetadataOrMulticast(t *testing.T) {
	for _, test := range []struct {
		address string
		public  bool
		private bool
	}{
		{"8.8.8.8", true, true},
		{"2606:4700:4700::1111", true, true},
		{"192.168.1.2", false, true},
		{"100.64.1.2", false, true},
		{"127.0.0.1", false, true},
		{"::1", false, true},
		{"fd00::1", false, true},
		{"::ffff:192.168.1.2", false, true},
		{"169.254.169.254", false, false},
		{"fe80::1", false, false},
		{"0.0.0.0", false, false},
		{"::", false, false},
		{"224.0.0.1", false, false},
		{"ff02::1", false, false},
		{"240.0.0.1", false, false},
		{"198.18.0.1", false, false},
		{"100.100.100.200", false, false},
		{"168.63.129.16", false, false},
		{"fd00:ec2::254", false, false},
		{"64:ff9b::a9fe:a9fe", false, false},
		{"2002:7f00:1::", false, false},
	} {
		address := netip.MustParseAddr(test.address)
		if allowedAddress(address, false) != test.public || allowedAddress(address, true) != test.private {
			t.Error("unexpected address policy", test.address)
		}
	}
}

func TestGuardedDialPinsValidatedIPAndRejectsMixedDNS(t *testing.T) {
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}

	dialed := ""
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		return nil, errors.New("test connection")
	}

	_, _ = guardedDial(false, lookup, dial)(context.Background(), "tcp", "git.example:443")
	if dialed != "8.8.8.8:443" {
		t.Fatal("dial resolved the hostname again", dialed)
	}

	dialed = ""
	lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
	}

	if _, err := guardedDial(false, lookup, dial)(context.Background(), "tcp", "git.example:443"); err == nil || dialed != "" {
		t.Fatal("mixed DNS answer reached a socket", err, dialed)
	}
}

func TestOriginRejectsCredentialsPathsAndNonHTTPS(t *testing.T) {
	for _, raw := range []string{
		"http://git.example", "https://user:secret@git.example", "https://git.example/path",
		"https://git.example?query", "https://git.example?", "https://git.example#fragment",
		"https://[fe80::1%25eth0]", "file:///etc/passwd",
	} {
		if _, err := Origin(raw); err == nil {
			t.Error("unsafe origin accepted", raw)
		}
	}

	if value, err := Origin(" https://Git.Example:8443/ "); err != nil || value != "https://git.example:8443" {
		t.Fatal("valid origin was not normalized", value, err)
	}
}
