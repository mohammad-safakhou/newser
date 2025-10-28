package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type templateStore interface {
	UpsertProceduralTemplateFingerprint(ctx context.Context, rec store.ProceduralTemplateFingerprintRecord) (store.ProceduralTemplateFingerprintRecord, error)
	ListProceduralTemplateFingerprints(ctx context.Context, topicID string, limit int) ([]store.ProceduralTemplateFingerprintRecord, error)
	CreateProceduralTemplate(ctx context.Context, rec store.ProceduralTemplateRecord) (store.ProceduralTemplateRecord, error)
	CreateProceduralTemplateVersion(ctx context.Context, rec store.ProceduralTemplateVersionRecord) (store.ProceduralTemplateVersionRecord, error)
	LinkProceduralTemplateFingerprint(ctx context.Context, topicID, fingerprint, templateID string) error
	RecordProceduralTemplateUsage(ctx context.Context, rec store.ProceduralTemplateUsageRecord) (store.ProceduralTemplateUsageRecord, error)
	GetProceduralTemplateMetrics(ctx context.Context, templateID string) (store.ProceduralTemplateMetricsRecord, bool, error)
}

type templateRepository struct {
	store  templateStore
	logger *log.Logger
}

func newTemplateRepository(st *store.Store, logger *log.Logger) agentcore.ProceduralTemplateRepository {
	if st == nil {
		return nil
	}
	if logger == nil {
		logger = log.New(log.Writer(), "[TEMPLATES] ", log.LstdFlags)
	}
	return &templateRepository{store: st, logger: logger}
}

func (r *templateRepository) UpsertFingerprint(ctx context.Context, req agentcore.TemplateFingerprint) (agentcore.TemplateFingerprintState, error) {
	if r.store == nil {
		return agentcore.TemplateFingerprintState{}, fmt.Errorf("template store unavailable")
	}
	rec := store.ProceduralTemplateFingerprintRecord{
		TopicID:      strings.TrimSpace(req.TopicID),
		Fingerprint:  strings.TrimSpace(req.Fingerprint),
		SampleGraph:  append(json.RawMessage(nil), req.SampleGraph...),
		SampleParams: append(json.RawMessage(nil), req.SampleParameters...),
		Metadata:     req.Metadata,
	}
	stored, err := r.store.UpsertProceduralTemplateFingerprint(ctx, rec)
	if err != nil {
		return agentcore.TemplateFingerprintState{}, err
	}
	return agentcore.TemplateFingerprintState{
		TopicID:          stored.TopicID,
		Fingerprint:      stored.Fingerprint,
		Occurrences:      stored.Occurrences,
		TemplateID:       stored.TemplateID,
		LastSeenAt:       stored.LastSeenAt,
		SampleGraph:      append(json.RawMessage(nil), stored.SampleGraph...),
		SampleParameters: append(json.RawMessage(nil), stored.SampleParams...),
		Metadata:         stored.Metadata,
	}, nil
}

func (r *templateRepository) ListFingerprints(ctx context.Context, topicID string, limit int) ([]agentcore.TemplateFingerprintState, error) {
	if r.store == nil {
		return nil, fmt.Errorf("template store unavailable")
	}
	items, err := r.store.ListProceduralTemplateFingerprints(ctx, topicID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]agentcore.TemplateFingerprintState, len(items))
	for i, rec := range items {
		out[i] = agentcore.TemplateFingerprintState{
			TopicID:          rec.TopicID,
			Fingerprint:      rec.Fingerprint,
			Occurrences:      rec.Occurrences,
			TemplateID:       rec.TemplateID,
			LastSeenAt:       rec.LastSeenAt,
			SampleGraph:      append(json.RawMessage(nil), rec.SampleGraph...),
			SampleParameters: append(json.RawMessage(nil), rec.SampleParams...),
			Metadata:         rec.Metadata,
		}
	}
	return out, nil
}

func (r *templateRepository) CreateTemplate(ctx context.Context, req agentcore.TemplateCreationRequest) (agentcore.ProceduralTemplate, error) {
	if r.store == nil {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("template store unavailable")
	}
	if strings.TrimSpace(req.TopicID) == "" {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("topic_id required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("template name required")
	}
	if len(req.Graph) == 0 {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("graph payload required")
	}

	templateRec, err := r.store.CreateProceduralTemplate(ctx, store.ProceduralTemplateRecord{
		TopicID:     strings.TrimSpace(req.TopicID),
		Name:        strings.TrimSpace(req.Name),
		Description: req.Description,
		CreatedBy:   req.CreatedBy,
	})
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}

	meta := req.Metadata
	if meta == nil {
		meta = map[string]interface{}{}
	}
	if req.Fingerprint != "" {
		meta["fingerprint"] = req.Fingerprint
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("marshal metadata: %w", err)
	}
	status := req.Status
	if strings.TrimSpace(status) == "" {
		status = store.ProceduralTemplateStatusPendingApproval
	}

	versionRec, err := r.store.CreateProceduralTemplateVersion(ctx, store.ProceduralTemplateVersionRecord{
		TemplateID: templateRec.ID,
		Status:     status,
		Graph:      append(json.RawMessage(nil), req.Graph...),
		Parameters: append(json.RawMessage(nil), req.Parameters...),
		Metadata:   json.RawMessage(metaBytes),
		Changelog:  req.Changelog,
	})
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}

	var versionMeta map[string]interface{}
	if len(versionRec.Metadata) > 0 {
		if err := json.Unmarshal(versionRec.Metadata, &versionMeta); err != nil {
			if r.logger != nil {
				r.logger.Printf("warn: decode template metadata %s:%d: %v", templateRec.ID, versionRec.Version, err)
			}
			versionMeta = map[string]interface{}{}
		}
	} else {
		versionMeta = map[string]interface{}{}
	}

	result := agentcore.ProceduralTemplate{
		ID:             templateRec.ID,
		TopicID:        templateRec.TopicID,
		Name:           templateRec.Name,
		Description:    templateRec.Description,
		CurrentVersion: templateRec.CurrentVersion,
		CreatedBy:      templateRec.CreatedBy,
		CreatedAt:      templateRec.CreatedAt,
		UpdatedAt:      templateRec.UpdatedAt,
		Version: agentcore.ProceduralTemplateVersion{
			ID:         versionRec.ID,
			Version:    versionRec.Version,
			Status:     versionRec.Status,
			Graph:      append(json.RawMessage(nil), versionRec.Graph...),
			Parameters: append(json.RawMessage(nil), versionRec.Parameters...),
			Metadata:   versionMeta,
			Changelog:  versionRec.Changelog,
			ApprovedBy: versionRec.ApprovedBy,
			ApprovedAt: versionRec.ApprovedAt,
			CreatedAt:  versionRec.CreatedAt,
		},
	}
	return result, nil
}

func (r *templateRepository) LinkFingerprint(ctx context.Context, topicID, fingerprint, templateID string) error {
	if r.store == nil {
		return fmt.Errorf("template store unavailable")
	}
	return r.store.LinkProceduralTemplateFingerprint(ctx, topicID, fingerprint, templateID)
}

func (r *templateRepository) RecordUsage(ctx context.Context, usage agentcore.TemplateUsageRecord) error {
	if r.store == nil {
		return fmt.Errorf("template store unavailable")
	}
	_, err := r.store.RecordProceduralTemplateUsage(ctx, store.ProceduralTemplateUsageRecord{
		RunID:           usage.RunID,
		TopicID:         usage.TopicID,
		TemplateID:      usage.TemplateID,
		TemplateVersion: usage.TemplateVersion,
		Fingerprint:     usage.Fingerprint,
		Stage:           usage.Stage,
		Success:         usage.Success,
		LatencyMS:       usage.LatencyMS,
		Metadata:        usage.Metadata,
	})
	return err
}

func (r *templateRepository) GetTemplateMetrics(ctx context.Context, templateID string) (agentcore.TemplateMetrics, bool, error) {
	if r.store == nil {
		return agentcore.TemplateMetrics{}, false, fmt.Errorf("template store unavailable")
	}
	rec, ok, err := r.store.GetProceduralTemplateMetrics(ctx, templateID)
	if err != nil || !ok {
		return agentcore.TemplateMetrics{}, ok, err
	}
	return agentcore.TemplateMetrics{
		TemplateID:   rec.TemplateID,
		UsageCount:   rec.UsageCount,
		SuccessCount: rec.SuccessCount,
		TotalLatency: rec.TotalLatency,
		LastUsedAt:   rec.LastUsedAt,
	}, true, nil
}
