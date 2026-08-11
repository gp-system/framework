package app

import (
	"context"
	"os"

	"github.com/gp-system/logx"
	"github.com/gp-system/telemetry"
)

// Telemetry wires logx into telemetry.Setup the same way in every engine
// chassis (server, worker, realtime): validate the log config, build the
// console handler when dev mode or LOG_STDOUT calls for one, and gate the
// whole logger fanout at LOG_LEVEL. It has no dependency on Run or Params,
// so a standalone consumer can call it without the rest of the chassis.
func Telemetry(ctx context.Context, tcfg telemetry.Config, lcfg logx.Config) (func(context.Context) error, error) {
	if err := lcfg.Validate(); err != nil {
		return nil, err
	}

	opts := []telemetry.Option{telemetry.WithLogLevel(lcfg.Level)}
	if tcfg.Otel.Dev || lcfg.Stdout {
		opts = append(opts, telemetry.WithLogHandler(
			logx.ConsoleHandler(os.Stdout, lcfg, tcfg.Otel.Dev, tcfg.Otel.ServiceName)))
	}

	return telemetry.Setup(ctx, tcfg, opts...)
}
