package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/managed"
)

// setup_answers.go is `fugaro setup --answers-file`: the wizard's questions
// answered as one document, so a surface that drives the CLI — the console's
// first-run wizard (D2904 of docs/console-improvements) — answers them all
// without a terminal.
//
// It is an input and never a consent. The file fills the same flags the
// wizard's answers fill (setupQuestions, setup.go), and nothing else: the
// steps that are a person's — creating the GCP project, linking its billing
// account, every secret value — have no
// field here, and the run that reads the file goes on to meet them exactly as
// a run answered at a terminal does. A document that claimed one of them would
// be a document confirming a step, which --json refuses by design
// (unconfirmedWhy, init.go).
//
// The flag is declared on both doors like every other converge flag (init.go's
// newInitCmdWith): the questions it answers are asked by whichever door is
// missing their answers, and the file is read on the doors that ask them
// (runInit, runInitRepo).

// setupAnswersVersion is the version of the answers document this build reads.
// A field never changes meaning (CLAUDE.md), so a later shape that has to
// change one raises this number and the reader refuses the older document
// rather than reading it as this one.
const setupAnswersVersion = 1

// setupAnswerKeys are the document's members, its version first: the questions
// of the wizard's table and nothing besides — the place question contributing
// two keys, `location` and `region`, because the region is the precise form of
// the same answer — which is what the schema's additionalProperties: false
// says in prose (schemas/setup-answers.schema.json).
func setupAnswerKeys() []string {
	keys := make([]string, 0, len(setupAnswerQuestions)+1)
	keys = append(keys, "version")
	for _, q := range setupAnswerQuestions {
		keys = append(keys, q.key)
	}
	return keys
}

// setupAnswerQuestions is the document's table: the member's key and the flag
// of the wizard's question it answers, in the order the wizard asks them. It is
// what ties the file to the questions — a question added to setupQuestions has
// no key here until this table is given one, which TestSetupAnswersSchema fails
// on — and the reader refuses every member outside it by name.
var setupAnswerQuestions = []struct {
	key  string
	flag string
}{
	{"gcp_project", "--gcp-project"},
	// One question, two keys (LP4 of docs/location-preference; R2, D3080): the
	// place the wizard asks (us or eu) and the region its precise form names.
	// Both are members because a member never changes meaning — `region` is
	// the field it always was — and a document may answer either one; the run
	// refuses the two only where they disagree (settleLocation).
	{"location", "--location"},
	{"region", "--region"},
	{"name", "--name"},
	{"rigor", "--rigor"},
	// The budget question's two members (T2b of docs/managed-flag-ergonomics;
	// R4, D2210, D2211, D2212): the amount the project's budget alert is created
	// with, and the currency it is in. Each is optional — a document answers
	// one, both or neither — and a member that is absent is the run's own
	// default (T1's constants). What a member answers is written onto its flag's
	// field and nothing else: the run's rule about a budget (an amount, a
	// currency and a billing account together) is not restated here.
	{"budget", "--budget"},
	{"budget_currency", "--budget-currency"},
	{"bake_repos", "--bake-repos"},
}

// setupAnswers is the answers document. bake_repos is a pointer because its
// two states are three: absent is a question nobody answered, and false is a
// written no that the local config keeps. Budget is a pointer for the same
// reason and one more: absent is a question nobody answered, while 0 is an
// amount the alert cannot be created with and is refused (managed.Budget's
// rule), so the two must not read as one state.
type setupAnswers struct {
	Version        int    `json:"version"`
	GCPProject     string `json:"gcp_project"`
	Location       string `json:"location"`
	Region         string `json:"region"`
	Name           string `json:"name"`
	Rigor          string `json:"rigor"`
	Budget         *int64 `json:"budget"`
	BudgetCurrency string `json:"budget_currency"`
	BakeRepos      *bool  `json:"bake_repos"`
}

// readSetupAnswers reads and decodes the answers document at path, "-" being
// the run's own stdin (the spelling `run --task-file -` uses, and the only one
// the console passes: bytes main holds in memory reach the child on its stdin
// and no file exists for another process to change between the check and this
// read).
//
// Every refusal names the file, the member that is wrong and the fix, and none
// of them quotes a value: the document is read, never echoed. The members are
// checked by name here rather than by the decoder's own strictness, because the
// sentence a reader is owed is the one that names every member the file may
// carry and not only the one it may not (the schema's additionalProperties:
// false, in the words of a terminal).
func readSetupAnswers(path string, stdin io.Reader) (*setupAnswers, error) {
	var (
		data []byte
		err  error
	)
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, userErr("--answers-file %s: %v", path, err)
	}
	where := "--answers-file " + path
	fix := "the file holds exactly " + strings.Join(setupAnswerKeys(), ", ") + " (fugaro setup --help)"
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, userErr("%s is not the wizard's answers: %v; %s", where, err, fix)
	}
	for _, key := range slices.Sorted(maps.Keys(members)) {
		if !slices.Contains(setupAnswerKeys(), key) {
			return nil, userErr("%s carries %q, which is no answer of the wizard's: %s", where, key, fix)
		}
	}
	a := &setupAnswers{}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(a); err != nil {
		return nil, userErr("%s is not the wizard's answers: %v; %s", where, err, fix)
	}
	if a.Version != setupAnswersVersion {
		return nil, userErr("%s is version %d, and this fugaro reads version %d: the answers file's version rises only when a member's meaning changes, so write version: %d and answer the questions (%s)",
			where, a.Version, setupAnswersVersion, setupAnswersVersion, fix)
	}
	if strings.TrimSpace(a.Rigor) == "" {
		return nil, userErr("%s answers no rigor, and the rigor mode has no default (D900): add \"rigor\": one of %s, or pass --rigor <mode>", where, rigorAnswerModes())
	}
	mode, err := setupRigorMode(a.Rigor)
	if err != nil {
		return nil, userErr("%s: rigor: %v, or pass --rigor <mode>", where, err)
	}
	a.Rigor = string(mode)
	// The place is an answer of two values (D3081), which is the schema's own
	// enum, and the reader refuses anything else in the words a run uses: the
	// document is the console's, and a place it writes must not be carried to
	// a flag that refuses it later. The region beside it is not checked for
	// shape here: a place and a region that disagree are a refusal about the
	// run rather than about the file, and the reading that settles them
	// (settleLocation, init_inputs.go) is the one that makes it.
	if a.Location != "" {
		if _, ok := parsePlace(a.Location); !ok {
			return nil, userErr("%s answers a location that is not a place: answer us or eu, and name a region in full with --region <region> (%s)", where, fix)
		}
		a.Location = strings.ToLower(strings.TrimSpace(a.Location))
	}
	// The budget members are the alert's own rule and not a second one (T2b of
	// docs/managed-flag-ergonomics; R4, D2211, D2212): the pair the run would
	// have is checked here — the member the document leaves out taking the
	// default it would take — so a document cannot answer an amount or a
	// currency the installation, the marker and the question at a terminal all
	// refuse, and the amounts the question takes and the file takes are one set.
	//
	// A member the document carries and cannot answer with is refused with the
	// fix, and the value is not echoed: `null` is the one such value the decoder
	// reads into the field it belongs to (the schema's `type: integer` refuses
	// it in the console's own words), and every other spelling of one fails the
	// decode above.
	if _, ok := members["budget"]; ok && a.Budget == nil {
		return nil, userErr("%s answers a budget that is not a whole number of units: %s", where, fix)
	}
	if _, ok := members["budget"]; ok || a.BudgetCurrency != "" {
		amount, currency := managed.DefaultBudgetAmount, managed.DefaultBudgetCurrency
		if a.Budget != nil {
			amount = *a.Budget
		}
		if a.BudgetCurrency != "" {
			currency = a.BudgetCurrency
		}
		if err := (managed.Budget{Amount: amount, Currency: currency}).Check(); err != nil {
			return nil, userErr("%s answers a budget the alert cannot be created with: %v — budget is a whole number from %d to %d and budget_currency is three ASCII uppercase letters such as %s, or the pair is left out and the run takes %d %s (D2211, D2212); %s",
				where, err, managed.MinBudgetAmount, managed.MaxBudgetAmount, managed.DefaultBudgetCurrency, managed.DefaultBudgetAmount, managed.DefaultBudgetCurrency, fix)
		}
		// The pair the check just read is the pair the document answers: the
		// member it left out is written back with the default it was checked
		// against, so a half-answered pair reaches the flags whole. The schema
		// promises exactly that (a document that leaves `budget` out answers
		// 100, one that leaves `budget_currency` out answers USD), and a pair
		// only substituted inside the check would leave the run with half of
		// one — which the run's own trio rule then refuses as a caller's
		// mistake rather than reading the default the document was told it
		// answers.
		a.Budget, a.BudgetCurrency = &amount, currency
	}
	return a, nil
}

// applySetupAnswers fills the wizard's questions' flags from the answers file,
// and says in one line which flags won where both answered a question.
//
// The file is the same door as the flags and never a wider one: an answer it
// carries is written onto the flag's own field, so nothing downstream — the
// questions that are not asked, the refusals that name a flag, the stages that
// act on the answers — can tell the two apart. A flag given beside the file
// wins, because it is the more specific door (a caller that passed one meant
// to override what it wrote), and the run names it rather than dropping the
// file's answer in silence.
func (r *initRun) applySetupAnswers() error {
	o := r.o
	if o.answersFile == "" {
		return nil
	}
	a, err := readSetupAnswers(o.answersFile, r.in)
	if err != nil {
		return err
	}
	var won []string
	for _, q := range setupAnswerQuestions {
		value, answered := a.answer(q.key)
		if !answered {
			continue
		}
		if o.answerGiven(q.flag) {
			won = append(won, q.flag)
			continue
		}
		if err := o.setAnswer(q.flag, value); err != nil {
			return err
		}
	}
	if len(won) > 0 && !o.printVars {
		fmt.Fprintf(r.w, "%sa flag wins over the answers file, and %s was passed: what the file answers for it is not used\n",
			o.programPrefix(), strings.Join(won, ", "))
	}
	return nil
}

// answer is the document's answer to the question whose key is given, and
// whether the document answered it at all. An absent member and an empty
// string are one state — nothing was answered, and the question is asked or
// refused exactly as it is without the flag — while bake_repos answers in both
// its values: a written false is an answer.
func (a *setupAnswers) answer(key string) (string, bool) {
	switch key {
	case "gcp_project":
		return a.GCPProject, a.GCPProject != ""
	case "location":
		return a.Location, a.Location != ""
	case "region":
		return a.Region, a.Region != ""
	case "name":
		return a.Name, a.Name != ""
	case "rigor":
		return a.Rigor, a.Rigor != ""
	case "budget":
		// An amount is answered in both its states: absent is a question nobody
		// answered, and 0 is an amount the reader refused one line above rather
		// than one it reads as absent.
		if a.Budget == nil {
			return "", false
		}
		return strconv.FormatInt(*a.Budget, 10), true
	case "budget_currency":
		return a.BudgetCurrency, a.BudgetCurrency != ""
	case "bake_repos":
		if a.BakeRepos == nil {
			return "", false
		}
		if *a.BakeRepos {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// setAnswer writes an answer onto the flag's own field, which is where the
// question that reads it reads it (wizard_stage.go) and where the rest of the
// run reads every input from: a person at a terminal, a scripted flag and an
// answers file answer one question in one place.
//
// It can fail only where a value that is no value of the field's kind reaches
// it, which the reader has already refused (readSetupAnswers): the error is
// returned rather than swallowed, so a document never fills a flag with
// something it did not answer.
func (o *initOptions) setAnswer(flag, value string) error {
	switch flag {
	case "--gcp-project":
		o.cloud.gcpProject = value
	case "--location":
		o.location = value
	case "--region":
		o.cloud.region = value
	case "--name":
		o.name = value
	case "--rigor":
		o.rigor = value
	case "--budget":
		// The amount is the member's own whole number, spelled by answer() one
		// line above: the pair read back as one the alert can be created with
		// (readSetupAnswers), so this cannot fail — and nothing is written where
		// it somehow does, rather than an amount nobody answered.
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return userErr("reading the answers file's budget %q: %v", value, err)
		}
		o.budget = n
	case "--budget-currency":
		o.budgetCurrency = value
	case "--bake-repos":
		// The value the flag names, and the fact that it was passed: an absent
		// key and a written yes are one statement to every reader and two
		// different runs to the question, which is asked only where nobody has
		// answered it (init.go's bakeReposChanged).
		o.bakeRepos, o.bakeReposChanged = value == "true", true
	}
	return nil
}

// answerGiven is whether the flag has already answered the question: the
// fields above, read before anything of this run has asked, so a value in them
// is the flag's. It is not "the file answered too": that is the caller's own
// test.
func (o *initOptions) answerGiven(flag string) bool {
	switch flag {
	case "--gcp-project":
		return o.cloud.gcpProject != ""
	case "--location":
		return o.location != ""
	case "--region":
		return o.cloud.region != ""
	case "--name":
		return o.name != ""
	case "--rigor":
		return o.rigor != ""
	case "--budget":
		return o.budget != 0
	case "--budget-currency":
		return o.budgetCurrency != ""
	case "--bake-repos":
		return o.bakeReposChanged
	}
	return false
}
