package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	// neturl "net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/mohammad-safakhou/newser/config"
	telemetrypkg "github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	"github.com/mohammad-safakhou/newser/internal/capability"
	"github.com/mohammad-safakhou/newser/internal/helpers"
)

// NewLLMProvider creates a new LLM provider based on configuration
func NewLLMProvider(cfg config.LLMConfig) (LLMProvider, error) {
	// For now, we'll create a simple OpenAI provider
	// In the future, this could support multiple providers
	if len(cfg.Providers) == 0 {
		return nil, fmt.Errorf("no LLM providers configured")
	}

	// Use the first configured provider
	for _, provider := range cfg.Providers {
		switch provider.Type {
		case "openai":
			return NewOpenAIProvider(provider), nil
		case "anthropic":
			return NewAnthropicProvider(provider), nil
		default:
			return nil, fmt.Errorf("unsupported LLM provider type: %s", provider.Type)
		}
	}

	return nil, fmt.Errorf("no valid LLM providers found")
}

// NewAgents creates all available agents
func NewAgents(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetrypkg.Telemetry, registry *capability.Registry) (map[string]Agent, error) {
	agentsMap := make(map[string]Agent)

	required := []struct {
		name    string
		factory func(*config.Config, LLMProvider, *telemetrypkg.Telemetry) Agent
	}{
		{"research", NewResearchAgent},
		{"analysis", NewAnalysisAgent},
		{"synthesis", NewSynthesisAgent},
		{"conflict_detection", NewConflictDetectionAgent},
		{"highlight_management", NewHighlightManagementAgent},
		{"knowledge_graph", NewKnowledgeGraphAgent},
	}
	for _, item := range required {
		if registry != nil {
			if _, ok := registry.Tool(item.name); !ok {
				return nil, fmt.Errorf("tool %s not registered", item.name)
			}
		}
		agentsMap[item.name] = item.factory(cfg, llmProvider, telemetry)
	}
	return agentsMap, nil
}

// NewSourceProviders creates all available source providers
func NewSourceProviders(cfg config.SourcesConfig) ([]SourceProvider, error) {
	httpc := NewHTTPClient(15*time.Second, 2, 300*time.Millisecond)
	var providers []SourceProvider
	if cfg.NewsAPI.APIKey != "" {
		providers = append(providers, &NewsAPIClient{cfg: cfg.NewsAPI, http: httpc})
	}
	if cfg.WebSearch.BraveAPIKey != "" {
		providers = append(providers, &BraveClient{cfg: cfg.WebSearch, http: httpc})
	}
	return providers, nil
}

// NewStorage creates a new storage instance
func NewStorage(cfg config.StorageConfig) (Storage, error) {
	// Prefer Postgres if configured (URL or host/dbname provided)
	// NOTE: config.Postgres may be zero-value; check key fields
	if cfg.Postgres.URL != "" || cfg.Postgres.Host != "" || cfg.Postgres.DBName != "" {
		ps, err := NewPostgresStorage(cfg.Postgres)
		if err == nil {
			return ps, nil
		}
		log.Printf("Warning: Postgres storage init failed: %v, falling back to Redis", err)
	}
	// Fallback to Redis-based storage
	return NewRedisStorage(cfg.Redis), nil
}

// OpenAIProvider implements LLMProvider for OpenAI
type OpenAIProvider struct {
	config    config.LLMProvider
	models    map[string]ModelInfo
	rawModels map[string]config.LLMModel
	client    *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(cfg config.LLMProvider) *OpenAIProvider {
	provider := &OpenAIProvider{
		config:    cfg,
		models:    make(map[string]ModelInfo),
		rawModels: cfg.Models,
		client:    &http.Client{Timeout: cfg.Timeout},
	}

	// Initialize model information
	for key, model := range cfg.Models {
		provider.models[key] = ModelInfo{
			Name:            model.Name,
			Provider:        "openai",
			MaxTokens:       model.MaxTokens,
			CostPer1KInput:  model.CostPer1K,
			CostPer1KOutput: model.CostPer1KOutput,
			Capabilities:    model.Capabilities,
			Description:     fmt.Sprintf("OpenAI %s model", model.Name),
		}
	}

	return provider
}

// Generate generates text using OpenAI
func (p *OpenAIProvider) Generate(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, error) {
	resp, _, _, err := p.GenerateWithTokens(ctx, prompt, model, options)
	return resp, err
}

// GenerateWithTokens generates text and returns token usage
func (p *OpenAIProvider) GenerateWithTokens(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, int64, int64, error) {
	apiKey := p.config.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return "", 0, 0, fmt.Errorf("OpenAI API key not configured")
	}

	m, ok := p.rawModels[model]
	if !ok {
		return "", 0, 0, fmt.Errorf("model %s not configured", model)
	}
	apiModel := m.APIName
	if apiModel == "" {
		apiModel = m.Name
	}

	temperature := m.Temperature
	if t, ok := options["temperature"].(float64); ok {
		temperature = t
	}
	maxTokens := m.MaxTokens
	if mt, ok := options["max_tokens"].(int); ok {
		maxTokens = mt
	}

	// Build request
	type chatMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type chatReq struct {
		Model       string    `json:"model"`
		Messages    []chatMsg `json:"messages"`
		Temperature float64   `json:"temperature,omitempty"`
		MaxTokens   int       `json:"max_completion_tokens,omitempty"`
	}

	body, err := json.Marshal(chatReq{
		Model: apiModel,
		Messages: []chatMsg{
			{Role: "user", Content: prompt},
		},
		Temperature: temperature,
		MaxTokens:   maxTokens,
	})
	if err != nil {
		return "", 0, 0, fmt.Errorf("marshal: %w", err)
	}

	baseURL := p.config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewBuffer(body))
	if err != nil {
		return "", 0, 0, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		var bodyResp []byte
		if resp != nil && resp.Body != nil {
			bodyResp, _ = helpers.ReadAllAndClose(resp.Body)
		}
		return "", 0, 0, fmt.Errorf("do: %w, %s", err, string(bodyResp))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyResp := []byte{}
		if resp.Body != nil {
			bodyResp, _ = helpers.ReadAllAndClose(resp.Body)
		}
		return "", 0, 0, fmt.Errorf("OpenAI status %d, %s", resp.StatusCode, string(bodyResp))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, 0, fmt.Errorf("decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", 0, 0, fmt.Errorf("no choices")
	}

	return out.Choices[0].Message.Content, int64(out.Usage.PromptTokens), int64(out.Usage.CompletionTokens), nil
}

// GetAvailableModels returns available models
func (p *OpenAIProvider) GetAvailableModels() []string {
	var models []string
	for name := range p.models {
		models = append(models, name)
	}
	return models
}

// GetModelInfo returns information about a specific model
func (p *OpenAIProvider) GetModelInfo(model string) (ModelInfo, error) {
	info, exists := p.models[model]
	if !exists {
		return ModelInfo{}, fmt.Errorf("model not found: %s", model)
	}
	return info, nil
}

// CalculateCost calculates the cost for a given number of tokens
func (p *OpenAIProvider) CalculateCost(inputTokens, outputTokens int64, model string) float64 {
	info, err := p.GetModelInfo(model)
	if err != nil {
		return 0.0
	}

	inputCost := float64(inputTokens) / 1000.0 * info.CostPer1KInput
	outputCost := float64(outputTokens) / 1000.0 * info.CostPer1KOutput
	return inputCost + outputCost
}

// AnthropicProvider implements LLMProvider for Anthropic
type AnthropicProvider struct {
	config config.LLMProvider
	models map[string]ModelInfo
}

// NewAnthropicProvider creates a new Anthropic provider
func NewAnthropicProvider(cfg config.LLMProvider) *AnthropicProvider {
	provider := &AnthropicProvider{
		config: cfg,
		models: make(map[string]ModelInfo),
	}

	// Initialize model information
	for name, model := range cfg.Models {
		provider.models[name] = ModelInfo{
			Name:            name,
			Provider:        "anthropic",
			MaxTokens:       model.MaxTokens,
			CostPer1KInput:  model.CostPer1K,
			CostPer1KOutput: model.CostPer1KOutput,
			Capabilities:    model.Capabilities,
			Description:     fmt.Sprintf("Anthropic %s model", name),
		}
	}

	return provider
}

// Generate generates text using Anthropic
func (p *AnthropicProvider) Generate(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, error) {
	// This is a placeholder implementation
	log.Printf("Anthropic Generate called with model: %s, prompt length: %d", model, len(prompt))

	// Simulate API call
	time.Sleep(100 * time.Millisecond)

	return fmt.Sprintf("Generated response for model %s: %s", model, prompt[:min(50, len(prompt))]), nil
}

// GenerateWithTokens generates text and returns token usage
func (p *AnthropicProvider) GenerateWithTokens(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, int64, int64, error) {
	response, err := p.Generate(ctx, prompt, model, options)
	if err != nil {
		return "", 0, 0, err
	}

	// Estimate token usage (rough approximation)
	inputTokens := int64(len(prompt) / 4)
	outputTokens := int64(len(response) / 4)

	return response, inputTokens, outputTokens, nil
}

// GetAvailableModels returns available models
func (p *AnthropicProvider) GetAvailableModels() []string {
	var models []string
	for name := range p.models {
		models = append(models, name)
	}
	return models
}

// GetModelInfo returns information about a specific model
func (p *AnthropicProvider) GetModelInfo(model string) (ModelInfo, error) {
	info, exists := p.models[model]
	if !exists {
		return ModelInfo{}, fmt.Errorf("model not found: %s", model)
	}
	return info, nil
}

// CalculateCost calculates the cost for a given number of tokens
func (p *AnthropicProvider) CalculateCost(inputTokens, outputTokens int64, model string) float64 {
	info, err := p.GetModelInfo(model)
	if err != nil {
		return 0.0
	}

	inputCost := float64(inputTokens) / 1000.0 * info.CostPer1KInput
	outputCost := float64(outputTokens) / 1000.0 * info.CostPer1KOutput
	return inputCost + outputCost
}

// RedisStorage implements Storage using Redis
type RedisStorage struct {
	config config.RedisConfig
}

// NewRedisStorage creates a new Redis storage instance
func NewRedisStorage(cfg config.RedisConfig) *RedisStorage {
	return &RedisStorage{
		config: cfg,
	}
}

// SaveProcessingResult saves a processing result
func (s *RedisStorage) SaveProcessingResult(ctx context.Context, result ProcessingResult) error {
	// This is a placeholder implementation
	log.Printf("Saving processing result: %s", result.ID)
	return nil
}

// GetProcessingResult retrieves a processing result
func (s *RedisStorage) GetProcessingResult(ctx context.Context, thoughtID string) (ProcessingResult, error) {
	// This is a placeholder implementation
	return ProcessingResult{}, fmt.Errorf("not implemented")
}

// SaveKnowledgeGraph saves a knowledge graph
func (s *RedisStorage) SaveKnowledgeGraph(ctx context.Context, graph KnowledgeGraph) error {
	// This is a placeholder implementation
	log.Printf("Saving knowledge graph: %s", graph.ID)
	return nil
}

// GetKnowledgeGraph retrieves a knowledge graph
func (s *RedisStorage) GetKnowledgeGraph(ctx context.Context, topic string) (KnowledgeGraph, error) {
	// This is a placeholder implementation
	return KnowledgeGraph{}, fmt.Errorf("not implemented")
}

// SaveHighlight saves a highlight
func (s *RedisStorage) SaveHighlight(ctx context.Context, highlight Highlight) error {
	// This is a placeholder implementation
	log.Printf("Saving highlight: %s", highlight.ID)
	return nil
}

// GetHighlights retrieves highlights for a topic
func (s *RedisStorage) GetHighlights(ctx context.Context, topic string) ([]Highlight, error) {
	// This is a placeholder implementation
	return []Highlight{}, nil
}

// UpdateHighlight updates a highlight
func (s *RedisStorage) UpdateHighlight(ctx context.Context, highlight Highlight) error {
	// This is a placeholder implementation
	log.Printf("Updating highlight: %s", highlight.ID)
	return nil
}

// DeleteHighlight deletes a highlight
func (s *RedisStorage) DeleteHighlight(ctx context.Context, highlightID string) error {
	// This is a placeholder implementation
	log.Printf("Deleting highlight: %s", highlightID)
	return nil
}

// PostgresStorage implements Storage using PostgreSQL
type PostgresStorage struct {
	db *sql.DB
}

func NewPostgresStorage(cfg config.PostgresConfig) (*PostgresStorage, error) {
	dsn := cfg.URL
	if dsn == "" {
		host := cfg.Host
		port := cfg.Port
		user := cfg.User
		pass := cfg.Password
		dbname := cfg.DBName
		ssl := cfg.SSLMode
		if host == "" {
			host = "localhost"
		}
		if port == "" {
			port = "5432"
		}
		if ssl == "" {
			ssl = "disable"
		}
		dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, dbname, ssl)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	ps := &PostgresStorage{db: db}
	if err := ps.ensureSchema(); err != nil {
		return nil, err
	}
	return ps, nil
}

func (s *PostgresStorage) ensureSchema() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS processing_results (
    id TEXT PRIMARY KEY,
    user_thought JSONB NOT NULL,
    summary TEXT,
    detailed_report TEXT,
    sources JSONB,
    highlights JSONB,
    conflicts JSONB,
    confidence DOUBLE PRECISION,
    processing_time BIGINT,
    cost_estimate DOUBLE PRECISION,
    tokens_used BIGINT,
    agents_used JSONB,
    llm_models_used JSONB,
    metadata JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
CREATE TABLE IF NOT EXISTS plan_graphs (
    plan_id TEXT PRIMARY KEY,
    thought_id TEXT NOT NULL,
    version TEXT NOT NULL,
    confidence DOUBLE PRECISION,
    execution_order TEXT[] DEFAULT ARRAY[]::TEXT[],
    budget JSONB,
    estimates JSONB,
    plan_json JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_plan_graphs_thought_id ON plan_graphs (thought_id);`)
	return err
}

func (s *PostgresStorage) SaveProcessingResult(ctx context.Context, result ProcessingResult) error {
	toJSON := func(v interface{}) ([]byte, error) { return json.Marshal(v) }
	userThought, _ := toJSON(result.UserThought)
	sources, _ := toJSON(result.Sources)
	highlights, _ := toJSON(result.Highlights)
	conflicts, _ := toJSON(result.Conflicts)
	agents, _ := toJSON(result.AgentsUsed)
	models, _ := toJSON(result.LLMModelsUsed)
	metadata, _ := toJSON(result.Metadata)

	_, err := s.db.ExecContext(ctx, `
INSERT INTO processing_results (
  id, user_thought, summary, detailed_report, sources, highlights, conflicts,
  confidence, processing_time, cost_estimate, tokens_used, agents_used, llm_models_used, metadata, created_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11, $12, $13, $14, NOW()
)
ON CONFLICT (id) DO UPDATE SET
  user_thought = EXCLUDED.user_thought,
  summary = EXCLUDED.summary,
  detailed_report = EXCLUDED.detailed_report,
  sources = EXCLUDED.sources,
  highlights = EXCLUDED.highlights,
  conflicts = EXCLUDED.conflicts,
  confidence = EXCLUDED.confidence,
  processing_time = EXCLUDED.processing_time,
  cost_estimate = EXCLUDED.cost_estimate,
  tokens_used = EXCLUDED.tokens_used,
  agents_used = EXCLUDED.agents_used,
  llm_models_used = EXCLUDED.llm_models_used,
  metadata = EXCLUDED.metadata;
`,
		result.ID, userThought, result.Summary, result.DetailedReport, sources, highlights, conflicts,
		result.Confidence, int64(result.ProcessingTime), result.CostEstimate, result.TokensUsed, agents, models, metadata,
	)
	return err
}

func (s *PostgresStorage) GetProcessingResult(ctx context.Context, thoughtID string) (ProcessingResult, error) {
	row := s.db.QueryRowContext(ctx, `SELECT user_thought, summary, detailed_report, sources, highlights, conflicts,
        confidence, processing_time, cost_estimate, tokens_used, agents_used, llm_models_used, metadata, created_at
        FROM processing_results WHERE id = $1`, thoughtID)

	var (
		userThoughtB, sourcesB, highlightsB, conflictsB, agentsB, modelsB, metadataB []byte
		res                                                                          ProcessingResult
		processingTime                                                               int64
	)
	res.ID = thoughtID
	if err := row.Scan(&userThoughtB, &res.Summary, &res.DetailedReport, &sourcesB, &highlightsB, &conflictsB,
		&res.Confidence, &processingTime, &res.CostEstimate, &res.TokensUsed, &agentsB, &modelsB, &metadataB, &res.CreatedAt); err != nil {
		return ProcessingResult{}, err
	}
	_ = json.Unmarshal(userThoughtB, &res.UserThought)
	_ = json.Unmarshal(sourcesB, &res.Sources)
	_ = json.Unmarshal(highlightsB, &res.Highlights)
	_ = json.Unmarshal(conflictsB, &res.Conflicts)
	_ = json.Unmarshal(agentsB, &res.AgentsUsed)
	_ = json.Unmarshal(modelsB, &res.LLMModelsUsed)
	if len(metadataB) > 0 {
		_ = json.Unmarshal(metadataB, &res.Metadata)
	}
	res.ProcessingTime = time.Duration(processingTime)
	return res, nil
}

func (s *PostgresStorage) GetKnowledgeGraph(ctx context.Context, topic string) (KnowledgeGraph, error) {
	var nodesB, edgesB, metaB []byte
	var kg KnowledgeGraph
	err := s.db.QueryRowContext(ctx, `SELECT id, nodes, edges, metadata, last_updated FROM knowledge_graphs WHERE topic=$1 ORDER BY last_updated DESC LIMIT 1`, topic).Scan(&kg.ID, &nodesB, &edgesB, &metaB, &kg.LastUpdated)
	if err != nil {
		return KnowledgeGraph{}, err
	}
	kg.Topic = topic
	_ = json.Unmarshal(nodesB, &kg.Nodes)
	_ = json.Unmarshal(edgesB, &kg.Edges)
	if len(metaB) > 0 {
		_ = json.Unmarshal(metaB, &kg.Metadata)
	}
	return kg, nil
}
func (s *PostgresStorage) SaveKnowledgeGraph(ctx context.Context, graph KnowledgeGraph) error {
	nodesB, _ := json.Marshal(graph.Nodes)
	edgesB, _ := json.Marshal(graph.Edges)
	metaB, _ := json.Marshal(graph.Metadata)
	_, err := s.db.ExecContext(ctx, `INSERT INTO knowledge_graphs (id, topic, nodes, edges, metadata, last_updated) VALUES ($1,$2,$3,$4,$5,NOW()) ON CONFLICT (id) DO UPDATE SET nodes=EXCLUDED.nodes, edges=EXCLUDED.edges, metadata=EXCLUDED.metadata, last_updated=NOW()`, graph.ID, graph.Topic, nodesB, edgesB, metaB)
	return err
}
func (s *PostgresStorage) SaveHighlight(ctx context.Context, h Highlight) error {
	sourcesB, _ := json.Marshal(h.Sources)
	_, err := s.db.ExecContext(ctx, `INSERT INTO highlights (id, topic, title, content, type, priority, sources, is_pinned, created_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,COALESCE($9,NOW()),$10) ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, content=EXCLUDED.content, type=EXCLUDED.type, priority=EXCLUDED.priority, sources=EXCLUDED.sources, is_pinned=EXCLUDED.is_pinned, expires_at=EXCLUDED.expires_at`, h.ID, graphTopicFromSources(h.Sources), h.Title, h.Content, h.Type, h.Priority, sourcesB, h.IsPinned, h.CreatedAt, h.ExpiresAt)
	return err
}
func (s *PostgresStorage) GetHighlights(ctx context.Context, topic string) ([]Highlight, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, content, type, priority, sources, is_pinned, created_at, expires_at FROM highlights WHERE topic=$1 ORDER BY created_at DESC`, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Highlight
	for rows.Next() {
		var h Highlight
		var sourcesB []byte
		if err := rows.Scan(&h.ID, &h.Title, &h.Content, &h.Type, &h.Priority, &sourcesB, &h.IsPinned, &h.CreatedAt, &h.ExpiresAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(sourcesB, &h.Sources)
		out = append(out, h)
	}
	return out, rows.Err()
}
func (s *PostgresStorage) UpdateHighlight(ctx context.Context, h Highlight) error {
	return s.SaveHighlight(ctx, h)
}
func (s *PostgresStorage) DeleteHighlight(ctx context.Context, highlightID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM highlights WHERE id=$1`, highlightID)
	return err
}

func graphTopicFromSources(srcs []string) string {
	if len(srcs) > 0 {
		return srcs[0]
	}
	return "general"
}

// Helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Simple agent implementations to avoid import cycles

// SimpleAgent is a basic agent implementation
type SimpleAgent struct {
	agentType   string
	config      *config.Config
	llmProvider LLMProvider
	telemetry   *telemetrypkg.Telemetry
	logger      *log.Logger
}

// NewResearchAgent creates a new research agent
func NewResearchAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetrypkg.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "research",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[RESEARCH-AGENT] ", log.LstdFlags),
	}
}

// NewAnalysisAgent creates a new analysis agent
func NewAnalysisAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetrypkg.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "analysis",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[ANALYSIS-AGENT] ", log.LstdFlags),
	}
}

// NewSynthesisAgent creates a new synthesis agent
func NewSynthesisAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetrypkg.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "synthesis",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[SYNTHESIS-AGENT] ", log.LstdFlags),
	}
}

// NewConflictDetectionAgent creates a new conflict detection agent
func NewConflictDetectionAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetrypkg.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "conflict_detection",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[CONFLICT-AGENT] ", log.LstdFlags),
	}
}

// NewHighlightManagementAgent creates a new highlight management agent
func NewHighlightManagementAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetrypkg.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "highlight_management",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[HIGHLIGHT-AGENT] ", log.LstdFlags),
	}
}

// NewKnowledgeGraphAgent creates a new knowledge graph agent
func NewKnowledgeGraphAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetrypkg.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "knowledge_graph",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[KNOWLEDGE-AGENT] ", log.LstdFlags),
	}
}

// Execute performs the agent task
func (a *SimpleAgent) Execute(ctx context.Context, task AgentTask) (AgentResult, error) {
	startTime := time.Now()

	a.logger.Printf("Executing %s task: %s", a.agentType, task.Description)

	// Simulate processing based on agent type
	var result AgentResult

	switch a.agentType {
	case "research":
		result = a.executeResearch(ctx, task)
	case "analysis":
		result = a.executeAnalysis(ctx, task)
	case "synthesis":
		result = a.executeSynthesis(ctx, task)
	case "conflict_detection":
		result = a.executeConflictDetection(ctx, task)
	case "highlight_management":
		result = a.executeHighlightManagement(ctx, task)
	case "knowledge_graph":
		result = a.executeKnowledgeGraph(ctx, task)
	default:
		return AgentResult{}, fmt.Errorf("unknown agent type: %s", a.agentType)
	}

	result.ID = task.ID + "_result"
	result.TaskID = task.ID
	result.AgentType = a.agentType
	result.Success = true
	result.ProcessingTime = time.Since(startTime)
	result.CreatedAt = time.Now()

	return result, nil
}

// GetCapabilities returns the agent's capabilities
func (a *SimpleAgent) GetCapabilities() []string {
	switch a.agentType {
	case "research":
		return []string{"research", "search", "information_gathering"}
	case "analysis":
		return []string{"analysis", "content_analysis", "relevance_scoring"}
	case "synthesis":
		return []string{"synthesis", "summarization", "report_generation"}
	case "conflict_detection":
		return []string{"conflict_detection", "fact_checking"}
	case "highlight_management":
		return []string{"highlight_management", "priority_ranking"}
	case "knowledge_graph":
		return []string{"knowledge_graph", "entity_extraction"}
	default:
		return []string{}
	}
}

// GetConfidence returns the agent's confidence in handling a task
func (a *SimpleAgent) GetConfidence(task AgentTask) float64 {
	if task.Type == a.agentType {
		return 0.9
	}
	return 0.3
}

// GetEstimatedCost returns estimated cost for a task
func (a *SimpleAgent) GetEstimatedCost(task AgentTask) float64 {
	switch a.agentType {
	case "research":
		return 0.5
	case "analysis":
		return 0.3
	case "synthesis":
		return 1.1
	case "conflict_detection":
		return 0.2
	case "highlight_management":
		return 0.15
	case "knowledge_graph":
		return 0.25
	default:
		return 0.1
	}
}

// GetEstimatedTime returns estimated time for a task
func (a *SimpleAgent) GetEstimatedTime(task AgentTask) time.Duration {
	switch a.agentType {
	case "research":
		return 60 * time.Second
	case "analysis":
		return 30 * time.Second
	case "synthesis":
		return 120 * time.Second
	case "conflict_detection":
		return 20 * time.Second
	case "highlight_management":
		return 15 * time.Second
	case "knowledge_graph":
		return 25 * time.Second
	default:
		return 30 * time.Second
	}
}

// Agent execution methods

func (a *SimpleAgent) executeResearch(ctx context.Context, task AgentTask) AgentResult {
	baseQuery, _ := task.Parameters["query"].(string)
	if baseQuery == "" {
		baseQuery = "general topic"
	}

	providers, _ := NewSourceProviders(a.config.Sources)
	ctx2, cancel := context.WithTimeout(ctx, a.config.General.DefaultTimeout)
	defer cancel()
	var sourcesList []Source
	// Build options from preferences + context for Brave
	var prefs map[string]interface{}
	if pm, ok := task.Parameters["preferences"].(map[string]interface{}); ok {
		prefs = pm
	}
	var ctxm map[string]interface{}
	if cm, ok := task.Parameters["context"].(map[string]interface{}); ok {
		ctxm = cm
	}

	// Build enriched query using preferences (keywords/domains)
	enrichedQuery := buildQueryFromPrefs(baseQuery, prefs)
	baseOpts := map[string]interface{}{"query": enrichedQuery}
	// Exclusions from context
	if ctxm != nil {
		if ku, ok := ctxm["known_urls"].([]string); ok && len(ku) > 0 {
			baseOpts["exclude_urls"] = ku
		}
		if ku2, ok := ctxm["known_urls"].([]interface{}); ok && len(ku2) > 0 {
			baseOpts["exclude_urls"] = ku2
		}
	}

	// Build Brave options
	braveOpts := buildBraveOptionsFromPrefs(prefs, ctxm)
	braveOpts["query"] = enrichedQuery

	// small pagination loop for Brave only
	const targetTotal = 30
	const maxPages = 3
	domainCap := 3
	domainCount := map[string]int{}
	// Domain allow/block sets from preferences
	preferred := toStringSet(getNestedStringList(prefs, "search", "domains_preferred"))
	blocked := toStringSet(getNestedStringList(prefs, "search", "domains_blocked"))
	strictAllow, _ := getNestedBool(prefs, "search", "strict_domains")

	bravePagesFetched := 0
	// simple per-run cache keyed by page offset
	pageCache := map[int][]Source{}
	for _, p := range providers {
		switch sp := p.(type) {
		case *BraveClient:
			collected := 0
			for page := 0; page < maxPages && collected < targetTotal; page++ {
				braveOpts["offset"] = page
				var res []Source
				var err error
				if cached, ok := pageCache[page]; ok {
					res = cached
				} else {
					res, err = sp.Search(ctx2, enrichedQuery, braveOpts)
					if err == nil {
						pageCache[page] = res
					}
				}
				if err != nil {
					break
				}
				bravePagesFetched++
				startSources := len(sourcesList)
				startDomains := len(domainCount)
				for _, s := range res {
					d := strings.ToLower(domainOnly(s.URL))
					if d != "" {
						if blocked[d] {
							continue
						}
						if strictAllow && len(preferred) > 0 && !preferred[d] {
							continue
						}
						if domainCount[d] >= domainCap {
							continue
						}
					}
					sourcesList = append(sourcesList, s)
					if d != "" {
						domainCount[d]++
					}
				}
				collected = len(sourcesList)
				if len(res) == 0 {
					break
				}
				// early stop if marginal utility is too low
				newSources := collected - startSources
				newDomains := len(domainCount) - startDomains
				if newSources < 3 && newDomains < 2 {
					break
				}
			}
		default:
			// other providers (e.g., NewsAPI) once with base options
			if res, err := p.Search(ctx2, enrichedQuery, baseOpts); err == nil {
				for _, s := range res {
					d := strings.ToLower(domainOnly(s.URL))
					if d != "" {
						if blocked[d] {
							continue
						}
						if strictAllow && len(preferred) > 0 && !preferred[d] {
							continue
						}
						if domainCount[d] >= domainCap {
							continue
						}
					}
					sourcesList = append(sourcesList, s)
					if d != "" {
						domainCount[d]++
					}
				}
			}
		}
	}
	// de-duplicate after collection
	sourcesList = DeduplicateSources(sourcesList)
	// rank results by credibility, domain preference, snippet quality, and recency if available
	ranked := rankSources(sourcesList, enrichedQuery, preferred)
	a.logger.Printf("research: brave pages=%d total_sources=%d unique_domains=%d", bravePagesFetched, len(sourcesList), len(domainCount))
	if a.telemetry != nil {
		a.telemetry.RecordResearchStats(ctx, bravePagesFetched, len(ranked), len(domainCount))
	}
	return AgentResult{
		Data: map[string]interface{}{
			"sources":       ranked,
			"total_sources": len(ranked),
			"query":         enrichedQuery,
			"search_params": braveOpts,
			"pages_fetched": bravePagesFetched,
			"domain_cap":    domainCap,
		},
		Sources:    ranked,
		Confidence: 0.85,
		Cost:       0.5,
		TokensUsed: 500,
		ModelUsed:  "gpt-5",
	}
}

// buildBraveOptionsFromPrefs maps preferences and context to Brave search parameters
func buildBraveOptionsFromPrefs(prefs map[string]interface{}, ctxm map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"count":            20,
		"text_decorations": false,
		"spellcheck":       true,
		"result_filter":    "web",
		"extra_snippets":   true,
	}
	// preferences.search.*
	if prefs != nil {
		if s, ok := getNestedString(prefs, "search", "country"); ok {
			out["country"] = s
		}
		if s, ok := getNestedString(prefs, "search", "search_lang"); ok {
			out["search_lang"] = s
		}
		if s, ok := getNestedString(prefs, "search", "ui_lang"); ok {
			out["ui_lang"] = s
		}
		if s, ok := getNestedString(prefs, "search", "safesearch"); ok {
			out["safesearch"] = s
		}
		if s, ok := getNestedString(prefs, "search", "freshness"); ok {
			out["freshness"] = s
		}
		if v, ok := getNestedInt(prefs, "search", "count"); ok {
			if v > 0 && v <= 20 {
				out["count"] = v
			}
		}
		if v, ok := getNestedBool(prefs, "search", "extra_snippets"); ok {
			out["extra_snippets"] = v
		}
		if s, ok := getNestedString(prefs, "search", "units"); ok {
			out["units"] = s
		}
	}
	// derive freshness if absent from last_run_time
	if _, have := out["freshness"]; !have && ctxm != nil {
		if s, ok := ctxm["last_run_time"].(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				dt := time.Since(t)
				switch {
				case dt.Hours() <= 24:
					out["freshness"] = "pd"
				case dt.Hours() <= 24*7:
					out["freshness"] = "pw"
				case dt.Hours() <= 24*31:
					out["freshness"] = "pm"
				default:
					out["freshness"] = "py"
				}
			}
		}
	}
	return out
}

// buildQueryFromPrefs injects operators (quotes, intitle:, site:, -site:) based on preferences.
func buildQueryFromPrefs(base string, prefs map[string]interface{}) string {
	q := strings.TrimSpace(base)
	if q == "" {
		q = "news"
	}
	// Gather keywords
	var kws []string
	if arr := getNestedStringList(prefs, "search", "keywords"); len(arr) > 0 {
		kws = append(kws, arr...)
	}
	if arr := getNestedStringList(prefs, "keywords"); len(arr) > 0 {
		kws = append(kws, arr...)
	}
	// Dedup
	seen := map[string]bool{}
	outParts := []string{q}
	// Add quoted keywords (cap to 5)
	added := 0
	for _, kw := range kws {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		lkw := strings.ToLower(kw)
		if seen[lkw] {
			continue
		}
		seen[lkw] = true
		if strings.ContainsAny(kw, " ") {
			outParts = append(outParts, fmt.Sprintf("\"%s\"", kw))
		} else {
			outParts = append(outParts, kw)
		}
		added++
		if added >= 5 {
			break
		}
	}
	// intitle keywords
	if arr := getNestedStringList(prefs, "search", "intitle_keywords"); len(arr) > 0 {
		n := 0
		for _, it := range arr {
			it = strings.TrimSpace(it)
			if it == "" {
				continue
			}
			if strings.ContainsAny(it, " ") {
				outParts = append(outParts, fmt.Sprintf("intitle:\"%s\"", it))
			} else {
				outParts = append(outParts, "intitle:"+it)
			}
			n++
			if n >= 3 {
				break
			}
		}
	}
	// domains
	pref := toStringSet(getNestedStringList(prefs, "search", "domains_preferred"))
	blocked := toStringSet(getNestedStringList(prefs, "search", "domains_blocked"))
	strict, _ := getNestedBool(prefs, "search", "strict_domains")
	// If strict, include site: filters (cap 4)
	if strict && len(pref) > 0 {
		count := 0
		ors := []string{}
		for d := range pref {
			ors = append(ors, "site:"+d)
			count++
			if count >= 4 {
				break
			}
		}
		sort.Strings(ors)
		if len(ors) > 0 {
			outParts = append(outParts, "("+strings.Join(ors, " OR ")+")")
		}
	}
	// Always add -site: for blocked (cap 5)
	if len(blocked) > 0 {
		count := 0
		// Deterministic order
		ds := make([]string, 0, len(blocked))
		for d := range blocked {
			ds = append(ds, d)
		}
		sort.Strings(ds)
		for _, d := range ds {
			outParts = append(outParts, "-site:"+d)
			count++
			if count >= 5 {
				break
			}
		}
	}
	// Join with spaces
	return strings.Join(outParts, " ")
}

func getNestedString(m map[string]interface{}, path ...string) (string, bool) {
	cur := m
	for i, k := range path {
		if i == len(path)-1 {
			if v, ok := cur[k].(string); ok {
				return v, true
			}
			return "", false
		}
		v, ok := cur[k].(map[string]interface{})
		if !ok {
			return "", false
		}
		cur = v
	}
	return "", false
}
func getNestedInt(m map[string]interface{}, path ...string) (int, bool) {
	cur := m
	for i, k := range path {
		if i == len(path)-1 {
			switch t := cur[k].(type) {
			case int:
				return t, true
			case float64:
				return int(t), true
			default:
				return 0, false
			}
		}
		v, ok := cur[k].(map[string]interface{})
		if !ok {
			return 0, false
		}
		cur = v
	}
	return 0, false
}
func getNestedFloat(m map[string]interface{}, path ...string) (float64, bool) {
	cur := m
	for i, k := range path {
		if i == len(path)-1 {
			switch t := cur[k].(type) {
			case float64:
				return t, true
			case int:
				return float64(t), true
			default:
				return 0, false
			}
		}
		v, ok := cur[k].(map[string]interface{})
		if !ok {
			return 0, false
		}
		cur = v
	}
	return 0, false
}
func getNestedBool(m map[string]interface{}, path ...string) (bool, bool) {
	cur := m
	for i, k := range path {
		if i == len(path)-1 {
			if v, ok := cur[k].(bool); ok {
				return v, true
			}
			return false, false
		}
		v, ok := cur[k].(map[string]interface{})
		if !ok {
			return false, false
		}
		cur = v
	}
	return false, false
}

func domainOnly(u string) string {
	if u == "" {
		return ""
	}
	s := u
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

func getNestedStringList(m map[string]interface{}, path ...string) []string {
	cur := m
	for i, k := range path {
		if i == len(path)-1 {
			var out []string
			if arr, ok := cur[k].([]string); ok {
				return arr
			}
			if ai, ok := cur[k].([]interface{}); ok {
				for _, it := range ai {
					if s, ok := it.(string); ok && s != "" {
						out = append(out, s)
					}
				}
			}
			return out
		}
		v, ok := cur[k].(map[string]interface{})
		if !ok {
			return nil
		}
		cur = v
	}
	return nil
}

func toStringSet(arr []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range arr {
		if s != "" {
			m[strings.ToLower(s)] = true
		}
	}
	return m
}

func rankSources(in []Source, query string, preferred map[string]bool) []Source {
	// naive scoring function: credibility + domain boost + snippet quality + (optional) recency
	type scored struct {
		s     Source
		score float64
	}
	qTokens := tokenizeQuery(query)
	scoredArr := make([]scored, 0, len(in))
	for _, s := range in {
		base := s.Credibility
		d := domainOnly(s.URL)
		if preferred[d] {
			base += 0.15
		}
		if isTrustedDomain(d) {
			base += 0.1
		}
		// snippet quality
		sn := s.Summary
		qual := 0.0
		if sn != "" {
			l := float64(len(sn))
			if l > 300 {
				l = 300
			}
			qual = l / 300.0
			// keyword overlap
			overlap := keywordOverlap(qTokens, strings.ToLower(sn))
			titleOverlap := keywordOverlap(qTokens, strings.ToLower(s.Title))
			qual = 0.4*qual + 0.4*overlap + 0.2*titleOverlap
		}
		// recency: if PublishedAt present, score higher if within 7d
		rec := 0.5
		if !s.PublishedAt.IsZero() {
			dt := time.Since(s.PublishedAt)
			switch {
			case dt.Hours() <= 24:
				rec = 1.0
			case dt.Hours() <= 24*7:
				rec = 0.8
			case dt.Hours() <= 24*31:
				rec = 0.6
			default:
				rec = 0.4
			}
		}
		score := 0.5*base + 0.25*qual + 0.25*rec
		scoredArr = append(scoredArr, scored{s: s, score: score})
	}
	// sort by score desc, stable on title
	sort.SliceStable(scoredArr, func(i, j int) bool {
		if scoredArr[i].score == scoredArr[j].score {
			return scoredArr[i].s.Title < scoredArr[j].s.Title
		}
		return scoredArr[i].score > scoredArr[j].score
	})
	out := make([]Source, 0, len(scoredArr))
	for _, it := range scoredArr {
		out = append(out, it.s)
	}
	return out
}

// isTrustedDomain applies a small boost for well-known authoritative outlets
func isTrustedDomain(d string) bool {
	if d == "" {
		return false
	}
	trusted := []string{
		"apnews.com", "reuters.com", "bbc.com", "bbc.co.uk", "npr.org", "politico.com",
		"axios.com", "theguardian.com", "cnbc.com", "bloomberg.com", "ft.com", "wsj.com",
		"nytimes.com", "washingtonpost.com", "economist.com",
	}
	d = strings.ToLower(d)
	for _, t := range trusted {
		if strings.HasSuffix(d, t) {
			return true
		}
	}
	return false
}

func tokenizeQuery(q string) []string {
	q = strings.ToLower(q)
	parts := strings.Fields(q)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) >= 3 {
			out = append(out, p)
		}
	}
	return out
}

func keywordOverlap(tokens []string, text string) float64 {
	if len(tokens) == 0 || text == "" {
		return 0
	}
	hits := 0
	for _, t := range tokens {
		if strings.Contains(text, t) {
			hits++
		}
	}
	return float64(hits) / float64(len(tokens))
}

type analysisParseResult struct {
	Relevance        float64  `json:"relevance_score"`
	Credibility      float64  `json:"credibility_score"`
	Importance       float64  `json:"importance_score"`
	Sentiment        string   `json:"sentiment"`
	KeyTopics        []string `json:"key_topics"`
	ContentQuality   string   `json:"content_quality"`
	InformationDepth string   `json:"information_depth"`
	Confidence       float64  `json:"confidence"`
}

func (a *SimpleAgent) executeAnalysis(ctx context.Context, task AgentTask) AgentResult {
	// Use LLM to assess relevance/credibility/importance from the task context
	model := a.config.LLM.Routing.Analysis
	if model == "" {
		model = a.config.LLM.Routing.Fallback
	}

	// Build analysis context
	query, _ := task.Parameters["query"].(string)
	if query == "" {
		query = task.Description
	}
	// Optional: include brief source summaries if provided
	var sources []Source
	if v, ok := task.Parameters["sources"].([]Source); ok {
		sources = v
	} else if vi, ok := task.Parameters["sources"].([]interface{}); ok {
		for _, it := range vi {
			if m, ok := it.(map[string]interface{}); ok {
				s := Source{}
				if t, _ := m["title"].(string); t != "" {
					s.Title = t
				}
				if u, _ := m["url"].(string); u != "" {
					s.URL = u
				}
				if ty, _ := m["type"].(string); ty != "" {
					s.Type = ty
				}
				sources = append(sources, s)
			}
		}
	}
	// Apply analysis preferences: min_credibility, sources_limit, language, weighting
	var prefs map[string]interface{}
	if pm, ok := task.Parameters["preferences"].(map[string]interface{}); ok {
		prefs = pm
	}
	// defaults
	minCred := 0.0
	if v, ok := getNestedInt(prefs, "analysis", "sources_limit"); ok && v > 0 {
		// will be applied below
	}
	if f, ok := getNestedFloat(prefs, "analysis", "min_credibility"); ok {
		minCred = f
	}
	lang, _ := getNestedString(prefs, "analysis", "language")
	relW, _ := getNestedFloat(prefs, "analysis", "relevance_weight")
	if relW == 0 {
		relW = 1
	}
	credW, _ := getNestedFloat(prefs, "analysis", "credibility_weight")
	if credW == 0 {
		credW = 1
	}
	impW, _ := getNestedFloat(prefs, "analysis", "importance_weight")
	if impW == 0 {
		impW = 1
	}
	topicsLimit, _ := getNestedInt(prefs, "analysis", "key_topics_limit")
	if topicsLimit <= 0 {
		topicsLimit = 5
	}
	sentimentMode, _ := getNestedString(prefs, "analysis", "sentiment_mode")

	// Filter and limit sources
	var filtered []Source
	for _, s := range sources {
		if s.Credibility < minCred {
			continue
		}
		filtered = append(filtered, s)
	}
	// Sort by credibility desc simple (stable by title)
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Credibility == filtered[j].Credibility {
			return filtered[i].Title < filtered[j].Title
		}
		return filtered[i].Credibility > filtered[j].Credibility
	})
	if lim, ok := getNestedInt(prefs, "analysis", "sources_limit"); ok && lim > 0 && lim < len(filtered) {
		filtered = filtered[:lim]
	}

	srcCtx := ""
	for i, s := range filtered {
		if i >= 8 {
			break
		}
		srcCtx += fmt.Sprintf("- %s (%s) %s\n", s.Title, s.Type, s.URL)
	}
	sentimentInstr := "detect"
	if strings.ToLower(sentimentMode) == "none" {
		sentimentInstr = "none"
	}
	langInstr := lang
	if langInstr == "" {
		langInstr = "auto"
	}
	prompt := fmt.Sprintf(`You are an assistant analyzing relevance, credibility, and importance for a research task.
TASK: %s
TOP CONTEXT SOURCES (may be empty):
%s
Weighting: relevance=%.2f, credibility=%.2f, importance=%.2f.
Language: %s. Sentiment mode: %s. Limit key_topics to %d.
Respond ONLY as strict JSON with keys:
{"relevance_score": number 0..1, "credibility_score": number 0..1, "importance_score": number 0..1, "sentiment": string one of [positive, neutral, negative], "key_topics": [string], "content_quality": string, "information_depth": string, "confidence": number 0..1}
`, query, srcCtx, relW, credW, impW, langInstr, sentimentInstr, topicsLimit)

	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, nil)
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}
	jOut, err := helpers.ExtractJSON(out)
	if err != nil {
		a.logger.Printf("Analysis agent JSON parse error: %v, raw output: %s", err, out)
		return AgentResult{Data: map[string]interface{}{"error": "failed to parse analysis output"}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}

	var parsed analysisParseResult
	// lenient JSON extraction
	if e := json.Unmarshal([]byte(jOut), &parsed); e != nil {
		// fallback minimal
		parsed = analysisParseResult{
			Relevance: 0.7, Credibility: 0.6, Importance: 0.65, Sentiment: "neutral", KeyTopics: []string{}, ContentQuality: "unknown", InformationDepth: "unknown", Confidence: 0.6,
		}
	}

	cost := a.llmProvider.CalculateCost(inTok, outTok, model)
	if a.telemetry != nil {
		a.telemetry.RecordAnalysisStats(ctx, len(filtered), minCred)
	}
	return AgentResult{
		Data: map[string]interface{}{
			"relevance_score":   parsed.Relevance,
			"credibility_score": parsed.Credibility,
			"importance_score":  parsed.Importance,
			"sentiment":         parsed.Sentiment,
			"key_topics":        parsed.KeyTopics,
			"content_quality":   parsed.ContentQuality,
			"information_depth": parsed.InformationDepth,
			"confidence":        parsed.Confidence,
			"preferences_used":  map[string]interface{}{"min_credibility": minCred, "sources_considered": len(filtered), "language": langInstr, "weights": map[string]float64{"relevance": relW, "credibility": credW, "importance": impW}, "key_topics_limit": topicsLimit, "sentiment_mode": sentimentInstr},
		},
		Confidence: parsed.Confidence,
		Cost:       cost,
		TokensUsed: inTok + outTok,
		ModelUsed:  model,
	}
}

func (a *SimpleAgent) executeSynthesis(ctx context.Context, task AgentTask) AgentResult {
	userThought, _ := task.Parameters["user_thought"].(string)
	if userThought == "" {
		userThought = task.Description
	}

	// Collect sources from parameters
	var sources []Source
	if v, ok := task.Parameters["sources"].([]Source); ok {
		sources = v
	} else if vi, ok := task.Parameters["sources"].([]interface{}); ok {
		for _, it := range vi {
			if m, ok := it.(map[string]interface{}); ok {
				s := Source{}
				if t, _ := m["title"].(string); t != "" {
					s.Title = t
				}
				if u, _ := m["url"].(string); u != "" {
					s.URL = u
				}
				if ty, _ := m["type"].(string); ty != "" {
					s.Type = ty
				}
				if c, ok2 := m["content"].(string); ok2 {
					s.Content = c
				}
				if sm, ok2 := m["summary"].(string); ok2 {
					s.Summary = sm
				}
				sources = append(sources, s)
			}
		}
	}

	// Build source context for the LLM
	ctxBuf := &bytes.Buffer{}
	max := 10
	for i, s := range sources {
		if i >= max {
			break
		}
		fmt.Fprintf(ctxBuf, "- Title: %s\n  URL: %s\n  Type: %s\n  Summary: %s\n", s.Title, s.URL, s.Type, s.Summary)
	}

	// Collect analysis signals from dependent tasks if available
	analysisBuf := &bytes.Buffer{}
	if inputsAny, ok := task.Parameters["inputs"].([]AgentResult); ok {
		// Use the first analysis result found (or aggregate if multiple)
		total := 0
		var avgRel, avgImp, avgCred float64
		keyTopicsSet := map[string]int{}
		for _, r := range inputsAny {
			if r.Data == nil {
				continue
			}
			if v, ok2 := r.Data["relevance_score"].(float64); ok2 {
				avgRel += v
			}
			if v, ok2 := r.Data["importance_score"].(float64); ok2 {
				avgImp += v
			}
			if v, ok2 := r.Data["credibility_score"].(float64); ok2 {
				avgCred += v
			}
			if kt, ok2 := r.Data["key_topics"].([]interface{}); ok2 {
				for _, t := range kt {
					if s, ok3 := t.(string); ok3 {
						keyTopicsSet[s]++
					}
				}
			}
			total++
		}
		if total > 0 {
			avgRel /= float64(total)
			avgImp /= float64(total)
			avgCred /= float64(total)
			// Prepare key topics list (top up to 10)
			type kv struct {
				k string
				v int
			}
			var arr []kv
			for k, v := range keyTopicsSet {
				arr = append(arr, kv{k, v})
			}
			sort.Slice(arr, func(i, j int) bool {
				if arr[i].v == arr[j].v {
					return arr[i].k < arr[j].k
				}
				return arr[i].v > arr[j].v
			})
			topics := []string{}
			for i := 0; i < len(arr) && i < 10; i++ {
				topics = append(topics, arr[i].k)
			}
			fmt.Fprintf(analysisBuf, "- avg_relevance: %.2f\n- avg_importance: %.2f\n- avg_credibility: %.2f\n- key_topics: %s\n", avgRel, avgImp, avgCred, strings.Join(topics, ", "))
		}
	}

	// Extract preference weights if present
	var weightLine string
	if pm, ok := task.Parameters["preferences"].(map[string]interface{}); ok {
		if am, ok2 := pm["analysis"].(map[string]interface{}); ok2 {
			rw, _ := am["relevance_weight"].(float64)
			cw, _ := am["credibility_weight"].(float64)
			iw, _ := am["importance_weight"].(float64)
			mc := 0.0
			if v, ok3 := am["min_credibility"].(float64); ok3 {
				mc = v
			}
			weightLine = fmt.Sprintf("- weights: relevance=%.2f, credibility=%.2f, importance=%.2f; min_credibility=%.2f\n", rw, cw, iw, mc)
		}
	}

	model := a.config.LLM.Routing.Synthesis
	if model == "" {
		model = a.config.LLM.Routing.Fallback
	}
	prompt := fmt.Sprintf(`You are a synthesis agent creating a comprehensive report from multiple sources.
USER THOUGHT: %s
SOURCES (top %d):
%s

ANALYSIS SIGNALS (use for ordering and coverage):
%s%s

Guidance:
- Produce a coherent report that is directly useful; do not force the user to research basic facts.
- If any item is significant (e.g., major release, critical event), include a deeper section with concrete details (what changed, why it matters, timeline, how-to, links).
- Use clear section headings in detailed_report (e.g., Executive Summary, Key Developments, Deep Dives, Outlook).
- Avoid speculation; cite facts from sources; prefer recency and credibility.
 - Make the summary concise (max ~120 words, 2–3 sentences). Keep all deeper details in detailed_report with clear headings.

Constraints:
- Produce between 6 and 12 items; drop any item that lacks published_at or sources.
- Each item MUST include at least one primary source (authoritative outlet where possible).
- Normalize domains and include archived_url where applicable.

Return ONLY strict JSON with keys:
{
  "summary": string,
  "detailed_report": string,
  "items": [
    {
      "title": string,
      "summary": string,
      "category": string,               // e.g., "top" | "policy" | "politics" | "legal" | "markets" | "other"
      "tags": [string],
      "published_at": string,           // ISO8601
      "sources": [ { "url": string, "domain": string, "archived_url": string } ],
      "importance": number,             // 0..1
      "confidence": number              // 0..1
    }
  ],
  "highlights": [ { "title": string, "content": string, "type": string, "priority": number 1..5, "source_urls": [string] } ],
  "conflicts": [ { "description": string, "severity": "low|medium|high|critical", "resolution": string, "source_urls": [string] } ],
  "confidence": number 0..1
}
`, userThought, max, ctxBuf.String(), analysisBuf.String(), weightLine)

	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, nil)
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}

	jOut, err := helpers.ExtractJSON(out)
	if err != nil {
		a.logger.Printf("Analysis agent JSON parse error: %v, raw output: %s", err, out)
		return AgentResult{Data: map[string]interface{}{"error": "failed to parse analysis output"}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}

	// Parse JSON
	var parsed struct {
		Summary    string  `json:"summary"`
		Detailed   string  `json:"detailed_report"`
		Confidence float64 `json:"confidence"`
		Items      []struct {
			Title       string   `json:"title"`
			Summary     string   `json:"summary"`
			Category    string   `json:"category"`
			Tags        []string `json:"tags"`
			PublishedAt string   `json:"published_at"`
			Importance  float64  `json:"importance"`
			Conf        float64  `json:"confidence"`
			Sources     []struct {
				URL         string `json:"url"`
				Domain      string `json:"domain"`
				ArchivedURL string `json:"archived_url"`
			} `json:"sources"`
		} `json:"items"`
		Highlights []struct {
			Title, Content, Type string
			Priority             int
			SourceURLs           []string `json:"source_urls"`
		} `json:"highlights"`
		Conflicts []struct {
			Description, Severity, Resolution string
			SourceURLs                        []string `json:"source_urls"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(jOut), &parsed); err != nil {
		// Fallback: use whole text as summary
		parsed.Summary = out
		parsed.Detailed = out
		parsed.Confidence = 0.6
	}

	// Map to internal types
	now := time.Now()
	var highlights []Highlight
	for _, h := range parsed.Highlights {
		hl := Highlight{ID: uuid.NewString(), Title: h.Title, Content: h.Content, Type: h.Type, Priority: h.Priority, CreatedAt: now, IsPinned: false}
		if len(h.SourceURLs) == 0 {
			// attach first 1-2 known source URLs
			n := 0
			for _, s := range sources {
				if s.URL != "" {
					hl.Sources = append(hl.Sources, s.URL)
					n++
					if n >= 2 {
						break
					}
				}
			}
		} else {
			hl.Sources = h.SourceURLs
		}
		highlights = append(highlights, hl)
	}
	var conflicts []Conflict
	for _, c := range parsed.Conflicts {
		conflicts = append(conflicts, Conflict{ID: uuid.NewString(), Description: c.Description, Resolution: c.Resolution, Severity: c.Severity, CreatedAt: now})
	}

	cost := a.llmProvider.CalculateCost(inTok, outTok, model)
	// Convert items into generic maps for metadata propagation
	var itemsOut []map[string]interface{}
	for _, it := range parsed.Items {
		m := map[string]interface{}{
			"title":        it.Title,
			"summary":      it.Summary,
			"category":     it.Category,
			"tags":         it.Tags,
			"published_at": it.PublishedAt,
			"importance":   it.Importance,
			"confidence":   it.Conf,
		}
		var srcs []map[string]interface{}
		for _, s := range it.Sources {
			srcs = append(srcs, map[string]interface{}{"url": s.URL, "domain": s.Domain, "archived_url": s.ArchivedURL})
		}
		m["sources"] = srcs
		itemsOut = append(itemsOut, m)
	}
	return AgentResult{
		Data: map[string]interface{}{
			"summary":         parsed.Summary,
			"detailed_report": parsed.Detailed,
			"items":           itemsOut,
			"highlights":      highlights,
			"conflicts":       conflicts,
			"confidence":      parsed.Confidence,
		},
		Sources:    sources,
		Confidence: parsed.Confidence,
		Cost:       cost,
		TokensUsed: inTok + outTok,
		ModelUsed:  model,
	}
}

func (a *SimpleAgent) executeConflictDetection(ctx context.Context, task AgentTask) AgentResult {
	// Gather sources either from parameters or dependency inputs
	var sources []Source
	if v, ok := task.Parameters["sources"].([]Source); ok {
		sources = v
	} else if vi, ok := task.Parameters["sources"].([]interface{}); ok {
		for _, it := range vi {
			if m, ok := it.(map[string]interface{}); ok {
				s := Source{}
				if t, _ := m["title"].(string); t != "" {
					s.Title = t
				}
				if u, _ := m["url"].(string); u != "" {
					s.URL = u
				}
				if ty, _ := m["type"].(string); ty != "" {
					s.Type = ty
				}
				if sm, ok2 := m["summary"].(string); ok2 {
					s.Summary = sm
				}
				sources = append(sources, s)
			}
		}
	}
	if inputs, ok := task.Parameters["inputs"].([]AgentResult); ok && len(sources) == 0 {
		for _, r := range inputs {
			if len(r.Sources) > 0 {
				sources = append(sources, r.Sources...)
			}
		}
	}
	sources = DeduplicateSources(sources)
	// Build statements from sources
	// Apply conflict preferences
	var prefs map[string]interface{}
	if pm, ok := task.Parameters["preferences"].(map[string]interface{}); ok {
		prefs = pm
	}
	sourcesWindow, _ := getNestedInt(prefs, "conflict", "sources_window")
	if sourcesWindow <= 0 {
		sourcesWindow = 12
	}
	contradictoryThresh, _ := getNestedFloat(prefs, "conflict", "contradictory_threshold")
	requireCitations, _ := getNestedBool(prefs, "conflict", "require_citations")
	stanceDetection, _ := getNestedBool(prefs, "conflict", "stance_detection")
	groupingBy, _ := getNestedString(prefs, "conflict", "grouping_by")
	minSupport, _ := getNestedInt(prefs, "conflict", "min_supporting_evidence")
	resolutionStrategy, _ := getNestedString(prefs, "conflict", "resolution_strategy")

	buf := &bytes.Buffer{}
	max := sourcesWindow
	for i, s := range sources {
		if i >= max {
			break
		}
		fmt.Fprintf(buf, "- %s :: %s\n", s.Title, s.Summary)
	}
	model := a.config.LLM.Routing.Analysis
	if model == "" {
		model = a.config.LLM.Routing.Fallback
	}
	prompt := fmt.Sprintf(`You are a conflict detection assistant.
Given the following statements extracted from different sources, find conflicting claims.
List concise conflicts with severity and a suggested resolution.
Sensitivity threshold: %.2f (0..1). Grouping: %s. Min supporting evidence: %d. Resolution strategy: %s.
Citation requirement: %t. Stance detection: %t.
STATEMENTS:\n%s\n
Return ONLY strict JSON: { "conflicts": [ { "description": string, "severity": "low|medium|high|critical", "resolution": string } ], "confidence": number 0..1 }`,
		contradictoryThresh, groupingBy, minSupport, resolutionStrategy, requireCitations, stanceDetection, buf.String())

	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, nil)
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}
	jOut, err := helpers.ExtractJSON(out)
	if err != nil {
		a.logger.Printf("Analysis agent JSON parse error: %v, raw output: %s", err, out)
		return AgentResult{Data: map[string]interface{}{"error": "failed to parse analysis output"}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}

	var parsed struct {
		Conflicts  []struct{ Description, Severity, Resolution string } `json:"conflicts"`
		Confidence float64                                              `json:"confidence"`
	}
	if e := json.Unmarshal([]byte(jOut), &parsed); e != nil {
		parsed.Conflicts = nil
		parsed.Confidence = 0.5
	}
	now := time.Now()
	var conflicts []Conflict
	for _, c := range parsed.Conflicts {
		conflicts = append(conflicts, Conflict{ID: uuid.NewString(), Description: c.Description, Resolution: c.Resolution, Severity: c.Severity, CreatedAt: now})
	}
	cost := a.llmProvider.CalculateCost(inTok, outTok, model)
	return AgentResult{
		Data: map[string]interface{}{
			"conflicts":        conflicts,
			"conflict_count":   len(conflicts),
			"confidence":       parsed.Confidence,
			"preferences_used": map[string]interface{}{"sources_window": sourcesWindow, "contradictory_threshold": contradictoryThresh, "require_citations": requireCitations, "stance_detection": stanceDetection, "grouping_by": groupingBy, "min_supporting_evidence": minSupport, "resolution_strategy": resolutionStrategy},
		},
		Sources:    sources,
		Confidence: parsed.Confidence,
		Cost:       cost,
		TokensUsed: inTok + outTok,
		ModelUsed:  model,
	}
}

func (a *SimpleAgent) executeHighlightManagement(ctx context.Context, task AgentTask) AgentResult {
	// Use LLM to extract key highlights from sources
	var sources []Source
	if v, ok := task.Parameters["sources"].([]Source); ok {
		sources = v
	}
	if vi, ok := task.Parameters["sources"].([]interface{}); ok && len(sources) == 0 {
		for _, it := range vi {
			if m, ok := it.(map[string]interface{}); ok {
				s := Source{}
				if t, _ := m["title"].(string); t != "" {
					s.Title = t
				}
				if u, _ := m["url"].(string); u != "" {
					s.URL = u
				}
				if sm, ok2 := m["summary"].(string); ok2 {
					s.Summary = sm
				}
				sources = append(sources, s)
			}
		}
	}
	if inputs, ok := task.Parameters["inputs"].([]AgentResult); ok && len(sources) == 0 {
		for _, r := range inputs {
			if len(r.Sources) > 0 {
				sources = append(sources, r.Sources...)
			}
		}
	}
	sources = DeduplicateSources(sources)
	buf := &bytes.Buffer{}
	max := 12
	for i, s := range sources {
		if i >= max {
			break
		}
		fmt.Fprintf(buf, "- %s :: %s (%s)\n", s.Title, s.Summary, s.URL)
	}
	model := a.config.LLM.Routing.Analysis
	if model == "" {
		model = a.config.LLM.Routing.Fallback
	}
	prompt := fmt.Sprintf(`Extract the most important highlights from the following source snippets.
Return ONLY strict JSON: { "highlights": [ { "title": string, "content": string, "type": "breaking|important|ongoing|general", "priority": 1|2|3|4|5, "source_urls": [string] } ], "confidence": number 0..1 }
SOURCES:\n%s`, buf.String())
	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, nil)
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}
	jOut, err := helpers.ExtractJSON(out)
	if err != nil {
		a.logger.Printf("Analysis agent JSON parse error: %v, raw output: %s", err, out)
		return AgentResult{Data: map[string]interface{}{"error": "failed to parse analysis output"}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}

	var parsed struct {
		Highlights []struct {
			Title, Content, Type string
			Priority             int
			SourceURLs           []string `json:"source_urls"`
		}
		Confidence float64
	}
	_ = json.Unmarshal([]byte(jOut), &parsed)
	now := time.Now()
	var highs []Highlight
	for _, h := range parsed.Highlights {
		hl := Highlight{ID: uuid.NewString(), Title: h.Title, Content: h.Content, Type: h.Type, Priority: h.Priority, CreatedAt: now, IsPinned: h.Priority <= 2}
		if len(h.SourceURLs) == 0 {
			// attach one source if none specified
			for _, s := range sources {
				if s.URL != "" {
					hl.Sources = []string{s.URL}
					break
				}
			}
		} else {
			hl.Sources = h.SourceURLs
		}
		highs = append(highs, hl)
	}
	cost := a.llmProvider.CalculateCost(inTok, outTok, model)
	return AgentResult{
		Data: map[string]interface{}{
			"highlights":      highs,
			"highlight_count": len(highs),
			"confidence":      parsed.Confidence,
		},
		Sources:    sources,
		Confidence: parsed.Confidence,
		Cost:       cost,
		TokensUsed: inTok + outTok,
		ModelUsed:  model,
	}
}

func (a *SimpleAgent) executeKnowledgeGraph(ctx context.Context, task AgentTask) AgentResult {
	// Build context from sources and prior results
	topic, _ := task.Parameters["topic"].(string)
	if topic == "" {
		topic = "General Topic"
	}
	var sources []Source
	if v, ok := task.Parameters["sources"].([]Source); ok {
		sources = v
	}
	if vi, ok := task.Parameters["sources"].([]interface{}); ok && len(sources) == 0 {
		for _, it := range vi {
			if m, ok := it.(map[string]interface{}); ok {
				s := Source{}
				if t, _ := m["title"].(string); t != "" {
					s.Title = t
				}
				if u, _ := m["url"].(string); u != "" {
					s.URL = u
				}
				if sm, ok2 := m["summary"].(string); ok2 {
					s.Summary = sm
				}
				sources = append(sources, s)
			}
		}
	}
	if inputs, ok := task.Parameters["inputs"].([]AgentResult); ok && len(sources) == 0 {
		for _, r := range inputs {
			if len(r.Sources) > 0 {
				sources = append(sources, r.Sources...)
			}
		}
	}
	sources = DeduplicateSources(sources)
	buf := &bytes.Buffer{}
	max := 10
	for i, s := range sources {
		if i >= max {
			break
		}
		fmt.Fprintf(buf, "- %s :: %s\n", s.Title, s.Summary)
	}
	// preferences for graph
	var prefs map[string]interface{}
	if pm, ok := task.Parameters["preferences"].(map[string]interface{}); ok {
		prefs = pm
	}
	entityTypes := getNestedStringList(prefs, "knowledge_graph", "entity_types")
	relationTypes := getNestedStringList(prefs, "knowledge_graph", "relation_types")
	minConf, _ := getNestedFloat(prefs, "knowledge_graph", "min_confidence")
	if minConf < 0 {
		minConf = 0
	} else if minConf > 1 {
		minConf = 1
	}
	dedupeThresh, _ := getNestedFloat(prefs, "knowledge_graph", "dedupe_threshold")
	includeAliases, _ := getNestedBool(prefs, "knowledge_graph", "include_aliases")
	maxNodes, _ := getNestedInt(prefs, "knowledge_graph", "max_nodes")
	if maxNodes <= 0 {
		maxNodes = 200
	}
	maxEdges, _ := getNestedInt(prefs, "knowledge_graph", "max_edges")
	if maxEdges <= 0 {
		maxEdges = 400
	}
	kgLang, _ := getNestedString(prefs, "knowledge_graph", "language")
	if kgLang == "" {
		kgLang = "auto"
	}

	model := a.config.LLM.Routing.Analysis
	if model == "" {
		model = a.config.LLM.Routing.Fallback
	}
	prompt := fmt.Sprintf(`Extract entities and relationships for a knowledge graph about "%s" from the notes below.
Entity types (whitelist): %v; Relation types (whitelist): %v.
Drop nodes/edges with confidence < %.2f. Include aliases: %t. Language: %s.
If too many items, cap at max_nodes=%d and max_edges=%d, preserving important items.
Return ONLY strict JSON:
{ "nodes": [ { "id": string, "type": string, "label": string, "description": string, "confidence": number 0..1 } ],
  "edges": [ { "id": string, "from": string, "to": string, "type": string, "weight": number 0..1, "confidence": number 0..1 } ],
  "confidence": number 0..1 } 
NOTES:\n%s`, topic, entityTypes, relationTypes, minConf, includeAliases, kgLang, maxNodes, maxEdges, buf.String())
	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, nil)
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}
	jOut, err := helpers.ExtractJSON(out)
	if err != nil {
		a.logger.Printf("Analysis agent JSON parse error: %v, raw output: %s", err, out)
		return AgentResult{Data: map[string]interface{}{"error": "failed to parse analysis output"}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}

	var parsed struct {
		Nodes []struct {
			ID, Type, Label, Description string
			Confidence                   float64
		} `json:"nodes"`
		Edges []struct {
			ID, From, To, Type string
			Weight, Confidence float64
		} `json:"edges"`
		Confidence float64 `json:"confidence"`
	}
	_ = json.Unmarshal([]byte(jOut), &parsed)
	now := time.Now()
	var nodes []KnowledgeNode
	for _, n := range parsed.Nodes {
		id := n.ID
		if id == "" {
			id = uuid.NewString()
		}
		if n.Confidence < minConf {
			continue
		}
		nodes = append(nodes, KnowledgeNode{ID: id, Type: n.Type, Label: n.Label, Description: n.Description, Confidence: n.Confidence, CreatedAt: now, UpdatedAt: now})
	}
	var edges []KnowledgeEdge
	for _, e := range parsed.Edges {
		id := e.ID
		if id == "" {
			id = uuid.NewString()
		}
		if e.Confidence < minConf {
			continue
		}
		edges = append(edges, KnowledgeEdge{ID: id, From: e.From, To: e.To, Type: e.Type, Weight: e.Weight, Confidence: e.Confidence, CreatedAt: now})
	}
	// Cap sizes
	if len(nodes) > maxNodes {
		nodes = nodes[:maxNodes]
	}
	if len(edges) > maxEdges {
		edges = edges[:maxEdges]
	}
	cost := a.llmProvider.CalculateCost(inTok, outTok, model)
	return AgentResult{
		Data: map[string]interface{}{
			"nodes":            nodes,
			"edges":            edges,
			"topic":            topic,
			"confidence":       parsed.Confidence,
			"preferences_used": map[string]interface{}{"entity_types": entityTypes, "relation_types": relationTypes, "min_confidence": minConf, "dedupe_threshold": dedupeThresh, "include_aliases": includeAliases, "max_nodes": maxNodes, "max_edges": maxEdges, "language": kgLang},
		},
		Sources:    sources,
		Confidence: parsed.Confidence,
		Cost:       cost,
		TokensUsed: inTok + outTok,
		ModelUsed:  model,
	}
}
