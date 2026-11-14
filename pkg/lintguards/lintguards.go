// Package lintguards is the CI-test half of the mechanical guards that keep a
// service from re-implementing something go-common already provides (audit-fix
// campaign spec §3.4). The other half is .golangci.strict.yml.
//
// A linter cannot tell `&kafka.Writer{...}` (a hand-built writer: banned) from
// `var w *kafka.Writer` (a variable that holds one go-common built: fine), nor
// match a string literal such as "X-Forwarded-For". This package can: it parses
// the service's own source and reports
//
//   - composite literals of banned types (kafka.Writer, http.Client), and
//   - string literals that name a banned header (X-Forwarded-For, X-Real-IP, …).
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
// Test files, testdata, vendor and hidden directories are not scanned.
package lintguards

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
		},
	}
}

// DefaultRules is every rule above.
func DefaultRules() []Rule {
	return []Rule{KafkaWriterLiterals(), HTTPClientLiterals(), ClientAddressHeaderLiterals()}
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
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || isExempt(rel, exempt) {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("lintguards: parse %s: %w", rel, parseErr)
		}
		out = append(out, scanFile(fset, file, rel, rules)...)
		return nil
	})
	if err != nil {
		return nil, err
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

	var out []Violation
	add := func(rule string, pos token.Pos) {
		out = append(out, Violation{Rule: rule, File: rel, Line: fset.Position(pos).Line})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			sel, ok := n.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			for _, r := range rules {
				if r.CompositeLiteral != nil && sel.Sel.Name == r.CompositeLiteral.Name &&
					imports[pkg.Name] == r.CompositeLiteral.ImportPath {
					add(r.Name, n.Pos())
				}
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			val, err := strconv.Unquote(n.Value)
			if err != nil {
				return true
			}
			for _, r := range rules {
				for _, s := range r.Strings {
					if strings.EqualFold(val, s) {
						add(r.Name, n.Pos())
					}
				}
			}
		}
		return true
	})
	return out
}
