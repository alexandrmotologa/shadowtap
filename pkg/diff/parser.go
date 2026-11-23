package diff

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

// PathTuple represents a single flattened node in a JSON payload.
type PathTuple struct {
	Path      string `json:"path"`
	Type      string `json:"type"`
	Value     any    `json:"value"`
	IsMasked  bool   `json:"is_masked"`
}

// FlattenJSON parses raw JSON and produces a path-indexed map of PathTuples.
// If maskConfig is provided, volatile fields are flagged and excluded or replaced.
func FlattenJSON(raw []byte, maskConfig *MaskConfig) (map[string]PathTuple, any, error) {
	if len(raw) == 0 {
		return make(map[string]PathTuple), nil, nil
	}

	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, nil, fmt.Errorf("invalid json: %w", err)
	}

	flattened := make(map[string]PathTuple)
	cleanRoot := traverseAndFlatten("", parsed, flattened, maskConfig)

	return flattened, cleanRoot, nil
}

// traverseAndFlatten recursively visits nodes, populates flattened map, and returns a clean tree.
func traverseAndFlatten(currentPath string, node any, out map[string]PathTuple, mask *MaskConfig) any {
	if node == nil {
		t := PathTuple{
			Path:  currentPath,
			Type:  "null",
			Value: nil,
		}
		if currentPath != "" {
			out[currentPath] = t
		}
		return nil
	}

	switch val := node.(type) {
	case map[string]any:
		cleanMap := make(map[string]any)
		// Sort keys for deterministic traversal
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			childPath := k
			if currentPath != "" {
				childPath = currentPath + "." + k
			}

			// Check if field path is ignored by mask
			if mask != nil && mask.IsPathIgnored(childPath) {
				out[childPath] = PathTuple{
					Path:     childPath,
					Type:     typeName(val[k]),
					Value:    val[k],
					IsMasked: true,
				}
				cleanMap[k] = "[MASKED_VOLATILE]"
				continue
			}

			cleanVal := traverseAndFlatten(childPath, val[k], out, mask)
			cleanMap[k] = cleanVal
		}
		return cleanMap

	case []any:
		cleanSlice := make([]any, len(val))
		for i, elem := range val {
			childPath := currentPath + "[" + strconv.Itoa(i) + "]"

			if mask != nil && mask.IsPathIgnored(childPath) {
				out[childPath] = PathTuple{
					Path:     childPath,
					Type:     typeName(elem),
					Value:    elem,
					IsMasked: true,
				}
				cleanSlice[i] = "[MASKED_VOLATILE]"
				continue
			}

			cleanElem := traverseAndFlatten(childPath, elem, out, mask)
			cleanSlice[i] = cleanElem
		}
		return cleanSlice

	case string:
		isVolatile := mask != nil && mask.IsValueVolatile(val)
		if isVolatile {
			if currentPath != "" {
				out[currentPath] = PathTuple{
					Path:     currentPath,
					Type:     "string",
					Value:    val,
					IsMasked: true,
				}
			}
			return "[MASKED_VOLATILE]"
		}
		if currentPath != "" {
			out[currentPath] = PathTuple{
				Path:     currentPath,
				Type:     "string",
				Value:    val,
				IsMasked: false,
			}
		}
		return val

	case float64:
		if currentPath != "" {
			out[currentPath] = PathTuple{
				Path:     currentPath,
				Type:     "number",
				Value:    val,
				IsMasked: false,
			}
		}
		return val

	case bool:
		if currentPath != "" {
			out[currentPath] = PathTuple{
				Path:     currentPath,
				Type:     "boolean",
				Value:    val,
				IsMasked: false,
			}
		}
		return val

	default:
		tName := typeName(val)
		if currentPath != "" {
			out[currentPath] = PathTuple{
				Path:     currentPath,
				Type:     tName,
				Value:    val,
				IsMasked: false,
			}
		}
		return val
	}
}

// typeName extracts a standardized JSON-friendly type string.
func typeName(v any) string {
	if v == nil {
		return "null"
	}
	switch v.(type) {
	case string:
		return "string"
	case float64, int, int64, float32:
		return "number"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return reflect.TypeOf(v).String()
	}
}
