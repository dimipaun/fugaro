package credsvc

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
)

// TestTheRetiredAPIKeyNamesAnEntryTheBundleHolds pins the name the contract
// answers for the retired `agent.auth: api-key` against the document the
// service holds. The project's own key is a bundle entry named
// config.AnthropicSecret — CredentialKey answers it under that name and
// Bundle.Lookup indexes it by it — and a craft's access names a NAME and not a
// type, so an answer spelled as the type is (config.CredentialTypeAnthropic,
// "anthropic") names no entry the bundle holds: the readiness check and the
// per-call lookup both miss it. It would also mean two credentials at once,
// since "anthropic" is a name a project may define a credential of its own
// under (R5).
//
// Hand mutation this fails on: spelling the constant as the type again, which
// leaves the run's own entry, the announced chosen credential and the record
// all naming an entry the bundle does not hold.
func TestTheRetiredAPIKeyNamesAnEntryTheBundleHolds(t *testing.T) {
	b, err := ParseBundle([]byte(config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n"))
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	entry, ok := craft.LegacyAccess(craft.LegacyInput{Auth: config.AuthAPIKey}, "implement", "")
	if !ok {
		t.Fatal("the legacy rule answered nothing for agent.auth: api-key")
	}
	if _, held := b.Lookup(entry.Name); !held {
		t.Fatalf("the rule answered %q, which no entry of the bundle holds: the bundle holds the project's key as %q and nothing else (%v)", entry.Name, config.AnthropicSecret, b.Names())
	}
	if name, _, _, ok := CredentialKey(entry.Name); !ok || name != entry.Name {
		t.Fatalf("CredentialKey(%q) = (%q, ok=%v), want the credential the bundle holds under that name", entry.Name, name, ok)
	}
}
