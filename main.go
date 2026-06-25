// Command fortytwode has the following commands:
//
//	fortytwode fetch          authenticate with 42 and save your raw data to ./output/*.json
//	fortytwode fetch curated  same, but write only ./output/curated.json (what the DB would store)
//	fortytwode serve          run the web app (accounts, public profiles) at :PORT
//	fortytwode migrate        apply any pending database migrations, then exit
//
// fetch is a standalone personal tool (no database). serve and migrate read the
// Postgres connection string from DATABASE_URL; serve and fetch also read the 42
// OAuth settings from the FT_* environment variables.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/EvAvKein/Fortytwode/internal/config"
	"github.com/EvAvKein/Fortytwode/internal/fetch"
	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/EvAvKein/Fortytwode/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nFatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	command := ""
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "fetch":
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		// `fetch curated` writes only the single curated snapshot we'd persist.
		if len(os.Args) > 2 && os.Args[2] == "curated" {
			return fetch.RunCurated(context.Background(), cfg)
		}
		return fetch.Run(context.Background(), cfg)

	case "serve":
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		ctx := context.Background()
		st, err := openStore(ctx)
		if err != nil {
			return err
		}
		defer st.Close()
		return web.Serve(cfg, st)

	case "migrate":
		// openStore applies pending migrations on connect (logging each one it
		// applies); this command just confirms and exits.
		st, err := openStore(context.Background())
		if err != nil {
			return err
		}
		st.Close()
		fmt.Println("Database is up to date.")
		return nil

	default:
		return fmt.Errorf("usage: fortytwode <fetch|serve|migrate>")
	}
}

// openStore connects to the database named by DATABASE_URL.
func openStore(ctx context.Context) (*store.Store, error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, fmt.Errorf("missing required environment variable: DATABASE_URL")
	}
	return store.Open(ctx, dsn)
}
