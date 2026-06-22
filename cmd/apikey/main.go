// Command apikey mints, lists, and revokes API keys for the gateway HTTP API.
//
//	export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
//	go run ./cmd/apikey create --name frontend   # prints the key ONCE
//	go run ./cmd/apikey list
//	go run ./cmd/apikey revoke --prefix dgw_AbCd
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/postgres"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		fail("DATABASE_URL must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := postgres.New(ctx, cfg.DatabaseURL, false)
	if err != nil {
		fail(err.Error())
	}
	defer store.Close()

	switch cmd {
	case "create":
		fs := flag.NewFlagSet("create", flag.ExitOnError)
		name := fs.String("name", "", "label for the key")
		_ = fs.Parse(os.Args[2:])
		key, err := store.CreateAPIKey(ctx, *name)
		if err != nil {
			fail(err.Error())
		}
		fmt.Println("API key created (shown once — store it now):")
		fmt.Println()
		fmt.Println("  " + key)
		fmt.Println()
		fmt.Println("Send it as:  Authorization: Bearer " + key)

	case "list":
		keys, err := store.ListAPIKeys(ctx)
		if err != nil {
			fail(err.Error())
		}
		if len(keys) == 0 {
			fmt.Println("no API keys")
			return
		}
		fmt.Printf("%-16s %-12s %-7s %-20s %s\n", "PREFIX", "NAME", "ACTIVE", "CREATED", "LAST USED")
		for _, k := range keys {
			fmt.Printf("%-16s %-12s %-7t %-20s %s\n",
				k.Prefix, k.Name, k.IsActive, k.CreatedAt.Format(time.RFC3339), formatTime(k.LastUsedAt))
		}

	case "revoke":
		fs := flag.NewFlagSet("revoke", flag.ExitOnError)
		prefix := fs.String("prefix", "", "display prefix of the key to revoke (required)")
		_ = fs.Parse(os.Args[2:])
		if *prefix == "" {
			fail("--prefix is required")
		}
		n, err := store.RevokeAPIKey(ctx, *prefix)
		if err != nil {
			fail(err.Error())
		}
		fmt.Printf("revoked %d key(s) with prefix %q\n", n, *prefix)

	default:
		usage()
	}
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: apikey <create|list|revoke> [flags]")
	os.Exit(2)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}
