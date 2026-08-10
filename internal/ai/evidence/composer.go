package evidence

import (
	"fmt"
	"sort"
	"strings"
)

// ComposeAnswer keeps the historical English rendering entry point. Claim
// discovery does not happen here; callers must provide Matcher reports.
func ComposeAnswer(reports []CoverageReport) string {
	return ComposeAnswerForLocale(reports, "en")
}

// ComposeAnswerForLocale renders only data that already exists in a
// CoverageReport. It never creates claims, selects evidence, or changes a
// coverage status.
func ComposeAnswerForLocale(reports []CoverageReport, locale string) string {
	if len(reports) == 0 {
		return ""
	}

	sortedReports := append([]CoverageReport(nil), reports...)
	sort.SliceStable(sortedReports, func(left, right int) bool {
		return sortedReports[left].Claim.ClaimID < sortedReports[right].Claim.ClaimID
	})

	if isChineseLocale(locale) {
		return composeChineseAnswer(sortedReports)
	}
	return composeEnglishAnswer(sortedReports)
}

func composeEnglishAnswer(reports []CoverageReport) string {
	var builder strings.Builder
	writeEnglishSection(&builder, "Verified facts", reports, CoverageCovered)
	writeEnglishSection(&builder, "Supported hypotheses", reports, CoverageSupported)
	writeEnglishSection(&builder, "Refuted hypotheses", reports, CoverageRefuted)
	writeEnglishSection(&builder, "Static implementation evidence", reports, CoverageStaticallySupported)
	writeEnglishSection(&builder, "Next checks", reports, CoverageActionOnly)
	writeEnglishSection(&builder, "Partially supported findings", reports, CoveragePartial)
	writeEnglishSection(&builder, "Unknown or failed checks", reports, CoverageUnknown)
	writeEnglishSection(&builder, "Evidence conflicts", reports, CoverageConflict)
	writeEnglishSection(&builder, "Evidence gaps", reports, CoverageUncovered)
	builder.WriteString("Next steps:\n")
	builder.WriteString("- Add targeted evidence for claims that are partial, unknown, or uncovered before making a stronger diagnosis.\n")
	return strings.TrimSpace(builder.String())
}

func composeChineseAnswer(reports []CoverageReport) string {
	var builder strings.Builder
	writeChineseSection(&builder, "已验证事实", reports, CoverageCovered)
	writeChineseSection(&builder, "已支持假设", reports, CoverageSupported)
	writeChineseSection(&builder, "已反驳假设", reports, CoverageRefuted)
	writeChineseSection(&builder, "静态实现证据", reports, CoverageStaticallySupported)
	writeChineseSection(&builder, "下一步检查", reports, CoverageActionOnly)
	writeChineseSection(&builder, "部分支持的判断", reports, CoveragePartial)
	writeChineseSection(&builder, "未知或失败的检查", reports, CoverageUnknown)
	writeChineseSection(&builder, "相互冲突的证据", reports, CoverageConflict)
	writeChineseSection(&builder, "证据缺口", reports, CoverageUncovered)
	builder.WriteString("下一步：\n")
	builder.WriteString("- 为部分支持、未知或未覆盖的 Claim 补充定向证据后，再给出更强的诊断结论。\n")
	return strings.TrimSpace(builder.String())
}

func writeEnglishSection(builder *strings.Builder, title string, reports []CoverageReport, status string) {
	selected := reportsWithStatus(reports, status)
	if len(selected) == 0 {
		return
	}
	builder.WriteString(title)
	builder.WriteString(":\n")
	for _, report := range selected {
		builder.WriteString("- ")
		builder.WriteString(formatClaim(report))
		builder.WriteByte('\n')
	}
	builder.WriteByte('\n')
}

func writeChineseSection(builder *strings.Builder, title string, reports []CoverageReport, status string) {
	selected := reportsWithStatus(reports, status)
	if len(selected) == 0 {
		return
	}
	builder.WriteString(title)
	builder.WriteString("：\n")
	for _, report := range selected {
		builder.WriteString("- ")
		builder.WriteString(formatChineseClaim(report))
		builder.WriteByte('\n')
	}
	builder.WriteByte('\n')
}

func reportsWithStatus(reports []CoverageReport, status string) []CoverageReport {
	selected := make([]CoverageReport, 0)
	for _, report := range reports {
		if report.Status == status {
			selected = append(selected, report)
		}
	}
	return selected
}

func formatClaim(report CoverageReport) string {
	claimName := report.Claim.ClaimType
	if report.Claim.ClaimID != "" {
		claimName += " [" + report.Claim.ClaimID + "]"
	}
	if report.Claim.AssertionKind != "" {
		claimName += " (" + report.Claim.AssertionKind + ")"
	}
	if fields := formatClaimFields(report.Claim.Fields); fields != "" {
		claimName += ": " + fields
	}
	if len(report.MatchedEvidenceIDs) > 0 {
		claimName += "; evidence=" + formatMatchedEvidence(report)
	}
	if len(report.FailedQueryIDs) > 0 {
		claimName += "; failed_queries=" + strings.Join(report.FailedQueryIDs, ", ")
	}
	if len(report.MissingFields) > 0 {
		claimName += "; missing=" + requirementNames(report.MissingFields)
	}
	return claimName
}

func formatMatchedEvidence(report CoverageReport) string {
	if len(report.MatchedFields) == 0 {
		return strings.Join(report.MatchedEvidenceIDs, ", ")
	}

	parts := make([]string, 0, len(report.MatchedFields))
	seen := make(map[string]bool)
	for _, match := range report.MatchedFields {
		if strings.TrimSpace(match.EvidenceID) == "" {
			continue
		}
		part := match.EvidenceID
		metadata := make([]string, 0, 4)
		if match.SourceKind != "" {
			metadata = append(metadata, "source="+string(match.SourceKind))
		}
		if match.Revision != "" {
			metadata = append(metadata, "revision="+match.Revision)
		}
		if match.VersionAlignment != "" {
			metadata = append(metadata, "version_alignment="+string(match.VersionAlignment))
		}
		if match.SourceKind != "" {
			metadata = append(metadata, fmt.Sprintf("runtime_verified=%t", match.RuntimeVerified))
		}
		if len(metadata) > 0 {
			part += "{" + strings.Join(metadata, ", ") + "}"
		}
		if !seen[part] {
			seen[part] = true
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return strings.Join(report.MatchedEvidenceIDs, ", ")
	}
	return strings.Join(parts, ", ")
}

func formatChineseClaim(report CoverageReport) string {
	claimName := chineseClaimType(report.Claim.ClaimType)
	if report.Claim.ClaimID != "" {
		claimName += "（" + report.Claim.ClaimID + "）"
	}
	if fields := formatClaimFields(report.Claim.Fields); fields != "" {
		claimName += "：" + fields
	}
	if len(report.MatchedEvidenceIDs) > 0 {
		claimName += "；证据=" + formatMatchedEvidence(report)
	}
	if len(report.FailedQueryIDs) > 0 {
		claimName += "；失败查询=" + strings.Join(report.FailedQueryIDs, ", ")
	}
	if len(report.MissingFields) > 0 {
		claimName += "；缺少=" + requirementNames(report.MissingFields)
	}
	return claimName
}

func formatClaimFields(fields map[string]any) string {
	if len(fields) == 0 {
		return ""
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(fmt.Sprint(fields[key]))
		if value == "" {
			continue
		}
		values = append(values, key+"="+value)
	}
	return strings.Join(values, ", ")
}

func chineseClaimType(claimType string) string {
	labels := map[string]string{
		ClaimMetricAnomaly:             "指标异常",
		ClaimMetricValue:               "指标值",
		ClaimLogError:                  "日志错误",
		ClaimStatusDistribution:        "状态分布",
		ClaimStateMismatch:             "状态不一致",
		ClaimSlowSQL:                   "慢 SQL",
		ClaimRunbookRule:               "运行手册规则",
		ClaimPaymentMetricAbnormal:     "支付指标异常",
		ClaimPaymentOrderState:         "支付订单状态",
		ClaimPaymentStateMismatch:      "支付状态不一致",
		ClaimPaymentLogError:           "支付日志错误",
		ClaimPaymentStatusDistribution: "支付状态分布",
		ClaimCallbackTimeoutRelated:    "支付回调超时关联",
		ClaimCallbackTimeoutRootCause:  "支付回调超时根因假设",
		ClaimImplementationContains:    "代码实现包含",
		ClaimImplementationMayCall:     "代码可能调用",
		ClaimConfigValueDefined:        "配置值定义",
		ClaimErrorMappedAtCodeLocation: "代码位置错误映射",
	}
	if label, ok := labels[claimType]; ok {
		return label
	}
	return claimType
}

func isChineseLocale(locale string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "zh")
}

func requirementNames(requirements []FieldRequirement) string {
	names := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		names = append(names, requirement.Field)
	}
	return strings.Join(names, ", ")
}
