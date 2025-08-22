package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// ==========================================================
// Config / Env
// ==========================================================

const (
	DefaultToolTimeout = 60 * time.Second
	DefaultNodeTimeout = 180 * time.Second
	MaxJSONFrameBytes  = 1 << 20
)

type AppConfig struct {
	GeneralCmd     string
	ToolTimeout    time.Duration
	NodeTimeout    time.Duration
	MaxNodeActions int
	MaxNodeDepth   int
}

func loadConfigFromEnv() AppConfig {
	cfg := AppConfig{
		GeneralCmd:     strings.TrimSpace(os.Getenv("MCP_GENERAL_CMD")),
		ToolTimeout:    durEnv("TOOL_TIMEOUT_SEC", DefaultToolTimeout),
		NodeTimeout:    durEnv("NODE_TIMEOUT_SEC", DefaultNodeTimeout),
		MaxNodeActions: intEnv("MAX_NODE_ACTIONS", 20),
		MaxNodeDepth:   intEnv("MAX_NODE_DEPTH", 4),
	}
	return cfg
}

func durEnv(k string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := time.ParseDuration(v + "s"); err == nil {
			return n
		}
	}
	return def
}
func intEnv(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
func floatEnv(k string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
func envOr(k, def string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v != "" {
		return v
	}
	return def
}
