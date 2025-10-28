package helpers

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// DiffSnapshot captures prior crawl metadata for comparison.
type DiffSnapshot struct {
	Hash      string
	LastSeen  time.Time
	Timestamp time.Time
}

// DiffDecision summarises whether content should be treated as updated.
type DiffDecision struct {
	CurrentHash  string
	PreviousHash string
	Changed      bool
	Stale        bool
	Reasons      []string
	SeenAt       time.Time
	Age          time.Duration
}

// NormalizeForDiff collapses whitespace and lowercases content to stabilise hash comparisons.
func NormalizeForDiff(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Collapse repeated whitespace and lowercase for stable comparisons.
	fields := strings.Fields(s)
	return strings.ToLower(strings.Join(fields, " "))
}

// ContentHash computes a SHA-256 hash for the normalised content.
func ContentHash(content string) string {
	norm := NormalizeForDiff(content)
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// EvaluateDiff compares the current content against a previous snapshot and optional freshness window.
// If maxAge is zero, staleness checks are skipped.
func EvaluateDiff(prev DiffSnapshot, currentContent string, seenAt time.Time, maxAge time.Duration) DiffDecision {
	hash := ContentHash(currentContent)
	decision := DiffDecision{
		CurrentHash:  hash,
		PreviousHash: prev.Hash,
		SeenAt:       seenAt,
	}

	if !seenAt.IsZero() && !prev.LastSeen.IsZero() {
		decision.Age = seenAt.Sub(prev.LastSeen)
	} else if !seenAt.IsZero() && !prev.Timestamp.IsZero() {
		decision.Age = seenAt.Sub(prev.Timestamp)
	}

	if prev.Hash == "" {
		decision.Changed = true
		decision.Reasons = append(decision.Reasons, "new_content")
	} else if prev.Hash != hash {
		decision.Changed = true
		decision.Reasons = append(decision.Reasons, "hash_mismatch")
	}

	if maxAge > 0 {
		var reference time.Time
		switch {
		case !prev.LastSeen.IsZero():
			reference = prev.LastSeen
		case !prev.Timestamp.IsZero():
			reference = prev.Timestamp
		}
		if !reference.IsZero() && !seenAt.IsZero() && seenAt.Sub(reference) >= maxAge {
			decision.Stale = true
			decision.Reasons = append(decision.Reasons, "stale_content")
		}
	}

	if decision.Stale && !decision.Changed {
		decision.Changed = true
	}

	return decision
}
