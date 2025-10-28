package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/memory/service"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type storeAPI interface {
	ListProceduralTemplateFingerprints(ctx context.Context, topicID string, limit int) ([]store.ProceduralTemplateFingerprintRecord, error)
	CreateProceduralTemplate(ctx context.Context, rec store.ProceduralTemplateRecord) (store.ProceduralTemplateRecord, error)
	CreateProceduralTemplateVersion(ctx context.Context, rec store.ProceduralTemplateVersionRecord) (store.ProceduralTemplateVersionRecord, error)
	LinkProceduralTemplateFingerprint(ctx context.Context, topicID, fingerprint, templateID string) error
	ListProceduralTemplates(ctx context.Context, topicID string) ([]store.ProceduralTemplateRecord, error)
	ListProceduralTemplateVersions(ctx context.Context, templateID string) ([]store.ProceduralTemplateVersionRecord, error)
}

type Manager struct {
	store  storeAPI
	logger *log.Logger
}

func NewManager(st storeAPI, logger *log.Logger) *Manager {
	if st == nil {
		return nil
	}
	if logger == nil {
		logger = log.New(log.Writer(), "[TEMPLATES] ", log.LstdFlags)
	}
	return &Manager{store: st, logger: logger}
}

func (m *Manager) ListFingerprints(ctx context.Context, topicID string, limit int) ([]agentcore.TemplateFingerprintState, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("template manager unavailable")
	}
	recs, err := m.store.ListProceduralTemplateFingerprints(ctx, topicID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]agentcore.TemplateFingerprintState, 0, len(recs))
	for _, rec := range recs {
		out = append(out, agentcore.TemplateFingerprintState{
			TopicID:          rec.TopicID,
			Fingerprint:      rec.Fingerprint,
			Occurrences:      rec.Occurrences,
			TemplateID:       rec.TemplateID,
			LastSeenAt:       rec.LastSeenAt,
			SampleGraph:      append(json.RawMessage(nil), rec.SampleGraph...),
			SampleParameters: append(json.RawMessage(nil), rec.SampleParams...),
			Metadata:         rec.Metadata,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TopicID == out[j].TopicID {
			if out[i].Occurrences == out[j].Occurrences {
				return out[i].Fingerprint < out[j].Fingerprint
			}
			return out[i].Occurrences > out[j].Occurrences
		}
		return out[i].TopicID < out[j].TopicID
	})
	return out, nil
}

func (m *Manager) PromoteFingerprint(ctx context.Context, req service.TemplatePromotionRequest) (agentcore.ProceduralTemplate, error) {
	if m == nil || m.store == nil {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("template manager unavailable")
	}
	topicID := strings.TrimSpace(req.TopicID)
	fingerprint := strings.TrimSpace(req.Fingerprint)
	if topicID == "" || fingerprint == "" {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("topic_id and fingerprint required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		if len(fingerprint) >= 8 {
			name = fmt.Sprintf("Template %s", fingerprint[:8])
		} else {
			name = fmt.Sprintf("Template %s", fingerprint)
		}
	}

	states, err := m.store.ListProceduralTemplateFingerprints(ctx, topicID, 100)
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}
	var fingerprintRec *store.ProceduralTemplateFingerprintRecord
	for i := range states {
		if states[i].Fingerprint == fingerprint {
			fingerprintRec = &states[i]
			break
		}
	}
	if fingerprintRec == nil {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("fingerprint %s not found", fingerprint)
	}
	if fingerprintRec.TemplateID != "" {
		templates, err := m.listTemplatesInternal(ctx, topicID)
		if err != nil {
			return agentcore.ProceduralTemplate{}, err
		}
		for _, tpl := range templates {
			if tpl.ID == fingerprintRec.TemplateID {
				return tpl, nil
			}
		}
	}

	templateRec, err := m.store.CreateProceduralTemplate(ctx, store.ProceduralTemplateRecord{
		TopicID:     topicID,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		CreatedBy:   strings.TrimSpace(req.CreatedBy),
	})
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}

	meta := req.Metadata
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta["fingerprint"] = fingerprint
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("marshal metadata: %w", err)
	}

	versionRec, err := m.store.CreateProceduralTemplateVersion(ctx, store.ProceduralTemplateVersionRecord{
		TemplateID: templateRec.ID,
		Status:     store.ProceduralTemplateStatusPendingApproval,
		Graph:      append(json.RawMessage(nil), fingerprintRec.SampleGraph...),
		Parameters: append(json.RawMessage(nil), fingerprintRec.SampleParams...),
		Metadata:   json.RawMessage(metaBytes),
		Changelog:  fmt.Sprintf("Promoted from fingerprint %s", shortFingerprint(fingerprint)),
	})
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}

	if err := m.store.LinkProceduralTemplateFingerprint(ctx, topicID, fingerprint, templateRec.ID); err != nil {
		if m.logger != nil {
			m.logger.Printf("warn: link fingerprint %s -> template %s: %v", fingerprint, templateRec.ID, err)
		}
	}

	return convertTemplateRecord(templateRec, versionRec)
}

func (m *Manager) ListTemplates(ctx context.Context, topicID string) ([]agentcore.ProceduralTemplate, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("template manager unavailable")
	}
	return m.listTemplatesInternal(ctx, topicID)
}

func (m *Manager) ApproveTemplate(ctx context.Context, req service.TemplateApprovalRequest) (agentcore.ProceduralTemplate, error) {
	if m == nil || m.store == nil {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("template manager unavailable")
	}
	templateID := strings.TrimSpace(req.TemplateID)
	if templateID == "" {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("template_id required")
	}
	versions, err := m.store.ListProceduralTemplateVersions(ctx, templateID)
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}
	if len(versions) == 0 {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("no versions for template %s", templateID)
	}
	base := versions[0]
	graph := req.Graph
	if len(graph) == 0 {
		graph = append(json.RawMessage(nil), base.Graph...)
	}
	parameters := req.Parameters
	if len(parameters) == 0 {
		parameters = append(json.RawMessage(nil), base.Parameters...)
	}
	metadata := req.Metadata
	if metadata == nil && len(base.Metadata) > 0 {
		if err := json.Unmarshal(base.Metadata, &metadata); err != nil {
			metadata = map[string]interface{}{}
		}
	}
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return agentcore.ProceduralTemplate{}, fmt.Errorf("marshal metadata: %w", err)
	}
	approvedAt := time.Now().UTC()
	versionRec, err := m.store.CreateProceduralTemplateVersion(ctx, store.ProceduralTemplateVersionRecord{
		TemplateID: templateID,
		Status:     store.ProceduralTemplateStatusApproved,
		Graph:      graph,
		Parameters: parameters,
		Metadata:   json.RawMessage(metaBytes),
		Changelog:  strings.TrimSpace(req.Changelog),
		ApprovedBy: strings.TrimSpace(req.ApprovedBy),
		ApprovedAt: &approvedAt,
	})
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}

	templates, err := m.store.ListProceduralTemplates(ctx, "")
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}
	for _, tpl := range templates {
		if tpl.ID == templateID {
			return convertTemplateRecord(tpl, versionRec)
		}
	}
	return agentcore.ProceduralTemplate{}, fmt.Errorf("template %s not found", templateID)
}

func (m *Manager) listTemplatesInternal(ctx context.Context, topicID string) ([]agentcore.ProceduralTemplate, error) {
	recs, err := m.store.ListProceduralTemplates(ctx, topicID)
	if err != nil {
		return nil, err
	}
	out := make([]agentcore.ProceduralTemplate, 0, len(recs))
	for _, tpl := range recs {
		versions, err := m.store.ListProceduralTemplateVersions(ctx, tpl.ID)
		if err != nil {
			return nil, err
		}
		if len(versions) == 0 {
			continue
		}
		converted, err := convertTemplateRecord(tpl, versions[0])
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TopicID == out[j].TopicID {
			return out[i].Name < out[j].Name
		}
		return out[i].TopicID < out[j].TopicID
	})
	return out, nil
}

func convertTemplateRecord(templateRec store.ProceduralTemplateRecord, versionRec store.ProceduralTemplateVersionRecord) (agentcore.ProceduralTemplate, error) {
	var versionMeta map[string]interface{}
	if len(versionRec.Metadata) > 0 {
		if err := json.Unmarshal(versionRec.Metadata, &versionMeta); err != nil {
			versionMeta = map[string]interface{}{}
		}
	} else {
		versionMeta = map[string]interface{}{}
	}
	tpl := agentcore.ProceduralTemplate{
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
	return tpl, nil
}

func shortFingerprint(fp string) string {
	if len(fp) <= 8 {
		return fp
	}
	return fp[:8]
}
