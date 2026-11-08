package gcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryGcloudStepScriptIsQuiet holds the no-prompt policy for the gcloud
// calls this package writes into Cloud Build step scripts: a step runs inside
// the build with no terminal and nobody to answer, so every gcloud command a
// step runs carries --quiet. The harness in internal/cli cannot reach these
// calls — they run in the build, not in the CLI's own process — so the check is
// a static scan of the step scripts themselves.
//
// It scans every non-test file of the package, and every string literal of
// every step it finds, so a gcloud call added to an existing script, or a whole
// step added elsewhere, is covered without touching this test.
func TestEveryGcloudStepScriptIsQuiet(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	calls := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			step, ok := n.(*ast.CompositeLit)
			if !ok || !buildStepLit(step) {
				return true
			}
			ast.Inspect(step, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				script, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				found := gcloudInvocations(script)
				calls += len(found)
				for _, call := range found {
					if !strings.Contains(call, "--quiet") {
						t.Errorf("%s:%d: the step runs %q, which carries no --quiet, and a build step has no one to answer a prompt",
							name, fset.Position(lit.Pos()).Line, call)
					}
				}
				return true
			})
			return false
		})
	}
	// The scan is only as good as what it read: no gcloud call at all means the
	// walk or the shape it finds steps by is wrong, not that the steps are
	// quiet.
	if calls == 0 {
		t.Fatal("the scan found no gcloud call in any step of this package: it is reading nothing")
	}
}

// buildStepLit reports whether cl is a &cloudbuild.BuildStep{…} literal, the
// shape every step of a build request in this package is written as. The
// package the type is selected from is not matched by name, so an import alias
// is scanned as well.
func buildStepLit(cl *ast.CompositeLit) bool {
	sel, ok := cl.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "BuildStep" {
		return false
	}
	_, ok = sel.X.(*ast.Ident)
	return ok
}

// TestGcloudStepScanIsNotVacuous checks the scan against scripts whose answer
// is known, so that a green TestEveryGcloudStepScriptIsQuiet means the scripts
// are quiet rather than that the scan stopped seeing calls.
func TestGcloudStepScanIsNotVacuous(t *testing.T) {
	for _, c := range []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "the catalog read step's copy",
			script: "set -eu\ngcloud storage cp --quiet \"$$BUCKET/$$KEY\" /workspace/context/Dockerfile\n",
			want:   []string{"gcloud storage cp --quiet \"$$BUCKET/$$KEY\" /workspace/context/Dockerfile"},
		},
		{
			// The pin step's read as it stood before this test: the call the
			// scan exists to catch.
			name:   "the pin step's read without --quiet",
			script: "set -eu\ngeneration=$$(gcloud storage objects describe \"$$BUCKET/$$KEY\" --format=\"value(generation)\")\n",
			want:   []string{"gcloud storage objects describe \"$$BUCKET/$$KEY\" --format=\"value(generation)\""},
		},
		{
			// The record step's token read runs inside a double-quoted curl
			// argument, and the --format value around it has parentheses of its
			// own: neither may cut the call short.
			name:   "a call inside a quoted argument",
			script: "set -eu\ncurl -H \"Authorization: Bearer $$(gcloud auth print-access-token --quiet)\" --format=\"value(x)\" \"$$url\"\n",
			want:   []string{"gcloud auth print-access-token --quiet"},
		},
		{
			// A URL's & is an argument, not a separator, and a line continued
			// with a backslash is one command.
			name:   "an & in a URL across a continuation",
			script: "set -eu\ngcloud storage cp \"$$BUCKET/a&b\" \\\n  /workspace/out/a --quiet\n",
			want:   []string{"gcloud storage cp \"$$BUCKET/a&b\" \\\n  /workspace/out/a --quiet"},
		},
		{
			// One command per separator: a quiet call must not cover a second
			// one beside it.
			name:   "two calls in one line",
			script: "set -eu\ngcloud storage cp --quiet a b && gcloud auth print-access-token\n",
			want:   []string{"gcloud storage cp --quiet a b", "gcloud auth print-access-token"},
		},
		{
			name:   "a mention of gcloud in another command",
			script: "set -eu\necho gcloud is not run here\n",
			want:   nil,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := gcloudInvocations(c.script)
			if len(got) != len(c.want) {
				t.Fatalf("the scan found %q in %q, want %q", got, c.script, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("the scan found %q, want %q", got, c.want)
				}
			}
		})
	}
}

// gcloudInvocations returns every gcloud command script runs, each as the text
// from the word gcloud to the end of that command. A command ends where the
// shell says it does: at a newline, at an unquoted ; | or &, or at the ) that
// closes the $( ) it sits in — which is how the record step reads its token
// inside a curl argument. Quotes and parentheses are tracked so that a & inside
// a URL, a ( ) inside a --format value or a line continued with a backslash cut
// nothing, and a substitution is scanned as the command it is rather than as
// text of the command it sits in.
func gcloudInvocations(script string) []string {
	// A frame is one open parenthesis: a $( ) substitution remembers the text
	// and the quoting it interrupted, a plain ( only pairs with its ).
	type frame struct {
		outer string
		quote byte
		sub   bool
	}
	var (
		out   []string
		cmd   strings.Builder
		stack []frame
		quote byte // the ' or " quoting runs under, 0 when unquoted
	)
	collect := func() {
		if text := strings.TrimSpace(cmd.String()); text == "gcloud" || strings.HasPrefix(text, "gcloud ") {
			out = append(out, text)
		}
		cmd.Reset()
	}
	toggle := func(c byte) {
		if quote == c {
			quote = 0
			return
		}
		quote = c
	}
	for i := 0; i < len(script); {
		c := script[i]
		switch {
		case c == '\'' && quote != '"', c == '"' && quote != '\'':
			toggle(c)
			cmd.WriteByte(c)
			i++
		case quote == '\'':
			cmd.WriteByte(c)
			i++
		case c == '$' && i+1 < len(script) && script[i+1] == '(':
			stack = append(stack, frame{outer: cmd.String(), quote: quote, sub: true})
			cmd.Reset()
			quote = 0
			i += 2
		case c == '(':
			stack = append(stack, frame{})
			cmd.WriteByte(c)
			i++
		case c == ')' && len(stack) > 0:
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.sub {
				collect()
				cmd.Reset()
				cmd.WriteString(f.outer)
				quote = f.quote
			} else {
				cmd.WriteByte(c)
			}
			i++
		case quote == 0 && (c == '\n' || c == ';' || c == '|' || c == '&'):
			// A backslash continues the line the newline ends.
			if c == '\n' && strings.HasSuffix(cmd.String(), "\\") {
				cmd.WriteByte(c)
			} else {
				collect()
			}
			i++
		default:
			cmd.WriteByte(c)
			i++
		}
	}
	collect()
	return out
}
