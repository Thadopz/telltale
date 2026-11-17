package evidence

// LogWindowView retains bounded backend statistics and controls, never raw log text.
func LogWindowView(data map[string]any) map[string]any {
	if data["count_unit"] != "log_event" {
		return nil
	}
	view := map[string]any{}
	for _, key := range []string{"source_id", "capabilities", "service", "operation", "start", "end", "baseline_start", "baseline_end", "baseline_reason", "baseline_coverage", "count_unit", "totals", "baseline_totals", "candidates", "other", "statistics_coverage", "samples_coverage", "query_ref", "limitations"} {
		if value, ok := data[key]; ok {
			view[key] = value
		}
	}
	var controls []any
	if samples, ok := data["samples"].([]any); ok {
		for _, value := range samples {
			if sample, ok := value.(map[string]any); ok && sample["level"] == "INFO" {
				controls = append(controls, map[string]any{"operation": sample["operation"], "level": sample["level"], "reference": sample["reference"]})
			}
		}
	}
	if len(controls) > 0 {
		view["controls"] = controls
	}
	return view
}
