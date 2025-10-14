package planner

import (
	"strings"
	"testing"
)

func TestNormalizePlanDocumentComputesExecutionHints(t *testing.T) {
	payload := []byte(`{
        "version": "v1",
        "tasks": [
            {"id": "t1", "type": "research"},
            {"id": "t2", "type": "analysis", "depends_on": ["t1"]},
            {"id": "t3", "type": "synthesis", "depends_on": ["t2"]}
        ]
    }`)

	doc, normalized, err := NormalizePlanDocument(payload)
	if err != nil {
		t.Fatalf("expected payload to normalise: %v", err)
	}
	if got, want := len(doc.ExecutionOrder), 3; got != want {
		t.Fatalf("expected execution order of %d, got %d", want, got)
	}
	expectedOrder := []string{"t1", "t2", "t3"}
	for i, id := range expectedOrder {
		if doc.ExecutionOrder[i] != id {
			t.Fatalf("execution order mismatch at %d: got %s want %s", i, doc.ExecutionOrder[i], id)
		}
	}
	if got, want := len(doc.ExecutionLayers), 3; got != want {
		t.Fatalf("expected %d execution layers, got %d", want, got)
	}
	for i, stage := range doc.ExecutionLayers {
		if len(stage.Tasks) != 1 || stage.Tasks[0] != expectedOrder[i] {
			t.Fatalf("expected layer %d to contain %q, got %#v", i, expectedOrder[i], stage.Tasks)
		}
	}
	if !strings.Contains(string(normalized), "execution_layers") {
		t.Fatalf("normalised payload should include execution_layers: %s", string(normalized))
	}
}

func TestNormalizePlanDocumentRejectsDuplicateTaskIDs(t *testing.T) {
	payload := []byte(`{
        "version": "v1",
        "tasks": [
            {"id": "t1", "type": "research"},
            {"id": "t1", "type": "analysis"}
        ]
    }`)

	if _, _, err := NormalizePlanDocument(payload); err == nil {
		t.Fatalf("expected duplicate task IDs to fail validation")
	} else {
		switch v := err.(type) {
		case ValidationErrors:
			if len(v) == 0 || !strings.Contains(v[0].Message, "duplicate task id") {
				t.Fatalf("unexpected validation error: %v", err)
			}
		case ValidationError:
			if !strings.Contains(v.Message, "duplicate task id") {
				t.Fatalf("unexpected validation message: %v", err)
			}
		default:
			t.Fatalf("unexpected error type: %T", err)
		}
	}
}

func TestNormalizePlanDocumentRejectsUnknownDependency(t *testing.T) {
	payload := []byte(`{
        "version": "v1",
        "tasks": [
            {"id": "t1", "type": "research", "depends_on": ["t-missing"]}
        ]
    }`)

	if _, _, err := NormalizePlanDocument(payload); err == nil {
		t.Fatalf("expected unknown dependency to fail validation")
	}
}

func TestNormalizePlanDocumentRejectsCycles(t *testing.T) {
	payload := []byte(`{
        "version": "v1",
        "tasks": [
            {"id": "a", "type": "research", "depends_on": ["b"]},
            {"id": "b", "type": "analysis", "depends_on": ["a"]}
        ]
    }`)

	_, _, err := NormalizePlanDocument(payload)
	if err == nil {
		t.Fatalf("expected cycle detection to fail")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestValidatePlanDocumentSchemaFailure(t *testing.T) {
	if err := ValidatePlanDocument([]byte(`{"version":"v1"}`)); err == nil {
		t.Fatalf("expected schema validation failure")
	}
}
