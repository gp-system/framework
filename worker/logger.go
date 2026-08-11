package worker

import (
	"context"
	"fmt"
	"log/slog"
)

// slogLogger adapts asynq's Logger to the kit's default slog logger, so worker
// internals log through the same pipeline as everything else.
type slogLogger struct{}

func (slogLogger) Debug(args ...any) { slog.Debug(fmt.Sprint(args...)) }
func (slogLogger) Info(args ...any)  { slog.Info(fmt.Sprint(args...)) }
func (slogLogger) Warn(args ...any)  { slog.Warn(fmt.Sprint(args...)) }
func (slogLogger) Error(args ...any) { slog.Error(fmt.Sprint(args...)) }

// Fatal logs at error level rather than exiting: asynq calls it only on
// unrecoverable Valkey errors, and the chassis owns process lifetime.
func (slogLogger) Fatal(args ...any) {
	slog.LogAttrs(context.Background(), slog.LevelError, fmt.Sprint(args...), slog.Bool("fatal", true))
}
