// Package lintguards is the CI-test half of the mechanical guards that keep a
// service from re-implementing something go-common already provides (audit-fix
// campaign spec §3.4). The other half is .golangci.strict.yml.
//
// A linter cannot tell `&kafka.Writer{...}` (a hand-built writer: banned) from
// `var w *kafka.Writer` (a variable that holds one go-common built: fine), nor
// match a string literal such as "X-Forwarded-For". This package can: it parses
// the service's own source and reports
//
//   - composite literals of banned types (kafka.Writer, http.Client),
//   - string literals that name a banned header (X-Forwarded-For, X-Real-IP, …),
//     header keys passed to a header accessor (r.Header.Get("Idempotency-Key")),
//     SQL statements that name a table a go-common package owns (whole
//     identifiers in statement position: FROM, INTO, UPDATE, JOIN, TABLE — not a
//     column or a longer table name that merely contains the word),
//   - uses of a banned package member (events.NewMemoryDeduplicator; with
//     OnlyUnder, pgxpool.Pool inside the handler packages).
//
// One test per service calls it with the service's own exempt list, where every
// line carries a reason:
//
//	func TestGoCommonGuards(t *testing.T) {
//		lintguards.Run(t, "../..", lintguards.DefaultRules(), map[string]string{
//			"internal/infrastructure/legacy/proxy.go": "streams a third-party response; moves to egress in #123",
//		})
//	}
//
// Test files, testdata, vendor and hidden directories are not scanned. A root
// with no non-test .go file at all is an error, not a pass: it is a wrong path.
package lintguards

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Rule is one banned construct.
type Rule struct {
	// Name labels the rule in a failure.
	Name string
	// Message says what to use instead.
	Message string

	// CompositeLiteral, when set, bans `pkg.Type{...}` where pkg is imported
	// from ImportPath (whatever the file calls it).
	CompositeLiteral *TypeRef
	// Strings, when set, bans a string literal equal (case-insensitively) to
	// any of them.
	Strings []string
	// Contains, when set, bans a string literal that contains any of them
	// (case-insensitively). A blunt tool: it also hits a column or a longer name
	// that merely contains the fragment. The table rules use SQLTables instead.
	Contains []string
	// SQLTables, when set, bans an SQL statement that names one of these tables
	// in statement position — directly after FROM, INTO, UPDATE, JOIN, TABLE
	// (also IF [NOT] EXISTS, ONLY, a schema prefix, quotes) or TRUNCATE — as a
	// WHOLE identifier, case-insensitively. An entry ending in "*" is a prefix
	// ("idempotency_*" is any table whose name starts with idempotency_). A
	// column (orders.idempotency_key), a longer table name
	// (conversation_processed_events) and a word inside a message do not match.
	SQLTables []string
	// HeaderKeys, when set, bans these header names (case-insensitively) as the
	// key of a header access: the argument of .Get/.Values/.Peek/.Set/.Add/.Del or
	// the index of a .Header[...] map. The same word as a CORS allow-list entry, a
	// config default or an error's field name is not an access and passes; the
	// way to NAME the header is the constant of the package that owns it
	// (idempotency.Header).
	HeaderKeys []string
	// Selector, when set, bans any use of pkg.Name — a call, a type, a value —
	// where pkg is imported from ImportPath.
	Selector *TypeRef
	// OnlyUnder, when set, restricts the rule to files whose root-relative path
	// starts with one of the prefixes (slash-separated, "internal/kafka/"). Empty
	// means everywhere.
	OnlyUnder []string
}

// TypeRef names a type by import path and name.
type TypeRef struct {
	ImportPath string
	Name       string
}

// KafkaWriterLiterals bans a hand-built kafka.Writer: its zero value is
// RequireNone, so a write "succeeds" before any broker answers (SL-01).
func KafkaWriterLiterals() Rule {
	return Rule{
		Name:             "kafka.Writer literal",
		Message:          "build the writer with go-common/pkg/kafkawriter (NewProducer / NewTopicWriter)",
		CompositeLiteral: &TypeRef{ImportPath: "github.com/segmentio/kafka-go", Name: "Writer"},
	}
}

// HTTPClientLiterals bans a hand-built http.Client outside pkg/egress: it would
// call a provider directly, with no egress proxy and often no timeout.
func HTTPClientLiterals() Rule {
	return Rule{
		Name:             "http.Client literal",
		Message:          "use go-common/pkg/egress (HTTPClient / Transport)",
		CompositeLiteral: &TypeRef{ImportPath: "net/http", Name: "Client"},
	}
}

// ClientAddressHeaderLiterals bans naming the headers a client controls and a
// service might believe as its address. The gateway is the single authority and
// writes X-Client-IP (spec §3.3); services read only that, through
// go-common/pkg/clientip.
//
// The list is the address headers of pkg/trustedheaders minus X-Client-IP itself
// (a service naming that is fine — only the helper should READ it, but a literal
// is not worth a false positive); TestClientAddressRuleCoversTrustedHeaders keeps
// the two in step. A service that must name one for a reason (a proxy that
// strips them, a test double) lists the file in its exempt map with that reason.
func ClientAddressHeaderLiterals() Rule {
	return Rule{
		Name:    "client address header",
		Message: "read the client address from X-Client-IP through the go-common helper, never from forwarding headers",
		Strings: []string{
			"X-Forwarded-For", "X-Real-IP", "Forwarded",
			"X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Forwarded-Prefix",
			"X-Original-Forwarded-For",
		},
	}
}

// IdempotencyKeyHeaderLiterals bans reading the Idempotency-Key header by hand:
// pkg/idempotency validates it (pkg/input), scopes it to the caller and the
// endpoint, and stores it in the work's transaction (spec §3.2). Only a literal
// used as a header ACCESS key is banned (r.Header.Get("Idempotency-Key")); the
// same word in a CORS allow-list, a config default or an error field name is not
// a read. Naming the header is what idempotency.Header is for.
func IdempotencyKeyHeaderLiterals() Rule {
	return Rule{
		Name:       "Idempotency-Key header literal",
		Message:    "use go-common/pkg/idempotency (Post/Add + ClaimFor + Claim.Run): it keeps the key in the work's transaction; to name the header use idempotency.Header",
		HeaderKeys: []string{"Idempotency-Key"},
	}
}

// HandRolledIdempotencyTables bans SQL that names an idempotency table: any
// table called idempotency_<something> (a service's own, whatever its shape) and
// common_idempotency_keys, the one pkg/idempotency owns (migration 006 of
// outbox.Migrate). A service that still has its own idempotency_keys table until
// its endpoints move lists the file that uses it in its exempt map, with the
// reason; it drops the table in a migration of its own afterwards. Columns such
// as orders.idempotency_key or chat's client_idempotency_key are not tables and
// are not matched.
func HandRolledIdempotencyTables() Rule {
	return Rule{
		Name:      "hand-rolled idempotency table",
		Message:   "use go-common/pkg/idempotency; a service keeps no idempotency table of its own (an old one stays exempt, with a reason, until it is dropped)",
		SQLTables: []string{"idempotency_*", "common_idempotency_keys"},
	}
}

// HandRolledDeduplicator bans SQL that names a consumer deduplication table:
// events.NewPostgresDeduplicator owns processed_events, and the dispatcher is
// the only thing that writes it, in the handler's transaction.
func HandRolledDeduplicator() Rule {
	return Rule{
		Name:      "hand-rolled deduplicator",
		Message:   "use events.NewPostgresDeduplicator with events.NewDispatcher and events.HandleTx; a service keeps no dedup table of its own",
		SQLTables: []string{"processed_events", "processed_outbox_messages", "processed_messages", "processed_event_ids"},
	}
}

// HandRolledOutbox bans SQL that names outbox_messages: rows are written by
// outbox.Publisher.PublishProto inside the business transaction and read by the
// relay, never by a service's own SQL.
func HandRolledOutbox() Rule {
	return Rule{
		Name:      "hand-rolled outbox access",
		Message:   "publish with outbox.Publisher.PublishProto (in the business transaction); the relay and reaper own the table",
		SQLTables: []string{"outbox_messages"},
	}
}

// MemoryDeduplicator bans events.NewMemoryDeduplicator in non-test code. The
// in-memory deduplicator forgets on restart and gives handlers no transaction;
// it exists for unit tests (whose files this scan skips).
func MemoryDeduplicator() Rule {
	return Rule{
		Name:     "in-memory deduplicator in production code",
		Message:  "use events.NewPostgresDeduplicator; NewMemoryDeduplicator is for _test.go files",
		Selector: &TypeRef{ImportPath: "github.com/STECH-Super-App/go-common/pkg/events", Name: "NewMemoryDeduplicator"},
	}
}

// LegacyEventHandlers bans events.Handle, the handler form that gets no
// transaction (SL-04). It is NOT in DefaultRules: a service adds it to its guard
// test once it has converted its handlers to events.HandleTx, so the old form
// cannot creep back.
func LegacyEventHandlers() Rule {
	return Rule{
		Name:     "legacy event handler form",
		Message:  "register handlers with events.HandleTx: it hands the handler the deduplicator's transaction, so the effect and the processed mark commit together",
		Selector: &TypeRef{ImportPath: "github.com/STECH-Super-App/go-common/pkg/events", Name: "Handle"},
	}
}

// NoPoolIn bans naming *pgxpool.Pool in the given directories: the packages that
// hold event handlers must write through the transaction the dispatcher hands
// them, never through a pool (that is a second transaction, outside the
// "processed" mark). Not in DefaultRules — the directories are the service's.
//
//	lintguards.NoPoolIn("internal/infrastructure/kafka/")
func NoPoolIn(dirs ...string) Rule {
	return Rule{
		Name:      "pool in an event-handler package",
		Message:   "an event handler writes through the events.Tx it is given, not through a pool",
		Selector:  &TypeRef{ImportPath: "github.com/jackc/pgx/v5/pgxpool", Name: "Pool"},
		OnlyUnder: dirs,
	}
}

// DefaultRules is every rule above that needs no per-service input and no
// finished migration: LegacyEventHandlers and NoPoolIn are opt-in.
func DefaultRules() []Rule {
	return []Rule{
		KafkaWriterLiterals(), HTTPClientLiterals(), ClientAddressHeaderLiterals(),
		IdempotencyKeyHeaderLiterals(), HandRolledIdempotencyTables(),
		HandRolledDeduplicator(), HandRolledOutbox(), MemoryDeduplicator(),
	}
}

// Violation is one banned use.
type Violation struct {
	Rule string
	File string // slash-separated, relative to the scanned root
	Line int
}

func (v Violation) String() string { return fmt.Sprintf("%s:%d: %s", v.File, v.Line, v.Rule) }

// Scan parses every non-test .go file under root and returns the violations of
// rules, minus those in an exempt file. exempt maps a root-relative path — a
// file, or a directory written with a trailing slash — to the REASON it is
// exempt; Scan returns an error for an empty reason, so an exemption cannot be
// silent.
func Scan(root string, rules []Rule, exempt map[string]string) ([]Violation, error) {
	for p, reason := range exempt {
		if strings.TrimSpace(reason) == "" {
			return nil, fmt.Errorf("lintguards: exempt entry %q has no reason", p)
		}
	}

	var out []Violation
	var goFiles int
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		goFiles++
		if isExempt(rel, exempt) {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("lintguards: parse %s: %w", rel, parseErr)
		}
		out = append(out, scanFile(fset, file, rel, applicable(rules, rel))...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if goFiles == 0 {
		// A guard that scans nothing is green for ever: a wrong relative path
		// (the test moved, the service's layout changed) must fail loudly.
		return nil, fmt.Errorf("lintguards: no non-test .go file under %q: the root is wrong, and a scan of nothing proves nothing", root)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// Run is Scan for a test: it fails t with every violation and its rule's
// message.
func Run(t testing.TB, root string, rules []Rule, exempt map[string]string) {
	t.Helper()
	violations, err := Scan(root, rules, exempt)
	if err != nil {
		t.Fatal(err)
	}
	msgs := map[string]string{}
	for _, r := range rules {
		msgs[r.Name] = r.Message
	}
	for _, v := range violations {
		t.Errorf("%s — %s (or add the file to the exempt list with a reason)", v, msgs[v.Rule])
	}
}

// applicable keeps the rules that apply to the file at rel.
func applicable(rules []Rule, rel string) []Rule {
	var out []Rule
	for _, r := range rules {
		if len(r.OnlyUnder) == 0 {
			out = append(out, r)
			continue
		}
		for _, prefix := range r.OnlyUnder {
			if strings.HasPrefix(rel, prefix) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func isExempt(rel string, exempt map[string]string) bool {
	for p := range exempt {
		if rel == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(rel, p)) {
			return true
		}
	}
	return false
}

func scanFile(fset *token.FileSet, file *ast.File, rel string, rules []Rule) []Violation {
	// local import name -> import path, so `kf "github.com/segmentio/kafka-go"`
	// is seen as kafka-go and a variable that happens to be called `kafka` is not.
	imports := map[string]string{}
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(p)
		if p == "github.com/segmentio/kafka-go" {
			name = "kafka"
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = p
	}

	sc := &fileScan{fset: fset, rel: rel, rules: rules, imports: imports}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			sc.compositeLit(n)
		case *ast.CallExpr:
			sc.call(n)
		case *ast.IndexExpr:
			sc.index(n)
		case *ast.SelectorExpr:
			sc.selector(n)
		case *ast.BasicLit:
			sc.basicLit(n)
		}
		return true
	})
	return sc.out
}

// fileScan holds what the node checks of one file share.
type fileScan struct {
	fset    *token.FileSet
	rel     string
	rules   []Rule
	imports map[string]string
	out     []Violation
}

func (s *fileScan) add(rule string, pos token.Pos) {
	s.out = append(s.out, Violation{Rule: rule, File: s.rel, Line: s.fset.Position(pos).Line})
}

// compositeLit: `pkg.Type{...}` of a banned type.
func (s *fileScan) compositeLit(n *ast.CompositeLit) {
	sel, ok := n.Type.(*ast.SelectorExpr)
	if !ok {
		return
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}
	for _, r := range s.rules {
		if r.CompositeLiteral != nil && sel.Sel.Name == r.CompositeLiteral.Name &&
			s.imports[pkg.Name] == r.CompositeLiteral.ImportPath {
			s.add(r.Name, n.Pos())
		}
	}
}

// call: a banned header name as the key of .Get/.Values/.Peek/.Set/.Add/.Del.
func (s *fileScan) call(n *ast.CallExpr) {
	sel, ok := n.Fun.(*ast.SelectorExpr)
	if !ok || !headerAccessors[sel.Sel.Name] {
		return
	}
	for _, arg := range n.Args {
		s.headerKey(arg)
	}
}

// index: a banned header name as the key of a `.Header[...]` map access.
func (s *fileScan) index(n *ast.IndexExpr) {
	if sel, ok := n.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Header" {
		s.headerKey(n.Index)
	}
}

func (s *fileScan) headerKey(e ast.Expr) {
	lit, ok := stringLiteral(e)
	if !ok {
		return
	}
	for _, r := range s.rules {
		if matchesAny(lit, r.HeaderKeys) {
			s.add(r.Name, e.Pos())
		}
	}
}

// selector: a use of a banned package member.
func (s *fileScan) selector(n *ast.SelectorExpr) {
	pkg, ok := n.X.(*ast.Ident)
	if !ok {
		return
	}
	for _, r := range s.rules {
		if r.Selector != nil && n.Sel.Name == r.Selector.Name && s.imports[pkg.Name] == r.Selector.ImportPath {
			s.add(r.Name, n.Pos())
		}
	}
}

// basicLit: a banned string, fragment or SQL table.
func (s *fileScan) basicLit(n *ast.BasicLit) {
	val, ok := stringLiteral(n)
	if !ok {
		return
	}
	lower := strings.ToLower(val)
	for _, r := range s.rules {
		if matchesAny(val, r.Strings) {
			s.add(r.Name, n.Pos())
		}
		for _, frag := range r.Contains {
			if strings.Contains(lower, strings.ToLower(frag)) {
				s.add(r.Name, n.Pos())
				break
			}
		}
		if len(r.SQLTables) > 0 && sqlNamesTable(val, r.SQLTables) {
			s.add(r.Name, n.Pos())
		}
	}
}

// headerAccessors are the method names that read or write a header by key.
var headerAccessors = map[string]bool{"Get": true, "Values": true, "Peek": true, "Set": true, "Add": true, "Del": true}

// stringLiteral returns the value of a string literal expression.
func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

func matchesAny(val string, names []string) bool {
	for _, n := range names {
		if strings.EqualFold(val, n) {
			return true
		}
	}
	return false
}

var sqlTableRegexps sync.Map // joined tables -> *regexp.Regexp

// sqlNamesTable reports whether the SQL text names one of tables in statement
// position (see Rule.SQLTables).
func sqlNamesTable(sql string, tables []string) bool {
	key := strings.Join(tables, "|")
	re, ok := sqlTableRegexps.Load(key)
	if !ok {
		alts := make([]string, len(tables))
		for i, t := range tables {
			if prefix, isPrefix := strings.CutSuffix(t, "*"); isPrefix {
				alts[i] = regexp.QuoteMeta(prefix) + `[a-z0-9_$]*`
			} else {
				alts[i] = regexp.QuoteMeta(t)
			}
		}
		// keyword, optional ONLY / IF [NOT] EXISTS, optional "schema"., then the
		// whole identifier (optionally quoted) and a non-identifier character.
		pattern := `(?i)\b(?:from|into|update|join|table|truncate)\s+(?:only\s+)?(?:if\s+(?:not\s+)?exists\s+)?` +
			`(?:"?[a-z0-9_$]+"?\.)?"?(?:` + strings.Join(alts, "|") + `)"?(?:[^a-z0-9_$]|$)`
		re, _ = sqlTableRegexps.LoadOrStore(key, regexp.MustCompile(pattern))
	}
	return re.(*regexp.Regexp).MatchString(sql)
}
