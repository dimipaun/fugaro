package cli

// This file is MG6 of docs/model-credentials-routing/implementation-plan.md: the
// help texts and the CLI reference.
//
// A repository had an auth mode -- agent.auth, oauth or api-key -- and a
// credential belonged to the repository that named it. The project's routing
// table says where a model goes out instead (D3170, D3182): one entry per model,
// an ordered chain of sources, first usable one wins, and a subscription token
// is reached through a pool rather than named by a repository. So the four
// commands whose help a person reads while migrating describe the chain and the
// source it names, and none of them still speaks of an oauth or an api-key
// repository. The generated CLI reference follows from these texts
// (website/content/cli-reference.md, `go run ./ee/clidoc`), and the two that
// print a figure -- budget preview's notional note and runs stats's NOTIONAL
// column -- say the same in their own words.
//
// The rest of the tree's help still carries the vocabulary of the auth mode
// where a later task owns it: `fugaro setup`'s --ask, `fugaro update` and
// `fugaro secrets ls` speak of the credentials a repository's agent.auth names
// until X6 removes the plumbing those texts describe. This guard is the four
// commands of MG6 and no more.

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// authModeHelpPhrases are the words a help text uses when it still ties a
// credential to a repository: the retired key itself, the mode it held, and the
// two repository kinds that mode named. A text that says where a model's calls
// go out -- the chain of the project's routing table -- names none of them.
var authModeHelpPhrases = []string{
	"agent.auth",
	"auth mode",
	"oauth repository",
	"api-key repository",
	"oauth repo",
	"api-key repo",
}

// helpTextOf is the whole of what a reader gets from `--help`: the command's own
// three texts and every flag's, which is exactly what the generated reference
// renders.
func helpTextOf(cmd *cobra.Command) string {
	return strings.Join([]string{cmd.Use, cmd.Short, cmd.Long, cmd.Flags().FlagUsages()}, "\n")
}

// TestNoHelpTextNamesARepositoryAuthMode is MG6's acceptance: each of the four
// commands names where a model's calls go out, in the routing table's own words,
// and none of them names a repository's auth mode.
func TestNoHelpTextNamesARepositoryAuthMode(t *testing.T) {
	root := NewRootCmd()
	for _, tc := range []struct {
		// path is the command in the tree.
		path string
		// names is the source a model goes out on, in the words that command
		// now owes the reader.
		names string
	}{
		{"run", "chain of the run's models"},
		{"budget preview", "subscription source"},
		{"runs stats", "subscription source"},
		{"models use", "routing table"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			cmd, _, err := root.Find(strings.Fields(tc.path))
			if err != nil || cmd == nil {
				t.Fatalf("no `fugaro %s` in this CLI: %v", tc.path, err)
			}
			help := helpTextOf(cmd)
			// The phrases are read off the text with every run of whitespace
			// collapsed, so a phrase the help wraps across two lines -- "routing
			// table" at a line break, say -- is still one phrase here, and the
			// check does not turn on where the text happens to wrap.
			flat := strings.Join(strings.Fields(help), " ")
			for _, phrase := range authModeHelpPhrases {
				if strings.Contains(flat, phrase) {
					t.Errorf("`fugaro %s`'s help still names a repository's auth mode (%q): it says where a model's calls go out -- the chain of the project's routing table -- or it says nothing:\n%s", tc.path, phrase, help)
				}
			}
			if !strings.Contains(flat, tc.names) {
				t.Errorf("`fugaro %s`'s help names no source a model goes out on (no %q):\n%s", tc.path, tc.names, help)
			}
		})
	}
}
