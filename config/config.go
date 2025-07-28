package config

import (
	"fmt"
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
}

type Databases struct {
	Redis Redis `mapstructure:"redis"` // redis configs
}

type Redis struct {
	Host    string        `mapstructure:"host"`    // redis host
	Port    string        `mapstructure:"port"`    // redis port
	Pass    string        `mapstructure:"pass"`    // redis pass
	DB      int           `mapstructure:"db"`      // redis db
	Timeout time.Duration `mapstructure:"timeout"` // redis timeout
}

type Providers struct {
	OpenAi OpenAi `mapstructure:"openai"` // OpenAI provider configs
}

type OpenAi struct {
	APIKey      string        `mapstructure:"api_key"`     // OpenAI API key
	Model       string        `mapstructure:"model"`       // OpenAI model to use
	Temperature float64       `mapstructure:"temperature"` // OpenAI temperature setting
	MaxTokens   int           `mapstructure:"max_tokens"`  // OpenAI max tokens setting
	Timeout     time.Duration `mapstructure:"timeout"`     // OpenAI request timeout
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
	} else {
		viper.SetConfigFile(path)
	}

	viper.SetEnvPrefix("NEWSER")
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)

	viper.AutomaticEnv() // read in environment variables that match

	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		panic(fmt.Errorf("fatal error config file: %w", err))
	}

	AppConfig = &config{}
	if err = viper.Unmarshal(&AppConfig); err != nil {
		panic(fmt.Errorf("fatal error config file: %w", err))
	}
}
