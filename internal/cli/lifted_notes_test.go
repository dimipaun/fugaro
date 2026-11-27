package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// lifted_notes_test.go is S2's half of lift L8: `fugaro validate` and `fugaro
// doctor` name every lifted key a repository's file still writes, every value
// RT1's tighten-only merge dropped, and the recipes cleanup diff — once each,
// as notes, and never as a refusal. Release A reads a lifted key and does not
// honour it, so none of these is a problem: release B is the release that
// refuses (spec §4), and until then the reader is told what the file still
// writes and what the project now decides.

// TestValidateNamesLiftedKeys: a file that still writes two of the keys the
// project now owns is valid — release A refuses nothing a repository wrote —
// and each of them is named once, with the fix C1's sentence carries, on
// stderr and in --json's notes.
func TestValidateNamesLiftedKeys(t *testing.T) {
	// cliPinnedYAML writes agent.model and agent.models.background, both of
	// C1's keys; writeConfig keeps the file's own lines as its author wrote
	// them and publishes their values as aurora's project layer, which is
	// where a run reads them now (S1, D3237), so the file resolves and
	// validates.
	path := writeConfig(t, cliPinnedYAML)
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("a file that writes only lifted keys must validate: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, "is valid") {
		t.Fatalf("out = %q", out)
	}
	for _, key := range []string{"agent.model", "agent.models.background"} {
		// C1's sentence names the key and the command that states it in its
		// new home; a second copy is the same key named twice, which the
		// acceptance forbids.
		if n := strings.Count(errOut, "note: "+key+" is the project's choice"); n != 1 {
			t.Errorf("validate printed %d notes for %s, want 1:\n%s", n, key, errOut)
		}
	}
	if !strings.Contains(errOut, "fugaro models use --layer project") {
		t.Errorf("the note must carry the fix that states the key in its new home:\n%s", errOut)
	}

	// --json carries the same notes, so a script sees what a person read.
	out, _, err = execute(t, "validate", "--json", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var got validateOutput
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if !got.Valid {
		t.Fatalf("valid = false: %+v", got)
	}
	joined := notesText(got.Notes)
	for _, key := range []string{"agent.model", "agent.models.background"} {
		if n := strings.Count(joined, key+" is the project's choice"); n != 1 {
			t.Errorf("--json named %s %d times, want 1: %+v", key, n, got.Notes)
		}
	}
}

// TestValidatePrintsDroppedValuesOnce: a value RT1's tighten-only merge drops
// is named once, with the project's value that stands in its place, and never
// as a problem — the file is valid, and the run simply does not use its line.
func TestValidatePrintsDroppedValuesOnce(t *testing.T) {
	// The project states review_rounds: 4 and the file writes 2, which does
	// not tighten it: RT1 drops the file's value and records it (B4, spec
	// §5.2, RT1's Resolution.Ignored).
	attachLayer(t, "aurora", minimalAnchored+"defaults:\n  agent: { review_rounds: 4 }\n")
	body := strings.Replace(cliMinimalYAML, "workflows:", "agent: { review_rounds: 2 }\nworkflows:", 1)
	path := writeRawConfig(t, anchoredText(body))
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("a dropped value is not a problem: %v\n%s%s", err, out, errOut)
	}
	if n := strings.Count(errOut, "note: agent.review_rounds: "); n != 1 {
		t.Errorf("validate printed %d notes for the dropped value, want 1:\n%s", n, errOut)
	}
	if !strings.Contains(errOut, "the project's 4 stands") {
		t.Errorf("the note must name the project's value that stands:\n%s", errOut)
	}

	out, _, err = execute(t, "validate", "--json", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var got validateOutput
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if n := strings.Count(notesText(got.Notes), "agent.review_rounds"); n != 1 {
		t.Errorf("--json named the dropped key %d times, want 1: %+v", n, got.Notes)
	}
}

// TestDoctorNamesLiftedKeys: doctor names the same keys, once each, in its
// human view and in --json's notes — a key the project now owns is something
// the reader has to act on, and doctor is one of the three places release A
// prints it (spec §4).
func TestDoctorNamesLiftedKeys(t *testing.T) {
	newDoctorRig(t)
	// The file writes agent.model, one of C1's keys, and names no gcp_project,
	// so no layer applies: the drop is the layerless arm's, which records the
	// note all the same (config.Resolve).
	checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML + "agent: { auth: api-key, model: claude-sonnet-5-5 }\n"})
	out, errOut, err := execute(t, "doctor")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if n := strings.Count(out, "note: agent.model is the project's choice"); n != 1 {
		t.Errorf("doctor printed %d notes for the lifted key, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "fugaro models use --layer project") {
		t.Errorf("doctor's note must carry the fix that states the key in its new home:\n%s", out)
	}

	out, _, err = execute(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	if n := strings.Count(notesText(o.Notes), "agent.model is the project's choice"); n != 1 {
		t.Errorf("--json named the lifted key %d times, want 1: %+v", n, o.Notes)
	}
}

// TestDoctorPrintsTheCleanupLines is doctor's half of PR5's cleanup diff
// (D3296, S2): a checkout that still holds .fugaro/recipes/ while the project
// store already holds what it says is offered the removal, in doctor's notes
// channel, and nothing is deleted.
func TestDoctorPrintsTheCleanupLines(t *testing.T) {
	r := newDoctorRig(t)
	// The store the cleanup diff reads is the runs bucket, so the rig's config
	// names a file:// one, as layerBucketWith does for the layer checks.
	isolateCache(t)
	store := t.TempDir()
	copyName := recipe.CopyName("team", "github", "acme/app")
	key := filepath.Join(store, filepath.FromSlash(recipe.ObjectKey(copyName)))
	if err := os.MkdirAll(filepath.Dir(key), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte(cleanupTeam), 0o644); err != nil {
		t.Fatal(err)
	}
	// The bucket the store is reached through names the installation, as a real
	// one does: every cloud read checks the project marker first, so without it
	// there is no store to look a copy up in.
	marker := filepath.Join(store, filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	body, merr := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if merr != nil {
		t.Fatal(merr)
	}
	if err := os.WriteFile(marker, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := r.cfg + "bucket_url: file://" + store + "\n"
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := repoCheckout(t, githubOrigin, cliMinimalYAML)
	testutil.WriteFiles(t, root, map[string]string{".fugaro/recipes/team.yaml": cleanupTeam})
	t.Chdir(root)

	out, _, err := execute(t, "doctor")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"is in the project store as " + copyName, "remove the folder with `git rm -r " + recipe.RepoDir + "`"} {
		if n := strings.Count(out, want); n != 1 {
			t.Errorf("doctor printed %q %d times, want 1:\n%s", want, n, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(recipe.RepoPath("team")))); err != nil {
		t.Fatalf("doctor deleted the leftover file, and it offers the diff rather than applying it: %v", err)
	}
}

// notesText is the notes as one text, the way each is printed, for a count
// over all of them.
func notesText(notes []config.Problem) string {
	lines := make([]string, len(notes))
	for i, n := range notes {
		lines[i] = n.String()
	}
	return strings.Join(lines, "\n")
}
