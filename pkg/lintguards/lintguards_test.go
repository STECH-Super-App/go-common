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
	write(t, root, "ok.go", "package x\n") // a scan of no file at all is an error
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
	for _, rule := range []string{
		"Idempotency-Key header literal", "hand-rolled idempotency table",
		"hand-rolled deduplicator", "hand-rolled outbox access",
		"in-memory deduplicator in production code",
	} {
		assert.Contains(t, joined, rule)
	}
	// Each of the two table spellings and both header accessors, not just one.
	assert.GreaterOrEqual(t, strings.Count(joined, "hand-rolled idempotency table"), 2)
	assert.GreaterOrEqual(t, strings.Count(joined, "Idempotency-Key header literal"), 2)
}

// The two opt-in rules are not in DefaultRules (a service adds them when its
// migration is done), and they fire when asked for.
func TestScan_OptInRulesFireOnTheSampleAndStayOutOfTheDefaults(t *testing.T) {
	for _, r := range lintguards.DefaultRules() {
		assert.NotEqual(t, lintguards.LegacyEventHandlers().Name, r.Name)
		assert.NotEqual(t, lintguards.NoPoolIn("x").Name, r.Name)
	}

	vs, err := lintguards.Scan("testdata/violations",
		[]lintguards.Rule{lintguards.LegacyEventHandlers(), lintguards.NoPoolIn("viol")}, nil)
	require.NoError(t, err)
	var names []string
	for _, v := range vs {
		names = append(names, v.Rule)
	}
	assert.Contains(t, names, "legacy event handler form")
	assert.Contains(t, names, "pool in an event-handler package")

	// OnlyUnder: the same rule scoped to another directory finds nothing.
	vs, err = lintguards.Scan("testdata/violations", []lintguards.Rule{lintguards.NoPoolIn("internal/kafka/")}, nil)
	require.NoError(t, err)
	assert.Empty(t, vs)
}

// HandleTx and HandlePrepared are the two non-legacy handler forms: the opt-in
// LegacyEventHandlers rule bans events.Handle and nothing else, and does not
// read the longer name HandlePrepared as a spelling of it.
func TestScan_LegacyEventHandlersAcceptsHandleTxAndHandlePrepared(t *testing.T) {
	root := t.TempDir()
	write(t, root, "internal/handlers.go", `package internal

import (
	"context"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

func register(d *events.Dispatcher) {
	events.HandleTx(d, func(context.Context, events.Tx, *usersv1.UserRegistered) error { return nil })
	events.HandlePrepared(d,
		func(context.Context, *usersv1.UserRegistered) (struct{}, error) { return struct{}{}, nil },
		func(context.Context, events.Tx, *usersv1.UserRegistered, struct{}) error { return nil })
}
`)
	vs, err := lintguards.Scan(root, []lintguards.Rule{lintguards.LegacyEventHandlers()}, nil)
	require.NoError(t, err)
	assert.Empty(t, vs)

	write(t, root, "internal/legacy.go", `package internal

import (
	"context"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	"github.com/STECH-Super-App/go-common/pkg/events"
)

func legacy(d *events.Dispatcher) {
	events.Handle(d, func(context.Context, *usersv1.UserRegistered) error { return nil })
}
`)
	vs, err = lintguards.Scan(root, []lintguards.Rule{lintguards.LegacyEventHandlers()}, nil)
	require.NoError(t, err)
	require.Len(t, vs, 1, "the control: the old form is still flagged")
	assert.Equal(t, "internal/legacy.go", strings.SplitN(vs[0].String(), ":", 2)[0])
}

func TestScan_ReleaseTwoRulesAreQuietOnLegitimateCode(t *testing.T) {
	root := t.TempDir()
	write(t, root, "internal/ok.go", `package internal

import (
	"github.com/STECH-Super-App/go-common/pkg/events"
	"github.com/STECH-Super-App/go-common/pkg/idempotency"
)

// events.NewPostgresDeduplicator and idempotency.Post are the way.
var _ = events.NewPostgresDeduplicator
var _ = idempotency.Post

func use() {
	_ = "SELECT id FROM orders"
	_ = "Content-Type"
	_ = "processed" // not a dedup table
}
`)
	assert.Empty(t, scan(t, root, nil))
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

// R12: a guard that scans nothing is green for ever. A root with no non-test .go
// file — the test moved, a wrong relative path — fails loudly.
func TestScan_ARootWithNoGoFilesIsAnError(t *testing.T) {
	root := t.TempDir()
	write(t, root, "README.md", "# not code")
	write(t, root, "a_test.go", "package x\n")
	write(t, root, "vendor/v.go", "package v\n")
	write(t, root, "testdata/t.go", "package t\n")

	_, err := lintguards.Scan(root, lintguards.DefaultRules(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no non-test .go file")

	// Files that are all exempt were still FOUND: the root is right.
	write(t, root, "internal/legacy.go", "package internal\n")
	_, err = lintguards.Scan(root, lintguards.DefaultRules(), map[string]string{"internal/legacy.go": "reason"})
	assert.NoError(t, err)
}

// R12: SQL is matched as whole identifiers in statement position. The old
// substring rules hit orders.idempotency_key, chat's client_idempotency_key and
// conversation_processed_events.
func TestScan_SQLRulesIgnoreColumnsAndLongerNames(t *testing.T) {
	root := t.TempDir()
	write(t, root, "internal/ok.go", "package internal\n\n"+
		"var queries = []string{\n"+
		"\t`SELECT id FROM orders WHERE idempotency_key = $1`,\n"+
		"\t`CREATE UNIQUE INDEX orders_create_idem ON orders (customer_id, idempotency_key)`,\n"+
		"\t`INSERT INTO orders (id, idempotency_key) VALUES ($1, $2)`,\n"+
		"\t`UPDATE orders SET idempotency_key = NULL WHERE id = $1`,\n"+
		"\t`SELECT m.id FROM conversation_messages m WHERE m.client_idempotency_key = $1`,\n"+
		"\t`INSERT INTO conversation_processed_events(event_id) VALUES($1) ON CONFLICT DO NOTHING`,\n"+
		"\t`DELETE FROM conversation_processed_events WHERE event_id IN (SELECT event_id FROM conversation_processed_events)`,\n"+
		"\t`SELECT 1 FROM outbox_messages_archive`,\n"+
		"\t`SELECT 1 FROM information_schema.tables WHERE table_name = 'processed_events'`,\n"+
		"\t\"failed to read outbox_messages: processed_events missing\",\n"+
		"\t`SELECT 1 FROM pending_processed_events`,\n"+
		"}\n")

	assert.Empty(t, scan(t, root, nil))
}

func TestScan_SQLRulesStillCatchAHandRolledTableInAnySpelling(t *testing.T) {
	for name, sql := range map[string]string{
		"select":              "SELECT 1 FROM idempotency_keys WHERE key = $1",
		"insert":              "INSERT INTO idempotency_keys (key) VALUES ($1)",
		"the library's table": "SELECT 1 FROM common_idempotency_keys",
		"another name":        "INSERT INTO idempotency_records (key) VALUES ($1)",
		"create":              "CREATE TABLE IF NOT EXISTS idempotency_keys (key text)",
		"delete":              "DELETE FROM idempotency_keys WHERE created_at < now()",
		"update":              "UPDATE idempotency_keys SET state = 'done'",
		"join":                "SELECT 1 FROM orders o JOIN idempotency_keys k ON k.key = o.idempotency_key",
		"lower case":          "select 1 from idempotency_keys",
		"schema and quotes":   `SELECT 1 FROM public."idempotency_keys"`,
		"multi-line raw":      "SELECT 1\n\tFROM\n\t\tidempotency_keys\n\tWHERE key = $1",
		"truncate":            "TRUNCATE idempotency_keys",
		"dedup":               "INSERT INTO processed_events (consumer, event_id) VALUES ($1, $2)",
		"old dedup":           "SELECT 1 FROM processed_outbox_messages",
		"outbox":              "UPDATE outbox_messages SET status = 'sent'",
		"outbox join":         "SELECT 1 FROM a JOIN outbox_messages o ON o.id = a.id",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "internal/q.go", "package internal\n\nvar q = `"+sql+"`\n")
			got := scan(t, root, nil)
			require.Len(t, got, 1, sql)
		})
	}
}

// The documented exemption: a service with its own old table lists the file that
// still uses it, with the reason, until it drops the table.
func TestScan_AnOldIdempotencyTableIsExemptByFileWithAReason(t *testing.T) {
	root := t.TempDir()
	write(t, root, "internal/idem/store.go", "package idem\n\nvar q = `DELETE FROM idempotency_keys WHERE expires_at < now()`\n")

	assert.Len(t, scan(t, root, nil), 1)
	assert.Empty(t, scan(t, root, map[string]string{
		"internal/idem/": "the old table, dropped in migration 00042 once its endpoints use pkg/idempotency",
	}))
}

// R12: the header literal is banned as a header ACCESS key, not as a word. The
// api-gateway names it in its CORS allow-list and notification-service in an
// error's field name.
func TestScan_IdempotencyKeyHeaderIsBannedAsAnAccessKeyOnly(t *testing.T) {
	ok := t.TempDir()
	write(t, ok, "config/cors.go", `package config

import "strings"

var allowHeaders = []string{"Content-Type", "Authorization", "Idempotency-Key", "Range"}

var defaultHeaders = strings.Split("Content-Type,Authorization,Idempotency-Key", ",")

type field struct{ Name string }

func f() field { return field{Name: "Idempotency-Key"} }

func g(h func(string, any), v any) { h("Idempotency-Key", v) }
`)
	assert.Empty(t, scan(t, ok, nil), "a CORS list, a config default and a field name are not reads")

	for name, access := range map[string]string{
		"Get":        `r.Header.Get("Idempotency-Key")`,
		"lower case": `r.Header.Get("idempotency-key")`,
		"chained":    `c.Request().Header.Get("Idempotency-Key")`,
		"Values":     `r.Header.Values("Idempotency-Key")`,
		"Set":        `h.Set("Idempotency-Key", "x")`,
		"index":      `r.Header["Idempotency-Key"]`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "a.go", "package a\n\nimport \"net/http\"\n\nvar r = &http.Request{}\nvar h = http.Header{}\n\nfunc f(c interface{ Request() *http.Request }) {\n\t_ = "+access+"\n}\n")
			assert.Equal(t, []string{"a.go:9: Idempotency-Key header literal"}, scan(t, root, nil))
		})
	}
}

// The lookalikes at the end of the sample are legitimate: a rule that flags one
// has turned back into a substring match.
func TestScan_TheSampleLookalikesAreNotReported(t *testing.T) {
	raw, err := os.ReadFile("testdata/violations/violations.go")
	require.NoError(t, err)
	first := 0
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "func lookalikes") {
			first = i + 1
		}
	}
	require.NotZero(t, first, "the sample must hold the lookalikes")

	vs, err := lintguards.Scan("testdata/violations", lintguards.DefaultRules(), nil)
	require.NoError(t, err)
	for _, v := range vs {
		assert.Less(t, v.Line, first, "a legitimate lookalike was reported: %s", v)
	}
}
