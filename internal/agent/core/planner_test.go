package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	"github.com/mohammad-safakhou/newser/internal/capability"
	plannerv1 "github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/policy"
)

func TestValidatePlanRejectsUnknownType(t *testing.T) {
	secret := "secret"
	signed := signAll(t, capability.DefaultToolCards(), secret)
	reg, err := capability.NewRegistry(signed, secret, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	planner := NewPlanner(&config.Config{}, stubLLM{}, telemetry.NewTelemetry(config.TelemetryConfig{}), reg)

	plan := PlanningResult{
		Tasks: []AgentTask{
			{ID: "t1", Type: "research"},
			{ID: "t2", Type: "unknown"},
			{ID: "t3", Type: "knowledge_graph", DependsOn: []string{"t1"}},
			{ID: "t4", Type: "synthesis", DependsOn: []string{"t1"}},
		},
		ExecutionOrder: []string{"t1", "t2", "t3", "t4"},
		EstimatedCost:  1.0,
		EstimatedTime:  time.Minute,
	}

	err = planner.ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "invalid task type") {
		t.Fatalf("expected invalid task type error, got %v", err)
	}
}

func TestValidatePlanAllowsRegisteredTypes(t *testing.T) {
	secret := "secret"
	signed := signAll(t, capability.DefaultToolCards(), secret)
	reg, err := capability.NewRegistry(signed, secret, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	planner := NewPlanner(&config.Config{}, stubLLM{}, telemetry.NewTelemetry(config.TelemetryConfig{}), reg)

	plan := PlanningResult{
		Tasks: []AgentTask{
			{ID: "t1", Type: "research"},
			{ID: "t2", Type: "analysis", DependsOn: []string{"t1"}},
			{ID: "t3", Type: "knowledge_graph", DependsOn: []string{"t1", "t2"}},
			{ID: "t4", Type: "synthesis", DependsOn: []string{"t1", "t2", "t3"}},
		},
		ExecutionOrder: []string{"t1", "t2", "t3", "t4"},
		EstimatedCost:  1.0,
		EstimatedTime:  time.Minute,
	}

	if err := planner.ValidatePlan(plan); err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
}

func TestParsePlanningResponseValidatesSchema(t *testing.T) {
	cfg := &config.Config{Agents: config.AgentsConfig{AgentTimeout: time.Minute}}
	planner := NewPlanner(cfg, stubLLM{}, telemetry.NewTelemetry(config.TelemetryConfig{}), nil)

	if _, err := planner.parsePlanningResponse("{\"version\":\"v1\"}"); err == nil {
		t.Fatalf("expected schema validation failure")
	}
}

func TestParsePlanningResponseSuccess(t *testing.T) {
	cfg := &config.Config{Agents: config.AgentsConfig{AgentTimeout: time.Minute}}
	planner := NewPlanner(cfg, stubLLM{}, telemetry.NewTelemetry(config.TelemetryConfig{}), nil)

	payload := `Plan proposal:
{"version":"v1","tasks":[{"id":"t1","type":"research"}],"execution_order":["t1"],"estimates":{"total_cost":2.5,"total_time":"7m"},"budget":{"max_cost":5}}`
	plan, err := planner.parsePlanningResponse(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Graph == nil {
		t.Fatalf("expected plan graph to be captured")
	}
	if plan.EstimatedCost != 2.5 {
		t.Fatalf("expected estimated cost 2.5, got %v", plan.EstimatedCost)
	}
	if plan.Budget == nil || plan.Budget.MaxCost != 5 {
		t.Fatalf("expected budget to be populated")
	}
}

func TestCreatePlanningPromptIncludesSchemaGuidance(t *testing.T) {
	pl := NewPlanner(&config.Config{}, stubLLM{}, telemetry.NewTelemetry(config.TelemetryConfig{}), nil)
	prompt := pl.createPlanningPrompt(UserThought{Content: "Assess semiconductor supply news"}, "")
	for _, snippet := range []string{"SCHEMA RULES", "VALID OUTPUT EXAMPLE", "\"tasks\""} {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("prompt missing snippet %q", snippet)
		}
	}
}

func TestCreatePlanningPromptIncludesPolicyGuidance(t *testing.T) {
	pl := NewPlanner(&config.Config{}, stubLLM{}, telemetry.NewTelemetry(config.TelemetryConfig{}), nil)
	pol := policy.UpdatePolicy{
		RefreshInterval:    2 * time.Hour,
		DedupWindow:        6 * time.Hour,
		RepeatMode:         policy.RepeatModeManual,
		FreshnessThreshold: 12 * time.Hour,
		Metadata: map[string]interface{}{
			"channels": []string{"rss"},
		},
	}
	prompt := pl.createPlanningPrompt(UserThought{Content: "Assess semiconductor supply news", Policy: &pol}, "")
	checks := []string{
		"Repeat mode: manual",
		"Refresh interval between runs: 2h0m0s",
		"Deduplicate content seen within: 6h0m0s",
	}
	for _, snippet := range checks {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("prompt missing policy snippet %q", snippet)
		}
	}
	if !strings.Contains(prompt, "Additional policy metadata") {
		t.Fatalf("prompt missing metadata block: %s", prompt)
	}
}

func TestAttachTemporalPolicyEmbedsMetadata(t *testing.T) {
	plan := PlanningResult{
		Tasks: []AgentTask{
			{ID: "t1", Type: "research"},
			{ID: "t2", Type: "analysis"},
			{ID: "t3", Type: "synthesis"},
		},
		Graph: &plannerv1.PlanDocument{
			Metadata: map[string]interface{}{"existing": "value"},
			Tasks: []plannerv1.PlanTask{
				{ID: "t1", Type: "research"},
				{ID: "t2", Type: "analysis"},
				{ID: "t3", Type: "synthesis"},
			},
		},
		RawJSON:        []byte(`{"metadata":{"existing":"value"}}`),
		ExecutionOrder: []string{"t1", "t2", "t3"},
	}
	pol := policy.UpdatePolicy{
		RefreshInterval:    time.Hour,
		DedupWindow:        2 * time.Hour,
		RepeatMode:         policy.RepeatModeAdaptive,
		FreshnessThreshold: 3 * time.Hour,
		Metadata:           map[string]interface{}{"channels": []string{"rss"}},
	}

	attachTemporalPolicy(&plan, &pol)

	if plan.TemporalPolicy == nil {
		t.Fatalf("expected temporal policy to be attached")
	}
	if plan.TemporalPolicy == &pol {
		t.Fatalf("expected planner to clone policy to avoid aliasing")
	}
	if plan.TemporalPolicy.RepeatMode != pol.RepeatMode {
		t.Fatalf("repeat mode mismatch: %s", plan.TemporalPolicy.RepeatMode)
	}

	meta, ok := plan.Graph.Metadata["temporal_policy"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected temporal_policy metadata, got %#v", plan.Graph.Metadata["temporal_policy"])
	}
	if meta["repeat_mode"] != string(pol.RepeatMode) {
		t.Fatalf("metadata repeat_mode mismatch: %#v", meta)
	}
	if meta["refresh_interval"] != pol.RefreshInterval.String() {
		t.Fatalf("metadata refresh interval mismatch: %#v", meta)
	}
	if plan.Tasks[0].Parameters["dedup_window_seconds"].(int64) != int64(pol.DedupWindow/time.Second) {
		t.Fatalf("expected dedup window seconds on research task: %#v", plan.Tasks[0].Parameters)
	}
	if plan.Tasks[1].Parameters["freshness_threshold_seconds"].(int64) != int64(pol.FreshnessThreshold/time.Second) {
		t.Fatalf("expected freshness threshold on analysis task: %#v", plan.Tasks[1].Parameters)
	}
	if !strings.Contains(string(plan.RawJSON), "temporal_policy") {
		t.Fatalf("raw JSON should include temporal_policy metadata: %s", string(plan.RawJSON))
	}
}

func TestPlannerAnnotateProceduralTemplatesCandidate(t *testing.T) {
	repo := &templateRepoStub{
		upsertStates: []TemplateFingerprintState{
			{Occurrences: 1},
		},
	}
	pl := NewPlanner(&config.Config{}, stubLLM{}, telemetry.NewTelemetry(config.TelemetryConfig{}), nil)
	pl.SetTemplateRepository(repo)

	plan := PlanningResult{
		Graph: &plannerv1.PlanDocument{
			Tasks: []plannerv1.PlanTask{
				{ID: "t1", Type: "research"},
				{ID: "t2", Type: "analysis", DependsOn: []string{"t1"}},
			},
			ExecutionLayers: []plannerv1.PlanStage{
				{Stage: "stage-01", Tasks: []string{"t1", "t2"}},
			},
		},
	}
	pl.annotateProceduralTemplates(context.Background(), UserThought{TopicID: "topic-1"}, &plan)
	items, ok := plan.Graph.Metadata["procedural_templates"].([]map[string]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("expected procedural templates metadata, got %#v", plan.Graph.Metadata["procedural_templates"])
	}
	entry := items[0]
	if entry["status"] != "candidate" {
		t.Fatalf("expected candidate status, got %#v", entry["status"])
	}
	if _, exists := entry["template_id"]; exists {
		t.Fatalf("did not expect template id for candidate")
	}
	if repo.createCount > 0 {
		t.Fatalf("unexpected template creation")
	}
}

func TestPlannerAnnotateProceduralTemplatesCreatesTemplate(t *testing.T) {
	repo := &templateRepoStub{
		upsertStates: []TemplateFingerprintState{
			{Occurrences: templateSuggestionThreshold},
		},
		createResults: []ProceduralTemplate{
			{
				ID:             "tpl-1",
				CurrentVersion: 1,
				Version: ProceduralTemplateVersion{
					Version: 1,
					Status:  templateStatusPendingApproval,
				},
			},
		},
	}
	pl := NewPlanner(&config.Config{}, stubLLM{}, telemetry.NewTelemetry(config.TelemetryConfig{}), nil)
	pl.SetTemplateRepository(repo)

	doc := &plannerv1.PlanDocument{
		Tasks: []plannerv1.PlanTask{
			{ID: "t1", Type: "research"},
			{ID: "t2", Type: "analysis", DependsOn: []string{"t1"}},
		},
		ExecutionLayers: []plannerv1.PlanStage{
			{Stage: "stage-01", Tasks: []string{"t1", "t2"}},
		},
	}
	plan := PlanningResult{Graph: doc}
	pl.annotateProceduralTemplates(context.Background(), UserThought{TopicID: "topic-1"}, &plan)

	items := plan.Graph.Metadata["procedural_templates"].([]map[string]interface{})
	entry := items[0]
	if entry["status"] != templateStatusPendingApproval {
		t.Fatalf("expected pending approval status, got %#v", entry["status"])
	}
	if entry["template_id"] != "tpl-1" {
		t.Fatalf("expected template id tpl-1, got %#v", entry["template_id"])
	}
	if repo.linkCount != 1 {
		t.Fatalf("expected fingerprint link to be recorded")
	}
}

func TestPlannerAnnotateProceduralTemplatesReuse(t *testing.T) {
	repo := &templateRepoStub{
		upsertStates: []TemplateFingerprintState{
			{Occurrences: 5, TemplateID: "tpl-1"},
		},
	}
	pl := NewPlanner(&config.Config{}, stubLLM{}, telemetry.NewTelemetry(config.TelemetryConfig{}), nil)
	pl.SetTemplateRepository(repo)
	doc := &plannerv1.PlanDocument{
		Tasks: []plannerv1.PlanTask{
			{ID: "t1", Type: "research"},
			{ID: "t2", Type: "analysis", DependsOn: []string{"t1"}},
		},
		ExecutionLayers: []plannerv1.PlanStage{
			{Stage: "stage-01", Tasks: []string{"t1", "t2"}},
		},
	}
	plan := PlanningResult{Graph: doc}
	pl.annotateProceduralTemplates(context.Background(), UserThought{TopicID: "topic-1"}, &plan)

	items := plan.Graph.Metadata["procedural_templates"].([]map[string]interface{})
	entry := items[0]
	if entry["status"] != "reused" {
		t.Fatalf("expected reused status, got %#v", entry["status"])
	}
	if val, ok := entry["template_id"]; !ok || val != "tpl-1" {
		t.Fatalf("expected template id tpl-1, got %#v", val)
	}
	if repo.createCount != 0 {
		t.Fatalf("template should not be created on reuse")
	}
	if len(repo.usageRecords) != 1 {
		t.Fatalf("expected usage to be recorded, got %d", len(repo.usageRecords))
	}
}

type semanticMemoryStub struct {
	lastRequest SemanticSearchRequest
	results     SemanticSearchResults
}

func (s *semanticMemoryStub) SearchSimilar(ctx context.Context, req SemanticSearchRequest) (SemanticSearchResults, error) {
	s.lastRequest = req
	return s.results, nil
}

type templateRepoStub struct {
	upsertStates   []TemplateFingerprintState
	createResults  []ProceduralTemplate
	createRequests []TemplateCreationRequest
	usageRecords   []TemplateUsageRecord
	linkCount      int
	stateIndex     int
	createCount    int
}

func (s *templateRepoStub) UpsertFingerprint(ctx context.Context, req TemplateFingerprint) (TemplateFingerprintState, error) {
	if s.stateIndex < len(s.upsertStates) {
		st := s.upsertStates[s.stateIndex]
		s.stateIndex++
		if st.TopicID == "" {
			st.TopicID = req.TopicID
		}
		if st.Fingerprint == "" {
			st.Fingerprint = req.Fingerprint
		}
		return st, nil
	}
	return TemplateFingerprintState{
		TopicID:     req.TopicID,
		Fingerprint: req.Fingerprint,
		Occurrences: 1,
	}, nil
}

func (s *templateRepoStub) ListFingerprints(ctx context.Context, topicID string, limit int) ([]TemplateFingerprintState, error) {
	return nil, nil
}

func (s *templateRepoStub) CreateTemplate(ctx context.Context, req TemplateCreationRequest) (ProceduralTemplate, error) {
	s.createRequests = append(s.createRequests, req)
	if s.createCount < len(s.createResults) {
		out := s.createResults[s.createCount]
		if out.ID == "" {
			out.ID = fmt.Sprintf("tpl-%d", s.createCount+1)
		}
		if out.Version.Version == 0 {
			out.Version.Version = 1
		}
		if out.Version.Status == "" {
			out.Version.Status = templateStatusPendingApproval
		}
		if out.CurrentVersion == 0 {
			out.CurrentVersion = out.Version.Version
		}
		s.createCount++
		return out, nil
	}
	s.createCount++
	return ProceduralTemplate{
		ID:             fmt.Sprintf("tpl-%d", s.createCount),
		CurrentVersion: 1,
		Version: ProceduralTemplateVersion{
			Version: 1,
			Status:  templateStatusPendingApproval,
		},
	}, nil
}

func (s *templateRepoStub) LinkFingerprint(ctx context.Context, topicID, fingerprint, templateID string) error {
	s.linkCount++
	return nil
}

func (s *templateRepoStub) RecordUsage(ctx context.Context, usage TemplateUsageRecord) error {
	s.usageRecords = append(s.usageRecords, usage)
	return nil
}

func (s *templateRepoStub) GetTemplateMetrics(ctx context.Context, templateID string) (TemplateMetrics, bool, error) {
	return TemplateMetrics{}, false, nil
}

type recordingLLM struct {
	lastPrompt string
}

func (r *recordingLLM) Generate(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, error) {
	r.lastPrompt = prompt
	return `{"version":"v1","tasks":[{"id":"t1","type":"research"},{"id":"t2","type":"knowledge_graph","depends_on":["t1"]},{"id":"t3","type":"synthesis","depends_on":["t1","t2"]}],"execution_order":["t1","t2","t3"]}`, nil
}

func (r *recordingLLM) GenerateWithTokens(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, int64, int64, error) {
	resp, err := r.Generate(ctx, prompt, model, options)
	return resp, 0, 0, err
}

func (*recordingLLM) Embed(ctx context.Context, model string, input []string) ([][]float32, error) {
	return nil, nil
}

func (*recordingLLM) GetAvailableModels() []string { return []string{"stub"} }

func (*recordingLLM) GetModelInfo(model string) (ModelInfo, error) {
	return ModelInfo{Name: model}, nil
}

func (*recordingLLM) CalculateCost(inputTokens, outputTokens int64, model string) float64 { return 0 }

func TestPlanIntegratesSemanticMemory(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{AgentTimeout: time.Minute},
		Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{Enabled: true, SearchTopK: 4, SearchThreshold: 0.7}},
	}
	llm := &recordingLLM{}
	planner := NewPlanner(cfg, llm, telemetry.NewTelemetry(config.TelemetryConfig{}), nil)
	semStub := &semanticMemoryStub{
		results: SemanticSearchResults{
			Runs: []SemanticRunMatch{
				{RunID: "run-42", TopicID: "topic-123", Kind: "run_summary", Distance: 0.25, Similarity: 0.75, Metadata: map[string]interface{}{"summary_snippet": "Latest inflation snapshot"}, CreatedAt: time.Now()},
			},
			PlanSteps: []SemanticPlanMatch{
				{RunID: "run-44", TopicID: "topic-123", TaskID: "t-plan", Kind: "analysis", Distance: 0.3, Similarity: 0.7, Metadata: map[string]interface{}{"description": "Compare CPI vs PPI"}, CreatedAt: time.Now()},
			},
		},
	}
	planner.SetSemanticMemory(semStub)
	thought := UserThought{Content: "Investigate inflation trends", TopicID: "topic-123"}
	plan, err := planner.Plan(context.Background(), thought)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if semStub.lastRequest.TopicID != thought.TopicID {
		t.Fatalf("expected topic ID %s, got %s", thought.TopicID, semStub.lastRequest.TopicID)
	}
	if semStub.lastRequest.Query == "" {
		t.Fatalf("expected semantic query to be populated")
	}
	if plan.Semantic == nil || len(plan.Semantic.Runs) == 0 {
		t.Fatalf("expected semantic run matches in plan result")
	}
	if plan.Graph == nil || plan.Graph.Metadata == nil {
		t.Fatalf("expected plan graph metadata to be present")
	}
	meta, ok := plan.Graph.Metadata["semantic_context"].(map[string]interface{})
	if !ok || len(meta) == 0 {
		t.Fatalf("expected semantic_context metadata, got %#v", plan.Graph.Metadata["semantic_context"])
	}
	if !strings.Contains(llm.lastPrompt, "SEMANTIC MEMORY HINTS") {
		t.Fatalf("expected prompt to include semantic memory hints, got: %s", llm.lastPrompt)
	}
}
