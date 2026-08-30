package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/diagnosis"
	"github.com/Thadopz/telltale/internal/observability"

	"github.com/gogf/gf/v2/frame/g"
	"go.opentelemetry.io/otel/attribute"
)

type Runner struct {
	config    Config
	collector Collector
	store     Store
	now       func() time.Time
}

func NewRunner(config Config, collector Collector, store Store) *Runner {
	return &Runner{
		config:    config,
		collector: collector,
		store:     store,
		now:       time.Now,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	if _, err := r.RunOnce(ctx); err != nil {
		g.Log().Warningf(ctx, "watch cycle completed with errors: %v", err)
	}

	ticker := time.NewTicker(r.config.Interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := r.RunOnce(ctx); err != nil {
				g.Log().Warningf(ctx, "watch cycle completed with errors: %v", err)
			}
		}
	}
}

func (r *Runner) RunOnce(ctx context.Context) ([]RuleResult, error) {
	startedAt := time.Now()
	cycleCtx, cycleSpan := observability.StartSpan(ctx, "watch.cycle",
		attribute.Int("watch.rule.count", len(r.config.Rules)),
		attribute.String("watch.tenant", r.config.TenantID),
		attribute.String("watch.environment", r.config.Environment),
	)
	results := make([]RuleResult, 0, len(r.config.Rules))
	var cycleErrors []error

	for _, rule := range r.config.Rules {
		ruleCtx, ruleSpan := observability.StartSpan(cycleCtx, "watch.rule",
			attribute.String("watch.rule.id", rule.ID),
			attribute.String("watch.domain", rule.Domain),
			attribute.String("watch.collector", rule.Collector),
		)
		result := r.evaluateRule(ruleCtx, rule)
		observability.EndSpan(ruleSpan, result.Err,
			attribute.String("watch.transition", result.Transition),
			attribute.Bool("watch.abnormal", result.Abnormal),
		)
		results = append(results, result)
		observability.RecordWatchRule(cycleCtx, rule.ID, rule.Domain, rule.Collector, result.Transition, result.Err)
		if result.Err != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("%s: %w", rule.ID, result.Err))
			continue
		}
		if result.Transition != TransitionNone {
			g.Log().Infof(ctx,
				"watch incident transition rule=%s domain=%s transition=%s value=%v evidence_id=%s",
				rule.ID,
				rule.Domain,
				result.Transition,
				result.Snapshot.Value,
				result.Snapshot.EvidenceID,
			)
		}
	}

	cycleErr := errorsJoin(cycleErrors)
	observability.RecordWatchCycle(cycleCtx, time.Since(startedAt), len(r.config.Rules), cycleErr)
	observability.EndSpan(cycleSpan, cycleErr)
	return results, cycleErr
}

func (r *Runner) evaluateRule(ctx context.Context, rule Rule) RuleResult {
	now := r.now()
	stateKey := r.stateKey(rule)
	state, err := r.store.LoadState(ctx, stateKey)
	if err != nil {
		return RuleResult{Rule: rule, Err: err}
	}
	state.StateKey = stateKey

	queryCtx, cancel := context.WithTimeout(ctx, r.config.QueryTimeout())
	snapshot, collectErr := r.collector.Collect(queryCtx, rule, now)
	cancel()
	if collectErr != nil {
		state = RecordCollectionError(state, rule.ID, collectErr, now)
		if saveErr := r.store.SaveState(ctx, state); saveErr != nil {
			collectErr = fmt.Errorf("%v; save collection error state: %w", collectErr, saveErr)
		}
		return RuleResult{Rule: rule, State: state, Err: collectErr}
	}

	state, transition := ApplySnapshot(state, rule, snapshot)
	abnormal := IsAbnormal(rule, snapshot)
	var incident *Incident
	var diagnosisJob *DiagnosisJob
	if state.State == StateFiring || transition == TransitionResolved {
		builtIncident := r.buildIncident(rule, snapshot, transition)
		incident = &builtIncident
		if transition == TransitionOpened && r.config.Diagnosis.Enabled && rule.AutoDiagnose {
			builtJob := r.buildDiagnosisJob(rule, builtIncident, snapshot)
			diagnosisJob = &builtJob
		}
	}
	if err = r.store.CommitEvaluation(ctx, state, incident, diagnosisJob); err != nil {
		return RuleResult{Rule: rule, Snapshot: snapshot, Abnormal: abnormal, Transition: transition, State: state, Err: err}
	}

	return RuleResult{
		Rule:       rule,
		Snapshot:   snapshot,
		Abnormal:   abnormal,
		Transition: transition,
		State:      state,
	}
}

func (r *Runner) buildIncident(rule Rule, snapshot Snapshot, transition string) Incident {
	fingerprint := r.stateKey(rule)
	hash := sha256.Sum256([]byte(fingerprint))
	incidentID := "inc_" + hex.EncodeToString(hash[:8])
	status := StateFiring
	if transition == TransitionResolved {
		status = "resolved"
	}
	return Incident{
		ID:           incidentID,
		Fingerprint:  fingerprint,
		RuleID:       rule.ID,
		Domain:       rule.Domain,
		Service:      rule.Service,
		Environment:  r.config.Environment,
		Status:       status,
		Severity:     rule.Severity,
		FirstSeenAt:  snapshot.CollectedAt,
		LastSeenAt:   snapshot.CollectedAt,
		ResolvedAt:   resolvedTime(transition, snapshot.CollectedAt),
		LatestValue:  snapshot.Value,
		EvidenceID:   snapshot.EvidenceID,
		EvidenceJSON: snapshot.RawOutput,
	}
}

func (r *Runner) buildDiagnosisJob(rule Rule, incident Incident, snapshot Snapshot) DiagnosisJob {
	openedAt := snapshot.CollectedAt.UTC()
	hash := sha256.Sum256([]byte(incident.Fingerprint + "|" + openedAt.Format(time.RFC3339Nano)))
	jobID := "diag_" + hex.EncodeToString(hash[:8])
	return DiagnosisJob{
		ID:              jobID,
		IncidentID:      incident.ID,
		EpisodeOpenedAt: openedAt,
		Status:          DiagnosisJobPending,
		NextAttemptAt:   openedAt,
		ConversationID:  "watch-" + jobID,
		Request: diagnosis.IncidentDiagnoseRequest{
			JobID:          jobID,
			IncidentID:     incident.ID,
			RuleID:         rule.ID,
			Domain:         rule.Domain,
			Service:        rule.Service,
			Environment:    r.config.Environment,
			Severity:       rule.Severity,
			CurrentStatus:  StateFiring,
			WindowMinutes:  rule.WindowMinutes,
			Operator:       rule.Operator,
			Threshold:      rule.Threshold,
			ObservedValue:  snapshot.Value,
			ObservedAt:     openedAt,
			EvidenceID:     snapshot.EvidenceID,
			DatabaseProbe:  rule.DatabaseProbe.Enabled,
			DatabaseTarget: rule.DatabaseProbe.Target,
		},
	}
}

func (r *Runner) stateKey(rule Rule) string {
	return strings.Join([]string{
		r.config.TenantID,
		r.config.Environment,
		rule.ID,
		rule.Service,
	}, "|")
}

func errorsJoin(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	var message strings.Builder
	for index, err := range errs {
		if index > 0 {
			message.WriteString("; ")
		}
		message.WriteString(err.Error())
	}
	return fmt.Errorf("%s", message.String())
}
