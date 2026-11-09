package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/webapi"
)

func run() error {
	// The database and its WAL contain account tokens and SSH private keys.
	syscall.Umask(0077)

	address := flag.String("addr", "127.0.0.1:8080", "listen address")
	dbPath := flag.String("db", "zentrale.duckdb", "DuckDB database path")
	frontend := flag.String("frontend", "frontend/dist", "built frontend directory")

	origin := flag.String("origin", "", "public origin, e.g. https://zentrale.example")
	secure := flag.Bool("secure-cookie", false, "require HTTPS for session cookies")
	flag.Parse()
	store, err := datastore.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	authentication, err := auth.New(store, os.Getenv("LOGIN_ARGON2ID"))
	if err != nil {
		return err
	}

	engine, err := agent.New(store, providers.New())
	if err != nil {
		return err
	}
	defer engine.Close()

	api := &webapi.Server{
		Store:        store,
		Auth:         authentication,
		Engine:       engine,
		Frontend:     *frontend,
		Origin:       *origin,
		SecureCookie: *secure,
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
	go func() {
		slog.Info("Zentrale ready", "address", *address)
		done <- server.ListenAndServe()
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
