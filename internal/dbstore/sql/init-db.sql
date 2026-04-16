-- init-db.sql — admin-scope bootstrap for the reddit-spy database.
--
-- This file documents what the Go admin bootstrap does so an operator can run
-- the same steps by hand (via psql as the postgres superuser) if needed.
-- Substitute <ROLE>, <PASSWORD>, and <DATABASE> with real values before running.
--
-- The preferred path is to let reddit-spy's startup code perform these steps
-- automatically when POSTGRES_ADMIN_URL is set. See internal/dbstore/bootstrap.go.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = '<ROLE>') THEN
        EXECUTE 'CREATE ROLE "<ROLE>" LOGIN PASSWORD ''<PASSWORD>''';
    ELSE
        EXECUTE 'ALTER ROLE "<ROLE>" WITH LOGIN PASSWORD ''<PASSWORD>''';
    END IF;
END
$$;

-- CREATE DATABASE cannot run inside a transaction block or DO block, so run it
-- separately. Safe to re-run because of the WHERE NOT EXISTS guard.
SELECT 'CREATE DATABASE "<DATABASE>" OWNER "<ROLE>"'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '<DATABASE>')
\gexec
