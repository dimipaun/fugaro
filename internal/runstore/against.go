package runstore

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/task"
)

// The three sources a record of what a review was checked against can name:
// the task of the run that opened the pull request, a file a person supplied
// by hand, or nothing at all.
const (
	AgainstSourceRun  = "run"
	AgainstSourceFile = "file"
	AgainstSourceNone = "none"
)

// AgainstSources is the whole vocabulary, in the order a message names it.
var AgainstSources = []string{AgainstSourceRun, AgainstSourceFile, AgainstSourceNone}

// againstSHA256RE is the form of the digest recorded beside the quoted text:
// lowercase hex, the same shape every other digest in this tree is stored in.
var againstSHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Against records what a review was checked against (spec §2, R8). One type,
// two records: the independent review job's artifact carries it (review.json)
// and so does every in-run review round (result.json's reviews[]), so a reader
// that wants to know whether a verdict judged the change against a brief reads
// the same object in both.
//
// It is the LAUNCHER's claim and never evidence (R16): the source it names is
// what the launcher decided, and the text it is a digest of travels beside it
// in the prompt rather than here. Reason is why there was nothing to check
// against, and it is carried with source none -- a review that judged the code
// alone says so rather than leaving a reader to guess.
type Against struct {
	Source string `json:"source"`
	// Run is the root run whose stored task was quoted, for source run.
	Run string `json:"run,omitempty"`
	// SHA256 is the digest of the text the reviewer was actually shown, after
	// the launcher clipped and redacted it -- so a reader can tell two reviews
	// of one pull request apart when the task behind them changed.
	SHA256 string `json:"sha256,omitempty"`
	// Reason is why nothing was found, for source none.
	Reason string `json:"reason,omitempty"`
}

// Validate holds one record to its own shape: the source is one of the three
// words, a run names a run, a reason belongs to source none and to nothing
// else, and a digest is a digest. It is what a reader checks before it acts on
// the record, so a source this build does not know is refused rather than read
// as the nearest one it does.
func (a Against) Validate() error {
	if !slices.Contains(AgainstSources, a.Source) {
		return fmt.Errorf("source is %q, and the three words are %s", a.Source, strings.Join(AgainstSources, ", "))
	}
	if a.Source != AgainstSourceNone && a.Reason != "" {
		return fmt.Errorf("source %s carries a reason, and only a review with nothing to check against has one", a.Source)
	}
	if a.Source == AgainstSourceNone {
		if strings.TrimSpace(a.Reason) == "" {
			return errors.New("source none carries no reason, so a reader cannot tell a code-only review from a launcher that lost the task")
		}
		if a.Run != "" || a.SHA256 != "" {
			return errors.New("source none carries a run or a digest, and nothing was quoted")
		}
		return nil
	}
	if a.Source == AgainstSourceRun && !task.ValidRunID(a.Run) {
		return fmt.Errorf("source run carries run %q, and a run id looks like 20260926-221530-a1b2", a.Run)
	}
	if a.Source == AgainstSourceFile && a.Run != "" {
		return fmt.Errorf("source file carries run %q, and a file a person supplied is not a run", a.Run)
	}
	if a.SHA256 != "" && !againstSHA256RE.MatchString(a.SHA256) {
		return fmt.Errorf("sha256 is %q, and a digest is %s", a.SHA256, againstSHA256RE)
	}
	return nil
}
