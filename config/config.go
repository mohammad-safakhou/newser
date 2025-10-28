package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the agent system
type Config struct {
	General        GeneralConfig        `mapstructure:"general"`
	Server         ServerConfig         `mapstructure:"server"`
	LLM            LLMConfig            `mapstructure:"llm"`
	Telemetry      TelemetryConfig      `mapstructure:"telemetry"`
	Capability     CapabilityConfig     `mapstructure:"capability"`
	Agents         AgentsConfig         `mapstructure:"agents"`
	Sources        SourcesConfig        `mapstructure:"sources"`
	Storage        StorageConfig        `mapstructure:"storage"`
	Memory         MemoryConfig         `mapstructure:"memory"`
	Security       SecurityConfig       `mapstructure:"security"`
	Explainability ExplainabilityConfig `mapstructure:"explainability"`
	Accessibility  AccessibilityConfig  `mapstructure:"accessibility"`
	Builder        BuilderConfig        `mapstructure:"builder"`
	Fairness       FairnessConfig       `mapstructure:"fairness"`
}

// ServerConfig contains HTTP server and auth settings
type ServerConfig struct {
	Address            string `mapstructure:"address"`
	JWTSecret          string `mapstructure:"jwt_secret"`
	PlanDryRunEnabled  bool   `mapstructure:"plan_dry_run_enabled"`
	PlanEstimateMode   string `mapstructure:"plan_estimate_mode"`
	RunManifestEnabled bool   `mapstructure:"run_manifest_enabled"`
	RunStreamEnabled   bool   `mapstructure:"run_stream_enabled"`
}

// GeneralConfig contains general application settings
type GeneralConfig struct {
	Debug             bool          `mapstructure:"debug"`
	LogLevel          string        `mapstructure:"log_level"`
	MaxProcessingTime time.Duration `mapstructure:"max_processing_time"`
	DefaultTimeout    time.Duration `mapstructure:"default_timeout"`
	JWTSecret         string        `mapstructure:"jwt_secret"` // JWT secret for auth
}

// LLMConfig contains LLM provider configurations
type LLMConfig struct {
	Providers map[string]LLMProvider `mapstructure:"providers"`
	Routing   LLMRoutingConfig       `mapstructure:"routing"`
}

// LLMProvider represents a single LLM provider configuration
type LLMProvider struct {
	Type       string              `mapstructure:"type"` // openai, anthropic, local, etc.
	APIKey     string              `mapstructure:"api_key"`
	BaseURL    string              `mapstructure:"base_url"`
	Models     map[string]LLMModel `mapstructure:"models"`
	MaxRetries int                 `mapstructure:"max_retries"`
	Timeout    time.Duration       `mapstructure:"timeout"`
}

// LLMModel represents a specific model configuration
type LLMModel struct {
	Name            string   `mapstructure:"name"`
	APIName         string   `mapstructure:"api_name"`
	MaxTokens       int      `mapstructure:"max_tokens"`
	Temperature     float64  `mapstructure:"temperature"`
	CostPer1K       float64  `mapstructure:"cost_per_1k_input"`
	CostPer1KOutput float64  `mapstructure:"cost_per_1k_output"`
	Capabilities    []string `mapstructure:"capabilities"` // planning, analysis, synthesis, etc.
}

// LLMRoutingConfig defines which model to use for different tasks
type LLMRoutingConfig struct {
	Planning  string `mapstructure:"planning"`  // Use for complex planning tasks
	Chatting  string `mapstructure:"chatting"`  // Use for complex chatting tasks
	Analysis  string `mapstructure:"analysis"`  // Use for content analysis
	Synthesis string `mapstructure:"synthesis"` // Use for content synthesis
	Research  string `mapstructure:"research"`  // Use for research tasks
	Fallback  string `mapstructure:"fallback"`  // Fallback model
}

// TelemetryConfig contains telemetry and monitoring settings
type TelemetryConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	MetricsPort  int    `mapstructure:"metrics_port"`
	LogFile      string `mapstructure:"log_file"`
	CostTracking bool   `mapstructure:"cost_tracking"`
	PeriodicLogs bool   `mapstructure:"periodic_logs"`
	OTLPEndpoint string `mapstructure:"otlp_endpoint"`
}

func (t TelemetryConfig) Validate() error {
	if t.Enabled && t.MetricsPort <= 0 {
		return fmt.Errorf("telemetry.metrics_port must be > 0 when telemetry is enabled")
	}
	return nil
}

// CapabilityConfig controls the ToolCard registry behaviour.
type CapabilityConfig struct {
	SigningSecret string   `mapstructure:"signing_secret"`
	RequiredTools []string `mapstructure:"required_tools"`
}

// SecurityConfig declares sandbox policy defaults.
type SecurityConfig struct {
	SandboxProvider string            `mapstructure:"sandbox_provider"`
	PolicyFile      string            `mapstructure:"policy_file"`
	DefaultTimeout  time.Duration     `mapstructure:"default_timeout"`
	DefaultCPU      float64           `mapstructure:"default_cpu"`
	DefaultMemory   string            `mapstructure:"default_memory"`
	CrawlPolicy     CrawlPolicyConfig `mapstructure:"crawl_policy"`
}

func (s SecurityConfig) Validate() error {
	if strings.TrimSpace(s.PolicyFile) == "" {
		return fmt.Errorf("security.policy_file is required")
	}
	if strings.TrimSpace(s.SandboxProvider) == "" {
		return fmt.Errorf("security.sandbox_provider is required")
	}
	if s.DefaultCPU <= 0 {
		return fmt.Errorf("security.default_cpu must be greater than zero")
	}
	if strings.TrimSpace(s.DefaultMemory) == "" {
		return fmt.Errorf("security.default_memory is required")
	}
	if s.DefaultTimeout <= 0 {
		s.DefaultTimeout = time.Minute
	}
	return nil
}

// ExplainabilityConfig controls how verbose evidence surfaces across the API/UI.
type ExplainabilityConfig struct {
	EnableEvidenceGraph bool `mapstructure:"enable_evidence_graph"`
	EnableHoverContext  bool `mapstructure:"enable_hover_context"`
	IncludeMemoryHits   bool `mapstructure:"include_memory_hits"`
	ShowPlannerTrace    bool `mapstructure:"show_planner_trace"`
	MaxTraceDepth       int  `mapstructure:"max_trace_depth"`
}

// Normalize applies defaults for unset explainability values.
func (c ExplainabilityConfig) Normalize() ExplainabilityConfig {
	if c.MaxTraceDepth <= 0 {
		c.MaxTraceDepth = 50
	}
	return c
}

// Validate ensures explainability settings are safe.
func (c ExplainabilityConfig) Validate() error {
	if c.MaxTraceDepth < 0 {
		return fmt.Errorf("explainability.max_trace_depth cannot be negative")
	}
	return nil
}

// AccessibilityConfig captures accessibility/i18n defaults for the WebUI.
type AccessibilityConfig struct {
	MinimumContrast   float64  `mapstructure:"minimum_contrast"`
	FocusRingClass    string   `mapstructure:"focus_ring_class"`
	DefaultLocale     string   `mapstructure:"default_locale"`
	SupportedLocales  []string `mapstructure:"supported_locales"`
	DefaultDateFormat string   `mapstructure:"default_date_format"`
	DefaultTimeFormat string   `mapstructure:"default_time_format"`
}

// Normalize applies sensible defaults when values are omitted.
func (c AccessibilityConfig) Normalize() AccessibilityConfig {
	if c.MinimumContrast <= 0 {
		c.MinimumContrast = 4.5
	}
	c.FocusRingClass = strings.TrimSpace(c.FocusRingClass)
	if c.FocusRingClass == "" {
		c.FocusRingClass = "focus:outline-blue-500 focus:ring-2 focus:ring-offset-2"
	}
	c.DefaultLocale = strings.TrimSpace(c.DefaultLocale)
	if c.DefaultLocale == "" {
		c.DefaultLocale = "en-US"
	}
	if len(c.SupportedLocales) == 0 {
		c.SupportedLocales = []string{c.DefaultLocale}
	} else {
		seen := make(map[string]struct{}, len(c.SupportedLocales))
		var dedup []string
		for _, locale := range c.SupportedLocales {
			locale = strings.TrimSpace(locale)
			if locale == "" {
				continue
			}
			if _, ok := seen[locale]; ok {
				continue
			}
			seen[locale] = struct{}{}
			dedup = append(dedup, locale)
		}
		if len(dedup) == 0 {
			dedup = []string{c.DefaultLocale}
		}
		c.SupportedLocales = dedup
		if _, ok := seen[c.DefaultLocale]; !ok {
			c.SupportedLocales = append(c.SupportedLocales, c.DefaultLocale)
		}
	}
	if strings.TrimSpace(c.DefaultDateFormat) == "" {
		c.DefaultDateFormat = "2006-01-02"
	}
	if strings.TrimSpace(c.DefaultTimeFormat) == "" {
		c.DefaultTimeFormat = "15:04"
	}
	return c
}

// Validate checks the accessibility configuration.
func (c AccessibilityConfig) Validate() error {
	if c.MinimumContrast < 1.0 {
		return fmt.Errorf("accessibility.minimum_contrast must be >= 1.0")
	}
	if strings.TrimSpace(c.DefaultLocale) == "" {
		return fmt.Errorf("accessibility.default_locale required")
	}
	return nil
}

// BuilderConfig defines conversational builder defaults.
type BuilderConfig struct {
	Defaults BuilderDefaultsConfig `mapstructure:"defaults"`
}

// BuilderDefaultsConfig captures default schemas per builder kind.
type BuilderDefaultsConfig struct {
	Topic     string `mapstructure:"topic"`
	Blueprint string `mapstructure:"blueprint"`
	View      string `mapstructure:"view"`
	Route     string `mapstructure:"route"`
}

// AgentsConfig contains agent-specific settings
type AgentsConfig struct {
	MaxConcurrentAgents int           `mapstructure:"max_concurrent_agents"`
	AgentTimeout        time.Duration `mapstructure:"agent_timeout"`
	MaxRetries          int           `mapstructure:"max_retries"`
	ConfidenceThreshold float64       `mapstructure:"confidence_threshold"`
}

// SourcesConfig contains news source configurations
type SourcesConfig struct {
	NewsAPI     NewsAPIConfig     `mapstructure:"newsapi"`
	WebSearch   WebSearchConfig   `mapstructure:"web_search"`
	SocialMedia SocialMediaConfig `mapstructure:"social_media"`
	Academic    AcademicConfig    `mapstructure:"academic"`
}

// NewsAPIConfig contains NewsAPI settings
type NewsAPIConfig struct {
	APIKey     string `mapstructure:"api_key"`
	Endpoint   string `mapstructure:"endpoint"`
	MaxResults int    `mapstructure:"max_results"`
}

// WebSearchConfig contains web search settings
type WebSearchConfig struct {
	BraveAPIKey  string        `mapstructure:"brave_api_key"`
	SerperAPIKey string        `mapstructure:"serper_api_key"`
	MaxResults   int           `mapstructure:"max_results"`
	Timeout      time.Duration `mapstructure:"timeout"`
}

// SocialMediaConfig contains social media monitoring settings
type SocialMediaConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Add specific social media configs as needed
}

// AcademicConfig contains academic paper search settings
type AcademicConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Add academic search configs as needed
}

// StorageConfig contains storage and persistence settings
type StorageConfig struct {
	Redis    RedisConfig    `mapstructure:"redis"`
	File     FileConfig     `mapstructure:"file"`
	Postgres PostgresConfig `mapstructure:"postgres"`
	S3       S3Config       `mapstructure:"s3"`
}

// MemoryConfig controls episodic/semantic memory behaviour.
type MemoryConfig struct {
	Episodic EpisodicMemoryConfig `mapstructure:"episodic"`
	Semantic SemanticMemoryConfig `mapstructure:"semantic"`
}

// EpisodicMemoryConfig defines retention settings for episodic traces.
type EpisodicMemoryConfig struct {
	Enabled       bool `mapstructure:"enabled"`
	RetentionDays int  `mapstructure:"retention_days"`
}

// SemanticMemoryConfig defines behaviour for semantic embedding storage and search.
type SemanticMemoryConfig struct {
	Enabled             bool          `mapstructure:"enabled"`
	EmbeddingModel      string        `mapstructure:"embedding_model"`
	EmbeddingDimensions int           `mapstructure:"embedding_dimensions"`
	SearchTopK          int           `mapstructure:"search_top_k"`
	SearchThreshold     float64       `mapstructure:"search_threshold"`
	WriterBatchSize     int           `mapstructure:"writer_batch_size"`
	RebuildOnStartup    bool          `mapstructure:"rebuild_on_startup"`
	DeltaThreshold      float64       `mapstructure:"delta_threshold"`
	DeltaWindow         time.Duration `mapstructure:"delta_window"`
	DeltaIncludeSteps   bool          `mapstructure:"delta_include_steps"`
	RetentionDays       int           `mapstructure:"retention_days"`
}

// FairnessConfig controls bias and credibility scoring adjustments.
type FairnessConfig struct {
	Enabled             bool               `mapstructure:"enabled"`
	BaselineCredibility float64            `mapstructure:"baseline_credibility"`
	MinCredibility      float64            `mapstructure:"min_credibility"`
	BiasAdjustmentRange float64            `mapstructure:"bias_adjustment_range"`
	DomainBias          map[string]float64 `mapstructure:"domain_bias"`
	Alerts              FairnessAlerts     `mapstructure:"alerts"`
}

// FairnessAlerts defines alert thresholds for fairness metrics.
type FairnessAlerts struct {
	LowCredibility float64 `mapstructure:"low_credibility"`
	HighBias       float64 `mapstructure:"high_bias"`
}

// CrawlPolicyConfig configures domain-level crawling rules.
type CrawlPolicyConfig struct {
	RespectRobots bool              `mapstructure:"respect_robots" json:"respect_robots"`
	Allow         []string          `mapstructure:"allow" json:"allow"`
	Disallow      []string          `mapstructure:"disallow" json:"disallow"`
	Paywall       []string          `mapstructure:"paywall" json:"paywall"`
	Attribution   map[string]string `mapstructure:"attribution" json:"attribution"`
}

// RedisConfig contains Redis connection settings
type RedisConfig struct {
	Host     string        `mapstructure:"host"`
	Port     string        `mapstructure:"port"`
	Password string        `mapstructure:"password"`
	DB       int           `mapstructure:"db"`
	Timeout  time.Duration `mapstructure:"timeout"`
}

func (r RedisConfig) Validate() error {
	if strings.TrimSpace(r.Host) == "" {
		return fmt.Errorf("storage.redis.host required")
	}
	if strings.TrimSpace(r.Port) == "" {
		return fmt.Errorf("storage.redis.port required")
	}
	return nil
}

// FileConfig contains file storage settings
type FileConfig struct {
	DataDir string `mapstructure:"data_dir"`
	LogDir  string `mapstructure:"log_dir"`
}

// PostgresConfig contains Postgres connection settings
type PostgresConfig struct {
	URL      string        `mapstructure:"url"`
	Host     string        `mapstructure:"host"`
	Port     string        `mapstructure:"port"`
	User     string        `mapstructure:"user"`
	Password string        `mapstructure:"password"`
	DBName   string        `mapstructure:"dbname"`
	SSLMode  string        `mapstructure:"sslmode"`
	Timeout  time.Duration `mapstructure:"timeout"`
}

func (p PostgresConfig) Validate() error {
	if strings.TrimSpace(p.URL) != "" {
		return nil
	}
	if strings.TrimSpace(p.Host) == "" {
		return fmt.Errorf("storage.postgres.host required when url is not provided")
	}
	if strings.TrimSpace(p.Port) == "" {
		return fmt.Errorf("storage.postgres.port required when url is not provided")
	}
	if strings.TrimSpace(p.DBName) == "" {
		return fmt.Errorf("storage.postgres.dbname required when url is not provided")
	}
	return nil
}

// S3Config contains object storage configuration.
type S3Config struct {
	Endpoint        string `mapstructure:"endpoint"`
	Region          string `mapstructure:"region"`
	Bucket          string `mapstructure:"bucket"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
}

func (s S3Config) Validate() error {
	if strings.TrimSpace(s.Endpoint) == "" && strings.TrimSpace(s.Bucket) == "" {
		return nil
	}
	if strings.TrimSpace(s.Bucket) == "" {
		return fmt.Errorf("storage.s3.bucket required when endpoint is provided")
	}
	return nil
}

// LoadConfig loads config from file
func LoadConfig(path string) *Config {
	viper.SetConfigName("config") // name of config file (without extension)
	viper.SetConfigType("json")   // REQUIRED if the config file does not have the extension in the name
	viper.SetDefault("server.plan_dry_run_enabled", true)
	viper.SetDefault("server.plan_estimate_mode", "auto")
	viper.SetDefault("server.run_manifest_enabled", true)
	viper.SetDefault("server.run_stream_enabled", true)
	viper.SetDefault("builder.defaults.topic", DefaultTopicSchema)
	viper.SetDefault("builder.defaults.blueprint", DefaultBlueprintSchema)
	viper.SetDefault("builder.defaults.view", DefaultViewSchema)
	viper.SetDefault("builder.defaults.route", DefaultRouteSchema)
	viper.SetDefault("fairness.enabled", false)
	viper.SetDefault("fairness.baseline_credibility", 0.6)
	viper.SetDefault("fairness.min_credibility", 0.3)
	viper.SetDefault("fairness.bias_adjustment_range", 0.25)
	viper.SetDefault("fairness.alerts.low_credibility", 0.2)
	viper.SetDefault("fairness.alerts.high_bias", 0.6)

	if path == "" {
		viper.AddConfigPath("./app/config") // path to look for the config file in
		viper.AddConfigPath("./config")     // path to look for the config file in
		viper.AddConfigPath(".")            // optionally look for config in the working directory
		exe, _ := os.Executable()
		exeDir := filepath.Dir(exe)
		viper.AddConfigPath(exeDir)                                // bin/
		viper.AddConfigPath(filepath.Join(exeDir, ".."))           // repo root
		viper.AddConfigPath(filepath.Join(exeDir, "..", "config")) // repo root/config
	} else {
		viper.SetConfigFile(path)
	}

	viper.SetEnvPrefix("NEWSER")
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)

	viper.AutomaticEnv() // read in environment variables that match (NEWSER_*)

	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		panic(fmt.Errorf("fatal error config file: %w", err))
	}

	// unmarshal config
	var config Config

	if err = viper.Unmarshal(&config); err != nil {
		panic(fmt.Errorf("fatal error config file: %w", err))
	}
	config.Fairness = config.Fairness.Normalize()
	config.Explainability = config.Explainability.Normalize()
	config.Accessibility = config.Accessibility.Normalize()

	if err := config.Telemetry.Validate(); err != nil {
		panic(err)
	}
	if err := config.Security.Validate(); err != nil {
		panic(err)
	}
	if err := config.Storage.Redis.Validate(); err != nil {
		panic(err)
	}
	if err := config.Storage.Postgres.Validate(); err != nil {
		panic(err)
	}
	if err := config.Storage.S3.Validate(); err != nil {
		panic(err)
	}
	if err := config.Explainability.Validate(); err != nil {
		panic(err)
	}
	if err := config.Accessibility.Validate(); err != nil {
		panic(err)
	}
	return &config
}
