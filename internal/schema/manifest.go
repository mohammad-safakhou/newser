package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mohammad-safakhou/newser/internal/manifest"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
)

const runManifestEventType = "run.manifest"

var (
	runManifestSchemaBytes = []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["manifest", "checksum", "signature", "algorithm", "signed_at"],
  "properties": {
    "manifest": {
      "type": "object",
      "required": ["version", "run_id", "topic_id", "user_id", "result", "sources", "created_at"],
      "properties": {
        "version": {"type": "string"},
        "run_id": {"type": "string"},
        "topic_id": {"type": "string"},
        "user_id": {"type": "string"},
        "thought": {"type": "object"},
        "result": {
          "type": "object",
          "required": ["summary", "detailed_report", "confidence", "cost_estimate", "tokens_used"],
          "additionalProperties": true
        },
        "sources": {
          "type": "array",
          "items": {"$ref": "#/definitions/source"}
        },
        "plan": {
          "type": ["object", "null"],
          "additionalProperties": true
        },
        "steps": {
          "type": "array",
          "items": {
            "type": "object",
            "required": ["step_index", "task", "result"],
            "additionalProperties": true
          }
        },
        "digest": {"type": ["object", "null"], "additionalProperties": true},
        "budget": {"type": ["object", "null"], "additionalProperties": true},
        "created_at": {"type": "string", "format": "date-time"}
      },
      "additionalProperties": true
    },
    "checksum": {"type": "string", "pattern": "^[a-f0-9]{64}$"},
    "signature": {"type": "string", "minLength": 1},
    "algorithm": {"type": "string", "minLength": 1},
    "signed_at": {"type": "string", "format": "date-time"}
  },
  "additionalProperties": false,
  "definitions": {
    "source": {
      "type": "object",
      "required": ["id", "title", "url", "domain"],
      "properties": {
        "id": {"type": "string"},
        "title": {"type": "string"},
        "url": {"type": "string", "format": "uri"},
        "domain": {"type": "string"},
        "snippet": {"type": "string"},
        "type": {"type": "string"},
        "credibility": {"type": "number"},
        "published_at": {"type": "string", "format": "date-time"}
      },
      "additionalProperties": true
    }
  }
}`)

	runManifestSchemaOnce sync.Once
	runManifestCompiled   *jsonschema.Schema
	runManifestSchemaErr  error
)

// RunManifestSchemaVersion returns the current manifest schema version.
func RunManifestSchemaVersion() string {
	return manifest.RunManifestVersion
}

// RunManifestSchema returns the raw JSON schema used to validate signed manifests.
func RunManifestSchema() []byte {
	return append([]byte(nil), runManifestSchemaBytes...)
}

// ValidateRunManifestDocument validates the JSON bytes against the manifest schema.
func ValidateRunManifestDocument(data []byte) error {
	runManifestSchemaOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("run_manifest.json", bytes.NewReader(runManifestSchemaBytes)); err != nil {
			runManifestSchemaErr = fmt.Errorf("add run manifest schema: %w", err)
			return
		}
		runManifestCompiled, runManifestSchemaErr = compiler.Compile("run_manifest.json")
	})
	if runManifestSchemaErr != nil {
		return runManifestSchemaErr
	}
	var payload interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("unmarshal manifest json: %w", err)
	}
	return runManifestCompiled.Validate(payload)
}

// ValidateSignedRunManifest marshals and validates the signed manifest payload.
func ValidateSignedRunManifest(signed manifest.SignedRunManifest) error {
	data, err := json.Marshal(signed)
	if err != nil {
		return err
	}
	return ValidateRunManifestDocument(data)
}

// SeedRunManifestSchema ensures the current manifest schema version exists in the registry.
func SeedRunManifestSchema(ctx context.Context, st Store) error {
	if st == nil {
		return fmt.Errorf("store is nil")
	}
	schemaBytes := RunManifestSchema()
	if err := ValidateSchema(schemaBytes); err != nil {
		return fmt.Errorf("validate run manifest schema: %w", err)
	}
	return st.UpsertMessageSchema(ctx, runManifestEventType, RunManifestSchemaVersion(), schemaBytes)
}
