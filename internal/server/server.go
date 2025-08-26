package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	agentcfg "github.com/mohammad-safakhou/newser/internal/agent/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	agenttele "github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	"github.com/mohammad-safakhou/newser/internal/store"
	"github.com/mohammad-safakhou/newser/provider"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

func Run(addr string) error {
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	// Unified HTTP error handler with structured JSON and logging
	baseLogger := log.New(log.Writer(), "[HTTP] ", log.LstdFlags)
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		code := http.StatusInternalServerError
		msg := err.Error()
		if he, ok := err.(*echo.HTTPError); ok {
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
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Cookie"},
		AllowCredentials: true,
	}))

	e.GET("/healthz", func(c echo.Context) error { return c.String(200, "ok") })
	registerDocs(e)
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	// load server config and agent config to consolidate settings
	srvCfg := loadServerConfig()
	if cfg, err := agentcfg.LoadConfig(); err == nil {
		// Prefer agent storage.postgres if envs are not set
		if os.Getenv("DATABASE_URL") == "" && (cfg.Storage.Postgres.URL != "" || cfg.Storage.Postgres.Host != "") {
			if cfg.Storage.Postgres.URL != "" {
				os.Setenv("DATABASE_URL", cfg.Storage.Postgres.URL)
			} else {
				if cfg.Storage.Postgres.Host != "" {
					os.Setenv("POSTGRES_HOST", cfg.Storage.Postgres.Host)
				}
				if cfg.Storage.Postgres.Port != 0 {
					os.Setenv("POSTGRES_PORT", fmt.Sprintf("%d", cfg.Storage.Postgres.Port))
				}
				if cfg.Storage.Postgres.User != "" {
					os.Setenv("POSTGRES_USER", cfg.Storage.Postgres.User)
				}
				if cfg.Storage.Postgres.Password != "" {
					os.Setenv("POSTGRES_PASSWORD", cfg.Storage.Postgres.Password)
				}
				if cfg.Storage.Postgres.DBName != "" {
					os.Setenv("POSTGRES_DB", cfg.Storage.Postgres.DBName)
				}
				if cfg.Storage.Postgres.SSLMode != "" {
					os.Setenv("POSTGRES_SSLMODE", cfg.Storage.Postgres.SSLMode)
				}
			}
		}
		// Prefer agent storage.redis for scheduler locks if server config missing
		if srvCfg.Redis.Host == "" && cfg.Storage.Redis.Host != "" {
			srvCfg.Redis.Host = cfg.Storage.Redis.Host
		}
		if srvCfg.Redis.Port == "" && cfg.Storage.Redis.Port != 0 {
			srvCfg.Redis.Port = fmt.Sprintf("%d", cfg.Storage.Redis.Port)
		}
		if srvCfg.Redis.Password == "" && cfg.Storage.Redis.Password != "" {
			srvCfg.Redis.Password = cfg.Storage.Redis.Password
		}
	}
	_ = Migrate("file://migrations", "", "up", 0)

	// Initialize shared dependencies (top-level DI)
	ctx := context.Background()
	st, err := store.New(ctx)
	if err != nil {
		return err
	}
	// Agent config + telemetry + orchestrator (single instance)
	agCfg, err := agentcfg.LoadConfig()
	if err != nil {
		return err
	}
	tele := agenttele.NewTelemetry(agCfg.Telemetry)
	orchLogger := log.New(log.Writer(), "[ORCH] ", log.LstdFlags)
	orch, err := agentcore.NewOrchestrator(agCfg, orchLogger, tele)
	if err != nil {
		return err
	}
	// LLM provider for topic chat
	llm, err := provider.NewProvider(provider.OpenAI)
	if err != nil {
		return err
	}

	// init auth and routes
	auth, err := initAuth(ctx, st, []byte(srvCfg.JWTSecret))
	if err != nil {
		return err
	}

	api := e.Group("/api")
	auth.Register(api.Group("/auth"))

	// protected group example
	me := api.Group("/me")
	me.Use(func(next echo.HandlerFunc) echo.HandlerFunc { return withAuth(next, auth.Secret) })
	me.GET("", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"user_id": c.Get("user_id").(string)})
	})

	// topics endpoints
	th := &TopicsHandler{Store: auth.Store, LLM: llm}
	th.Register(api.Group("/topics"), auth.Secret)

	rh := &RunsHandler{Store: auth.Store, Orch: orch}
	rh.Register(api.Group("/topics"), auth.Secret)

	// start scheduler (with redis for locks if available)
	var rdb *redis.Client
	if host := srvCfg.Redis.Host; host != "" {
		rdb = redis.NewClient(&redis.Options{
			Addr:     host + ":" + srvCfg.Redis.Port,
			Password: srvCfg.Redis.Password,
			DB:       0,
		})
	}
	sched := &Scheduler{Store: auth.Store, Stop: make(chan struct{}), Rdb: rdb, Orch: orch}
	sched.Start()

	// Note: Web UI is served by a separate container; backend only exposes APIs

	if addr == "" {
		addr = srvCfg.Address
	}
	log.Printf("listening on %s", addr)
	return e.Start(addr)
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
