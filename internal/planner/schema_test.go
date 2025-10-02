package planner

import "testing"

func TestValidatePlanDocument(t *testing.T) {
	payload := []byte(`{
        "version": "v1",
        "tasks": [
            {"id": "t1", "type": "research"}
        ],
        "execution_order": ["t1"],
        "estimates": {"total_cost": 1.5, "total_time": "5m"},
        "budget": {"max_cost": 3.0}
    }`)
	if err := ValidatePlanDocument(payload); err != nil {
		t.Fatalf("expected payload to validate: %v", err)
	}
}

func TestValidatePlanDocumentFails(t *testing.T) {
	payload := []byte(`{"version": "v1"}`)
	if err := ValidatePlanDocument(payload); err == nil {
		t.Fatalf("expected schema validation to fail")
	}
}
