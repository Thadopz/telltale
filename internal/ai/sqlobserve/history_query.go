package sqlobserve

import (
	"context"
	"fmt"
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/gogf/gf/v2/frame/g"
)

func queryLocalHistory(ctx context.Context, plan *QueryPlan) ([]map[string]any, error) {
	if plan == nil || plan.TimeRange == nil {
		return nil, fmt.Errorf("postgresql sampled history requires a time range")
	}
	var statuses []struct {
		Status        string    `json:"status"`
		ErrorCode     string    `json:"error_code"`
		LastSuccessAt time.Time `json:"last_success_at"`
	}
	if err := g.DB().Model("database_observation_status").Ctx(ctx).
		Fields("status", "error_code", "last_success_at").Where("target", plan.Target).Limit(1).Scan(&statuses); err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		return nil, fmt.Errorf("postgresql sampled history source unavailable: sampler has no successful sample")
	}
	if statuses[0].Status != "available" && statuses[0].LastSuccessAt.IsZero() {
		return nil, fmt.Errorf("postgresql sampled history source unavailable: %s", statuses[0].ErrorCode)
	}
	result, err := g.DB().GetAll(ctx, plan.SQL, plan.Args...)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(result))
	for _, record := range result {
		rows = append(rows, record.Map())
	}

	var coverage []struct {
		Start time.Time `json:"actual_start"`
		End   time.Time `json:"actual_end"`
		Count int       `json:"bucket_count"`
	}
	err = g.DB().Raw(`SELECT MIN(bucket_start) AS actual_start, MAX(bucket_end) AS actual_end, COUNT(DISTINCT bucket_end) AS bucket_count
FROM database_observation_intervals
WHERE target = ? AND bucket_end > ? AND bucket_start < ?`,
		plan.Target, plan.TimeRange.Start.UTC(), plan.TimeRange.End.UTC()).Ctx(ctx).Scan(&coverage)
	if err != nil {
		return nil, err
	}
	plan.DataStatus = evidence.DataPartial
	plan.Coverage = map[string]any{
		"requested_start": plan.TimeRange.Start.UTC().Format(time.RFC3339),
		"requested_end":   plan.TimeRange.End.UTC().Format(time.RFC3339),
		"time_semantics":  "sampled_delta",
	}
	if statuses[0].Status != "available" {
		plan.Coverage["sampler_status"] = statuses[0].Status
		plan.Coverage["sampler_error_code"] = statuses[0].ErrorCode
		plan.Coverage["last_success_at"] = statuses[0].LastSuccessAt.UTC().Format(time.RFC3339)
	}
	if len(coverage) > 0 && !coverage[0].Start.IsZero() && !coverage[0].End.IsZero() {
		actualStart := coverage[0].Start.UTC()
		actualEnd := coverage[0].End.UTC()
		plan.Coverage["actual_start"] = actualStart.Format(time.RFC3339)
		plan.Coverage["actual_end"] = actualEnd.Format(time.RFC3339)
		plan.Coverage["sample_buckets"] = coverage[0].Count
		sampleInterval := defaultHistorySampleInterval
		if seconds := optionalConfigInt(ctx, "database.observation_history.sample_interval_seconds"); seconds > 0 {
			sampleInterval = time.Duration(seconds) * time.Second
		}
		requiredBuckets := int((plan.TimeRange.Window() + sampleInterval - 1) / sampleInterval)
		if statuses[0].Status == "available" && !actualStart.After(plan.TimeRange.Start) && !actualEnd.Before(plan.TimeRange.End) && coverage[0].Count >= requiredBuckets {
			plan.DataStatus = evidence.DataPresent
		}
	}
	return rows, nil
}
