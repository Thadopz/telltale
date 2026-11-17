package mem

import (
	"encoding/hex"
	"encoding/json"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"regexp"
	"strings"
)

const RedactionVersion = 1

// RedactToolPayload reuses persistence redaction before untrusted log text reaches a model.
func RedactToolPayload(value any) any { return redactJSONValue("", value) }

// RedactKnowledgePayload preserves validated document identifiers while redacting content.
func RedactKnowledgePayload(value any) any {
	docs, ok := value.([]any)
	if !ok {
		return RedactToolPayload(value)
	}
	for index, item := range docs {
		doc, ok := item.(map[string]any)
		if !ok {
			docs[index] = RedactToolPayload(item)
			continue
		}
		fields := RedactKnowledgeFields(map[string]any{"document_id": doc["id"], "metadata": doc["meta_data"]})
		redacted := redactAnyMap(doc)
		if id, ok := fields["document_id"].(string); ok && validKnowledgeHash(id) {
			redacted["id"] = id
		}
		if metadata, ok := fields["metadata"].(map[string]any); ok {
			redacted["meta_data"] = metadata
		}
		docs[index] = redacted
	}
	return docs
}

// RedactKnowledgeFields preserves typed document references, never free text or secrets.
func RedactKnowledgeFields(fields map[string]any) map[string]any {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return redactAnyMap(fields)
	}
	var copy map[string]any
	if json.Unmarshal(encoded, &copy) != nil {
		return redactAnyMap(fields)
	}
	refs := map[string]string{}
	for _, key := range []string{"document_id"} {
		if text, ok := copy[key].(string); ok && validKnowledgeHash(text) {
			refs[key] = text
		}
	}
	type group struct {
		key    string
		values map[string]string
	}
	var groups []group
	for _, key := range []string{"metadata", "selection"} {
		if values, ok := copy[key].(map[string]any); ok {
			keep := map[string]string{}
			for _, name := range []string{"_source_hash", "_chunk_hash", "goal_hash", "source_digest"} {
				if text, ok := values[name].(string); ok && validKnowledgeHash(text) {
					keep[name] = text
				}
			}
			groups = append(groups, group{key, keep})
		}
	}
	redacted := redactAnyMap(copy)
	for key, value := range refs {
		redacted[key] = value
	}
	for _, group := range groups {
		values := redacted[group.key].(map[string]any)
		for key, value := range group.values {
			values[key] = value
		}
	}
	return redacted
}

func validKnowledgeHash(text string) bool {
	if len(text) != 64 {
		return false
	}
	_, err := hex.DecodeString(text)
	return err == nil
}

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
	env.Summary = redactText(env.Summary)
	if env.Failure != nil {
		env.Failure.Reason = redactText(env.Failure.Reason)
	}
	if env.Evidence == nil {
		return evidence.Marshal(env)
	}
	for index := range env.Evidence.Observations {
		env.Evidence.Observations[index].Subject.Fields = redactStringMap(env.Evidence.Observations[index].Subject.Fields)
		env.Evidence.Observations[index].Scope.Fields = redactStringMap(env.Evidence.Observations[index].Scope.Fields)
		if env.Evidence.Observations[index].Kind == evidence.ObservationRunbookRule {
			env.Evidence.Observations[index].Fields = RedactKnowledgeFields(env.Evidence.Observations[index].Fields)
		} else {
			env.Evidence.Observations[index].Fields = redactAnyMap(env.Evidence.Observations[index].Fields)
		}
		env.Evidence.Observations[index].Summary = redactText(env.Evidence.Observations[index].Summary)
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
