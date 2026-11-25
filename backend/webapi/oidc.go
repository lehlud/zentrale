package webapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

const (
	OIDCCallbackPath = "/auth/oidc/callback"
	oidcStartPath    = "/auth/oidc/start"
)

func (server *Server) setSessionCookie(request echo.Context, token string, maxAge int) {
	sameSite := http.SameSiteStrictMode
	if server.OIDC != nil {
		sameSite = http.SameSiteLaxMode
	}
	request.SetCookie(&http.Cookie{
		Name:     "zentrale_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   server.SecureCookie || request.Request().TLS != nil,
		SameSite: sameSite,
		MaxAge:   maxAge,
	})
}

func (server *Server) startOIDC(request echo.Context) error {
	if err := server.OIDC.Start(request.Response(), request.Request()); err != nil {
		return request.String(http.StatusServiceUnavailable, "OpenID Connect login is temporarily unavailable.")
	}
	return nil
}

func (server *Server) registerOIDC(e *echo.Echo) {
	e.GET(oidcStartPath, func(request echo.Context) error {
		request.Response().Header().Set("Cache-Control", "no-store")
		if server.Auth.Valid(server.token(request.Request())) {
			return request.Redirect(http.StatusSeeOther, "/")
		}
		return server.startOIDC(request)
	})

	e.GET(OIDCCallbackPath, func(request echo.Context) error {
		identity, err := server.OIDC.Callback(request.Response(), request.Request())
		if err != nil {
			return request.String(http.StatusUnauthorized, "OpenID Connect login failed. Open Zentrale to try again.")
		}

		token, err := server.Auth.LoginOIDC(identity.Issuer, identity.Subject, identity.PreferredUsername, identity.Name)
		if err != nil {
			return request.String(http.StatusForbidden, "No active Zentrale account for this OpenID Connect identity. Contact your administrator.")
		}

		server.setSessionCookie(request, token, 86400)
		return request.Redirect(http.StatusSeeOther, "/")
	})
}
