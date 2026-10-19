package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// Since 0.7.0 launchers hold objectViewer and a runs/-conditioned objectUser
// on the runs bucket, never objectAdmin (docs/design/bucket-iam.md). The
// sentences that said otherwise are gone from every doc but the hardening's
// own design, the plans and the release notes (not scanned).
func TestDocsLaunchersDoNotHoldObjectAdmin(t *testing.T) {
	stale := []string{
		"Launchers and operators already hold `objectAdmin`",
		"launchers and operators already hold `roles/storage.objectAdmin`",
		"A launcher also gets `roles/storage.objectAdmin`",
		"`fugaroJobRunner` on each workflow job (§3.4), `roles/storage.objectAdmin`",
		"`roles/storage.objectAdmin` on it only to launchers",
		"Anyone holding `roles/storage.objectAdmin` on the runs bucket: launchers and operators",
		"The bucket stays writable by launchers until the hardening lands",
		"every launcher holds `roles/storage.objectAdmin` on the runs bucket and can write the layer object",
		"any launcher's own terminal can publish today",
		"today that is every launcher and every operator, not operators alone",
		"the bucket stays writable by every launcher until then",
		"any launcher holding `roles/storage.objectAdmin`, not only an",
		"any **launcher** holding `roles/storage.objectAdmin` on the runs bucket, not only operators",
		"Until the bucket IAM hardening ships (release 0.7.0,",
		"any run of the same repository, since they share the job's service account and bucket prefix — can plant context there",
		"a profile is a key inside the same launcher-writable `fugaro/project-layer.yaml` object T1 already distrusts",
		"only an operator or the sweeper can clear it there",
		"for an operator, or the sweeper, to run instead",
		"an operator or the sweeper runs instead",
	}
	var files []string
	for _, g := range []string{"../../docs/*.md", "../../docs/design/*.md", "../../README.md", "../../SECURITY.md", "../../plugin/skills/*/SKILL.md", "../../plugin/skills/*/reference/*.md"} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "bucket-iam.md") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range stale {
			if strings.Contains(string(data), s) {
				t.Errorf("%s still says %q", f, s)
			}
		}
	}
	// Claims the hardening actually makes, which a careless edit could
	// silently widen back out (a mutation of each is proven red in the PR
	// that added it): the end-state table's launcher row, the shared-config
	// error-table row for a refused publish, recipes.md's operator-only
	// sentence, release.md's 0.7.0 section and v1.md's launcher grant.
	required := map[string][]string{
		"../../docs/gcp-setup.md": {
			"write only `runs/`", "runs-bucket-iam", "add-iam-policy-binding", "DATA_WRITE",
			"| Launcher (not an operator) | read | read | read, write | read |",
			"| needs the operator role | a launcher ran a publish",
			"only operators can write it",
		},
		"../../docs/recipes.md": {
			"It is an operator command: once an operator has applied the 0.7.0 bucket hardening",
		},
		"../../docs/release.md": {
			"## Rolling out the 0.7.0 bucket hardening",
			"`runs-bucket-iam` ok",
		},
		"../../docs/design/v1.md": {
			"`roles/storage.objectUser` on its `runs/` only",
		},
	}
	for file, wants := range required {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s lacks %q", file, want)
			}
		}
	}
}

// TestBucketIamDocNamesTheWidenedLauncherCondition: the launchers' objectUser
// grant on the runs bucket is conditioned to runs/ **and** tokens/, under the
// title gcp.LauncherBucketConditionTitle ("fugaro-launchers-runs-tokens"), and
// the widening is where this document says it is (design
// budget-token-channel.md §7.5, decisions D16 and D19). A condition that
// differs by one byte is a different binding (names.go, H5's own convention),
// which is why the widening took a new title rather than a new expression
// under the old one. Three places in this file still named the runs/-only
// condition — H5's two bullets, the install guard's exact-match row and H5's
// decision row — while the end-state table above them already carried the
// widened pair, and this is the document an operator reads the binding out
// of. The title is read from the constant, and the expression is compared
// against the one gcp.LauncherBucketCondition builds, so a doc and a tree that
// disagree fail here instead of only the doc.
//
// Mutation (run, restore): rename the title back to `fugaro-launchers-runs`,
// or drop either `tokens/` clause from the bullet — the test fails on each.
func TestBucketIamDocNamesTheWidenedLauncherCondition(t *testing.T) {
	const path = "../../docs/design/bucket-iam.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)

	if want := "fugaro-launchers-runs-tokens"; gcp.LauncherBucketConditionTitle != want {
		t.Fatalf("gcp.LauncherBucketConditionTitle is %q; this guard pins the title %s must carry, so the two have to be updated together", gcp.LauncherBucketConditionTitle, path)
	}
	if strings.Contains(doc, "`fugaro-launchers-runs`") {
		t.Errorf("%s still names the runs/-only launcher condition title `fugaro-launchers-runs`: a binding under that title is not the one every launcher carries", path)
	}
	// Every place this file names it: the end-state table's launcher row, H5's
	// two bullets, and the install guard's condition row.
	if n := strings.Count(doc, gcp.LauncherBucketConditionTitle); n < 4 {
		t.Errorf("%s names %s %d times: the end-state table, H5's bullets and the guard's condition row all carry it", path, gcp.LauncherBucketConditionTitle, n)
	}
	// The expression H5 describes has to be the one the code builds, both
	// clauses of it, with the bucket left as the placeholder H5 writes (the
	// bucket a real call takes is validated, so the doc's placeholder is put
	// back over a real name rather than passed to it).
	const bucket = "fugaro-runs"
	wantExpr := `resource.name.startsWith("projects/_/buckets/` + bucket + `/objects/runs/") || resource.name.startsWith("projects/_/buckets/` + bucket + `/objects/tokens/")`
	if got := gcp.LauncherBucketCondition(bucket); got != wantExpr {
		t.Fatalf("gcp.LauncherBucketCondition(%q) is %q; this guard pins the expression %s must carry", bucket, got, path)
	}
	docExpr := strings.ReplaceAll(wantExpr, bucket, "<bucket>")
	if !strings.Contains(doc, docExpr) {
		t.Errorf("%s does not spell the widened condition H5 is about: %q", path, docExpr)
	}
}
