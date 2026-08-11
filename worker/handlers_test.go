package worker

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/hibiken/asynq"
)

// capturingHandler records the slog records it receives at or above Error.
type capturingHandler struct {
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func runTracing(t *testing.T, taskType string, handlerErr error) []slog.Record {
	t.Helper()
	cap := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := TracingMiddleware(asynq.HandlerFunc(func(context.Context, *asynq.Task) error {
		return handlerErr
	}))
	_ = h.ProcessTask(context.Background(), asynq.NewTask(taskType, nil))
	return cap.records
}

// find returns the first record with the given message, or nil.
func find(recs []slog.Record, msg string) *slog.Record {
	for i := range recs {
		if recs[i].Message == msg {
			return &recs[i]
		}
	}
	return nil
}

// attr returns the value of the named attribute on a record, or the zero Value.
func attr(r *slog.Record, key string) slog.Value {
	var out slog.Value
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			out = a.Value
			return false
		}
		return true
	})
	return out
}

func TestTracingMiddlewareLogsStartAndDoneOnSuccess(t *testing.T) {
	recs := runTracing(t, "listener:orders", nil)
	started := find(recs, "worker: task started")
	done := find(recs, "worker: task done")
	if started == nil || done == nil {
		t.Fatalf("want task started + task done records, got %d records", len(recs))
	}
	if started.Level != slog.LevelInfo || done.Level != slog.LevelInfo {
		t.Errorf("started/done should be Info level (started=%v done=%v)", started.Level, done.Level)
	}
	if got := attr(started, "task_type").String(); got != "listener:orders" {
		t.Errorf("started task_type = %q, want listener:orders", got)
	}
	if attr(done, "duration").Kind() != slog.KindDuration {
		t.Errorf("done record missing duration attr")
	}
	if find(recs, "worker: task failed") != nil {
		t.Errorf("successful task should not log a failure")
	}
}

func TestTracingMiddlewareLogsFailure(t *testing.T) {
	recs := runTracing(t, "listener:orders", errors.New("handler blew up"))
	if find(recs, "worker: task started") == nil {
		t.Errorf("failing task should still log task started")
	}
	if find(recs, "worker: task done") != nil {
		t.Errorf("failing task should not log task done")
	}
	failed := find(recs, "worker: task failed")
	if failed == nil {
		t.Fatalf("want a task failed record, got none")
	}
	if failed.Level != slog.LevelError {
		t.Errorf("level = %v, want Error", failed.Level)
	}
	if got := attr(failed, "task_type").String(); got != "listener:orders" {
		t.Errorf("failed task_type = %q, want listener:orders", got)
	}
	if _, ok := attr(failed, "error").Any().(error); !ok {
		t.Errorf("failed record missing error attr")
	}
}

func TestTracingMiddlewareSkipsBenignSkipRetry(t *testing.T) {
	recs := runTracing(t, "listener:orders", asynq.SkipRetry)
	if find(recs, "worker: task failed") != nil {
		t.Errorf("bare SkipRetry should not be logged as a failure")
	}
	if find(recs, "worker: task started") == nil {
		t.Errorf("task started should still be logged for a skipped task")
	}
}

func TestWithTaskMiddlewareRunsAfterKitTrio(t *testing.T) {
	var order []string
	probe := func(name string) asynq.MiddlewareFunc {
		return func(next asynq.Handler) asynq.Handler {
			return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
				order = append(order, name)
				return next.ProcessTask(ctx, task)
			})
		}
	}
	h := newHandlers(nil, nil, nil)
	mux := h.mux(func(mux *asynq.ServeMux) {
		mux.HandleFunc("test:", func(context.Context, *asynq.Task) error {
			order = append(order, "handler")
			return nil
		})
	}, []asynq.MiddlewareFunc{probe("first"), probe("second")})

	if err := mux.ProcessTask(context.Background(), asynq.NewTask("test:x", nil)); err != nil {
		t.Fatal(err)
	}
	want := []string{"first", "second", "handler"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
