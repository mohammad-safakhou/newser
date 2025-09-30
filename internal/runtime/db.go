package runtime

import (
	"fmt"

	"github.com/mohammad-safakhou/newser/config"
)

// BuildPostgresDSN constructs a DSN from the application configuration.
func BuildPostgresDSN(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	p := cfg.Storage.Postgres
	if p.URL != "" {
		return p.URL, nil
	}
	if p.Host == "" || p.DBName == "" {
		return "", fmt.Errorf("postgres configuration incomplete: host/dbname required")
	}
	port := p.Port
	if port == "" {
		port = "5432"
	}
	ssl := p.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", p.User, p.Password, p.Host, port, p.DBName, ssl), nil
}
