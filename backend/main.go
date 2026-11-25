package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/auth/oidc"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/webapi"
)

func run() error {
	started := time.Now()
	// The database and its WAL contain account tokens and SSH private keys.
	syscall.Umask(0077)

	address := flag.String("addr", "127.0.0.1:8080", "listen address")
	dbPath := flag.String("db", "zentrale.sqlite", "SQLite database path (local filesystem)")
	frontend := flag.String("frontend", "frontend/dist", "built frontend directory")

	public, err := publicServerFlags(flag.CommandLine)
	if err != nil {
		return err
	}
	flag.Parse()
	if err := public.validate(); err != nil {
		return err
	}
	store, err := datastore.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	dbReady := time.Now()

	config, oidcEnabled, err := oidc.ConfigFromEnv()
	if err != nil {
		return err
	}
	authReady := time.Now()

	authentication, err := auth.NewWithIssuer(store, os.Getenv("LOGIN_ARGON2ID"), config.Issuer)
	if err != nil {
		return err
	}

	var oidcClient *oidc.Client
	if oidcEnabled {
		redirect, err := url.Parse(config.RedirectURL)
		if err != nil || redirect.EscapedPath() != webapi.OIDCCallbackPath {
			return fmt.Errorf("ZENTRALE_OIDC_REDIRECT_URL must end with %s", webapi.OIDCCallbackPath)
		}
		origin := redirect.Scheme + "://" + redirect.Host
		if public.Origin != "" && public.Origin != origin {
			return errors.New("ZENTRALE_OIDC_REDIRECT_URL must match the configured public origin")
		}
		public.Origin = origin
		public.SecureCookie = redirect.Scheme == "https"

		discovery, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		oidcClient, err = oidc.New(discovery, config)
		cancel()
		if err != nil {
			return err
		}
	}

	engine, err := agent.New(store, providers.New())
	if err != nil {
		return err
	}
	defer engine.Close()
	engineReady := time.Now()
	configureSyntheticSSHFixtures(engine)

	api := &webapi.Server{
		Store:        store,
		Auth:         authentication,
		OIDC:         oidcClient,
		Engine:       engine,
		Frontend:     *frontend,
		Origin:       public.Origin,
		SecureCookie: public.SecureCookie,
	}

	handler, err := api.Handler()
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              *address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	done := make(chan error, 1)
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	defer listener.Close()

	slog.Info("Zentrale ready", "address", listener.Addr().String(), "pid", os.Getpid(), "database", *dbPath,
		"startup_ms", time.Since(started).Milliseconds(), "phases_ms", map[string]int64{
			"database":       dbReady.Sub(started).Milliseconds(),
			"authentication": authReady.Sub(dbReady).Milliseconds(),
			"engine":         engineReady.Sub(authReady).Milliseconds(),
			"http":           time.Since(engineReady).Milliseconds(),
		})
	go func() {
		done <- server.Serve(listener)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		return server.Shutdown(shutdown)
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
