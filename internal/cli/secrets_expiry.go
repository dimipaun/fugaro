package cli

import (
	"fmt"
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
// settings write of its own — its own diff, its own backup, its own --dry-run
// answer and its own after-write hook — so two flags in one invocation are two
// writes of two keys and never one write of two, which is what the settings
// write path is built for (one edit, one key, one document).
func secretsSetSettings(cmd *cobra.Command, o secretsOptions, name string) error {
	if err := settingsDryRunFlags(settingsWriteOptions{dryRun: o.dryRun, jsonOut: o.asJSON, yes: o.yes}); err != nil {
		return err
	}
	// --repo and --project-wide say where a VALUE is stored. These records are
	// this machine's own knowledge about secrets, in the local config: no
	// repository owns one and the project-wide copy has none, so either flag
	// beside them would be read as narrowing a write it cannot narrow.
	if cmd.Flags().Changed("repo") || cmd.Flags().Changed("project-wide") {
		return userErr("--repo and --project-wide say where a secret's value is stored, and --expires, --rotation-days and --bitbucket-api-root record what this machine knows about secrets in its own local config: a record has no repository's copy and no project-wide one — drop the flag")
	}
	changes, err := secretsSettingsChanges(cmd, o, name)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		// The only way here is a bare --layer: every other spelling that
		// reaches this path names one of the three flags.
		return userErr("--layer says where a settings write lands and goes with --expires, --rotation-days or --bitbucket-api-root: a secret's own value is stored in Secret Manager, which has no layer")
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	command := secretsSettingsCommand(cmd, o, name)
	for _, change := range changes {
		if err := secretsSettingsRecord(cmd, o, change, sel, lc, command); err != nil {
			return err
		}
	}
	return nil
}

// secretsSettingsRecord writes one record: the row's own layer, the file the
// settings resolver names for it, the edit the change carries, and then the
// stages the write owes.
func secretsSettingsRecord(cmd *cobra.Command, o secretsOptions, change secretsSettingsChange, sel localcfg.Selection, lc *localcfg.Config, command string) error {
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
		DryRun:  secretsSettingsDryRun(o, target, change, old, next),
	}); err != nil {
		return err
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
	if o.expires != "" {
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
			return nil, userErr("--rotation-days takes the period in days this installation means to go between rotations, and a negative one is not a period: pass the number of days (30 for a month, 90 for a quarter), or `fugaro settings unset secret_rotation_days` to record none")
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
			// change": the key is the owner's list, and the flag adds to it
			// rather than writing it whole, so no invocation of this command
			// can drop a root the owner put there. Taking one away is
			// `fugaro settings set bitbucket_api_roots --remove URL`.
			set: func(path []string, old []byte) ([]byte, error) { return yamledit.AddListItem(old, path, root) },
		})
	}
	return out, nil
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
// write path's own document, for the key this record writes.
func secretsSettingsDryRun(o secretsOptions, target settingsTarget, change secretsSettingsChange, old, next []byte) *settingsDryRun {
	if !o.dryRun {
		return nil
	}
	return &settingsDryRun{
		key:      change.settingsKey(),
		layer:    target.layer,
		oldValue: settingsValueIn(old, target.keyPath),
		newValue: settingsValueIn(next, target.keyPath),
		jsonOut:  o.asJSON,
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
	if o.layer != "" {
		parts = append(parts, "--layer", o.layer)
	}
	return strings.Join(parts, " ")
}
