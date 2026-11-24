// The orchestrator's own names, and the two things that hang off them: the
// reference one orchestrator makes to another's work item (D3208) and the path
// a launch's claim lives at (D3209).
//
// An orchestrator id is the project's name for the project's default
// orchestrator, and the project's name, a `~` and a slug for a named one
// (D3207, D3218). The rule is its own and is not idRE widened (F36): a work
// item's id is letters, digits, `-` and `_` capped at 64, and admitting `~` to
// it would let an item's id and an orchestrator's be the same string in one
// keyspace — which is why the store's id check is this rule and not a widened
// idRE (store.go's checkAgentID). The project half is config.ProjectNameRE
// itself, so the default id
// is exactly the project's name and no id can name a project a project cannot
// be.
//
// The graph scope grammar is this rule too (F40): `orchestrator:<id>` and
// `local:<id>` are read by the CLI's ParseScope and by the console's own
// pattern, and both are ValidScopeID, so a scope cannot name an orchestrator
// the rest of the tree refuses.

package orchestrator

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// NameSeparator joins a project's name to a named orchestrator's name:
	// `<project>~<name>`. No project name carries one (config.ProjectNameRE),
	// so the first separator is the whole parse.
	NameSeparator = "~"

	// MaxOrchestratorNameLength is the longest name a named orchestrator may
	// carry, and MaxOrchestratorIDLength the longest id the rule admits: a
	// project at config.ProjectNameRE's cap, the separator, and a name at the
	// name's cap. The id bound is the one the graph scope grammar shares (F40),
	// so the CLI and the console admit exactly the ids this package does.
	MaxOrchestratorNameLength = 40
	MaxOrchestratorIDLength   = 40 + len(NameSeparator) + MaxOrchestratorNameLength

	// ClaimsKey is the child of the orchestrator subtree that holds the
	// project's claims and ClaimsRoot is that node, `orchestrator/~claims`. No
	// orchestrator id can take the key — an id begins with a lower-case letter
	// or a digit — so a reader listing the subtree's ids (NodeIDs) never
	// mistakes the claims node for an orchestrator, and one project's claims
	// are one node however many orchestrators it has (D3209).
	ClaimsKey  = "~claims"
	ClaimsRoot = SubtreeRoot + "/" + ClaimsKey

	// DriverNode is the sibling of the state, lease and audit nodes that names
	// the orchestrator's driver, `orchestrator/<id>/driver` (D3218, F18). It is
	// a node of its own and not a field of the state or the lease, which are
	// decoded strictly and gain none.
	DriverNode = "driver"
)

// The two halves of an id. The project half is the project's own name rule
// (config.ProjectNameRE) and the name half is D3207's: 1 to 40 characters of
// a-z, 0-9 and `-`, starting with a letter or a digit. The name half admits a
// trailing `-` where a project name does not, and that asymmetry is the
// owner's — a slug may end in one, a project's name may not.
var (
	orchestratorProjectRE = config.ProjectNameRE
	orchestratorNameRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

// ParseOrchestratorID splits an id into the project it belongs to and, for a
// named orchestrator, its name; ok is false for a string the rule refuses. The
// name is "" for a project's default orchestrator.
func ParseOrchestratorID(id string) (project, name string, ok bool) {
	project, name, named := strings.Cut(id, NameSeparator)
	if !orchestratorProjectRE.MatchString(project) {
		return "", "", false
	}
	if !named {
		return project, "", true
	}
	if !orchestratorNameRE.MatchString(name) {
		return "", "", false
	}
	return project, name, true
}

// ValidOrchestratorID reports whether id is one an orchestrator node may be
// keyed by.
func ValidOrchestratorID(id string) bool {
	_, _, ok := ParseOrchestratorID(id)
	return ok
}

// ValidScopeID reports whether id is one a graph scope may carry: the id half
// of `orchestrator:<id>` and `local:<id>` (F40). It is the orchestrator id rule
// and nothing else — one rule, so the command that reads a named orchestrator
// and the console that asks for one cannot admit different ids.
func ValidScopeID(id string) bool { return ValidOrchestratorID(id) }

// DefaultOrchestratorID is a project's default orchestrator: the project's own
// name.
func DefaultOrchestratorID(project string) (string, error) {
	if !orchestratorProjectRE.MatchString(project) {
		return "", fmt.Errorf("%w: %q is not a project name, so it names no orchestrator", ErrInvalid, safetext.OneLine(project))
	}
	return project, nil
}

// ValidOrchestratorName reports whether name is the name half of an
// orchestrator id (D3207) — the rule NamedOrchestratorID applies to its second
// argument. It is exported for the one caller that has no project to build an
// id from: `fugaro orchestrator install`'s --name, which reads no project at all
// and turns the name into a login unit's own file name, so a name the rule
// refuses has to be refused before it becomes one.
func ValidOrchestratorName(name string) bool { return orchestratorNameRE.MatchString(name) }

// NamedIDsAreAddressable reports whether this release's node paths take a named
// orchestrator's id at all — whether `orchestrator/<project>~<name>` and the
// nodes under it can be built. Every path in this package is built through one
// rule (checkAgentID, store.go), so this is one fact about the build and not one
// per project: the rule reads the whole id as a name, and an id it refuses it
// refuses whatever project and name the halves are.
//
// It exists for the one caller that acts on an orchestrator without reading a
// project. `fugaro orchestrator install --name` reads none — a unit carries
// nothing from a project and the daemon reads the project half itself — so it
// cannot ask PathState about the id it is about to write a unit for, and a
// daemon given an id its own release cannot address is not a refusal a person
// sees: the unit exits at once and the service manager restarts it at every
// beat. The task that widens the paths (CG1a) makes this answer true, and the
// check the caller makes with it becomes the cheap guard it reads as.
func NamedIDsAreAddressable() bool {
	_, err := PathState(namedIDProbe)
	return err == nil
}

// namedIDProbe is one well-formed id of the shape `--name` builds: a project's
// own name and a name half, each a value its own rule admits, written out so
// that a refusal can only be the separator's and not a half's.
const namedIDProbe = "probe~probe"

// NamedOrchestratorID is one named orchestrator of a project:
// `<project>~<name>`.
func NamedOrchestratorID(project, name string) (string, error) {
	if !orchestratorProjectRE.MatchString(project) {
		return "", fmt.Errorf("%w: %q is not a project name, so it names no orchestrator", ErrInvalid, safetext.OneLine(project))
	}
	if !orchestratorNameRE.MatchString(name) {
		return "", fmt.Errorf("%w: %q is not an orchestrator name: a name is 1 to %d characters of a-z, 0-9 and -, starting with a letter or a digit",
			ErrInvalid, safetext.OneLine(name), MaxOrchestratorNameLength)
	}
	return project + NameSeparator + name, nil
}

// Driver is the document at `orchestrator/<id>/driver`: who is driving the
// orchestrator — the principal of the credential that holds the lease. It is
// written by whoever holds the lease and read by the listing, the map and the
// console, and it is a node of its own because the state and the lease are
// decoded strictly and gain no field (D3218 as amended, F35, F18).
type Driver struct {
	// Principal is the credential's principal, in the address spelling the
	// rest of the tree uses for a person (localcfg's Me): a person's account or
	// a service account's address.
	Principal string `json:"principal"`
	// At is the server time the holder wrote the node (rtdb.ServerNow), so a
	// reader can tell a driver written before a takeover from one written after
	// it without reading the lease beside it.
	At time.Time `json:"at"`
}

// DecodeDriver reads the driver node. Strict, like the state and the lease
// beside it: a node this release does not model is refused rather than read as
// a driver of nobody, and so is a node naming no principal — the two readings
// that would let a screen show "driven by nobody" for a node it cannot read.
func DecodeDriver(raw []byte) (Driver, error) {
	d, err := decodeStrict[Driver](raw)
	if err != nil {
		return Driver{}, err
	}
	if d.Principal == "" {
		return Driver{}, fmt.Errorf("%w: the driver node names no principal", ErrInvalid)
	}
	return d, nil
}

// PathDriver is the driver node's path, `orchestrator/<id>/driver`.
func PathDriver(agentID string) (string, error) { return pathOf(agentID, DriverNode) }

// ItemRef is one work item of an orchestrator, named from outside it:
// `<orchestrator>/<item>` (D3208). The orchestrator half is an orchestrator id
// and the item half is the item's own identity — a work item id, or the
// `<plan>/<task>` a plan item is keyed by — so the reference names the item a
// launch of that orchestrator would, and the resolver looks for it in that
// orchestrator's shards.
type ItemRef struct {
	Orchestrator string
	Item         string
}

// String is the reference as it is written, `<orchestrator>/<item>`.
func (r ItemRef) String() string { return r.Orchestrator + "/" + r.Item }

// ParseItemRef reads a `<orchestrator>/<item>` reference. The split is at the
// first separator, an orchestrator id carrying none, so everything after it is
// the item — the `<plan>/<task>` of a plan item included.
func ParseItemRef(ref string) (ItemRef, error) {
	orchestrator, item, ok := strings.Cut(ref, "/")
	if !ok {
		return ItemRef{}, fmt.Errorf("%w: %q names no item: a reference is <orchestrator>/<item>", ErrInvalid, safetext.OneLine(ref))
	}
	if !ValidOrchestratorID(orchestrator) {
		return ItemRef{}, fmt.Errorf("%w: %q is not an orchestrator id", ErrInvalid, safetext.OneLine(orchestrator))
	}
	if !validItem(item) {
		return ItemRef{}, fmt.Errorf("%w: %q is not a work item id or a plan task", ErrInvalid, safetext.OneLine(item))
	}
	return ItemRef{Orchestrator: orchestrator, Item: item}, nil
}

// validItem reports whether item is the identity a reference or a claim may
// name: a work item id (idRE, the shape a state document's own items carry) or
// the `<plan>/<task>` a plan item is keyed by (task.PlanTaskProblem). The two
// things that name an item across orchestrators — a reference and a claim —
// share this one rule, so an item one of them admits is an item the other does.
func validItem(item string) bool {
	return idRE.MatchString(item) || task.PlanTaskProblem(item) == ""
}

// ClaimPath is the path of one item's claim, under the node of the subtree no
// orchestrator id can take (`orchestrator/~claims`). The two kinds of item are
// keyed differently, because their identities are not the same kind of thing:
//
//   - a **plan task** is claimed under itself, `~claims/<plan task>`, because
//     its identity is the plan's folder and the task — and the folder is the
//     one the import read (D2704), so two orchestrators of one project that
//     imported the same plan mean the same task when they name it. That is what
//     makes the claim project-wide (D3209): the ETag decides which of them
//     launches the task, and the others skip it and may depend on it.
//   - a **work item id** is claimed under its orchestrator,
//     `~claims/<orchestrator>/<item>`, because a work item id is minted inside
//     one node: a backlog node mints `<day>-<n>` off its own work list (D26),
//     so the same id in two orchestrators is two different items. Keying it
//     project-wide would let one orchestrator's backlog item block another's,
//     and the two would then be one item to every reader of the claims.
//
// The item half is escaped the way every key of this tree is (budget.Key), so a
// plan task's `/` and `.` reach the database as one key rather than as a path;
// an orchestrator id needs no escaping and none is left to chance. Neither
// shape can take the other's key: a plan task's key always carries an escaped
// `/` and an orchestrator id never carries a `%`.
func ClaimPath(ref ItemRef) (string, error) {
	if !ValidOrchestratorID(ref.Orchestrator) {
		return "", fmt.Errorf("%w: %q is not an orchestrator id", ErrInvalid, safetext.OneLine(ref.Orchestrator))
	}
	if !validItem(ref.Item) {
		return "", fmt.Errorf("%w: %q is not a work item id or a plan task, so it names no item to claim", ErrInvalid, safetext.OneLine(ref.Item))
	}
	if task.PlanTaskProblem(ref.Item) == "" {
		return ClaimsRoot + "/" + budget.Key(ref.Item), nil
	}
	return ClaimsRoot + "/" + budget.Key(ref.Orchestrator) + "/" + budget.Key(ref.Item), nil
}
