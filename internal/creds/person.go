package creds

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dimipaun/fugaro/internal/config"
)

// This file is C1 of docs/workspace-credentials/implementation-plan.md: the names and the two wire
// shapes a person's own workspace token travels under, and nothing that computes. Its document is
// docs/workspace-credentials/contract-person.md, which states the same facts for a reader who is not
// reading Go; a change to a name or a shape here is a contract change
// (docs/contract-first-decoupling/spec.md), raised before a side is written against it.
//
// **What it is for.** A person's Claude token is stored once at the workspace and served to the runs
// that person launched (the spec's §3 item 2, D2960, D2962, A1). Four tasks build that half, and all
// four need the same handful of names before any of them can be written: WC7 (internal/cli) asks the
// workspace's service for a grant at a launch and hands the job the slip's id; WC8
// (internal/gateway, internal/runner) claims that slip and sends it to the workspace's service alone;
// WC5 (internal/credsvc) reads the switch and offers the person's own token as the first candidate;
// WC11 (internal/cli) reports the switch in `fugaro doctor`. WC4 (internal/credsvc) and WC6
// (internal/projectpolicy) build the other side of these names: the grant route that answers them and
// the published policy that carries the switch. The stub the sides were written against
// (person_stub.go) is gone: both sides are wired, and nothing in this package implements the switch.
//
// **Why the names are here and not in the service.** internal/credsvc is the service and
// internal/gateway is the run's proxy, and the gateway cannot import the service's wire constants for
// the reason F26 of docs/keys-through-service states for the provider route: the service imports the
// gateway (its provider route forwards with the gateway's helpers and its `Route`), so the dependency
// cannot run the other way. It imports no constant of this package either — internal/creds imports
// internal/budget, which imports the gateway, so that too is a cycle. What the sides of this wire do
// share is this package — internal/credsvc, internal/cli and internal/runner all import it — so the
// names a job's environment and a call's headers are spelled with live here, once, and those three
// sides respell none of them. **The gateway is the one side that will respell them, and that mirror
// is WC8's to write.** A call WC8 builds to the workspace's service carries the slip that service
// granted, and the claim secret after the claim (R4), and internal/gateway can spell neither through
// this package — so WC8 declares these names a second time under internal/gateway, the shape
// internal/gateway/providerwire.go already has for the provider route (F26) and
// internal/gateway/service.go for the model wire, and holds the two spellings to each other name by
// name with an external test (package gateway_test, which may import both). Until WC8 lands, this
// package is their only declaration. R18 is not a way out of it: the slip that rule leaves off a call
// is a *member's*, whose claim secret would be handed to a service outside the member (F13 of
// docs/workspace-credentials/reconciliation.md), and the slip the workspace's service grants is the
// workspace's own secret — the model wire's fields in internal/gateway/gateway.go say the same of
// their own.
//
// **What it never carries.** No credential value, no claim secret and no person's identity: the
// grant's answer is a slip's id or the fact that no token is held, the claim secret is the claim
// route's alone (F14), and every field here is a name or a bound. The one entry that reaches a job is
// the slip's id, which is not a secret (creds.Slip and credsvc's HeaderSlip both say why: it is
// bound to the run and admitted by the claim secret).

// PersonSwitchKey is the key of R9's switch: the one place a member turns the personal half of the
// workspace's credentials on.
//
// **Its home is the top level of the member's published policy** (`projectpolicy.Policy`, the node
// `config/policy/shared` of the member's own project), which is what U8 asked C1 to settle. The
// workspace's service reads it there — the only enforcer, R9 — and on the member's own machine the
// launcher and `fugaro doctor` read the member's own copy of it to decide whether a launch asks for
// a grant at all (WC7, WC11). It is a *local* key the node is published from, exactly the shape
// `triage` and `review_against_task` have (their rows in internal/settings): the member's admin sets
// it with `fugaro settings set` — a boolean in the machine's own config is the generic path's, as
// those rows say — and the member's `fugaro update` carries it to the node. U8's other candidate, a
// plain field of the member's local config that no settings row and no publish touches, is not the
// answer: the workspace's service is the only enforcer and reads the member's published policy, never
// anything on the member's disk, so a key with no road to the node would be a switch nothing could
// obey. WC6 builds this side, and it adds the key's carrier to `internal/localcfg/localcfg.go` —
// a `PersonalWorkspaceTokens *bool` field and its keep rule, beside `Triage` and
// `ReviewAgainstTask` — because that is what the road above is: the file decodes strictly, `settings
// set` writes and re-parses it, and the publish reads it, so a settings row whose key is publishable
// is a field of that file. What U8 refused is the alternative (a field no settings row and no
// publish touches), not the file; the switch's home, reader and enforcer do not move.
//
// **Absent reads as off**, and that is the whole of the default: a member that never set the key
// serves no personal token (D2972), and the key's own value is a boolean. The policy's version does
// not move for it, and an older reader that lists it as an unknown top-level key decodes past it and
// reads every other member (R9). That is the decode's half of the skew alone: the readers that
// decide something by the node — the CLI's launch gate among them — refuse one carrying a key they
// do not know and name the upgrade, so the member's CLI is upgraded before the `fugaro update` that
// publishes the switch, the skew `bitbucket_tokens` had (docs/releases/v0.25.0.md).
const PersonSwitchKey = "personal_workspace_tokens"

// PersonTokenName is the ONE personal name this plan serves (A1): the bare, unsuffixed name of the
// default Claude account's token, which a person stores under their own prefix as
// `fugaro_u_<sub>_claude-oauth-token` (secretname.User).
//
// It is `config.ClaudeOAuthToken` and not a spelling of its own: the token a person stores is the
// token `fugaro secrets user set` and the accounts' own namespace already name, and a second
// spelling of it would be a second name for one container. What this plan adds is not the name but
// the fact that it is SERVED — under the person's own prefix, to that person's runs, at the
// workspace (R6, R7). A name in the `claude-oauth-token-<account>` namespace is NOT this name: the
// workspace's service reads one container per person, and the plan serves the default account's
// token alone.
const PersonTokenName = config.ClaudeOAuthToken

// PersonSlipEnv is the variable a job reads the workspace's slip's id from: the launcher asks the
// workspace's service for a grant before it launches (WC7), and this entry carries the id it was
// answered into the execution's environment, where the runner claims it (WC8).
//
// It is runner.SlipEnv's shape one slip over, and it is a SECOND variable rather than the same one
// because a run holds both slips at once and they are not interchangeable: FUGARO_SLIP is the
// member's own slip, whose claim secret is the member's, and this one is the workspace's, which a
// member's admins can neither forge nor claim (R3, R4). A job with neither reads neither.
//
// It is plain environment and never a secret entry, for FUGARO_SLIP's own reason: the id is not a
// credential, the claim secret is what admits a call, and the secret never reaches the environment
// at all (F14). And it is the RUNNER's variable, not the agent's: the id sits in the environment the
// runner reads and the agent's own environment is built without it, exactly as FUGARO_SLIP is
// (internal/runner's slipHold and its TestSlipNotExportedToAgentEnv).
const PersonSlipEnv = "FUGARO_WORKSPACE_SLIP"

// The two header names a workspace slip travels in. They are the names C1 exists for: a slip's id
// travels on every call a run makes under it, and the claim secret travels with it once the claim
// has answered one.
//
// **They are this contract's own names and not credsvc's HeaderSlip and HeaderSlipClaim**, and that
// is R4 stated as a spelling. A run holds two slips, and the two are served by two services that a
// member's admins control differently: the member's own service, which its admins own, and the
// workspace's, which they do not (F13). Two names make the separation structural rather than a
// routing decision — a workspace slip presented under the member's own header is a call the member's
// service reads as a slip it granted, and a member's slip presented under these names is one the
// workspace's service reads and refuses — so the gateway's two upstreams cannot be told apart by
// anything but which service the call goes to (D2982's route, R4).
const (
	// PersonSlipHeader carries the workspace slip's id: what the grant answered and the launcher
	// handed the job (PersonSlipEnv). It is read by the claim (the run's first call) and by every
	// call after it, exactly as credsvc's HeaderSlip is. It is not a secret.
	PersonSlipHeader = "X-Fugaro-Workspace-Slip"
	// PersonSlipClaimHeader carries the claim secret the workspace's claim answered, which is the
	// ONE value of this contract (F14): it is presented on every call after the claim, the workspace
	// keeps only its hash, and no line, no error and no log of either side may carry it. It is
	// credsvc's HeaderSlipClaim one service over, and its value is registered with the call's scrub
	// for the same reason.
	PersonSlipClaimHeader = "X-Fugaro-Workspace-Slip-Claim"
)

// ErrPersonGrantRefused is the one class a refused grant is answered with (R3). Every refusal of a
// workspace grant comes back as this and nothing finer: the grant route names no account, no
// repository and no person in a refusal, so that a person who holds a token learns nothing from it
// beyond the fact of the refusal, and the launcher has one thing to branch on (D3147).
//
// The service answers it on the wire as credsvc's one request-refusal class — the status
// `StatusRequestRefused` with the code `ErrorRequestRefused` — and the launcher that calls the route
// reads that answer back as this sentinel. It is told apart from the two things it is not: "no token
// held", which is an ANSWER and not a refusal (PersonGrant.Held), and a service that could not be
// reached, which is an error of the transport and never this class. D3147 gives all three one
// outcome at a launch — the run starts on the shared entries and says why — but they are three
// answers, and a launcher that folded them together could not say which happened.
var ErrPersonGrantRefused = errors.New("creds: the workspace refused the grant")

// PersonSwitchReader answers R9's switch for one member project: whether that member's published
// policy turns the personal half on. It is the seam WC6 builds the other side of, in
// internal/projectpolicy (PersonSwitchReader over the member's published node), and this package
// implements it nowhere: the readers of this wire are the sides that read a policy.
//
// It is asked by project name because the workspace's service serves many members and reads each
// member's own policy (credsvc.Workspace.Policy, reached from the verified run identity), while a
// launcher and `fugaro doctor` on a member's own machine ask about the project they are in. An
// error is a policy that could not be read, and it is never "off": WC5 refuses a call whose member's
// policy could not be read rather than serving the shared entries as if the switch were off, and
// WC7 starts the run without the personal token and says why (D3147).
type PersonSwitchReader interface {
	// PersonSwitchOn reports whether the member's policy names PersonSwitchKey with a true value.
	// Absent is off (R9), and a policy that could not be read is an error and not a false.
	PersonSwitchOn(ctx context.Context, project string) (bool, error)
}

// PersonGrantRequest is the request of a workspace grant: what a launcher asks the workspace's
// service for, at the launch of a run that is to draw on its launcher's own token (R8, D3141).
//
// Every field is a name or an id of the calling run and never a value, and every one of them is
// already the member's own grant's word for the same thing (credsvc.SlipGrantRequest): the
// repository, the run and the job account are the binding a workspace slip is held to as well (R3),
// so a caller that has built one request has the other's four spellings in hand.
//
// **It is a shape of its own and not credsvc.SlipGrantRequest**, for two reasons that are the same
// reason. The question is different: a member's grant asks for a slip over a list of the run's
// resolved access names, and this one asks whether the caller holds ONE personal token and, if they
// do, binds the slip to it (A1, R7). And the two must never be readable as each other: the strict
// decode below refuses a body carrying `credentials` or `max_runtime_seconds` and a body carrying
// none of this shape's fields, so a member's launcher that pointed its grant at the workspace's
// service is answered a refusal and not a workspace slip, which is R4 and F13 held by the wire
// rather than by the address it was sent to.
type PersonGrantRequest struct {
	// Slug is the repository the run belongs to, as the launcher's own tokens spell it
	// (task.Slug) — the same value the member's grant carries, and the one the workspace's own node
	// holds the calling job account to before it writes anything (R3).
	Slug string `json:"slug"`
	// Run is the run id the launcher minted (task.ValidRunID): the other half of the binding, and
	// what the claim of any other run of the repository is refused by.
	Run string `json:"run"`
	// Account is the job service account the run will call as: one account is dedicated to one
	// Cloud Run job (D1937), and the claim is checked against it (R3).
	Account string `json:"account"`
	// Name is the personal name the caller asks the slip to bind, which for every request this plan
	// writes is PersonTokenName. It is a field rather than an assumed constant so that a name this
	// plan does not serve is REFUSED here rather than silently answered about another container:
	// the owner of the container is the verified token's own `sub` (F11), so the request names no
	// person, and the name is the whole of what the caller chooses.
	Name string `json:"name"`
}

// PersonGrant is the answer of a workspace grant, and it has exactly two states: the caller holds a
// token of the name it asked for, and the answer is the id of the slip bound to it; or the caller
// holds none, and the run is served the shared entries exactly as before (R8, D3147).
//
// **The two states are one shape and not an error**, because "no token held" is an ordinary
// beginning of a run: a person who never stored a token launches the same way they launched before
// this plan, and a refusal here would send their launcher down the failure path for the one case
// that is not a failure. R3 makes the answer cheap as well — "no token held" is answered before the
// node is read, so only a person who really holds a token learns anything from the route at all.
//
// **It carries no claim secret** (F14). The claim secret is answered by the claim route alone, once,
// to the run the slip names; a grant that carried it would hand a credential to the launcher's
// process and to every line that process writes. The strict decode below is what makes that a
// property of the wire rather than of the service's manners: an answer carrying `claim_secret` is
// refused, as any key this contract does not name is.
type PersonGrant struct {
	// Held reports whether the caller holds a token of the name the request asked for. It is the
	// whole of the difference between this plan's two beginnings: false is no slip and the shared
	// entries, and it is answered before the workspace's own node is read (R3).
	Held bool `json:"held"`
	// Slip is the workspace slip's id, present exactly when Held. The launcher hands it to the job
	// as PersonSlipEnv, holds it to its own bound for an environment entry exactly as it holds the
	// member's slip's id (internal/cli's slipIDUsable), and the run claims it at the workspace's
	// service for the claim secret it will present on every call after (PersonSlipClaimHeader).
	Slip string `json:"slip,omitempty"`
}

// ParsePersonGrantRequest reads a grant's request body, strictly: a body that is not one JSON object
// is refused before it is read at all, a key this contract does not name is refused rather than
// decoded past, and a body missing a field of the shape is refused rather than read as a zero value.
// The caller supplies the bound on the body's size — the service's own
// route reads a bounded body before this (credsvc's maxMintRequestBytes) — and everything the
// request's FIELDS must be beyond their presence (a slug of task.Slug's shape, an account of the
// job's) is the route's, which holds them to the same rules the member's grant's fields are held to.
func ParsePersonGrantRequest(raw []byte) (PersonGrantRequest, error) {
	const what = "the workspace grant's request is not one this contract reads"
	req, err := decodePersonShape[PersonGrantRequest](raw)
	if err != nil {
		return PersonGrantRequest{}, fmt.Errorf("%s: %w", what, err)
	}
	for _, f := range []struct {
		field string
		value string
	}{
		{"slug", req.Slug},
		{"run", req.Run},
		{"account", req.Account},
		{"name", req.Name},
	} {
		if f.value == "" {
			return PersonGrantRequest{}, fmt.Errorf("%s: the call names no %s", what, f.field)
		}
	}
	if req.Name != PersonTokenName {
		// The refusal names the field and never its value: a name is a caller's text, and the rule
		// is the same one credsvc's field checks keep. It is one name this plan serves, so a request
		// that names another is a request about a container this contract does not answer about.
		return PersonGrantRequest{}, fmt.Errorf("%s: the call names a personal token this plan does not serve", what)
	}
	return req, nil
}

// ParsePersonGrant reads a grant's answer, strictly: a body that is not one JSON object is refused
// before it is read at all, a key this contract does not name is refused rather than decoded past,
// and an answer whose two fields disagree is refused rather than read as whichever of the two the
// caller looks at first.
//
// A body that spells `held:false` and a body that spells neither field are ONE answer — no token
// held, the state a run starts on as before — because there is no third state for a missing key to
// fall into and the zero of a boolean a caller branches on as "not granted" is the benign reading.
// A body that names a slip and does not say it holds a token is the disagreement the check refuses.
// This is the only zero value this contract reads, and the request's fields are all refused when
// absent for the reason it is safe here and not there: a request that named no repository would ask
// about nothing, and its answer would be a grant this package could not hold anyone to.
//
// The slip's id is NOT held to a bound here. It becomes one environment entry of a job, and the
// launcher that puts it there is where that is checked (internal/cli's slipIDUsable, the member's
// slip's own rule): a launcher's bound is its own, and a value it will not carry must not be one
// this package refused on its behalf.
func ParsePersonGrant(raw []byte) (PersonGrant, error) {
	const what = "the workspace grant's answer is not one this contract reads"
	g, err := decodePersonShape[PersonGrant](raw)
	if err != nil {
		return PersonGrant{}, fmt.Errorf("%s: %w", what, err)
	}
	switch {
	case g.Held && g.Slip == "":
		return PersonGrant{}, fmt.Errorf("%s: the answer says the caller holds a token and names no slip", what)
	case !g.Held && g.Slip != "":
		return PersonGrant{}, fmt.Errorf("%s: the answer names a slip and says the caller holds no token", what)
	}
	return g, nil
}

// decodePersonShape decodes one body of this contract's shapes, refusing any body that is not one
// JSON object, any key it does not name, and any byte after the JSON value.
//
// It is not the package's decodeStrict, which reads a NODE this package owns: the two agree on
// refusing an unknown member, and they differ where the medium differs. A wire body is one value
// and nothing else, so trailing bytes are refused here and not there — a second JSON value after a
// grant is a body no reader of this contract wrote, and a reader that stopped at the first value
// would take it. And the refusal is worded for a call rather than for a node, because an operator
// reads it from a launcher's own line.
func decodePersonShape[T any](raw []byte) (T, error) {
	var v T
	// The body must be one JSON OBJECT, and it is checked before it is decoded: `null` decodes
	// into a Go value of any shape without an error, so without this a body the contract never
	// writes would be read as the shape's zero value — for a grant's answer, "no token held",
	// the benign state, which is exactly the reading the contract's "a body that is not one JSON
	// object is refused" forbids. Every other non-object is refused by the decode below.
	first := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := first.Token(); err != nil {
		return v, err
	} else if d, ok := tok.(json.Delim); !ok || d != '{' {
		return v, errors.New("the body is not one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return v, errors.New("trailing data after the JSON value")
	}
	return v, nil
}
