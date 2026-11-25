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
// The orchestrator id is the selected project's name, or the project's name and
// a `~` and a name for one of the project's named orchestrators (C-ORCH's
// `<project>~<name>`, D3207): a machine keeps one orchestrator per project it
// manages as the default (docs/cli-output-console-graph, D1358) and any number
// of named ones beside it, each with its own node, its own daemon and its own
// lease (D3206). A command names one with `--name`, and no command takes a whole
// id of its own: the project half is the selection every command in this CLI
// shares, so an id can never name a project the session is not in. The daemon's
// two other writers — the launch loop (O6, O8) and the session-start cache — are
// not this command family's.

// orchestratorOutputVersion is the version of the documents this family prints
// with --json (schemas/orchestrator-output.schema.json). A field never changes
// meaning across it: a new one is additive, and a renamed one is a new version.
const orchestratorOutputVersion = 1

// orchestratorTarget is what one command of this family acts through: the
// selected project, the orchestrator id `--name` resolved to, and the id's node
// and database.
//
// The id is carried here rather than left for the caller to resolve again
// because it is one answer to "which orchestrator is this command about": it is
// what the command prints, what its node's paths were built from and what a
// refusal names (D3206, D3207). A caller that rebuilt it would be a second
// place the flag is read, and the two could come to disagree.
type orchestratorTarget struct {
	lc   *localcfg.Config
	id   string
	node *orchestrator.Node
	db   *rtdb.Client
}

// openOrchestrator selects the project and returns the state node of the
// orchestrator name names: the project's own for "", `<project>~<name>` for a
// name (D3206, D3207).
//
// The node is built without a lease: the CLI is the person's own credential
// and holds no daemon's claim (D1241), so the one write it makes is the
// conditional write its ETag bounds and nothing else fences it. The client is
// openBudget's — the same database, and the same check that it is this
// installation's — because the orchestrator's three nodes live in the budget
// database, and a write into somebody else's installation is what that check
// is for.
func openOrchestrator(ctx context.Context, o cloudOptions, name string) (*orchestratorTarget, error) {
	return openOrchestratorShard(ctx, o, orchestrator.ShardState, name)
}

// openOrchestratorShard is openOrchestrator for one of the nodes beside the
// state node: the standing backlog's (D2715) or an imported plan's (D2735).
// Which node the caller gets is the shard and nothing else — the id, the role,
// the lease and the ownership check are the same for all three — because a
// second opener would be a second place for one of those four to differ.
func openOrchestratorShard(ctx context.Context, o cloudOptions, shard orchestrator.Shard, name string) (*orchestratorTarget, error) {
	lc, db, err := openBudget(ctx, o)
	if err != nil {
		return nil, err
	}
	id, err := orchestratorIDFor(lc, name)
	if err != nil {
		return nil, err
	}
	node, err := orchestratorNodeShard(db, id, shard)
	if err != nil {
		return nil, err
	}
	return &orchestratorTarget{lc: lc, id: id, node: node, db: db}, nil
}

// orchestratorNodeShard builds one node of one orchestrator id: the state node
// (ShardState, the zero shard), the standing backlog's (ShardBacklog) or one
// imported plan's (PlanShard).
//
// The id is the caller's, resolved the one way every command resolves it
// (orchestratorIDFor), so that the id a command prints and the id its node is
// built from are one resolution rather than two.
func orchestratorNodeShard(db orchestrator.CAS, id string, shard orchestrator.Shard) (*orchestrator.Node, error) {
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{DB: db, AgentID: id, Role: orchestrator.RoleOrchestrator, Shard: shard})
	if err != nil {
		return nil, userErr("%v", err)
	}
	return node, nil
}

// orchestratorID is the orchestrator a command acts on when it was given no
// --name: the selected project's own orchestrator, whose id is the project's
// name.
func orchestratorID(lc *localcfg.Config) (string, error) { return orchestratorIDFor(lc, "") }

// orchestratorIDFor is the orchestrator a command acts on: the selected
// project's own when name is empty, and one of its named orchestrators —
// `<project>~<name>` (D3207) — when --name was given. The project half comes
// from the selection and never from the flag, so a command can act on a named
// orchestrator of the project it is in and on no other.
//
// The id is built by the two rules that own its halves — the project half is
// config.ProjectNameRE and the name half is D3207's name rule, both inside
// NamedOrchestratorID — and is then checked as the database key it is with the
// rule the node's paths are built by, orchestrator.PathState, before any request
// is sent rather than left to the transport to refuse. That last check is why a
// name can be well-formed and still refused: this release's paths are built with
// the work-item rule, which admits the project's own id alone, and the task that
// widens it to the orchestrator rule (CG1a) is what makes the check pass for a
// named id. Until then the two refusals are the two cases — the project that
// cannot name an id, and the name this release cannot address — and each says
// which rule said no rather than leaving a command to fail at the database.
func orchestratorIDFor(lc *localcfg.Config, name string) (string, error) {
	id := lc.Name
	if name != "" {
		named, err := orchestrator.NamedOrchestratorID(lc.Name, name)
		if err != nil {
			return "", userErr("%v", err)
		}
		id = named
	}
	if _, err := orchestrator.PathState(id); err != nil {
		if name == "" {
			return "", userErr("%v: the orchestrator id is the project's name, and project %s cannot name one", err, safetext.Field(lc.Name))
		}
		return "", userErr("%v: `--name %s` names the orchestrator %s, and this release cannot address a named one — the paths under `orchestrator/` are built with the rule that admits a project's own id alone, so nothing is read and nothing is written for it", err, safetext.Field(name), safetext.Field(id))
	}
	return id, nil
}

// orchestratorNameFlag is the flag every command that acts on one orchestrator
// takes, and addOrchestratorNameFlag registers it.
const orchestratorNameFlag = "name"

// addOrchestratorNameFlag registers `--name`. It is one function because the
// flag means one thing wherever it appears — which of the project's
// orchestrators the command is about — and a second wording would be a second
// answer to "is this the same flag" (C-ORCH, D3206, D3207).
func addOrchestratorNameFlag(cmd *cobra.Command, name *string) {
	cmd.Flags().StringVar(name, orchestratorNameFlag, "",
		"which of the project's orchestrators to act on: `--name night` is the named orchestrator <project>~night, and no --name is the project's own")
}

// orchestratorStateCommand is the command a refusal sends a reader to for the
// revision a patch or a dump is built against, carrying the flag the run was
// given: the revision of a named orchestrator's node is one only a named read
// prints, and a reader sent to the project's own would build a patch from
// another node's numbers and have it refused for a revision nobody moved.
//
// The name is written as it was given, and not fenced or rewritten: it is a
// flag of a command the reader types back, and the resolver has already held it
// to the name rule (orchestratorIDFor) before any of these refusals is reached.
func orchestratorStateCommand(name string) string {
	if name == "" {
		return "fugaro orchestrator state"
	}
	return "fugaro orchestrator state --name " + name
}

// selectOrchestratorProject selects the project without reaching for a
// database: the offline print needs the project's name alone, and the shared
// config is never fetched here (a print that ran at every session start must
// not read a bucket, D1243). It answers with the project and the orchestrator
// id `--name` resolved to, which is what the cached brief is filed under.
func selectOrchestratorProject(ctx context.Context, o cloudOptions, name string) (*localcfg.Config, string, error) {
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil, "", err
	}
	sel, lc, err := selectFrom(o, co, false)
	if err != nil {
		return nil, "", err
	}
	id, err := orchestratorIDFor(lc, name)
	if err != nil {
		return nil, "", err
	}
	if err := announce(o, sel, lc); err != nil {
		return nil, "", err
	}
	return lc, id, nil
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
			"name typed: a session that has none writes nothing.\n" +
			"import loads one of two documents: a plan's graph, which becomes the\n" +
			"work items of the plan's own node, or the one-time session dump,\n" +
			"validated as a patch and refused the person's own ops: no dump approves\n" +
			"work, and no import approves anything.\n\n" +
			"A command acts on the project's own orchestrator unless --name names one\n" +
			"of its named ones: `--name night` is the orchestrator `<project>~night`,\n" +
			"with its own node and its own daemon, and the project half is always the\n" +
			"project this session is in.\n\n" +
			"run is the daemon itself, in the foreground. install, uninstall and\n" +
			"status are the login unit that keeps it running: the three read no\n" +
			"project and reach no network, and they write no system path and need no\n" +
			"root — the unit is one file under your own home and the service manager\n" +
			"is asked at your own login level.",
	}
	cmd.AddCommand(newOrchestratorLsCmd(), newOrchestratorStateCmd(), newOrchestratorEventsCmd(), newOrchestratorPatchCmd(),
		newOrchestratorImportCmd(), newOrchestratorRunCmd(), newOrchestratorInstallCmd(), newOrchestratorUninstallCmd(), newOrchestratorStatusCmd(),
		newOrchestratorServeCmd())
	return cmd
}
