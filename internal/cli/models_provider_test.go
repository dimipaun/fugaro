package cli

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/settings"
)

// The two commands this file is about run against the models family's own
// fixture (modelsWriteExtra, models_write_test.go): project aurora in a config
// home of its own, a route named `openrouter` with a comment above it, a
// model_prices entry of its own, and no coding-agent marker set — so a write
// that is meant to land is not refused by the session the test binary happens
// to run in.

// TestProviderSetWritesEveryLeaf is the write D2613 asks for: every leaf of a
// providers entry one command can set, written in one edit into the world's own
// project config, read back through the loader as the values that were passed,
// with the comment above the route and the leaf this command does not write
// (`models`) exactly as they were.
func TestProviderSetWritesEveryLeaf(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, _, err := execute(t, "models", "provider", "set", "openrouter",
		"--kind", "anthropic-compat",
		"--base-url", "https://openrouter.ai/api/v2",
		"--auth", "x-api-key",
		"--secret", "openrouter-rotated-key",
		"--price-policy", "highest",
		"--require-zdr=false",
		"--route-fee-pct", "7.5",
		"--allow-data-to", "owner/repo-a, owner/repo-b")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	p := modelsWriteLocalConfig(t, path).Providers["openrouter"]
	if p.Kind != "anthropic-compat" {
		t.Errorf("kind = %q", p.Kind)
	}
	if p.BaseURL != "https://openrouter.ai/api/v2" {
		t.Errorf("base_url = %q", p.BaseURL)
	}
	if p.Auth != "x-api-key" {
		t.Errorf("auth = %q", p.Auth)
	}
	if p.Secret != "openrouter-rotated-key" {
		t.Errorf("secret = %q, want the name it was handed", p.Secret)
	}
	if p.PricePolicy != "highest" {
		t.Errorf("price_policy = %q", p.PricePolicy)
	}
	if p.RequireZDR == nil || *p.RequireZDR {
		t.Errorf("require_zdr = %v, want an explicit false", p.RequireZDR)
	}
	if p.RouteFeePct != 7.5 {
		t.Errorf("route_fee_pct = %v", p.RouteFeePct)
	}
	if want := []string{"owner/repo-a", "owner/repo-b"}; !slices.Equal(p.AllowDataTo, want) {
		t.Errorf("allow_data_to = %v, want %v", p.AllowDataTo, want)
	}
	// The leaf no flag of this command writes is not its to change, and the
	// comment above the route is the file's own.
	if want := []string{"deepseek/*", "vendor/model-x"}; !slices.Equal(p.Models, want) {
		t.Errorf("models = %v, want the claim untouched: %v", p.Models, want)
	}
	after := readFileString(t, path)
	if !strings.Contains(after, "# the owner's route") {
		t.Errorf("the edit dropped the comment above the route:\n%s", after)
	}
	if !strings.Contains(out, "providers.openrouter carries") {
		t.Errorf("the write names nothing about what the entry carries now:\n%s", out)
	}
}

// TestProviderSetRefusesANameTheFileDoesNotCarry: a provider entry is not one
// without the model IDs it serves, and no flag of this command writes that list
// — so a new name is refused with the two ways an entry comes to exist rather
// than written as a block the loader would then refuse to read back.
func TestProviderSetRefusesANameTheFileDoesNotCarry(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)

	out, _, err := execute(t, "models", "provider", "set", "fresh", "--kind", "anthropic-compat")
	if err == nil {
		t.Fatalf("an entry that cannot read back was written:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	for _, want := range []string{"providers.fresh", "models", "docs/multi-model.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write touched the file:\n%s", got)
	}
}

// TestProviderSecretIsAReferenceNeverAValue is the plan's red step: before
// modelsProviderSecretRef a value that looks like a provider key was accepted
// and written into the `secret:` leaf, which is the one thing that leaf must
// never carry. The probe is built at run time from pieces — this repository
// carries no secret-shaped literal in a test — and what is asserted about it is
// both halves of the rule: the value is refused, and the refusal never repeats
// it back, because a %q of it is exactly the leak the check exists to prevent.
func TestProviderSecretIsAReferenceNeverAValue(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)
	value := "sk-" + strings.Repeat("a1b2c3d4", 4)

	out, _, err := execute(t, "models", "provider", "set", "openrouter", "--secret", value)
	if err == nil {
		t.Fatalf("a value was accepted where the name of a secret belongs:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	for _, printed := range []string{out, err.Error()} {
		if strings.Contains(printed, value) {
			t.Errorf("the refusal echoed the value it refused:\n%s", printed)
		}
	}
	for _, want := range []string{"--secret", "fugaro secrets set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write touched the file:\n%s", got)
	}

	// The reference is the other half of the leaf: a name is carried, the file
	// holds that name, and the value is nowhere in it.
	if out, _, err = execute(t, "models", "provider", "set", "openrouter", "--secret", "openrouter-rotated-key"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	p := modelsWriteLocalConfig(t, path).Providers["openrouter"]
	if p.Secret != "openrouter-rotated-key" {
		t.Errorf("secret = %q, want the name that was passed", p.Secret)
	}
	if got := readFileString(t, path); strings.Contains(got, value) {
		t.Errorf("the value reached the file:\n%s", got)
	}
}

// TestPriceSetWritesTheLeaves is the price half of D2613: one leaf of an entry
// the file carries, a whole new entry, and the two refusals a model_prices key
// has — an entry that is not there needs both rates (the loader's own rule), and
// an alias is not a key the block carries.
func TestPriceSetWritesTheLeaves(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, _, err := execute(t, "models", "price", "set", "vendor/model-x", "--input-per-m", "2.5", "--cache-read", "0.4")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	entry := modelsWriteLocalConfig(t, path).ModelPrices["vendor/model-x"]
	if entry.InputPerM == nil || *entry.InputPerM != 2.5 {
		t.Errorf("input_per_m = %v, want 2.5", entry.InputPerM)
	}
	if entry.OutputPerM == nil || *entry.OutputPerM != 2 {
		t.Errorf("output_per_m = %v, want the untouched 2", entry.OutputPerM)
	}
	if entry.CacheRead == nil || *entry.CacheRead != 0.4 {
		t.Errorf("cache_read = %v, want 0.4", entry.CacheRead)
	}
	if entry.CacheWrite5m != nil {
		t.Errorf("cache_write_5m = %v, want it left at its default", entry.CacheWrite5m)
	}

	out, _, err = execute(t, "models", "price", "set", "vendor/model-new",
		"--input-per-m", "1", "--output-per-m", "2", "--web-search-per-1k", "0.01")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	fresh := modelsWriteLocalConfig(t, path).ModelPrices["vendor/model-new"]
	if fresh.InputPerM == nil || *fresh.InputPerM != 1 || fresh.OutputPerM == nil || *fresh.OutputPerM != 2 {
		t.Errorf("the new entry carries %v in and %v out, want 1 and 2", fresh.InputPerM, fresh.OutputPerM)
	}
	if fresh.WebSearchPer1k == nil || *fresh.WebSearchPer1k != 0.01 {
		t.Errorf("web_search_per_1k = %v, want 0.01", fresh.WebSearchPer1k)
	}

	// A model ID may carry a dot, and it is one name whatever dots it carries:
	// the key `model_prices.moonshotai/kimi-k2.5.input_per_m` is a spelling of
	// the row `model_prices.*.input_per_m` even though splitting both on the dot
	// counts four segments against three.
	out, _, err = execute(t, "models", "price", "set", "moonshotai/kimi-k2.5", "--input-per-m", "0.6", "--output-per-m", "2.5")
	if err != nil {
		t.Fatalf("a model ID with a dot was refused: %v\n%s", err, out)
	}
	dotted := modelsWriteLocalConfig(t, path).ModelPrices["moonshotai/kimi-k2.5"]
	if dotted.InputPerM == nil || *dotted.InputPerM != 0.6 || dotted.OutputPerM == nil || *dotted.OutputPerM != 2.5 {
		t.Errorf("the dotted entry carries %v in and %v out, want 0.6 and 2.5", dotted.InputPerM, dotted.OutputPerM)
	}

	before := readFileString(t, path)
	out, _, err = execute(t, "models", "price", "set", "vendor/model-half", "--cache-read", "0.2")
	if err == nil {
		t.Fatalf("an entry with no input or output rate was written:\n%s", out)
	}
	for _, want := range []string{"--input-per-m", "--output-per-m", "model_prices.vendor/model-half"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write touched the file:\n%s", got)
	}

	if _, _, err = execute(t, "models", "price", "set", "sonnet", "--input-per-m", "1", "--output-per-m", "2"); err == nil || !strings.Contains(err.Error(), "sonnet") {
		t.Errorf("an alias was accepted as a model_prices key: %v", err)
	}
}

// TestProviderReadsNoStdin is the policy's half of this module (plan §2). The
// write runs with stdin at EOF — a closed pipe, which is the descriptor the
// harness hands every leaf — and the assertion that matters is the second one:
// the write landed. A command that read stdin would see EOF rather than a hang,
// so what a closed descriptor proves is that nothing the write needed was a
// person's answer. The removal and the price write are held to it too.
func TestProviderReadsNoStdin(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, errOut, err := modelsProviderRunClosedStdin(t, "models", "provider", "set", "openrouter", "--auth", "x-api-key")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if p := modelsWriteLocalConfig(t, path).Providers["openrouter"]; p.Auth != "x-api-key" {
		t.Errorf("auth = %q: the write did not land with stdin closed", p.Auth)
	}
	assertNoPrompt(t, out+errOut)

	out, errOut, err = modelsProviderRunClosedStdin(t, "models", "price", "set", "vendor/model-x", "--output-per-m", "3")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if e := modelsWriteLocalConfig(t, path).ModelPrices["vendor/model-x"]; e.OutputPerM == nil || *e.OutputPerM != 3 {
		t.Errorf("output_per_m = %v: the price write did not land with stdin closed", e.OutputPerM)
	}
	assertNoPrompt(t, out+errOut)

	out, errOut, err = modelsProviderRunClosedStdin(t, "models", "provider", "rm", "openrouter")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if _, ok := modelsWriteLocalConfig(t, path).Providers["openrouter"]; ok {
		t.Error("the route is still there: the removal did not land with stdin closed")
	}
	assertNoPrompt(t, out+errOut)
}

// modelsProviderRunClosedStdin runs the tree with a stdin at EOF, the way the
// closed-stdin harness does (noprompt_harness_test.go): the pipe's write end is
// closed before the run, so a read returns EOF and an *os.File is what makes
// stdinIsTerminal answer false for the unpatched reason too.
func modelsProviderRunClosedStdin(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	defer r.Close()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(r)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(NormalizeArgs(args))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// modelsProviderWideningLeaves are the four leaves D2617 names, each with the
// flag and the value a write of it carries.
var modelsProviderWideningLeaves = []struct {
	arg  string
	leaf string
}{
	{"--base-url=https://elsewhere.example.com/api", "base_url"},
	{"--allow-data-to=owner/repo-b", "allow_data_to"},
	{"--require-zdr=false", "require_zdr"},
	{"--route-fee-pct=9", "route_fee_pct"},
}

// TestProviderWideningRefusedInAnAgentSession is D2617 for this family, in its
// two observable halves. The first is the rule itself: each of the four leaves
// D2617 names is refused in a coding agent's session whatever the gate says —
// the diff is printed, the command to run at a terminal is named, and the file
// is left byte-identical. The loop is over agentenv.Markers itself rather than a
// copy, so a marker the tree adds is one these writes are held to.
//
// The second half is the classification the refusal rests on: the four leaves
// are the edit's `Typed` ones, and what says so on the owner's own terminal is
// the sentence naming what each widens — a write of one prints it, a write of a
// leaf that is not one of the four does not. Dropping the Typed marker from the
// write makes that half fail. (The first half does not move with it: a local
// project config is outside every repository, so an agent session is refused
// every write of it, Typed or not — which is the stricter of the two fences and
// is why the marker is carried for the sentence, the contract's vocabulary and
// the day this path's fence changes.)
func TestProviderWideningRefusedInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		for _, tc := range modelsProviderWideningLeaves {
			t.Run(marker+"/"+tc.leaf, func(t *testing.T) {
				_, path := modelsWriteHome(t)
				before := readFileString(t, path)
				t.Setenv(marker, "1")

				out, _, err := execute(t, "models", "provider", "set", "openrouter", tc.arg)
				if err == nil {
					t.Fatalf("the widening write landed in a coding agent's session:\n%s", out)
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
				}
				if !strings.Contains(err.Error(), marker) {
					t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
				}
				if !strings.Contains(err.Error(), "fugaro models provider set openrouter") {
					t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
				}
				if !strings.Contains(out, path) || !(strings.Contains(out, "  + ") || strings.Contains(out, "  - ")) {
					t.Errorf("the refusal does not print the diff of what it refused:\n%s", out)
				}
				if got := readFileString(t, path); got != before {
					t.Errorf("the file changed in a coding agent's session:\n%s", got)
				}
			})
		}
	}

	_, path := modelsWriteHome(t)
	out, _, err := execute(t, "models", "provider", "set", "openrouter", "--route-fee-pct", "9")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "this widens a provider route") || !strings.Contains(out, "route_fee_pct is added to every charge") {
		t.Errorf("a widening write does not say what it widens:\n%s", out)
	}
	if p := modelsWriteLocalConfig(t, path).Providers["openrouter"]; p.RouteFeePct != 9 {
		t.Errorf("route_fee_pct = %v: the owner's own write did not land", p.RouteFeePct)
	}

	out, _, err = execute(t, "models", "provider", "set", "openrouter", "--auth", "x-api-key")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "this widens a provider route") {
		t.Errorf("a write that widens nothing reads as a widening:\n%s", out)
	}
}

// TestProviderWriterRegistersEveryLeaf: every leaf the two commands write has a
// writer under its own pattern — one pattern per leaf, because the argv differs
// per leaf — whose store is the local project config, and whose argv is the
// command, the segment the key's name goes into, the leaf's own flag and the
// contract's value placeholder, in that order. A leaf left unwritten here is a
// row whose refusal would keep a family sentence and a conformance gap.
func TestProviderWriterRegistersEveryLeaf(t *testing.T) {
	type writerRow struct {
		key     string
		command string
		group   string
		name    string
		leaf    modelsWriterLeaf
	}
	var rows []writerRow
	for _, leaf := range modelsProviderWriterLeaves {
		rows = append(rows, writerRow{"providers.openrouter." + leaf.leaf, "fugaro models provider set", "provider", "openrouter", leaf})
	}
	for _, leaf := range modelsPriceWriterLeaves {
		rows = append(rows, writerRow{"model_prices.vendor/model-x." + leaf.leaf, "fugaro models price set", "price", "vendor/model-x", leaf})
	}
	for _, row := range rows {
		w, ok := settings.WriterOf(row.key)
		if !ok {
			t.Errorf("%s has no writer: its refusal would keep a family sentence", row.key)
			continue
		}
		if w.Store != settings.StoreLocal {
			t.Errorf("the writer of %s states store %q, want %q", row.key, w.Store, settings.StoreLocal)
		}
		if w.Command != row.command {
			t.Errorf("the writer of %s names %q, want %q", row.key, w.Command, row.command)
		}
		// The words from the leaf's flag on: the command and the placeholder the
		// key's own segment stands in are the two tests below, which read whether
		// this tree fills a `<1>` at all.
		argv := w.ArgvFor(row.key)
		if len(argv) < 4 || !slices.Equal(argv[:3], []string{"models", row.group, "set"}) {
			t.Errorf("the writer of %s answers %v, want the command that writes it", row.key, argv)
			continue
		}
		if want := row.leaf.valueWords(modelsValuePlaceholder); !slices.Equal(argv[4:], want) {
			t.Errorf("the writer of %s answers %v, want the leaf's own flag and the value placeholder: %v", row.key, argv, want)
		}
		if argv[3] != modelsProviderSegment && argv[3] != row.name {
			t.Errorf("the writer of %s answers %q for the key's name, want the %s placeholder or %q", row.key, argv[3], modelsProviderSegment, row.name)
		}
	}
}

// TestProviderRefusalPrintsTheLineThatRuns is the one hand-off D2617 exists for:
// what the refusal hands the person is the line they run themselves, so the line
// is taken back out of the refusal text and run here with the marker gone — for
// both directions of the boolean leaf, whose value pflag reads as a positional
// when the flag and its value are two words, and for a widening leaf beside it.
func TestProviderRefusalPrintsTheLineThatRuns(t *testing.T) {
	for _, tc := range []struct {
		arg   string
		leaf  string
		check func(t *testing.T, p config.ModelProvider)
	}{
		{"--require-zdr=false", "require_zdr", func(t *testing.T, p config.ModelProvider) {
			if p.RequireZDR == nil || *p.RequireZDR {
				t.Errorf("require_zdr = %v, want the explicit false the printed line carries", p.RequireZDR)
			}
		}},
		{"--require-zdr", "require_zdr", func(t *testing.T, p config.ModelProvider) {
			if p.RequireZDR == nil || !*p.RequireZDR {
				t.Errorf("require_zdr = %v, want the true the bare flag means", p.RequireZDR)
			}
		}},
		{"--route-fee-pct=9", "route_fee_pct", func(t *testing.T, p config.ModelProvider) {
			if p.RouteFeePct != 9 {
				t.Errorf("route_fee_pct = %v, want 9", p.RouteFeePct)
			}
		}},
	} {
		t.Run(tc.leaf+"/"+tc.arg, func(t *testing.T) {
			_, path := modelsWriteHome(t)
			marker := agentenv.Markers[0]
			t.Setenv(marker, "1")

			_, _, err := execute(t, "models", "provider", "set", "openrouter", tc.arg)
			if err == nil {
				t.Fatalf("the write landed in a coding agent's session")
			}
			line := modelsProviderRefusalLine(t, ErrorText(err))
			args := strings.Fields(line)
			if len(args) == 0 || args[0] != "fugaro" {
				t.Fatalf("the refusal prints %q, which is not a command line", line)
			}
			os.Unsetenv(marker)
			out, errOut, err := execute(t, args[1:]...)
			if err != nil {
				t.Fatalf("the printed line does not run: %v\n%s%s\nline: %s", err, out, errOut, line)
			}
			tc.check(t, modelsWriteLocalConfig(t, path).Providers["openrouter"])
		})
	}
}

// modelsProviderRefusalLine is the command a refusal prints, as the sentence
// around it spells it: `run this yourself at your own terminal: <line>; nothing
// was written`.
func modelsProviderRefusalLine(t *testing.T, text string) string {
	t.Helper()
	const from, to = "at your own terminal: ", "; nothing was written"
	i, j := strings.Index(text, from), strings.Index(text, to)
	if i < 0 || j < i {
		t.Fatalf("the refusal prints no line to run:\n%s", text)
	}
	return text[i+len(from) : j]
}

// TestProviderArgvFillsTheSegmentPlaceholder is the shape of the registered
// argv once CR1 (#2842) fills the per-segment placeholders: the provider's own
// name where the pattern's `*` stood, the leaf's flag after it, and the value
// the contract leaves for T1 to fill (docs/contracts/config-ownership.md §2).
// It skips on a tree that does not carry CR1 yet, decided by the registry
// itself rather than by a build tag, so it starts running the day CR1 merges.
func TestProviderArgvFillsTheSegmentPlaceholderWithTheProviderName(t *testing.T) {
	if !modelsCR1SegmentPlaceholders(t) {
		t.Skip("needs CR1 (#2842): per-segment argv placeholders")
	}
	w, ok := settings.WriterOf("providers.openrouter.base_url")
	if !ok {
		t.Fatal("no writer answers providers.openrouter.base_url")
	}
	if argv, want := w.ArgvFor("providers.openrouter.base_url"), []string{"models", "provider", "set", "openrouter", "--base-url", modelsValuePlaceholder}; !slices.Equal(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
	// A second key, so the filler is the key's own segment and the leaf its own
	// flag: a name the config does not carry, and the one leaf whose flag takes
	// the name of a secret rather than a value.
	secret, ok := settings.WriterOf("providers.fresh.secret")
	if !ok {
		t.Fatal("no writer answers providers.fresh.secret")
	}
	if argv, want := secret.ArgvFor("providers.fresh.secret"), []string{"models", "provider", "set", "fresh", "--secret", modelsValuePlaceholder}; !slices.Equal(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

// TestProviderArgvFillsTheSegmentPlaceholderWithTheModelName is the same for
// the price writer this family registers beside the provider one, whose key's
// segment is a model ID rather than a provider name.
func TestProviderArgvFillsTheSegmentPlaceholderWithTheModelName(t *testing.T) {
	if !modelsCR1SegmentPlaceholders(t) {
		t.Skip("needs CR1 (#2842): per-segment argv placeholders")
	}
	w, ok := settings.WriterOf("model_prices.vendor/model-x.cache_read")
	if !ok {
		t.Fatal("no writer answers model_prices.vendor/model-x.cache_read")
	}
	if argv, want := w.ArgvFor("model_prices.vendor/model-x.cache_read"), []string{"models", "price", "set", "vendor/model-x", "--cache-read", modelsValuePlaceholder}; !slices.Equal(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

// modelsCR1SegmentPlaceholders reports whether this tree's writer registry fills
// the per-segment placeholders CR1 adds. On the C1 base `ArgvFor` leaves `<1>`
// where it stands, so the two tests above would fail for a reason that is not
// this family's. The answer is read from the registry rather than from a build
// tag or a version, so nothing has to be edited the day CR1 merges.
func modelsCR1SegmentPlaceholders(t *testing.T) bool {
	t.Helper()
	w, ok := settings.WriterOf("providers.openrouter.kind")
	if !ok {
		t.Fatal("no writer answers providers.openrouter.kind")
	}
	for _, word := range w.ArgvFor("providers.openrouter.kind") {
		if strings.Contains(word, modelsProviderSegment) {
			return false
		}
	}
	return true
}
