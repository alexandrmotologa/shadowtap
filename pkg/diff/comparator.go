package diff

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// CompareOptions configures fine-grained diffing behaviors.
type CompareOptions struct {
	// NumericEpsilon defines floating point equality tolerance (e.g. 1e-6).
	NumericEpsilon float64
	// StrictStatus flags any HTTP status difference as a hard regression.
	StrictStatus bool
	// RegressionThreshold determines the minimum semantic distance to flag a regression (default 0.0).
	RegressionThreshold float64
}

// DefaultCompareOptions provides robust defaults.
func DefaultCompareOptions() CompareOptions {
	return CompareOptions{
		NumericEpsilon:      1e-6,
		StrictStatus:        true,
		RegressionThreshold: 0.0,
	}
}

// CompareSnapshots executes a comprehensive semantic diff between primary and shadow responses.
func CompareSnapshots(primary, shadow ResponseSnapshot, maskConfig *MaskConfig, opts CompareOptions) DiffResult {
	if maskConfig == nil {
		cfg := DefaultMaskConfig()
		maskConfig = &cfg
	}

	result := DiffResult{
		IsMatch:        true,
		Mismatches:     make([]Mismatch, 0),
		LatencyDelta:   shadow.Latency - primary.Latency,
		LatencyDeltaMs: float64((shadow.Latency - primary.Latency).Microseconds()) / 1000.0,
	}

	// 1. Evaluate HTTP Status Code
	if opts.StrictStatus && primary.StatusCode != shadow.StatusCode {
		result.StatusDrift = true
		result.IsMatch = false
		result.SemanticDistance = 1.0
		result.Mismatches = append(result.Mismatches, Mismatch{
			Path:         "_status",
			Kind:         MismatchKindStatusDrift,
			PrimaryValue: primary.StatusCode,
			PrimaryType:  "int",
			ShadowValue:  shadow.StatusCode,
			ShadowType:   "int",
			Message:      fmt.Sprintf("HTTP status code drift: primary returned %d, shadow returned %d", primary.StatusCode, shadow.StatusCode),
		})
	}

	// Check if calls produced execution errors
	if primary.Error != "" || shadow.Error != "" {
		if primary.Error != shadow.Error {
			result.IsMatch = false
			result.SemanticDistance = 1.0
			result.Mismatches = append(result.Mismatches, Mismatch{
				Path:         "_network_error",
				Kind:         MismatchKindStatusDrift,
				PrimaryValue: primary.Error,
				PrimaryType:  "error",
				ShadowValue:  shadow.Error,
				ShadowType:   "error",
				Message:      fmt.Sprintf("Network error mismatch: primary=%q shadow=%q", primary.Error, shadow.Error),
			})
			return result
		}
	}

	// 2. Parse and Flatten Payloads
	primBytes := []byte(strings.TrimSpace(primary.Body))
	shadBytes := []byte(strings.TrimSpace(shadow.Body))

	// Fast path for identical raw payloads
	if string(primBytes) == string(shadBytes) {
		if result.StatusDrift {
			return result
		}
		result.SemanticDistance = 0.0
		result.IsMatch = true
		result.PrimaryCleanJSON = string(primBytes)
		result.ShadowCleanJSON = string(shadBytes)
		return result
	}

	primFlat, primClean, primErr := FlattenJSON(primBytes, maskConfig)
	shadFlat, shadClean, shadErr := FlattenJSON(shadBytes, maskConfig)

	// If either body is not valid JSON, fall back to string comparison
	if primErr != nil || shadErr != nil {
		if string(primBytes) != string(shadBytes) {
			result.IsMatch = false
			result.SemanticDistance = 1.0
			result.Mismatches = append(result.Mismatches, Mismatch{
				Path:         "_body_text",
				Kind:         MismatchKindValueDrift,
				PrimaryValue: primary.Body,
				PrimaryType:  "text",
				ShadowValue:  shadow.Body,
				ShadowType:   "text",
				Message:      "Non-JSON response bodies differ",
			})
		}
		result.PrimaryCleanJSON = primary.Body
		result.ShadowCleanJSON = shadow.Body
		return result
	}

	// Format pretty clean JSON for cockpit side-by-side rendering
	if primClean != nil {
		if formatted, err := json.MarshalIndent(primClean, "", "  "); err == nil {
			result.PrimaryCleanJSON = string(formatted)
		}
	}
	if shadClean != nil {
		if formatted, err := json.MarshalIndent(shadClean, "", "  "); err == nil {
			result.ShadowCleanJSON = string(formatted)
		}
	}

	// 3. Compare Flattened Trees (ignoring masked volatile fields)
	activeKeys := make(map[string]bool)
	diffCount := 0

	// Check primary against shadow
	for pKey, pTuple := range primFlat {
		if pTuple.IsMasked {
			continue
		}
		activeKeys[pKey] = true

		sTuple, exists := shadFlat[pKey]
		if !exists || sTuple.IsMasked {
			diffCount++
			result.Mismatches = append(result.Mismatches, Mismatch{
				Path:         pKey,
				Kind:         MismatchKindMissingKey,
				PrimaryValue: pTuple.Value,
				PrimaryType:  pTuple.Type,
				ShadowValue:  nil,
				ShadowType:   "missing",
				Message:      fmt.Sprintf("Key %q is missing in shadow response", pKey),
			})
			continue
		}

		// Type drift check
		if pTuple.Type != sTuple.Type {
			diffCount++
			result.Mismatches = append(result.Mismatches, Mismatch{
				Path:         pKey,
				Kind:         MismatchKindTypeDrift,
				PrimaryValue: pTuple.Value,
				PrimaryType:  pTuple.Type,
				ShadowValue:  sTuple.Value,
				ShadowType:   sTuple.Type,
				Message:      fmt.Sprintf("Type drift at %q: primary is %s, shadow is %s", pKey, pTuple.Type, sTuple.Type),
			})
			continue
		}

		// Value equality check
		if !isSemanticallyEqual(pTuple.Value, sTuple.Value, opts.NumericEpsilon) {
			diffCount++
			result.Mismatches = append(result.Mismatches, Mismatch{
				Path:         pKey,
				Kind:         MismatchKindValueDrift,
				PrimaryValue: pTuple.Value,
				PrimaryType:  pTuple.Type,
				ShadowValue:  sTuple.Value,
				ShadowType:   sTuple.Type,
				Message:      fmt.Sprintf("Value mismatch at %q: primary=%v, shadow=%v", pKey, pTuple.Value, sTuple.Value),
			})
		}
	}

	// Check shadow for unexpected extra keys
	for sKey, sTuple := range shadFlat {
		if sTuple.IsMasked {
			continue
		}
		if _, seen := activeKeys[sKey]; !seen {
			activeKeys[sKey] = true
			diffCount++
			result.Mismatches = append(result.Mismatches, Mismatch{
				Path:         sKey,
				Kind:         MismatchKindUnexpectedKey,
				PrimaryValue: nil,
				PrimaryType:  "missing",
				ShadowValue:  sTuple.Value,
				ShadowType:   sTuple.Type,
				Message:      fmt.Sprintf("Unexpected key %q found in shadow response", sKey),
			})
		}
	}

	// 4. Compute Jaccard Semantic Distance
	totalActiveKeys := len(activeKeys)
	if totalActiveKeys > 0 {
		result.SemanticDistance = float64(diffCount) / float64(totalActiveKeys)
	} else if result.StatusDrift {
		result.SemanticDistance = 1.0
	} else {
		result.SemanticDistance = 0.0
	}

	// Cap at 1.0
	if result.SemanticDistance > 1.0 {
		result.SemanticDistance = 1.0
	}

	// Determine match status based on threshold and status drift
	if result.StatusDrift || result.SemanticDistance > opts.RegressionThreshold {
		result.IsMatch = false
	} else {
		result.IsMatch = true
	}

	return result
}

// isSemanticallyEqual compares two primitive values with floating point epsilon tolerance.
func isSemanticallyEqual(a, b any, epsilon float64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Numeric float comparison
	aNum, aIsNum := toFloat64(a)
	bNum, bIsNum := toFloat64(b)
	if aIsNum && bIsNum {
		return math.Abs(aNum-bNum) <= epsilon
	}

	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// toFloat64 attempts numeric conversion.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	default:
		return 0, false
	}
}

// NewResponseSnapshot creates a snapshot from standard HTTP response components.
func NewResponseSnapshot(statusCode int, header http.Header, body string, latency time.Duration, err error) ResponseSnapshot {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	return ResponseSnapshot{
		StatusCode: statusCode,
		Headers:    header.Clone(),
		Body:       body,
		Latency:    latency,
		LatencyMs:  float64(latency.Microseconds()) / 1000.0,
		Error:      errMsg,
	}
}
