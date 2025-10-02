package server

// HTTPError is a generic error envelope returned by the server.
type HTTPError struct {
	Error string `json:"error"`
}

// AuthSignupRequest represents the signup payload.
type AuthSignupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// AuthLoginRequest represents the login payload.
type AuthLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// TokenResponse carries a bearer token.
type TokenResponse struct {
	Token string `json:"token"`
}

// IDResponse is a generic id response wrapper.
type IDResponse struct {
	ID string `json:"id"`
}

// MeResponse returns the current authenticated user id.
type MeResponse struct {
	UserID string `json:"user_id"`
}

type Preferences map[string]interface{}

// CreateTopicRequest represents a new topic payload.
type CreateTopicRequest struct {
	Name         string      `json:"name"`
	Preferences  Preferences `json:"preferences"`
	ScheduleCron string      `json:"schedule_cron"`
}

// TopicDetailResponse is a detailed topic view.
type TopicDetailResponse struct {
	ID            string                  `json:"id"`
	Name          string                  `json:"name"`
	ScheduleCron  string                  `json:"schedule_cron"`
	Preferences   Preferences             `json:"preferences"`
	Policy        *TemporalPolicyResponse `json:"temporal_policy,omitempty"`
	Budget        *BudgetConfigResponse   `json:"budget,omitempty"`
	PendingBudget *PendingApproval        `json:"pending_budget,omitempty"`
}

// TemporalPolicyResponse surfaces the temporal policy attached to a topic.
type TemporalPolicyResponse struct {
	RepeatMode         string                 `json:"repeat_mode"`
	RefreshInterval    string                 `json:"refresh_interval,omitempty"`
	DedupWindow        string                 `json:"dedup_window,omitempty"`
	FreshnessThreshold string                 `json:"freshness_threshold,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
}

// UpdateTemporalPolicyRequest updates the per-topic temporal policy.
type UpdateTemporalPolicyRequest struct {
	RepeatMode         string                 `json:"repeat_mode"`
	RefreshInterval    *string                `json:"refresh_interval,omitempty"`
	DedupWindow        *string                `json:"dedup_window,omitempty"`
	FreshnessThreshold *string                `json:"freshness_threshold,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
}

type BudgetConfigResponse struct {
	MaxCost           *float64               `json:"max_cost,omitempty"`
	MaxTokens         *int64                 `json:"max_tokens,omitempty"`
	MaxTimeSeconds    *int64                 `json:"max_time_seconds,omitempty"`
	ApprovalThreshold *float64               `json:"approval_threshold,omitempty"`
	RequireApproval   bool                   `json:"require_approval"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

type UpdateBudgetConfigRequest struct {
	MaxCost           *float64               `json:"max_cost,omitempty"`
	MaxTokens         *int64                 `json:"max_tokens,omitempty"`
	MaxTimeSeconds    *int64                 `json:"max_time_seconds,omitempty"`
	ApprovalThreshold *float64               `json:"approval_threshold,omitempty"`
	RequireApproval   *bool                  `json:"require_approval,omitempty"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

type PendingApproval struct {
	RunID         string  `json:"run_id"`
	EstimatedCost float64 `json:"estimated_cost"`
	Threshold     float64 `json:"threshold"`
	CreatedAt     string  `json:"created_at"`
	RequestedBy   string  `json:"requested_by"`
}

// ChatRequest is the request body for topic chat endpoints.
type ChatRequest struct {
	Message string `json:"message"`
}

// ChatResponse is the response for topic chat endpoints.
type ChatResponse struct {
	Message string                 `json:"message"`
	Topic   map[string]interface{} `json:"topic"`
}

// AssistRequest is the request for LLM assist endpoint.
type AssistRequest struct {
	Message      string      `json:"message"`
	Name         string      `json:"name"`
	Preferences  Preferences `json:"preferences"`
	ScheduleCron string      `json:"schedule_cron"`
}

// AssistResponse mirrors ChatResponse shape.
type AssistResponse = ChatResponse

// ExpandRequest asks server to generate deeper details for a run item
type ExpandRequest struct {
	HighlightIndex *int   `json:"highlight_index,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	Focus          string `json:"focus,omitempty"`
}

type ExpandResponse struct {
	Markdown string `json:"markdown"`
}

// ExpandAllRequest asks to generate a deep-dive markdown for the whole run
type ExpandAllRequest struct {
	GroupBy string `json:"group_by,omitempty"` // "type" (highlight type), "domain", "none", or "taxonomy"
	Focus   string `json:"focus,omitempty"`
}

type ExpandAllResponse = ExpandResponse

// Chat log models
type ChatLogMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}
