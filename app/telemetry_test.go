package app

import (
	"context"
	"testing"

	"github.com/gp-system/logx"
	"github.com/gp-system/telemetry"
	"github.com/gp-system/telemetry/otelx"
)

func TestTelemetryRejectsUnknownLogFormat(t *testing.T) {
	tcfg := telemetry.Config{Otel: otelx.Config{Dev: true}}
	lcfg := logx.Config{Format: "fancy"}
	if _, err := Telemetry(context.Background(), tcfg, lcfg); err == nil {
		t.Fatal("want error for unknown LOG_FORMAT")
	}
}

func TestTelemetryDevModeSucceeds(t *testing.T) {
	tcfg := telemetry.Config{Otel: otelx.Config{Dev: true}}
	shutdown, err := Telemetry(context.Background(), tcfg, logx.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}
