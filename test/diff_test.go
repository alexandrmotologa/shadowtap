package test

import (
	"net/http"
	"testing"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
)

func TestSemanticDiff_ExactMatch(t *testing.T) {
	jsonPayload := `{"user_id": 101, "name": "Alice", "role": "admin"}`
	snap1 := diff.NewResponseSnapshot(200, http.Header{}, jsonPayload, 10*time.Millisecond, nil)
	snap2 := diff.NewResponseSnapshot(200, http.Header{}, jsonPayload, 12*time.Millisecond, nil)

	res := diff.CompareSnapshots(snap1, snap2, nil, diff.DefaultCompareOptions())
	if !res.IsMatch {
		t.Fatalf("expected exact match, got mismatches: %+v", res.Mismatches)
	}
	if res.SemanticDistance != 0.0 {
		t.Fatalf("expected semantic distance 0.0, got %f", res.SemanticDistance)
	}
}

func TestSemanticDiff_VolatileNoiseIgnored(t *testing.T) {
	// Payloads differ only in volatile timestamps, UUIDs, and trace IDs
	primary := `{
		"order_id": "ORD-9901",
		"created_at": "2026-11-23T15:00:00Z",
		"request_id": "4a7b98d2-1c23-4e56-9a01-abcdef012345",
		"amount": 49.99,
		"status": "COMPLETED"
	}`
	shadow := `{
		"order_id": "ORD-9901",
		"created_at": "2026-11-23T15:00:01Z",
		"request_id": "9f8e7d6c-5b4a-4321-8765-fedcba543210",
		"amount": 49.99,
		"status": "COMPLETED"
	}`

	snap1 := diff.NewResponseSnapshot(200, http.Header{}, primary, 10*time.Millisecond, nil)
	snap2 := diff.NewResponseSnapshot(200, http.Header{}, shadow, 11*time.Millisecond, nil)

	maskCfg := diff.DefaultMaskConfig()
	res := diff.CompareSnapshots(snap1, snap2, &maskCfg, diff.DefaultCompareOptions())

	if !res.IsMatch {
		t.Fatalf("expected volatile fields to be ignored, but got mismatches: %+v", res.Mismatches)
	}
	if res.SemanticDistance != 0.0 {
		t.Fatalf("expected semantic distance 0.0, got %f", res.SemanticDistance)
	}
}

func TestSemanticDiff_TypeDriftDetected(t *testing.T) {
	primary := `{"id": 42, "item": "book", "price": 19.99}`
	// Shadow candidate accidentally converted price to string
	shadow := `{"id": 42, "item": "book", "price": "19.99"}`

	snap1 := diff.NewResponseSnapshot(200, http.Header{}, primary, 5*time.Millisecond, nil)
	snap2 := diff.NewResponseSnapshot(200, http.Header{}, shadow, 6*time.Millisecond, nil)

	res := diff.CompareSnapshots(snap1, snap2, nil, diff.DefaultCompareOptions())
	if res.IsMatch {
		t.Fatal("expected type drift regression to fail match")
	}

	foundTypeDrift := false
	for _, m := range res.Mismatches {
		if m.Kind == diff.MismatchKindTypeDrift && m.Path == "price" {
			foundTypeDrift = true
			break
		}
	}
	if !foundTypeDrift {
		t.Fatalf("expected TYPE_DRIFT mismatch on price, got: %+v", res.Mismatches)
	}
}

func TestSemanticDiff_MissingAndUnexpectedKeys(t *testing.T) {
	primary := `{"sku": "SKU-1", "title": "Widget", "legacy_field": "old"}`
	shadow := `{"sku": "SKU-1", "title": "Widget", "new_feature": true}`

	snap1 := diff.NewResponseSnapshot(200, http.Header{}, primary, 8*time.Millisecond, nil)
	snap2 := diff.NewResponseSnapshot(200, http.Header{}, shadow, 9*time.Millisecond, nil)

	res := diff.CompareSnapshots(snap1, snap2, nil, diff.DefaultCompareOptions())
	if res.IsMatch {
		t.Fatal("expected schema regression to fail match")
	}

	hasMissing := false
	hasUnexpected := false
	for _, m := range res.Mismatches {
		if m.Kind == diff.MismatchKindMissingKey && m.Path == "legacy_field" {
			hasMissing = true
		}
		if m.Kind == diff.MismatchKindUnexpectedKey && m.Path == "new_feature" {
			hasUnexpected = true
		}
	}

	if !hasMissing || !hasUnexpected {
		t.Fatalf("expected missing and unexpected key mismatches, got: %+v", res.Mismatches)
	}
}

func TestSemanticDiff_StatusDrift(t *testing.T) {
	snap1 := diff.NewResponseSnapshot(200, http.Header{}, `{"ok": true}`, 10*time.Millisecond, nil)
	snap2 := diff.NewResponseSnapshot(500, http.Header{}, `{"error": "internal"}`, 15*time.Millisecond, nil)

	res := diff.CompareSnapshots(snap1, snap2, nil, diff.DefaultCompareOptions())
	if res.IsMatch {
		t.Fatal("status code difference must fail match")
	}
	if !res.StatusDrift {
		t.Fatal("expected StatusDrift to be true")
	}
	if res.SemanticDistance != 1.0 {
		t.Fatalf("expected semantic distance 1.0 for status drift, got %f", res.SemanticDistance)
	}
}
