package core

import (
	"context"
	"time"

	"github.com/mohammad-safakhou/newser/internal/budget"
	planner "github.com/mohammad-safakhou/newser/internal/planner"
	policypkg "github.com/mohammad-safakhou/newser/internal/policy"
)

// UserThought represents a user's thought or request
type UserThought struct {
	ID          string                  `json:"id"`
	Content     string                  `json:"content"`
	Timestamp   time.Time               `json:"timestamp"`
	UserID      string                  `json:"user_id,omitempty"`
	TopicID     string                  `json:"topic_id,omitempty"`
	Preferences map[string]interface{}  `json:"preferences,omitempty"`
	Context     map[string]interface{}  `json:"context,omitempty"`
	Policy      *policypkg.UpdatePolicy `json:"temporal_policy,omitempty"`
	Budget      *budget.Config          `json:"budget,omitempty"`
}

// ProcessingResult represents the final result of processing a user thought
type ProcessingResult struct {
	ID             string                 `json:"id"`
	UserThought    UserThought            `json:"user_thought"`
	Summary        string                 `json:"summary"`
	DetailedReport string                 `json:"detailed_report"`
	Sources        []Source               `json:"sources"`
	Highlights     []Highlight            `json:"highlights"`
	Conflicts      []Conflict             `json:"conflicts,omitempty"`
	Confidence     float64                `json:"confidence"`
	ProcessingTime time.Duration          `json:"processing_time"`
	CostEstimate   float64                `json:"cost_estimate"`
	TokensUsed     int64                  `json:"tokens_used"`
	AgentsUsed     []string               `json:"agents_used"`
	LLMModelsUsed  []string               `json:"llm_models_used"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
}

// Source represents a source of information
type Source struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Type        string    `json:"type"`        // news, web, social, academic, etc.
	Credibility float64   `json:"credibility"` // 0.0 to 1.0
	PublishedAt time.Time `json:"published_at"`
	ExtractedAt time.Time `json:"extracted_at"`
	Content     string    `json:"content,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
}

// Highlight represents a key highlight or pinned information
type Highlight struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Type      string    `json:"type"`     // breaking, important, ongoing, etc.
	Priority  int       `json:"priority"` // 1-5, higher is more important
	Sources   []string  `json:"sources"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	IsPinned  bool      `json:"is_pinned"`
}

// Conflict represents conflicting information from different sources
type Conflict struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Sources     []Source  `json:"sources"`
	Resolution  string    `json:"resolution,omitempty"`
	Severity    string    `json:"severity"` // low, medium, high, critical
	CreatedAt   time.Time `json:"created_at"`
}

// AgentTask represents a task for an agent to execute
type AgentTask struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"` // research, analysis, synthesis, etc.
	Description string                 `json:"description"`
	Priority    int                    `json:"priority"`
	Parameters  map[string]interface{} `json:"parameters"`
	DependsOn   []string               `json:"depends_on,omitempty"`
	Timeout     time.Duration          `json:"timeout"`
	CreatedAt   time.Time              `json:"created_at"`
}

// AgentResult represents the result of an agent execution
type AgentResult struct {
	ID             string                 `json:"id"`
	TaskID         string                 `json:"task_id"`
	AgentType      string                 `json:"agent_type"`
	Success        bool                   `json:"success"`
	Data           map[string]interface{} `json:"data"`
	Sources        []Source               `json:"sources"`
	Confidence     float64                `json:"confidence"`
	Error          string                 `json:"error,omitempty"`
	ProcessingTime time.Duration          `json:"processing_time"`
	Cost           float64                `json:"cost"`
	TokensUsed     int64                  `json:"tokens_used"`
	ModelUsed      string                 `json:"model_used"`
	Prompt         string                 `json:"prompt,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
}

// PlanningResult represents the result of planning phase
type PlanningResult struct {
	Tasks                  []AgentTask             `json:"tasks"`
	ExecutionOrder         []string                `json:"execution_order"`
	EstimatedCost          float64                 `json:"estimated_cost"`
	EstimatedTime          time.Duration           `json:"estimated_time"`
	Confidence             float64                 `json:"confidence"`
	Budget                 *planner.PlanBudget     `json:"budget,omitempty"`
	Edges                  []planner.PlanEdge      `json:"edges,omitempty"`
	Estimates              *planner.PlanEstimates  `json:"estimates,omitempty"`
	Graph                  *planner.PlanDocument   `json:"graph,omitempty"`
	RawJSON                []byte                  `json:"-"`
	TemporalPolicy         *policypkg.UpdatePolicy `json:"temporal_policy,omitempty"`
	BudgetConfig           *budget.Config          `json:"-"`
	BudgetApprovalRequired bool                    `json:"budget_approval_required"`
	Prompt                 string                  `json:"-"`
}

// KnowledgeGraph represents the knowledge graph for a topic
type KnowledgeGraph struct {
	ID          string                 `json:"id"`
	Topic       string                 `json:"topic"`
	Nodes       []KnowledgeNode        `json:"nodes"`
	Edges       []KnowledgeEdge        `json:"edges"`
	LastUpdated time.Time              `json:"last_updated"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// KnowledgeNode represents a node in the knowledge graph
type KnowledgeNode struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"` // concept, entity, event, etc.
	Label       string                 `json:"label"`
	Description string                 `json:"description"`
	Confidence  float64                `json:"confidence"`
	Sources     []string               `json:"sources"`
	Properties  map[string]interface{} `json:"properties,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// KnowledgeEdge represents an edge in the knowledge graph
type KnowledgeEdge struct {
	ID         string                 `json:"id"`
	From       string                 `json:"from"`
	To         string                 `json:"to"`
	Type       string                 `json:"type"` // relates_to, causes, part_of, etc.
	Weight     float64                `json:"weight"`
	Confidence float64                `json:"confidence"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
}

// Agent interface defines the contract for all agents
type Agent interface {
	// Execute performs the agent's main task
	Execute(ctx context.Context, task AgentTask) (AgentResult, error)

	// GetCapabilities returns the agent's capabilities
	GetCapabilities() []string

	// GetConfidence returns the agent's confidence in handling a task
	GetConfidence(task AgentTask) float64

	// GetEstimatedCost returns estimated cost for a task
	GetEstimatedCost(task AgentTask) float64

	// GetEstimatedTime returns estimated time for a task
	GetEstimatedTime(task AgentTask) time.Duration
}

// PlannerInterface defines the contract for planning agents
type PlannerInterface interface {
	// Plan creates an execution plan for a user thought
	Plan(ctx context.Context, thought UserThought) (PlanningResult, error)

	// ValidatePlan validates if a plan is feasible
	ValidatePlan(plan PlanningResult) error

	// OptimizePlan optimizes a plan for cost/time/quality
	OptimizePlan(plan PlanningResult, constraints map[string]interface{}) (PlanningResult, error)
}

// OrchestratorInterface defines the contract for the main orchestrator
type OrchestratorInterface interface {
	// ProcessThought processes a user thought and returns results
	ProcessThought(ctx context.Context, thought UserThought) (ProcessingResult, error)

	// GetStatus returns the current status of processing
	GetStatus(thoughtID string) (ProcessingStatus, error)

	// CancelProcessing cancels ongoing processing
	CancelProcessing(thoughtID string) error

	// GetPerformanceMetrics returns performance metrics
	GetPerformanceMetrics() map[string]interface{}
}

// ProcessingStatus represents the status of processing a thought
type ProcessingStatus struct {
	ThoughtID      string                 `json:"thought_id"`
	Status         string                 `json:"status"`   // pending, planning, executing, completed, failed
	Progress       float64                `json:"progress"` // 0.0 to 1.0
	CurrentTask    string                 `json:"current_task,omitempty"`
	CompletedTasks int                    `json:"completed_tasks"`
	TotalTasks     int                    `json:"total_tasks"`
	EstimatedTime  time.Duration          `json:"estimated_time"`
	ElapsedTime    time.Duration          `json:"elapsed_time"`
	Error          string                 `json:"error,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	LastUpdated    time.Time              `json:"last_updated"`
	CreatedAt      time.Time              `json:"created_at"`
}

// LLMProvider interface defines the contract for LLM providers
type LLMProvider interface {
	// Generate generates text using the LLM
	Generate(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, error)

	// GenerateWithTokens generates text and returns token usage
	GenerateWithTokens(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, int64, int64, error)

	// Embed generates vector embeddings for the provided inputs.
	Embed(ctx context.Context, model string, input []string) ([][]float32, error)

	// GetAvailableModels returns available models
	GetAvailableModels() []string

	// GetModelInfo returns information about a specific model
	GetModelInfo(model string) (ModelInfo, error)

	// CalculateCost calculates the cost for a given number of tokens
	CalculateCost(inputTokens, outputTokens int64, model string) float64
}

// ModelInfo contains information about an LLM model
type ModelInfo struct {
	Name            string   `json:"name"`
	Provider        string   `json:"provider"`
	MaxTokens       int      `json:"max_tokens"`
	CostPer1KInput  float64  `json:"cost_per_1k_input"`
	CostPer1KOutput float64  `json:"cost_per_1k_output"`
	Capabilities    []string `json:"capabilities"`
	Description     string   `json:"description"`
}

// SourceProvider interface defines the contract for information sources
type SourceProvider interface {
	// Search searches for information
	Search(ctx context.Context, query string, options map[string]interface{}) ([]Source, error)

	// GetSource retrieves a specific source
	GetSource(ctx context.Context, sourceID string) (Source, error)

	// GetSourceTypes returns supported source types
	GetSourceTypes() []string

	// GetCredibility returns credibility score for a source
	GetCredibility(source Source) float64
}

// Storage interface defines the contract for data storage
type Storage interface {
	// SaveProcessingResult saves a processing result
	SaveProcessingResult(ctx context.Context, result ProcessingResult) error

	// GetProcessingResult retrieves a processing result
	GetProcessingResult(ctx context.Context, thoughtID string) (ProcessingResult, error)

	// SaveKnowledgeGraph saves a knowledge graph
	SaveKnowledgeGraph(ctx context.Context, graph KnowledgeGraph) error

	// GetKnowledgeGraph retrieves a knowledge graph
	GetKnowledgeGraph(ctx context.Context, topic string) (KnowledgeGraph, error)

	// SaveHighlight saves a highlight
	SaveHighlight(ctx context.Context, highlight Highlight) error

	// GetHighlights retrieves highlights for a topic
	GetHighlights(ctx context.Context, topic string) ([]Highlight, error)

	// UpdateHighlight updates a highlight
	UpdateHighlight(ctx context.Context, highlight Highlight) error

	// DeleteHighlight deletes a highlight
	DeleteHighlight(ctx context.Context, highlightID string) error
}

// PlanRepository persists validated planner graphs for later inspection or replay.
type PlanRepository interface {
	SavePlanGraph(ctx context.Context, thoughtID string, doc *planner.PlanDocument, raw []byte) (string, error)
	GetLatestPlanGraph(ctx context.Context, thoughtID string) (StoredPlanGraph, bool, error)
}

// StoredPlanGraph returns the durable representation of a plan graph.
type StoredPlanGraph struct {
	PlanID    string
	ThoughtID string
	Document  *planner.PlanDocument
	RawJSON   []byte
	UpdatedAt time.Time
}

// EpisodeRepository persists episodic snapshots for replay and auditing.
type EpisodeRepository interface {
	SaveEpisode(ctx context.Context, snapshot EpisodicSnapshot) error
}

// EpisodicSnapshot captures a run trace suitable for episodic memory storage.
type EpisodicSnapshot struct {
	RunID        string
	TopicID      string
	UserID       string
	Thought      UserThought
	PlanDocument *planner.PlanDocument
	PlanRaw      []byte
	PlanPrompt   string
	Result       ProcessingResult
	Steps        []EpisodicStep
}

// EpisodicStep represents a single agent execution within an episodic snapshot.
type EpisodicStep struct {
	StepIndex     int
	Task          AgentTask
	InputSnapshot map[string]interface{}
	Prompt        string
	Result        AgentResult
	Artifacts     []map[string]interface{}
	StartedAt     time.Time
	CompletedAt   time.Time
}
