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
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	ScheduleCron string      `json:"schedule_cron"`
	Preferences  Preferences `json:"preferences"`
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
