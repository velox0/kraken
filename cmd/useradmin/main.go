package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"kraken/internal/config"
)

func usage() {
	s := os.Getenv("DATABASE_URL")

	fmt.Fprintf(os.Stderr, `Kraken User Admin Tool

Usage:
  useradmin create  --email EMAIL --password PASS [--name NAME]
  useradmin passwd  --email EMAIL --password NEWPASS
  useradmin [info | freeze | unfreeze | delete]    --email EMAIL

Environment:
  DATABASE_URL  PostgreSQL connection string
                (default: %s)
`, s)
	os.Exit(1)
}

func getFlag(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func must(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func connStr() string {
	return config.Load().PostgresURL
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	cmd := strings.ToLower(os.Args[1])
	args := os.Args[2:]
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, connStr())
	must(err)
	defer conn.Close(ctx)

	switch cmd {
	case "create":
		email := strings.TrimSpace(strings.ToLower(getFlag(args, "--email")))
		password := getFlag(args, "--password")
		name := getFlag(args, "--name")
		if name == "" {
			name = "Admin"
		}
		if email == "" || password == "" {
			fmt.Fprintln(os.Stderr, "error: --email and --password are required")
			usage()
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		must(err)

		scopes := []string{"admin"}
		var id int64
		err = conn.QueryRow(ctx, `
			INSERT INTO users(email, password_hash, display_name, scopes, role_level)
			VALUES($1, $2, $3, $4, 0)
			ON CONFLICT(email) DO UPDATE SET
				password_hash = EXCLUDED.password_hash,
				display_name = EXCLUDED.display_name,
				scopes = EXCLUDED.scopes,
				role_level = 0,
				is_frozen = FALSE,
				failed_attempts = 0,
				frozen_at = NULL,
				updated_at = NOW()
			RETURNING id
		`, email, string(hash), name, scopes).Scan(&id)
		must(err)

		fmt.Printf("✓ Super-admin created/updated  id=%d  email=%s  role_level=0\n", id, email)

	case "passwd":
		email := strings.TrimSpace(strings.ToLower(getFlag(args, "--email")))
		password := getFlag(args, "--password")
		if email == "" || password == "" {
			fmt.Fprintln(os.Stderr, "error: --email and --password are required")
			usage()
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		must(err)

		tag, err := conn.Exec(ctx, `
			UPDATE users SET password_hash=$2, failed_attempts=0, is_frozen=FALSE, frozen_at=NULL, updated_at=NOW()
			WHERE email=$1
		`, email, string(hash))
		must(err)

		if tag.RowsAffected() == 0 {
			fmt.Fprintf(os.Stderr, "error: no user found with email %s\n", email)
			os.Exit(1)
		}
		fmt.Printf("✓ Password reset for %s (account also unfrozen)\n", email)

	case "freeze":
		email := strings.TrimSpace(strings.ToLower(getFlag(args, "--email")))
		if email == "" {
			fmt.Fprintln(os.Stderr, "error: --email is required")
			usage()
		}

		tag, err := conn.Exec(ctx, `
			UPDATE users SET is_frozen=TRUE, updated_at=NOW()
			WHERE email=$1
		`, email)
		must(err)

		if tag.RowsAffected() == 0 {
			fmt.Fprintf(os.Stderr, "error: no user found with email %s\n", email)
			os.Exit(1)
		}
		fmt.Printf("✓ User %s has been frozen\n", email)

	case "unfreeze":
		email := strings.TrimSpace(strings.ToLower(getFlag(args, "--email")))
		if email == "" {
			fmt.Fprintln(os.Stderr, "error: --email is required")
			usage()
		}

		tag, err := conn.Exec(ctx, `
			UPDATE users SET is_frozen=FALSE, updated_at=NOW()
			WHERE email=$1
		`, email)
		must(err)

		if tag.RowsAffected() == 0 {
			fmt.Fprintf(os.Stderr, "error: no user found with email %s\n", email)
			os.Exit(1)
		}
		fmt.Printf("✓ User %s has been unfrozen\n", email)

	case "delete":
		email := strings.TrimSpace(strings.ToLower(getFlag(args, "--email")))
		if email == "" {
			fmt.Fprintln(os.Stderr, "error: --email is required")
			usage()
		}

		tag, err := conn.Exec(ctx, `
			DELETE FROM users WHERE email=$1
		`, email)
		must(err)

		if tag.RowsAffected() == 0 {
			fmt.Fprintf(os.Stderr, "error: no user found with email %s\n", email)
			os.Exit(1)
		}
		fmt.Printf("✓ User %s has been frozen\n", email)

	case "info":
		email := strings.TrimSpace(strings.ToLower(getFlag(args, "--email")))
		if email == "" {
			fmt.Fprintln(os.Stderr, "error: --email is required")
			usage()
		}

		var id int64
		var displayName string
		var roleLevel int
		var isFrozen bool
		var failedAttempts int
		var scopes []string
		err := conn.QueryRow(ctx, `
			SELECT id, display_name, role_level, is_frozen, failed_attempts, scopes
			FROM users WHERE email=$1
		`, email).Scan(&id, &displayName, &roleLevel, &isFrozen, &failedAttempts, &scopes)
		if err != nil {
			if err == pgx.ErrNoRows {
				fmt.Fprintf(os.Stderr, "no user found with email %s\n", email)
				os.Exit(1)
			}
			must(err)
		}

		fmt.Printf("ID:              %d\n", id)
		fmt.Printf("Email:           %s\n", email)
		fmt.Printf("Display Name:    %s\n", displayName)
		fmt.Printf("Role Level:      %d\n", roleLevel)
		fmt.Printf("Frozen:          %v\n", isFrozen)
		fmt.Printf("Failed Attempts: %d\n", failedAttempts)
		fmt.Printf("Scopes:          %v\n", scopes)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
	}
}
