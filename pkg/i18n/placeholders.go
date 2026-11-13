package i18n

import (
	"fmt"
	"sort"
	"text/template"
	"text/template/parse"
)

// Placeholders returns the sorted, de-duplicated names of the parameters a
// translation value reads from its params map, found by walking the parsed
// template — never by pattern. {{.a}}, {{if .a}}, {{- .a -}}, {{.a | f}},
// {{.a.b}}, {{else if .a}}, {{index . "a"}} and {{$.a}} all report "a".
//
// A value that is not a valid template is an error (the same one rendering it
// would hit), so a caller that validates with Placeholders also rejects
// `{{.a}`.
//
// The walk is deliberately flat: params is a map, so a field inside {{with}}
// or {{range}} is still counted as a parameter name. That can only over-report,
// which is the safe direction for an allow-list check.
func Placeholders(s string) ([]string, error) {
	t, err := template.New("i18n").Option("missingkey=error").Parse(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}
	found := map[string]bool{}
	if t.Tree != nil && t.Root != nil {
		walkNode(t.Root, found)
	}
	names := make([]string, 0, len(found))
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

func walkNode(n parse.Node, found map[string]bool) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			walkNode(c, found)
		}
	case *parse.ActionNode:
		walkNode(n.Pipe, found)
	case *parse.IfNode:
		walkBranch(&n.BranchNode, found)
	case *parse.RangeNode:
		walkBranch(&n.BranchNode, found)
	case *parse.WithNode:
		walkBranch(&n.BranchNode, found)
	case *parse.TemplateNode:
		walkNode(n.Pipe, found)
	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, c := range n.Cmds {
			walkNode(c, found)
		}
	case *parse.CommandNode:
		walkCommand(n, found)
	case *parse.FieldNode:
		if len(n.Ident) > 0 {
			found[n.Ident[0]] = true
		}
	case *parse.VariableNode:
		// {{$.name}}: $ is the root params map.
		if len(n.Ident) > 1 && n.Ident[0] == "$" {
			found[n.Ident[1]] = true
		}
	case *parse.ChainNode:
		walkNode(n.Node, found)
	}
}

func walkBranch(b *parse.BranchNode, found map[string]bool) {
	walkNode(b.Pipe, found)
	walkNode(b.List, found)
	walkNode(b.ElseList, found)
}

func walkCommand(c *parse.CommandNode, found map[string]bool) {
	// {{index . "name"}} reads params["name"] through a string literal.
	if len(c.Args) >= 3 {
		if id, ok := c.Args[0].(*parse.IdentifierNode); ok && id.Ident == "index" {
			if _, dot := c.Args[1].(*parse.DotNode); dot {
				if lit, isStr := c.Args[2].(*parse.StringNode); isStr {
					found[lit.Text] = true
				}
			}
		}
	}
	for _, a := range c.Args {
		walkNode(a, found)
	}
}
