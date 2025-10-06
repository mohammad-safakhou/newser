package manifest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	core "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/store"
)

// RunManifestVersion identifies the current schema version of run manifests.
const RunManifestVersion = "v1"

// RunManifestPayload captures the immutable payload that is signed for a run.
type RunManifestPayload struct {
	Version   string            `json:"version"`
	RunID     string            `json:"run_id"`
	TopicID   string            `json:"topic_id"`
	UserID    string            `json:"user_id"`
	Thought   core.UserThought  `json:"thought"`
	Result    RunManifestResult `json:"result"`
	Sources   []ManifestSource  `json:"sources"`
	Plan      *ManifestPlan     `json:"plan,omitempty"`
	Steps     []ManifestStep    `json:"steps,omitempty"`
	Digest    map[string]any    `json:"digest,omitempty"`
	Budget    map[string]any    `json:"budget,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// RunManifestResult summarises the processing outcome for the manifest.
type RunManifestResult struct {
	Summary        string           `json:"summary"`
	DetailedReport string           `json:"detailed_report"`
	Confidence     float64          `json:"confidence"`
	Highlights     []core.Highlight `json:"highlights,omitempty"`
	Conflicts      []core.Conflict  `json:"conflicts,omitempty"`
	Items          []map[string]any `json:"items,omitempty"`
	AgentsUsed     []string         `json:"agents_used,omitempty"`
	ModelsUsed     []string         `json:"models_used,omitempty"`
	CostEstimate   float64          `json:"cost_estimate"`
	TokensUsed     int64            `json:"tokens_used"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
}

// ManifestSource captures source metadata embedded in the manifest.
type ManifestSource struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Domain      string    `json:"domain"`
	Snippet     string    `json:"snippet,omitempty"`
	Type        string    `json:"type,omitempty"`
	Credibility float64   `json:"credibility,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
}

// ManifestPlan includes plan context embedded with the manifest.
type ManifestPlan struct {
	Prompt   string                `json:"prompt,omitempty"`
	Document *planner.PlanDocument `json:"document,omitempty"`
}

// ManifestStep records execution metadata for each agent step.
type ManifestStep struct {
	StepIndex   int              `json:"step_index"`
	Task        core.AgentTask   `json:"task"`
	Result      core.AgentResult `json:"result"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
	Artifacts   []map[string]any `json:"artifacts,omitempty"`
}

// SignedRunManifest captures the payload along with checksum and signature metadata.
type SignedRunManifest struct {
	Manifest  RunManifestPayload `json:"manifest"`
	Checksum  string             `json:"checksum"`
	Signature string             `json:"signature"`
	Algorithm string             `json:"algorithm"`
	SignedAt  time.Time          `json:"signed_at"`
}

// BuildRunManifest constructs a manifest payload from the stored episode snapshot.
func BuildRunManifest(ep store.Episode) (RunManifestPayload, error) {
	if ep.RunID == "" || ep.TopicID == "" || ep.UserID == "" {
		return RunManifestPayload{}, fmt.Errorf("episode missing identifiers")
	}
	payload := RunManifestPayload{
		Version:   RunManifestVersion,
		RunID:     ep.RunID,
		TopicID:   ep.TopicID,
		UserID:    ep.UserID,
		Thought:   ep.Thought,
		CreatedAt: ep.CreatedAt.UTC(),
	}

	res := ep.Result
	if res.ID == "" {
		res.ID = ep.RunID
	}
	items := extractItems(res.Metadata)
	payload.Result = RunManifestResult{
		Summary:        res.Summary,
		DetailedReport: res.DetailedReport,
		Confidence:     res.Confidence,
		Highlights:     res.Highlights,
		Conflicts:      res.Conflicts,
		Items:          items,
		AgentsUsed:     res.AgentsUsed,
		ModelsUsed:     res.LLMModelsUsed,
		CostEstimate:   res.CostEstimate,
		TokensUsed:     res.TokensUsed,
		Metadata:       filterMetadata(res.Metadata),
	}
	if len(items) == 0 {
		payload.Result.Items = nil
	}

	sources := make([]ManifestSource, 0, len(res.Sources))
	for _, src := range res.Sources {
		if src.ID == "" {
			return RunManifestPayload{}, fmt.Errorf("source missing id for url %s", src.URL)
		}
		manifestSource := ManifestSource{
			ID:          src.ID,
			Title:       src.Title,
			URL:         src.URL,
			Domain:      sourceDomain(src.URL),
			Snippet:     trimSnippet(src.Summary, src.Content),
			Type:        src.Type,
			Credibility: src.Credibility,
		}
		if !src.PublishedAt.IsZero() {
			manifestSource.PublishedAt = src.PublishedAt
		}
		sources = append(sources, manifestSource)
	}
	payload.Sources = sources

	if ep.PlanDocument != nil || ep.PlanPrompt != "" {
		payload.Plan = &ManifestPlan{Prompt: ep.PlanPrompt, Document: ep.PlanDocument}
	}

	if len(ep.Steps) > 0 {
		steps := make([]ManifestStep, len(ep.Steps))
		for i, step := range ep.Steps {
			steps[i] = ManifestStep{
				StepIndex:   step.StepIndex,
				Task:        step.Task,
				Result:      step.Result,
				StartedAt:   step.StartedAt,
				CompletedAt: step.CompletedAt,
				Artifacts:   step.Artifacts,
			}
		}
		payload.Steps = steps
	}

	if stats, ok := res.Metadata["digest_stats"].(map[string]any); ok {
		payload.Digest = stats
	}
	if budgetMeta, ok := res.Metadata["budget_usage"].(map[string]any); ok {
		payload.Budget = budgetMeta
	}

	return payload, nil
}

// SignRunManifest signs the payload using the provided secret and returns the signed manifest.
func SignRunManifest(payload RunManifestPayload, secret string, signedAt time.Time) (SignedRunManifest, error) {
	if secret == "" {
		return SignedRunManifest{}, fmt.Errorf("signing secret required")
	}
	if signedAt.IsZero() {
		signedAt = time.Now().UTC()
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return SignedRunManifest{}, err
	}
	sum := sha256.Sum256(canonical)
	checksum := hex.EncodeToString(sum[:])

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(checksum))
	signature := hex.EncodeToString(mac.Sum(nil))

	return SignedRunManifest{
		Manifest:  payload,
		Checksum:  checksum,
		Signature: signature,
		Algorithm: "hmac-sha256",
		SignedAt:  signedAt.UTC(),
	}, nil
}

// VerifyRunManifest recomputes checksum/signature and ensures they match the stored values.
func VerifyRunManifest(signed SignedRunManifest, secret string) error {
	canonical, err := json.Marshal(signed.Manifest)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canonical)
	expectedChecksum := hex.EncodeToString(sum[:])
	if signed.Checksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed.Checksum))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expectedSignature), []byte(signed.Signature)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

func extractItems(meta map[string]any) []map[string]any {
	if meta == nil {
		return nil
	}
	raw, ok := meta["items"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []map[string]any:
		return v
	case []interface{}:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func filterMetadata(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	copy := make(map[string]any, len(meta))
	for k, v := range meta {
		if k == "items" || k == "digest_stats" || k == "budget_usage" {
			continue
		}
		copy[k] = v
	}
	if len(copy) == 0 {
		return nil
	}
	return copy
}

func sourceDomain(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return strings.ToLower(u.Host)
}

func trimSnippet(summary string, fallback string) string {
	source := strings.TrimSpace(summary)
	if source == "" {
		source = strings.TrimSpace(fallback)
	}
	if source == "" {
		return ""
	}
	const maxRunes = 280
	runes := []rune(source)
	if len(runes) <= maxRunes {
		return source
	}
	trimmed := strings.TrimSpace(string(runes[:maxRunes]))
	if strings.HasSuffix(trimmed, "…") || strings.HasSuffix(trimmed, "...") {
		return trimmed
	}
	return trimmed + "…"
}
