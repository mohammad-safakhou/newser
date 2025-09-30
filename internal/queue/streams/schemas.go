package streams

import "fmt"

// Definition describes a schema entry managed by the registry.
type Definition struct {
	EventType string
	Version   string
	Schema    []byte
}

var baseDefinitions = []Definition{
	{
		EventType: "run.enqueued",
		Version:   "v1",
		Schema: []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["topic_id", "user_id", "trigger", "preferences_snapshot", "context_snapshot"],
  "properties": {
    "topic_id": {"type": "string"},
    "user_id": {"type": "string"},
    "trigger": {"type": "string", "enum": ["manual", "schedule"]},
    "preferences_snapshot": {"type": "object"},
    "context_snapshot": {"type": "object"}
  },
  "additionalProperties": true
}`),
	},
	{
		EventType: "task.dispatch",
		Version:   "v1",
		Schema: []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["run_id", "task_id", "task_type", "priority", "plan_snapshot", "parameters", "checkpoint_token"],
  "properties": {
    "run_id": {"type": "string"},
    "task_id": {"type": "string"},
    "task_type": {"type": "string"},
    "priority": {"type": "integer"},
    "plan_snapshot": {"type": "object"},
    "parameters": {"type": "object"},
    "checkpoint_token": {"type": "string"}
  },
  "additionalProperties": true
}`),
	},
	{
		EventType: "task.result",
		Version:   "v1",
		Schema: []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["run_id", "task_id", "success", "output", "checkpoint_token"],
  "properties": {
    "run_id": {"type": "string"},
    "task_id": {"type": "string"},
    "success": {"type": "boolean"},
    "output": {"type": "object"},
    "cost_estimate": {"type": "number"},
    "tokens_used": {"type": "integer"},
    "artifacts": {"type": "array"},
    "checkpoint_token": {"type": "string"}
  },
  "additionalProperties": true
}`),
	},
	{
		EventType: "crawl.request",
		Version:   "v1",
		Schema: []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["run_id", "request_id", "urls"],
  "properties": {
    "run_id": {"type": "string"},
    "request_id": {"type": "string"},
    "urls": {
      "type": "array",
      "items": {"type": "string", "format": "uri"},
      "minItems": 1
    },
    "freshness_hint": {"type": "string"},
    "policy_profile": {"type": "string"}
  },
  "additionalProperties": true
}`),
	},
	{
		EventType: "run.completed",
		Version:   "v1",
		Schema: []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["run_id", "topic_id", "status"],
  "properties": {
    "run_id": {"type": "string"},
    "topic_id": {"type": "string"},
    "status": {"type": "string"},
    "summary": {"type": "string"},
    "detailed_report_ref": {"type": "string"},
    "metrics": {"type": "object"}
  },
  "additionalProperties": true
}`),
	},
}

// BaseDefinitions returns the built-in schema definitions.
func BaseDefinitions() []Definition {
	defs := make([]Definition, len(baseDefinitions))
	copy(defs, baseDefinitions)
	return defs
}

// RegisterBaseSchemas loads the baseline event schemas into the provided registry.
func RegisterBaseSchemas(reg *SchemaRegistry) error {
	if reg == nil {
		return fmt.Errorf("registry is nil")
	}
	for _, def := range baseDefinitions {
		if err := reg.Register(def.EventType, def.Version, def.Schema); err != nil {
			return fmt.Errorf("register %s %s: %w", def.EventType, def.Version, err)
		}
	}
	return nil
}
