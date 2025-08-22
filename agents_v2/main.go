// master_tree.go
// Hierarchical, LLM-driven Master–Agents orchestration with MCP tools.
//
// Key ideas:
//   - One or more MCP agents expose tools (web.search.*, web.fetch.*, web.ingest, search.query, ...)
//   - A "MasterNode" uses an LLM to PLAN arbitrary actions (a DAG), EXECUTE them, VERIFY results,
//     and optionally SPAWN child masters for sub-goals. Results + observations flow upward.
//   - Model routing: root can use a large model; children may use lighter models based on heuristics.
//   - Robust stdio JSON-RPC client for MCP; structured logs; run summaries; optional final report.
//
// Build: go build -o master-tree
// Run (example):
//
//	MCP_SEARCH_CMD=./bin/newser-mcp MCP_FETCH_CMD=./bin/newser-mcp MCP_INGEST_CMD=./bin/newser-mcp \
//	OPENAI_API_KEY=sk-... MASTER_AUTONOMOUS=true ./master-tree
//
// Env knobs (non-exhaustive; see envOr* calls below):
//
//	MASTER_AUTONOMOUS=true|false
//	ROOT_MODEL, CHILD_MODEL, JUDGE_MODEL (OpenAI chat models)
//	ROOT_TEMP, CHILD_TEMP, JUDGE_TEMP
//	MCP_SEARCH_CMD, MCP_FETCH_CMD, MCP_INGEST_CMD (may be same binary)
//	TOOL_TIMEOUT_SEC, NODE_TIMEOUT_SEC, REPORT_MODEL
//	MAX_NODE_ACTIONS, MAX_NODE_DEPTH
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// ==========================================================
// Domain: Topic / Goal
// ==========================================================

type Topic struct {
	State         TopicState             `json:"state"`
	History       []string               `json:"history"`
	Title         string                 `json:"title"`
	Subtopics     []string               `json:"subtopics"`
	KeyConcepts   []string               `json:"key_concepts"`
	RelatedTopics []string               `json:"related_topics"`
	Preferences   map[string]interface{} `json:"preferences"`
	CronSpec      string                 `json:"cron_spec"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

type TopicState string

const (
	TopicStateInitial    TopicState = "initial"
	TopicStateInProgress TopicState = "in_progress"
	TopicStateCompleted  TopicState = "completed"
)

// ==========================================================
// Capabilities / Tools
// ==========================================================

const (
	CapWebSearch   = "web.search"
	CapWebFetch    = "web.fetch"
	CapWebIngest   = "web.ingest"
	CapSearchQuery = "search.query"
	CapEmbedding   = "embedding"
)

var CapabilityTools = map[string][]string{
	CapWebSearch:   {"web.search.brave", "web.search.serper"},
	CapWebFetch:    {"web.fetch.chromedp"},
	CapWebIngest:   {"web.ingest"},
	CapSearchQuery: {"search.query"},
	CapEmbedding:   {"embedding.embed_many"},
}

// ==========================================================
// Main: boot registry, root node, autonomous run
// ==========================================================

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	cfg := loadConfigFromEnv()
	ctx := context.Background()

	reg := NewAgentRegistry()
	if cfg.GeneralCmd != "" {
		if client, err := StartStdioMCP(ctx, cfg.GeneralCmd); err != nil {
			log.Fatalf("start search MCP: %v", err)
		} else {
			ag := &Agent{Name: "search-agent", Client: client, Tools: map[string]MCPTool{}, Can: map[string]bool{}}
			_ = primeAgent(ctx, ag)
			reg.Add(ag)
		}
	}
	if len(reg.All()) == 0 {
		log.Println("[warn] No MCP agents provided. Set MCP_*_CMD.")
	}

	repo := NewInMemoryRepo()
	sink, _ := NewFileRunSink("runs")
	stream := make(chan StreamItem, 1024)
	go func() {
		for s := range stream {
			log.Printf("[%s] %-6s node=%s url=%s title=%s note=%s err=%s", s.RunID, s.Stage, s.Node, s.URL, s.Title, s.Note, s.Err)
		}
	}()

	router := ModelRouter{RootModel: envOr("ROOT_MODEL", "gpt-5-2025-08-07"), ChildModel: envOr("CHILD_MODEL", "gpt-5-mini"), RootTemp: floatEnv("ROOT_TEMP", 0.7), ChildTemp: floatEnv("CHILD_TEMP", 0.5)}
	judge := SimpleJudge{Model: envOr("JUDGE_MODEL", ""), APIKey: envOr("OPENAI_API_KEY", ""), Temp: floatEnv("JUDGE_TEMP", 0.0)}

	root := &MasterNode{Name: "root", Depth: 0, Cfg: cfg, Agents: reg, Repo: repo, Sink: sink, Stream: stream, Router: router, Judge: judge, RunID: randomID()}

	// Example topic; in your app feed this from your scheduler/UI
	topic := Topic{State: TopicStateInitial, Title: envOr("GOAL_TITLE", "OpenAI o3 news"), Preferences: map[string]any{"recency_days": 14}}

	if strings.EqualFold(os.Getenv("MASTER_AUTONOMOUS"), "true") {
		ctx2, cancel := context.WithTimeout(ctx, time.Duration(intEnv("MASTER_TIMEOUT_SEC", 600))*time.Second)
		defer cancel()
		res, err := root.Run(ctx2, topic)
		if err != nil {
			log.Println("root error:", err)
		}
		log.Printf("final: run=%s actions=%d children=%d report=runs/%s_report.md", root.RunID, res.Actions, res.Children, root.RunID)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
}
