package lintguards_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/STECH-Super-App/go-common/pkg/clientip"
	"github.com/STECH-Super-App/go-common/pkg/lintguards"
	"github.com/STECH-Super-App/go-common/pkg/trustedheaders"
)

func write(t *testing.T, root, rel, src string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
	require.NoError(t, os.WriteFile(p, []byte(src), 0o600))
}

func scan(t *testing.T, root string, exempt map[string]string) []string {
	t.Helper()
	vs, err := lintguards.Scan(root, lintguards.DefaultRules(), exempt)
	require.NoError(t, err)
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.String()
	}
	return out
}

func TestScan_FindsEachBannedConstruct(t *testing.T) {
	root := t.TempDir()
	write(t, root, "cmd/main.go", `package main

import (
	"net/http"

	kf "github.com/segmentio/kafka-go"
)

func main() {
	_ = &kf.Writer{Addr: kf.TCP("x:1")}
	_ = http.Client{}
	_ = &http.Client{}
	_ = "x-forwarded-for"
	_ = "X-Real-IP"
}
`)

	got := scan(t, root, nil)

	assert.Equal(t, []string{
		"cmd/main.go:10: kafka.Writer literal",
		"cmd/main.go:11: http.Client literal",
		"cmd/main.go:12: http.Client literal",
		"cmd/main.go:13: client address header",
		"cmd/main.go:14: client address header",
	}, got)
}

func TestScan_LeavesLegitimateUsesAlone(t *testing.T) {
	root := t.TempDir()
	write(t, root, "internal/ok.go", `package internal

import (
	"net/http"

	"github.com/segmentio/kafka-go"
)

type holder struct {
	w *kafka.Writer // a variable of the type is fine: only a literal is banned
	c *http.Client
}

func use(h holder, kafka struct{ Writer struct{} }) {
	_ = kafka.Writer // a local named kafka is not the import
	_ = "X-Client-IP"
	_ = "Content-Type"
}
`)

	assert.Empty(t, scan(t, root, nil))
}

func TestScan_SkipsTestsTestdataVendorAndHiddenDirs(t *testing.T) {
	const bad = `package x
import "net/http"
var _ = http.Client{}
`
	root := t.TempDir()
	write(t, root, "a_test.go", bad)
	write(t, root, "testdata/a.go", bad)
	write(t, root, "vendor/a.go", bad)
	write(t, root, ".claude/a.go", bad)

	assert.Empty(t, scan(t, root, nil))
}

func TestScan_ExemptFilesAndDirectories(t *testing.T) {
	const bad = `package x
import "net/http"
var _ = http.Client{}
`
	root := t.TempDir()
	write(t, root, "internal/legacy/a.go", bad)
	write(t, root, "internal/legacy/deep/b.go", bad)
	write(t, root, "internal/other.go", bad)

	got := scan(t, root, map[string]string{
		"internal/legacy/": "third-party proxy, moves to egress in #123",
	})
	assert.Equal(t, []string{"internal/other.go:3: http.Client literal"}, got)

	got = scan(t, root, map[string]string{"internal/other.go": "reason"})
	assert.Len(t, got, 2)
}

func TestScan_AnExemptionWithoutAReasonIsRefused(t *testing.T) {
	_, err := lintguards.Scan(t.TempDir(), lintguards.DefaultRules(), map[string]string{"a.go": "  "})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reason")
}

func TestScan_ReportsUnparseableSource(t *testing.T) {
	root := t.TempDir()
	write(t, root, "broken.go", "package x\nfunc (")

	_, err := lintguards.Scan(root, lintguards.DefaultRules(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "broken.go")
}

// The shipped violations sample must trip every AST rule, so a rule that stops
// matching shows up here and not in a service.
func TestScan_TheViolationsSampleTripsEveryRule(t *testing.T) {
	got := scan(t, "testdata/violations", nil)

	joined := strings.Join(got, "\n")
	assert.Contains(t, joined, "kafka.Writer literal")
	assert.Contains(t, joined, "http.Client literal")
	assert.Contains(t, joined, "client address header")
}

// The AST rule bans naming each client-address header; the list lives here
// because lintguards must not need a service's imports to run, so a test keeps
// it equal to the one the gateway strips.
func TestClientAddressRuleCoversTrustedHeaders(t *testing.T) {
	banned := map[string]bool{}
	for _, s := range lintguards.ClientAddressHeaderLiterals().Strings {
		banned[http.CanonicalHeaderKey(s)] = true
	}
	for _, name := range trustedheaders.ClientAddress() {
		if name == http.CanonicalHeaderKey(clientip.Header) {
			assert.False(t, banned[name], "X-Client-IP is the header services read: naming it is not a violation")
			continue
		}
		assert.True(t, banned[name], "%s is a client-address header the gateway strips but lintguards does not ban", name)
	}
}

func TestScan_FlagsEveryForwardingHeaderSpelling(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", `package a

func f(h interface{ Get(string) string }) {
	_ = h.Get("x-forwarded-port")
	_ = h.Get("X-Forwarded-Prefix")
	_ = h.Get("X-Client-IP")
}
`)
	assert.Equal(t, []string{"a.go:4: client address header", "a.go:5: client address header"}, scan(t, root, nil))
}
