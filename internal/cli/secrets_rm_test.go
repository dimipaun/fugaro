package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// TestSecretsRmRemovesTheVersionAndTheEntry: `secrets rm NAME` withdraws a
// project secret — the Secret Manager container with every version it holds,
// and the local config's project_secrets.NAME entry that made it
// project-wide — which is the pair D1171 says a secret with no `rm` leaves
// standing forever.
func TestSecretsRmRemovesTheVersionAndTheEntry(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [\"acme/app\"] }\n")
	id := gcp.ProjectSecretID("aurora", "bitbucket-token")
	sm.Seed(id, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue,
		gcp.LabelProject: "aurora",
		gcp.LabelSecret:  "bitbucket-token",
	}, []byte(tokenValue))

	out, errOut, err := execute(t, "secrets", "rm", "bitbucket-token", "--yes")
	if err != nil {
		t.Fatalf("secrets rm: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if got := sm.Deleted(); len(got) != 1 || got[0] != id {
		t.Fatalf("deleted %v, want just %s", got, id)
	}
	if got := sm.Latest(id); got != nil {
		t.Fatalf("the container still holds %q", got)
	}
	after := readFileString(t, os.Getenv("FUGARO_CONFIG"))
	if strings.Contains(after, "project_secrets") || strings.Contains(after, "bitbucket-token") {
		t.Fatalf("the config entry is still there:\n%s", after)
	}
	if strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("the value leaked: %q %q", out, errOut)
	}
}
