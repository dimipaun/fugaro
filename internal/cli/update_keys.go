package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"google.golang.org/api/googleapi"
	"gopkg.in/yaml.v3"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The copy of this installation's model keys into the credentials service's
// bundle: the stage KS12 adds to `fugaro update` (D3001, R4 of
// docs/keys-through-service).
//
// **What it is for.** Every model key an installation holds today lives in a
// container of its own — a provider's secret, the project's own Anthropic key,
// a repository's — and a run reaches it through a mount. The credentials
// service serves the same keys out of ONE bundle (C1), so a key that is not in
// that bundle is a key a released run is no longer served: the copy is what
// moves the installation onto the service without asking anybody to re-enter a
// value nobody can read back. It is the one exception to the rule that no value
// is ever copied between the two stores, and internal/credsvc/dynamicsecrets.go
// is where that rule is written down.
//
// **The rules it keeps, in the order it keeps them.** Only a project owner
// copies (D3006): KS2's own probe is asked before any value is read, and an
// operator's update is skipped with the sentence that names who can make the
// write. The value is read into memory and never printed, logged or carried in
// an error: every line here names an entry, a container or a version, and every
// value the stage holds is registered with the run's redaction while it is
// held. An entry the copy writes carries the container and the version it came
// from (the marker, D3014), which is what makes the copy idempotent and what
// tells a later run that the entry is the copy's own; an entry it did not write
// is never overwritten without saying so, one a container has moved past is
// copied again and named in one line, and one that already says what this run
// would write adds no version at all. It asks nothing: an operator who is not
// the project owner is told who to ask, and nobody is prompted for a value.
//
// **The container is the source and C1's name is the entry.** A pair of those
// two is what this file calls a copy: the container is read with this
// installation's own Secret Manager client, and the entry's name is built by
// C1's rule and never spelled here. One value several repositories share is one
// pair per repository (D3008): the slug is what makes them different entries,
// and no member of the family names two repositories.
//
// **The write goes through the guard every writer of the family goes through**
// (refuseAnEntryTheServiceCannotRead, F22): a document holding an entry the
// deployed credentials service cannot read is a document that service refuses
// WHOLE, halting every caller of it, coding runs included — so the copy is
// refused rather than landed where the service is too old, and this stage sits
// immediately after the one that deploys that service, which is what leaves the
// guard's answer a state the same run has already fixed.
//
// **A read that failed is not an absence** (R7): a container that is not there,
// or one holding no version this run can read, is a pair with nothing to copy
// and a line saying so; a read that came back an error is this stage's failure,
// because a copy that quietly skipped a key would leave a repository with
// neither a mount (release B drops them) nor an entry the service serves.
//
// **Nothing here is cached across the check, the plan and the apply, and that
// is deliberate.** The stages whose read is remembered (the route, the
// workspace) hold an answer about documents this run does not itself move; this
// one decides on the release of a service the stage immediately before it
// redeploys, and on a container the installation stage creates — so a read
// taken in the preview pass, before either has happened, is a read of another
// installation, and believing it would leave the copy owed on the very run that
// made it possible. The read is made again for each of the three, which is a
// document and a handful of containers against an update that takes minutes.

// keyCopy is one pair the copy works on: a container that holds a model key
// outside the bundle, and the bundle entry that value belongs under.
type keyCopy struct {
	// container is the container's LOGICAL name as this installation spells it
	// (config.AnthropicSecret, a provider's own name, a repository's): a name
	// and never a value, and the name the marker carries, because it is the
	// name the operator's own commands and `fugaro secrets ls` speak
	// (credsvc.ProviderKeySource).
	container string
	// secretID is the container's Secret Manager ID in this installation,
	// whichever naming the key's own declaration chooses: what the read is made
	// of, and deliberately not what the marker carries.
	secretID string
	// entry is the bundle key the value belongs under, as C1 builds it
	// (credsvc.RepoCredentialKey).
	entry string
	// baseURL and auth are the entry's two routing facts: where a routed call
	// goes and how the key is sent. They are the credential's own declaration
	// in the owner's config and never a caller's (F45, D1418), and the loader
	// holds an entry to the same two rules before it serves one.
	baseURL, auth string
	// repo and credential are the two halves of the entry's name as a person
	// reads them — the repository and the credential the value is entered for —
	// for the lines this stage prints. Neither is a value.
	repo, credential string
}

// name is the pair as one of this stage's lines names it.
func (c keyCopy) name() string {
	if c.repo == "" {
		return pluginwire.Printable(c.credential)
	}
	return fmt.Sprintf("%s for %s", pluginwire.Printable(c.credential), pluginwire.Printable(c.repo))
}

// copySources answers the pairs this run has to copy: for each model key the
// installation holds outside the credentials service's bundle, the container
// that holds it and the entry it belongs under — one pair per repository a
// shared key is entered for (D3008).
//
// It is the half of the copy that reads the installation's own config, and it
// is a variable so that the machinery the rest of this file is — the read, the
// marker, the decision, the guard and the write — can be put in front of pairs
// a test builds. What it answers today is none: the enumeration is the part of
// KS12 its provider-key and repository-key tasks complete, and until they do an
// installation is copied nothing and the stage says so rather than pretending
// otherwise.
var copySources = func(ctx context.Context, lc *localcfg.Config) ([]keyCopy, error) { return nil, nil }

// copyNoServiceReason is the stage's skip on an installation whose config
// carries no credentials service at all. The block's presence is the opt-in
// (localcfg.CredentialsService), so an installation without it has no bundle
// for the copy to write into — and nothing beyond that to say: this is the
// state of every installation that has not turned the service on, and it is not
// work the operator owes anybody.
const copyNoServiceReason = "the project's config carries no credentials_service block, and its presence is what turns the credentials service on: this installation has no credentials service, so there is no bundle for the copy to write a model key into"

// copyNothingReason is the stage's skip when the installation holds nothing
// outside the bundle. It is not the same answer as the one above: the service
// is here, and the copy looked.
const copyNothingReason = "this installation holds no model key outside the credentials service's bundle: nothing to copy"

// copySkip is a state the copy does not act on, with the sentence that names
// why. undone marks the skips that leave work owed — a bundle this run could
// not read, a person who is not a project owner, a service too old to read the
// document — as against the two that mean there is nothing to do at all.
type copySkip struct {
	reason string
	undone bool
}

func (e *copySkip) Error() string { return e.reason }

// copySkipOf is the skip Check, Plan and Apply all answer with.
func copySkipOf(err error) (copySkip, bool) {
	var skip *copySkip
	if errors.As(err, &skip) {
		return *skip, true
	}
	return copySkip{}, false
}

// copyRowState is what the copy does with one pair, read from the entry the
// bundle holds for it and from the version the container's newest one is.
type copyRowState int

const (
	// copyNew is a pair the bundle holds no entry for: it is written.
	copyNew copyRowState = iota
	// copyFollow is an entry the copy wrote, from an older version of the same
	// container than the one the container holds now (D3014), or with the two
	// routing facts the credential's own declaration has changed since: it is
	// written again.
	copyFollow
	// copyUnchanged is an entry that already says exactly what this run would
	// write: nothing is written and no version of the bundle is added, which is
	// what makes a second `fugaro update` change nothing.
	copyUnchanged
	// copyLeft is an entry the copy did not write, and which does not say what
	// it would: it is left as it is and named in one line.
	copyLeft
	// copyAbsent is a container with no version this run can read: there is
	// nothing to copy out of it.
	copyAbsent
)

// copyRow is one pair and what this run does with it.
type copyRow struct {
	pair keyCopy
	// held is the container's newest version as it was read: the version
	// number, the bytes (in memory and nowhere else) and the slot the run's
	// redaction keeps them out of any output with.
	held  *copyHold
	state copyRowState
	// why is the clause the state is reported with: what the entry holds
	// against what this run would write. It carries names and versions only.
	why string
}

// copyHold is one container read once, and everything the run holds of it.
//
// The value is held rather than passed around so that a line printed while it
// is in memory cannot carry it: initEngine.hold registers the whole value and
// each of its lines with the run's redaction, and release gives the slots back.
// The read is made once per CONTAINER and not once per pair, because one key
// several repositories share is many pairs over one container (D3008) — and the
// slots a run has are bounded (maxHeldSecrets).
type copyHold struct {
	value   []byte
	version string
	release func()
}

// copyPlan is the whole of one run's copy: the document it was decided against,
// the pairs, and what each of them answers.
type copyPlan struct {
	lc     *localcfg.Config
	store  *bundleStore
	before bundleDoc
	// held is every container this read looked at, by Secret Manager ID.
	held map[string]*copyHold
	rows []copyRow
}

// release clear()s every value the plan holds and gives its slot back. It is
// called after the write — the values are the document's by then — and wherever
// a plan is dropped unfinished: a value in memory is a value with a lifetime,
// and the run's redaction is a fixed set of slots.
func (p *copyPlan) release() {
	for id, h := range p.held {
		clear(h.value)
		h.release()
		delete(p.held, id)
	}
}

// copyPlanRead is the whole read the copy is decided on: the two config-side
// answers, the bundle, who is asking, the guard for the service that will read
// the document, and then one read per container.
//
// The order is the one every writer of this document keeps
// (providerKeyBundleStore), and each step is where it is for its own reason:
// the config's two answers first, because an installation with no service and
// one with nothing outside the bundle make no cloud call at all; the container
// before the probe, so a container nobody has created yet is answered as the
// missing container it is rather than as a refusal of the person; the person
// before any value is read (D3006); the guard before the document is built; and
// the values last.
func copyPlanRead(ctx context.Context, e *initEngine) (*copyPlan, error) {
	lc := e.lc
	if lc.CredentialsService == nil {
		return nil, &copySkip{reason: copyNoServiceReason}
	}
	pairs, err := copySources(ctx, lc)
	if err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return nil, &copySkip{reason: copyNothingReason}
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return nil, copyBundleUnreadable(err)
	}
	if err := store.check(ctx); err != nil {
		return nil, copyBundleUnreadable(err)
	}
	if err := bundleOwnerProbe(ctx, store, lc, "copied"); err != nil {
		// D3006: an operator's update does not copy, and the probe's own
		// sentence names the project owner who can.
		return nil, &copySkip{reason: oneLineCLI(err.Error()), undone: true}
	}
	if err := refuseAnEntryTheServiceCannotRead(ctx, lc, repoCredentialFamily, repoCredentialSince); err != nil {
		// The deployed service cannot read the family, so a document holding
		// one of its entries is a document that service refuses WHOLE (F22):
		// every caller of it, a coding run included, would be halted by this
		// write. The work is owed, and the run that settles it is this one's
		// own service stage, or the next `fugaro update`.
		return nil, &copySkip{reason: oneLineCLI(err.Error()), undone: true}
	}
	before, err := providerKeyBundleBefore(ctx, store)
	if err != nil {
		return nil, copyBundleUnreadable(err)
	}
	// The document is judged through the service's own loader, because what the
	// decision reads is what the SERVICE will read and not a second reading of
	// the same YAML. providerKeyBundleBefore has already refused a document
	// this cannot read, so the error here is unreachable and is the failure it
	// would be.
	var held *credsvc.Bundle
	if len(before.raw) > 0 {
		if held, err = credsvc.ParseBundle(before.raw); err != nil {
			return nil, credsDocRefusal(store.id, err)
		}
	}
	p := &copyPlan{lc: lc, store: store, before: before, held: map[string]*copyHold{}}
	for _, pair := range pairs {
		row, err := p.read(ctx, e, pair, held)
		if err != nil {
			// Nothing stays held over a read that did not finish: these values
			// are not going to be written by this run.
			p.release()
			return nil, err
		}
		p.rows = append(p.rows, row)
	}
	return p, nil
}

// copyBundleUnreadable is the skip for a bundle this read could not get at. It
// is a skip and not a failure, for routeFactsOf's reason: the copy is the last
// stage of an update whose other stages have already done their work, and a run
// that failed here — over a container the installation stage has just created,
// say — would be an update the person has to make twice. What the copy owes is
// said as owed instead.
func copyBundleUnreadable(err error) error {
	return &copySkip{
		reason: fmt.Sprintf("%s, and the copy of this installation's model keys is written into that bundle: nothing was copied", oneLineCLI(remoteErr(err, nil).Error())),
		undone: true,
	}
}

// read is one pair's whole answer: the container's newest version, read once
// per container, and the clause the bundle's own entry decides.
func (p *copyPlan) read(ctx context.Context, e *initEngine, pair keyCopy, held *credsvc.Bundle) (copyRow, error) {
	row := copyRow{pair: pair}
	h, ok := p.held[pair.secretID]
	if !ok {
		value, version, found, err := copyContainerNewest(ctx, p.store, pair)
		if err != nil {
			return row, err
		}
		if !found {
			row.state = copyAbsent
			row.why = fmt.Sprintf("%s holds no version this run can read", safetext.Field(pair.container))
			return row, nil
		}
		// The slots are built once per engine, and a stage driven on its own —
		// a test, or an engine whose options were never read — has not built
		// them yet.
		e.slots()
		release, err := e.hold(value)
		if err != nil {
			clear(value)
			return row, err
		}
		h = &copyHold{value: value, version: version, release: release}
		p.held[pair.secretID] = h
	}
	row.held = h
	row.state, row.why = copyDecide(held, pair, h.value, h.version)
	return row, nil
}

// copyContainerNewest reads the newest version of one container the copy reads
// a value OUT of, in memory, with the version number the read resolved to (the
// server's, as bundleStore.access reads the bundle's).
//
// found=false is "there is nothing here to copy out of": a container that is
// not there, one whose newest version carries no payload, and one Secret
// Manager will not serve. The last is a DISABLED version — the bytes are there
// and retired, and Secret Manager refuses them with a 400 — and it is answered
// as an absence because a value somebody disabled deliberately is not one the
// copy may resurrect or report as this run's fault. A version reference this
// tree builds is not a malformed request, which is the other thing a 400 can
// mean.
//
// A read that FAILED is neither: it is this stage's failure, for the reason
// R7's own rule gives for the service — a copy that skipped a key it could not
// read would leave a repository with neither a mount nor an entry the service
// serves. What the server said is never repeated as it came: the failure is
// bounded and redacted like every other remote error of this document.
func copyContainerNewest(ctx context.Context, store *bundleStore, pair keyCopy) (value []byte, version string, found bool, err error) {
	r, err := store.svc.Projects.Secrets.Versions.
		Access("projects/" + store.project + "/secrets/" + pair.secretID + "/versions/latest").
		Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusNotFound || ge.Code == http.StatusBadRequest) {
			return nil, "", false, nil
		}
		return nil, "", false, remoteErr(fmt.Errorf("reading the newest version of %s: %w", safetext.Field(pair.container), err), nil)
	}
	if r.Payload == nil || r.Payload.Data == "" {
		return nil, "", false, nil
	}
	value, err = base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		// The payload is not in the error: it is a value, and the failure says
		// only that it was not base64.
		return nil, "", false, remote(fmt.Errorf("reading the newest version of %s: the payload is not base64", safetext.Field(pair.container)))
	}
	number, err := versionNumber(r.Name)
	if err != nil {
		return nil, "", false, remote(err)
	}
	return value, number, true, nil
}

// copyDecide is the whole of the marker's rule (R4, D3014), read from the entry
// the bundle holds for the pair through C1's own accessor — the loader the
// service reads the document with, so the two cannot disagree about what an
// entry says.
//
// A pair the bundle holds no entry for is written; an entry that already says
// what this run would write is left alone and no version is added; an entry the
// COPY wrote is followed when the container has moved past the version its
// marker names, or when the credential's own declaration has changed under it;
// and an entry the copy did not write is never overwritten. Those are the four
// answers, and the last one is the one a person is told about.
func copyDecide(held *credsvc.Bundle, pair keyCopy, value []byte, version string) (copyRowState, string) {
	slug, credential, ok := credsvc.RepoCredentialByKey(pair.entry)
	if !ok {
		// A name C1 cannot read back is a name this tree built wrongly, and
		// the loader would refuse the whole document over it: it is left
		// alone, named, and never written.
		return copyLeft, fmt.Sprintf("the entry name %s is not one C1's rule builds, so this run writes nothing for it", safetext.Field(pair.entry))
	}
	entry, found := credsvc.ReadProviderKey(held, slug, credential)
	switch {
	case !found:
		return copyNew, ""
	case entry.Source.IsZero():
		// An entry the copy did not write: one written by hand, or one
		// `fugaro creds set` set at the owner's terminal. It is not the copy's
		// to replace (R4) — but an entry that already holds exactly what this
		// run would write is not replaced either, because there is nothing to
		// write, and adding a version for it would be an update that changed
		// the bundle on every run.
		if copyEntryMatches(entry, pair, value) {
			return copyUnchanged, fmt.Sprintf("the entry holds what %s and the credential's own declaration say, and carries no marker of this copy", safetext.Field(pair.container))
		}
		return copyLeft, fmt.Sprintf("the entry was not written by this copy and does not hold what %s does, and this run does not overwrite an entry it did not write", safetext.Field(pair.container))
	case entry.Source.Container != pair.container:
		return copyLeft, fmt.Sprintf("the entry was copied from the container %s and this run reads %s", safetext.Field(entry.Source.Container), safetext.Field(pair.container))
	}
	switch {
	case entry.Source.Version == version && copyEntryFactsMatch(entry, pair):
		return copyUnchanged, fmt.Sprintf("the entry was copied from %s version %s and still says what this run would write", safetext.Field(pair.container), safetext.Field(version))
	case newerVersion(entry.Source.Version, version):
		// A marker naming a version the container no longer holds: both are
		// the copy's own, and the entry's is the newer of them, so a rotation
		// since undone is not followed backwards over it.
		return copyLeft, fmt.Sprintf("the entry was copied from version %s of %s, newer than the %s this container holds now", safetext.Field(entry.Source.Version), safetext.Field(pair.container), safetext.Field(version))
	case entry.Source.Version != version:
		return copyFollow, fmt.Sprintf("%s holds version %s, newer than the %s the entry was copied from", safetext.Field(pair.container), safetext.Field(version), safetext.Field(entry.Source.Version))
	}
	// The same version, and the credential's own declaration has moved since
	// the copy wrote the entry: the entry is the copy's own, so it carries what
	// the config says now.
	return copyFollow, fmt.Sprintf("%s holds the version the entry was copied from (%s), and the credential's own declaration in the config has changed since", safetext.Field(pair.container), safetext.Field(version))
}

// copyEntryMatches is whether an entry the copy did not write already says what
// this run would write: the value the container holds, and the two routing
// facts the credential's own declaration states.
func copyEntryMatches(entry credsvc.ProviderKeyEntry, pair keyCopy, value []byte) bool {
	return entry.Value() == string(value) && copyEntryFactsMatch(entry, pair)
}

// copyEntryFactsMatch is whether the entry's two routing facts are the ones the
// credential's own declaration states.
func copyEntryFactsMatch(entry credsvc.ProviderKeyEntry, pair keyCopy) bool {
	return entry.BaseURL == pair.baseURL && entry.Auth == pair.auth
}

// copyEntryNode is the value node one entry is written with: the mapping of
// exactly the fields the service's loader reads — key, base_url and auth, the
// three providerKeyValueNode builds — with the copy's marker beside them
// (source: container, version), which is what tells a later run that this entry
// is one the copy wrote and which version of which container it came from
// (D3014).
//
// The two routing facts are held to the same two rules the loader holds an
// entry to before they are written — the platform's provider-URL check and the
// closed set of two auth shapes — so a pair this document could not carry is
// refused here, in a sentence about the declaration it came from, rather than
// by the service as a document it refuses whole.
//
// Nothing here renders the value: it goes into the node, the node into the
// document, and the document to Secret Manager. Every line this stage prints
// names an entry, a container or a version.
func copyEntryNode(pair keyCopy, value []byte, version string) (*yaml.Node, error) {
	if err := config.CheckProviderBaseURL(pair.baseURL); err != nil {
		return nil, userErr("%s is not an address one of the credentials service's entries may hold (%v): the entry's base_url is the destination of every routed call spent on this key, and the loader and the gateway's routes hold it to the same rule. Fix the credential's own declaration in the project config and run %s again; nothing was copied",
			pluginwire.Printable(pair.name()), err, selfCommand()+" update")
	}
	if pair.auth != providerAuthBearer && pair.auth != providerAuthXAPIKey {
		return nil, userErr("%s's declaration carries the auth shape %s, and one of C1's entries may hold only the two the gateway can send: %s or %s. Fix the credential's own declaration in the project config and run %s again; nothing was copied",
			pluginwire.Printable(pair.name()), pluginwire.Printable(pair.auth), providerAuthBearer, providerAuthXAPIKey, selfCommand()+" update")
	}
	str := func(v string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}
	}
	source := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	source.Content = append(source.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "container"}, str(pair.container),
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"}, str(version))
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "key"}, str(string(value)),
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "base_url"}, str(pair.baseURL),
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "auth"}, str(pair.auth),
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source"}, source)
	return root, nil
}

// writes are the rows this run would write, in the order the pairs came in.
func (p *copyPlan) writes() []copyRow {
	var out []copyRow
	for _, row := range p.rows {
		if row.state == copyNew || row.state == copyFollow {
			out = append(out, row)
		}
	}
	return out
}

// count is how many pairs the plan answers with one state.
func (p *copyPlan) count(state copyRowState) int {
	n := 0
	for _, row := range p.rows {
		if row.state == state {
			n++
		}
	}
	return n
}

// rest is the parenthetical the counts after the first one are said in.
func (p *copyPlan) rest() string {
	var parts []string
	if n := p.count(copyUnchanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d already there", n))
	}
	if n := p.count(copyLeft); n > 0 {
		parts = append(parts, fmt.Sprintf("%d left as they are", n))
	}
	if n := p.count(copyAbsent); n > 0 {
		parts = append(parts, fmt.Sprintf("%d with no version to copy", n))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// idle is whether this run has nothing to write, and the line that says so.
func (p *copyPlan) idle() (string, bool) {
	if len(p.writes()) > 0 {
		return "", false
	}
	return "every model key of this installation is already in the credentials service's bundle" + p.rest(), true
}

// planDetail is what the stage says it is about to do.
func (p *copyPlan) planDetail() string {
	return fmt.Sprintf("the credentials service's bundle %s at %s: this run copies %d model key(s) into it%s",
		safetext.Field(p.store.id), versionWord(p.before.version), len(p.writes()), p.rest())
}

// versionWord is the bundle's version as a sentence says it.
func versionWord(version string) string {
	if version == "" {
		return "the first version it will hold"
	}
	return "version " + safetext.Field(version)
}

// document is the bundle this run leaves in place: the document as it is, with
// one entry set for each pair that is written — all of them in ONE version.
//
// One version and not one write per pair, for providerKeyBundleDoc's reason: an
// entry written for some of the repositories a key serves and not for the
// others is a state in which the key serves a run on some repositories and not
// on others, which is a half-applied move. The read-modify-write edits YAML
// nodes rather than rebuilding the document from a map, so every entry this
// write was not asked about keeps its bytes.
func (p *copyPlan) document() ([]byte, error) {
	doc, root, err := bundleNodes(p.before.raw)
	if err != nil {
		return nil, err
	}
	for _, row := range p.writes() {
		node, err := copyEntryNode(row.pair, row.held.value, row.held.version)
		if err != nil {
			return nil, err
		}
		setBundleNodeValue(root, row.pair.entry, node)
	}
	return marshalBundle(doc)
}

// write lands the whole document as one version of the bundle, through the tail
// every writer of this document shares (bundleAddVerified): the add, the check
// of the version it landed as, the re-read it is verified with, and the
// retirement of the version it replaced.
//
// The value the tail is given is nil, and that is not an omission: the request
// it sends IS the document, and the forms a failure is redacted with are the
// document's own (writeForms registers the document, every secret the loader
// finds in it, and each of them base64 as sent) — so every value this write
// sets is one of them.
//
// The step it is given ends nowhere: a reporter with no writer discards what
// the tail writes into it, and the stage's own account is the loop's line.
func (p *copyPlan) write(ctx context.Context, cmd *cobra.Command) (bundleAddResult, error) {
	doc, err := p.document()
	if err != nil {
		return bundleAddResult{}, err
	}
	if _, err := credsvc.ParseBundle(doc); err != nil {
		return bundleAddResult{}, credsDocRefusal(p.store.id, err)
	}
	st := report.New(report.Options{}).Step(initflow.CredentialsCopy)
	return bundleAddVerified(ctx, cmd, st, p.store, p.before, doc, nil)
}

// confirm is the confirmation this write is made behind: the ordinary step,
// confirmed by confirmStageWrite — nothing is asked at the owner's terminal
// (D1540), --yes stands in where it reaches the stage, and --ask puts the
// question back. The diff is printed either way.
//
// It is the ordinary confirmation and not the workspace record's widened one,
// and the difference is what the two writes are: this one copies values that
// are already this installation's into the document its own service reads, and
// it decides nothing about who is admitted, what may be spent or what may be
// read.
func (p *copyPlan) confirm(cmd *cobra.Command, yes bool, w io.Writer, c initflow.Conditions) error {
	p.print(w)
	return confirmStageWrite(cmd, p.lc, c, yes, p.what())
}

// what names this write as the confirmation reads it.
func (p *copyPlan) what() string {
	return fmt.Sprintf("this copies %d model key(s) of this installation into the credentials service's bundle %s, in one version: each entry carries the container and the version it was copied from, and no value is printed, logged or carried anywhere else",
		len(p.writes()), safetext.Field(p.store.id))
}

// print is the one confirmation diff: each pair and what this run does with it.
// It carries entry names, container names and versions, and never a value.
func (p *copyPlan) print(w io.Writer) {
	fmt.Fprintf(w, "%s: this run copies this installation's model keys into the credentials service's bundle at %s\n",
		safetext.Field(p.store.id), versionWord(p.before.version))
	for _, row := range p.rows {
		fmt.Fprintf(w, "  %s: %s\n", safetext.Field(row.pair.entry), row.line())
	}
}

// line is one pair as the diff says it: what this run does, and why.
func (r copyRow) line() string {
	switch r.state {
	case copyNew:
		return fmt.Sprintf("written, from %s version %s", safetext.Field(r.pair.container), safetext.Field(r.held.version))
	case copyFollow:
		return "written again: " + r.why
	case copyUnchanged:
		return "unchanged: " + r.why
	case copyAbsent:
		return "nothing to copy: " + r.why
	}
	return "left as it is: " + r.why
}

// note is the one line this stage prints for an entry it wrote, after the
// version has landed — which is D3014's line, naming the container the value
// was copied from and the entry it was written under. Nothing else about the
// write is printed per pair: the stage's own line carries the count.
func (r copyRow) note() string {
	if r.state == copyNew {
		return fmt.Sprintf("note: the entry %s is copied from version %s of the container %s, for %s\n",
			safetext.Field(r.pair.entry), safetext.Field(r.held.version), safetext.Field(r.pair.container), r.pair.name())
	}
	return fmt.Sprintf("note: the container %s holds version %s, newer than the one the entry %s was copied from: the entry is copied again, for %s\n",
		safetext.Field(r.pair.container), safetext.Field(r.held.version), safetext.Field(r.pair.entry), r.pair.name())
}

// copyStage is the stage itself. It is SelfConfirming, like the policy, the
// workspace, the route and the layer stages: it shows its own diff, takes its
// own confirmation, and refuses a coding agent's session.
//
// It is NOT a stage of the managed gate (gatedStages, update_managed.go), and
// that is this stage's own answer rather than an omission: what it moves is
// every model credential the installation holds into the document its own
// service reads, which is not a write anybody's coding agent makes on their
// behalf — and KS2's probe answers an operator's run with the owner's name
// already.
type copyStage struct {
	e *initEngine
	// p is what Apply read and wrote, kept for Verify. Nothing is carried
	// across the check, the plan and the apply (see the file comment).
	p *copyPlan
}

func newCredentialsCopyStage(e *initEngine) *copyStage { return &copyStage{e: e} }

func (copyStage) Name() string    { return initflow.CredentialsCopy }
func (copyStage) SelfConfirming() {}

func (copyStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.CredentialsCopy, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// Check reports what the copy would write. A refusal here is the run's: a
// container this read cannot get at is not something the operator finishes by
// hand. The skips are the vocabulary of the config, the bundle and the two
// guards, and only the ones that leave work owed carry Undone.
func (s *copyStage) Check(ctx context.Context) (initflow.Status, error) {
	p, err := copyPlanRead(ctx, s.e)
	if skip, ok := copySkipOf(err); ok {
		return initflow.Status{State: initflow.Skipped, Detail: skip.reason, Undone: skip.undone}, nil
	}
	if err != nil {
		return initflow.Status{}, err
	}
	defer p.release()
	if detail, ok := p.idle(); ok {
		return initflow.Status{State: initflow.Done, Detail: detail}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: p.planDetail()}, nil
}

// Plan is what the run says it is about to do. The diff is printed where a
// person can read it — a terminal, or --yes, which is a person's word that they
// have read it — because the confirmation is made against it. Apply prints the
// same diff again at its own turn, which is where a converge confirms.
func (s *copyStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	p, err := copyPlanRead(ctx, s.e)
	if skip, ok := copySkipOf(err); ok {
		return initflow.Plan{NothingToDo: true, Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Plan{}, err
	}
	defer p.release()
	if detail, ok := p.idle(); ok {
		return initflow.Plan{NothingToDo: true, Detail: detail}, nil
	}
	if env.Interactive || env.Yes {
		p.print(s.e.r.cmd.OutOrStdout())
	}
	return initflow.Plan{Detail: p.planDetail()}, nil
}

// Apply is the write. The session that may not write to the cloud refuses
// first, exactly as the route and layer stages do: the bundle holds every model
// credential the service issues, and no flag a coding agent's session carries
// reaches it.
func (s *copyStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
		return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
	}
	p, err := copyPlanRead(ctx, s.e)
	if skip, ok := copySkipOf(err); ok {
		return initflow.Outcome{Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Outcome{}, err
	}
	if detail, ok := p.idle(); ok {
		p.release()
		return initflow.Outcome{Detail: "No changes: " + detail}, nil
	}
	cmd := s.e.r.cmd
	if err := s.e.r.asking(func() error {
		return p.confirm(cmd, env.Yes, s.e.r.w, s.e.r.conditions())
	}); err != nil {
		p.release()
		return initflow.Outcome{}, err
	}
	res, err := p.write(ctx, cmd)
	if err != nil {
		p.release()
		return initflow.Outcome{}, err
	}
	// The values are the document's by now — every form of each of them is
	// written, and the redaction carried them while the run held them — so what
	// the run holds is given back before anything else is printed.
	writes := p.writes()
	p.release()
	for _, row := range writes {
		fmt.Fprint(s.e.r.w, row.note())
	}
	s.p = p
	return initflow.Outcome{
		Changed: true,
		Detail: fmt.Sprintf("copied %d model key(s) into the credentials service's bundle %s, as version %s",
			len(writes), safetext.Field(p.store.id), safetext.Field(res.version)),
	}, nil
}

// Verify re-reads every entry this run wrote and checks that the bundle really
// carries it, with the marker naming the container and the version it came
// from. The value is not compared: the write's own re-read is the whole
// document byte for byte (bundleAddVerified), and what is left for this to
// check is the half a whole-document comparison cannot speak about — that the
// entry the service will be served is under the name the copy meant, carrying
// the copy's own marker and the facts the declaration states.
func (s *copyStage) Verify(ctx context.Context) error {
	if s.p == nil || len(s.p.writes()) == 0 {
		// The run wrote nothing, or was skipped: there is nothing of this
		// run's to read back.
		return nil
	}
	p := s.p
	after, err := p.store.access(ctx, "latest")
	if err != nil {
		return remoteErr(err, nil)
	}
	held, err := credsvc.ParseBundle(after.raw)
	if err != nil {
		return credsDocRefusal(p.store.id, err)
	}
	for _, row := range p.writes() {
		slug, credential, ok := credsvc.RepoCredentialByKey(row.pair.entry)
		if !ok {
			return fmt.Errorf("the entry name %s is not one C1's rule builds", safetext.Field(row.pair.entry))
		}
		entry, found := credsvc.ReadProviderKey(held, slug, credential)
		if !found {
			return fmt.Errorf("the credentials service's bundle at version %s holds no entry %s, though this run copied one into it", safetext.Field(after.version), safetext.Field(row.pair.entry))
		}
		if entry.Source.Container != row.pair.container || entry.Source.Version != row.held.version {
			return fmt.Errorf("the entry %s was written by this run but does not carry the container and the version it was copied from", safetext.Field(row.pair.entry))
		}
		if !copyEntryFactsMatch(entry, row.pair) {
			return fmt.Errorf("the entry %s was written by this run but does not carry the address and the auth shape the credential's own declaration states", safetext.Field(row.pair.entry))
		}
	}
	return nil
}
