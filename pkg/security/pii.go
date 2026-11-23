package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

var (
	// Regex for credit card numbers (13 to 19 digits, optional dashes or spaces).
	regexCreditCard = regexp.MustCompile(`\b(?:\d{4}[ -]?){3}\d{4}\b|\b\d{13,19}\b`)

	// Regex for US Social Security Numbers (SSN).
	regexSSN = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)

	// Regex for email addresses.
	regexEmail = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)

	// Common sensitive field names in JSON payloads.
	sensitiveKeyNames = map[string]bool{
		"password":      true,
		"passwd":        true,
		"secret":        true,
		"token":         true,
		"access_token":  true,
		"refresh_token": true,
		"api_key":       true,
		"apikey":        true,
		"cvv":           true,
		"cvc":           true,
		"credit_card":   true,
		"card_number":   true,
		"ssn":           true,
	}

	// Sensitive HTTP header names to redact or hash before shadowing.
	sensitiveHeaderNames = map[string]bool{
		"authorization":       true,
		"cookie":              true,
		"set-cookie":          true,
		"x-api-key":           true,
		"x-auth-token":        true,
		"proxy-authorization": true,
	}
)

// PIIConfig controls sanitization parameters.
type PIIConfig struct {
	MaskCreditCards bool
	MaskSSNs        bool
	MaskEmails      bool
	MaskTokens      bool
	Deterministic   bool // Use SHA-256 slice rather than static [REDACTED]
}

// DefaultPIIConfig returns standard security defaults.
func DefaultPIIConfig() PIIConfig {
	return PIIConfig{
		MaskCreditCards: true,
		MaskSSNs:        true,
		MaskEmails:      true,
		MaskTokens:      true,
		Deterministic:   true,
	}
}

// MaskValue computes a masked string representation.
func (cfg PIIConfig) MaskValue(raw string) string {
	if !cfg.Deterministic {
		return "[REDACTED_PII]"
	}
	hash := sha256.Sum256([]byte(raw))
	shortHex := hex.EncodeToString(hash[:8])
	return "[MASKED-SHA256:" + shortHex + "]"
}

// SanitizeHeaders redacts or hashes sensitive credentials in outbound shadow headers.
func SanitizeHeaders(headers http.Header, cfg PIIConfig) http.Header {
	sanitized := headers.Clone()
	for name := range sanitized {
		lower := strings.ToLower(name)
		if sensitiveHeaderNames[lower] {
			vals := sanitized[name]
			for i, v := range vals {
				sanitized[name][i] = cfg.MaskValue(v)
			}
		}
	}
	return sanitized
}

// SanitizeBody processes request or response bodies, scrubbing sensitive PII fields.
func SanitizeBody(body []byte, cfg PIIConfig) []byte {
	if len(body) == 0 {
		return body
	}

	// Attempt structured JSON sanitization first
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		sanitizedTree := sanitizeJSONTree(parsed, cfg)
		if out, err := json.Marshal(sanitizedTree); err == nil {
			return out
		}
	}

	// Raw string regex fallback
	text := string(body)
	if cfg.MaskCreditCards {
		text = regexCreditCard.ReplaceAllStringFunc(text, func(s string) string {
			return cfg.MaskValue(s)
		})
	}
	if cfg.MaskSSNs {
		text = regexSSN.ReplaceAllStringFunc(text, func(s string) string {
			return cfg.MaskValue(s)
		})
	}
	if cfg.MaskEmails {
		text = regexEmail.ReplaceAllStringFunc(text, func(s string) string {
			return cfg.MaskValue(s)
		})
	}
	return []byte(text)
}

// sanitizeJSONTree recursively scrubs sensitive keys and pattern matches in JSON values.
func sanitizeJSONTree(node any, cfg PIIConfig) any {
	if node == nil {
		return nil
	}

	switch val := node.(type) {
	case map[string]any:
		res := make(map[string]any, len(val))
		for k, v := range val {
			kLower := strings.ToLower(k)
			if sensitiveKeyNames[kLower] {
				res[k] = cfg.MaskValue(anyToString(v))
				continue
			}
			res[k] = sanitizeJSONTree(v, cfg)
		}
		return res

	case []any:
		res := make([]any, len(val))
		for i, v := range val {
			res[i] = sanitizeJSONTree(v, cfg)
		}
		return res

	case string:
		scrubbed := val
		if cfg.MaskCreditCards && regexCreditCard.MatchString(scrubbed) {
			scrubbed = regexCreditCard.ReplaceAllStringFunc(scrubbed, cfg.MaskValue)
		}
		if cfg.MaskSSNs && regexSSN.MatchString(scrubbed) {
			scrubbed = regexSSN.ReplaceAllStringFunc(scrubbed, cfg.MaskValue)
		}
		if cfg.MaskEmails && regexEmail.MatchString(scrubbed) {
			scrubbed = regexEmail.ReplaceAllStringFunc(scrubbed, cfg.MaskValue)
		}
		return scrubbed

	default:
		return val
	}
}

func anyToString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
