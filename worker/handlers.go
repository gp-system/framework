package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/gp-system/errs"
	"github.com/gp-system/events"
	"github.com/gp-system/events/scheduler"
	"github.com/gp-system/queue"
	"github.com/gp-system/telemetry/sentryx"
)

var tracer = otel.Tracer("github.com/gp-system/framework/worker")

// handlers holds the dependencies the asynq task handlers close over.
type handlers struct {
	registry *events.Registry
	client   *queue.Client
	jobs     map[string]scheduler.JobEntry
}

func newHandlers(registry *events.Registry, client *queue.Client, sched *scheduler.Schedule) *handlers {
	jobs := map[string]scheduler.JobEntry{}
	if sched != nil {
		for _, j := range sched.Jobs() {
			jobs[j.Name] = j
		}
	}
	return &handlers{registry: registry, client: client, jobs: jobs}
}

// mux builds the asynq handler with the kit middleware trio, the
// WithTaskMiddleware extras and the four task-type handlers (event fan-out,
// listener, scheduled-event trigger, job).
func (h *handlers) mux(extra func(*asynq.ServeMux), mw []asynq.MiddlewareFunc) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.Use(HubMiddleware, RecoverMiddleware, TracingMiddleware)
	mux.Use(mw...)
	mux.HandleFunc(queue.EventTaskPrefix, h.handleEvent)
	mux.HandleFunc(queue.ListenerTaskPrefix, h.handleListener)
	mux.HandleFunc(queue.ScheduleTaskPrefix, h.handleSchedule)
	mux.HandleFunc(queue.JobTaskPrefix, h.handleJob)
	if extra != nil {
		extra(mux)
	}
	return mux
}

// handleEvent expands an event into one task per listener.
func (h *handlers) handleEvent(ctx context.Context, task *asynq.Task) error {
	env, err := queue.DecodeEnvelope(task)
	if err != nil {
		return skipRetry(err)
	}
	return h.fanout(ctx, env)
}

// handleSchedule turns a scheduled trigger into a fresh event fan-out. The
// envelope id is the trigger's task id, so a retry of the same fire reuses it
// and stays idempotent, while each new fire gets a new id.
func (h *handlers) handleSchedule(ctx context.Context, task *asynq.Task) error {
	env, err := queue.DecodeEnvelope(task)
	if err != nil {
		return skipRetry(err)
	}
	if id, ok := asynq.GetTaskID(ctx); ok && id != "" {
		env.ID = id
	} else {
		env.ID = uuid.NewString()
	}
	env.OccurredAt = time.Now()
	env.InjectTrace(ctx)
	return h.fanout(ctx, env)
}

func (h *handlers) fanout(ctx context.Context, env queue.Envelope) error {
	tasks, err := h.registry.FanoutTasks(env)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		slog.WarnContext(ctx, "worker: event has no listeners", slog.String("event", env.Name))
		return nil
	}
	for _, ft := range tasks {
		if _, err := h.client.Enqueue(ctx, ft.Task, ft.Opts...); err != nil {
			if errors.Is(err, queue.ErrDuplicate) {
				continue
			}
			return errs.Wrap(err, "worker: fan out", errs.With("event", env.Name), errs.With("listener", ft.Listener))
		}
	}
	return nil
}

// handleListener runs one listener. A task for a listener that is no longer
// registered (e.g. removed in a later deploy) is skipped rather than retried
// forever.
func (h *handlers) handleListener(ctx context.Context, task *asynq.Task) error {
	handle, ok := h.registry.Handler(task.Type())
	if !ok {
		slog.WarnContext(ctx, "worker: no handler for listener task, skipping", slog.String("task_type", task.Type()))
		return asynq.SkipRetry
	}
	env, err := queue.DecodeEnvelope(task)
	if err != nil {
		return skipRetry(err)
	}
	attempt, _ := asynq.GetRetryCount(ctx)
	ctx = events.ContextWithMeta(ctx, events.Meta{
		ID:         env.ID,
		Name:       env.Name,
		OccurredAt: env.OccurredAt,
		Attempt:    attempt,
	})
	return handle(ctx, env)
}

// handleJob runs a scheduled job.
func (h *handlers) handleJob(ctx context.Context, task *asynq.Task) error {
	name, _ := queue.JobName(task.Type())
	job, ok := h.jobs[name]
	if !ok {
		slog.WarnContext(ctx, "worker: no handler for job, skipping", slog.String("job", name))
		return asynq.SkipRetry
	}
	return job.Fn(ctx)
}

// HubMiddleware gives each task its own Sentry hub so breadcrumbs and scope
// attach only to that task's events. Outermost of the kit trio, so the hub
// is on the context for the tracing and recovery middleware too. No-op when
// Sentry is off. The asynq middleware shape (an error return driving retry
// semantics) is deliberately not unified with the net/http one in httpmw;
// only the context helper (sentryx.WithRequestHub) is shared.
func HubMiddleware(next asynq.Handler) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
		return next.ProcessTask(sentryx.WithRequestHub(ctx), task)
	})
}

// RecoverMiddleware turns a panicking handler into an errs error so the task
// is retried (and the stack is logged) instead of crashing the worker. It
// runs inside HubMiddleware and outside TracingMiddleware, so the converted
// error is still recorded and logged by the tracing span.
func RecoverMiddleware(next asynq.Handler) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				err = errs.NewPanic(rec)
			}
		}()
		return next.ProcessTask(ctx, task)
	})
}

// TracingMiddleware continues the producer's trace (extracted from the
// task's envelope when present) and records a span per task execution, with
// trace-correlated start/done/failed logs.
func TracingMiddleware(next asynq.Handler) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
		if env, err := queue.DecodeEnvelope(task); err == nil {
			ctx = env.ExtractTrace(ctx)
		}
		ctx, span := tracer.Start(ctx, "task "+task.Type(), trace.WithSpanKind(trace.SpanKindConsumer))
		defer span.End()
		span.SetAttributes(attribute.String("messaging.asynq.task_type", task.Type()))
		taskID, _ := asynq.GetTaskID(ctx)
		if taskID != "" {
			span.SetAttributes(attribute.String("messaging.message.id", taskID))
		}
		attempt, _ := asynq.GetRetryCount(ctx)
		slog.LogAttrs(ctx, slog.LevelInfo, "worker: task started",
			slog.String("task_type", task.Type()),
			slog.String("task_id", taskID),
			slog.Int("attempt", attempt))
		start := time.Now()
		err := next.ProcessTask(ctx, task)
		duration := time.Since(start)
		if err != nil {
			span.RecordError(err)
			// Log the failure at Error level with the consumer span still on the
			// context, so it reaches the otelslog bridge (trace-correlated) and
			// the Sentry capture handler. SkipRetry marks a permanent, expected
			// skip (e.g. no handler registered) already warned at the handler.
			if !errors.Is(err, asynq.SkipRetry) {
				slog.LogAttrs(ctx, slog.LevelError, "worker: task failed",
					slog.String("task_type", task.Type()),
					slog.String("task_id", taskID),
					slog.Duration("duration", duration),
					slog.Any("error", err))
			}
			return err
		}
		slog.LogAttrs(ctx, slog.LevelInfo, "worker: task done",
			slog.String("task_type", task.Type()),
			slog.String("task_id", taskID),
			slog.Duration("duration", duration))
		return nil
	})
}

// skipRetry marks a permanent (non-retryable) failure such as an undecodable
// payload; retrying cannot help.
func skipRetry(err error) error {
	return fmt.Errorf("%w: %w", asynq.SkipRetry, err)
}
