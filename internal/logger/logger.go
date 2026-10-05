package logger

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"

	"ky27/backend/internal/config"
)

func Init(cfg config.Config) {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}

	var baseHandler slog.Handler
	if cfg.IsDev() {
		opts.Level = slog.LevelDebug
		baseHandler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		baseHandler = slog.NewJSONHandler(os.Stdout, opts)
	}

	handler := &traceHandler{Handler: baseHandler}

	logger := slog.New(handler).With(
		"service", "ky27-payment",
		"env", cfg.Env,
	)

	slog.SetDefault(logger)
}

func InitTest() {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))
}

// traceHandler injects trace_id and span_id from OTel context.
type traceHandler struct {
	slog.Handler
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", spanCtx.TraceID().String()),
			slog.String("span_id", spanCtx.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}
