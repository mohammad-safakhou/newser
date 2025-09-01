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
	General   GeneralConfig   `mapstructure:"general"`
	Server    ServerConfig    `mapstructure:"server"`
	LLM       LLMConfig       `mapstructure:"llm"`
	Telemetry TelemetryConfig `mapstructure:"telemetry"`
	Agents    AgentsConfig    `mapstructure:"agents"`
	Sources   SourcesConfig   `mapstructure:"sources"`
	Storage   StorageConfig   `mapstructure:"storage"`
}

// ServerConfig contains HTTP server and auth settings
type ServerConfig struct {
	Address   string `mapstructure:"address"`
	JWTSecret string `mapstructure:"jwt_secret"`
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
	Redis    RedisConfig    `mapstructure:"redis"`
	File     FileConfig     `mapstructure:"file"`
	Postgres PostgresConfig `mapstructure:"postgres"`
}

// RedisConfig contains Redis connection settings
type RedisConfig struct {
	Host     string        `mapstructure:"host"`
	Port     string        `mapstructure:"port"`
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
	Port     string        `mapstructure:"port"`
	User     string        `mapstructure:"user"`
	Password string        `mapstructure:"password"`
	DBName   string        `mapstructure:"dbname"`
	SSLMode  string        `mapstructure:"sslmode"`
	Timeout  time.Duration `mapstructure:"timeout"`
}

// LoadConfig loads config from file
func LoadConfig(path string) *Config {
	viper.SetConfigName("config") // name of config file (without extension)
	viper.SetConfigType("json")   // REQUIRED if the config file does not have the extension in the name

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
	return &config
}
