package config

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SL-23: GetEnv's doc said an EMPTY variable yields the default; the code
// returned "" for set-but-empty, so a ConfigMap key rendered empty gave an
// empty broker list instead of the default.
func TestGetEnv_EmptyIsUnset(t *testing.T) {
	t.Setenv("SL23_EMPTY", "")
	assert.Equal(t, "fallback", GetEnv("SL23_EMPTY", "fallback"))
	assert.Equal(t, "", GetEnv("SL23_EMPTY"), "no default given: still the empty string")

	t.Setenv("SL23_SET", "value")
	assert.Equal(t, "value", GetEnv("SL23_SET", "fallback"))

	assert.Equal(t, "fallback", GetEnv("SL23_UNSET_FOR_SURE", "fallback"))
}

// invalidRecorder captures what the typed getters report.
type invalidRecorder struct {
	mu    sync.Mutex
	calls [][2]string
}

func (r *invalidRecorder) handler(key, want string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [2]string{key, want})
}

func recordInvalid(t *testing.T) *invalidRecorder {
	t.Helper()
	r := &invalidRecorder{}
	prev := SetInvalidHandler(r.handler)
	t.Cleanup(func() { SetInvalidHandler(prev) })
	return r
}

// SL-23: the typed getters fell back to the default on any parse error without
// a word, so SOME_TIMEOUT=30 (no unit) or X_ENABLED=yes ran on the default.
func TestTypedGetters_ReportAnUnparseableValueAndUseTheDefault(t *testing.T) {
	rec := recordInvalid(t)

	t.Setenv("SL23_TIMEOUT", "30")
	assert.Equal(t, 5*time.Second, GetEnvDuration("SL23_TIMEOUT", 5*time.Second))

	t.Setenv("SL23_ENABLED", "yes")
	assert.True(t, GetEnvBool("SL23_ENABLED", true))

	t.Setenv("SL23_N", "twelve")
	assert.Equal(t, 7, GetEnvInt("SL23_N", 7))
	assert.EqualValues(t, 8, GetEnvInt64("SL23_N", 8))
	assert.EqualValues(t, 9, GetEnvUint32("SL23_N", 9))
	assert.InDelta(t, 1.5, GetEnvFloat("SL23_N", 1.5), 0)

	require.Len(t, rec.calls, 6)
	assert.Equal(t, [2]string{"SL23_TIMEOUT", "duration"}, rec.calls[0])
	assert.Equal(t, [2]string{"SL23_ENABLED", "bool"}, rec.calls[1])
	assert.Equal(t, [2]string{"SL23_N", "int"}, rec.calls[2])
}

func TestTypedGetters_UnsetAndEmptyAreSilent(t *testing.T) {
	rec := recordInvalid(t)

	t.Setenv("SL23_EMPTY_INT", "")
	assert.Equal(t, 3, GetEnvInt("SL23_EMPTY_INT", 3))
	assert.Equal(t, 4, GetEnvInt("SL23_NEVER_SET", 4))
	assert.True(t, GetEnvBool("SL23_NEVER_SET", true))
	assert.Equal(t, time.Second, GetEnvDuration("SL23_NEVER_SET", time.Second))

	assert.Empty(t, rec.calls, "an absent or empty variable is not an error")
}

func TestTypedGetters_ValidValuesAreSilent(t *testing.T) {
	rec := recordInvalid(t)

	t.Setenv("SL23_OK_N", "42")
	t.Setenv("SL23_OK_D", "1m30s")
	t.Setenv("SL23_OK_B", "true")
	assert.Equal(t, 42, GetEnvInt("SL23_OK_N", 0))
	assert.Equal(t, 90*time.Second, GetEnvDuration("SL23_OK_D", 0))
	assert.True(t, GetEnvBool("SL23_OK_B", false))

	assert.Empty(t, rec.calls)
}

func TestDefaultInvalidHandler_WritesOneJSONLineWithoutTheValue(t *testing.T) {
	var buf bytes.Buffer
	reportTo(&buf, "DB_PASSWORD_TTL", "duration")

	line := strings.TrimSpace(buf.String())
	assert.Contains(t, line, `"level":"warn"`)
	assert.Contains(t, line, `"key":"DB_PASSWORD_TTL"`)
	assert.Contains(t, line, `"want":"duration"`)
	assert.Equal(t, 1, strings.Count(buf.String(), "\n"))
}
