package telemetry

import "log/slog"

func telemetryLogger() *slog.Logger {
	return slog.Default().With("component", "telemetry")
}
