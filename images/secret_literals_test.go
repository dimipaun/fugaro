package images_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// jwtLiteral is the shape Trivy's own `jwt-token` rule reads as a credential:
// three base64url segments of at least ten characters each, the first opening
// with `eyJ` (the base64url of `{"`). The catalog build's catalog-scan step
// stops on one -- Cloud Build 24f5, 2026-11-22, failed there and published no
// image -- so this scan runs first, over the checkout the image carries, and a
// fixture that needs a token stores it as its parts and joins them at the point
// of use instead. images/trivy-secret.yaml is the gate this mirrors and is
// never given an allowance for a path of ours.
var jwtLiteral = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)

// jwtscanFiles lists the files this scan reads: what git tracks when root is a
// checkout (repoFiles), minus node_modules. A dependency tree is not ours to
// edit, the scanners have rules of their own for it, and no fixture of this
// repository lives in one. testdata is deliberately *not* excluded: the image
// carries the whole checkout, testdata included, and the finding this scan
// exists for was in one.
func jwtscanFiles(t *testing.T, root string) []string {
	t.Helper()
	files, err := repoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, rel := range files {
		if rel == "node_modules" || strings.HasPrefix(rel, "node_modules/") || strings.Contains(rel, "/node_modules/") {
			continue
		}
		out = append(out, rel)
	}
	return out
}

// jwtLiteralOffenders returns one message per line of the named files under
// root that carries a JWT-shaped literal. A file git lists but that is not on
// disk (deleted, not staged) is not a file this checkout has, and a file with a
// NUL byte in it is binary and not read -- the scanner's text rules do not
// apply to it either.
func jwtLiteralOffenders(root string, files []string) ([]string, error) {
	var hits []string
	for _, rel := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			// A submodule's gitlink: ls-files names the directory, not a file.
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if jwtLiteral.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d carries a JWT-shaped literal: a file stores a token as its parts and joins them where it is used, so the catalog image scan is never the first to find it", rel, i+1))
			}
		}
	}
	return hits, nil
}

// TestNoJWTLiteralInTheCheckout is the fence in front of the catalog build. A
// JWT-shaped string in any tracked file -- a fixture, a test, a golden, a doc
// -- fails the image's secret scan after a build, which is a lost build and an
// unpublished image; failing here instead costs a test run. The scan is only as
// good as its listing, so a listing that came back nearly empty is a failure
// rather than a pass.
func TestNoJWTLiteralInTheCheckout(t *testing.T) {
	root := ".."
	files := jwtscanFiles(t, root)
	if len(files) < 100 {
		t.Fatalf("listed only %d files: the scan would not see the checkout", len(files))
	}
	hits, err := jwtLiteralOffenders(root, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range hits {
		t.Error(hit)
	}
}

// TestJWTLiteralScanFindsAToken is the mutation this scan exists to catch, and
// the shape of the fix in one test. The token below is assembled from its three
// parts, one literal per line so that this file passes its own scan: written as
// one string it is a finding, written as its parts it is not. A scan that
// stopped matching, or one that flagged the parts too, fails here.
func TestJWTLiteralScanFindsAToken(t *testing.T) {
	root := t.TempDir()
	parts := []string{
		"eyJhbGciOiJFZERTQSJ9",
		"eyJhdWQiOiJhY21lIn0",
		"ABCDEFGHIJKLMNOPQRST",
	}
	token := strings.Join(parts, ".")
	writeFile(t, root, "shared/joined.json", `{"token":"`+token+`"}`)
	writeFile(t, root, "shared/parts.json", `{"parts":[
	"`+parts[0]+`",
	"`+parts[1]+`",
	"`+parts[2]+`"
]}`)
	writeFile(t, root, "shared/clean.go", "package shared\n")

	files, err := walkFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %v, want the three written files", files)
	}
	hits, err := jwtLiteralOffenders(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.HasPrefix(hits[0], "shared/joined.json:1") {
		t.Fatalf("hits = %v, want the one line of shared/joined.json: the joined token is a finding and its parts are not", hits)
	}
}
