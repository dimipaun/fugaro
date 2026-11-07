package cli

import (
	"slices"
	"strings"
	"testing"
)

// TestCredentialKeysAreNeverShared pins the owner-only rule at its third door,
// the one every local-only key has (the account-choice keys' test next door is
// the same shape): localcfg.Config.Shared drops the credentials, the pools and
// the crafts, `sharedForbidden` refuses them by key, and the decoded-value case
// refuses a file that spelled one past the key scan. The shared file is the one
// any launcher with objectAdmin on the runs bucket can write, and a credential
// defined there — or a craft's access naming one — is that launcher's to set,
// on a teammate's machine, against a project whose owner never admitted it
// (docs/model-crafts/reconciliation.md F12, D1628).
//
// Nothing reads these keys yet (the resolver is MC2), which is why this is the
// door worth closing now: the file is written today and read tomorrow.
func TestCredentialKeysAreNeverShared(t *testing.T) {
	for _, key := range []string{"credentials", "pools", "crafts"} {
		if !slices.Contains(sharedForbidden, key) {
			t.Errorf("sharedForbidden does not name %s, so a published credential would be read", key)
		}
	}
	for name, block := range map[string]string{
		"a credential":     "credentials:\n  - {name: acme-main, type: openrouter}\n",
		"a pool":           "pools:\n  - {name: acme-pool, members: [vertex]}\n",
		"a craft's access": "crafts:\n  coder: {access: acme-main}\n",
		"a craft's model":  "crafts:\n  coder: {model: claude-opus-5-5}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseShared([]byte(validShared()+block), sharedAnchor)
			if err == nil {
				t.Fatalf("a shared config carrying %s was accepted", block)
			}
			for _, want := range []string{"never published", "fugaro update"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}
