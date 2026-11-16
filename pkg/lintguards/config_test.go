package lintguards_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enabledLinters returns the names under `linters: enable:` of a golangci file.
func enabledLinters(t *testing.T, file string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(file))
	require.NoError(t, err)

	var out []string
	inEnable := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "enable:":
			inEnable = true
		case inEnable && strings.HasPrefix(trimmed, "- "):
			out = append(out, strings.TrimPrefix(trimmed, "- "))
		case inEnable && trimmed != "" && !strings.HasPrefix(trimmed, "#"):
			return out
		}
	}
	return out
}

// .golangci.strict.yml is a SUPERSET of .golangci.yml: a service that switches to
// it must not lose a linter the base file runs. golangci-lint v2 reads one
// config file and cannot merge two, so the two are kept in step by this test.
func TestStrictConfigCoversTheBase(t *testing.T) {
	base := enabledLinters(t, "../../.golangci.yml")
	strict := enabledLinters(t, "../../.golangci.strict.yml")
	require.NotEmpty(t, base)

	for _, linter := range base {
		assert.Contains(t, strict, linter, "linter %q is in .golangci.yml but not in .golangci.strict.yml", linter)
	}
	assert.Contains(t, strict, "forbidigo")
	assert.Contains(t, strict, "depguard")

	rawBase, err := os.ReadFile("../../.golangci.yml")
	require.NoError(t, err)
	rawStrict, err := os.ReadFile("../../.golangci.strict.yml")
	require.NoError(t, err)
	assert.Contains(t, string(rawStrict), `text: "var-naming: avoid package names that conflict"`)
	assert.Contains(t, string(rawBase), `text: "var-naming: avoid package names that conflict"`,
		"the revive exclusion both files carry")
}
