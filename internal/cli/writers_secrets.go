package cli

import (
	"github.com/dimipaun/fugaro/internal/settings"
)

// secretsValuePlaceholder is the word a registered argv uses where the value
// goes. It is written as the literal word rather than through a constant
// because the contract revision that adds `settings.ValuePlaceholder` (CR1,
// #2842) holds exactly this spelling: the two agree today, and the rebase onto
// CR1 replaces the word with the constant in one place.
const secretsValuePlaceholder = "<value>"

// secretsNamePlaceholder is the word the one template that needs a secret's
// name writes where it goes. It is this family's own word and not one of the
// contract's three (KeyPlaceholder, ValuePlaceholder and the per-segment
// `<n>`): `secret_expires` is a map keyed by a secret's name, the whole key
// there is the row and not a name a command accepts, and ArgvFor leaves a word
// it cannot place as written — so the template states the name the way the help
// and the refusals do, and the conformance test fills it.
const secretsNamePlaceholder = "<name>"

// secretsSettingsStore is where a secret record's write lands, read off the
// writer registered for the key: the store is what the after-write hook reads
// to choose the stages the write owes, and this file is the one place that says
// which it is for these three keys (D2615). A key with no registration answers
// the local config, which is where every key of this file lands — a
// registration that went missing is the conformance test's failure (T1) and not
// a write that quietly owes no stage.
func secretsSettingsStore(key string) settings.Store {
	if w, ok := settings.WriterOf(key); ok {
		return w.Store
	}
	return settings.StoreLocal
}

// This file registers the three settings rows the secrets family owns, the
// ones no other family writes (docs/contracts/config-ownership.md §4, D2616):
// the machine's own records about secrets — when one lapses, how often the
// installation rotates, and which Bitbucket API roots a token may be sent to.
//
// They are registered here rather than in `internal/settings`, which holds no
// writer, and each pattern is one of the concrete registry rows
// (`secret_rotation_days`, `secret_expires`, `bitbucket_api_roots`): none
// carries a `*`, and none collides with another family's pattern.
//
// Two of the three argv templates are the whole command line a person runs.
// `secret_expires` is not: the row is a map keyed by secret name, so the
// command that writes one entry takes the name the key cannot supply, and the
// template carries it as the word `<name>` — the same word the help and the
// refusals use for a secret's name, left as written by ArgvFor.
func init() {
	for _, w := range []struct {
		pattern string
		writer  settings.Writer
	}{
		{"secret_expires", settings.Writer{
			Command: "fugaro secrets set",
			Argv:    []string{"secrets", "set", secretsNamePlaceholder, "--expires", secretsValuePlaceholder},
			Store:   settings.StoreLocal,
		}},
		{"secret_rotation_days", settings.Writer{
			Command: "fugaro secrets set",
			Argv:    []string{"secrets", "set", "--rotation-days", secretsValuePlaceholder},
			Store:   settings.StoreLocal,
		}},
		{"bitbucket_api_roots", settings.Writer{
			Command: "fugaro secrets set",
			Argv:    []string{"secrets", "set", "--bitbucket-api-root", secretsValuePlaceholder},
			Store:   settings.StoreLocal,
		}},
	} {
		// A duplicate pattern or an out-of-vocabulary writer is a programming
		// error and not a user's mistake: the registry refuses it, and the
		// binary has to fail loudly rather than have one of two registrants
		// silently win (docs/contracts/config-ownership.md §2).
		if err := settings.RegisterWriter(w.pattern, w.writer); err != nil {
			panic(err)
		}
	}
}
