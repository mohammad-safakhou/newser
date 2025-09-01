package main

import (
	"fmt"

	"github.com/mohammad-safakhou/newser/config"
	srv "github.com/mohammad-safakhou/newser/internal/server"
	"github.com/spf13/cobra"
)

func migrateCMD() *cobra.Command {
	var migDir string
	var migDirDefault = "file://migrations"
	var direction string
	var steps int
	var cfgPath string

	var migrate = &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.LoadConfig(cfgPath)
			var dsn string
			if cfg.Storage.Postgres.URL != "" {
				dsn = cfg.Storage.Postgres.URL
			} else {
				host := cfg.Storage.Postgres.Host
				port := cfg.Storage.Postgres.Port
				user := cfg.Storage.Postgres.User
				pass := cfg.Storage.Postgres.Password
				db := cfg.Storage.Postgres.DBName
				ssl := cfg.Storage.Postgres.SSLMode
				if host == "" || db == "" {
					return fmt.Errorf("postgres not configured (databases.postgres.host/dbname or url)")
				}
				if port == "" {
					port = "5432"
				}
				if ssl == "" {
					ssl = "disable"
				}
				dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, db, ssl)
			}
			if migDir == "" {
				migDir = migDirDefault
			}
			return srv.Migrate(migDir, dsn, direction, steps)
		},
	}
	migrate.Flags().StringVar(&migDir, "dir", migDirDefault, "migrations source (file://migrations)")
	migrate.Flags().StringVar(&direction, "direction", "up", "up or down")
	migrate.Flags().IntVar(&steps, "steps", 0, "number of steps (0 = all)")
	migrate.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "config file (default is .)")

	return migrate
}
