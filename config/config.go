package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

var AppConfig *config // global app config

type config struct {
    General   General   `mapstructure:"general"`   // general configs
    Databases Databases `mapstructure:"databases"` // databases configs
    Providers Providers `mapstructure:"providers"` // providers configs
    NewsApi   NewsApi   `mapstructure:"newsapi"`   // newsapi configs
}

type General struct {
    Debug           bool          `mapstructure:"debug"`            // debug mode
    Listen          string        `mapstructure:"listen"`           // rest listen port
    Host            string        `mapstructure:"host"`             // rest host
    LogLevel        int8          `mapstructure:"log_level"`        // logger level
    ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"` // shutdown timeout
    AutoTLS         bool          `mapstructure:"auto_tls"`         // auto tls
    AppName         string        `mapstructure:"app_name"`         // application name
    JWTSecret       string        `mapstructure:"jwt_secret"`       // JWT secret for auth
}

type Databases struct {
    Redis Redis `mapstructure:"redis"` // redis configs
    Postgres Postgres `mapstructure:"postgres"` // postgres configs
}

type Redis struct {
    Host    string        `mapstructure:"host"`    // redis host
    Port    string        `mapstructure:"port"`    // redis port
    Pass    string        `mapstructure:"pass"`    // redis pass
    DB      int           `mapstructure:"db"`      // redis db
    Timeout time.Duration `mapstructure:"timeout"` // redis timeout
}

type Postgres struct {
    URL      string        `mapstructure:"url"`      // full DSN
    Host     string        `mapstructure:"host"`     // host
    Port     string        `mapstructure:"port"`     // port
    User     string        `mapstructure:"user"`     // user
    Password string        `mapstructure:"password"` // password
    DBName   string        `mapstructure:"dbname"`   // db name
    SSLMode  string        `mapstructure:"sslmode"`  // ssl mode
    Timeout  time.Duration `mapstructure:"timeout"`  // connect timeout
}

type Providers struct {
	OpenAi OpenAi `mapstructure:"openai"` // OpenAI provider configs
}

type OpenAi struct {
	APIKey          string        `mapstructure:"api_key"`          // OpenAI API key
	CompletionModel string        `mapstructure:"completion_model"` // OpenAI model to use
	EmbeddingModel  string        `mapstructure:"embedding_model"`  // OpenAI model to use
	Temperature     float64       `mapstructure:"temperature"`      // OpenAI temperature setting
	MaxTokens       int           `mapstructure:"max_tokens"`       // OpenAI max tokens setting
	Timeout         time.Duration `mapstructure:"timeout"`          // OpenAI request timeout
}

type NewsApi struct {
	APIKey   string `mapstructure:"api_key"` // NewsAPI API key
	Endpoint string `mapstructure:"endpoint"`
}

// LoadConfig loads config from file
func LoadConfig(path string, isTest bool) {
	if isTest {
		viper.SetConfigName("config_test") // name of test config file (without extension)
	} else {
		viper.SetConfigName("config") // name of config file (without extension)
	}
	viper.SetConfigType("json") // REQUIRED if the config file does not have the extension in the name

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

    // Map well-known non-prefixed env vars into config (single source for all consumers)
    if v := os.Getenv("OPENAI_API_KEY"); v != "" { viper.Set("providers.openai.api_key", v) }
    if v := os.Getenv("JWT_SECRET"); v != "" { viper.Set("general.jwt_secret", v) }
    if v := os.Getenv("NEWSER_HTTP_ADDR"); v != "" { viper.Set("general.listen", v) }
    // Postgres
    if v := os.Getenv("DATABASE_URL"); v != "" { viper.Set("databases.postgres.url", v) }
    if v := os.Getenv("POSTGRES_HOST"); v != "" { viper.Set("databases.postgres.host", v) }
    if v := os.Getenv("POSTGRES_PORT"); v != "" { viper.Set("databases.postgres.port", v) }
    if v := os.Getenv("POSTGRES_USER"); v != "" { viper.Set("databases.postgres.user", v) }
    if v := os.Getenv("POSTGRES_PASSWORD"); v != "" { viper.Set("databases.postgres.password", v) }
    if v := os.Getenv("POSTGRES_DB"); v != "" { viper.Set("databases.postgres.dbname", v) }
    if v := os.Getenv("POSTGRES_SSLMODE"); v != "" { viper.Set("databases.postgres.sslmode", v) }
    // Redis
    if v := os.Getenv("REDIS_HOST"); v != "" { viper.Set("databases.redis.host", v) }
    if v := os.Getenv("REDIS_PORT"); v != "" { viper.Set("databases.redis.port", v) }
    if v := os.Getenv("REDIS_PASSWORD"); v != "" { viper.Set("databases.redis.pass", v) }
    // NewsAPI
    if v := os.Getenv("NEWSAPI_API_KEY"); v != "" { viper.Set("newsapi.api_key", v) }

    AppConfig = &config{}
    if err = viper.Unmarshal(&AppConfig); err != nil {
        panic(fmt.Errorf("fatal error config file: %w", err))
    }
}
