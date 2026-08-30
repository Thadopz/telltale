package input_preprocessor

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultMaxQueryChars = 4000

	IntentChat     = "chat"
	IntentDiagnose = "diagnose"
	IntentQuery    = "query"
	IntentExplain  = "explain"
	IntentPlan     = "plan"
	IntentReport   = "report"

	DomainPayment   = "payment"
	DomainOrder     = "order"
	DomainNotify    = "notify"
	DomainAlert     = "alert"
	DomainLog       = "log"
	DomainSlowSQL   = "slow_sql"
	DomainKnowledge = "knowledge"
	DomainGeneral   = "general"

	RiskReadOnly = "read_only"
	RiskHigh     = "high"
)

type ProcessedInput struct {
	OriginalQuery string
	CleanQuery    string
	Intent        string
	Domain        string
	Confidence    string
	Entities      map[string]string
	TimeRange     *TimeRange
	ToolHints     []ToolHint
	MissingSlots  []string
	RiskLevel     string
	Notes         []string
}

type TimeRange struct {
	Start       time.Time
	End         time.Time
	Source      string
	Description string
}

type ToolHint struct {
	Name   string
	Reason string
}

type intentDomain struct {
	Intent     string
	Domain     string
	Confidence string
}

func Process(ctx context.Context, query string, now time.Time) ProcessedInput {
	_ = ctx
	clean := CleanQuery(query, DefaultMaxQueryChars)
	entities := ExtractEntities(clean)
	classification := DetectIntentDomain(clean, entities)
	risk := DetectRisk(clean)
	tr := CompleteTimeRange(clean, classification.Intent, now)

	processed := ProcessedInput{
		OriginalQuery: query,
		CleanQuery:    clean,
		Intent:        classification.Intent,
		Domain:        classification.Domain,
		Confidence:    classification.Confidence,
		Entities:      entities,
		TimeRange:     tr,
		RiskLevel:     risk,
	}
	processed.ToolHints = BuildToolHints(processed)
	processed.MissingSlots = DetectMissingSlots(processed)
	if clean == "" {
		processed.Notes = append(processed.Notes, "empty query after cleaning")
	}
	if utf8.RuneCountInString(query) > DefaultMaxQueryChars {
		processed.Notes = append(processed.Notes, fmt.Sprintf("query truncated to %d chars", DefaultMaxQueryChars))
	}
	return processed
}

func (p ProcessedInput) FormatPrompt() string {
	var b strings.Builder
	writeKV(&b, "Intent", p.Intent)
	writeKV(&b, "Domain", p.Domain)
	writeKV(&b, "Confidence", p.Confidence)
	writeKV(&b, "Risk Level", p.RiskLevel)
	writeKV(&b, "Clean Query", p.CleanQuery)
	if p.TimeRange != nil {
		writeKV(&b, "Time Range", fmt.Sprintf("%s to %s (%s; %s)",
			p.TimeRange.Start.Format("2006-01-02 15:04:05"),
			p.TimeRange.End.Format("2006-01-02 15:04:05"),
			p.TimeRange.Source,
			p.TimeRange.Description,
		))
	}
	if len(p.Entities) > 0 {
		writeKV(&b, "Entities", formatStringMap(p.Entities))
	}
	if len(p.ToolHints) > 0 {
		parts := make([]string, 0, len(p.ToolHints))
		for _, hint := range p.ToolHints {
			if hint.Reason == "" {
				parts = append(parts, hint.Name)
			} else {
				parts = append(parts, hint.Name+" ("+hint.Reason+")")
			}
		}
		writeKV(&b, "Suggested Tools", strings.Join(parts, ", "))
	}
	if len(p.MissingSlots) > 0 {
		writeKV(&b, "Missing Slots", strings.Join(p.MissingSlots, ", "))
	}
	if len(p.Notes) > 0 {
		writeKV(&b, "Notes", strings.Join(p.Notes, "; "))
	}
	return strings.TrimSpace(b.String())
}

func CleanQuery(query string, maxChars int) string {
	query = strings.TrimPrefix(query, "\ufeff")
	var b strings.Builder
	lastSpace := false
	for _, r := range query {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	clean := strings.TrimSpace(b.String())
	if maxChars <= 0 || utf8.RuneCountInString(clean) <= maxChars {
		return clean
	}
	runes := []rune(clean)
	return string(runes[:maxChars])
}

func ExtractEntities(query string) map[string]string {
	entities := map[string]string{}
	patterns := []struct {
		key string
		re  *regexp.Regexp
	}{
		{"payment_order_no", regexp.MustCompile(`(?i)(?:payment[_\s-]*order[_\s-]*no|payment[_\s-]*no|支付(?:订单|单)?号|支付订单|支付单|订单号|订单)\s*[:：=]?\s*([A-Za-z0-9][A-Za-z0-9._:-]{2,})`)},
		{"notify_task_no", regexp.MustCompile(`(?i)(?:notify[_\s-]*task[_\s-]*no|notifyTaskNo|通知任务号|通知任务)\s*[:：=]?\s*([A-Za-z0-9][A-Za-z0-9._:-]{2,})`)},
		{"trace_id", regexp.MustCompile(`(?i)(?:trace[_\s-]*id|traceId|链路ID|追踪ID)\s*[:：=]?\s*([A-Za-z0-9][A-Za-z0-9._:-]{2,})`)},
		{"error_code", regexp.MustCompile(`(?i)(?:error[_\s-]*code|错误码)\s*[:：=]?\s*([A-Za-z0-9][A-Za-z0-9._:-]{2,})`)},
		{"service", regexp.MustCompile(`(?i)(?:service|服务)\s*[:：=]?\s*([A-Za-z0-9][A-Za-z0-9._:-]{1,})`)},
	}
	for _, pattern := range patterns {
		if match := pattern.re.FindStringSubmatch(query); len(match) > 1 {
			entities[pattern.key] = match[1]
		}
	}
	if _, ok := entities["error_code"]; !ok {
		if match := regexp.MustCompile(`\b(?:ERR|ERROR|FAIL|E)[_-]?[A-Z0-9]{2,}\b`).FindString(query); match != "" {
			entities["error_code"] = match
		}
	}
	return entities
}

func DetectIntentDomain(query string, entities map[string]string) intentDomain {
	normalized := strings.ToLower(query)
	domain := DomainGeneral
	confidence := "low"
	switch {
	case containsAny(normalized, "慢 sql", "慢sql", "slow sql", "sql latency", "rows examined", "performance_schema"):
		domain, confidence = DomainSlowSQL, "high"
	case containsAny(normalized, "支付", "payment", "回调", "callback", "退款", "refund") || entities["payment_order_no"] != "":
		domain, confidence = DomainPayment, "high"
	case containsAny(normalized, "通知", "notify", "notify task") || entities["notify_task_no"] != "":
		domain, confidence = DomainNotify, "high"
	case containsAny(normalized, "订单", "order"):
		domain, confidence = DomainOrder, "medium"
	case containsAny(normalized, "告警", "报警", "alert", "prometheus"):
		domain, confidence = DomainAlert, "high"
	case containsAny(normalized, "日志", "log") || entities["trace_id"] != "":
		domain, confidence = DomainLog, "medium"
	case containsAny(normalized, "文档", "知识库", "手册", "runbook", "sop", "manual"):
		domain, confidence = DomainKnowledge, "high"
	}

	intent := IntentChat
	hasQueryVerb := containsAny(normalized, "查", "查询", "看一下", "有没有", "多少", "状态", "query", "search", "show", "status")
	switch {
	case containsAny(normalized, "报告", "report"):
		intent = IntentReport
	case containsAny(normalized, "方案", "计划", "plan", "步骤", "怎么处理", "如何处理"):
		intent = IntentPlan
	case hasQueryVerb && (entities["payment_order_no"] != "" || entities["notify_task_no"] != "" || entities["trace_id"] != ""):
		intent = IntentQuery
	case containsAny(normalized, "失败", "异常", "超时", "报错", "错误", "积压", "排查", "诊断", "故障", "troubleshoot", "diagnose", "timeout", "failure", "error", "exception", "stuck"):
		intent = IntentDiagnose
	case hasQueryVerb:
		intent = IntentQuery
	case containsAny(normalized, "解释", "什么是", "为什么", "why", "explain"):
		intent = IntentExplain
	}
	if intent == IntentChat && domain != DomainGeneral {
		intent = IntentQuery
	}
	if confidence == "low" && intent != IntentChat {
		confidence = "medium"
	}
	return intentDomain{Intent: intent, Domain: domain, Confidence: confidence}
}

func CompleteTimeRange(query string, intent string, now time.Time) *TimeRange {
	normalized := strings.ToLower(query)
	if containsAny(normalized, "今天", "today") {
		return &TimeRange{Start: dayStart(now), End: now, Source: "explicit", Description: "today"}
	}
	if containsAny(normalized, "昨天", "yesterday") {
		yesterday := now.AddDate(0, 0, -1)
		start := dayStart(yesterday)
		return &TimeRange{Start: start, End: start.Add(24*time.Hour - time.Second), Source: "explicit", Description: "yesterday"}
	}
	if containsAny(normalized, "过去半小时", "近半小时", "最近半小时") {
		return &TimeRange{Start: now.Add(-30 * time.Minute), End: now, Source: "relative", Description: "relative recent window"}
	}
	if match := regexp.MustCompile(`(?:最近|过去|近)\s*(\d+)\s*(分钟|分|小时|个小时)`).FindStringSubmatch(normalized); len(match) > 2 {
		n, _ := strconv.Atoi(match[1])
		if n > 0 {
			unit := match[2]
			duration := time.Duration(n) * time.Minute
			if strings.Contains(unit, "小时") {
				duration = time.Duration(n) * time.Hour
			}
			return &TimeRange{Start: now.Add(-duration), End: now, Source: "relative", Description: "relative recent window"}
		}
	}
	if match := regexp.MustCompile(`(?i)(?:last|past)\s+(\d+)\s*(m|min|minute|minutes|h|hour|hours)`).FindStringSubmatch(normalized); len(match) > 2 {
		n, _ := strconv.Atoi(match[1])
		if n > 0 {
			duration := time.Duration(n) * time.Minute
			if strings.HasPrefix(match[2], "h") {
				duration = time.Duration(n) * time.Hour
			}
			return &TimeRange{Start: now.Add(-duration), End: now, Source: "relative", Description: "relative recent window"}
		}
	}
	if containsAny(normalized, "刚才", "现在", "最近", "current", "now", "recent") {
		return &TimeRange{Start: now.Add(-30 * time.Minute), End: now, Source: "relative", Description: "recent default 30 minutes"}
	}
	if intent == IntentDiagnose || intent == IntentQuery || intent == IntentReport {
		return &TimeRange{Start: now.Add(-30 * time.Minute), End: now, Source: "default", Description: "missing time range; default 30 minutes for investigation/query"}
	}
	return nil
}

// NeedsCurrentTime reports whether a request needs the wall clock in its model
// context. A closed range such as "yesterday" does not need a volatile time
// message, while live investigations and relative ranges do.
func NeedsCurrentTime(processed ProcessedInput) bool {
	normalized := strings.ToLower(processed.CleanQuery)
	isKnowledgeRequest := processed.Domain == DomainKnowledge || containsAny(normalized, "文档", "知识库", "手册", "runbook", "sop", "manual")
	if processed.TimeRange == nil || isKnowledgeRequest {
		return false
	}

	switch processed.TimeRange.Source {
	case "relative":
		return true
	case "explicit":
		return processed.TimeRange.Description == "today"
	case "default":
		return processed.Intent == IntentDiagnose || processed.Intent == IntentQuery || processed.Intent == IntentReport
	default:
		return false
	}
}

func BuildToolHints(p ProcessedInput) []ToolHint {
	var hints []ToolHint
	add := func(name string, reason string) {
		for _, hint := range hints {
			if hint.Name == name {
				return
			}
		}
		hints = append(hints, ToolHint{Name: name, Reason: reason})
	}
	normalized := strings.ToLower(p.CleanQuery)
	if containsAny(normalized, "kubectl", "kubernetes", "k8s", "namespace", "pod", "deployment", "deploy/", "rollout", "astronomy-shop") {
		add("observability_agent", "infrastructure and workload evidence")
	}
	switch p.Domain {
	case DomainPayment, DomainOrder, DomainNotify:
		add("payment_agent", "payment, order, or notification domain")
	case DomainSlowSQL:
		add("database_agent", "database and slow SQL domain")
	case DomainAlert:
		add("observability_agent", "alerts and runtime observability domain")
	case DomainLog:
		add("observability_agent", "logs and runtime observability domain")
	case DomainKnowledge:
		add("knowledge_agent", "internal documentation domain")
	}
	if p.Entities["payment_order_no"] != "" {
		add("payment_agent", "payment order domain")
	}
	if p.Entities["trace_id"] != "" && p.Domain != DomainSlowSQL {
		if p.Domain == DomainPayment || p.Domain == DomainOrder || p.Domain == DomainNotify {
			add("payment_agent", "payment trace domain")
		} else {
			add("observability_agent", "runtime trace domain")
		}
	}
	if p.Domain != DomainKnowledge && containsAny(normalized, "文档", "知识库", "手册", "runbook", "sop", "manual") {
		add("knowledge_agent", "internal documentation domain")
	}
	return hints
}

func DetectRisk(query string) string {
	normalized := strings.ToLower(query)
	if containsAny(normalized, "退款", "重试", "关闭订单", "关单", "删除", "更新", "写库", "ddl", "kill session", "重启", "扩缩容", "refund", "retry", "delete", "update", "restart", "scale") {
		return RiskHigh
	}
	return RiskReadOnly
}

func DetectMissingSlots(p ProcessedInput) []string {
	normalized := strings.ToLower(p.CleanQuery)
	if p.Entities["payment_order_no"] == "" &&
		containsAny(normalized, "这个订单", "这笔支付", "这个支付", "this order", "this payment") &&
		!containsAny(normalized, "是否异常", "异常吗", "失败多", "有没有异常") {
		return []string{"payment_order_no"}
	}
	return nil
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func writeKV(b *strings.Builder, key string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString("- ")
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
}

func formatStringMap(values map[string]string) string {
	parts := make([]string, 0, len(values))
	for _, key := range sortedKeys(values) {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
