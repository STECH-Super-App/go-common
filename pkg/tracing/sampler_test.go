package tracing

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unsetSampler clears both sampler variables with t.Setenv's automatic restore.
func unsetSampler(t *testing.T) {
	t.Helper()
	for _, key := range []string{envSampler, envSamplerArg} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
}

// With OTEL_TRACES_SAMPLER SET, Init must install no sampler of its own so the
// SDK's documented env handling wins. This is the knob operators actually
// reach for at launch, and a sampler passed as an option would silently
// override it.
func TestDefaultSampler_EnvSamplerSetDefersToSDK(t *testing.T) {
	for _, value := range []string{"always_off", "always_on", "traceidratio", "parentbased_traceidratio"} {
		t.Run(value, func(t *testing.T) {
			unsetSampler(t)
			t.Setenv(envSampler, value)

			sampler, ok := defaultSampler()
			assert.False(t, ok, "OTEL_TRACES_SAMPLER=%s must be left to the SDK", value)
			assert.Nil(t, sampler)
		})
	}
}

// With OTEL_TRACES_SAMPLER unset we install the documented default
// (parentbased_traceidratio) so that OTEL_TRACES_SAMPLER_ARG is honoured on its
// own — the SDK's fallback ignores the ARG entirely unless the sampler
// variable is also set, which would silently give an operator who set only the
// ratio full sampling.
func TestDefaultSampler_UnsetInstallsParentBasedRatio(t *testing.T) {
	cases := []struct {
		name      string
		arg       string
		setArg    bool
		wantRatio string
	}{
		{name: "no arg means keep every trace", setArg: false, wantRatio: "1"},
		{name: "explicit ratio is honoured", arg: "0.25", setArg: true, wantRatio: "0.25"},
		{name: "unparseable ratio falls back to full", arg: "abc", setArg: true, wantRatio: "1"},
		{name: "negative ratio falls back to full", arg: "-0.5", setArg: true, wantRatio: "1"},
		{name: "greater than one falls back to full", arg: "2", setArg: true, wantRatio: "1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetSampler(t)
			if tc.setArg {
				t.Setenv(envSamplerArg, tc.arg)
			}

			sampler, ok := defaultSampler()
			require.True(t, ok)
			require.NotNil(t, sampler)

			desc := sampler.Description()
			assert.True(t, strings.HasPrefix(desc, "ParentBased{"),
				"the default must be parent-based so the gateway's decision governs the chain, got %q", desc)
			assert.Contains(t, desc, "TraceIDRatioBased{"+tc.wantRatio+"}", "description was %q", desc)
		})
	}
}

func TestSamplerRatio(t *testing.T) {
	unsetSampler(t)
	assert.Equal(t, 1.0, samplerRatio())

	t.Setenv(envSamplerArg, "0.1")
	assert.Equal(t, 0.1, samplerRatio())

	t.Setenv(envSamplerArg, "0")
	assert.Equal(t, 0.0, samplerRatio(), "an explicit zero must be respected, not treated as absent")
}

func TestEndpoint_TracesEndpointOverridesGeneric(t *testing.T) {
	t.Setenv(EnvEndpoint, "http://tempo:4317")
	t.Setenv(EnvTracesEndpoint, "http://tempo-traces:4317")
	assert.Equal(t, "http://tempo-traces:4317", endpoint())

	t.Setenv(EnvTracesEndpoint, "")
	assert.Equal(t, "http://tempo:4317", endpoint())
}
