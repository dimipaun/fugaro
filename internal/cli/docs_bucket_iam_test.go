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
