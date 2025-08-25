package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the agent system
type Config struct {
	General   GeneralConfig   `mapstructure:"general"`
	LLM       LLMConfig       `mapstructure:"llm"`
	Telemetry TelemetryConfig `mapstructure:"telemetry"`
	Agents    AgentsConfig    `mapstructure:"agents"`
	Sources   SourcesConfig   `mapstructure:"sources"`
	Storage   StorageConfig   `mapstructure:"storage"`
}

// GeneralConfig contains general application settings
type GeneralConfig struct {
	Debug             bool          `mapstructure:"debug"`
	LogLevel          string        `mapstructure:"log_level"`
	MaxProcessingTime time.Duration `mapstructure:"max_processing_time"`
	DefaultTimeout    time.Duration `mapstructure:"default_timeout"`
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
	Redis RedisConfig `mapstructure:"redis"`
	File  FileConfig  `mapstructure:"file"`
    Postgres PostgresConfig `mapstructure:"postgres"`
}

// RedisConfig contains Redis connection settings
type RedisConfig struct {
	Host     string        `mapstructure:"host"`
	Port     int           `mapstructure:"port"`
	Password string        `mapstructure:"password"`
	DB       int           `mapstructure:"db"`
	Timeout  time.Duration `mapstructure:"timeout"`
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
    Port     int           `mapstructure:"port"`
    User     string        `mapstructure:"user"`
    Password string        `mapstructure:"password"`
    DBName   string        `mapstructure:"dbname"`
    SSLMode  string        `mapstructure:"sslmode"`
    Timeout  time.Duration `mapstructure:"timeout"`
}

// LoadConfig loads configuration from file and environment variables
func LoadConfig() (*Config, error) {
	viper.SetConfigName("agent_config")
	viper.SetConfigType("json")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	// Set environment variable prefix
	viper.SetEnvPrefix("NEWSER_AGENT")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Set defaults
	setDefaults()

	// Read config file (optional - will use defaults if not found)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Override with environment variables for sensitive data
	overrideFromEnv()

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Validate configuration
	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &config, nil
}

// setDefaults sets default configuration values
func setDefaults() {
	// General defaults
	viper.SetDefault("general.debug", false)
	viper.SetDefault("general.log_level", "info")
	viper.SetDefault("general.max_processing_time", "10m")
	viper.SetDefault("general.default_timeout", "30s")

	// LLM defaults (only gpt-5 family)
	viper.SetDefault("llm.routing.planning", "gpt-5")
	viper.SetDefault("llm.routing.analysis", "gpt-5")
	viper.SetDefault("llm.routing.synthesis", "gpt-5")
	viper.SetDefault("llm.routing.research", "gpt-5")
	viper.SetDefault("llm.routing.fallback", "gpt-5-nano")

	// Agent defaults
	viper.SetDefault("agents.max_concurrent_agents", 5)
	viper.SetDefault("agents.agent_timeout", "2m")
	viper.SetDefault("agents.max_retries", 3)
	viper.SetDefault("agents.confidence_threshold", 0.7)

	// Telemetry defaults
	viper.SetDefault("telemetry.enabled", true)
	viper.SetDefault("telemetry.metrics_port", 9090)
	viper.SetDefault("telemetry.cost_tracking", true)
	viper.SetDefault("telemetry.periodic_logs", false)

	// Sources defaults
	viper.SetDefault("sources.newsapi.max_results", 50)
	viper.SetDefault("sources.web_search.max_results", 20)
	viper.SetDefault("sources.web_search.timeout", "30s")

	// Storage defaults
	viper.SetDefault("storage.redis.host", "localhost")
	viper.SetDefault("storage.redis.port", 6379)
	viper.SetDefault("storage.redis.db", 0)
	viper.SetDefault("storage.redis.timeout", "5s")
	viper.SetDefault("storage.file.data_dir", "./data")
	viper.SetDefault("storage.file.log_dir", "./logs")
    viper.SetDefault("storage.postgres.host", "")
    viper.SetDefault("storage.postgres.port", 5432)
    viper.SetDefault("storage.postgres.user", "")
    viper.SetDefault("storage.postgres.password", "")
    viper.SetDefault("storage.postgres.dbname", "")
    viper.SetDefault("storage.postgres.sslmode", "disable")
    viper.SetDefault("storage.postgres.timeout", "5s")
}

// overrideFromEnv overrides configuration with environment variables
func overrideFromEnv() {
	// LLM API keys
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		viper.Set("llm.providers.openai.api_key", apiKey)
	}
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		viper.Set("llm.providers.anthropic.api_key", apiKey)
	}

	// News sources API keys
	if apiKey := os.Getenv("NEWSAPI_API_KEY"); apiKey != "" {
		viper.Set("sources.newsapi.api_key", apiKey)
	}
	if apiKey := os.Getenv("BRAVE_SEARCH_KEY"); apiKey != "" {
		viper.Set("sources.web_search.brave_api_key", apiKey)
	}
	if apiKey := os.Getenv("SERPER_API_KEY"); apiKey != "" {
		viper.Set("sources.web_search.serper_api_key", apiKey)
	}

	// Redis configuration
	if host := os.Getenv("REDIS_HOST"); host != "" {
		viper.Set("storage.redis.host", host)
	}
	if port := os.Getenv("REDIS_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			viper.Set("storage.redis.port", p)
		}
	}
	if password := os.Getenv("REDIS_PASSWORD"); password != "" {
		viper.Set("storage.redis.password", password)
	}
    // Postgres configuration (single block)
    if url := os.Getenv("DATABASE_URL"); url != "" {
        viper.Set("storage.postgres.url", url)
    }
    if host := os.Getenv("POSTGRES_HOST"); host != "" {
        viper.Set("storage.postgres.host", host)
    }
    if port := os.Getenv("POSTGRES_PORT"); port != "" {
        if p, err := strconv.Atoi(port); err == nil {
            viper.Set("storage.postgres.port", p)
        }
    }
    if user := os.Getenv("POSTGRES_USER"); user != "" {
        viper.Set("storage.postgres.user", user)
    }
    if pass := os.Getenv("POSTGRES_PASSWORD"); pass != "" {
        viper.Set("storage.postgres.password", pass)
    }
    if db := os.Getenv("POSTGRES_DB"); db != "" {
        viper.Set("storage.postgres.dbname", db)
    }
}

// validateConfig validates the configuration
func validateConfig(config *Config) error {
	// Validate that at least one LLM provider is configured
	if len(config.LLM.Providers) == 0 {
		return fmt.Errorf("at least one LLM provider must be configured")
	}

	// Validate that routing models exist in providers
	routingModels := []string{
		config.LLM.Routing.Planning,
		config.LLM.Routing.Analysis,
		config.LLM.Routing.Synthesis,
		config.LLM.Routing.Research,
		config.LLM.Routing.Fallback,
	}

	for _, model := range routingModels {
		if model == "" {
			continue
		}
		found := false
		for _, provider := range config.LLM.Providers {
			for _, providerModel := range provider.Models {
				if providerModel.Name == model {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("routing model '%s' not found in any provider", model)
		}
	}

	return nil
}
