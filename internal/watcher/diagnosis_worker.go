package watcher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Thadopz/telltale/internal/diagnosis"
	"github.com/Thadopz/telltale/internal/observability"
	"github.com/gogf/gf/v2/frame/g"
)

type DiagnosisWorker struct {
	config  Config
	store   *SQLiteStore
	service diagnosis.DiagnosticService
	now     func() time.Time
}

func NewDiagnosisWorker(config Config, store *SQLiteStore, service diagnosis.DiagnosticService) *DiagnosisWorker {
	return &DiagnosisWorker{
		config:  config,
		store:   store,
		service: service,
		now:     time.Now,
	}
}

func (worker *DiagnosisWorker) Run(ctx context.Context) error {
	if err := worker.validate(); err != nil {
		return err
	}
	ticker := time.NewTicker(worker.config.DiagnosisPollInterval())
	defer ticker.Stop()
	for {
		if _, err := worker.RunReady(ctx); err != nil && !errors.Is(err, context.Canceled) {
			g.Log().Warningf(ctx, "incident diagnosis worker cycle failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// RunReady processes every job that is ready now. It does not wait for a
// future retry delay, which keeps `watch --once` deterministic.
func (worker *DiagnosisWorker) RunReady(ctx context.Context) (int, error) {
	if err := worker.validate(); err != nil {
		return 0, err
	}
	processed := 0
	for {
		job, err := worker.claim(ctx)
		if err != nil {
			return processed, err
		}
		if job == nil {
			return processed, nil
		}
		processed++
		if err := worker.runJob(ctx, *job); err != nil {
			return processed, err
		}
	}
}

func (worker *DiagnosisWorker) validate() error {
	if worker == nil || worker.store == nil {
		return fmt.Errorf("incident diagnosis worker store is unavailable")
	}
	if worker.service == nil {
		return fmt.Errorf("incident diagnosis service is unavailable")
	}
	return nil
}

func (worker *DiagnosisWorker) claim(ctx context.Context) (*DiagnosisJob, error) {
	now := worker.now().UTC()
	leaseDuration := worker.config.DiagnosisRunTimeout() + 30*time.Second
	maxAttempts := len(worker.config.DiagnosisRetryDelays()) + 1
	job, err := worker.store.ClaimDiagnosisJob(ctx, now, leaseDuration, maxAttempts)
	if err != nil {
		return nil, err
	}
	if job != nil {
		queueDelay := time.Duration(0)
		if !job.CreatedAt.IsZero() {
			queueDelay = now.Sub(job.CreatedAt)
		}
		observability.RecordWatchDiagnosisJob(ctx, "claimed", job.Request.Domain, job.Request.Service, queueDelay, nil)
	}
	return job, nil
}

func (worker *DiagnosisWorker) runJob(ctx context.Context, job DiagnosisJob) error {
	startedAt := worker.now().UTC()
	runCtx, cancel := context.WithTimeout(ctx, worker.config.DiagnosisRunTimeout())
	result, runErr := worker.service.DiagnoseIncident(runCtx, job.Request)
	cancel()

	if ctx.Err() != nil {
		requeueContext, requeueCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer requeueCancel()
		if err := worker.store.RequeueCanceledDiagnosisJob(requeueContext, job, worker.now().UTC()); err != nil {
			return err
		}
		observability.RecordWatchDiagnosisJob(ctx, "requeued", job.Request.Domain, job.Request.Service, time.Since(startedAt), ctx.Err())
		return ctx.Err()
	}
	if runErr != nil {
		status, nextAttemptAt, err := worker.store.FailDiagnosisJob(ctx, job, runErr, worker.now().UTC(), worker.config.DiagnosisRetryDelays())
		if err != nil {
			return err
		}
		outcome := "failed"
		if status == DiagnosisJobPending {
			outcome = "retry_scheduled"
		}
		observability.RecordWatchDiagnosisJob(ctx, outcome, job.Request.Domain, job.Request.Service, time.Since(startedAt), runErr)
		g.Log().Warningf(ctx,
			"incident diagnosis job=%s incident=%s attempt=%d status=%s next_attempt_at=%v",
			job.ID,
			job.IncidentID,
			job.AttemptCount,
			status,
			nextAttemptAt,
		)
		return nil
	}
	if err := worker.store.CompleteDiagnosisJob(ctx, job, result, worker.now().UTC()); err != nil {
		return err
	}
	observability.RecordWatchDiagnosisJob(ctx, "succeeded", job.Request.Domain, job.Request.Service, time.Since(startedAt), nil)
	g.Log().Infof(ctx,
		"incident diagnosis completed job=%s incident=%s attempt=%d run_id=%s",
		job.ID,
		job.IncidentID,
		job.AttemptCount,
		result.RunID,
	)
	return nil
}
