package database

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvPostgresAdminURL, if set, points at a connection string with superuser
// privileges (typically to the `postgres` database). When present, Bootstrap
// uses it to create the application role and database before applying the
// schema. When absent, Bootstrap skips the admin phase and assumes the role
// + database already exist.
const EnvPostgresAdminURL = "POSTGRES_ADMIN_URL"

//go:embed sql/schema.sql
var schemaSQL string

// Bootstrap brings the target database to a runnable state. It's idempotent
// and safe to call on every process start. Sequence:
//
//  1. If POSTGRES_ADMIN_URL is set, connect as the admin and ensure the app
//     role + database exist (ensureRoleAndDatabase).
//  2. Apply the embedded schema.sql (all statements are IF NOT EXISTS).
func Bootstrap(ctx context.Context, pool *pgxpool.Pool, role, password, database string) error {
	if adminURL := strings.TrimSpace(os.Getenv(EnvPostgresAdminURL)); adminURL != "" {
		if err := ensureRoleAndDatabase(ctx, adminURL, role, password, database); err != nil {
			return fmt.Errorf("admin bootstrap: %w", err)
		}
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

// ensureRoleAndDatabase opens a short-lived connection to the admin URL,
// creates the role + database if missing, and keeps the password in sync
// with what the app expects.
func ensureRoleAndDatabase(ctx context.Context, adminURL, role, password, database string) error {
	if role == "" || database == "" {
		return errors.New("role and database must be non-empty")
	}

	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect admin: %w", err)
	}
	defer conn.Close(ctx)

	roleIdent := pgx.Identifier{role}.Sanitize()
	dbIdent := pgx.Identifier{database}.Sanitize()
	// Postgres doesn't accept bind parameters inside CREATE ROLE / CREATE
	// DATABASE, so identifiers are quoted via pgx.Identifier and the password
	// is passed as a literal through doubled-quote escaping. The password
	// originates from a sealed secret, not user input — but the fully
	// constructed SQL travels over the wire in plaintext and is visible in
	// pg_stat_activity and the server log's statement sample for the duration
	// of the exec. Rotate the app password after any bootstrap against an
	// audited database. Do NOT replace this with $1 bind parameters — they
	// are a syntax error inside CREATE ROLE / EXECUTE format(...) context.
	escapedPassword := "'" + strings.ReplaceAll(password, "'", "''") + "'"

	roleDDL := fmt.Sprintf(`
DO $bootstrap$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = %s) THEN
        EXECUTE 'CREATE ROLE %s LOGIN PASSWORD %s';
    ELSE
        EXECUTE 'ALTER ROLE %s WITH LOGIN PASSWORD %s';
    END IF;
END
$bootstrap$;`,
		quoteLiteral(role),
		roleIdent, escapedPassword,
		roleIdent, escapedPassword,
	)
	if _, err := conn.Exec(ctx, roleDDL); err != nil {
		return fmt.Errorf("ensure role: %w", err)
	}

	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, database).Scan(&exists); err != nil {
		return fmt.Errorf("check database: %w", err)
	}
	if !exists {
		// CREATE DATABASE cannot run inside a transaction; use a single Exec.
		if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, dbIdent, roleIdent)); err != nil {
			return fmt.Errorf("create database: %w", err)
		}
	}
	return nil
}

// quoteLiteral wraps s in single quotes and doubles any embedded single quote.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
