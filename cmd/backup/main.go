// Command backup dumps and restores the gateway's own database (device registry,
// users, API keys, mappings, settings, clip/snapshot metadata — NOT telemetry).
//
//	export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
//	go run ./cmd/backup dump    > gateway-backup.tar.gz      # or: dump --out file.tar.gz
//	go run ./cmd/backup restore < gateway-backup.tar.gz      # or: restore --in file.tar.gz
//
// restore is DESTRUCTIVE: it truncates and reloads the gateway tables. Point it at a
// database whose schema already exists (start the gateway once, or restore over the
// existing one). Telemetry is unaffected — it lives in the external webhook store.
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	store, err := postgres.New(ctx, cfg.DatabaseURL, false)
	if err != nil {
		fail(err.Error())
	}
	defer store.Close()

	switch cmd {
	case "dump":
		fs := flag.NewFlagSet("dump", flag.ExitOnError)
		out := fs.String("out", "", "write archive to this file (default: stdout)")
		_ = fs.Parse(os.Args[2:])
		w := os.Stdout
		if *out != "" {
			f, err := os.Create(*out)
			if err != nil {
				fail(err.Error())
			}
			defer f.Close()
			w = f
		}
		rows, err := store.DumpTo(ctx, w, time.Now())
		if err != nil {
			fail(err.Error())
		}
		fmt.Fprintf(os.Stderr, "dumped %d rows\n", rows)

	case "restore":
		fs := flag.NewFlagSet("restore", flag.ExitOnError)
		in := fs.String("in", "", "read archive from this file (default: stdin)")
		yes := fs.Bool("yes", false, "skip the destructive-restore confirmation")
		_ = fs.Parse(os.Args[2:])
		if !*yes {
			fmt.Fprint(os.Stderr, "restore OVERWRITES the gateway database. Re-run with --yes to proceed.\n")
			os.Exit(1)
		}
		r := os.Stdin
		if *in != "" {
			f, err := os.Open(*in)
			if err != nil {
				fail(err.Error())
			}
			defer f.Close()
			r = f
		}
		if err := store.RestoreFrom(ctx, r); err != nil {
			fail(err.Error())
		}
		fmt.Fprintln(os.Stderr, "restore complete")

	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: backup <dump|restore> [--out/--in file] [--yes]")
	os.Exit(2)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}
