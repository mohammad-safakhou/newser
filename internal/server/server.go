package server

import (
    "context"
    "fmt"
    "log"
    "net/http"

    "github.com/labstack/echo/v4"
    "github.com/labstack/echo/v4/middleware"
    appconfig "github.com/mohammad-safakhou/newser/config"
    agentcfg "github.com/mohammad-safakhou/newser/internal/agent/config"
    agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
    agenttele "github.com/mohammad-safakhou/newser/internal/agent/telemetry"
    "github.com/mohammad-safakhou/newser/internal/store"
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

    // Unified config (single source of truth)
    appconfig.LoadConfig("", false)
	_ = Migrate("file://migrations", "", "up", 0)

        // Initialize shared dependencies (top-level DI)
        ctx := context.Background()
        // Build Postgres DSN from AppConfig
        var dsn string
        if appconfig.AppConfig.Databases.Postgres.URL != "" {
            dsn = appconfig.AppConfig.Databases.Postgres.URL
        } else {
            host := appconfig.AppConfig.Databases.Postgres.Host
            port := appconfig.AppConfig.Databases.Postgres.Port
            user := appconfig.AppConfig.Databases.Postgres.User
            pass := appconfig.AppConfig.Databases.Postgres.Password
            db := appconfig.AppConfig.Databases.Postgres.DBName
            ssl := appconfig.AppConfig.Databases.Postgres.SSLMode
            if host == "" || db == "" { return fmt.Errorf("postgres not configured (databases.postgres.host/dbname or url)") }
            if port == "" { port = "5432" }
            if ssl == "" { ssl = "disable" }
            dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, db, ssl)
        }
	st, err := store.NewWithDSN(ctx, dsn)
	if err != nil {
		return err
	}
    // Agent config + telemetry + orchestrator (single instance)
    agCfg := buildAgentConfigFromApp()
    tele := agenttele.NewTelemetry(agCfg.Telemetry)
    orchLogger := log.New(log.Writer(), "[ORCH] ", log.LstdFlags)
    orch, err := agentcore.NewOrchestrator(agCfg, orchLogger, tele)
    if err != nil {
        return err
    }
    // LLM provider for topic chat using internal core factory
    llmProvider, err := agentcore.NewLLMProvider(agCfg.LLM)
    if err != nil {
        return err
    }

    // init auth and routes
    secret := appconfig.AppConfig.General.JWTSecret
    if secret == "" { return fmt.Errorf("jwt secret not configured (general.jwt_secret)") }
    auth, err := initAuth(ctx, st, []byte(secret))
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

    // topics endpoints backed by core LLM provider
    if appconfig.AppConfig.Providers.OpenAi.APIKey == "" {
        return fmt.Errorf("OPENAI_API_KEY not configured")
    }
    // choose a model from routing for chat/assist
    chatModel := agCfg.LLM.Routing.Analysis
    if chatModel == "" { chatModel = agCfg.LLM.Routing.Planning }
    if chatModel == "" { chatModel = agCfg.LLM.Routing.Fallback }
    th := &TopicsHandler{Store: auth.Store, LLM: llmProvider, Model: chatModel}
	th.Register(api.Group("/topics"), auth.Secret)

	rh := &RunsHandler{Store: auth.Store, Orch: orch}
	rh.Register(api.Group("/topics"), auth.Secret)

    // start scheduler (with redis for locks) using AppConfig
    var rdb *redis.Client
    if appconfig.AppConfig.Databases.Redis.Host == "" || appconfig.AppConfig.Databases.Redis.Port == "" {
        return fmt.Errorf("redis not configured (databases.redis.host/port)")
    }
    rdb = redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", appconfig.AppConfig.Databases.Redis.Host, appconfig.AppConfig.Databases.Redis.Port), Password: appconfig.AppConfig.Databases.Redis.Pass, DB: appconfig.AppConfig.Databases.Redis.DB})
    if err := rdb.Ping(context.Background()).Err(); err != nil {
        return fmt.Errorf("redis connection failed (%s:%s): %w", appconfig.AppConfig.Databases.Redis.Host, appconfig.AppConfig.Databases.Redis.Port, err)
    }
	sched := &Scheduler{Store: auth.Store, Stop: make(chan struct{}), Rdb: rdb, Orch: orch}
	sched.Start()

	// Note: Web UI is served by a separate container; backend only exposes APIs

    if addr == "" {
        addr = appconfig.AppConfig.General.Listen
        if addr != "" && addr[0] != ':' { addr = ":" + addr }
        if addr == "" { addr = ":10001" }
    }
	log.Printf("listening on %s", addr)
	return e.Start(addr)
}

// Helper to map AppConfig to agent config
func buildAgentConfigFromApp() *agentcfg.Config {
    cm := appconfig.AppConfig.Providers.OpenAi.CompletionModel
    if cm == "" { cm = "gpt-4o-mini" }
    models := map[string]agentcfg.LLMModel{
        cm: {
            Name:        cm,
            APIName:     cm,
            MaxTokens:   appconfig.AppConfig.Providers.OpenAi.MaxTokens,
            Temperature: appconfig.AppConfig.Providers.OpenAi.Temperature,
        },
    }
    return &agentcfg.Config{
        General: agentcfg.GeneralConfig{ Debug: false, LogLevel: "info" },
        Server:  agentcfg.ServerConfig{ Address: appconfig.AppConfig.General.Listen, JWTSecret: appconfig.AppConfig.General.JWTSecret },
        LLM: agentcfg.LLMConfig{
            Providers: map[string]agentcfg.LLMProvider{
                "openai": { Type: "openai", APIKey: appconfig.AppConfig.Providers.OpenAi.APIKey, Models: models, Timeout: appconfig.AppConfig.Providers.OpenAi.Timeout },
            },
            Routing: agentcfg.LLMRoutingConfig{ Planning: cm, Analysis: cm, Synthesis: cm, Research: cm, Fallback: cm },
        },
        Telemetry: agentcfg.TelemetryConfig{ Enabled: true, MetricsPort: 9090, CostTracking: true },
        Agents:    agentcfg.AgentsConfig{ MaxConcurrentAgents: 5, AgentTimeout: appconfig.AppConfig.Providers.OpenAi.Timeout, MaxRetries: 3, ConfidenceThreshold: 0.7 },
        Sources:   agentcfg.SourcesConfig{ NewsAPI: agentcfg.NewsAPIConfig{ APIKey: appconfig.AppConfig.NewsApi.APIKey, Endpoint: appconfig.AppConfig.NewsApi.Endpoint, MaxResults: 50 } },
        Storage: agentcfg.StorageConfig{
            Redis: agentcfg.RedisConfig{ Host: appconfig.AppConfig.Databases.Redis.Host, Port: toInt(appconfig.AppConfig.Databases.Redis.Port), Password: appconfig.AppConfig.Databases.Redis.Pass, DB: appconfig.AppConfig.Databases.Redis.DB, Timeout: appconfig.AppConfig.Databases.Redis.Timeout },
            Postgres: agentcfg.PostgresConfig{ URL: appconfig.AppConfig.Databases.Postgres.URL, Host: appconfig.AppConfig.Databases.Postgres.Host, Port: toInt(appconfig.AppConfig.Databases.Postgres.Port), User: appconfig.AppConfig.Databases.Postgres.User, Password: appconfig.AppConfig.Databases.Postgres.Password, DBName: appconfig.AppConfig.Databases.Postgres.DBName, SSLMode: appconfig.AppConfig.Databases.Postgres.SSLMode, Timeout: appconfig.AppConfig.Databases.Postgres.Timeout },
        },
    }
}

func toInt(s string) int { var n int; fmt.Sscanf(s, "%d", &n); return n }
