package cli

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// setup_answers_test.go pins task ST1 of docs/console-improvements/implementation-plan.md:
// `fugaro setup --answers-file`, the wizard's five questions answered as one
// document, so a caller with no terminal — the console's first-run wizard,
// D2904 — drives the CLI with what it collected.
//
// The document is an input and never a consent: it fills the same five flags
// the wizard's answers fill (setup_answers.go), and every step that is a
// person's is met exactly as a run answered at a terminal meets it.

// answersGCPProject is the project the documents below name: the same ID the
// rest of this package's rigs use, so a test reading it back reads one name.
const answersGCPProject = initProject

// answersDoc builds one answers document out of its members, with the version
// every document carries: the spelling the console writes and the reader
// decodes.
func answersDoc(members ...string) string {
	return "{" + strings.Join(append([]string{`"version":1`}, members...), ",") + "}"
}

// answersFile writes a document where a run can read it: the PATH form of
// --answers-file.
func answersFile(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "answers.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fullAnswers is the document that answers every question the wizard asks,
// the budget question included (T2b of docs/managed-flag-ergonomics; T1's
// constants are the default, and a document that answers the amount and the
// currency is the console's own spelling of an owner's choice).
func fullAnswers() string {
	return answersDoc(
		`"gcp_project":"`+answersGCPProject+`"`,
		`"region":"europe-west4"`,
		`"name":"aurora"`,
		`"rigor":"pro"`,
		`"budget":2500`,
		`"budget_currency":"EUR"`,
		`"bake_repos":true`,
	)
}

// answersRun is a first run that has read the answers file at path: the file is
// applied where runInit applies it, before anything is checked or asked.
func answersRun(t *testing.T, path, stdin string, o *initOptions) (*initRun, *strings.Builder) {
	t.Helper()
	r, out := wizardRun(t, stdin, o)
	// The file's budget on a creating run needs the account the alert is
	// created with, which no file answers and no fixture's state records.
	r.o.billingAccount = "000000-000000-000000"
	r.o.answersFile = path
	if err := r.applySetupAnswers(); err != nil {
		t.Fatalf("%s was refused: %v\n%s", path, err, out)
	}
	return r, out
}

// TestSetupAnswersFile: the document answers the wizard's five questions, and
// the run that reads one is the run that was passed those five flags — nothing
// more (a flag given beside the file wins and is named in the plan output) and
// nothing less (a question the file leaves out is asked as it always was).
func TestSetupAnswersFile(t *testing.T) {
	t.Run("the file answers every question and none is asked", func(t *testing.T) {
		r, out := answersRun(t, answersFile(t, fullAnswers()), "", nil)
		o := r.o
		if o.cloud.gcpProject != answersGCPProject || o.cloud.region != "europe-west4" || o.name != "aurora" {
			t.Errorf("the file's answers are not on the flags' own fields: project %q, region %q, name %q",
				o.cloud.gcpProject, o.cloud.region, o.name)
		}
		if o.rigor != string(rigor.Pro) {
			t.Errorf("rigor %q, want %q", o.rigor, rigor.Pro)
		}
		// The bake switch's two states are three, and a written true is the
		// answer the local config keeps: the field and the fact that it was
		// answered both come from the file.
		if !o.bakeRepos || !o.bakeReposChanged {
			t.Errorf("bake_repos: value %v, changed %v, want true from the file", o.bakeRepos, o.bakeReposChanged)
		}
		// Nothing is left to ask: the run at a terminal with an empty stdin
		// goes on. (The billing question is the one question left, and it is
		// the installation's own: with no account to offer it hands its line
		// back rather than prompting, which is what this run reads.)
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("a run whose answers all came from the file was refused: %v\n%s", err, out)
		}
		for _, q := range setupQuestions {
			asked := q.what
			if q.flag == "--bake-repos" {
				// Asked under its label: its what is a clause and not a thing
				// to type after "Type".
				asked = q.label
			}
			if strings.Contains(out.String(), asked) {
				t.Errorf("%s answers %q, and the wizard asked it anyway:\n%s", q.flag, asked, out)
			}
		}
	})

	t.Run("a flag given beside the file wins, and the run names it", func(t *testing.T) {
		doc := answersDoc(`"region":"europe-west4"`, `"name":"aurora"`, `"rigor":"pro"`)
		r, out := answersRun(t, answersFile(t, doc), "", optionsFor("--region"))
		if r.o.cloud.region != defaultRegion {
			t.Errorf("the flag's region was overwritten by the file: %q", r.o.cloud.region)
		}
		if r.o.name != "aurora" {
			t.Errorf("the file's answer for a question no flag answered was dropped: name %q", r.o.name)
		}
		if line := out.String(); !strings.Contains(line, "--region") || !strings.Contains(line, "wins") {
			t.Errorf("the run did not name the flag that won:\n%s", line)
		} else if strings.Contains(line, "--name") || strings.Contains(line, "--rigor") {
			t.Errorf("the run named a flag the file's answer won for:\n%s", line)
		}
	})

	t.Run("the document's own text is never printed", func(t *testing.T) {
		// The file may hold nothing secret, and it is still not echoed: the
		// reader prints the members it refuses and the flags that won, never
		// the document or a line of it.
		doc := fullAnswers()
		r, out := answersRun(t, answersFile(t, doc), "", optionsFor("--region"))
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if strings.Contains(out.String(), doc) {
			t.Errorf("the plan printed the answers document:\n%s", out)
		}
		for _, key := range setupAnswerKeys() {
			if strings.Contains(out.String(), `"`+key+`"`) {
				t.Errorf("the plan printed the document's own member %q:\n%s", key, out)
			}
		}
	})

	t.Run("a question the file leaves out is asked as it always was", func(t *testing.T) {
		// The file answers what it carries and nothing else: the questions
		// left open are the run's own, at a terminal or refused with their
		// flag.
		doc := answersDoc(`"region":"europe-west4"`, `"rigor":"pro"`)
		r, out := answersRun(t, answersFile(t, doc), stdinFor(""), nil)
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		q := questionFor("--gcp-project")
		if !strings.Contains(out.String(), q.what) {
			t.Errorf("the file answers no project ID, and the wizard did not ask for one:\n%s", out)
		}
		if r.o.cloud.gcpProject == "" {
			t.Error("the question was asked and its answer was not recorded")
		}
	})
}

// TestSetupAnswersBudgetMembers is T2b's red step (docs/managed-flag-ergonomics;
// R4, D2210, D2211, D2212): the answers file carries the wizard's budget
// question too — the amount the project's alert is created with, and the
// currency it is in — and adding members leaves the version at 1, because no
// member's meaning changed (CLAUDE.md's rule, applied in R4).
//
// Each member is optional, which is the schema's own word, and the pair is
// checked as the pair the run would have: a member that is absent is answered
// with T1's default for the check alone, so a document may answer the question
// with one member or with two, and what a half-answered pair meets afterwards
// is the run's own rule and not the file's (an alert needs a billing account,
// which no member of this file answers — F6 of the plan's reconciliation).
func TestSetupAnswersBudgetMembers(t *testing.T) {
	// What each member does on its own, read on the flags' own fields: the
	// amount is --budget's, the currency --budget-currency's, and a member the
	// document leaves out is one this file does not answer at all.
	for _, tc := range []struct {
		name     string
		doc      string
		amount   int64
		currency string
	}{
		{"the amount and the currency", answersDoc(`"rigor":"pro"`, `"budget":2500`, `"budget_currency":"EUR"`), 2500, "EUR"},
		{"the amount alone", answersDoc(`"rigor":"pro"`, `"budget":2500`), 2500, ""},
		{"the currency alone", answersDoc(`"rigor":"pro"`, `"budget_currency":"EUR"`), 0, "EUR"},
		{"the defaults, written out", answersDoc(`"rigor":"pro"`, `"budget":100`, `"budget_currency":"USD"`), 100, "USD"},
		{"neither member", answersDoc(`"rigor":"pro"`), 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, out := answersRun(t, answersFile(t, tc.doc), "", nil)
			if r.o.budget != tc.amount || r.o.budgetCurrency != tc.currency {
				t.Errorf("%s: the run has %d %s, want %d %s:\n%s", tc.doc, r.o.budget, r.o.budgetCurrency, tc.amount, tc.currency, out)
			}
		})
	}

	if setupAnswersVersion != 1 {
		t.Errorf("the answers file's version is %d, want 1: the budget members are added and no member's meaning changes (R4)", setupAnswersVersion)
	}

	// The pair is the marker's own rule (managed.Budget.Check, T1), so a
	// document cannot answer an amount or a currency the alert would refuse to
	// be created with — and every one of them is refused here, with the file and
	// the fix named and nothing written onto the flags.
	for _, tc := range []struct {
		name  string
		doc   string
		names []string
	}{
		{"an amount of zero", answersDoc(`"rigor":"pro"`, `"budget":0`), []string{"budget", "outside 1 to 10000000"}},
		{"an amount above the ceiling", answersDoc(`"rigor":"pro"`, `"budget":10000001`), []string{"budget", "outside 1 to 10000000"}},
		{"a currency that is no code", answersDoc(`"rigor":"pro"`, `"budget":100`, `"budget_currency":"usd"`), []string{"budget", "three ASCII uppercase letters"}},
		{"a currency of the wrong length", answersDoc(`"rigor":"pro"`, `"budget":100`, `"budget_currency":"US"`), []string{"budget"}},
		{"an amount that is no whole number", answersDoc(`"rigor":"pro"`, `"budget":2.5`), []string{"budget"}},
		{"an amount that is a string", answersDoc(`"rigor":"pro"`, `"budget":"100"`), []string{"budget"}},
		{"an amount that is null", answersDoc(`"rigor":"pro"`, `"budget":null`), []string{"budget"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, out := wizardRun(t, "", nil)
			r.o.answersFile = answersFile(t, tc.doc)
			err := r.applySetupAnswers()
			if err == nil {
				t.Fatalf("the run read %s:\n%s", tc.doc, out)
			}
			for _, want := range append([]string{"--answers-file", "version, gcp_project"}, tc.names...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if r.o.budget != 0 || r.o.budgetCurrency != "" {
				t.Errorf("a refused document filled the budget: %d %s", r.o.budget, r.o.budgetCurrency)
			}
		})
	}
}

// TestSetupAnswersFileRefusesUnknownKey is the plan's red step, and the
// document's own contract: the reader is strict, and every refusal names the
// file, what is wrong and the fix.
//
// It is the refusal a reader that decoded into a struct alone does not make:
// encoding/json drops a member it has no field for, and a run would go on with
// a document it did not understand — exactly the plan's "an unknown key ... is
// refused with the fix".
func TestSetupAnswersFileRefusesUnknownKey(t *testing.T) {
	const members = "the file holds exactly version, gcp_project, location, region, name, rigor, budget, budget_currency, bake_repos"
	for _, tc := range []struct {
		name  string
		doc   string
		names []string // what the refusal has to name beyond the file and the fix
	}{
		{
			name:  "a member that is no answer of the wizard's",
			doc:   answersDoc(`"rigor":"pro"`, `"billing_account":"0X0X0X-0X0X0X-0X0X0X"`),
			names: []string{`"billing_account"`, members},
		},
		{
			name:  "a member spelled like the flag and not like the key",
			doc:   answersDoc(`"rigor":"pro"`, `"gcp-project":"`+answersGCPProject+`"`),
			names: []string{`"gcp-project"`, members},
		},
		{
			name: "a member only a confirmation could carry",
			// The steps that are a person's have no key here, and a document
			// that names one is refused rather than read as one.
			doc:   answersDoc(`"rigor":"pro"`, `"create_project":true`),
			names: []string{`"create_project"`, members},
		},
		{
			name:  "a member of the wrong type",
			doc:   answersDoc(`"rigor":"pro"`, `"name":5`),
			names: []string{"name", members},
		},
		{
			name:  "a document that is not an object",
			doc:   `["version",1]`,
			names: []string{members},
		},
		{
			name:  "a document that is not JSON at all",
			doc:   `{`,
			names: []string{members},
		},
		{
			name:  "no version",
			doc:   `{"rigor":"pro"}`,
			names: []string{"version", members},
		},
		{
			name:  "a version this build does not read",
			doc:   `{"version":2,"rigor":"pro"}`,
			names: []string{"version 2", members},
		},
		{
			name: "no rigor at all",
			// The mode has no default (D900), so a document that answers
			// nothing for it is refused rather than defaulted.
			doc:   answersDoc(`"name":"aurora"`),
			names: []string{"rigor", rigorAnswerModes(), "--rigor"},
		},
		{
			name:  "a rigor nobody knows",
			doc:   answersDoc(`"rigor":"paranoid"`),
			names: []string{"rigor", rigorAnswerModes(), "--rigor"},
		},
		{
			name:  "the operator's own per-run mode",
			doc:   answersDoc(`"rigor":"` + string(rigor.Hotfix) + `"`),
			names: []string{"rigor", "--rigor"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, out := wizardRun(t, "", nil)
			r.o.answersFile = answersFile(t, tc.doc)
			err := r.applySetupAnswers()
			if err == nil {
				t.Fatalf("the run read %s:\n%s", tc.doc, out)
			}
			msg := err.Error()
			if !strings.Contains(msg, "--answers-file") {
				t.Errorf("the refusal does not name the file: %v", err)
			}
			for _, want := range tc.names {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			// A document that is not understood is not half-applied: not one
			// of the questions is answered by it.
			o := r.o
			if o.cloud.gcpProject != "" || o.cloud.region != "" || o.name != "" || o.rigor != "" || o.bakeReposChanged {
				t.Errorf("a refused document filled a question: project %q, region %q, name %q, rigor %q, bake %v",
					o.cloud.gcpProject, o.cloud.region, o.name, o.rigor, o.bakeReposChanged)
			}
			// And it is not echoed: the refusal says what is wrong with it, not
			// what it held.
			if strings.Contains(msg, tc.doc) {
				t.Errorf("the refusal quoted the document: %v", err)
			}
		})
	}

	t.Run("the members a person's steps could be spelled with", func(t *testing.T) {
		// Enumerated rather than assumed: the document's members are the
		// wizard's five questions and its own version, so a key for any of the
		// steps that are a person's is refused by name.
		// `location` is not in this list: it is the wizard's place question
		// (LP4 of docs/location-preference; R2), and the members a person's
		// steps are spelled with are the others. `firestore_location` stays:
		// that permanent location is confirmed by a person at the terminal and
		// is not an answer any document carries.
		for _, key := range []string{
			"create_project", "link_billing", "billing_account", "firestore_location",
			"project", "yes", "confirm", "ask", "secrets", "secret",
		} {
			if slices.Contains(setupAnswerKeys(), key) {
				t.Errorf("%q is a member of the answers file, and it is not an answer of the wizard's", key)
				continue
			}
			r, _ := wizardRun(t, "", nil)
			r.o.answersFile = answersFile(t, answersDoc(`"rigor":"pro"`, `"`+key+`":true`))
			if err := r.applySetupAnswers(); err == nil {
				t.Errorf("a document carrying %q was read", key)
			}
		}
	})
}

// jsonStrings is a schema's own list value — `required`, an `enum` — as the
// strings it holds, for the comparisons above.
func jsonStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

// TestSetupAnswersSchema holds the published contract and the reader to each
// other: the schema is what the console writes against, and the reader is the
// same refusal in a run's own words. They are one document (schemas/), so a
// member the one refuses the other refuses too.
func TestSetupAnswersSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "setup-answers.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := doc.(map[string]any)
	if body == nil {
		t.Fatal("schemas/setup-answers.schema.json is not an object")
	}
	id, _ := body["$id"].(string)
	if id == "" {
		t.Fatal("schemas/setup-answers.schema.json has no $id")
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		t.Fatal(err)
	}

	// The members are the wizard's questions and the document's own version:
	// the table is what ties the two (setupAnswerQuestions), so a question
	// added to the wizard has no key here until it is given one.
	t.Run("the members are the wizard's questions", func(t *testing.T) {
		props, _ := body["properties"].(map[string]any)
		if props == nil {
			t.Fatal("the schema has no properties")
		}
		if got, want := slices.Sorted(maps.Keys(props)), slices.Sorted(slices.Values(setupAnswerKeys())); !slices.Equal(got, want) {
			t.Errorf("the schema's members are %v, want %v", got, want)
		}
		if extra, _ := body["additionalProperties"].(bool); extra {
			t.Error("the schema allows a member its own properties do not name")
		}
		// Every question of the wizard has a key, each key is its flag's own
		// spelling, and the place question has a second one: the region is the
		// precise form of the same answer (R2, D3080), so `region` answers the
		// question `--location` asks and is a member in its own right.
		keys, answered := map[string]bool{}, map[string]bool{}
		for _, row := range setupAnswerQuestions {
			if want := strings.ReplaceAll(strings.TrimPrefix(row.flag, "--"), "-", "_"); row.key != want {
				t.Errorf("%s is answered by the key %q, want %q", row.flag, row.key, want)
			}
			if keys[row.key] {
				t.Errorf("two rows of the answers file carry the key %q", row.key)
			}
			keys[row.key], answered[row.flag] = true, true
		}
		for _, q := range setupQuestions {
			if !answered[q.flag] {
				t.Errorf("the wizard asks %s and no member of the file answers it", q.flag)
			}
		}
		if !answered["--region"] {
			t.Error("the answers file has no `region` member: it is the place question's precise form (D3080), and the console writes it")
		}
		// The mode has no default (D900) and hotfix is out of it (D834), so
		// the schema requires the one and refuses the other.
		required := jsonStrings(body["required"])
		if !slices.Contains(required, "version") || !slices.Contains(required, "rigor") {
			t.Errorf("required is %v, want version and rigor", required)
		}
		// A list's order carries nothing here, so the two are compared as sets:
		// the question's own order is the one it prints in.
		rigorProp, _ := props["rigor"].(map[string]any)
		got := slices.Sorted(slices.Values(jsonStrings(rigorProp["enum"])))
		want := make([]string, 0, len(rigorChoices))
		for _, c := range rigorChoices {
			want = append(want, string(c.mode))
		}
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("the schema's rigor enum is %v, want the modes the question offers (%v)", got, want)
		}
	})

	// The two refuse the same documents: what a caller validates against the
	// schema is what the run reads.
	t.Run("the schema and the reader agree", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			doc  string
		}{
			{"every answer", fullAnswers()},
			// The place question's two members (LP4 of docs/location-preference;
			// R2, D3080): the schema's enum and the reader's own refusal are one
			// answer, and a document that answers both spellings of the one
			// question is a whole document — whether they agree is the run's to
			// settle, not the file's.
			{"the place alone", answersDoc(`"rigor":"pro"`, `"location":"eu"`)},
			{"the place and its region together", answersDoc(`"rigor":"pro"`, `"location":"eu"`, `"region":"europe-west4"`)},
			{"a location that is no place", answersDoc(`"rigor":"pro"`, `"location":"asia"`)},
			// The budget question's two members (T2b; R4, D2211, D2212): each is
			// optional and the pair is the marker's own rule, so the schema's
			// bounds and the reader's refusal are one answer.
			{"a budget and its currency", answersDoc(`"rigor":"pro"`, `"budget":2500`, `"budget_currency":"EUR"`)},
			{"a budget with no currency", answersDoc(`"rigor":"pro"`, `"budget":2500`)},
			{"a currency with no budget", answersDoc(`"rigor":"pro"`, `"budget_currency":"EUR"`)},
			{"a budget of zero", answersDoc(`"rigor":"pro"`, `"budget":0`)},
			{"a budget above the ceiling", answersDoc(`"rigor":"pro"`, `"budget":10000001`)},
			{"a budget that is no whole number", answersDoc(`"rigor":"pro"`, `"budget":2.5`)},
			{"a budget that is a string", answersDoc(`"rigor":"pro"`, `"budget":"100"`)},
			{"a budget that is null", answersDoc(`"rigor":"pro"`, `"budget":null`)},
			{"a currency that is no code", answersDoc(`"rigor":"pro"`, `"budget":100`, `"budget_currency":"usd"`)},
			{"a question left out", answersDoc(`"rigor":"pro"`)},
			{"no rigor", answersDoc(`"name":"aurora"`)},
			{"hotfix", answersDoc(`"rigor":"hotfix"`)},
			{"an unknown member", answersDoc(`"rigor":"pro"`, `"billing_account":"x"`)},
			{"nothing at all", `{}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				read := func() error {
					_, err := readSetupAnswers(answersFile(t, tc.doc), strings.NewReader(""))
					return err
				}
				inst, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.doc))
				if err != nil {
					t.Fatal(err)
				}
				validated := schema.Validate(inst) == nil
				if readErr := read(); (readErr == nil) != validated {
					t.Errorf("the reader answered %v and the schema %v for %s", readErr == nil, validated, tc.doc)
				}
			})
		}
	})
}

// TestSetupAnswersFromStdin: "-" is the run's own stdin, the spelling
// `run --task-file -` uses and the only one a caller with no file of its own
// passes — the console holds the document in memory and hands it over on the
// child's stdin, so no file exists for another process to change between a
// check and the read.
func TestSetupAnswersFromStdin(t *testing.T) {
	doc := fullAnswers()
	r, out := wizardRun(t, doc, nil)
	r.o.answersFile = "-"
	if err := r.applySetupAnswers(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.cloud.gcpProject != answersGCPProject || r.o.name != "aurora" || r.o.rigor != string(rigor.Pro) {
		t.Errorf("the document on stdin did not fill the questions: %+v", r.o)
	}
	// It is read once and it is the whole of what stdin held: a second run over
	// the same stdin finds nothing, which is the run whose terminal has no
	// answers left to give.
	if err := r.applySetupAnswers(); err == nil {
		t.Error("the run read the answers file twice")
	}

	// Through the command, and without a cloud: the file is read from the run's
	// own stdin, before anything else is checked. A document the reader refuses
	// is the file's own refusal naming the spelling that was passed; a document
	// whose rigor reaches a flag that cannot carry one is refused by that flag's
	// own rule — which is how a run with no --rigor of its own comes to have
	// one.
	t.Run("through the command", func(t *testing.T) {
		firstRunEnv(t)
		fakeTerminal(t)
		bad := answersDoc(`"rigor":"paranoid"`)
		out, errOut, err := executeStdin(t, bad, "setup", "--answers-file", "-", "--json")
		if err == nil {
			t.Fatalf("the run read a document that answers no rigor it knows:\n%s%s", out, errOut)
		}
		if !strings.Contains(err.Error(), "--answers-file -") || !strings.Contains(err.Error(), rigorAnswerModes()) {
			t.Errorf("the refusal does not name the file it read on stdin and the fix: %v", err)
		}

		// The wizard's answers without the budget's: a document that carries one
		// meets the door's own rule about a budget first (an alert needs a
		// billing account, and the file answers no account — init.go's trio
		// check, which is the run's rule and not the file's), and what this arm
		// is about is the rigor answer reaching --print-vars. The budget members
		// are TestSetupAnswersBudgetMembers'.
		bare := answersDoc(
			`"gcp_project":"`+answersGCPProject+`"`,
			`"region":"europe-west4"`,
			`"name":"aurora"`,
			`"rigor":"pro"`,
			`"bake_repos":true`,
		)
		out, errOut, err = executeStdin(t, bare, "setup", "--answers-file", "-", "--print-vars")
		if err == nil {
			t.Fatalf("--print-vars took a rigor from the answers file:\n%s%s", out, errOut)
		}
		if !strings.Contains(err.Error(), "--rigor") || !strings.Contains(err.Error(), "--print-vars") {
			t.Errorf("the file's rigor did not reach the flag that refuses it: %v", err)
		}
	})
}

// TestSetupAnswersFileGrantsNoConsent: the file is an input and never a
// consent. `--json` confirms no step (unconfirmedWhy, init.go), and the
// console's own window is not one the CLI accepts (F27, O6): what the document
// answers is the five questions, and every step that is a person's is met
// exactly as a run answered at a terminal meets it.
func TestSetupAnswersFileGrantsNoConsent(t *testing.T) {
	t.Run("no answer of the file's is a permission", func(t *testing.T) {
		// The members are the questions alone: the creation of the GCP
		// project, the billing link, the permanent Firestore location and
		// every secret value have no key here, and a run that read one would
		// be a run a document confirmed something for.
		want := []string{"version", "gcp_project", "location", "region", "name", "rigor", "budget", "budget_currency", "bake_repos"}
		if got := setupAnswerKeys(); !slices.Equal(got, want) {
			t.Errorf("the answers file's members are %v, want %v", got, want)
		}
	})

	t.Run("the document fills the questions and nothing else of the run", func(t *testing.T) {
		// Read off the product rather than trusted: a whole document applied to
		// a run changes its five question fields and not one other thing. A
		// handler that also set --yes, or recorded a creation, or named a
		// billing account would be a document giving a consent, and this is
		// where that fails.
		o := &initOptions{asJSON: true}
		r, out := wizardRun(t, "", o)
		r.o.answersFile = answersFile(t, fullAnswers())
		before := *r.o
		if err := r.applySetupAnswers(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		after := *r.o
		after.cloud.gcpProject, after.cloud.region, after.name, after.rigor = "", "", "", ""
		after.budget, after.budgetCurrency = 0, ""
		after.bakeRepos, after.bakeReposChanged = false, false
		if !reflect.DeepEqual(after, before) {
			t.Errorf("the document changed something of the run besides the questions it answers:\n%+v\nwant\n%+v", after, before)
		}
	})

	for _, tc := range []struct {
		name  string
		o     *initOptions
		agent string
	}{
		{"--json", &initOptions{asJSON: true}, ""},
		{"a run that never prompts", &initOptions{nonInteractive: true}, ""},
		{"a coding agent's session", &initOptions{}, "CLAUDECODE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Every question is in hand from the file, and the run is still one
			// nobody can confirm a step for: the answers fill the questions and
			// none of the run's conditions, which is what the arm above reads
			// field by field — so the apply path this run reaches is the one
			// TestInitInAnAgentSessionAppliesNothing pins.
			for _, k := range agentMarkers {
				t.Setenv(k, "")
			}
			if tc.agent != "" {
				t.Setenv(tc.agent, "1")
			}
			r, out := answersRun(t, answersFile(t, fullAnswers()), "", tc.o)
			for _, q := range setupQuestions {
				// The place question takes either of its two keys (R2, D3080):
				// the document above answers it with the region.
				answered := r.o.answerGiven(q.flag)
				if q.flag == "--location" {
					answered = answered || r.o.answerGiven("--region")
				}
				if !answered {
					t.Errorf("the file answers %s and the run has no answer for it:\n%s", q.flag, out)
				}
			}
			c := initflow.Conditions{
				Terminal:       r.stdinTerminal(),
				Agent:          agentMarker(os.Getenv, r.stdinTerminal()),
				Ask:            r.o.ask,
				NonInteractive: r.o.nonInteractive,
				JSON:           r.o.asJSON,
				Yes:            r.o.yes,
			}
			if initflow.PersonAtTheTerminal(c) || initflow.CanConfirmRun(c) {
				t.Errorf("a whole answers file put a person at the terminal the confirmations need: %+v", c)
			}
		})
	}
}
