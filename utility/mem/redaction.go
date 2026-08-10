package mem

import (
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"encoding/json"
	"regexp"
	"strings"
)

const RedactionVersion = 1

var (
	persistedSecretKeyPattern = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|authorization|private[_-]?key|client[_-]?secret|access[_-]?key|dsn|connection[_-]?string)`)
	persistedDSNPattern       = regexp.MustCompile(`(?i)(\b(?:mysql|postgres|postgresql|redis|mongodb|sqlite)://)[^\s"']+`)
	persistedBearerPattern    = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{12,}`)
	persistedPEMPattern       = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
	persistedAssignment       = regexp.MustCompile(`(?i)(\b(?:password|passwd|secret|token|api[_-]?key|authorization)\b\s*[:=]\s*)([^\s,;}]+)`)
	persistedHighEntropy      = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9+/_=-]{31,}\b`)
)

func redactToolInput(raw string) string {
	return redactJSON(raw)
}

// redactToolOutput stores only a sanitized envelope. The raw data field is
// intentionally removed because structured observations are the durable
// evidence contract; raw tool payloads are not.
func redactToolOutput(raw string) string {
	parsed := evidence.Parse(raw)
	if !parsed.IsJSON {
		return ""
	}
	env := parsed.Envelope
	env.Data = nil
	env.Message = redactText(env.Message)
	env.Error = redactText(env.Error)
	env.Summary = redactText(env.Summary)
	env.Subject.Fields = redactStringMap(env.Subject.Fields)
	env.Scope.Fields = redactStringMap(env.Scope.Fields)
	for index := range env.Observations {
		env.Observations[index].Subject.Fields = redactStringMap(env.Observations[index].Subject.Fields)
		env.Observations[index].Scope.Fields = redactStringMap(env.Observations[index].Scope.Fields)
		env.Observations[index].Fields = redactAnyMap(env.Observations[index].Fields)
		env.Observations[index].Summary = redactText(env.Observations[index].Summary)
	}
	return evidence.Marshal(env)
}

func redactJSON(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return ""
	}
	value = redactJSONValue("", value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func redactJSONValue(key string, value any) any {
	if persistedSecretKeyPattern.MatchString(key) {
		return "<redacted>"
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			typed[childKey] = redactJSONValue(childKey, child)
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = redactJSONValue(key, child)
		}
		return typed
	case string:
		return redactText(typed)
	default:
		return value
	}
}

func redactAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return values
	}
	redacted := make(map[string]any, len(values))
	for key, value := range values {
		redacted[key] = redactJSONValue(key, value)
	}
	return redacted
}

func redactStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return values
	}
	redacted := make(map[string]string, len(values))
	for key, value := range values {
		if persistedSecretKeyPattern.MatchString(key) {
			redacted[key] = "<redacted>"
			continue
		}
		redacted[key] = redactText(value)
	}
	return redacted
}

func redactText(text string) string {
	text = persistedPEMPattern.ReplaceAllString(text, "<redacted:pem>")
	text = persistedDSNPattern.ReplaceAllString(text, `${1}<redacted:dsn>`)
	text = persistedBearerPattern.ReplaceAllString(text, `${1}<redacted:token>`)
	text = persistedAssignment.ReplaceAllString(text, `${1}<redacted>`)
	return persistedHighEntropy.ReplaceAllString(text, "<redacted:high-entropy>")
}
