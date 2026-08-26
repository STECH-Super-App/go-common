package logger_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/STECH-Super-App/go-common/pkg/logger"
)

// captureStderr swaps os.Stderr for a pipe, runs fn, and returns everything
// written. zap resolves the "stderr" sink to os.Stderr at Build time, so the
// swap must happen before logger.New is called — which is exactly what this
// helper guarantees.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())

	return string(out)
}

// Test (j): a logger built under every APP_ENV value the fleet actually uses
// emits a PARSEABLE JSON line carrying the service field.
//
// This is the regression that stops the log pipeline silently reverting to
// console output: a console line is unparseable by LogQL's `| json`, which
// takes out the level label, the request_id filter and the trace_id→Tempo join
// in one stroke (§3b). No workload anywhere sets APP_ENV=production, which is
// why the old encoder gate never selected JSON in any environment.
func TestNew_EmitsJSONWithServiceFieldForEveryAppEnv(t *testing.T) {
	cases := []struct {
		name   string
		appEnv string
		set    bool
	}{
		{name: "local (compose + k8s dev)", appEnv: "local", set: true},
		{name: "prod (k8s prod overlay)", appEnv: "prod", set: true},
		{name: "production (legacy gate value)", appEnv: "production", set: true},
		{name: "unset", set: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("APP_ENV", tc.appEnv)
			} else {
				// t.Setenv registers the restore; Unsetenv alone would leak.
				t.Setenv("APP_ENV", "")
				require.NoError(t, os.Unsetenv("APP_ENV"))
			}

			out := captureStderr(t, func() {
				l, err := logger.New("info", "order-service")
				require.NoError(t, err)
				l.Info("boot", zap.String("extra", "value"))
				_ = l.Sync()
			})

			require.NotEmpty(t, out, "logger must emit to stderr")

			var line map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &line),
				"log line must be parseable JSON, got: %q", out)

			assert.Equal(t, "order-service", line["service"],
				"every line must carry the service field (Loki stream label ↔ Prometheus job ↔ in-line field)")
			assert.Equal(t, "info", line["level"], "level must be the lowercase string form")
			assert.Equal(t, "boot", line["msg"])
			assert.Contains(t, line, "ts")
			assert.Equal(t, "value", line["extra"])
		})
	}
}

func TestNew_LevelIsHonoured(t *testing.T) {
	out := captureStderr(t, func() {
		l, err := logger.New("warn", "svc")
		require.NoError(t, err)
		l.Info("suppressed")
		l.Warn("emitted")
		_ = l.Sync()
	})

	assert.NotContains(t, out, "suppressed")
	assert.Contains(t, out, "emitted")
}

func TestNew_UnknownLevelFallsBackToInfo(t *testing.T) {
	out := captureStderr(t, func() {
		l, err := logger.New("not-a-level", "svc")
		require.NoError(t, err)
		l.Info("emitted")
		_ = l.Sync()
	})

	assert.Contains(t, out, "emitted")
}

func TestIntoContext_FromContext_RoundTrip(t *testing.T) {
	base, err := logger.New("info", "svc")
	require.NoError(t, err)

	child := base.With(zap.String("request_id", "req-1"))
	ctx := logger.IntoContext(context.Background(), child)

	assert.Same(t, child, logger.FromContext(ctx),
		"FromContext must return the exact logger stored by IntoContext")
}

func TestFromContext_FallsBackToLastBuiltLogger(t *testing.T) {
	// New must run inside the capture: zap binds the "stderr" sink at Build
	// time, so a logger built before the swap writes to the real stderr.
	out := captureStderr(t, func() {
		built, err := logger.New("info", "fallback-service")
		require.NoError(t, err)
		require.NotNil(t, built)

		got := logger.FromContext(context.Background())
		require.NotNil(t, got, "FromContext must never return nil")

		got.Info("no context logger here")
		_ = got.Sync()
	})

	var line map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &line))
	assert.Equal(t, "fallback-service", line["service"],
		"the fallback is the last logger built by New, so lines keep their service identity")
}

func TestIntoContext_NilLoggerIsIgnored(t *testing.T) {
	ctx := logger.IntoContext(context.Background(), nil)
	assert.NotNil(t, logger.FromContext(ctx),
		"a nil logger must not poison the context")
}
