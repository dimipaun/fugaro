package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runner"
)

// codeSpanRE finds one `code span`.
var codeSpanRE = regexp.MustCompile("`([^`]+)`")

// commandWordRE is one word of a command path a code span spells.
var commandWordRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// shortFormRE matches the words a text uses for the form a person reads — the
// short ID (written with a space or a hyphen), the ending two runs are told
// apart by, the last four or more characters of a run ID — and fullFormRE the
// words for the form a command is given. Claims are read by these words rather
// than by one phrasing, so a rule reworded into "give `fugaro logs` the short
// ID" or "`fugaro logs` takes the run's ending" is read the same way the plan's
// own sentence was. negationRE matches the words that turn a sentence into a
// rule not to do something, contractions included, so that a sentence carrying
// one is reported rather than read (shortFormClaims).
var (
	shortFormRE = regexp.MustCompile(`(?i)\b(short(?:er|est)?(?:[ -]run)?[ -]ID|last (?:four|4)(?: or more)? characters|ending)`)
	fullFormRE  = regexp.MustCompile(`(?i)\bfull(?: run)? ID\b`)
	negationRE  = regexp.MustCompile(`(?i)(\bnever\b|\bnot\b|\bcannot\b|\w+n't\b)`)
	sentenceRE  = regexp.MustCompile(`[.;]\s+`)
)

// abbreviationRE matches the two abbreviations whose own period is not the end
// of a sentence. Splitting after one of them would cut an example off the claim
// it belongs to — "pass it to e.g. `fugaro logs`" would leave a sentence with
// the words of the rule and none of the command — and the claim would go with
// it.
var abbreviationRE = regexp.MustCompile(`\b(?:e\.g\.|i\.e\.)`)

// heldPeriod stands in for an abbreviation's period while a line is split, so
// that the split sees the sentences a reader does. It is a character no text
// carries, and it is put back before anything is reported.
const heldPeriod = "\x00"

// flagWordRE is one flag of a command path a code span spells, without the
// value a flag may be written with: `--retry` in `--retry=<run>` as much as in
// `--retry <run>`.
var flagWordRE = regexp.MustCompile(`^--[a-z][a-z0-9-]*$`)

// runFlagHelpRE matches a flag's own help naming a run as the value the flag
// takes: `--retry`'s "the stored task of RUN", `--run-id`'s "run ID to use".
// The capitals are the argument a flag's help spells in capitals, the slug form
// is the other way it writes one, and "run id" in any case is the third. This
// is what tells a flag that names a run from one that does not: `--json`
// carries both forms without taking either. It is also why the flags a text is
// held to are read off the tree rather than listed here — a flag added later is
// covered the day it is written, as long as its help says which form it takes.
var runFlagHelpRE = regexp.MustCompile(`(\bRUN\b|<repo-slug>/<run-id>|(?i:\brun id\b))`)

// endingHelpRE matches a command's or a flag's own help *offering* an ending,
// the last four or more characters of a run ID: the RUN argument SR14 gives
// `logs`, `diagnose` and `artifacts` when it wires them to resolveRun. It names
// the form as this CLI spells it rather than the word "ending", because a help
// may mention endings to say the opposite and a flag is held to its help alone:
// `--retry`'s "a run an ending names is never launched, since the bucket cannot
// vouch for which run an ending means" is the one a keyword would read as an
// offer, and the flag launches a run, so no probe can refuse it. A help that
// words an offer some other way fails the check and says so, the message naming
// the form the help asks for. A command is not left to this reading, though: a
// help may state a refusal in an offer's own words — cancel's "is the one
// command that acts on a run and takes no ending. Given the last four or more
// characters of a run ID it cancels nothing" — and it is the probe above, which
// every command the check names gets, that refuses that one
// (TestEndingHelpDistinguishesOfferFromWarning).
var endingHelpRE = regexp.MustCompile(`(?i)(characters? of a run ID|short(er)? run ID)`)

// sentences splits a text into the units a claim is made in: a line of a
// markdown file, or one sentence of it. A claim is read inside one of these, so
// a command is never matched to the form words of the sentence before it. The
// periods of "e.g." and "i.e." are held out of the split (abbreviationRE),
// because they end no sentence and cutting after one would take an example's
// command away from the words that claim it.
func sentences(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = abbreviationRE.ReplaceAllStringFunc(line, func(abbr string) string {
			return strings.ReplaceAll(abbr, ".", heldPeriod)
		})
		for _, sentence := range sentenceRE.Split(line, -1) {
			out = append(out, strings.ReplaceAll(sentence, heldPeriod, "."))
		}
	}
	return out
}

// commandIn returns the command a code span names — the words after `fugaro`,
// or the span's own, stopping at the first word that is not a command — and ""
// for a span that names none: a placeholder (`<repo-slug>/<run-id>`), a flag
// (`--run-id`, which flagWords reads instead), a record's field (`run`) or
// another document (`working`).
//
// A flag written before the command is stepped over, because a span spells a
// whole command line and a global flag may come first: `fugaro --json cancel`
// names `cancel`. After the command the path has begun, and there a flag ends
// it like any other argument does (`fugaro run --pr N --retry <run>` names
// `run`) — which is also what keeps a flag's value from being read as a word of
// the command: the words past the flag are never looked at.
func commandIn(span string) string {
	words := strings.Fields(span)
	if len(words) > 0 && words[0] == "fugaro" {
		words = words[1:]
	}
	var path []string
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			if len(path) > 0 {
				break
			}
			continue
		}
		if !commandWordRE.MatchString(w) {
			break
		}
		path = append(path, w)
	}
	return strings.Join(path, " ")
}

// commandsAndLeaves returns c, and every command under it when c is a group: a
// rule naming `artifacts` promises the form for all of what it runs.
func commandsAndLeaves(c *cobra.Command) []*cobra.Command {
	subs := c.Commands()
	if len(subs) == 0 {
		return []*cobra.Command{c}
	}
	var out []*cobra.Command
	for _, sub := range subs {
		out = append(out, commandsAndLeaves(sub)...)
	}
	return out
}

// runArgLeaves returns the commands under c that take a run as their argument:
// `logs RUN`, `artifacts get RUN [NAME]`. A text naming any other command —
// `fugaro runs ls`, a record's `run` field — claims nothing about the form a
// run is named in, and running the launcher is no test's business: the flags
// that do name a run are read from their own help instead (flagClaims).
func runArgLeaves(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, leaf := range commandsAndLeaves(c) {
		if words := strings.Fields(leaf.Use); len(words) > 1 && strings.HasPrefix(words[1], "RUN") {
			out = append(out, leaf)
		}
	}
	return out
}

// flagWords returns every flag a code span names, in the order the span writes
// them, and nil for a span that names none. Every flag, not the first alone: a
// span spells a command line with its arguments — `fugaro run --pr N --retry
// <run>` — and the run-taking flag may be any of them, so reading only the
// first would drop the claim as quietly as reading none. A value written onto
// the flag is cut from the word, so that `--retry=<run>` names `--retry` the
// way the spaced form does.
func flagWords(span string) []string {
	var out []string
	for _, w := range strings.Fields(span) {
		if i := strings.IndexByte(w, '='); i >= 0 {
			w = w[:i]
		}
		if flagWordRE.MatchString(w) {
			out = append(out, w)
		}
	}
	return out
}

// flagClaims returns the commands whose flag of this name takes a run, as
// "cmd --flag": a flag belongs to the command that reads it, and a text writing
// `--run-id` on its own names the flag without naming that command.
func flagClaims(root *cobra.Command, flag string) []string {
	var out []string
	for _, cmd := range commandsAndLeaves(root) {
		fl := cmd.Flags().Lookup(strings.TrimPrefix(flag, "--"))
		if fl == nil || !runFlagHelpRE.MatchString(fl.Usage) {
			continue
		}
		out = append(out, strings.TrimPrefix(cmd.CommandPath(), "fugaro ")+" "+flag)
	}
	return out
}

// claimNames returns what a code span names as taking a run, in the form the
// checker resolves it by: a command that takes one as its argument ("logs"), or
// a flag whose own help names a run ("run --retry"). A span that names neither
// claims nothing about the form a run is given — a placeholder, a record's
// field, another document, or a flag that takes something else, `--json` being
// the one the texts carry.
//
// A span is read for all of it, and everything any part of it names is claimed:
// one span writes `fugaro cancel --json`, which names the command as surely as
// `fugaro cancel` does, and a flag that takes no run — `--json`, in that same
// span — must not be what silences it; another writes `fugaro run --pr N
// --retry <run>`, where the run-taking flag is the span's second one. Reading
// part of a span alone would drop the claim, and a dropped claim is the one
// thing this guard promises never to do: the text is reported, or checked,
// never skipped.
func claimNames(root *cobra.Command, span string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(names ...string) {
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	for _, flag := range flagWords(span) {
		add(flagClaims(root, flag)...)
	}
	name := commandIn(span)
	if name == "" {
		return out
	}
	cmd, _, err := root.Find(strings.Fields(name))
	if err != nil || cmd == nil || cmd == root || len(runArgLeaves(cmd)) == 0 {
		return out
	}
	add(name)
	return out
}

// splitFlagClaim splits a "cmd --flag" claim, and reports false for a claim
// that names a command alone.
func splitFlagClaim(name string) (cmdPath, flag string, ok bool) {
	if i := strings.Index(name, " --"); i >= 0 {
		return name[:i], name[i+1:], true
	}
	return name, "", false
}

// shortFormClaims returns what this CLI has that a text names as taking a short
// run ID — a command, or a flag whose help names a run — and the sentences no
// scan can read: one that names a command beside the full form in one breath,
// and one that negates anything at all. A negation is not a licence to skip the
// sentence — "never pass the short ID to `fugaro cancel`" claims the opposite,
// but "if the run has not finished, pass the short ID to `fugaro logs`" claims
// the rule, and no scan tells a negation of the claim from a negation elsewhere
// in the sentence. Reporting both keeps the promise that this guard holds
// whatever words a rule is written in: the text is reworded, rather than read
// the wrong way. A claim is every code span of a readable sentence about the
// short form alone that resolves to something taking a run, a command or a
// flag; a span naming something else is no claim either way, because a flag
// that takes no run is not a claim about the form a run is given.
//
// A sentence that names no command and ends in a colon is the third thing this
// scan cannot read: "Pass the short ID to these:" and, on the line below,
// "`fugaro cancel`" make one claim a person reads at once and this scan reads
// as two sentences, neither of them naming both halves. A sentence is not
// joined to the lines under it — a scan that guessed which lines belong
// together would guess wrong about prose as ordinary as "the forms, in order:"
// — so the sentence the colon makes a promise in is reported instead, and the
// text is written with its command in the sentence that names the form.
func shortFormClaims(root *cobra.Command, text string) (claims, unreadable []string) {
	seen := map[string]bool{}
	report := func(sentence string) {
		if trimmed := strings.TrimSpace(sentence); !seen[trimmed] {
			seen[trimmed] = true
			unreadable = append(unreadable, trimmed)
		}
	}
	for _, sentence := range sentences(text) {
		if len(shortFormRE.FindAllStringIndex(sentence, -1)) == 0 {
			continue
		}
		negated := negationRE.MatchString(sentence)
		full := len(fullFormRE.FindAllStringIndex(sentence, -1)) > 0
		named := false
		for _, span := range codeSpanRE.FindAllStringSubmatchIndex(sentence, -1) {
			names := claimNames(root, sentence[span[2]:span[3]])
			if len(names) == 0 {
				continue
			}
			named = true
			if negated || full {
				report(sentence)
				continue
			}
			claims = append(claims, names...)
		}
		if !named && strings.HasSuffix(strings.TrimSpace(sentence), ":") {
			report(sentence)
		}
	}
	return claims, unreadable
}

// shortIDRuleTexts returns every text that carries the rule for an agent: the
// job prompt, which every implement and fix stage gets, and every plugin skill
// file.
func shortIDRuleTexts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{
		"the job prompt": runner.SystemPrompt(runner.PromptData{Branch: "fugaro/x", Base: "main", StateDir: "/s"}, ""),
	}
	for path, text := range skillTexts(t) {
		out[path] = text
	}
	return out
}

// endingRefusalText runs one command path with an ending in place of the run
// and returns what the CLI said when it refused the ending as a name it cannot
// resolve, "" when the command took it. A refusal is a lookup's message for a
// ref that names no run, and SR14 has landed (cloud.go): locateRun's "is
// neither <repo-slug>/<run-id> nor a run ID", resolveRun's "no run ending ...
// among the active runs or the runs the bucket still holds" (D1709) and "no
// active run ending ..." for its active-only readers, and — what the probe
// below reads, because it runs `cancel`, a destructive command that lists its
// matches instead of acting (D1710) — "<ending>" names N active runs: ... pass
// one of the full run IDs above. Each phrase is the refusal's own wording, so
// the last one is the sentence cloud.go prints after the list and not the
// "is an ending" the same refusal was drafted with. A vocabulary missing one
// would call a command that did refuse one whose silence this test then trusts.
func endingRefusalText(t *testing.T, path, ending string) string {
	t.Helper()
	out, errOut, err := execute(t, append(strings.Fields(path), ending)...)
	if err == nil {
		return ""
	}
	msg := err.Error() + " " + errOut + " " + out
	for _, refusal := range []string{"is neither", "no run ending", "no active run ending", "pass one of the full run IDs above"} {
		if strings.Contains(msg, refusal) {
			return strings.TrimSpace(msg)
		}
	}
	return ""
}

// TestShortIDRuleNamesOnlyCommandsThatTakeOne: the rule the job prompt and the
// plugin skills carry tells an agent to pass a run's short ID to a command.
// That is a claim about this CLI, so it is checked here, where the commands
// are: a command the rule names that way must take one, or the agent is sent to
// a command that refuses the ID it was told to pass. SR14 has landed on main --
// `logs`, `diagnose` and `artifacts` go through resolveRun and take an ending,
// and `cancel` refuses one by listing the runs it names (D1710) -- so the
// commands a text may name now have to accept what it says they take, and the
// texts that ship name none: the rule asks a command for the ID its own help
// asks for. This is the test that fails the moment a text names a command that
// refuses an ending, whatever words the sentence is written in. A sentence the
// scan cannot read is reported rather than skipped (shortFormClaims), so a rule
// cannot slip past by phrasing itself around the scan.
//
// The rule names a flag as readily as a command -- "`fugaro run --retry` takes
// the short ID" -- and a flag is the one shape of claim that cannot be run: the
// command behind it launches a run. So a flag is held to its own help, which is
// where the form it takes is stated, and to nothing else; that is a weaker
// promise than the commands get, and it is the strongest one a launcher admits.
func TestShortIDRuleNamesOnlyCommandsThatTakeOne(t *testing.T) {
	// The ending fixture, not a bare cloud one: `cancel` now reads the live
	// runs to refuse an ending, and an installation with no budget database
	// answers a lookup that cannot see them with an error rather than a
	// refusal (F6). A probe run against that installation would read the
	// config error, report the command as one that took the ending, and fail
	// on a command that had in fact refused it.
	f := newEndingFixture(t)
	id := recentRunID(t, time.Minute, "abcd")
	seedTitledRun(t, f.cloudFixture, id, "the run")
	f.db.Set(budget.PathAgent(appSlug, id), map[string]any{"run_id": id, "title": "the run"})
	root := NewRootCmd()

	// The scan is exercised on the wordings a rule may use, because a guard
	// that knows one phrasing passes whatever the texts say once they change.
	// The two negations are the trap: skipping a sentence because it carries
	// one reads "if the run has not finished, pass the short ID to `fugaro
	// logs`" as a rule not to, and lets the claim through. No scan tells a
	// negation of the claim from a negation elsewhere in the sentence, so both
	// are reported and the text fails the guard either way.
	for _, c := range []struct {
		text       string
		short      []string
		unreadable []string
	}{
		{"Pass the short ID to `logs`, `diagnose` and `artifacts` (the commands that accept it)", []string{"logs", "diagnose", "artifacts"}, nil},
		{"Give `fugaro logs` the short ID", []string{"logs"}, nil},
		{"`diagnose` takes the short ID", []string{"diagnose"}, nil},
		{"`fugaro logs` takes the last four characters of a run ID", []string{"logs"}, nil},
		{"`fugaro logs`, `fugaro diagnose` and `fugaro artifacts` take the full ID", nil, nil},
		{"The `run` field of a row is the full ID, and a plan task is its name", nil, nil},
		{"Never pass the short ID to `fugaro cancel`", nil, []string{"Never pass the short ID to `fugaro cancel`"}},
		{"If the run has not finished, pass the short ID to `fugaro logs` to follow it.", nil, []string{"If the run has not finished, pass the short ID to `fugaro logs` to follow it."}},
		{"When the user does not want the run, give `fugaro cancel` the short ID.", nil, []string{"When the user does not want the run, give `fugaro cancel` the short ID."}},
		{"`fugaro logs` takes the short ID and `fugaro cancel` the full ID", nil, []string{"`fugaro logs` takes the short ID and `fugaro cancel` the full ID"}},

		// The words the rule is written in: the hyphen, the ending, and an
		// example whose "e.g." must not cut the command off the claim.
		{"Give `fugaro logs` the short-ID", []string{"logs"}, nil},
		{"`fugaro logs` takes the run's ending", []string{"logs"}, nil},
		{"Pass the short ID to e.g. `fugaro logs`", []string{"logs"}, nil},
		{"Pass the short ID to `fugaro logs`, i.e. the command that follows a run", []string{"logs"}, nil},

		// The flags that name a run, named the two ways a text writes them.
		{"Give `fugaro run --retry` the short ID", []string{"run --retry"}, nil},
		{"`--review-run` takes the short ID", []string{"run --review-run"}, nil},
		{"Pass the short ID to `fugaro run --run-id`", []string{"run --run-id"}, nil},
		{"Never give `fugaro run --retry` the short ID", nil, []string{"Never give `fugaro run --retry` the short ID"}},
		{"`fugaro run --retry` takes the short ID and `--run-id` the full ID", nil, []string{"`fugaro run --retry` takes the short ID and `--run-id` the full ID"}},

		// A span naming a command and a flag at once claims the command: the
		// flag is read where it names a run, and a flag that takes no run
		// (`--json` here) is not what makes the command's claim disappear.
		{"Pass the short ID to `fugaro cancel --json`", []string{"cancel"}, nil},
		{"`fugaro logs --json` takes the short ID", []string{"logs"}, nil},

		// A span spells a whole command line, and the flag that takes the run
		// is not always its first word nor its own: `--pr` carries a pull
		// request number, and the value may be written onto the flag.
		{"Give `fugaro run --pr N --retry <run>` the short ID", []string{"run --retry"}, nil},
		{"Pass the short ID to `fugaro run --retry=<run>`", []string{"run --retry"}, nil},
		{"Give `fugaro --json cancel` the short ID", []string{"cancel"}, nil},
		{"`fugaro runs ls --pr N --json` takes the short ID", nil, nil},

		// A flag that takes no run claims nothing, whatever the sentence says
		// about it: `--json` carries both forms without being given either.
		{"`--json` prints the short ID", nil, nil},
		{"`fugaro runs ls --group-by` takes the short ID", nil, nil},

		// The form a markdown list writes its claim in: the sentence that says
		// what follows names no command, and the command is on the line after
		// it. The introducing sentence is reported, so the text fails rather
		// than passing with the claim unread; the colon inside a sentence that
		// does name its command is nothing to report.
		{"Pass the short ID to these:\n- `fugaro cancel`", nil, []string{"Pass the short ID to these:"}},
		{"The rule is simple: give `fugaro logs` the short ID", []string{"logs"}, nil},
	} {
		short, unreadable := shortFormClaims(root, c.text)
		if !slices.Equal(short, c.short) || !slices.Equal(unreadable, c.unreadable) {
			t.Errorf("shortFormClaims(%q) = %v, %v, want %v, %v", c.text, short, unreadable, c.short, c.unreadable)
		}
	}

	// The probe, exercised on the one command the rule names as taking the full
	// ID: cancel never acts on an ending -- locateRun refuses it today, and
	// SR14 has it list the matches and exit as a user error (D1710) -- so a
	// probe that cannot see this refusal would pass every claim below.
	if refusal := endingRefusalText(t, "cancel", "abcd"); refusal == "" {
		t.Fatalf("fugaro cancel abcd -- an ending -- was not refused: %s is the form cancel acts on, and the probe above cannot be trusted until this one is refused", id)
	}

	for path, text := range shortIDRuleTexts(t) {
		claims, unreadable := shortFormClaims(root, text)
		for _, sentence := range unreadable {
			t.Errorf("%s carries a sentence this guard cannot read -- it names a command beside both the short and the full form, it negates something, or it ends in a colon with the command on the line below: %q -- one form per sentence, no negation in a sentence that names a command, and the command in the sentence that names the form, so this guard can check it", path, sentence)
		}
		for _, name := range claims {
			if cmdPath, flag, ok := splitFlagClaim(name); ok {
				cmd, _, err := root.Find(strings.Fields(cmdPath))
				if err != nil || cmd == nil {
					t.Errorf("%s tells an agent to pass a short ID to `%s`, and no such command exists", path, name)
					continue
				}
				fl := cmd.Flags().Lookup(strings.TrimPrefix(flag, "--"))
				if fl == nil {
					t.Errorf("%s tells an agent to pass a short ID to `%s`, and %s has no such flag", path, name, cmd.CommandPath())
					continue
				}
				if !endingHelpRE.MatchString(fl.Usage) {
					t.Errorf("%s tells an agent to pass a short ID to `%s`, and the flag's own help offers none: %q -- a flag's help is where the form it takes is stated, so wire the flag to a lookup that takes an ending in the same change that names it here, or name the form the help asks for", path, name, fl.Usage)
				}
				continue
			}
			cmd, _, err := root.Find(strings.Fields(name))
			if err != nil || cmd == nil {
				continue
			}
			for _, leaf := range runArgLeaves(cmd) {
				help := leaf.Long + " " + leaf.Short + " " + leaf.Use
				if !endingHelpRE.MatchString(help) {
					t.Errorf("%s tells an agent to pass a short ID to `%s`, and %s's own help offers none: wire the command to resolveRun (SR14) in the same change that names it here, or name the form the help asks for", path, name, leaf.CommandPath())
				}
				if refusal := endingRefusalText(t, strings.TrimPrefix(leaf.CommandPath(), "fugaro "), "abcd"); refusal != "" {
					t.Errorf("%s tells an agent to pass a short ID to `%s`, and %s refused one: %s", path, name, leaf.CommandPath(), refusal)
				}
			}
		}
	}
}

// TestEndingHelpDistinguishesOfferFromWarning: the help check above is only as
// good as what it reads as an offer of an ending, and on a flag the reading is
// the whole guard: `fugaro run --retry` launches a run, so no probe can refuse
// an ending for it. Its help mentions endings twice and offers neither — "a run
// an ending names is never launched, since the bucket cannot vouch for which
// run an ending means" — so a check that matched the word would pass a text
// that claimed a short ID for the flag, the false claim it exists to catch. A
// help that offers one has to read as one for the same reason in reverse: the
// check fails a text naming `logs`, `diagnose` or `artifacts` if their help
// stops reading as an offer, and those are the commands SR14 wired to
// resolveRun.
//
// Only a flag is held to the reading. A command's help may state the refusal in
// the very words of an offer — cancel's "takes no ending. Given the last four
// or more characters of a run ID it cancels nothing" — and no reading of prose
// tells that offer from that refusal; running the command does, which is what
// the check above does, and why cancel is not in this test.
func TestEndingHelpDistinguishesOfferFromWarning(t *testing.T) {
	root := NewRootCmd()
	helpOf := func(cmd *cobra.Command) string { return cmd.Long + " " + cmd.Short + " " + cmd.Use }
	find := func(path string) *cobra.Command {
		t.Helper()
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil || cmd == nil {
			t.Fatalf("no %s in this CLI: %v", path, err)
		}
		return cmd
	}

	for _, path := range []string{"logs", "diagnose", "artifacts ls", "artifacts get"} {
		if help := helpOf(find(path)); !endingHelpRE.MatchString(help) {
			t.Errorf("%s reads an ending through resolveRun and its help does not read as an offer: %q -- a text naming it as a command that takes a short ID would fail the help check on a command that takes one", path, help)
		}
	}

	retry := find("run").Flags().Lookup("retry")
	if retry == nil {
		t.Fatal("no --retry on `fugaro run`: the flag this test exists for is gone")
	}
	if endingHelpRE.MatchString(retry.Usage) {
		t.Errorf("`fugaro run --retry` takes a full ID only -- an ending names a run the bucket cannot vouch for -- and its help reads as an offer: %q -- a text naming the flag as one that takes a short ID would pass, and no probe can refuse it: the help is the whole guard on a flag that launches a run", retry.Usage)
	}
}
