package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsStateWhichConfigTheReadinessGateCanFireFor: the setup guide used to
// say that what blocks a deploy of a project whose jobs carry no backend is the
// readiness gate, "which fires once the project has published a policy". That
// is not what the gate does. Discovery cannot see a policy at all — the node is
// read through the operator's own config (internal/cli's policyReadOnce, over
// budget.rtdb_url) — so a config that names no database address reports no
// policy however much the project has published, and a job deployed from it is
// never held back. Nothing refuses there; the job simply goes out unable to
// read a policy that may exist. The owner accepted the gap, and the guide now
// states it exactly, because the old sentence is wrong in the direction that
// costs something: an operator reads it and waits for a refusal that never
// comes.
func TestDocsStateWhichConfigTheReadinessGateCanFireFor(t *testing.T) {
	data, err := os.ReadFile("../../docs/gcp-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(data)), " ")
	for _, want := range []string{
		"the readiness gate above blocks a *deploy* only where the config making the plan can see a policy, because that config is what it reads the node through",
		"A config that names **no** `budget.rtdb_url` sees no policy at all, so the gate never fires for it, whatever the project has published",
		"nothing refuses that deploy, and the job goes out unable to read a policy that may well exist",
		"That is a gap in the gate, accepted by the owner on purpose, not a rule a policy would close.",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/gcp-setup.md does not say %q", want)
		}
	}
	if strings.Contains(doc, "which fires once the project has published a policy") {
		t.Error("docs/gcp-setup.md still says the gate \"fires once the project has published a policy\": a config that cannot see a policy is never refused by it")
	}
}
