package evidence

import (
	"encoding/json"
	"regexp"
)

const untrustedInstructionPlaceholder = "<untrusted_instruction_redacted>"

var untrustedInstructionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget|override)\s+(?:all\s+)?(?:the\s+)?(?:previous|prior|above|earlier)\s+(?:instructions|rules|messages|prompts)\b`),
	regexp.MustCompile(`(?i)\b(?:system|developer)\s+(?:message|prompt|instructions?)\s*:`),
	regexp.MustCompile(`(?i)\b(?:you\s+are\s+now|act\s+as)\s+(?:the\s+)?(?:system|developer|administrator)\b`),
	regexp.MustCompile("\u5ffd\u7565\u4e4b\u524d\u7684\u89c4\u5219"),
	regexp.MustCompile("(?:\u5ffd\u7565|\u65e0\u89c6|\u8df3\u8fc7|\u8986\u76d6)(?:\u6240\u6709)?(?:\u4e4b\u524d|\u4ee5\u4e0a|\u5148\u524d)(?:\u7684)?(?:\u6307\u4ee4|\u89c4\u5219|\u63d0\u793a)"),
	regexp.MustCompile("(?:\u7cfb\u7edf|\u5f00\u53d1\u8005)(?:\u6d88\u606f|\u63d0\u793a|\u6307\u4ee4)\\s*[:\uff1a]"),
	regexp.MustCompile("\u5e76\u58f0\u79f0\u6570\u636e\u5e93\u5df2\u7ecf\u5220\u9664"),
}

// SanitizeUntrustedText removes known instruction-shaped content from data
// returned by tools. Tool output remains data, but it must not be allowed to
// act as a second system prompt for the model.
func SanitizeUntrustedText(value string) string {
	sanitized := value
	for _, pattern := range untrustedInstructionPatterns {
		sanitized = pattern.ReplaceAllString(sanitized, untrustedInstructionPlaceholder)
	}
	return sanitized
}

// SanitizeUntrustedValue recursively sanitizes values that may be shown to an
// agent. It deliberately preserves the value shape so useful tool data is not
// silently discarded.
func SanitizeUntrustedValue(value any) any {
	switch typed := value.(type) {
	case string:
		return SanitizeUntrustedText(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = SanitizeUntrustedValue(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = SanitizeUntrustedValue(item)
		}
		return result
	default:
		return value
	}
}

// SanitizeUntrustedEnvelope preserves the structured result while making
// instruction-shaped content inert before it reaches the model.
func SanitizeUntrustedEnvelope(envelope Envelope) Envelope {
	envelope.Summary = SanitizeUntrustedText(envelope.Summary)
	envelope.Data = SanitizeUntrustedValue(envelope.Data)
	if envelope.Failure != nil {
		envelope.Failure.Reason = SanitizeUntrustedText(envelope.Failure.Reason)
	}
	if envelope.Evidence == nil {
		return envelope
	}
	for index := range envelope.Evidence.Observations {
		observation := &envelope.Evidence.Observations[index]
		observation.Subject = sanitizeSubject(observation.Subject)
		observation.Scope = sanitizeScope(observation.Scope)
		observation.Fields = sanitizeStringMapValues(observation.Fields)
		observation.Summary = SanitizeUntrustedText(observation.Summary)
	}
	return envelope
}

// SanitizeUntrustedJSON keeps a raw JSON fixture or adapter response valid
// while applying the same content rule recursively to all string values.
func SanitizeUntrustedJSON(raw string) string {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return SanitizeUntrustedText(raw)
	}
	sanitized, err := json.Marshal(SanitizeUntrustedValue(value))
	if err != nil {
		return SanitizeUntrustedText(raw)
	}
	return string(sanitized)
}

func sanitizeSubject(subject SubjectRef) SubjectRef {
	subject.Type = SanitizeUntrustedText(subject.Type)
	subject.Service = SanitizeUntrustedText(subject.Service)
	subject.OrderNo = SanitizeUntrustedText(subject.OrderNo)
	subject.TraceID = SanitizeUntrustedText(subject.TraceID)
	subject.Fields = sanitizeStringMap(subject.Fields)
	return subject
}

func sanitizeScope(scope EvidenceScope) EvidenceScope {
	scope.TimeRange = SanitizeUntrustedText(scope.TimeRange)
	scope.StartTime = SanitizeUntrustedText(scope.StartTime)
	scope.EndTime = SanitizeUntrustedText(scope.EndTime)
	scope.Fields = sanitizeStringMap(scope.Fields)
	return scope
}

func sanitizeStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[SanitizeUntrustedText(key)] = SanitizeUntrustedText(value)
	}
	return result
}

func sanitizeStringMapValues(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[SanitizeUntrustedText(key)] = SanitizeUntrustedValue(value)
	}
	return result
}
