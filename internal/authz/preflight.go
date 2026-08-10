package authz

import "strings"

// RequestPreflightDecision is an advisory compatibility result. It must not
// grant or deny a tool call; the Authorizer and tool metadata are authoritative.
type RequestPreflightDecision struct {
	Blocked bool
	Reason  string
}

const highRiskMutationReason = "high-risk mutation request"

var highRiskActionPhrases = []string{
	"删除",
	"清空",
	"销毁",
	"delete",
	"drop",
	"truncate",
	"写入",
	"insert",
	"update",
	"更新",
	"修改",
	"更改",
	"覆盖",
	"修复",
	"恢复",
	"回滚",
	"rollback",
	"restore",
	"patch",
	"退款",
	"refund",
	"补偿",
	"重试",
	"retry",
	"关闭",
	"禁用",
	"重启",
	"restart",
	"扩容",
	"缩容",
	"scale",
	"发布",
	"下线",
	"切换",
}

var highRiskTargetPhrases = []string{
	"数据库",
	"mysql",
	"sql",
	"表",
	"订单",
	"支付",
	"payment",
	"order",
	"生产",
	"prod",
	"服务",
	"deployment",
	"pod",
	"集群",
	"cluster",
	"系统",
	"账号",
	"用户",
	"配置",
	"secret",
	"密钥",
	"文件",
	"缓存",
	"cache",
}

var broadScopePhrases = []string{
	"所有",
	"全部",
	"批量",
	"全量",
	"all",
	"every",
	"bulk",
	"entire",
}

var executionPhrases = []string{
	"请",
	"立刻",
	"立即",
	"马上",
	"现在",
	"直接",
	"调用",
	"执行",
	"帮我",
	"开始",
	"please",
	"immediately",
	"now",
	"execute",
	"invoke",
	"perform",
	"apply",
}

var mutationStartPhrases = []string{
	"删除",
	"清空",
	"销毁",
	"写入",
	"更新",
	"修改",
	"更改",
	"覆盖",
	"修复",
	"恢复",
	"回滚",
	"退款",
	"补偿",
	"重试",
	"关闭",
	"禁用",
	"重启",
	"扩容",
	"缩容",
	"发布",
	"下线",
	"切换",
	"delete ",
	"drop ",
	"truncate ",
	"insert ",
	"update ",
	"write ",
	"refund ",
	"retry ",
	"restart ",
	"restore ",
	"rollback ",
	"patch ",
	"scale ",
}

var guidancePhrases = []string{
	"如何",
	"怎么",
	"怎样",
	"为什么",
	"只解释",
	"仅解释",
	"what is",
	"how to",
	"why",
	"explain",
}

// PreflightRequest recognizes likely mutation intent for telemetry and legacy
// clients. The result is not used as the runtime permission decision.
func PreflightRequest(query string) RequestPreflightDecision {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return RequestPreflightDecision{}
	}
	if isGuidanceRequest(query) || isExplicitlyReadOnly(query) {
		return RequestPreflightDecision{}
	}
	if !containsAnyPhrase(query, highRiskActionPhrases) {
		return RequestPreflightDecision{}
	}
	if !containsAnyPhrase(query, highRiskTargetPhrases) && !containsAnyPhrase(query, broadScopePhrases) {
		return RequestPreflightDecision{}
	}
	if !hasExecutionIntent(query) {
		return RequestPreflightDecision{}
	}

	return RequestPreflightDecision{
		Blocked: true,
		Reason:  highRiskMutationReason,
	}
}

func isGuidanceRequest(query string) bool {
	return containsAnyPhrase(query, guidancePhrases)
}

func isExplicitlyReadOnly(query string) bool {
	readOnlyPhrases := []string{
		"只读",
		"只查询",
		"仅查询",
		"不要写入",
		"不要修改",
		"不要删除",
		"read-only",
		"read only",
		"do not write",
		"do not modify",
		"do not delete",
	}
	if !containsAnyPhrase(query, readOnlyPhrases) {
		return false
	}
	return !containsAnyPhrase(query, executionPhrases)
}

func hasExecutionIntent(query string) bool {
	if containsAnyPhrase(query, executionPhrases) {
		return true
	}
	if containsAnyPrefix(query, mutationStartPhrases) {
		return true
	}
	return containsAnyPhrase(query, broadScopePhrases)
}

func containsAnyPhrase(query string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(query, phrase) {
			return true
		}
	}
	return false
}

func containsAnyPrefix(query string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(query, prefix) {
			return true
		}
	}
	return false
}
