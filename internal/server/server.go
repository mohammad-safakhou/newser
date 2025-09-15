package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	agenttele "github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	"github.com/mohammad-safakhou/newser/internal/store"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

func Run(cfg *config.Config) error {
	// Initialize shared dependencies (top-level DI)
	ctx := context.Background()
	// Build Postgres DSN from AppConfig
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
	st, err := store.NewWithDSN(ctx, dsn)
	if err != nil {
		return err
	}
	// Agent config + telemetry + orchestrator (single instance)
	tele := agenttele.NewTelemetry(cfg.Telemetry)
	orchLogger := log.New(log.Writer(), "[ORCH] ", log.LstdFlags)
	orch, err := agentcore.NewOrchestrator(cfg, orchLogger, tele)
	if err != nil {
		return err
	}
	// LLM provider for topic chat using internal core factory
	llmProvider, err := agentcore.NewLLMProvider(cfg.LLM)
	if err != nil {
		return err
	}

	// Echo instance
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	// Unified HTTP error handler with structured JSON and logging
	baseLogger := log.New(log.Writer(), "[HTTP] ", log.LstdFlags)
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		code := http.StatusInternalServerError
		msg := err.Error()
		var he *echo.HTTPError
		if errors.As(err, &he) {
			code = he.Code
			if he.Message != nil {
				msg = fmt.Sprint(he.Message)
			}
		}
		req := c.Request()
		baseLogger.Printf("%d %s %s from %s: %v", code, req.Method, req.URL.Path, c.RealIP(), err)
		if !c.Response().Committed {
			_ = c.JSON(code, map[string]interface{}{"error": msg})
		}
	}
	allowed := os.Getenv("CORS_ALLOW_ORIGINS")
	if allowed == "" {
		allowed = "http://localhost:3000"
	}
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     strings.Split(allowed, ","),
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Cookie", "Authorization"},
		AllowCredentials: true,
	}))

	e.GET("/healthz", func(c echo.Context) error { return c.String(200, "ok") })
	registerDocs(e)
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	// init auth and routes
	secret := cfg.Server.JWTSecret
	if secret == "" {
		return fmt.Errorf("jwt secret not configured (general.jwt_secret)")
	}
	auth, err := initAuth(ctx, st, []byte(secret))
	if err != nil {
		return err
	}

	api := e.Group("/api")
	auth.Register(api.Group("/auth"))

	// protected group example
	me := api.Group("/me")
	me.Use(func(next echo.HandlerFunc) echo.HandlerFunc { return withAuth(next, auth.Secret) })
	me.GET("", meHandler)

	th := &TopicsHandler{Store: auth.Store, LLM: llmProvider, Model: cfg.LLM.Routing.Chatting}
	th.Register(api.Group("/topics"), auth.Secret)

	rh := NewRunsHandler(cfg, auth.Store, orch)
	rh.Register(api.Group("/topics"), auth.Secret)

	// ops endpoints (authenticated)
	oh := NewOpsHandler(orch)
	oh.Register(api.Group("/ops", func(next echo.HandlerFunc) echo.HandlerFunc { return withAuth(next, auth.Secret) }))

	// start scheduler (with redis for locks) using AppConfig
	var rdb *redis.Client
	if cfg.Storage.Redis.Host == "" || cfg.Storage.Redis.Port == "" {
		return fmt.Errorf("redis not configured (databases.redis.host/port)")
	}
	rdb = redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", cfg.Storage.Redis.Host, cfg.Storage.Redis.Port), Password: cfg.Storage.Redis.Password, DB: cfg.Storage.Redis.DB})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return fmt.Errorf("redis connection failed (%s:%s): %w", cfg.Storage.Redis.Host, cfg.Storage.Redis.Port, err)
	}
	sched := NewScheduler(cfg, auth.Store, rdb, orch)
	sched.Start()

	// Note: Web UI is served by a separate container; backend only exposes APIs

	if cfg.Server.Address == "" {
		cfg.Server.Address = ":10001"
	}
	log.Printf("listening on %s", cfg.Server.Address)

	serverErr := make(chan error, 1)
	go func() {
		if err := e.Start(cfg.Server.Address); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("received signal %s, initiating shutdown", sig)
	case err := <-serverErr:
		return fmt.Errorf("http server error: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	return nil
}

func toInt(s string) int { var n int; fmt.Sscanf(s, "%d", &n); return n }

// meHandler returns the authenticated user id.
//
//	@Summary	Current user
//	@Tags		auth
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Produce	json
//	@Success	200	{object}	MeResponse
//	@Router		/api/me [get]
func meHandler(c echo.Context) error {
	return c.JSON(200, map[string]string{"user_id": c.Get("user_id").(string)})
}
