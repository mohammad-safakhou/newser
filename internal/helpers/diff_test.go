package helpers

import (
	"testing"
	"time"
)

func TestNormalizeForDiff(t *testing.T) {
	t.Parallel()
	in := " Hello\tWorld\nNew  Line "
	got := NormalizeForDiff(in)
	if got != "hello world new line" {
		t.Fatalf("NormalizeForDiff() = %q", got)
	}
}

func TestContentHashDeterministic(t *testing.T) {
	t.Parallel()
	a := ContentHash("Breaking News!")
	b := ContentHash("  BREAKING   news!  ")
	if a != b {
		t.Fatalf("expected identical hashes, got %s vs %s", a, b)
	}
}

func TestEvaluateDiffDetectsChanges(t *testing.T) {
	t.Parallel()
	prev := DiffSnapshot{
		Hash:     ContentHash("hello world"),
		LastSeen: time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC),
	}
	now := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)

	decision := EvaluateDiff(prev, "hello world updated", now, 0)
	if !decision.Changed {
		t.Fatalf("expected change detection")
	}
	if decision.Reasons[0] != "hash_mismatch" {
		t.Fatalf("unexpected reason: %#v", decision.Reasons)
	}
}

func TestEvaluateDiffMarksStale(t *testing.T) {
	t.Parallel()
	prev := DiffSnapshot{
		Hash:     ContentHash("same content"),
		LastSeen: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	now := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)

	decision := EvaluateDiff(prev, "same content", now, 24*time.Hour*3)
	if !decision.Stale {
		t.Fatalf("expected stale content")
	}
	if !decision.Changed {
		t.Fatalf("stale content should still mark change")
	}
}
