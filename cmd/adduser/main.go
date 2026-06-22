// Command adduser creates (or resets the password of) a front-end user account.
//
// The password is read from a no-echo prompt, or from stdin when piped — never
// from a command-line argument (those leak via `ps` and shell history).
//
//	export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
//	go run ./cmd/adduser --email alice@dfm.co            # interactive prompt
//	printf '%s' "$PW" | go run ./cmd/adduser --email alice@dfm.co   # piped
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/postgres"
)

func main() {
	email := flag.String("email", "", "user email (required)")
	flag.Parse()

	if strings.TrimSpace(*email) == "" {
		fail("--email is required")
	}

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		fail("DATABASE_URL must be set")
	}

	password, err := readPassword()
	if err != nil {
		fail(err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	store, err := postgres.New(ctx, cfg.DatabaseURL, false)
	if err != nil {
		fail(err.Error())
	}
	defer store.Close()

	if err := store.UpsertUser(ctx, *email, password); err != nil {
		fail(err.Error())
	}
	fmt.Printf("user %q saved\n", strings.TrimSpace(*email))
}

// readPassword reads without echo from a TTY (with confirmation), or reads the
// first line from stdin when piped.
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Print("Password: ")
		first, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", err
		}
		fmt.Print("Confirm:  ")
		second, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", err
		}
		if string(first) != string(second) {
			return "", errors.New("passwords do not match")
		}
		return string(first), nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimRight(scanner.Text(), "\r\n"), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("no password provided on stdin")
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}
