package streams

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
)

// SchemaRegistry stores compiled JSON Schemas keyed by event type and payload version.
type SchemaRegistry struct {
	mu      sync.RWMutex
	schemas map[string]map[string]*jsonschema.Schema
}

// NewSchemaRegistry constructs an empty registry instance.
func NewSchemaRegistry() *SchemaRegistry {
	return &SchemaRegistry{schemas: make(map[string]map[string]*jsonschema.Schema)}
}

// Register compiles and stores a JSON schema for the given event type and version.
func (r *SchemaRegistry) Register(eventType, version string, schemaBytes []byte) error {
	if eventType == "" {
		return fmt.Errorf("eventType must be provided")
	}
	if version == "" {
		return fmt.Errorf("version must be provided")
	}
	if len(schemaBytes) == 0 {
		return fmt.Errorf("schemaBytes is empty")
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(schemaBytes)); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.schemas[eventType]; !ok {
		r.schemas[eventType] = make(map[string]*jsonschema.Schema)
	}
	r.schemas[eventType][version] = compiled
	return nil
}

// Validate checks payload bytes against the registered schema for event type/version.
func (r *SchemaRegistry) Validate(eventType, version string, payload []byte) error {
	if eventType == "" {
		return fmt.Errorf("eventType must be provided")
	}
	if version == "" {
		return fmt.Errorf("version must be provided")
	}

	r.mu.RLock()
	versions, ok := r.schemas[eventType]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no schema registered for event type %q", eventType)
	}

	schema, ok := versions[version]
	if !ok {
		return fmt.Errorf("no schema registered for event %q version %q", eventType, version)
	}

	if len(payload) == 0 {
		return fmt.Errorf("payload is empty")
	}

	var doc interface{}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("payload validation failed: %w", err)
	}
	return nil
}
