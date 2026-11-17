package mem

import (
	"regexp"
	"strings"
)

var sensitiveMemoryPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|passwd|secret|private[_ -]?key)\b\s*[:=]`),
	regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b(bearer)\s+[a-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)\b(mysql|postgres|mongodb|redis)://[^@\s]+@`),
}

var temporaryMemoryPhrases = []string{
	"当前错误率", "当前成功率", "当前延迟", "当前告警", "本次故障", "这次故障", "刚才查询", "工具返回",
	"today's error rate", "current error rate", "current latency", "current alert", "this incident", "tool returned",
}

func containsSensitiveMemory(content string) bool {
	for _, pattern := range sensitiveMemoryPatterns {
		if pattern.MatchString(content) {
			return true
		}
	}
	return false
}

func containsTemporaryMemory(content string) bool {
	normalized := strings.ToLower(content)
	for _, phrase := range temporaryMemoryPhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}
