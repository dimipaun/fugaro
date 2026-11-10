package cli

import (
	"context"
	"errors"
	"os"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The managed gate and the coding agent's refusal as `fugaro image map set`
// reads them (D1011, D1012).
//
// Why the map write is a session's to be refused and never to be admitted.
// The rule behind the gate's command set is one sentence (managed spec §3.2):
// a command is in the set only when its whole effect is visible in a document
// the command produces before it writes. This command's document is the map —
// the bytes of the local project config it is about to write — and what the map
// decides is which image each job of a repository runs. The entry it names is
// not that document: the entry's own definition (the Dockerfile, the repository
// that file is read from, the base it is built on) belongs to the catalog,
// which a session writes with `fugaro image add` and which no gate judges. A
// session that wrote both would be choosing where the project's code is built
// from, one ordinary-looking step at a time, and the map is the step where the
// two meet — so the map is the owner's own write, at their own terminal, and
// what a session gets is the refusal below rather than an admission.
//
// Why a refusal, and not the absence of a row. A command outside the set keeps
// the refusal this program has always given a session (imageMapSession), and
// that is what a session reads in a project this program cannot prove is
// managed. In a managed project the gate is read, the map is judged, and the
// refusal is recorded: an owner auditing a managed project reads that a session
// tried to move where their repositories' images come from, and which rules
// refused it (D1014, D1025). An absent row would leave neither.
//
// Why no budget database and no ownership question. `fugaro image map set`
// writes this machine's project config and nothing else — the publish is
// fugaro update's — so there is no database to ask D1017's question about, and
// the gate is read the way the settings row reads it (settingsWriteGate): the
// two marks, through the installation's own Storage and Resource Manager
// endpoints.

// imageMapGate reads the managed gate for one map write and judges it: the two
// marks, per command and never cached (D1015), through the helper every command
// in the set uses, so the verdict and the audit record are the same calls.
//
// It is called only in a coding agent's session, with the marker the caller
// already read: the write asks no confirmation of anyone, so at a person's own
// terminal the gate has no prompt to stand in for and that path reads no mark
// and touches no cloud.
//
// It answers nil when no gate spoke — a project that is not managed, a client
// that could not be built, a mark that did not read, a second read that did not
// answer — and the caller then refuses the session in this command's own words,
// which is the refusal it has always given. Where the gate does answer, the
// verdict is a refusal by construction (imageMapRules), and the answer is the
// gate's own sentence with the record of it written before this returns: a
// nil error here means the gate admitted a document these rules cannot admit,
// and the caller's own refusal is where that falls.
func imageMapGate(ctx context.Context, lc *localcfg.Config, doc []byte, marker string) error {
	// A no_auth installation that names no Storage or Resource Manager endpoint
	// has no cloud to read the marks from at all, and no call is attempted.
	if lc.Endpoints.NoAuth && (lc.Endpoints.Storage == "" || lc.Endpoints.ResourceManager == "") {
		return nil
	}
	s, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return nil
	}
	c, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		return nil
	}
	g, err := readManagedGate(ctx, managedGateEnv{lc: lc, s: s, crm: c}, managedGateFacts{
		Subcommand:  "image map set",
		Argv:        os.Args[1:],
		Declined:    "nothing was written",
		AgentMarker: marker,
		// The whole-project inventory (D59) is `fugaro managed apply`'s read and
		// is not consulted here: a command that has none passes false.
		InventoryFallback: false,
	})
	if err != nil || !g.managed {
		return nil
	}
	if _, err := g.admit(ctx, initflow.YesOrTyped, managedmode.ConvergeRefused(doc, initflow.YesOrTyped, imageMapRules())); errors.Is(err, managedmode.ErrNotConverged) {
		// The gate's own sentence and the rules that refused, unwrapped: the
		// session reads why the write stopped, the record of the refusal was
		// written by admit before it returned, and the exit class is the user's
		// — a session asked for a step the project's gate does not admit — and
		// not a failure of this program.
		return &ExitError{Code: ExitUserError, Err: err}
	}
	return nil
}

// imageMapRules is the rules the gate judges a session's map write by: one
// rule, which refuses it, in the words the refusal and the record carry.
func imageMapRules() []string {
	return []string{"the map decides which image every job of the repository runs, and the catalog entry it names is another document: the entry's own definition — the Dockerfile it is built from, the repository that file lives in — is what says where this repository's code is built, and it is not among the bytes judged here, so the project's owner writes the map at their own terminal"}
}

// imageMapSession is this command's own refusal in a coding agent's session:
// what a session reads when no gate spoke at all — a project this program could
// not prove is managed, a mark read that did not answer, the two cases a mark
// that cannot be read is not a permission for. A managed project's gate refuses
// a session's write as a rule of this command (imageMapRules), and that
// refusal reaches the session in the gate's own words instead of this one
// (imageMapGate).
func imageMapSession(marker, repo, entry string) error {
	return userErr("nothing was written: mapping %s to the catalog entry %s decides which image every job of that repository runs, and the entry's own definition — where the repository's code is built from — is another document, which a session writes beside it: %s. Run it in your own terminal window: %s",
		pluginwire.Printable(repo), pluginwire.Printable(entry), initflow.AgentRefusal(marker), imageMapCommand(repo, entry))
}
