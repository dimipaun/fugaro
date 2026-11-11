package endpoint

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/printer"
	gotoken "go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// otherHex is the digit a test writes over one of the token's own, so a case
// changes the value whatever the random token happened to be: a literal
// replacement would be a case that is a no-op whenever crypto/rand draws the
// character the test wrote.
func otherHex(c byte) byte {
	if c == '0' {
		return '1'
	}
	return '0'
}

// TestConstantTimeTokenCompare holds D1359's constant-time rule, and it holds it
// from both sides, because neither side on its own can fail for the reason the
// rule exists.
//
// The behaviour is the table below: a credential that differs anywhere -- the
// first byte, the last, its length -- is refused, so the whole value decides the
// answer. What no run can measure is how long the answer took, and a wall clock
// in a test measures the machine it runs on rather than the comparison: an
// early-exit byte loop agrees with crypto/subtle on every row below, so a test
// made only of rows stays green through exactly the regression the rule is about
// (F23: a local process narrowing the token one byte at a time and reading the
// answer off its own clock).
//
// So the other half is read off the source of the function itself. sameToken's
// body has to be the one call to crypto/subtle.ConstantTimeCompare over the two
// values it was given and nothing else -- no branch, no loop, no compare that
// stops at the first byte that differs -- which is the claim token.go makes for
// it and the claim this test discharges. It is read off the syntax tree rather
// than off the text of the file, the way internal/cli's census holds its own
// rule: a definition is not a call, and a word in a comment is not a comparison.
func TestConstantTimeTokenCompare(t *testing.T) {
	token, err := mint()
	if err != nil {
		t.Fatalf("minting a token: %v", err)
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("the token is not hex: %v", err)
	}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"the token against itself", token, token, true},
		{"two empty values", "", "", true},
		{"the first byte changed", string(otherHex(token[0])) + token[1:], token, false},
		{"the last byte changed", token[:len(token)-1] + string(otherHex(token[len(token)-1])), token, false},
		{"a prefix of the token", token[:len(token)-1], token, false},
		{"the token with a byte appended", token + "0", token, false},
		{"an empty credential", "", token, false},
		{"another value of the same length", strings.Repeat("a", len(token)), token, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameToken(c.a, c.b); got != c.want {
				t.Errorf("sameToken over %d-byte values = %v, want %v", len(c.a), got, c.want)
			}
			if want := subtle.ConstantTimeCompare([]byte(c.a), []byte(c.b)) == 1; sameToken(c.a, c.b) != want {
				t.Errorf("sameToken disagrees with crypto/subtle over %d-byte values", len(c.a))
			}
		})
	}
	// Every position of the value decides it, which is what "the whole value"
	// means: a comparison that stopped after a byte, a length or a checksum
	// would accept one of these.
	for i := range token {
		changed := []byte(token)
		changed[i] = otherHex(changed[i])
		if sameToken(string(changed), token) {
			t.Fatalf("a token changed at position %d is accepted", i)
		}
	}

	sameTokenIsOneConstantTimeCompare(t)
}

// sameTokenSrc is sameToken as its own source has it: the body the rule is
// about, the names it was given, and the name this file gives crypto/subtle.
type sameTokenSrc struct {
	body   *ast.BlockStmt
	params []string
	subtle string
}

// readSameToken parses this package's token.go and finds sameToken in it. The
// file is found from this test's own path rather than from a working directory,
// so the pin reads the source it ships beside and not whatever the test happened
// to be run from.
func readSameToken(t *testing.T) sameTokenSrc {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("this test cannot find its own file, so it cannot read the function it pins: the constant-time rule is then held by nothing")
	}
	path := filepath.Join(filepath.Dir(file), "token.go")
	parsed, err := parser.ParseFile(gotoken.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	src := sameTokenSrc{subtle: ""}
	for _, spec := range parsed.Imports {
		if p, err := strconv.Unquote(spec.Path.Value); err != nil || p != "crypto/subtle" {
			continue
		}
		src.subtle = "subtle"
		if spec.Name != nil {
			src.subtle = spec.Name.Name
		}
	}
	if src.subtle == "" {
		t.Fatalf("%s no longer imports crypto/subtle, so the comparison is not the constant-time one (D1359)", path)
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "sameToken" {
			continue
		}
		src.body = fn.Body
		for _, field := range fn.Type.Params.List {
			for _, name := range field.Names {
				src.params = append(src.params, name.Name)
			}
		}
	}
	if src.body == nil {
		t.Fatalf("%s defines no sameToken: the comparison this test pins has been renamed or removed", path)
	}
	if len(src.params) != 2 {
		t.Fatalf("sameToken takes %d values, and the rule is about comparing the two it is given", len(src.params))
	}
	return src
}

// sameTokenIsOneConstantTimeCompare refuses anything but the body D1359 asks
// for: one return, of one call to crypto/subtle.ConstantTimeCompare, over the
// two values the function was given, compared against 1.
//
// The strictness is the point rather than a preference. Every row of the table
// above passes for a re-implementation that stops at the first byte that
// differs, so the shape of the body is the only thing left that can fail when
// the constant-time comparison is replaced by a comparison that is not one --
// which is what this test is named for.
func sameTokenIsOneConstantTimeCompare(t *testing.T) {
	t.Helper()
	src := readSameToken(t)
	if len(src.body.List) != 1 {
		t.Fatalf("sameToken's body is %d statements, want one: a comparison with anything around it -- a branch, a loop, a compare that stops at the first byte that differs -- is the regression this test is named for", len(src.body.List))
	}
	ret, ok := src.body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		t.Fatalf("sameToken does not return a single expression, so the comparison is not the whole of what it decides")
	}
	cmp, ok := ret.Results[0].(*ast.BinaryExpr)
	if !ok || cmp.Op != gotoken.EQL {
		t.Fatalf("sameToken does not return a comparison: %s", exprText(ret.Results[0]))
	}
	if lit, ok := cmp.Y.(*ast.BasicLit); !ok || lit.Value != "1" {
		t.Errorf("sameToken compares against %s, want 1: crypto/subtle answers with an int and 1 is the answer that means the values are equal", exprText(cmp.Y))
	}
	call, ok := cmp.X.(*ast.CallExpr)
	if !ok {
		t.Fatalf("sameToken's comparison is not a function call: %s", exprText(cmp.X))
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "ConstantTimeCompare" {
		t.Fatalf("sameToken does not call crypto/subtle.ConstantTimeCompare: %s", exprText(call.Fun))
	}
	qual, ok := sel.X.(*ast.Ident)
	if !ok || qual.Name != src.subtle {
		t.Errorf("sameToken's comparison is not this file's crypto/subtle (%q)", exprText(sel.X))
	}
	if len(call.Args) != 2 {
		t.Fatalf("sameToken compares %d values, want the two it was given", len(call.Args))
	}
	compared := map[string]bool{}
	for _, arg := range call.Args {
		name, ok := convertedValue(arg)
		if !ok {
			t.Errorf("sameToken compares %s, which is not one of its values as bytes", exprText(arg))
			continue
		}
		compared[name] = true
	}
	// Both of the function's values and no others: the two argument checks above
	// leave at most two names here, so these two lines are the whole of it.
	for _, p := range src.params {
		if !compared[p] {
			t.Errorf("sameToken does not compare %s, so a credential is judged on less than the whole of what it sent", p)
		}
	}
	for name := range compared {
		if !slices.Contains(src.params, name) {
			t.Errorf("sameToken compares %q, which is not one of its values", name)
		}
	}
}

// convertedValue is the name of the parameter a `[]byte(x)` conversion carries,
// and whether the expression is one at all.
func convertedValue(expr ast.Expr) (string, bool) {
	conv, ok := expr.(*ast.CallExpr)
	if !ok || len(conv.Args) != 1 {
		return "", false
	}
	arr, ok := conv.Fun.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return "", false
	}
	if elt, ok := arr.Elt.(*ast.Ident); !ok || elt.Name != "byte" {
		return "", false
	}
	id, ok := conv.Args[0].(*ast.Ident)
	if !ok {
		return "", false
	}
	return id.Name, true
}

// exprText is the expression as the source writes it, for a failure message a
// reader can act on: a mutation reported as "something else" is a mutation
// nobody can see.
func exprText(expr ast.Expr) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, gotoken.NewFileSet(), expr); err != nil {
		return "an expression that could not be printed"
	}
	return b.String()
}

// TestMintIsA256BitRandomToken pins what a token is, because every rule built
// on one assumes exactly this: tokenBytes drawn from crypto/rand, written as
// lower-case hex, and never the same value twice.
func TestMintIsA256BitRandomToken(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		token, err := mint()
		if err != nil {
			t.Fatalf("minting a token: %v", err)
		}
		if len(token) != 2*tokenBytes {
			t.Fatalf("the token is %d characters, want %d", len(token), 2*tokenBytes)
		}
		raw, err := hex.DecodeString(token)
		if err != nil || len(raw) != tokenBytes {
			t.Fatalf("the token is %d characters and decodes to %d bytes, want %d of hex", len(token), len(raw), tokenBytes)
		}
		if token != strings.ToLower(token) {
			t.Fatalf("the token is not lower-case hex")
		}
		if seen[token] {
			t.Fatalf("mint returned the same token twice")
		}
		seen[token] = true
	}
}

// TestBearerTokenReadsOneHeader pins the one place the credential is read:
// exactly one Authorization header, carrying the bearer scheme, quoted whole
// the way the registry file quotes it.
func TestBearerTokenReadsOneHeader(t *testing.T) {
	token, err := mint()
	if err != nil {
		t.Fatalf("minting a token: %v", err)
	}
	cases := []struct {
		name    string
		headers []string
		want    string
		ok      bool
	}{
		{"the bearer header", []string{"Bearer " + token}, token, true},
		{"the scheme in another case", []string{"bearer " + token}, token, true},
		{"no header at all", nil, "", false},
		{"another scheme", []string{"Token " + token}, "", false},
		{"basic auth", []string{"Basic " + token}, "", false},
		{"the scheme alone", []string{"Bearer"}, "", false},
		{"an empty value", []string{""}, "", false},
		{"the value with no scheme", []string{token}, "", false},
		{"two headers carrying it", []string{"Bearer " + token, "Bearer " + token}, "", false},
		// A second space is not the header's to normalise: the credential it
		// hands over carries the space, and a token with one in front of it is
		// not the token.
		{"two spaces after the scheme", []string{"Bearer  " + token}, " " + token, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/state", nil)
			for _, h := range c.headers {
				r.Header.Add(authorizationHeader, h)
			}
			got, ok := bearerToken(r)
			if ok != c.ok || got != c.want {
				t.Errorf("bearerToken = (%d characters, %v), want (%d characters, %v)", len(got), ok, len(c.want), c.ok)
			}
		})
	}
}
