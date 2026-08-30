package sqlobserve

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

const (
	DefaultObservationWindow = 30 * time.Minute
	MaxObservationWindow     = 24 * time.Hour
	allowedFutureSkew        = 5 * time.Minute
)

type TimeRangeSource string

const (
	TimeRangeIncident        TimeRangeSource = "incident"
	TimeRangeExplicit        TimeRangeSource = "explicit"
	TimeRangeWindow          TimeRangeSource = "window"
	TimeRangeDatabaseDefault TimeRangeSource = "database_default"
)

type TimeRange struct {
	Start       time.Time       `json:"start"`
	End         time.Time       `json:"end"`
	Source      TimeRangeSource `json:"source"`
	Semantics   string          `json:"semantics"`
	ClockSource string          `json:"clock_source"`
}

func (r TimeRange) Window() time.Duration {
	return r.End.Sub(r.Start)
}

type trustedTimeRangeKey struct{}

type trustedTimeRange struct {
	Target string
	Range  TimeRange
	Calls  *atomic.Int32
}

// WithTrustedTimeRange pins an automatic diagnosis to its controlled target
// and incident window. Tool arguments supplied by the model cannot override it.
func WithTrustedTimeRange(ctx context.Context, target string, start, end time.Time) context.Context {
	rangeValue := TimeRange{
		Start:       start.UTC(),
		End:         end.UTC(),
		Source:      TimeRangeIncident,
		Semantics:   "exact_half_open",
		ClockSource: "incident_observed_at",
	}
	return context.WithValue(ctx, trustedTimeRangeKey{}, trustedTimeRange{
		Target: strings.TrimSpace(target),
		Range:  rangeValue,
		Calls:  &atomic.Int32{},
	})
}

func applyTrustedTimeRange(ctx context.Context, request QueryRequest) (QueryRequest, error) {
	trusted, ok := ctx.Value(trustedTimeRangeKey{}).(trustedTimeRange)
	if !ok {
		return request, nil
	}
	if trusted.Calls != nil && trusted.Calls.Add(1) > 1 {
		return request, fmt.Errorf("automatic incident database probe allows at most one slow-query call")
	}
	request.Target = trusted.Target
	request.StartTime = ""
	request.EndTime = ""
	request.Window = ""
	request.DefaultTimeRange = false
	request.ResolvedTimeRange = &trusted.Range
	return request, nil
}

func needsDatabaseClock(request QueryRequest) bool {
	if request.ResolvedTimeRange != nil {
		return false
	}
	return strings.TrimSpace(request.Window) != "" || request.DefaultTimeRange
}

func applyConfiguredTimeLimits(ctx context.Context, request QueryRequest) QueryRequest {
	if request.DefaultWindow <= 0 {
		request.DefaultWindow = DefaultObservationWindow
		if minutes := optionalConfigInt(ctx, "database.observation_history.default_window_minutes"); minutes > 0 {
			request.DefaultWindow = time.Duration(minutes) * time.Minute
		}
	}
	if request.MaxWindow <= 0 {
		request.MaxWindow = MaxObservationWindow
		if hours := optionalConfigInt(ctx, "database.observation_history.max_window_hours"); hours > 0 {
			request.MaxWindow = time.Duration(hours) * time.Hour
		}
	}
	if request.MaxWindow > MaxObservationWindow {
		request.MaxWindow = MaxObservationWindow
	}
	if request.DefaultWindow > request.MaxWindow {
		request.DefaultWindow = request.MaxWindow
	}
	return request
}

func resolveTimeRange(request QueryRequest, databaseNow time.Time) (*TimeRange, error) {
	if request.ResolvedTimeRange != nil {
		copyRange := *request.ResolvedTimeRange
		if err := validateTimeRange(copyRange.Start, copyRange.End, databaseNow, request.MaxWindow); err != nil {
			return nil, err
		}
		return &copyRange, nil
	}

	startText := strings.TrimSpace(request.StartTime)
	endText := strings.TrimSpace(request.EndTime)
	windowText := strings.TrimSpace(request.Window)
	if (startText == "") != (endText == "") {
		return nil, fmt.Errorf("start_time and end_time must be provided together")
	}
	if startText != "" && windowText != "" {
		return nil, fmt.Errorf("start_time/end_time cannot be combined with window")
	}

	if startText != "" {
		start, err := time.Parse(time.RFC3339, startText)
		if err != nil {
			return nil, fmt.Errorf("invalid start_time: use RFC3339")
		}
		end, err := time.Parse(time.RFC3339, endText)
		if err != nil {
			return nil, fmt.Errorf("invalid end_time: use RFC3339")
		}
		start = start.UTC()
		end = end.UTC()
		if err := validateTimeRange(start, end, databaseNow, request.MaxWindow); err != nil {
			return nil, err
		}
		return &TimeRange{Start: start, End: end, Source: TimeRangeExplicit, Semantics: "exact_half_open", ClockSource: "explicit"}, nil
	}

	if windowText == "" && !request.DefaultTimeRange {
		return nil, nil
	}
	window := request.DefaultWindow
	if window <= 0 {
		window = DefaultObservationWindow
	}
	source := TimeRangeDatabaseDefault
	if windowText != "" {
		parsed, err := time.ParseDuration(windowText)
		if err != nil {
			return nil, fmt.Errorf("invalid window: use a duration such as 30m")
		}
		window = parsed
		source = TimeRangeWindow
	}
	end := databaseNow.UTC()
	start := end.Add(-window)
	if err := validateTimeRange(start, end, databaseNow, request.MaxWindow); err != nil {
		return nil, err
	}
	return &TimeRange{Start: start, End: end, Source: source, Semantics: "exact_half_open", ClockSource: "database_current_timestamp"}, nil
}

func validateTimeRange(start, end, databaseNow time.Time, maximum time.Duration) error {
	if start.IsZero() || end.IsZero() {
		return fmt.Errorf("time range cannot contain a zero timestamp")
	}
	if !start.Before(end) {
		return fmt.Errorf("time range must be non-empty and use start < end")
	}
	if maximum <= 0 || maximum > MaxObservationWindow {
		maximum = MaxObservationWindow
	}
	if end.Sub(start) > maximum {
		return fmt.Errorf("time range exceeds maximum of %s", maximum)
	}
	if !databaseNow.IsZero() && end.After(databaseNow.UTC().Add(allowedFutureSkew)) {
		return fmt.Errorf("time range end is too far in the future")
	}
	return nil
}
