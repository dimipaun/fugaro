package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro orchestrator (docs/stateless-orchestrator, D1223): read the state of
// the project's orchestrator and write the typed patch a person or a session
// may write. The state lives in the project's Firebase Realtime Database under
// orchestrator/<id>/{state,lease,audit} (D1220, O2's rules), and every read and
// write here carries the person's own credential, so the database's IAM roles
// decide what a run may do (D1241).
//
// The orchestrator id is the selected project's name: a machine keeps one
// orchestrator per project it manages (docs/cli-output-console-graph, D1358),
// and no command takes an id of its own, so the node a run reads is the node
// its project's daemon writes. The daemon's two other writers — the launch loop
// (O6, O8) and the session-start cache — are not this command family's.

// orchestratorOutputVersion is the version of the documents this family prints
// with --json (schemas/orchestrator-output.schema.json). A field never changes
// meaning across it: a new one is additive, and a renamed one is a new version.
const orchestratorOutputVersion = 1

// openOrchestrator selects the project and returns its config, the state node
// of its orchestrator and the database client.
//
// The node is built without a lease: the CLI is the person's own credential
// and holds no daemon's claim (D1241), so the one write it makes is the
// conditional write its ETag bounds and nothing else fences it. The client is
// openBudget's — the same database, and the same check that it is this
// installation's — because the orchestrator's three nodes live in the budget
// database, and a write into somebody else's installation is what that check
// is for.
func openOrchestrator(ctx context.Context, o cloudOptions) (*localcfg.Config, *orchestrator.Node, *rtdb.Client, error) {
	lc, db, err := openBudget(ctx, o)
	if err != nil {
		return nil, nil, nil, err
	}
	node, err := orchestratorNodeFor(lc, db)
	if err != nil {
		return nil, nil, nil, err
	}
	return lc, node, db, nil
}

// orchestratorNodeFor builds the state node of the project's orchestrator.
func orchestratorNodeFor(lc *localcfg.Config, db orchestrator.CAS) (*orchestrator.Node, error) {
	id, err := orchestratorID(lc)
	if err != nil {
		return nil, err
	}
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{DB: db, AgentID: id, Role: orchestrator.RoleOrchestrator})
	if err != nil {
		return nil, userErr("%v", err)
	}
	return node, nil
}

// orchestratorID is the orchestrator a run acts on: the selected project's
// name. It is a database key as well as a name (O3), so it is checked with the
// rule the node's paths are built by, before any request is sent, rather than
// refused by the transport.
func orchestratorID(lc *localcfg.Config) (string, error) {
	if _, err := orchestrator.PathState(lc.Name); err != nil {
		return "", userErr("%v: the orchestrator id is the project's name, and project %s cannot name one", err, safetext.Field(lc.Name))
	}
	return lc.Name, nil
}

// selectOrchestratorProject selects the project without reaching for a
// database: the offline print needs the project's name alone, and the shared
// config is never fetched here (a print that ran at every session start must
// not read a bucket, D1243).
func selectOrchestratorProject(ctx context.Context, o cloudOptions) (*localcfg.Config, error) {
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil, err
	}
	sel, lc, err := selectFrom(o, co, false)
	if err != nil {
		return nil, err
	}
	if _, err := orchestratorID(lc); err != nil {
		return nil, err
	}
	if err := announce(o, sel, lc); err != nil {
		return nil, err
	}
	return lc, nil
}

func newOrchestratorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orchestrator",
		Short: "Read and patch the project's orchestrator state",
		Long: "Read the project's orchestrator state, its audit log, and write the typed\n" +
			"patch a person or a session may write. The state is the document the\n" +
			"project's daemon keeps in its Firebase Realtime Database\n" +
			"(orchestrator/<project>/{state,lease,audit}), and this command acts as\n" +
			"the person the Application Default Credentials belong to, so the\n" +
			"database's own roles decide what it may read and write.\n\n" +
			"state prints the generated summary of the live node; --brief prints only\n" +
			"that summary, and --brief --offline prints the brief the daemon cached at\n" +
			"session start instead, reaching no network at all. events reads the audit\n" +
			"log. patch applies one document from -f, and the ops only a person may\n" +
			"land (approving a work item, setting the goal, closing a decision that\n" +
			"waits on a person) are confirmed at a real terminal with the project's\n" +
			"name typed, never by --yes: a session that has none writes nothing.",
	}
	cmd.AddCommand(newOrchestratorStateCmd(), newOrchestratorEventsCmd(), newOrchestratorPatchCmd())
	return cmd
}
