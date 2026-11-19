package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
)

// setup_answers.go is `fugaro setup --answers-file`: the wizard's questions
// answered as one document, so a surface that drives the CLI — the console's
// first-run wizard (D2904 of docs/console-improvements) — answers them all
// without a terminal.
//
// It is an input and never a consent. The file fills the same five flags the
// wizard's answers fill (setupQuestions, setup.go), and nothing else: the
// steps that are a person's — creating the GCP project, linking its billing
// account, the permanent Firestore location, every secret value — have no
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

// setupAnswerKeys are the document's members, its version first: the five
// questions of the wizard's table and nothing besides, which is what the
// schema's additionalProperties: false says in prose
// (schemas/setup-answers.schema.json).
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
	{"region", "--region"},
	{"name", "--name"},
	{"rigor", "--rigor"},
	{"bake_repos", "--bake-repos"},
}

// setupAnswers is the answers document. bake_repos is a pointer because its
// two states are three: absent is a question nobody answered, and false is a
// written no that the local config keeps.
type setupAnswers struct {
	Version    int    `json:"version"`
	GCPProject string `json:"gcp_project"`
	Region     string `json:"region"`
	Name       string `json:"name"`
	Rigor      string `json:"rigor"`
	BakeRepos  *bool  `json:"bake_repos"`
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
// sentence a reader is owed is the one that names the six members the file may
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
		o.setAnswer(q.flag, value)
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
	case "region":
		return a.Region, a.Region != ""
	case "name":
		return a.Name, a.Name != ""
	case "rigor":
		return a.Rigor, a.Rigor != ""
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
func (o *initOptions) setAnswer(flag, value string) {
	switch flag {
	case "--gcp-project":
		o.cloud.gcpProject = value
	case "--region":
		o.cloud.region = value
	case "--name":
		o.name = value
	case "--rigor":
		o.rigor = value
	case "--bake-repos":
		// The value the flag names, and the fact that it was passed: an absent
		// key and a written yes are one statement to every reader and two
		// different runs to the question, which is asked only where nobody has
		// answered it (init.go's bakeReposChanged).
		o.bakeRepos, o.bakeReposChanged = value == "true", true
	}
}

// answerGiven is whether the flag has already answered the question: the
// fields above, read before anything of this run has asked, so a value in them
// is the flag's. It is not "the file answered too": that is the caller's own
// test.
func (o *initOptions) answerGiven(flag string) bool {
	switch flag {
	case "--gcp-project":
		return o.cloud.gcpProject != ""
	case "--region":
		return o.cloud.region != ""
	case "--name":
		return o.name != ""
	case "--rigor":
		return o.rigor != ""
	case "--bake-repos":
		return o.bakeReposChanged
	}
	return false
}
