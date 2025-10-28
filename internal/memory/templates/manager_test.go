package templates

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/internal/memory/service"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type templateStoreStub struct {
	fingerprints []store.ProceduralTemplateFingerprintRecord
	templates    []store.ProceduralTemplateRecord
	versions     map[string][]store.ProceduralTemplateVersionRecord
}

func (s *templateStoreStub) ListProceduralTemplateFingerprints(ctx context.Context, topicID string, limit int) ([]store.ProceduralTemplateFingerprintRecord, error) {
	return s.fingerprints, nil
}

func (s *templateStoreStub) CreateProceduralTemplate(ctx context.Context, rec store.ProceduralTemplateRecord) (store.ProceduralTemplateRecord, error) {
	rec.CreatedAt = time.Now()
	rec.UpdatedAt = rec.CreatedAt
	s.templates = append(s.templates, rec)
	return rec, nil
}

func (s *templateStoreStub) CreateProceduralTemplateVersion(ctx context.Context, rec store.ProceduralTemplateVersionRecord) (store.ProceduralTemplateVersionRecord, error) {
	if rec.Version == 0 {
		rec.Version = len(s.versions[rec.TemplateID]) + 1
	}
	rec.ID = int64(rec.Version)
	rec.CreatedAt = time.Now()
	s.versions[rec.TemplateID] = append([]store.ProceduralTemplateVersionRecord{rec}, s.versions[rec.TemplateID]...)
	return rec, nil
}

func (s *templateStoreStub) LinkProceduralTemplateFingerprint(ctx context.Context, topicID, fingerprint, templateID string) error {
	for i := range s.fingerprints {
		if s.fingerprints[i].Fingerprint == fingerprint {
			s.fingerprints[i].TemplateID = templateID
		}
	}
	return nil
}

func (s *templateStoreStub) ListProceduralTemplates(ctx context.Context, topicID string) ([]store.ProceduralTemplateRecord, error) {
	return s.templates, nil
}

func (s *templateStoreStub) ListProceduralTemplateVersions(ctx context.Context, templateID string) ([]store.ProceduralTemplateVersionRecord, error) {
	return s.versions[templateID], nil
}

func TestPromoteFingerprintCreatesTemplate(t *testing.T) {
	sampleGraph := json.RawMessage(`{"stage":"alpha"}`)
	stub := &templateStoreStub{
		fingerprints: []store.ProceduralTemplateFingerprintRecord{{
			TopicID:      "topic-1",
			Fingerprint:  "abc123",
			SampleGraph:  sampleGraph,
			SampleParams: json.RawMessage(`{"tasks":2}`),
			Metadata:     map[string]interface{}{"stage": "alpha"},
		}},
		versions: make(map[string][]store.ProceduralTemplateVersionRecord),
	}
	mgr := NewManager(stub, nil)

	tpl, err := mgr.PromoteFingerprint(context.Background(), service.TemplatePromotionRequest{
		TopicID:     "topic-1",
		Fingerprint: "abc123",
		Name:        "Alpha Template",
		CreatedBy:   "user-1",
	})
	if err != nil {
		t.Fatalf("PromoteFingerprint: %v", err)
	}
	if tpl.TopicID != "topic-1" {
		t.Fatalf("unexpected topic: %s", tpl.TopicID)
	}
	if tpl.Version.Status != store.ProceduralTemplateStatusPendingApproval {
		t.Fatalf("expected pending approval status, got %s", tpl.Version.Status)
	}
}

func TestApproveTemplateCreatesNewVersion(t *testing.T) {
	templateID := "tpl-1"
	stub := &templateStoreStub{
		templates: []store.ProceduralTemplateRecord{{
			ID:      templateID,
			TopicID: "topic-1",
			Name:    "Template",
		}},
		versions: map[string][]store.ProceduralTemplateVersionRecord{
			templateID: {
				{
					TemplateID: templateID,
					Version:    1,
					Status:     store.ProceduralTemplateStatusPendingApproval,
					Graph:      json.RawMessage(`{"stage":"alpha"}`),
					Parameters: json.RawMessage(`{}`),
				},
			},
		},
	}
	mgr := NewManager(stub, nil)

	tpl, err := mgr.ApproveTemplate(context.Background(), service.TemplateApprovalRequest{
		TemplateID: templateID,
		ApprovedBy: "reviewer",
	})
	if err != nil {
		t.Fatalf("ApproveTemplate: %v", err)
	}
	if tpl.Version.Status != store.ProceduralTemplateStatusApproved {
		t.Fatalf("expected approved status, got %s", tpl.Version.Status)
	}
	if len(stub.versions[templateID]) == 0 {
		t.Fatalf("expected versions to be recorded")
	}
}

func TestListFingerprintsReturnsOccurrencesSorted(t *testing.T) {
	stub := &templateStoreStub{
		fingerprints: []store.ProceduralTemplateFingerprintRecord{
			{TopicID: "topic-1", Fingerprint: "b", Occurrences: 2},
			{TopicID: "topic-1", Fingerprint: "a", Occurrences: 5},
		},
		versions: make(map[string][]store.ProceduralTemplateVersionRecord),
	}
	mgr := NewManager(stub, nil)
	result, err := mgr.ListFingerprints(context.Background(), "topic-1", 10)
	if err != nil {
		t.Fatalf("ListFingerprints: %v", err)
	}
	if len(result) != 2 || result[0].Fingerprint != "a" {
		t.Fatalf("fingerprints not sorted by occurrences: %#v", result)
	}
}
