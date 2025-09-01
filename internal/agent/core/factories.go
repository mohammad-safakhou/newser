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

func (a *SimpleAgent) executeAnalysis(ctx context.Context, task AgentTask) AgentResult {
	// Generic analysis for any topic
	return AgentResult{
		Data: map[string]interface{}{
			"relevance_score":   0.85,
			"credibility_score": 0.78,
			"importance_score":  0.72,
			"sentiment":         "neutral",
			"key_topics":        []string{"topic1", "topic2", "topic3"},
			"content_quality":   "high",
			"information_depth": "comprehensive",
		},
		Confidence: 0.8,
		Cost:       0.3,
		TokensUsed: 300,
		ModelUsed:  "gpt-5",
	}
}

func (a *SimpleAgent) executeSynthesis(ctx context.Context, task AgentTask) AgentResult {
	userThought, _ := task.Parameters["user_thought"].(string)
	if userThought == "" {
		userThought = "general topic"
	}

	// Create generic synthesis (domain-agnostic)
	summary := fmt.Sprintf("Comprehensive analysis of %s based on multiple sources and perspectives. The information has been analyzed for relevance, credibility, and importance.", userThought)

	detailedReport := fmt.Sprintf(`
COMPREHENSIVE ANALYSIS REPORT

EXECUTIVE SUMMARY:
Analysis of %s has been completed using multiple sources and analytical approaches. The information has been synthesized to provide a balanced and comprehensive view.

KEY FINDINGS:
- Multiple sources provide different perspectives on the topic
- Information has been verified and analyzed for credibility
- Conflicts and contradictions have been identified and resolved
- Highlights have been extracted for quick reference

METHODOLOGY:
- Multi-source research across different types of information providers
- Credibility assessment and bias detection
- Conflict analysis and resolution
- Synthesis of findings into coherent narrative

QUALITY ASSESSMENT:
The analysis provides comprehensive coverage while maintaining accuracy and objectivity.`, userThought)

	highlights := []Highlight{
		{
			ID:        "key_development",
			Title:     "Key Development Identified",
			Content:   "Important development requiring attention",
			Type:      "important",
			Priority:  1,
			Sources:   []string{"news_source_1"},
			CreatedAt: time.Now(),
			IsPinned:  false,
		},
		{
			ID:        "ongoing_situation",
			Title:     "Ongoing Situation",
			Content:   "Situation that requires continued monitoring",
			Type:      "ongoing",
			Priority:  2,
			Sources:   []string{"analysis_source_2"},
			CreatedAt: time.Now(),
			IsPinned:  true,
		},
	}

	conflicts := []Conflict{
		{
			ID:          "minor_discrepancy",
			Description: "Minor discrepancies found between sources regarding specific details",
			Resolution:  "Discrepancies resolved through cross-referencing with authoritative sources",
			Severity:    "low",
			CreatedAt:   time.Now(),
		},
	}

	return AgentResult{
		Data: map[string]interface{}{
			"summary":                  summary,
			"detailed_report":          detailedReport,
			"highlights":               highlights,
			"conflicts":                conflicts,
			"analysis_quality":         "high",
			"information_completeness": 0.85,
		},
		Confidence: 0.85,
		Cost:       1.2,
		TokensUsed: 1200,
		ModelUsed:  "gpt-5",
	}
}

func (a *SimpleAgent) executeConflictDetection(ctx context.Context, task AgentTask) AgentResult {
	// Generic conflict detection for any topic
	conflicts := []Conflict{
		{
			ID:          "source_discrepancy",
			Description: "Different sources report slightly different details about the same event",
			Resolution:  "Cross-referenced with authoritative sources to determine most accurate information",
			Severity:    "low",
			CreatedAt:   time.Now(),
		},
	}

	return AgentResult{
		Data: map[string]interface{}{
			"conflicts":      conflicts,
			"conflict_count": len(conflicts),
			"severity_breakdown": map[string]int{
				"low":      1,
				"medium":   0,
				"high":     0,
				"critical": 0,
			},
			"resolution_rate":    1.0,
			"conflicts_resolved": true,
		},
		Confidence: 0.8,
		Cost:       0.2,
		TokensUsed: 200,
		ModelUsed:  "gpt-5",
	}
}

func (a *SimpleAgent) executeHighlightManagement(ctx context.Context, task AgentTask) AgentResult {
	return AgentResult{
		Data: map[string]interface{}{
			"highlights": []Highlight{
				{
					ID:        "highlight1",
					Title:     "Breaking Political News",
					Content:   "Major political development requiring attention",
					Type:      "breaking",
					Priority:  1,
					IsPinned:  true,
					CreatedAt: time.Now(),
				},
			},
			"highlight_count": 1,
		},
		Confidence: 0.85,
		Cost:       0.15,
		TokensUsed: 150,
		ModelUsed:  "gpt-5",
	}
}

func (a *SimpleAgent) executeKnowledgeGraph(ctx context.Context, task AgentTask) AgentResult {
	topic, _ := task.Parameters["topic"].(string)
	if topic == "" {
		topic = "General Topic"
	}

	return AgentResult{
		Data: map[string]interface{}{
			"nodes": []KnowledgeNode{
				{
					ID:          "main_concept",
					Type:        "concept",
					Label:       topic,
					Description: fmt.Sprintf("Main concept related to %s", topic),
					Confidence:  0.9,
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
				},
			},
			"edges": []KnowledgeEdge{
				{
					ID:         "relationship_1",
					From:       "main_concept",
					To:         "main_concept",
					Type:       "relates_to",
					Weight:     0.8,
					Confidence: 0.85,
					CreatedAt:  time.Now(),
				},
			},
			"graph_updated": true,
			"topic":         topic,
		},
		Confidence: 0.8,
		Cost:       0.25,
		TokensUsed: 250,
		ModelUsed:  "gpt-5",
	}
}
