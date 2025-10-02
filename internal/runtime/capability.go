package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/capability"
	"github.com/mohammad-safakhou/newser/internal/store"
)

// EnsureCapabilityRegistry seeds defaults if necessary and returns a validated registry.
func EnsureCapabilityRegistry(ctx context.Context, st *store.Store, cfg *config.Config) (*capability.Registry, error) {
	wrapper := capabilityWrapperFromConfig(cfg)
	if wrapper.SigningSecret == "" {
		return nil, errors.New("capability.signing_secret not configured")
	}
	// Seed defaults if registry empty.
	cards, err := st.ListToolCards(ctx)
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		if err := seedDefaultToolCards(ctx, st, wrapper.SigningSecret); err != nil {
			return nil, err
		}
		cards, err = st.ListToolCards(ctx)
		if err != nil {
			return nil, err
		}
	}
	toolCards, err := ToolCardsFromRecords(cards)
	if err != nil {
		return nil, err
	}
	registry, err := capability.NewRegistry(toolCards, wrapper.SigningSecret, wrapper.RequiredTools)
	if err != nil {
		return nil, err
	}
	return registry, nil
}

func seedDefaultToolCards(ctx context.Context, st *store.Store, signingSecret string) error {
	for _, tc := range capability.DefaultToolCards() {
		checksum, err := capability.ComputeChecksum(tc)
		if err != nil {
			return err
		}
		tc.Checksum = checksum
		sig, err := capability.SignToolCard(tc, signingSecret)
		if err != nil {
			return err
		}
		tc.Signature = sig
		rec, err := ToolCardRecordFromToolCard(tc)
		if err != nil {
			return err
		}
		if err := st.UpsertToolCard(ctx, rec); err != nil {
			return err
		}
	}
	return nil
}

// ToolCardsFromRecords converts store records to ToolCards.
func ToolCardsFromRecords(records []store.ToolCardRecord) ([]capability.ToolCard, error) {
	out := make([]capability.ToolCard, 0, len(records))
	for _, rec := range records {
		tc, err := toolCardFromRecord(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, nil
}

// ToolCardFromRecord converts a store record to a ToolCard.
func ToolCardFromRecord(rec store.ToolCardRecord) (capability.ToolCard, error) {
	return toolCardFromRecord(rec)
}

// ToolCardRecordFromToolCard converts ToolCard to store record.
func ToolCardRecordFromToolCard(tc capability.ToolCard) (store.ToolCardRecord, error) {
	return recordFromToolCard(tc)
}

func toolCardFromRecord(rec store.ToolCardRecord) (capability.ToolCard, error) {
	var input map[string]interface{}
	var output map[string]interface{}
	var side []string
	if len(rec.InputSchema) > 0 {
		if err := json.Unmarshal(rec.InputSchema, &input); err != nil {
			return capability.ToolCard{}, err
		}
	}
	if len(rec.OutputSchema) > 0 {
		if err := json.Unmarshal(rec.OutputSchema, &output); err != nil {
			return capability.ToolCard{}, err
		}
	}
	if len(rec.SideEffects) > 0 {
		if err := json.Unmarshal(rec.SideEffects, &side); err != nil {
			return capability.ToolCard{}, err
		}
	}
	return capability.ToolCard{
		Name:         rec.Name,
		Version:      rec.Version,
		Description:  rec.Description,
		AgentType:    rec.AgentType,
		InputSchema:  input,
		OutputSchema: output,
		CostEstimate: rec.CostEstimate,
		SideEffects:  side,
		Checksum:     rec.Checksum,
		Signature:    rec.Signature,
	}, nil
}

func recordFromToolCard(tc capability.ToolCard) (store.ToolCardRecord, error) {
	inputBytes, err := json.Marshal(tc.InputSchema)
	if err != nil {
		return store.ToolCardRecord{}, err
	}
	outputBytes, err := json.Marshal(tc.OutputSchema)
	if err != nil {
		return store.ToolCardRecord{}, err
	}
	sideBytes, err := json.Marshal(tc.SideEffects)
	if err != nil {
		return store.ToolCardRecord{}, err
	}
	return store.ToolCardRecord{
		Name:         tc.Name,
		Version:      tc.Version,
		Description:  tc.Description,
		AgentType:    tc.AgentType,
		InputSchema:  inputBytes,
		OutputSchema: outputBytes,
		CostEstimate: tc.CostEstimate,
		SideEffects:  sideBytes,
		Checksum:     tc.Checksum,
		Signature:    tc.Signature,
	}, nil
}

// capabilityConfigWrapper extracts capability config to avoid cyclic import.
type capabilityConfigWrapper struct {
	SigningSecret string
	RequiredTools []string
}

func capabilityWrapperFromConfig(cfg *config.Config) capabilityConfigWrapper {
	return capabilityConfigWrapper{
		SigningSecret: cfg.Capability.SigningSecret,
		RequiredTools: cfg.Capability.RequiredTools,
	}
}
