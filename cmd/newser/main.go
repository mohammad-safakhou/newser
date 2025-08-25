package main

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
    srv "github.com/mohammad-safakhou/newser/internal/server"
)

func main() {
    var root = &cobra.Command{Use: "newser"}

    var serveAddr string
    var serve = &cobra.Command{
        Use:   "serve",
        Short: "Run HTTP API server",
        RunE: func(cmd *cobra.Command, args []string) error {
            if serveAddr == "" { serveAddr = os.Getenv("NEWSER_HTTP_ADDR") }
            if serveAddr == "" { serveAddr = ":8080" }
            return srv.Run(serveAddr)
        },
    }
    serve.Flags().StringVar(&serveAddr, "addr", ":8080", "listen address")

    var migDir string
    var migDirDefault = "file://migrations"
    var direction string
    var steps int
    var migrate = &cobra.Command{
        Use:   "migrate",
        Short: "Run database migrations",
        RunE: func(cmd *cobra.Command, args []string) error {
            dsn := os.Getenv("DATABASE_URL")
            if dsn == "" {
                host := getenv("POSTGRES_HOST", "localhost")
                port := getenv("POSTGRES_PORT", "5432")
                user := os.Getenv("POSTGRES_USER")
                pass := os.Getenv("POSTGRES_PASSWORD")
                db := os.Getenv("POSTGRES_DB")
                ssl := getenv("POSTGRES_SSLMODE", "disable")
                dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, db, ssl)
            }
            if migDir == "" { migDir = migDirDefault }
            return srv.Migrate(migDir, dsn, direction, steps)
        },
    }
    migrate.Flags().StringVar(&migDir, "dir", migDirDefault, "migrations source (file://migrations)")
    migrate.Flags().StringVar(&direction, "direction", "up", "up or down")
    migrate.Flags().IntVar(&steps, "steps", 0, "number of steps (0 = all)")

    var worker = &cobra.Command{
        Use:   "worker",
        Short: "Run background scheduler",
        RunE: func(cmd *cobra.Command, args []string) error {
            // reuse server Run which starts scheduler by default for now
            addr := getenv("NEWSER_HTTP_ADDR", ":8080")
            return srv.Run(addr)
        },
    }

    root.AddCommand(serve, migrate, worker)
    _ = root.Execute()
}

func getenv(key, def string) string { v := os.Getenv(key); if v == "" { return def }; return v }


