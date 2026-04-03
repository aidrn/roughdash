package jobs

import (
	"context"
	"errors"
	"sync"
	"time"

	"roughdash/internal/db"
	"roughdash/internal/models"
)

type Handler func(context.Context, models.Job) error

type Subscriber interface {
	NotifyJob(models.Job)
	NotifyJobEvent(models.JobEvent)
}

type Engine struct {
	store       *db.Store
	subscriber  Subscriber
	handlers    map[string]Handler
	wakeCh      chan struct{}
	activeJobs  map[string]context.CancelFunc
	activeMu    sync.Mutex
}

func NewEngine(store *db.Store, subscriber Subscriber) *Engine {
	return &Engine{
		store:      store,
		subscriber: subscriber,
		handlers:   make(map[string]Handler),
		wakeCh:     make(chan struct{}, 1),
		activeJobs: make(map[string]context.CancelFunc),
	}
}

func (e *Engine) Register(jobType string, handler Handler) {
	e.handlers[jobType] = handler
}

func (e *Engine) Wake() {
	select {
	case e.wakeCh <- struct{}{}:
	default:
	}
}

func (e *Engine) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		if err := e.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			time.Sleep(time.Second)
		}

		select {
		case <-ctx.Done():
			return
		case <-e.wakeCh:
		case <-ticker.C:
		}
	}
}

func (e *Engine) runOnce(ctx context.Context) error {
	jobs, err := e.store.ListRunnableJobs(ctx)
	if err != nil {
		return err
	}

	for _, job := range jobs {
		handler, ok := e.handlers[job.Type]
		if !ok {
			_ = e.store.UpdateJobState(ctx, job.ID, models.JobStatusFailed, "no job handler registered", job.Progress)
			continue
		}
		if err := e.store.UpdateJobState(ctx, job.ID, models.JobStatusRunning, "", job.Progress); err != nil {
			return err
		}
		running, _ := e.store.GetJob(ctx, job.ID)
		if running != nil {
			e.subscriber.NotifyJob(*running)
		}

		jobCtx, cancel := context.WithCancel(ctx)
		e.activeMu.Lock()
		e.activeJobs[job.ID] = cancel
		e.activeMu.Unlock()

		err := handler(jobCtx, job)

		e.activeMu.Lock()
		delete(e.activeJobs, job.ID)
		e.activeMu.Unlock()
		cancel()

		updated, getErr := e.store.GetJob(ctx, job.ID)
		if getErr != nil {
			continue
		}

		switch {
		case err == nil:
			_ = e.store.UpdateJobState(ctx, job.ID, models.JobStatusCompleted, "", 1)
		case errors.Is(err, context.Canceled):
			if updated.Status == models.JobStatusCancelled || updated.Status == models.JobStatusPaused {
				break
			}
			_ = e.store.UpdateJobState(ctx, job.ID, models.JobStatusInterrupted, "", updated.Progress)
		default:
			_, _ = e.store.AddJobEvent(ctx, job.ID, "error", err.Error())
			_ = e.store.UpdateJobState(ctx, job.ID, models.JobStatusFailed, err.Error(), updated.Progress)
		}
		finalJob, _ := e.store.GetJob(ctx, job.ID)
		if finalJob != nil {
			e.subscriber.NotifyJob(*finalJob)
		}
	}
	return nil
}

func (e *Engine) AddEvent(ctx context.Context, jobID, level, message string) error {
	event, err := e.store.AddJobEvent(ctx, jobID, level, message)
	if err != nil {
		return err
	}
	e.subscriber.NotifyJobEvent(*event)
	return nil
}

func (e *Engine) UpdateProgress(ctx context.Context, jobID string, progress float64) error {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if err := e.store.UpdateJobState(ctx, jobID, job.Status, job.Error, progress); err != nil {
		return err
	}
	updated, err := e.store.GetJob(ctx, jobID)
	if err == nil {
		e.subscriber.NotifyJob(*updated)
	}
	return err
}

func (e *Engine) Pause(ctx context.Context, jobID string) error {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status == models.JobStatusQueued || job.Status == models.JobStatusInterrupted || job.Status == models.JobStatusFailed {
		return e.store.UpdateJobState(ctx, jobID, models.JobStatusPaused, "", job.Progress)
	}
	if job.Status == models.JobStatusRunning {
		if err := e.store.UpdateJobState(ctx, jobID, models.JobStatusPaused, "", job.Progress); err != nil {
			return err
		}
		e.activeMu.Lock()
		cancel := e.activeJobs[jobID]
		e.activeMu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return nil
}

func (e *Engine) Resume(ctx context.Context, jobID string) error {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status != models.JobStatusPaused && job.Status != models.JobStatusFailed && job.Status != models.JobStatusInterrupted {
		return nil
	}
	if err := e.store.UpdateJobState(ctx, jobID, models.JobStatusQueued, "", job.Progress); err != nil {
		return err
	}
	e.Wake()
	return nil
}

func (e *Engine) Cancel(ctx context.Context, jobID string) error {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if err := e.store.UpdateJobState(ctx, jobID, models.JobStatusCancelled, "", job.Progress); err != nil {
		return err
	}
	e.activeMu.Lock()
	cancel := e.activeJobs[jobID]
	e.activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}
