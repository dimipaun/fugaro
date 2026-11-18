package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro rigor security` — M4 of
// docs/config-ownership/implementation-plan.md, which is D2613's
// `rigor security add|rm` in the family G6 opened beside it.
//
// The command writes the two halves of docs/security-classification's block:
// the list `security.patterns` — one row per tree a change has to touch to be
// reviewed as a security-sensitive change, each row a name, a doublestar glob
// and one of seven categories — and the project layer's own
// `security.always_level_3`, the switch that puts every run of the project at
// review level 3 whatever paths it changed (D1501, D1503, D1517).
//
// A row is addressed by its NAME and not by its glob, which is where this
// command parts company with `fugaro rigor set` beside it. A rigor rule is
// addressed by its glob because one glob addresses one rule
// (internal/config's validateRigor, F14); a security row is addressed by its
// name because the name is what identity means in this list: the list in force
// is the engine's floor, then the project layer's rows, then the repository's,
// unioned by row name, and the first layer that writes a name keeps it (D1506).
// The union's key field is therefore the name, and it is D1163's rule for an
// object list exactly — `add --name NAME` writes the row that name holds,
// creating it when the list does not carry it yet, and `rm --name NAME` takes
// it away.
//
// Both layers the key has are reached (config.Scopes: `security.patterns` is
// project and repo): the layer file `fugaro update` publishes is the default,
// as it is for `fugaro rigor set`, and `--layer repo` writes a checkout's own
// `fugaro.yaml`, where the rows are live once the owner's branch is merged to
// the base branch and nowhere before (the composite D1564, and this command
// commits, pushes and opens nothing). The switch is the layer file's alone: a
// repository's `security:` block is its rows and nothing else, so a repository
// that could write always_level_3 would be answering a project's control from
// below (D1501), and `--always-level-3 --layer repo` is refused in those words
// rather than by a row lookup that says less.
//
// The two directions are not the same size, which is what the classification
// below is: an `add` that creates a row, or the switch turned on, can only ever
// escalate more — a change matching one more row reaches review level 3, and
// D1517 makes the switch turn on and never off. What lowers the level is a
// `rm`, and, on an `add`, a row the list already carries whose MATCH the edit
// replaces: the paths the row named before stop being escalated unless another
// row names them. All three are `Typed` (D2617): the owner's own step, refused
// in a coding agent's session whatever the gate says and whatever --yes says,
// and printed with the sentence saying what it lowers at the owner's terminal
// (D1552, D1555, D1026 — a Typed edit is never gate-admitted and no prompt
// stands in front of it, D1540).
//
// The write itself is the one write path (settings_write.go), exactly as every
// other settings write is: settingsResolveTarget resolves the row and the
// layer, the diff and the atomic write are applySettingsEdit's, and the two
// rules that stand in front of a layer write — the reserved keys and the keys
// that run as shell — are settingsLayerGate's. This file decides three things
// of its own: which row a verb addresses, what the edited document is, and
// which edits are the lowering direction.
//
// The five registry rows are the family's (internal/settings' rows for
// `security.patterns`, the layer's three row fields and its switch), and this
// command's writer table is writers_security.go.

// The three registry rows this command writes, by the spelling the settings
// write path looks a row up by. `security.patterns` is one row in both files
// this command edits — the layer file states it as the file's own `security:`
// block, a checkout's fugaro.yaml states the same key — so a write of the list
// reaches the layers the row HAS, and `--layer local` is refused by the
// resolver naming both of them.
const (
	securityPatternsKey = "security.patterns"
	securityAlwaysKey   = "layer.security.always_level_3"
)

// securityCategoryPlaceholder is the word a registered argv uses where the row's
// category goes. It is this family's own word and not one of the contract's
// three (KeyPlaceholder, ValuePlaceholder and the per-segment `<n>`): the
// command takes one category per row and the registry row states none, so the
// template names the word the way the help does and ArgvFor leaves it as
// written (writers_security.go, contract §2).
const securityCategoryPlaceholder = "<category>"

// securityOptions are `fugaro rigor security add|rm`'s flags.
type securityOptions struct {
	project string
	config  string
	// layer is --layer: where the write lands. Empty is the project layer file,
	// which is what a bare `fugaro rigor set` writes too; `repo` is a checkout's
	// own fugaro.yaml, which --repo names.
	layer string
	// repo is --repo: the repository a `--layer repo` write is for, as
	// owner/name or as a path to its checkout. Empty is the checkout the
	// command runs in.
	repo string
	// repoPath is the checkout --repo named when it named one by path, for the
	// reason rigorOptions.repoPath states: the reduction to owner/name loses
	// which clone that was, and the path is what the write lands in.
	repoPath string
	// name is --name: the row to write, addressed by the name the union keeps it
	// by. Required unless the write is the switch.
	name string
	// match is --match: the row's glob. Required to create a row, and optional
	// for a row the document already carries, where omitting it keeps the glob
	// the row has.
	match string
	// category is --category: one of sensitivity.Categories. Required to create
	// a row, and optional for one that exists, where omitting it keeps the
	// category the row has.
	category string
	// always is --always-level-3: the project layer's switch, instead of a row
	// of the list. `add` writes it on and `rm` writes it off, and it is the
	// layer file's alone.
	always bool
	// yes is the settings family's --yes, which no flag of this command sets:
	// the flag is T11's to remove, and it stands in the conditions the fence and
	// the gate are decided under (settingsGateReads, settingsEdit.Yes).
	yes bool
	// ask is --ask, the settings family's own: in a project whose marks say it
	// is managed, ask the confirmation this write would otherwise get instead of
	// writing on the gate's verdict. It never stands in for the session fence: a
	// removal is refused in a coding agent's session whatever --ask says.
	ask bool
}

// newRigorSecurityCmd is `fugaro rigor security`: the paths this project's and
// this repository's runs treat as security-sensitive.
func newRigorSecurityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "security",
		Short: "Add or remove a security path, or the project's always-level-3 switch",
		Long: `Write the paths a change has to touch to be reviewed as a security-sensitive change, and the switch that puts every change at review level 3.

A row is addressed by its name: ` + "`fugaro rigor security add --name NAME --match GLOB --category CATEGORY`" + ` writes the row that name holds, adding it when the list does not carry it yet — its glob and its category are then required — and replacing the glob and the category of the row it names when it does. A row already in the list keeps the value its flag does not state. ` + "`fugaro rigor security rm --name NAME`" + ` takes the row away whole.

` + "`--always-level-3`" + ` writes the switch instead of a row: ` + "`add`" + ` turns it on and ` + "`rm`" + ` turns it off. It is the project layer file's alone — a repository's ` + "`security:`" + ` block is its own rows and nothing else — and it puts every change of every repository of the project at review level 3, which is the answer D1503 gives a ` + "`**`" + ` pattern: a pattern that matches everything is not a pattern, it is this switch.

A removal, and an add that gives a row already in the list another glob, are the lowering direction: a coding agent's session is refused them with the diff and the command to run at your own terminal, and your own terminal writes them and asks nothing. Adding a row, or turning the switch on, only ever escalates more.

With no ` + "`--layer`" + ` the write lands in this machine's own project layer file, the one ` + "`fugaro update`" + ` publishes, and it ends with ` + "`next: fugaro update`" + `. ` + "`--layer repo`" + ` writes a checkout's own ` + "`fugaro.yaml`" + ` instead, where the rows are live once your branch is merged to the base branch — this command commits, pushes and opens nothing. ` + "`--repo`" + ` names that repository, as owner/name or as a path to its checkout; without it the checkout the command runs in is the one written. A layer the key does not have is refused naming the layers it does: ` + "`local`" + ` is not one of them.`,
	}
	cmd.AddCommand(newRigorSecurityAddCmd())
	cmd.AddCommand(newRigorSecurityRmCmd())
	return cmd
}

func newRigorSecurityAddCmd() *cobra.Command {
	var o securityOptions
	cmd := &cobra.Command{
		Use:   "add (--name NAME --match GLOB --category CATEGORY | --always-level-3) [--layer LAYER] [--repo REPO]",
		Short: "Add a security path, or turn the project's always-level-3 switch on",
		Long: `Add one row to the security list, or turn the project layer's switch on.

The row is addressed by its name. A name the list does not carry yet is added, and its glob and its category are then both required: a row with no glob is one no matcher can use, and a category outside the seven the classifier labels matches with is a row the loader refuses. A name the list already carries has its glob and its category replaced by the ones the flags state, and keeps the ones it has for a flag that is left out — so a row the list carries is corrected rather than duplicated, and one call writes one row.

Giving such a row another glob is the lowering direction: the paths the row named before stop being escalated to review level 3 unless another row names them, so it is the step a coding agent's session is refused. Adding a row is not, and ` + "`--always-level-3`" + ` only puts every change of the project at review level 3.

` + "`--always-level-3`" + ` writes the layer file's own switch and takes no --name, --match or --category. Turning it off is ` + "`fugaro rigor security rm --always-level-3`" + `, and no layer writes it for one repository: a repository's ` + "`security:`" + ` block is its rows and nothing else.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRigorSecurity(cmd, o, false)
		},
	}
	f := securityFlags(cmd, &o)
	f.StringVar(&o.match, "match", "", "the row's glob, relative to the repository root (e.g. internal/creds/**); required for a name the list does not carry yet, and optional for one it does")
	f.StringVar(&o.category, "category", "", "the row's category: "+sensitivity.CategoryList()+"; required for a name the list does not carry yet, and optional for one it does")
	f.BoolVar(&o.always, "always-level-3", false, "write the project layer's own switch instead of a row: turn it on, so every change of every repository of the project reaches review level 3")
	return cmd
}

func newRigorSecurityRmCmd() *cobra.Command {
	var o securityOptions
	cmd := &cobra.Command{
		Use:   "rm (--name NAME | --always-level-3) [--layer LAYER] [--repo REPO]",
		Short: "Remove a security path, or turn the project's always-level-3 switch off",
		Long: `Take one row out of the security list, or turn the project layer's switch off.

The row is taken away whole, by its name: a name the list does not carry is not a failure, and the command says so and writes nothing. A removal is the lowering direction — the paths the row named stop being escalated to review level 3 unless another row names them — so a coding agent's session is refused it with the diff, and your own terminal writes it and asks nothing. It is not a re-aim: a row that should name other paths is corrected with ` + "`fugaro rigor security add`" + `, which refuses the same session for the same reason.

` + "`--always-level-3`" + ` turns the layer file's own switch off: with it off, a run of the project reaches review level 3 through the paths its list matches and the per-run ` + "`fugaro run --security`" + ` request, and nothing else. A layer that is not carrying the switch already is not a failure either.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRigorSecurity(cmd, o, true)
		},
	}
	f := securityFlags(cmd, &o)
	f.BoolVar(&o.always, "always-level-3", false, "write the project layer's own switch instead of a row: turn it off, so a change is escalated by the paths the list matches and by nothing else")
	return cmd
}

// securityFlags are the flags both verbs take: where the write lands, in which
// project, and the switch. --name is registered here rather than per verb
// because both address a row by its name; the glob and the category are the
// add's alone, since a removal takes the row away whole.
func securityFlags(cmd *cobra.Command, o *securityOptions) *pflag.FlagSet {
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.StringVar(&o.layer, "layer", "", "where the write lands: project (this machine's project layer file, the default) or repo (a checkout's own fugaro.yaml, where the rows are live once your branch is merged to the base branch)")
	f.StringVar(&o.repo, "repo", "", "the repository a --layer repo write is for, as owner/name or as a path to its checkout; without it, the checkout the command runs in")
	f.StringVar(&o.name, "name", "", "the row to write, by the name that is its identity in the union of the three layers")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmation this write would otherwise get instead of writing on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the question back. Refused where nobody can answer it (a coding agent's session, a pipe); a no-op with a sentence in a project that is not managed")
	return f
}

// runRigorSecurity is both verbs of `fugaro rigor security`, in the order the
// siblings take: the flags, the vocabulary, the target, the old document, the
// edit, the loader's read of it, the classification, and then the one write
// path with the gate and the fence beside it.
func runRigorSecurity(cmd *cobra.Command, o securityOptions, remove bool) error {
	verb := "add"
	if remove {
		verb = "rm"
	}
	// The switch and a row are two writes of one verb, and one call writes one
	// of them: the flags that address a row and the one that names the switch
	// are refused together rather than resolved in some order.
	if o.always && (o.name != "" || o.match != "" || o.category != "") {
		return userErr("--always-level-3 is the project layer's switch and not a row of the list: it takes no --name, --match or --category, and `fugaro rigor security %s --always-level-3` is the whole of what writes it; nothing was written", verb)
	}
	if !o.always && o.name == "" {
		return userErr("one write names one thing: `fugaro rigor security %s --name NAME` writes a row of the list by its name, and `--always-level-3` writes the project layer's switch; nothing was written", verb)
	}
	if !o.always && remove && (o.match != "" || o.category != "") {
		// The two flags are not registered on rm, so this is the sentence for a
		// caller that reached the body another way: a removal takes the row
		// away whole, and a row that should name other paths is an add.
		return userErr("a removal takes the row away whole, so --match and --category are the add's: `fugaro rigor security add --name %s --match GLOB --category CATEGORY` rewrites the row; nothing was written", safetext.Field(o.name))
	}
	// The switch's layers are the project layer file's alone (D1501). The
	// refusal is this command's rather than the row's layers answer, because the
	// reason is one a person has to read: a repository's security: block is its
	// rows and nothing else, and the row is refused at --layer repo by a sentence
	// about a registry spelling.
	if o.always && o.layer == settingsLayerRepo {
		return userErr("security.always_level_3 is the project layer's alone: a repository's security: block is its rows and nothing else, because putting every change of the project at review level 3 is not one repository's call (D1501); `fugaro rigor security %s --always-level-3 --layer project` writes it; nothing was written", verb)
	}
	// The layer is the project layer file's unless --layer names another, as it
	// is for `fugaro rigor set`: its path is derived from the project's name, and
	// it is the layer `fugaro update` publishes.
	layer := o.layer
	if layer == "" {
		layer = settingsLayerProject
	}
	key := securityPatternsKey
	if o.always {
		key = securityAlwaysKey
	}
	row, ok := settings.Lookup(key)
	if !ok {
		// The registry is in this binary, so a row that is not there is this
		// program's own mistake and not the person's.
		return fmt.Errorf("internal: the settings registry holds no %s row", key)
	}
	sel, lc, err := settingsAimedSelection(settingsWriteOptions{project: o.project, config: o.config})
	if err != nil {
		return err
	}
	// The raw --repo travels to the resolver for every layer, so that a --repo
	// on a write that lands elsewhere is refused in the resolver's own words
	// rather than silently ignored; the repository branch replaces it with the
	// resolved name.
	opts := settingsWriteOptions{layer: layer, repo: o.repo, project: o.project, config: o.config}
	repo := ""
	if layer == settingsLayerRepo {
		if o.repoPath, err = repoArgPath(cmd.Context(), o.repo); err != nil {
			return err
		}
		if repo, err = repoArg(cmd.Context(), settingsAim(o.project, o.config), o.repo); err != nil {
			return err
		}
		opts.repoPath = o.repoPath
		_, here := checkoutOriginAt("")
		if repo == "" {
			repo = here
		}
		if repo == "" {
			return userErr("a --layer repo write names the repository: `%s --repo owner/name`, or run it from that repository's checkout", securityCommand(o, remove))
		}
		if here != "" && strings.EqualFold(here, repo) {
			opts.repo = ""
		} else {
			opts.repo = repo
		}
	}
	target, err := settingsResolveTarget(cmd.Context(), row, opts, lc, sel)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	// The document's own loader is what says a project layer file is a project
	// layer and a checkout's fugaro.yaml is a fugaro.yaml, and it is where the
	// list's two rules live: a name the engine's floor already spends and a name
	// written twice in one list are refused by validateSecurity, at the row's own
	// index, and neither is a rule this file may restate (D1502, D1506). Reading
	// first is also what makes the edit: it is the document the row's fields are
	// written against.
	block, problems := securityReadDocument(target, old)
	if len(problems) > 0 {
		return userErr("%s: %s", target.path, problems[0].String())
	}
	name := strings.Join(target.keyPath, ".")
	// A state that is already the answer is not a failure (D852), and for these
	// three the answer is said rather than written: a removal whose row or switch
	// the document does not carry, and a switch already on, write nothing. The
	// alternative — writing the bytes the edit would produce anyway — is a diff
	// that changes no value, and for `rm --always-level-3` on a layer that never
	// wrote the key it would write the key to say false.
	switch {
	case remove && o.always && !block.always:
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s is not set in it\n", target.path, name)
		return nil
	case remove && !o.always && block.row(o.name) == nil:
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s carries no security path named %s\n", target.path, name, safetext.Field(o.name))
		return nil
	}
	next, changed, err := securityEdit(old, target, block, o, remove)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s already reads what was asked for\n", target.path, name)
		return nil
	}
	if err := settingsReparse(target, next); err != nil {
		return err
	}
	typed, why := securityClassification(target, block, o, remove)
	edit := settingsEdit{
		Path:     target.path,
		Old:      old,
		New:      next,
		What:     securityWhat(o, target, remove),
		Typed:    typed,
		TypedWhy: why,
		Kind:     target.kind,
		Command:  securityCommand(o, remove),
		Yes:      o.yes,
		Ask:      o.ask,
	}
	// The gate is read where this command would ask, exactly as the other layer
	// writes read it (M0). A session's own refusal comes first at the write path
	// and whatever --yes says, which is why the read is skipped for the widening
	// class at the owner's terminal and nowhere else (settingsGateReads): a
	// removal in a session reaches this call, and the fence below is what refuses
	// it.
	managed, gate, err := settingsManagedStep(cmd.Context(), lc, sel.Path, target, key, typed, why, old, next, "rigor security", settingsGateReads(cmd, o.yes, o.ask, typed))
	if err != nil {
		return err
	}
	// The layer document's own two refusals, after the gate and never instead of
	// it: the reserved keys and the keys that run as shell (spec §3.6, D1175). A
	// security edit moves neither, and the check is here because this is a layer
	// write and a later change to what the key holds would otherwise bypass it.
	// A repository's own file is no layer file, and settingsLayerGate answers
	// nothing for it.
	if err := settingsLayerGate(cmd, target, settingsWriteOptions{layer: layer}, key, old, next); err != nil {
		return err
	}
	if err := askDecide(cmd.OutOrStdout(), askFlags{ask: o.ask}, managed.Managed, settingsEditConditions(cmd, edit)); err != nil {
		return err
	}
	edit.Managed = managed
	if err := applySettingsEdit(cmd, lc, edit); err != nil {
		return err
	}
	// The record is written after the write and for a write that happened
	// (D1014).
	if err := gate.recorded(cmd.Context()); err != nil {
		return err
	}
	if target.kind == settingsDocRepo {
		fmt.Fprintln(cmd.OutOrStdout(), repoWorkTreeLine(repo))
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// securityBlock is one document's own security configuration, in the vocabulary
// the edit and the classification are written in: the rows of its list, and —
// for the project layer file — the switch beside them. A repository's own file
// has no switch: config.Security is the rows and nothing else, so the field is
// false there whatever the file says, and the file that says otherwise is
// refused by its own loader.
type securityBlock struct {
	rows   []sensitivity.Pattern
	always bool
}

// row is the row this list holds under name: nil when it holds none. It is the
// one address a row is found by, and the comparison is the name as written —
// the same comparison RemoveSeqItemByKey and UpsertSeqItemByKey address the
// item by.
func (b securityBlock) row(name string) *sensitivity.Pattern {
	for i := range b.rows {
		if b.rows[i].Name == name {
			return &b.rows[i]
		}
	}
	return nil
}

// securityReadDocument reads the document a write is about in that document's
// own loader and answers the security block it carries. It is rigorReadDocument
// for this block: the project layer file is read by config.ParseProjectLayer
// against the anchor the write path reads it against, so a layer file that is
// not a layer file is refused before any rule below is asked, and a checkout's
// fugaro.yaml is read by config.Parse, the reader `fugaro validate` and every
// run of that file read.
//
// The problems are the loader's own, unwrapped: the caller words them. A
// document that carries no security: block at all answers the zero block — no
// rows and the switch off — which is exactly how a resolver reads a document
// that names none.
func securityReadDocument(target settingsTarget, data []byte) (securityBlock, []config.Problem) {
	if target.kind == settingsDocRepo {
		cfg, ps := config.Parse(data)
		if len(ps) > 0 {
			return securityBlock{}, ps
		}
		block := securityBlock{}
		if cfg.Security != nil {
			block.rows = cfg.Security.Patterns
		}
		return block, nil
	}
	l, ps := config.ParseProjectLayer(data, target.anchor)
	if len(ps) > 0 {
		return securityBlock{}, ps
	}
	block := securityBlock{}
	if l.Security != nil {
		block.rows, block.always = l.Security.Patterns, l.Security.AlwaysLevel3
	}
	return block, nil
}

// securityEdit builds the edited document and says whether it differs from the
// one it was built from. The two verbs are the two edits yamledit has for a
// sequence of mappings addressed by a key field, and the switch is one scalar:
//
//   - the row of a name the list does not carry yet is added with the glob and
//     the category the flags state, and both are required for it — a row with no
//     glob is one no matcher can use, and a category outside the seven is a row
//     the loader refuses;
//   - the row of a name it does carry has the glob and the category replaced by
//     the ones the flags state, and keeps the value of a flag that is absent, so
//     a call that states one field does not blank the other;
//   - a removal takes the row away whole, leaving every other row, and the
//     switch writes one boolean.
//
// The row is held to sensitivity.Pattern.Validate before a byte is built. That
// is the row's own check — the name's character set, the doublestar glob, the
// `*`/`**` refusal that names always_level_3, the alternation refusal and the
// category vocabulary — so the refusal a person reads is the rule's own words
// rather than a second copy of them here, and a row the matcher could not use
// never reaches the file.
func securityEdit(old []byte, target settingsTarget, block securityBlock, o securityOptions, remove bool) ([]byte, bool, error) {
	if o.always {
		// The switch writes one value and never clears the key: false and an
		// absent key are the same statement to every reader (D1517), and the key
		// alone is what the job-image gates judge a project against — so a layer
		// that carries it keeps carrying it, and `rm` is the layer saying off.
		on := "true"
		if remove {
			on = "false"
		}
		next, err := yamledit.SetScalar(old, target.keyPath, on)
		if err != nil {
			return nil, false, userErr("%s: %v", strings.Join(target.keyPath, "."), err)
		}
		return next, yamledit.Changed(old, next), nil
	}
	if remove {
		next, err := yamledit.RemoveSeqItemByKey(old, target.keyPath, "name", o.name)
		if err != nil {
			return nil, false, userErr("%s: %v", strings.Join(target.keyPath, "."), err)
		}
		return next, yamledit.Changed(old, next), nil
	}
	held := block.row(o.name)
	match, category := o.match, o.category
	if held != nil {
		if match == "" {
			match = held.Match
		}
		if category == "" {
			category = string(held.Category)
		}
	}
	var missing []string
	if match == "" {
		missing = append(missing, "--match GLOB")
	}
	if category == "" {
		missing = append(missing, "--category CATEGORY")
	}
	if len(missing) > 0 {
		return nil, false, userErr("%s carries no security path named %s yet, so it takes %s: the row is written whole, and a row with no glob or no category is one the loader refuses (sensitivity.Pattern.Validate); nothing was written",
			strings.Join(target.keyPath, "."), safetext.Field(o.name), strings.Join(missing, " and "))
	}
	written := sensitivity.Pattern{Name: o.name, Match: match, Category: sensitivity.Category(category)}
	if msg := written.Validate(); msg != "" {
		return nil, false, userErr("the security path %s: %s; nothing was written", safetext.Field(o.name), msg)
	}
	// The name is the key field, so it is not one of the fields written: the
	// upsert addresses the item by it, and yamledit refuses an upsert that
	// states its own key field twice.
	next, err := yamledit.UpsertSeqItemByKey(old, target.keyPath, "name", o.name, map[string]string{"match": match, "category": category})
	if err != nil {
		return nil, false, userErr("%s: %v", strings.Join(target.keyPath, "."), err)
	}
	return next, yamledit.Changed(old, next), nil
}

// securityClassification is the edit's direction (D2617): whether it is the
// `Typed` step, which a coding agent's session is refused whatever the gate
// says, and the sentence saying what it lowers, which the owner's own terminal
// reads beside the diff.
//
// Three edits lower. A removal takes a row away, or turns the project's switch
// off. An add that gives a row the list already carries another glob takes the
// paths that row named away from the list — the one lowering an add can be, and
// the reason this is not simply "the removals are Typed": the row a person
// re-aims is the row an agent could re-aim, and then the owner merges a branch
// whose security list no longer names what it named. The two other adds escalate
// at most: a new row can only add matches, and D1517 makes the switch turn on
// and never off.
func securityClassification(target settingsTarget, block securityBlock, o securityOptions, remove bool) (bool, string) {
	switch {
	case remove && o.always:
		return true, "this lowers the project's review level by turning security.always_level_3 off: every run of this project's branches stops being put at review level 3 whatever paths it changed, and this layer is where the switch is stated"
	case remove:
		return true, securityRemovalWhy(target, o.name)
	case block.row(o.name) != nil && o.match != "" && block.row(o.name).Match != o.match:
		return true, securityRewriteWhy(target, o.name)
	}
	return false, ""
}

// securityRemovalWhy is the sentence a removal prints. The half of it that
// differs between the two layers is whose runs lose the escalation and which
// document the list is stated in, because that is the difference between the two
// writes a person is asked to weigh.
func securityRemovalWhy(target settingsTarget, name string) string {
	if target.kind == settingsDocRepo {
		return fmt.Sprintf("this lowers this repository's security list by removing the security path %s: a change touching the paths it names stops being escalated to review level 3 in every run of its branches, and this is the file where the list is stated", safetext.Field(name))
	}
	return fmt.Sprintf("this lowers the project's security list by removing the security path %s: a change touching the paths it names stops being escalated to review level 3 in every run of the project's branches, and this is the layer where the list is stated", safetext.Field(name))
}

// securityRewriteWhy is the sentence an add that re-aims an existing row prints.
// It is a removal's sentence with one thing more to say: the row stays, so what
// left the list is the glob it named and not the name it is read by.
func securityRewriteWhy(target settingsTarget, name string) string {
	if target.kind == settingsDocRepo {
		return fmt.Sprintf("this lowers this repository's security list by giving the security path %s another glob: the paths the row named before stop being escalated to review level 3 unless another row names them, and this is the file where the list is stated", safetext.Field(name))
	}
	return fmt.Sprintf("this lowers the project's security list by giving the security path %s another glob: the paths the row named before stop being escalated to review level 3 unless another row names them, and this is the layer where the list is stated", safetext.Field(name))
}

// securityWhat is the sentence naming what changes, printed before the write
// (settingsEdit's own What). The key is the document's own spelling of it, so
// the sentence names what a person will find in the file they are about to
// edit.
func securityWhat(o securityOptions, target settingsTarget, remove bool) string {
	name := strings.Join(target.keyPath, ".")
	if o.always {
		return fmt.Sprintf("set %s to %t in %s", name, !remove, target.path)
	}
	if remove {
		return fmt.Sprintf("remove the security path %s from %s in %s", safetext.Field(o.name), name, target.path)
	}
	return fmt.Sprintf("add the security path %s to %s in %s", safetext.Field(o.name), name, target.path)
}

// securityCommand is this edit spelled for a person's own terminal, which is
// what a refusal in a coding agent's session prints. The flags are the ones that
// were given, so the command a person pastes is the edit that was refused and
// nothing more: an add that keeps a row's glob does not state the one it keeps.
func securityCommand(o securityOptions, remove bool) string {
	parts := []string{"fugaro", "rigor", "security"}
	if remove {
		parts = append(parts, "rm")
	} else {
		parts = append(parts, "add")
	}
	if o.always {
		parts = append(parts, "--always-level-3")
	} else {
		parts = append(parts, "--name", quoteWord(o.name))
		if o.match != "" {
			parts = append(parts, "--match", quoteWord(o.match))
		}
		if o.category != "" {
			parts = append(parts, "--category", quoteWord(o.category))
		}
	}
	// --layer and --repo only when they were given: the project layer file is
	// the layer a bare command writes, and a --repo filled in from the checkout
	// the command ran in is that same checkout.
	if o.layer != "" {
		parts = append(parts, "--layer", quoteWord(o.layer))
	}
	if o.repo != "" {
		parts = append(parts, "--repo", quoteWord(o.repo))
	}
	if o.project != "" {
		parts = append(parts, "--project", quoteWord(o.project))
	}
	if o.config != "" {
		parts = append(parts, "--config", quoteWord(o.config))
	}
	return strings.Join(parts, " ")
}
