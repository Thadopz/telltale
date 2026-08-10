package codeintel

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)(\b(?:password|passwd|secret|token|api[_-]?key|authorization|private[_-]?key|client[_-]?secret|access[_-]?key|secret[_-]?key|dsn|connection[_-]?string)\b\s*(?::=|[:=])\s*)("[^"]*"|'[^']*'|[^,\s#]+)`)
	dsnUserInfoPattern      = regexp.MustCompile(`(?i)(://)([^/\s:@]+(?::[^@\s]*)?@)`)
	driverDSNPattern        = regexp.MustCompile(`(?i)(\b(?:mysql|pgsql|postgres|sqlite|redis):)([^:\s]+):([^@\s]+)@`)
	bearerPattern           = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{12,}`)
	highEntropyPattern      = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9+/_=-]{31,}\b`)
	sensitiveQueryPattern   = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9]{16,}|bearer\s+[A-Za-z0-9._~+/=-]{12,}|(?:password|passwd|secret|token|api[_-]?key)\s*[:=]\s*\S+)`)
)

func sanitizeSource(path string, raw []byte) ([]byte, bool) {
	if len(raw) == 0 || bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
		return nil, false
	}
	if bytes.Contains(raw, []byte("-----BEGIN")) {
		return nil, false
	}
	sanitized := redactText(string(raw))
	if strings.TrimSpace(sanitized) == "" {
		return nil, false
	}
	return []byte(sanitized), true
}

func redactText(content string) string {
	lines := strings.SplitAfter(content, "\n")
	for index, line := range lines {
		line = secretAssignmentPattern.ReplaceAllStringFunc(line, redactAssignment)
		line = dsnUserInfoPattern.ReplaceAllString(line, `${1}<redacted>@`)
		line = driverDSNPattern.ReplaceAllString(line, `${1}<redacted>:<redacted>@`)
		line = bearerPattern.ReplaceAllString(line, `${1}<redacted:bearer>`)
		line = highEntropyPattern.ReplaceAllStringFunc(line, redactHighEntropyToken)
		lines[index] = line
	}
	return strings.Join(lines, "")
}

func redactAssignment(match string) string {
	separator := strings.Index(match, ":=")
	separatorLength := 2
	if separator < 0 {
		separator = strings.IndexAny(match, ":=")
		separatorLength = 1
	}
	if separator < 0 {
		return "<redacted:secret>"
	}
	prefix := match[:separator+separatorLength]
	value := strings.TrimSpace(match[separator+separatorLength:])
	if strings.HasPrefix(value, "\"") {
		return prefix + " \"<redacted:secret>\""
	}
	if strings.HasPrefix(value, "'") {
		return prefix + " '<redacted:secret>'"
	}
	return prefix + " <redacted:secret>"
}

func redactHighEntropyToken(token string) string {
	if strings.HasPrefix(token, "<redacted") || !looksLikeHighEntropyToken(token) {
		return token
	}
	return "<redacted:high_entropy>"
}

func looksLikeHighEntropyToken(token string) bool {
	if len(token) < 32 {
		return false
	}
	hasLetter := false
	hasDigit := false
	hasSeparator := false
	for _, character := range token {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z':
			hasLetter = true
		case character >= '0' && character <= '9':
			hasDigit = true
		case strings.ContainsRune("+/_=-", character):
			hasSeparator = true
		}
	}
	return hasLetter && hasDigit && (hasSeparator || len(token) >= 40)
}

func containsSensitiveQuery(query string) bool {
	if sensitiveQueryPattern.MatchString(strings.TrimSpace(query)) {
		return true
	}
	return looksLikeHighEntropyToken(strings.TrimSpace(query))
}

func isBroadQuery(query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	for _, word := range []string{
		"payment", "error", "callback", "order", "service", "config", "configuration", "sql", "database", "notification", "notify",
	} {
		if query == word {
			return true
		}
	}
	return false
}

func isSensitiveFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".key", ".pem", ".crt", ".cer", ".p12", ".pfx":
		return true
	}
	for _, marker := range []string{"credential", "secret", "private-key"} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return false
}
