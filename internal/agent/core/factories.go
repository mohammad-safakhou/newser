package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
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
func NewAgents(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetry.Telemetry) (map[string]Agent, error) {
	agentsMap := make(map[string]Agent)

	// Create research agent
	researchAgent := NewResearchAgent(cfg, llmProvider, telemetry)
	agentsMap["research"] = researchAgent

	// Create analysis agent
	analysisAgent := NewAnalysisAgent(cfg, llmProvider, telemetry)
	agentsMap["analysis"] = analysisAgent

	// Create synthesis agent
	synthesisAgent := NewSynthesisAgent(cfg, llmProvider, telemetry)
	agentsMap["synthesis"] = synthesisAgent

	// Create conflict detection agent
	conflictAgent := NewConflictDetectionAgent(cfg, llmProvider, telemetry)
	agentsMap["conflict_detection"] = conflictAgent

	// Create highlight management agent
	highlightAgent := NewHighlightManagementAgent(cfg, llmProvider, telemetry)
	agentsMap["highlight_management"] = highlightAgent

	// Create knowledge graph agent
	knowledgeAgent := NewKnowledgeGraphAgent(cfg, llmProvider, telemetry)
	agentsMap["knowledge_graph"] = knowledgeAgent

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
	if cfg.WebSearch.SerperAPIKey != "" {
		providers = append(providers, &SerperClient{cfg: cfg.WebSearch, http: httpc})
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
		MaxTokens   int       `json:"max_tokens,omitempty"`
	}

	body, err := json.Marshal(chatReq{
		Model:       apiModel,
		Messages:    []chatMsg{{Role: "user", Content: prompt}},
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
		return "", 0, 0, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, 0, fmt.Errorf("OpenAI status %d", resp.StatusCode)
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
	telemetry   *telemetry.Telemetry
	logger      *log.Logger
}

// NewResearchAgent creates a new research agent
func NewResearchAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetry.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "research",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[RESEARCH-AGENT] ", log.LstdFlags),
	}
}

// NewAnalysisAgent creates a new analysis agent
func NewAnalysisAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetry.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "analysis",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[ANALYSIS-AGENT] ", log.LstdFlags),
	}
}

// NewSynthesisAgent creates a new synthesis agent
func NewSynthesisAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetry.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "synthesis",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[SYNTHESIS-AGENT] ", log.LstdFlags),
	}
}

// NewConflictDetectionAgent creates a new conflict detection agent
func NewConflictDetectionAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetry.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "conflict_detection",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[CONFLICT-AGENT] ", log.LstdFlags),
	}
}

// NewHighlightManagementAgent creates a new highlight management agent
func NewHighlightManagementAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetry.Telemetry) Agent {
	return &SimpleAgent{
		agentType:   "highlight_management",
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[HIGHLIGHT-AGENT] ", log.LstdFlags),
	}
}

// NewKnowledgeGraphAgent creates a new knowledge graph agent
func NewKnowledgeGraphAgent(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetry.Telemetry) Agent {
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
	query, _ := task.Parameters["query"].(string)
	if query == "" {
		query = "general topic"
	}

	providers, _ := NewSourceProviders(a.config.Sources)
	ctx2, cancel := context.WithTimeout(ctx, a.config.General.DefaultTimeout)
	defer cancel()
	var sourcesList []Source
	for _, p := range providers {
		if res, err := p.Search(ctx2, query, map[string]interface{}{"query": query}); err == nil {
			sourcesList = append(sourcesList, res...)
		}
	}
	// de-duplicate
	sourcesList = DeduplicateSources(sourcesList)

	return AgentResult{
		Data: map[string]interface{}{
			"sources":       sourcesList,
			"total_sources": len(sourcesList),
			"query":         query,
		},
		Sources:    sourcesList,
		Confidence: 0.85,
		Cost:       0.5,
		TokensUsed: 500,
		ModelUsed:  "gpt-5",
	}
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
	srcCtx := ""
	for i, s := range sources {
		if i >= 5 {
			break
		}
		srcCtx += fmt.Sprintf("- %s (%s) %s\n", s.Title, s.Type, s.URL)
	}
	prompt := fmt.Sprintf(`You are an assistant analyzing relevance and credibility for a research task.
TASK: %s
TOP CONTEXT SOURCES (may be empty):
%s
Respond ONLY as strict JSON with keys:
{"relevance_score": number 0..1, "credibility_score": number 0..1, "importance_score": number 0..1, "sentiment": string one of [positive, neutral, negative], "key_topics": [string], "content_quality": string, "information_depth": string, "confidence": number 0..1}
`, query, srcCtx)

	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, map[string]interface{}{"temperature": 0.2, "max_tokens": 800})
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}
	var parsed analysisParseResult
	// lenient JSON extraction
	if e := json.Unmarshal([]byte(extractFirstJSON(out)), &parsed); e != nil {
		// fallback minimal
		parsed = analysisParseResult{
			Relevance: 0.7, Credibility: 0.6, Importance: 0.65, Sentiment: "neutral", KeyTopics: []string{}, ContentQuality: "unknown", InformationDepth: "unknown", Confidence: 0.6,
		}
	}

	cost := a.llmProvider.CalculateCost(inTok, outTok, model)
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

	model := a.config.LLM.Routing.Synthesis
	if model == "" {
		model = a.config.LLM.Routing.Fallback
	}
	prompt := fmt.Sprintf(`You are a synthesis agent creating a report from multiple sources.
USER THOUGHT: %s
SOURCES (top %d):
%s
Return ONLY strict JSON with keys:
{
  "summary": string,
  "detailed_report": string,
  "highlights": [ { "title": string, "content": string, "type": string, "priority": number 1..5, "source_urls": [string] } ],
  "conflicts": [ { "description": string, "severity": "low|medium|high|critical", "resolution": string, "source_urls": [string] } ],
  "confidence": number 0..1
}
`, userThought, max, ctxBuf.String())

	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, map[string]interface{}{"temperature": 0.2, "max_tokens": 1400})
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}

	// Parse JSON
	var parsed struct {
		Summary    string  `json:"summary"`
		Detailed   string  `json:"detailed_report"`
		Confidence float64 `json:"confidence"`
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
	raw := extractFirstJSON(out)
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
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
	return AgentResult{
		Data: map[string]interface{}{
			"summary":         parsed.Summary,
			"detailed_report": parsed.Detailed,
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
	buf := &bytes.Buffer{}
	max := 12
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
STATEMENTS:\n%s\n
Return ONLY strict JSON: { "conflicts": [ { "description": string, "severity": "low|medium|high|critical", "resolution": string } ], "confidence": number 0..1 }`, buf.String())

	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, map[string]interface{}{"temperature": 0.2, "max_tokens": 800})
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}
	var parsed struct {
		Conflicts  []struct{ Description, Severity, Resolution string } `json:"conflicts"`
		Confidence float64                                              `json:"confidence"`
	}
	if e := json.Unmarshal([]byte(extractFirstJSON(out)), &parsed); e != nil {
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
			"conflicts":      conflicts,
			"conflict_count": len(conflicts),
			"confidence":     parsed.Confidence,
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
	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, map[string]interface{}{"temperature": 0.3, "max_tokens": 900})
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
	}
	var parsed struct {
		Highlights []struct {
			Title, Content, Type string
			Priority             int
			SourceURLs           []string `json:"source_urls"`
		}
		Confidence float64
	}
	_ = json.Unmarshal([]byte(extractFirstJSON(out)), &parsed)
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
	model := a.config.LLM.Routing.Analysis
	if model == "" {
		model = a.config.LLM.Routing.Fallback
	}
	prompt := fmt.Sprintf(`Extract entities and relationships for a knowledge graph about "%s" from the notes below.
Return ONLY strict JSON:
{ "nodes": [ { "id": string, "type": string, "label": string, "description": string, "confidence": number 0..1 } ],
  "edges": [ { "id": string, "from": string, "to": string, "type": string, "weight": number 0..1, "confidence": number 0..1 } ],
  "confidence": number 0..1 } 
NOTES:\n%s`, topic, buf.String())
	out, inTok, outTok, err := a.llmProvider.GenerateWithTokens(ctx, prompt, model, map[string]interface{}{"temperature": 0.2, "max_tokens": 900})
	if err != nil {
		return AgentResult{Data: map[string]interface{}{"error": err.Error()}, Success: false, AgentType: a.agentType, ModelUsed: model}
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
	_ = json.Unmarshal([]byte(extractFirstJSON(out)), &parsed)
	now := time.Now()
	var nodes []KnowledgeNode
	for _, n := range parsed.Nodes {
		id := n.ID
		if id == "" {
			id = uuid.NewString()
		}
		nodes = append(nodes, KnowledgeNode{ID: id, Type: n.Type, Label: n.Label, Description: n.Description, Confidence: n.Confidence, CreatedAt: now, UpdatedAt: now})
	}
	var edges []KnowledgeEdge
	for _, e := range parsed.Edges {
		id := e.ID
		if id == "" {
			id = uuid.NewString()
		}
		edges = append(edges, KnowledgeEdge{ID: id, From: e.From, To: e.To, Type: e.Type, Weight: e.Weight, Confidence: e.Confidence, CreatedAt: now})
	}
	cost := a.llmProvider.CalculateCost(inTok, outTok, model)
	return AgentResult{
		Data: map[string]interface{}{
			"nodes":      nodes,
			"edges":      edges,
			"topic":      topic,
			"confidence": parsed.Confidence,
		},
		Sources:    sources,
		Confidence: parsed.Confidence,
		Cost:       cost,
		TokensUsed: inTok + outTok,
		ModelUsed:  model,
	}
}

// extractFirstJSON attempts to find the first top-level JSON object in a string
func extractFirstJSON(s string) string {
	start := -1
	depth := 0
	for i, ch := range s {
		if ch == '{' {
			if depth == 0 {
				start = i
			}
			depth++
		} else if ch == '}' {
			if depth > 0 {
				depth--
			}
			if depth == 0 && start != -1 {
				return s[start : i+1]
			}
		}
	}
	return s
}
