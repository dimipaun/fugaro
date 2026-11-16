package cli

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is the settings half of `fugaro secrets set`: the three keys of the
// secrets family that are records ABOUT secrets rather than the values
// themselves — when one lapses, how often the installation rotates, and which
// Bitbucket API roots a token may be sent to (D2616, plan S1). They are written
// here, by the family's own flags, and never by an argument or a flag value:
// `secrets set NAME` with no such flag is the value write and is untouched by
// anything below, which is the separation the whole command is built on (a
// value is not a setting, D1171).
//
// The write path is the settings one — the resolver that turns a key and a
// layer into a file, the reader, the edit, and applySettingsEdit's fence, dry
// run, diff, compare-and-swap and backup — so a record lands exactly as
// `fugaro settings set` lands one and no second writer of these files exists.
// What is this file's own is the flag surface: which layer a record may be
// written at (the registry row's, through settingsResolveTarget, which is the
// T3 refusal), what each value has to be, and the sentences a person reads.

// secretsSettingsNow is the clock --expires is read against: a date behind
// today records an expiry that has already passed, which is not a record but a
// mistake. It is the package's variable and not time.Now at the call site, so a
// test pins the day it is asking about and reads one answer twice.
var secretsSettingsNow = time.Now

// secretsNoEcho ends every refusal this file makes about a value a person typed
// for one of the three flags. Nothing prints what it was given: `secrets set`
// is where a value pasted into the wrong place lands, this family never echoes
// one (secretsSet does not quote the name, and the command's own flag error
// says "and never the value" for pflag's reason), and a flag whose whole point
// is that it carries no value must not become a way to print one.
const secretsNoEcho = "The value given is not printed: a token pasted into the wrong flag must not be echoed, and no flag of this command takes a secret on the command line."

// secretsRecordFlags names the four flags this file writes, in the order the
// help lists them, and every sentence below that has to name them reads them
// from here: a refusal that named three of four is how a person reads a rule
// this command no longer has.
const secretsRecordFlags = "the record flags (--expires, --rotation-days, --bitbucket-api-root, --remove-bitbucket-api-root)"

// secretsSettingsChange is one record this run writes: the registry row, the
// map member when the row is keyed by a secret's name, the sentence naming the
// write, and the edit itself. The edit takes the path the resolver built and
// not one of its own, so a key's place in the file is spelled once.
type secretsSettingsChange struct {
	key  string
	name string
	what string
	set  func(path []string, old []byte) ([]byte, error)
}

// settingsKey is the key as `fugaro settings` spells it: the row, with the map
// member the record is about when it has one.
func (c secretsSettingsChange) settingsKey() string {
	if c.name == "" {
		return c.key
	}
	return c.key + "." + c.name
}

// secretsSetSettings is the settings half of the command. Each flag given is a
// settings write of its own — its own diff, its own backup and its own
// after-write hook — so two flags in one invocation are two writes of two keys
// and never one write of two, which is what the settings write path is built
// for (one edit, one key). The dry run is the exception and the reason the run
// collects its documents: the answer to one run is one document, and the
// records of the run are what is inside it (writeSecretsSettingsDryRun).
func secretsSetSettings(cmd *cobra.Command, o secretsOptions, name string) error {
	if err := settingsDryRunFlags(settingsWriteOptions{dryRun: o.dryRun, jsonOut: o.asJSON, yes: o.yes}); err != nil {
		return err
	}
	// --repo and --project-wide say where a VALUE is stored. These records are
	// this machine's own knowledge about secrets, in the local config: no
	// repository owns one and the project-wide copy has none, so either flag
	// beside them would be read as narrowing a write it cannot narrow.
	if cmd.Flags().Changed("repo") || cmd.Flags().Changed("project-wide") {
		return userErr("--repo and --project-wide say where a secret's value is stored, and %s record what this machine knows about secrets in its own local config: a record has no repository's copy and no project-wide one — drop the flag", secretsRecordFlags)
	}
	// The policy flags write the entry that decides who may mount a value, and
	// this run writes the records instead. Both are decided from the flags
	// alone and neither reads a value, so a run naming both would take the
	// settings branch and leave the policy write the person asked for plainly
	// undone — which is the one outcome no sentence of this command may leave
	// unsaid (the value path's own "to store a value and change its policy, run
	// the command twice" is the same rule, D1171).
	if o.policyWrite(cmd) {
		return userErr("--allow-data-to and --proxy write the entry that decides who may mount a secret's value, and %s write this machine's own records about secrets: this command does one write per run — pass one of the two, then repeat it with the other", secretsRecordFlags)
	}
	changes, err := secretsSettingsChanges(cmd, o, name)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		// The only way here is a bare --layer: every other spelling that
		// reaches this path names one of the record flags.
		return userErr("--layer says where a settings write lands and goes with %s: a secret's own value is stored in Secret Manager, which has no layer", secretsRecordFlags)
	}
	sel, lc, err := o.aimedSelection()
	if err != nil {
		return err
	}
	command := secretsSettingsCommand(cmd, o, name)
	var docs []settingsWriteDoc
	// A dry run writes nothing, so the record after the first would read the
	// file on disk and work its edit out from a state the write never leaves
	// behind: `--bitbucket-api-root a --bitbucket-api-root b` adds both roots,
	// and a dry run that read the file twice would answer b's record with a list
	// holding b alone. carried is what the dry run hands from one record to the
	// next, by the file that record resolved to, and it is nil for a write,
	// where every edit has landed before the next record reads the file.
	var carried map[string][]byte
	if o.dryRun {
		carried = map[string][]byte{}
	}
	for _, change := range changes {
		if err := secretsSettingsRecord(cmd, o, change, sel, lc, command, &docs, carried); err != nil {
			return err
		}
	}
	if o.dryRun {
		return writeSecretsSettingsDryRun(cmd.OutOrStdout(), docs, o.asJSON)
	}
	return nil
}

// writeSecretsSettingsDryRun prints this run's dry-run answer: one record keeps
// the document the dry run has always answered with, and a run of several
// records prints one document that holds every one of them, because a consumer
// reads one value and several objects printed one after the other are not one.
// Without --json the run prints one table per record: the table is the human
// shape, a nested table is not a table, and the two shapes of the document
// itself are still one contract. docs holds a record for every change the run
// made and is never empty — a run that names no record is refused before it
// reaches here (secretsSetSettings' bare --layer refusal).
func writeSecretsSettingsDryRun(w io.Writer, docs []settingsWriteDoc, jsonOut bool) error {
	if len(docs) == 1 {
		return writeSettingsWriteDoc(w, docs[0], jsonOut)
	}
	if jsonOut {
		return writeSettingsWriteDoc(w, secretsSettingsRunDoc(docs), true)
	}
	for _, doc := range docs {
		if err := writeSettingsWriteTable(w, doc); err != nil {
			return err
		}
	}
	return nil
}

// secretsSettingsRunDoc is the one document a run of several records prints:
// the run's own facts, and every record in Records. Layer and Path are the
// run's because the three rows are local config keys (the registry's own local
// rows), so no run of them can reach two layers or two files; Changed, Widening
// and Why are the run's answer to whether any of its records changes the file,
// widens a limit, or says what it widens. The three members a record is
// addressed by are the records' own and are not the run's: Key is left out,
// because a run names no single key, Old and New are empty, because the values
// are the records', and each record carries all three in Records.
func secretsSettingsRunDoc(docs []settingsWriteDoc) settingsWriteDoc {
	run := settingsWriteDoc{Version: settingsWriteDocVersion, Records: docs}
	for _, doc := range docs {
		run.Changed = run.Changed || doc.Changed
		run.Widening = run.Widening || doc.Widening
		if run.Layer == "" {
			run.Layer = doc.Layer
		}
		if run.Path == "" {
			run.Path = doc.Path
		}
		if run.Why == "" {
			run.Why = doc.Why
		}
	}
	return run
}

// secretsSettingsRecord writes one record: the row's own layer, the file the
// settings resolver names for it, the edit the change carries, and then the
// stages the write owes. carried is a dry run's own state, keyed by file (see
// secretsSetSettings) and nil for a write.
func secretsSettingsRecord(cmd *cobra.Command, o secretsOptions, change secretsSettingsChange, sel localcfg.Selection, lc *localcfg.Config, command string, collect *[]settingsWriteDoc, carried map[string][]byte) error {
	row, ok := settings.Lookup(change.key)
	if !ok {
		// Unreachable: every change comes from a row this command registers
		// against, and the registry is compiled in. Refused rather than
		// assumed away, so a key renamed in the registry is a sentence and not
		// a write to a path nothing reads.
		return userErr("%s: the settings registry holds no row for this key, so nothing was written", change.key)
	}
	// The layer refusal is the resolver's: a row is writable at the layers its
	// own row names, and `--layer project` for a local config key is refused
	// with the layers the key does have (T3, docs/contracts/config-ownership.md §6).
	target, err := settingsResolveTarget(cmd.Context(), row, settingsWriteOptions{layer: o.layer}, lc, sel)
	if err != nil {
		return err
	}
	if change.name != "" {
		target.keyPath = append(target.keyPath, change.name)
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	// A record of a file this run has already answered for starts where that
	// record left it, and not from the file on disk: in a dry run nothing was
	// written in between, so the disk still holds the state before the run.
	if prior, ok := carried[target.path]; ok {
		old = prior
	}
	next, err := change.set(target.keyPath, old)
	if err != nil {
		return userErr("%s: %v", change.settingsKey(), err)
	}
	if _, err := localcfg.Parse(next); err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    target.path,
		Old:     old,
		New:     next,
		What:    change.what,
		Kind:    target.kind,
		Command: command,
		Yes:     o.yes,
		DryRun:  secretsSettingsDryRun(o, target, change, old, next, collect),
	}); err != nil {
		return err
	}
	if carried != nil {
		// Where this record would leave the file, for the next record of it: the
		// answer the write itself gets by writing.
		carried[target.path] = next
	}
	// The hook runs for the write that happened. A dry run wrote nothing, and a
	// record the file already holds — an API root added twice — is the "no
	// change" the edit answers with, so neither owes a stage.
	if o.dryRun || !yamledit.Changed(old, next) {
		return nil
	}
	return runAfterWrite(cmd.Context(), cmd, afterWriteChange{Key: change.key, Store: secretsSettingsStore(change.key)})
}

// secretsSettingsChanges reads the three flags into the records this run
// writes, refusing a value that is not one of what the flag takes before
// anything is opened or read.
func secretsSettingsChanges(cmd *cobra.Command, o secretsOptions, name string) ([]secretsSettingsChange, error) {
	var out []secretsSettingsChange
	// An empty value is refused here, before anything is opened or read, and it
	// is refused for every flag that was GIVEN one: the route to this half is
	// decided from Changed and not from the value (settingsWrite), so `--expires
	// ""` and `--bitbucket-api-root ""` arrive as records of nothing rather than
	// as no flag at all. Left to fall through, the first would go on to the value
	// path and read a value, and the second would write the other flags of the
	// run and silently drop the record this one names.
	if cmd.Flags().Changed("expires") && o.expires == "" {
		return nil, userErr("--expires takes a date written YYYY-MM-DD, such as 2026-12-01, and this run passed it an empty value: an empty date is not a record, and dropping the flag is what stores a value — pass the date, or drop the flag to store one")
	}
	if cmd.Flags().Changed("bitbucket-api-root") && len(o.bitbucketAPIRoots) == 0 {
		return nil, userErr("--bitbucket-api-root takes an API root — an https origin such as https://api.bitbucket.org that reaches one host, with no userinfo, no query and no fragment — and this run passed it an empty value: an empty root adds nothing to bitbucket_api_roots, and dropping the flag is what stores a value — pass the root, or drop the flag to store one")
	}
	if cmd.Flags().Changed("remove-bitbucket-api-root") && len(o.removeBitbucketAPIRoots) == 0 {
		return nil, userErr("--remove-bitbucket-api-root takes an API root out of the list, so it takes the same value --bitbucket-api-root does — an https origin such as https://api.bitbucket.org, with no userinfo, no query and no fragment — and this run passed it an empty value: an empty root removes nothing, and dropping the flag is what stores a value — pass the root, or drop the flag to store one")
	}
	if cmd.Flags().Changed("expires") {
		if name == "" {
			return nil, userErr("--expires records the day one secret lapses, so it needs the secret's NAME: pass it (fugaro secrets set NAME --expires YYYY-MM-DD), or use --rotation-days and --bitbucket-api-root, which are records of the installation rather than of one secret")
		}
		date, err := secretsSettingsExpiry(o.expires)
		if err != nil {
			return nil, err
		}
		out = append(out, secretsSettingsChange{
			key:  "secret_expires",
			name: name,
			what: fmt.Sprintf("record in secret_expires that %s expires on %s, the day fugaro doctor warns ahead of", pluginwire.Printable(name), date),
			set:  func(path []string, old []byte) ([]byte, error) { return yamledit.SetScalar(old, path, date) },
		})
	}
	if cmd.Flags().Changed("rotation-days") {
		if o.rotationDays < 0 {
			return nil, userErr("--rotation-days takes the period in days this installation means to go between rotations, and a negative one is not a period: pass the number of days (30 for a month, 90 for a quarter), or `fugaro secrets set --rotation-days 0` to record none")
		}
		days := strconv.Itoa(o.rotationDays)
		out = append(out, secretsSettingsChange{
			key:  "secret_rotation_days",
			what: fmt.Sprintf("record secret_rotation_days as %s days, the period this installation means to go between rotations", days),
			set:  func(path []string, old []byte) ([]byte, error) { return yamledit.SetScalar(old, path, days) },
		})
	}
	for _, root := range o.bitbucketAPIRoots {
		if problem := secretsAPIRootProblem(root); problem != "" {
			return nil, userErr("--bitbucket-api-root takes an API root — an https origin such as https://api.bitbucket.org that reaches one host, with no userinfo, no query and no fragment — and the value given %s. %s", problem, secretsNoEcho)
		}
		out = append(out, secretsSettingsChange{
			key:  "bitbucket_api_roots",
			what: fmt.Sprintf("add %s to bitbucket_api_roots, the roots a run may send the Bitbucket token to", pluginwire.Printable(root)),
			// A root already in the list is left alone and the run answers "no
			// change": the key is the owner's list, and this flag adds to it
			// rather than writing it whole, so no invocation of this command
			// can drop a root the owner put there. --remove-bitbucket-api-root
			// is the other direction, and the two are the whole of what this
			// command does to the list.
			set: func(path []string, old []byte) ([]byte, error) { return secretsAddAPIRoot(old, path, root) },
		})
	}
	for _, root := range o.removeBitbucketAPIRoots {
		if problem := secretsAPIRootProblem(root); problem != "" {
			return nil, userErr("--remove-bitbucket-api-root takes an API root out of the list, so it takes the same values --bitbucket-api-root does — an https origin such as https://api.bitbucket.org, with no userinfo, no query and no fragment — and the value given %s. %s", problem, secretsNoEcho)
		}
		out = append(out, secretsSettingsChange{
			key:  "bitbucket_api_roots",
			what: fmt.Sprintf("take %s out of bitbucket_api_roots, the roots a run may send the Bitbucket token to", pluginwire.Printable(root)),
			set:  func(path []string, old []byte) ([]byte, error) { return secretsRemoveAPIRoot(old, path, root) },
		})
	}
	return out, nil
}

// secretsAddAPIRoot adds one root to bitbucket_api_roots, and
// secretsRemoveAPIRoot takes one out; both are the rules below. AddListItem and
// RemoveListItemWithOptions come first in each: those are the edits that keep
// the file's comments and the shape the owner gave the list, and a root the list does not
// hold (in either direction) is left alone rather than rewritten. The removal
// takes the key away with the last root, which is how this program spells a
// list with no items — an empty one is a different record, and the loader tells
// the two apart.
//
// A flow-style list is the one shape the editor refuses: bitbucket_api_roots:
// ["https://api.bitbucket.org"] is a shape the loader accepts, this program's
// own fixtures write and the operator's hand writes, and a refusal here would
// leave the two flags unable to reach the list the help names. It is the
// answer the family's policy lists already give (secretsPolicyAddRepos,
// secretsPolicyRemoveRepos): the list is written again in the block style this
// program writes one in, from the roots the file already names, so nothing the
// owner put there is lost with the shape it was written in; the diff shows the
// reformatting, so it is never silent.
func secretsAddAPIRoot(old []byte, path []string, root string) ([]byte, error) {
	next, err := yamledit.AddListItem(old, path, root)
	if err == nil {
		return next, nil
	}
	// The refusal is kept under its own name: the fallback reads it, and a nil
	// one (the parse below overwrites err) is the caller's to report rather
	// than a flow collection's to rewrite.
	refusal := err
	if yamledit.ReasonOf(refusal) != yamledit.ReasonFlowCollection {
		return nil, refusal
	}
	have, err := secretsAPIRootsIn(old)
	if err != nil {
		return nil, err
	}
	return settingsSetListFallback(old, path, append(have, root), refusal)
}

// secretsRemoveAPIRoot takes one root out of the list, under the rules above.
func secretsRemoveAPIRoot(old []byte, path []string, root string) ([]byte, error) {
	next, err := yamledit.RemoveListItemWithOptions(old, path, root, yamledit.RemoveOptions{DropEmptyParents: true})
	if err == nil {
		return next, nil
	}
	refusal := err
	if yamledit.ReasonOf(refusal) != yamledit.ReasonFlowCollection {
		return nil, refusal
	}
	have, err := secretsAPIRootsIn(old)
	if err != nil {
		return nil, err
	}
	want := make([]string, 0, len(have))
	for _, held := range have {
		if held != root {
			want = append(want, held)
		}
	}
	return settingsSetListFallback(old, path, want, refusal)
}

// secretsAPIRootsIn is the list as the file holds it, which is what a rewrite
// of a flow-style list is built from: the shape is being replaced, the roots
// are not.
func secretsAPIRootsIn(old []byte) ([]string, error) {
	lc, err := localcfg.Parse(old)
	if err != nil {
		return nil, fmt.Errorf("the local config does not read back: %w", err)
	}
	return lc.BitbucketAPIRoots, nil
}

// secretsSettingsExpiry is --expires read as the date the record holds: the
// canonical YYYY-MM-DD the loader and doctor both read (a date typed without
// its padding is written with it), refused when it is not a date at all and
// when it is behind today.
func secretsSettingsExpiry(value string) (string, error) {
	date, err := time.Parse(localcfg.SecretExpiryLayout, value)
	if err != nil {
		return "", userErr("--expires takes a date written YYYY-MM-DD, such as 2026-12-01, and the value given is not one: secret_expires records the day a secret lapses, which is what fugaro doctor warns ahead of. %s", secretsNoEcho)
	}
	// The comparison is between two dates and not two instants, so a run either
	// side of midnight reads the same file the same way.
	now := secretsSettingsNow()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if date.Before(today) {
		return "", userErr("--expires takes a day that has not passed, and this one is behind today (%s): secret_expires records when a secret lapses, so a date behind today records one that already has — rotate the secret at its provider, store the new value, and record the new date",
			today.Format(localcfg.SecretExpiryLayout))
	}
	return date.Format(localcfg.SecretExpiryLayout), nil
}

// secretsAPIRootProblem is what is wrong with a --bitbucket-api-root value, or
// "" when nothing is. The rules are the node's own (projectpolicy.
// ValidBitbucketAPIRoots, which `fugaro project policy publish` holds this key
// to when it copies it into the published node): an https root, one host, no
// userinfo, no query and no fragment.
//
// The sentences are this file's because the node's quote the value — "…: %q is
// not a URL" — and that quoting is the one thing this flag may not do: it is a
// root and never a credential, and the person most likely to type a token into
// it is the one who would read it back out of the refusal. The rule is kept in
// step with the node's by TestSecretsAPIRootRefusalHoldsTheNodesRule, which
// asks both for a verdict on the same values and fails when the two disagree.
func secretsAPIRootProblem(root string) string {
	u, err := url.Parse(root)
	switch {
	case err != nil:
		return "is not a URL"
	case u.Scheme != "https":
		return "is not an https URL: the runner sends the Bitbucket token to this root, so an http root would send it in clear"
	case u.Host == "":
		return "names no host: an API root is an origin such as https://api.bitbucket.org"
	case u.User != nil:
		return "carries userinfo, and a credential belongs in a secret rather than in the config"
	case u.RawQuery != "" || u.Fragment != "":
		return "carries a query or a fragment, and an API root is the origin a request is built against, matched exactly after one trailing \"/\""
	}
	return ""
}

// secretsSettingsDryRun is the --dry-run answer for one record: the settings
// write path's own document, for the key this record writes, handed to the run
// that prints it rather than printed per record (collect).
func secretsSettingsDryRun(o secretsOptions, target settingsTarget, change secretsSettingsChange, old, next []byte, collect *[]settingsWriteDoc) *settingsDryRun {
	if !o.dryRun {
		return nil
	}
	return &settingsDryRun{
		key:      change.settingsKey(),
		layer:    target.layer,
		oldValue: settingsValueIn(old, target.keyPath),
		newValue: settingsValueIn(next, target.keyPath),
		jsonOut:  o.asJSON,
		collect:  collect,
	}
}

// secretsSettingsCommand is this write spelled whole, for the refusal a coding
// agent's session reads in applySettingsEdit: the person runs it at their own
// terminal, so the flags this run was given are the flags it carries and no
// others, each value quoted so a name from the command line cannot become a
// second word of the command they paste.
func secretsSettingsCommand(cmd *cobra.Command, o secretsOptions, name string) string {
	parts := []string{selfCommand(), "secrets", "set"}
	if name != "" {
		parts = append(parts, quoteWord(name))
	}
	if o.expires != "" {
		parts = append(parts, "--expires", quoteWord(o.expires))
	}
	if cmd.Flags().Changed("rotation-days") {
		parts = append(parts, "--rotation-days", strconv.Itoa(o.rotationDays))
	}
	for _, root := range o.bitbucketAPIRoots {
		parts = append(parts, "--bitbucket-api-root", quoteWord(root))
	}
	for _, root := range o.removeBitbucketAPIRoots {
		parts = append(parts, "--remove-bitbucket-api-root", quoteWord(root))
	}
	if o.layer != "" {
		parts = append(parts, "--layer", o.layer)
	}
	return strings.Join(parts, " ")
}
