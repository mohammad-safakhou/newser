package server

import (
    "strings"

    "github.com/spf13/viper"
)

type ServerConfig struct {
    Address   string       `mapstructure:"address"`
    JWTSecret string       `mapstructure:"jwt_secret"`
    Redis     RedisConfig  `mapstructure:"redis"`
}

type RedisConfig struct {
    Host     string `mapstructure:"host"`
    Port     string `mapstructure:"port"`
    Password string `mapstructure:"password"`
}

func loadServerConfig() *ServerConfig {
    v := viper.New()
    v.SetConfigName("server")
    v.SetConfigType("json")
    v.AddConfigPath("./config")
    v.AddConfigPath(".")
    v.SetEnvPrefix("NEWSER")
    v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
    v.AutomaticEnv()

    v.SetDefault("address", ":10001")
    v.SetDefault("jwt_secret", "dev-secret-change-me")
    v.SetDefault("redis.port", "6379")

    // Back-compat for NEWSER_HTTP_ADDR
    if addr := v.GetString("http_addr"); addr != "" {
        v.Set("address", addr)
    }

    _ = v.ReadInConfig()

    var cfg ServerConfig
    _ = v.Unmarshal(&cfg)
    return &cfg
}


