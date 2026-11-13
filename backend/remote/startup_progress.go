package remote

import "context"

type startupProgressKey struct{}

// WithStartupProgress attaches a request-local, server-push observer. It does
// not change readiness, permissions, VM identity, or runtime configuration.
func WithStartupProgress(ctx context.Context, report func(string)) context.Context {
	return context.WithValue(ctx, startupProgressKey{}, report)
}

func startupProgress(ctx context.Context) func(string) {
	report, _ := ctx.Value(startupProgressKey{}).(func(string))
	return report
}

// ReportStartupPhase reports only harness-owned stages, never command output.
func ReportStartupPhase(ctx context.Context, phase string) {
	if report := startupProgress(ctx); report != nil {
		report(phase)
	}
}
