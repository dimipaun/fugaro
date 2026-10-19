package cli

import (
	"os"
	"path/filepath"
	"regexp"
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
// name the runs/-only title without saying it is the pre-0.9.0 form, or drop
// either `tokens/` clause from the bullet — the test fails on each.
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
	// The runs/-only title may stand in this document only where the document
	// says what pre-0.9.0 releases wrote: the base's f532ee10 added
	// gcp.LegacyLauncherBucketCondition and the title beside it — `fugaro
	// doctor` recognises the old binding and replaces it by `fugaro init`
	// rather than deleting it — and the document names it for that reason. As
	// *this* document's title it may not appear: what the title is has to be
	// the constant the code carries.
	if !strings.Contains(doc, "`gcp.LauncherBucketConditionTitle` is `"+gcp.LauncherBucketConditionTitle+"`") {
		t.Errorf("%s does not state that the launcher condition's title is %s, which is what gcp.LauncherBucketConditionTitle carries", path, gcp.LauncherBucketConditionTitle)
	}
	for _, line := range strings.Split(doc, "\n") {
		i := strings.Index(line, "`"+gcp.LegacyLauncherBucketConditionTitle+"`")
		if i == -1 {
			continue
		}
		// The wording that makes it the legacy form has to come before the title
		// itself: a line that names it first and explains it afterwards reads as
		// this document's title with a footnote.
		lower := strings.ToLower(line[:i])
		if !strings.Contains(lower, "before 0.9.0") && !strings.Contains(lower, "old form") && !strings.Contains(lower, "legacy") {
			t.Errorf("%s names the runs/-only launcher condition title `%s` without saying it is the pre-0.9.0 form: a binding under that title is not the one every launcher carries now: %.160s", path, gcp.LegacyLauncherBucketConditionTitle, line)
		}
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

// The design doc that gcp/names.go, internal/infra/hardening.go and
// internal/infra/tf/cover.go cite as the contract states the launchers'
// condition in four places — §3's end-state table, §4's H5 bullets, §5's H6
// person row and §12's H5 row — and the file is excluded from the stale scan
// above because it also records what the 0.7.0 decision was. When the sealed
// budget token moved to tokens/ (budget-token-channel.md §7.2, D16) §3 was
// regenerated and the three lower places were left stating the runs/-only
// pair, contradicting the table and the code, and a later review (PR #642)
// found the fourth: §4's H1 item 2, which states in prose what the launcher's
// objectUser grant brings, and still said runs/ alone. This pins them:
//
//   - every launchers' clause the doc spells out, with <bucket> for the
//     bucket name, is one the code writes for that same placeholder, and
//     every clause the code writes is spelled out — so a doc that drops the
//     tokens/ clause, or invents one, fails against gcp.LauncherBucketCondition;
//   - §4's H1 item 2 names every prefix the code's condition writes, read off
//     the condition itself;
//   - the title statements are the current title, built from
//     gcp.LauncherBucketConditionTitle, and the three stale sentences are
//     gone. The pre-seal pair may be *named* in H5's own note (doctor has to
//     recognise it), which is why the ban is on the sentences that read as
//     the current grant rather than on the older title string.
func TestDocsBucketIAMStatesTheLaunchersCurrentCondition(t *testing.T) {
	const path = "../../docs/design/bucket-iam.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)

	clause := regexp.MustCompile(`resource\.name\.startsWith\("projects/_/buckets/<bucket>/objects/([a-z]+)/"\)`)
	spelled := map[string]bool{}
	for _, m := range clause.FindAllStringSubmatch(doc, -1) {
		spelled[m[1]] = true
	}
	if len(spelled) == 0 {
		t.Fatalf("%s spells no launchers' clause with the <bucket> placeholder: has H5 been rewritten without one?", path)
	}
	for _, m := range regexp.MustCompile(`/objects/([a-z]+)/"\)`).FindAllStringSubmatch(gcp.LauncherBucketCondition("bucket"), -1) {
		if !spelled[m[1]] {
			t.Errorf("%s does not spell the %s/ clause gcp.LauncherBucketCondition writes", path, m[1])
		}
		delete(spelled, m[1])
	}
	for p := range spelled {
		t.Errorf("%s spells a %s/ clause, which is not one of the two gcp.LauncherBucketCondition writes", path, p)
	}

	// §4's H1 item 2 states the grant in prose, and the prose is where a prefix
	// went missing: it said runs/ alone after the budget token moved to
	// tokens/. The prefixes are read off the condition, so a change on either
	// side — a clause added to gcp.LauncherBucketCondition, or a bullet that
	// stops at runs/ — fails here.
	const h1Item = "2. `roles/storage.objectUser` with the launcher condition"
	start := strings.Index(doc, h1Item)
	if start < 0 {
		t.Fatalf("%s's §4 no longer opens H1's second item with %q: the launcher's objectUser grant moved, and this test's prose check needs the new wording", path, h1Item)
	}
	item := doc[start:]
	if end := strings.Index(item, "\n\n"); end >= 0 {
		item = item[:end]
	}
	for _, m := range regexp.MustCompile(`/objects/([a-z]+)/"\)`).FindAllStringSubmatch(gcp.LauncherBucketCondition("bucket"), -1) {
		if !strings.Contains(item, "`"+m[1]+"/`") {
			t.Errorf("%s's H1 item 2 does not say the launcher condition brings access under `%s/`, which is one of the clauses gcp.LauncherBucketCondition writes: the launchers write the sealed budget token there (budget-token-channel.md §7.2, D16)", path, m[1])
		}
	}
	if stale := "which brings `create`, `delete`, `update` and `get` under `runs/`."; strings.Contains(item, stale) {
		t.Errorf("%s's H1 item 2 still states the launcher's grant as runs/ alone (%q), while gcp.LauncherBucketCondition covers tokens/ too", path, stale)
	}

	title := gcp.LauncherBucketConditionTitle
	for _, stale := range []string{
		"- `gcp.LauncherBucketConditionTitle` is `fugaro-launchers-runs`, with no description, as for the job grants.",
		"| `objectUser` | person | the condition is exactly `{title: fugaro-launchers-runs, expression: LauncherBucketCondition(b)}` for a bucket `b` in `Cover.Buckets` |",
		"| H5 | Where the condition lives | Go (`gcp.LauncherBucketCondition`), passed as a tfvar, title `fugaro-launchers-runs` | HCL-computed |",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("%s still states the launchers' pre-seal condition as the current one: %q", path, stale)
		}
	}
	for _, want := range []string{
		"`gcp.LauncherBucketConditionTitle` is `" + title + "`",
		"`{title: " + title + ", expression: LauncherBucketCondition(b)}`",
		"passed as a tfvar, title `" + title + "`",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}
