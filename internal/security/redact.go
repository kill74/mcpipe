package security

import (
	"fmt"
	"regexp"
	"strings"
)

type Redactor struct {
	values   []string
	patterns []*regexp.Regexp
}

func NewRedactor() Redactor {
	return Redactor{
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{12,}`),
			regexp.MustCompile(`(?i)(api[_-]?key["']?\s*[:=]\s*["']?)[A-Za-z0-9._~+/=-]{8,}`),
			regexp.MustCompile(`(?i)(token["']?\s*[:=]\s*["']?)[A-Za-z0-9._~+/=-]{8,}`),
			regexp.MustCompile(`(?i)(password["']?\s*[:=]\s*["']?)[^"',\s]{4,}`),
			regexp.MustCompile(`sk-ant-[A-Za-z0-9._-]+`),
		},
	}
}

func (r *Redactor) AddValue(value string) {
	value = strings.TrimSpace(value)
	if len(value) < 4 || value == "[REDACTED]" {
		return
	}
	for _, existing := range r.values {
		if existing == value {
			return
		}
	}
	r.values = append(r.values, value)
}

func (r *Redactor) AddNamedValue(name string, value string) {
	if IsSensitiveName(name) {
		r.AddValue(value)
	}
}

func (r Redactor) RedactString(input string) string {
	out := input
	for _, value := range r.values {
		out = strings.ReplaceAll(out, value, "[REDACTED]")
	}
	for _, pattern := range r.patterns {
		out = pattern.ReplaceAllStringFunc(out, func(match string) string {
			lower := strings.ToLower(match)
			for _, marker := range []string{"bearer ", "api", "token", "password"} {
				if strings.Contains(lower, marker) {
					if idx := strings.LastIndexAny(match, "=: "); idx >= 0 && idx+1 < len(match) {
						return match[:idx+1] + "[REDACTED]"
					}
				}
			}
			return "[REDACTED]"
		})
	}
	return out
}

func (r Redactor) RedactAny(value any) any {
	switch typed := value.(type) {
	case string:
		return r.RedactString(typed)
	case map[string]any:
		out := map[string]any{}
		for key, item := range typed {
			if IsSensitiveName(key) {
				out[key] = "[REDACTED]"
			} else {
				out[key] = r.RedactAny(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = r.RedactAny(item)
		}
		return out
	case map[string]string:
		out := map[string]string{}
		for key, item := range typed {
			if IsSensitiveName(key) {
				out[key] = "[REDACTED]"
			} else {
				out[key] = r.RedactString(item)
			}
		}
		return out
	default:
		return value
	}
}

func (r Redactor) Error(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", r.RedactString(err.Error()))
}

func IsSensitiveName(name string) bool {
	name = strings.ToLower(name)
	for _, marker := range []string{"secret", "token", "api_key", "apikey", "password", "credential", "private_key", "bearer"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}
