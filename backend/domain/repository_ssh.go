package domain

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var repositorySSHUser = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`)
var repositorySSHHost = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// RepositorySSHAddress identifies the server independently of the user and path.
func RepositorySSHAddress(repositoryURL string) (string, error) {
	if err := ValidateRepositorySSHURL(repositoryURL); err != nil {
		return "", err
	}
	if strings.HasPrefix(repositoryURL, "ssh://") {
		parsed, err := url.Parse(repositoryURL)
		if err != nil {
			return "", err
		}
		port := parsed.Port()
		if port == "" {
			port = "22"
		}
		return net.JoinHostPort(strings.ToLower(parsed.Hostname()), port), nil
	}
	destination, _, _ := strings.Cut(repositoryURL, ":")
	_, host, _ := strings.Cut(destination, "@")
	return net.JoinHostPort(strings.ToLower(host), "22"), nil
}

// ValidateRepositorySSHURL accepts Git's SSH URL and user@host:path forms.
func ValidateRepositorySSHURL(address string) error {
	invalid := errors.New("repository requires an SSH Git URL (ssh://host/path or user@host:path)")
	unsafe := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }
	if address == "" || len(address) > 4096 || strings.ContainsFunc(address, unsafe) {
		return invalid
	}

	if strings.HasPrefix(address, "ssh://") {
		parsed, err := url.Parse(address)
		if err != nil || strings.Trim(parsed.Path, "/") == "" || strings.ContainsFunc(parsed.Path, unsafe) ||
			parsed.ForceQuery || parsed.RawQuery != "" || strings.Contains(address, "#") {
			return invalid
		}
		if host := parsed.Hostname(); !repositorySSHHost.MatchString(host) && net.ParseIP(host) == nil {
			return invalid
		}
		if parsed.User != nil {
			if _, password := parsed.User.Password(); password || !repositorySSHUser.MatchString(parsed.User.Username()) {
				return invalid
			}
		}
		if port := parsed.Port(); port != "" {
			value, err := strconv.ParseUint(port, 10, 16)
			if err != nil || value == 0 || strconv.FormatUint(value, 10) != port {
				return invalid
			}
		} else if strings.HasSuffix(parsed.Host, ":") {
			return invalid
		}
		return nil
	}

	destination, path, _ := strings.Cut(address, ":")
	user, host, _ := strings.Cut(destination, "@")
	if path == "" || strings.Contains(address, "://") ||
		!repositorySSHUser.MatchString(user) || !repositorySSHHost.MatchString(host) {
		return invalid
	}
	return nil
}
