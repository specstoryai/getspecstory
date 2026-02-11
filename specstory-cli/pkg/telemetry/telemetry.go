// Package telemetry provides OpenTelemetry trace and metric initialization for the SpecStory CLI.
// It follows the same idempotent-init pattern as pkg/analytics: first call to Init wins,
// disabled path uses the OTel no-op provider (zero overhead, no nil checks needed).
package telemetry

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	// defaultEndpoint is the OTLP gRPC collector address used when
	// OTEL_EXPORTER_OTLP_ENDPOINT is set but Options.Endpoint is empty.
	defaultEndpoint = "localhost:4317"

	// metricExportInterval is how often metrics are exported to the collector.
	metricExportInterval = 10 * time.Second
)

// Options configures telemetry initialisation.
type Options struct {
	ServiceName string // OTel service.name resource attribute (default "specstory-cli")
	Endpoint    string // OTLP gRPC collector address (default "localhost:4317")
	Enabled     bool   // When false, Init is a no-op and the global no-op provider is used
}

var (
	initOnce       sync.Once
	traceProvider  *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	meter          metric.Meter
	metricsEnabled bool

	// Metric instruments
	sessionsProcessed  metric.Int64Counter
	exchangesProcessed metric.Int64Counter
	messagesProcessed  metric.Int64Counter
	toolsUsed          metric.Int64Counter
	processingDuration metric.Float64Histogram

	// Token usage metrics (common)
	inputTokensTotal  metric.Int64Counter
	outputTokensTotal metric.Int64Counter

	// Token usage metrics (Claude Code specific)
	cacheCreationTokens metric.Int64Counter
	cacheReadTokens     metric.Int64Counter

	// Token usage metrics (Codex CLI specific)
	cachedInputTokens     metric.Int64Counter
	reasoningOutputTokens metric.Int64Counter

	// Common attributes parsed from OTEL_RESOURCE_ATTRIBUTES to include on all metrics
	commonMetricAttrs []attribute.KeyValue
)

// parseResourceAttributes parses OTEL_RESOURCE_ATTRIBUTES env var into attribute.KeyValue slice.
// Format: "key1=value1,key2=value2"
func parseResourceAttributes() []attribute.KeyValue {
	envVal := os.Getenv("OTEL_RESOURCE_ATTRIBUTES")
	if envVal == "" {
		return nil
	}

	var attrs []attribute.KeyValue
	pairs := strings.Split(envVal, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			key := strings.TrimSpace(kv[0])
			value := strings.TrimSpace(kv[1])
			if key != "" {
				attrs = append(attrs, attribute.String(key, value))
			}
		}
	}
	return attrs
}

// parseEndpoint normalises an OTLP endpoint string into a bare host:port suitable
// for otlptracegrpc.WithEndpoint and a flag indicating whether TLS should be disabled.
//
// Accepted formats:
//
//	"localhost:4317"              → host="localhost:4317",  insecure=true
//	"http://localhost:4317"       → host="localhost:4317",  insecure=true
//	"https://collector.example:4317" → host="collector.example:4317", insecure=false
//	""                            → host=defaultEndpoint,  insecure=true
func parseEndpoint(raw string) (host string, insecure bool) {
	if raw == "" {
		return defaultEndpoint, true
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Not a URL — treat the whole string as host:port (bare address).
		return raw, true
	}

	// Parsed successfully as a URL with a scheme.
	host = u.Host
	insecure = u.Scheme != "https"
	return host, insecure
}

// Init configures the OTel tracing and metrics subsystem. Thread-safe and idempotent — only the
// first call takes effect. When Enabled is false the global no-op provider remains
// active, so callers can emit spans/events and record metrics safely with zero overhead.
func Init(ctx context.Context, opts Options) error {
	var initErr error
	initOnce.Do(func() {
		if !opts.Enabled {
			telemetryLogger().Info("Telemetry disabled, using no-op provider")
			return
		}

		serviceName := opts.ServiceName
		if serviceName == "" {
			serviceName = "specstory-cli"
		}

		// Parse the endpoint to extract a bare host:port for WithEndpoint and
		// determine TLS mode from the scheme. OTEL_EXPORTER_OTLP_ENDPOINT is
		// conventionally a full URL (e.g. "http://localhost:4317"), but
		// WithEndpoint expects just "host:port".
		host, insecure := parseEndpoint(opts.Endpoint)

		// Create shared resource for both traces and metrics.
		// Use resource.New() with detectors to pick up OTEL_RESOURCE_ATTRIBUTES env var.
		res, err := resource.New(ctx,
			resource.WithFromEnv(),      // OTEL_RESOURCE_ATTRIBUTES and OTEL_SERVICE_NAME
			resource.WithTelemetrySDK(), // telemetry.sdk.* attributes
			resource.WithHost(),         // host.* attributes
			resource.WithAttributes(attribute.String("service.name", serviceName)), // explicit service name (can override env)
		)
		if err != nil {
			initErr = fmt.Errorf("create OTel resource: %w", err)
			return
		}

		// Log the resource attributes for debugging
		telemetryLogger().Debug("Resource attributes created",
			"attributes", res.Attributes(),
			"OTEL_RESOURCE_ATTRIBUTES", os.Getenv("OTEL_RESOURCE_ATTRIBUTES"),
		)

		// Initialize tracing
		if err := initTracing(ctx, host, insecure, res); err != nil {
			initErr = err
			return
		}

		// Initialize metrics
		if err := initMetrics(ctx, host, insecure, res); err != nil {
			initErr = err
			return
		}

		metricsEnabled = true

		// Parse OTEL_RESOURCE_ATTRIBUTES and store as common metric attributes.
		// This ensures resource attributes appear as metric tags in backends like Datadog
		// that may not automatically promote resource-level attributes to metric tags.
		commonMetricAttrs = parseResourceAttributes()
		if len(commonMetricAttrs) > 0 {
			telemetryLogger().Debug("Parsed OTEL_RESOURCE_ATTRIBUTES for metrics",
				"count", len(commonMetricAttrs),
				"attributes", commonMetricAttrs,
			)
		}

		telemetryLogger().Info("Telemetry initialised",
			"endpoint", host,
			"insecure", insecure,
			"serviceName", serviceName,
		)
	})

	return initErr
}

// initTracing sets up the trace provider and exporter.
func initTracing(ctx context.Context, host string, insecure bool, res *resource.Resource) error {
	exporterOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(host),
	}
	if insecure {
		exporterOpts = append(exporterOpts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, exporterOpts...)
	if err != nil {
		return fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	traceProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(traceProvider)

	return nil
}

// initMetrics sets up the meter provider, exporter, and metric instruments.
func initMetrics(ctx context.Context, host string, insecure bool, res *resource.Resource) error {
	exporterOpts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(host),
	}
	if insecure {
		exporterOpts = append(exporterOpts, otlpmetricgrpc.WithInsecure())
	}

	exporter, err := otlpmetricgrpc.New(ctx, exporterOpts...)
	if err != nil {
		return fmt.Errorf("create OTLP metric exporter: %w", err)
	}

	meterProvider = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(metricExportInterval),
		)),
	)
	otel.SetMeterProvider(meterProvider)

	meter = meterProvider.Meter("specstory-cli")

	// Initialize metric instruments
	if err := initMetricInstruments(); err != nil {
		return fmt.Errorf("create metric instruments: %w", err)
	}

	return nil
}

// initMetricInstruments creates all the metric instruments used by the CLI.
func initMetricInstruments() error {
	var err error

	sessionsProcessed, err = meter.Int64Counter("specstory.sessions.processed",
		metric.WithDescription("Number of sessions processed"),
		metric.WithUnit("{session}"),
	)
	if err != nil {
		return err
	}

	exchangesProcessed, err = meter.Int64Counter("specstory.exchanges.processed",
		metric.WithDescription("Number of exchanges processed across all sessions"),
		metric.WithUnit("{exchange}"),
	)
	if err != nil {
		return err
	}

	messagesProcessed, err = meter.Int64Counter("specstory.messages.processed",
		metric.WithDescription("Number of messages processed across all sessions"),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return err
	}

	toolsUsed, err = meter.Int64Counter("specstory.tools.used",
		metric.WithDescription("Number of tool invocations across all sessions"),
		metric.WithUnit("{invocation}"),
	)
	if err != nil {
		return err
	}

	processingDuration, err = meter.Float64Histogram("specstory.session.duration",
		metric.WithDescription("Session processing duration"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	// Token usage metrics
	inputTokensTotal, err = meter.Int64Counter("specstory.tokens.input",
		metric.WithDescription("Total input tokens consumed across all sessions"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return err
	}

	outputTokensTotal, err = meter.Int64Counter("specstory.tokens.output",
		metric.WithDescription("Total output tokens generated across all sessions"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return err
	}

	cacheCreationTokens, err = meter.Int64Counter("specstory.tokens.cache_creation",
		metric.WithDescription("Total tokens written to cache across all sessions"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return err
	}

	cacheReadTokens, err = meter.Int64Counter("specstory.tokens.cache_read",
		metric.WithDescription("Total tokens read from cache across all sessions (Claude)"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return err
	}

	// Codex CLI specific counters
	cachedInputTokens, err = meter.Int64Counter("specstory.tokens.cached_input",
		metric.WithDescription("Total cached input tokens across all sessions (Codex)"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return err
	}

	reasoningOutputTokens, err = meter.Int64Counter("specstory.tokens.reasoning_output",
		metric.WithDescription("Total reasoning output tokens across all sessions (Codex)"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return err
	}

	return nil
}

// Shutdown flushes pending spans/metrics and shuts down both providers.
// Safe to call even when Init was never called or telemetry is disabled.
// For short-lived CLIs, this performs explicit ForceFlush calls before shutdown
// to ensure all data is exported before the process exits.
func Shutdown(ctx context.Context) error {
	var errs []error

	if traceProvider != nil {
		telemetryLogger().Info("Flushing and shutting down trace provider")
		// ForceFlush ensures all pending spans are exported before shutdown.
		// This is critical for short-lived CLIs that may exit before the
		// periodic exporter has time to run.
		if err := traceProvider.ForceFlush(ctx); err != nil {
			telemetryLogger().Warn("Failed to flush trace provider", "error", err)
		}
		if err := traceProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown trace provider: %w", err))
		}
	}

	if meterProvider != nil {
		telemetryLogger().Info("Flushing and shutting down meter provider")
		// ForceFlush ensures all pending metrics are exported before shutdown.
		// The periodic reader has a 10-second interval, so without this,
		// metrics from short CLI runs would never be sent.
		if err := meterProvider.ForceFlush(ctx); err != nil {
			telemetryLogger().Warn("Failed to flush meter provider", "error", err)
		}
		if err := meterProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown meter provider: %w", err))
		}
	}

	if len(errs) > 0 {
		return errs[0] // Return first error
	}
	return nil
}

// ForceFlush explicitly flushes all pending spans and metrics.
// Call this at the end of short-lived commands to ensure data is exported
// before the command returns. Safe to call when telemetry is disabled.
func ForceFlush(ctx context.Context) error {
	var errs []error

	if traceProvider != nil {
		telemetryLogger().Debug("Force flushing trace provider")
		if err := traceProvider.ForceFlush(ctx); err != nil {
			telemetryLogger().Warn("Failed to force flush trace provider", "error", err)
			errs = append(errs, err)
		}
	}

	if meterProvider != nil {
		telemetryLogger().Debug("Force flushing meter provider")
		if err := meterProvider.ForceFlush(ctx); err != nil {
			telemetryLogger().Warn("Failed to force flush meter provider", "error", err)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// Tracer returns a named tracer from the global provider. When telemetry is
// disabled the returned tracer is a no-op (standard OTel behaviour).
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// Meter returns the meter instance. When telemetry is disabled, returns the
// global no-op meter.
func Meter() metric.Meter {
	if meter != nil {
		return meter
	}
	return otel.Meter("specstory-cli")
}

// ContextWithSessionTrace returns a context that will cause any spans started
// from it to share the same trace ID (derived deterministically from sessionID).
// This groups all spans for a given session into a single trace, even across
// multiple invocations (e.g., in autosave mode where processSingleSession is
// called repeatedly as the session file grows).
func ContextWithSessionTrace(ctx context.Context, sessionID string) context.Context {
	traceID := traceIDFromSessionID(sessionID)

	// Create a SpanContext with the deterministic trace ID but no span ID.
	// When we start a span with this as parent, OTel will generate a new span ID
	// but inherit the trace ID.
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		TraceFlags: trace.FlagsSampled, // Ensure it's sampled
	})

	// Inject the SpanContext as a remote parent so child spans inherit the trace ID
	return trace.ContextWithRemoteSpanContext(ctx, sc)
}

// traceIDFromSessionID generates a deterministic 16-byte trace ID from a session ID
// by hashing the session ID with SHA-256 and taking the first 16 bytes.
func traceIDFromSessionID(sessionID string) trace.TraceID {
	hash := sha256.Sum256([]byte(sessionID))
	var traceID trace.TraceID
	copy(traceID[:], hash[:16])
	return traceID
}

// --- Metric Recording Functions ---
// These are internal functions called by RecordSessionMetrics in session_helpers.go.
// The metricsEnabled check is done once in RecordSessionMetrics.

// buildMetricAttrs combines specific metric attributes with common attributes from OTEL_RESOURCE_ATTRIBUTES.
func buildMetricAttrs(specific ...attribute.KeyValue) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(specific)+len(commonMetricAttrs))
	attrs = append(attrs, specific...)
	attrs = append(attrs, commonMetricAttrs...)
	return attrs
}

// recordSessionProcessed increments the sessions processed counter.
func recordSessionProcessed(ctx context.Context, agent string, sessionID string) {
	attrs := buildMetricAttrs(
		attribute.String("specstory.agent", agent),
		attribute.String("specstory.session.id", sessionID),
	)
	sessionsProcessed.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// recordExchanges increments the exchanges counter by the given count.
func recordExchanges(ctx context.Context, agent string, sessionID string, count int64) {
	attrs := buildMetricAttrs(
		attribute.String("specstory.agent", agent),
		attribute.String("specstory.session.id", sessionID),
	)
	exchangesProcessed.Add(ctx, count, metric.WithAttributes(attrs...))
}

// recordMessages increments the messages counter by the given count.
func recordMessages(ctx context.Context, agent string, sessionID string, count int64) {
	attrs := buildMetricAttrs(
		attribute.String("specstory.agent", agent),
		attribute.String("specstory.session.id", sessionID),
	)
	messagesProcessed.Add(ctx, count, metric.WithAttributes(attrs...))
}

// recordToolUsage increments the tool usage counter by the given count.
func recordToolUsage(ctx context.Context, agent string, sessionID string, count int64) {
	attrs := buildMetricAttrs(
		attribute.String("specstory.agent", agent),
		attribute.String("specstory.session.id", sessionID),
	)
	toolsUsed.Add(ctx, count, metric.WithAttributes(attrs...))
}

// recordProcessingDuration records the session processing duration.
func recordProcessingDuration(ctx context.Context, agent string, sessionID string, duration time.Duration) {
	attrs := buildMetricAttrs(
		attribute.String("specstory.agent", agent),
		attribute.String("specstory.session.id", sessionID),
	)
	processingDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
}

// TokenUsage holds aggregated token counts for telemetry recording.
// Different providers track different token types:
//   - Claude Code: InputTokens, OutputTokens, CacheCreationInputTokens, CacheReadInputTokens
//   - Codex CLI: InputTokens, OutputTokens, CachedInputTokens, ReasoningOutputTokens
type TokenUsage struct {
	// Common fields (all providers)
	InputTokens  int
	OutputTokens int

	// Claude Code specific
	CacheCreationInputTokens int
	CacheReadInputTokens     int

	// Codex CLI specific
	CachedInputTokens     int
	ReasoningOutputTokens int
}

// recordTokenUsage records token usage metrics for a session.
// Only non-zero values are recorded.
func recordTokenUsage(ctx context.Context, agent string, sessionID string, usage TokenUsage) {
	attrs := buildMetricAttrs(
		attribute.String("specstory.agent", agent),
		attribute.String("specstory.session.id", sessionID),
	)

	// Common metrics
	if usage.InputTokens > 0 {
		inputTokensTotal.Add(ctx, int64(usage.InputTokens), metric.WithAttributes(attrs...))
	}
	if usage.OutputTokens > 0 {
		outputTokensTotal.Add(ctx, int64(usage.OutputTokens), metric.WithAttributes(attrs...))
	}

	// Claude Code specific
	if usage.CacheCreationInputTokens > 0 {
		cacheCreationTokens.Add(ctx, int64(usage.CacheCreationInputTokens), metric.WithAttributes(attrs...))
	}
	if usage.CacheReadInputTokens > 0 {
		cacheReadTokens.Add(ctx, int64(usage.CacheReadInputTokens), metric.WithAttributes(attrs...))
	}

	// Codex CLI specific
	if usage.CachedInputTokens > 0 {
		cachedInputTokens.Add(ctx, int64(usage.CachedInputTokens), metric.WithAttributes(attrs...))
	}
	if usage.ReasoningOutputTokens > 0 {
		reasoningOutputTokens.Add(ctx, int64(usage.ReasoningOutputTokens), metric.WithAttributes(attrs...))
	}
}
