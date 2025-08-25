package server

import (
    "fmt"
    "os"

    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/postgres"
    _ "github.com/golang-migrate/migrate/v4/source/file"
)

// Migrate applies database migrations from the given directory.
// dir example: file://migrations
func Migrate(dir string, dsn string, direction string, steps int) error {
    if dir == "" {
        dir = "file://migrations"
    }
    if dsn == "" {
        // try env
        dsn = os.Getenv("DATABASE_URL")
        if dsn == "" {
            host := getEnvDefault("POSTGRES_HOST", "localhost")
            port := getEnvDefault("POSTGRES_PORT", "5432")
            user := os.Getenv("POSTGRES_USER")
            pass := os.Getenv("POSTGRES_PASSWORD")
            db := os.Getenv("POSTGRES_DB")
            ssl := getEnvDefault("POSTGRES_SSLMODE", "disable")
            dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, db, ssl)
        }
    }

    m, err := migrate.New(dir, dsn)
    if err != nil { return err }
    switch direction {
    case "up":
        if steps > 0 {
            return m.Steps(steps)
        }
        return m.Up()
    case "down":
        if steps > 0 {
            return m.Steps(-steps)
        }
        return m.Down()
    default:
        return fmt.Errorf("unknown direction: %s", direction)
    }
}

func getEnvDefault(k, def string) string { v := os.Getenv(k); if v == "" { return def }; return v }


