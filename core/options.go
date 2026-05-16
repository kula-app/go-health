package core

import "log/slog"

// Option configures an Engine at construction time. Apply options by
// passing them to NewEngine. Options compose. Each option receives the
// Engine being built and may mutate any field it cares about.
type Option func(*Engine)

// WithLogger overrides the default slog.Default() logger used by the
// Engine for emitting warn-level and error-level messages when an
// aggregated response carries StatusWarn or StatusFail. Pass a logger
// configured with the same handler that the rest of the service uses
// so that health-check events appear alongside other operational logs.
func WithLogger(l *slog.Logger) Option {
	return func(e *Engine) { e.logger = l }
}
