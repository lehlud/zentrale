package webapi

import (
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// Office is a static browser bundle. This route never takes documents, reads
// application data, or forwards requests to an Office service.
func (server *Server) officeAsset(c echo.Context) error {
	// The editor emits repeated slashes in some of its bundled font paths.
	asset := path.Clean(c.Param("*"))
	if asset == "." || !filepath.IsLocal(asset) {
		return echo.ErrNotFound
	}
	filename := filepath.Join(server.Frontend, "office", filepath.FromSlash(asset))
	if info, err := os.Stat(filename); err != nil || !info.Mode().IsRegular() {
		return echo.ErrNotFound
	}

	host := c.Request().Host
	if server.Origin != "" {
		if origin, err := url.Parse(server.Origin); err == nil && origin.Host != "" {
			host = origin.Host
		}
	}
	if host == "" || strings.ContainsAny(host, " \t\r\n'\"*/;,@?#\\") {
		return echo.ErrBadRequest
	}
	// Permit only these static assets, including when the editor's origin is
	// opaque or TLS terminates at a reverse proxy. No authenticated API paths.
	assets := "http://" + host + "/office/ https://" + host + "/office/"
	scriptPolicy := " 'wasm-unsafe-eval'"
	framePolicy := ""
	if strings.HasSuffix(asset, ".html") {
		// The pinned editor compiles its own UI templates inside the sandbox.
		// Neither the main application nor the converter worker needs eval.
		scriptPolicy = " 'unsafe-inline' 'unsafe-eval'"
		framePolicy = "; sandbox allow-scripts"
	}
	policy := "default-src 'none'; script-src " + assets + scriptPolicy + "; " +
		"connect-src " + assets + " blob: data:; img-src " + assets + " blob: data:; " +
		"style-src " + assets + " 'unsafe-inline'; font-src " + assets + " data:; " +
		"worker-src blob:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'" + framePolicy

	headers := c.Response().Header()
	headers.Set("Content-Security-Policy", policy)
	headers.Set("Access-Control-Allow-Origin", "*")
	headers.Set("Referrer-Policy", "no-referrer")
	if info, err := os.Stat(filename + ".gz"); err == nil && info.Mode().IsRegular() {
		headers.Add("Vary", "Accept-Encoding")
		if c.Request().Header.Get("Range") == "" && acceptsOfficeGzip(c.Request().Header.Get("Accept-Encoding")) {
			headers.Set("Content-Encoding", "gzip")
			headers.Set("Content-Type", mime.TypeByExtension(filepath.Ext(filename)))
			return c.File(filename + ".gz")
		}
	}
	return c.File(filename)
}

func acceptsOfficeGzip(header string) bool {
	for _, entry := range strings.Split(header, ",") {
		coding, options, _ := strings.Cut(entry, ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		for _, option := range strings.Split(options, ";") {
			name, value, ok := strings.Cut(option, "=")
			if ok && strings.TrimSpace(name) == "q" {
				quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				return err == nil && quality > 0 && quality <= 1
			}
		}
		return true
	}
	return false
}
