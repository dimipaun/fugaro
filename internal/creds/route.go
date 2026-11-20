package creds

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// C2, the wire contract of the workspace's credentials service
// (docs/workspace-service/implementation-plan.md §3; R3 and R4 of its
// reconciliation; the prose half is docs/workspace-service/contract-wire.md).
//
// **Why the run is told rather than left to work it out.** A member's run takes
// each kind of credential from one of two services — its own project's or the
// workspace's — and it decides once, at its start, from what the project
// publishes (D2982): a project that holds a usable model credential goes on
// using its own service for the whole run and waits when that credential is
// exhausted, rather than falling through to the workspace mid-run (D2989). The
// run cannot work the choice out for itself: what a member's policy admits and
// what its bundle holds are two lists only the member's own machine reads, and
// a run that guessed would either spend the workspace's money on a project that
// chose its own credential or wait on a project that holds none. So the member's
// own `fugaro update` computes the answer per repository and kind and publishes
// it, and the run reads that answer back.
//
// **The answer is per repository, because admission is** (F34). A project may
// hold a credential for one repository and none for another, and the member's
// policy admits each of them separately, so a single "this project is on the
// workspace" statement would send every repository of a project to the
// workspace the moment one of them is uncovered.
//
// **The workspace's address travels here and never in the environment** (R3,
// F32). It is a fifth field of the member's own pin, written by `fugaro
// workspace add`, and it reaches the run inside this record; it is never a job
// or launch variable, because an address a launcher supplies is an address a
// launcher can move, and the run mints an ID token for whatever address it
// reads (F30's accepted identity design).
//
// **A repository with no record is served by the project's own service**, which
// is exactly today's behaviour: the record is published by a member that is in
// a workspace and carries an address, and a member that left one publishes
// nothing.
//
// **Nothing here is a second implementation of anything.** The names below are
// the spellings other tasks build their sides from — WS3 writes the pin's
// address field, WS6 deploys the service under this name and sets this mode
// name, WS9 publishes the record, WS11 reads it in the run, WS14 reports it,
// WS20's checks name it and WS22 admits a run's token to its path — and a
// spelling that lived in two files would be a contract two sides could drift
// apart on. What this file does *not* hold is any of their behaviour: it holds
// the record's shape, the two functions that read and write it, and a stub.

// The environment name that selects workspace mode in `fugaro credentials
// serve` (R2): the process is exactly today's without it, and with it it serves
// one workspace's members through the same routes. Its value names the
// workspace being served — the `workspace_credentials/<name>` node the process
// reads its member table from (R13) — so one name selects the mode and says
// which workspace's table is the answer.
//
// It is a name and not a flag for the module's reason: the service is a Cloud
// Run revision whose whole configuration is its environment, and the two
// services of that project differ in this one entry and not in their image.
const WorkspaceModeEnv = "FUGARO_WORKSPACE_MODE"

// WorkspaceServiceName is the workspace's own credentials service (D2980): the
// second Cloud Run service in the workspace's project, the same image as the
// installation's, running as the existing workspace account. The workspace's
// project is an ordinary installation, so its own service is
// internal/infra's CredentialsServiceName (F6) and this one is the workspace
// half of the pair; the two are told apart by their identity and their
// admission, never by their code.
//
// The name is a fact of the configuration rather than of the apply — a Cloud
// Run name is a force-new attribute, and `fugaro doctor` finds the service by
// it from a machine that planned nothing.
const WorkspaceServiceName = "fugaro-workspace-credentials"

// WorkspacePinAddressField is the field of the member's own workspace pin that
// carries the workspace's service address (R3): written by `fugaro workspace
// add`, read by the member's `fugaro update` to build the record below, and
// never read from anything the workspace publishes — a workspace that could
// name the address its members call would be a workspace that could move every
// member's calls.
//
// A pin without it is valid and means "this member takes no workspace
// credential", so a pin an earlier release wrote converges with no edit, and
// the address must be https with no query or fragment (WS3's own validation of
// the pin).
const WorkspacePinAddressField = "address"

// WorkspaceBeatSetting is the setting that turns the run's keep-warm beat off
// (R10, D2985): the run calls the workspace's keep-warm route at the slip
// beat's interval so a scaled-to-zero service is warm when the run needs it.
// On is the default — the beat's absence would be a cold start inside a paid
// call — and the owner asked for the switch because the beat may yet prove
// unnecessary at a real deployment's shape (Check 772).
//
// The key is an `agent.` one because the beat is the run's: it is started where
// the slip beat is started, it can be turned off where a run's own behaviour is
// configured, and it is not a property of the workspace or of the service.
const WorkspaceBeatSetting = "agent.workspace_beat"

// RouteVersion is the version of the credential-route record this Fugaro writes
// and reads. A reader refuses a version it does not know rather than reading
// what it recognises (the policy node's rule, P8): the run would otherwise send
// a member's credential to a service chosen by a document it half-read.
//
// A field added to the record is an addition a later version carries, and a
// version this code does not read is refused by name, before the strict decode
// below can refuse a new field in the voice of an unknown one.
const RouteVersion = 1

// Kind is one kind of credential a route decides about (R4, D2984): the model
// credentials a run's calls go out on, the Bitbucket credential a repository's
// git traffic uses, and the GitHub App the run's tokens are minted with. The
// route is by kind because a run's model call names no credential at all — the
// choice of service is the only place the kind is visible — and D2984 serves
// every kind at once rather than models first.
type Kind string

const (
	KindModel     Kind = "model"
	KindBitbucket Kind = "bitbucket"
	KindGitHub    Kind = "github"
)

// Kinds is the closed vocabulary, in the order the record lists it. The two
// forge kinds are their forge's own name (internal/gitprov's KindBitbucket and
// KindGitHub), which TestRouteNamesArePinned holds them to: a route that
// spelled a forge another way would be a route the forge's own readers cannot
// line up with the credential it is about.
var Kinds = []Kind{KindModel, KindBitbucket, KindGitHub}

// Holder names the service a kind is served from (R4): the member's own
// project, or the workspace. The vocabulary has no third value and no unknown
// one: a kind the record does not name, and a kind it names as the project's,
// are the same answer.
type Holder string

const (
	// HolderProject is the member's own service, and the answer for everything
	// the record does not move to the workspace. It is the state of a member
	// that is in no workspace, and of a member whose own bundle covers the
	// repository and kind (D2973: the project's own copy wins when both exist).
	HolderProject Holder = "project"
	// HolderWorkspace is the workspace's service, which serves the kind under
	// the workspace entry's allow-list, the member's own policy and the
	// repository's admission (R5).
	HolderWorkspace Holder = "workspace"
)

// Holders is the closed vocabulary, in the order the record reads it.
var Holders = []Holder{HolderProject, HolderWorkspace}

// Route is one repository's credential-route record decoded: where each kind of
// its credentials is served from, and the address of the workspace's service
// when a kind is served there.
//
// A kind the record does not name is HolderProject through Holder below, so the
// zero Route is "everything is the project's, and there is no second service to
// call" — which is what a run with no record at all reads, and what the stub
// answers. Only a written `workspace` moves a kind, so a record that is
// truncated, mistyped or half-understood cannot send a call anywhere but the
// project.
type Route struct {
	// Version is the record's version: RouteVersion for every record DecodeRoute
	// reads.
	Version int
	// Address is the workspace's service, from the member's own pin (R3). Every
	// record carries one — RouteRecord refuses to write one without it and
	// DecodeRoute refuses to read one without it — so it is empty exactly for a
	// Route that came from no record at all: the zero Route a repository with no
	// published route reads, and what the stub answers.
	//
	// It is not the answer to whether the workspace serves anything. A record may
	// name every kind the project's, and its address is then never called: it is
	// Holder(kind) that says where a kind is served, and a run builds no second
	// upstream and keeps no beat warm for an address nothing is served from.
	Address string
	// Model, Bitbucket and GitHub are each kind's holder, empty where the record
	// names none.
	Model     Holder
	Bitbucket Holder
	GitHub    Holder
}

// Holder is the service the given kind is served from: HolderWorkspace when the
// record names the workspace for it, and HolderProject otherwise — including
// for a kind this code does not know, because a kind no record moved is served
// where every kind was served before the record existed.
func (r Route) Holder(kind Kind) Holder {
	switch kind {
	case KindModel:
		if r.Model == HolderWorkspace {
			return HolderWorkspace
		}
	case KindBitbucket:
		if r.Bitbucket == HolderWorkspace {
			return HolderWorkspace
		}
	case KindGitHub:
		if r.GitHub == HolderWorkspace {
			return HolderWorkspace
		}
	}
	return HolderProject
}

// RouteReader answers one repository's route: where each kind of its
// credentials is served from, and the workspace's address when one of them is
// served there. WS11 reads it once at the run's start and keeps the answer
// (D2982); WS14 reads it on a member's own machine to report it.
//
// A repository with no record is answered with the zero Route and no error: no
// record is today's run, not a failure, and an error here means the record
// could not be read or is one this code refuses. A caller that treated an error
// as "the project's" would serve a credential from a service an unreadable
// record may have moved.
type RouteReader interface {
	Route(ctx context.Context, slug string) (Route, error)
}

// ErrRouteRecord is the refusal of a credential-route record this code does not
// read: a version it does not know, a key it does not model, a holder spelling
// outside the vocabulary, or an address a run may not be sent to. Every
// refusal DecodeRoute makes wraps it, so a caller has one sentinel for "this
// record is not one a run may be served from" and no refusal tells it a value.
var ErrRouteRecord = errors.New("creds: the credential-route record is not one a run may be served from")

// routeWire is the record as the node carries it, and the one spelling its
// encoder writes: a kind the route does not move to the workspace is left out
// rather than written as the project's, so two encodings of one route are one
// record — the publish is conditional on the bytes (WS9) and a writer that
// spelled the default two ways would rewrite the node forever.
//
// The omission is movedHolder's and not omitempty's: HolderProject is the string
// "project" and not the zero Holder, so a tag alone writes it out, and the same
// route spelled with it and without it would be two records.
type routeWire struct {
	Version   int    `json:"version"`
	Address   string `json:"address"`
	Model     Holder `json:"model,omitempty"`
	Bitbucket Holder `json:"bitbucket,omitempty"`
	GitHub    Holder `json:"github,omitempty"`
}

// RouteRecord encodes r as the record a run reads: the bytes WS9 publishes at
// RoutePath(slug).
//
// A kind the route does not move to the workspace is left out of the bytes,
// whether r spells HolderProject for it or names no holder at all: the two are
// one answer to Holder, so they are one record, and a writer that published both
// would rewrite the node on every update.
//
// A route this code will not read back is refused here rather than published: a
// version outside RouteVersion, a holder outside the vocabulary, and an address
// a run may not be sent to — the member's own pin holds it to https with no
// query, and the reader cannot ask the writer whether it did.
func RouteRecord(r Route) ([]byte, error) {
	if r.Version != RouteVersion {
		return nil, fmt.Errorf("%w: it is version %d and this Fugaro writes version %d", ErrRouteRecord, r.Version, RouteVersion)
	}
	if err := checkServiceURL(r.Address, ErrRouteRecord); err != nil {
		return nil, err
	}
	holders := []struct {
		kind Kind
		h    Holder
	}{{KindModel, r.Model}, {KindBitbucket, r.Bitbucket}, {KindGitHub, r.GitHub}}
	for _, h := range holders {
		if err := checkHolder(h.h); err != nil {
			return nil, fmt.Errorf("%w: the %s holder %s", ErrRouteRecord, h.kind, err)
		}
	}
	out, err := json.Marshal(routeWire{
		Version:   r.Version,
		Address:   r.Address,
		Model:     movedHolder(r.Model),
		Bitbucket: movedHolder(r.Bitbucket),
		GitHub:    movedHolder(r.GitHub),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: it could not be encoded: %v", ErrRouteRecord, err)
	}
	return out, nil
}

// DecodeRoute reads the record a run reads. found is false for a node that is
// not there at all (null, or nothing), which is a member that publishes no
// route and not a refusal.
//
// It decodes strictly: a key this code does not model is refused rather than
// decoded past, because the record moves a member's credential from one service
// to another and a key nobody read is a decision nobody made. The version is
// read first and by name, so a record a later release wrote is refused as a
// version this code does not know rather than as one unknown field.
func DecodeRoute(raw []byte) (Route, bool, error) {
	if projectpolicy.IsNull(raw) {
		return Route{}, false, nil
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return Route{}, true, fmt.Errorf("%w: %v", ErrRouteRecord, err)
	}
	if head.Version != RouteVersion {
		return Route{}, true, fmt.Errorf("%w: it is version %d and this Fugaro reads version %d", ErrRouteRecord, head.Version, RouteVersion)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var w routeWire
	if err := dec.Decode(&w); err != nil {
		return Route{}, true, fmt.Errorf("%w: %v", ErrRouteRecord, err)
	}
	for _, h := range []Holder{w.Model, w.Bitbucket, w.GitHub} {
		if err := checkHolder(h); err != nil {
			return Route{}, true, fmt.Errorf("%w: %s", ErrRouteRecord, err)
		}
	}
	if err := checkServiceURL(w.Address, ErrRouteRecord); err != nil {
		return Route{}, true, err
	}
	return Route{
		Version:   w.Version,
		Address:   w.Address,
		Model:     w.Model,
		Bitbucket: w.Bitbucket,
		GitHub:    w.GitHub,
	}, true, nil
}

// movedHolder is the holder the record writes for one kind: HolderWorkspace when
// the route moves it there, and nothing at all otherwise. HolderProject and a
// kind the route says nothing about are one answer to Holder, so the two have to
// be one record: writing the default out would give a route two spellings, and
// WS9's conditional publish would rewrite the node on every update.
func movedHolder(h Holder) Holder {
	if h == HolderWorkspace {
		return HolderWorkspace
	}
	return ""
}

// checkHolder refuses a holder outside the vocabulary, naming the spellings it
// accepts and never a credential's value: a holder is a word of this contract
// and not a secret.
func checkHolder(h Holder) error {
	switch h {
	case "", HolderProject, HolderWorkspace:
		return nil
	}
	return fmt.Errorf("%q is neither %q nor %q", string(h), HolderProject, HolderWorkspace)
}

// routePath is the policy subtree one repository's route lives under, spelled
// as the database rules spell it: the record is a policy-side leaf a run's own
// token may read and no token but the member's admins' may write, which is the
// per-child shape config/policy/repos/<slug> has and for the same reason — a
// read granted on the parent would cascade to every repository's record.
const routePath = "config/policy/route/"

// RoutePath is the node one repository's route lives at, relative to the
// project's database root and without a leading slash. The caller passes the
// raw slug.
//
// The segment is escaped with budget.Key, which is the spelling the rules
// compare an RTDB token's `fs` claim with (auth.token.fs == $slug): a read
// granted on this node admits the run whose slug the token proves and no other,
// so the path and the claim must be one escape.
func RoutePath(slug string) string {
	if slug == "" {
		panic("creds: empty slug in a credential-route path")
	}
	return routePath + budget.Key(slug)
}

// RoutePathParent is the subtree the per-repository routes sit under, without a
// trailing slash. It carries no grant of its own: its children do, so it is for
// the member's own `fugaro update`, which removes the records of repositories
// it no longer publishes, and not for a run token.
func RoutePathParent() string { return routePath[:len(routePath)-1] }
