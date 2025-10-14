package streams

import (
	"encoding/json"
	"testing"
	"time"
)

func TestArtifactSchemasValidate(t *testing.T) {
	reg := NewSchemaRegistry()
	if err := RegisterBaseSchemas(reg); err != nil {
		t.Fatalf("register base schemas: %v", err)
	}

	artifactPayload := map[string]interface{}{
		"run_id":  "run-123",
		"task_id": "task-1",
		"attempt": 0,
		"artifact": map[string]interface{}{
			"artifact_id": "art-1",
			"name":        "report.pdf",
			"media_type":  "application/pdf",
			"size_bytes":  1024,
			"checksum":    "sha256:deadbeef",
			"uri":         "s3://bucket/run-123/report.pdf",
			"metadata": map[string]interface{}{
				"role": "report",
			},
		},
	}
	data, err := json.Marshal(artifactPayload)
	if err != nil {
		t.Fatalf("marshal artifact payload: %v", err)
	}
	if err := reg.Validate("artifact.created", "v1", data); err != nil {
		t.Fatalf("expected artifact payload to validate: %v", err)
	}

	resultPayload := map[string]interface{}{
		"run_id":           "run-123",
		"task_id":          "task-1",
		"success":          true,
		"output":           map[string]interface{}{"summary": "done"},
		"checkpoint_token": "chk-1",
		"artifacts": []map[string]interface{}{
			{
				"artifact_id": "art-1",
				"uri":         "s3://bucket/run-123/report.pdf",
				"media_type":  "application/pdf",
			},
		},
	}
	data, err = json.Marshal(resultPayload)
	if err != nil {
		t.Fatalf("marshal result payload: %v", err)
	}
	if err := reg.Validate("task.result", "v1", data); err != nil {
		t.Fatalf("expected task.result payload to validate: %v", err)
	}

	approvalPayload := map[string]interface{}{
		"run_id":           "run-123",
		"topic_id":         "topic-123",
		"requested_by":     "user-1",
		"estimated_cost":   25.5,
		"threshold":        20.0,
		"require_approval": true,
		"created_at":       time.Now().UTC().Format(time.RFC3339),
	}
	data, err = json.Marshal(approvalPayload)
	if err != nil {
		t.Fatalf("marshal approval payload: %v", err)
	}
	if err := reg.Validate("budget.approval.requested", "v1", data); err != nil {
		t.Fatalf("expected budget.approval.requested payload to validate: %v", err)
	}
}
