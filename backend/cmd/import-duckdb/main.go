//go:build duckdb_import

package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/lehlud/zentrale/datastore"
)

func main() {
	source := flag.String("source", "zentrale.duckdb", "existing DuckDB (read only; stop Zentrale before import)")
	target := flag.String("target", "zentrale.sqlite", "new SQLite file (must not exist)")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	counts, err := datastore.ImportDuckDB(ctx, *source, *target)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(counts); err != nil {
		log.Fatal(err)
	}
}
