package scripts_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// liveCheckScripts are the owner-run live checks' scripts and their tests.
// Each test drives its script against fake tools on PATH -- a fake `claude`
// and a fake upstream for Check 90, fake `gcloud`, `fugaro` and `curl` for
// Check 91, fake `gcloud` and `fugaro` for Checks 27, 111, 146 and 401, fake `gcloud`,
// `git`, `curl` and `fugaro` for Check 190, fake `gcloud`, `fugaro` and a fake
// Realtime Database behind a fake `curl` for Check 191, fake `gcloud`, `fugaro`
// and `curl` for Check 195 (a small project whose service and account appear and
// disappear, whose IAM policy names what was granted to it, and whose build ends
// the way its own step's line says), a fake `gcloud` that
// evaluates the conditions of Check 340's own IAM bindings for Check 340, and
// fake `gcloud`, `fugaro` and a fake credentials service, GitHub and Identity
// Toolkit behind a fake `curl` for Check 96 -- or, for Checks 145 and 180,
// against a fake Bitbucket Cloud on a loopback port; and Check 149's is a fake
// `fugaro` alone, answering the one read the check makes out of the case's own
// files and logging every call, so a case can say what the check read and what
// it did not -- so no
// network, no cloud project and no credential is touched here, and no live
// step of any of them is run by an agent. Check 130's fake is a fake `curl`
// alone, answering the three anonymous GETs the check makes about a
// repository; its fake `gcloud` and `fugaro` log any call and fail, so the
// test can say the check never reaches a cloud tool. Check 69's fakes are a
// fake `curl` (the origin probe, the four pages and one status per HEAD, all
// out of the case's own files), a fake `openssl` for the certificate's subject
// and its names, and a fake `npm` that writes the artifact `check69.sh` then
// compares the deployed page against -- with fake `gcloud`, `fugaro` and
// `belong` that log any call and fail, since the check names no cloud project
// and runs no `fugaro` command. Check 300's fakes are a
// fake `gcloud`, a fake `fugaro` and a fake RTDB behind a fake `curl`, and
// Checks 303 and 304's fake is a fake `fugaro` alone -- 303's answers `doctor`,
// `version`, the daemon's `run`, the node's `state` and `events` and the
// installation's config, and 304's never performs a handoff, so a case can say
// the check stopped at the build's own verb help -- and Check 305's are a fake
// `fugaro`, a fake `gcloud` and a fake front door on a loopback port that mints
// the check's tokens and serves the state group's own documents, with a fake
// browser that opens the authorization URL the check prints and follows it to
// the check's own loopback, so both sign-ins run with no network. Check 330's
// are a fake `fugaro` answering the three installation reads it makes and a fake
// `gcloud` whose runs bucket, per-execution gateway logs and job descriptions
// are files of the case's own, both logging every call with the directory it was
// made in: a case says what the check read off the run record and the gateway log
// of the pair -- and which of the two review variants, which env entries and
// which credential a run's review was served by -- as well as that it read the
// installation from the sandbox and nowhere else. Check 361's
// are a fake `gcloud` that mints a token sentinel and a fake
// `curl` that answers the check's one read from files the test writes and
// records the request -- the URL, the headers, and the Authorization header
// of the `--config` file the check handed over -- so a case can say the token
// travelled through a 0600 file and never onto a command line or into the
// block. Check 401's fake `curl`
// serves the grants node from the case's own directory and its fake `fugaro`
// update PRETENDS to be the migration, so a case says what the converge did by
// writing files. Checks 410 to 412 drive the diagnosis of a real failed run
// against a fake `gcloud` (a project that is NOT_FOUND) and a fake `fugaro`
// whose update fails and leaves a log; 411's fake `fugaro` also writes the
// source record a machine without git would have fetched, and its PATH is built
// to hold no git at all, so the case says the check really ran with none; 412's
// fake `claude` is the client the check wraps, so the wrapper is the real one
// and the tool log is the child's own. Checks 500 and 501 drive a fake `gcloud`
// and a fake `fugaro` that logs every call with the directory it was made in, so
// a case says both what the check measured and that no image was built, no
// `fugaro update` was answered for the owner and no run was launched. Checks 520
// and 521 drive the same two fakes over the vision-model work: a fake `fugaro`
// whose `doctor --json` carries the `vision-coder` sentence the premise is read
// from, whose `run --json` launches the run the check follows, whose `runs ls
// --json` says whether it has ended, and whose `cancel` answers the command 521
// stops a run with -- against a fake `gcloud` that serves the run's record from
// the case's own directory, with a mode for each state a case bends: a build
// older than the release, a coder that reads images itself, a slot that is set,
// a run that never ends, a record that is another run's, and a refusal whose
// sentence has lost one of the parts an owner needs.
// Check 440's fakes are a fake `fugaro` that resolves a run ending the way the
// CLI does -- the active runs and the window first, the whole bucket only on a
// miss, the numbered pick list and one question only at a real terminal, and
// never a cancel through an ending -- and a fake `gcloud` for the two rows that
// read the runs bucket, over a run set each case seeds, so a case says what the
// check measured and that it launched nothing here.
// Check 291's fakes are a fake `fugaro`, a fake `gcloud` and a fake `gh`, with
// real git against a bare repository inside each case's own directory: the
// catalog entry's line, the job's stamp and the names a run drops, the
// reservation read with a name set on the job by hand, and the run's own
// transcript all come out of the case's own state files, so a case bends one
// reading at a time and says what the check then reads -- and its `--dry-run`
// case pins that a plan makes no call at all. Its fake `run` names the run it
// launched only in the `--json` document (a person's own line is a reporter step
// on stderr, and its short name is not the run's id), and its fake `runs ls`
// keeps a run in flight until its second poll and writes that run's transcript
// only where the row turns terminal, so a check that read a run id off the human
// line or read a transcript before its run ended fails these cases.
// Checks 490 to 492 drive a fake `fugaro` and a fake `gcloud` over the
// post-review triage work, one per reading an owner takes on a real run: 490
// launches ONE sandbox run and reads its record's triage block and its
// deferrals, 491 reads a parent run's deferrals and files one follow-up task per
// group, and 492 launches TWO sandbox runs (the second with `--security`, so its
// record carries a level-3 triage round) and grades that each run's finding is
// placed by the mode's table as `block` with the floor's own sentence as its
// reason and that no deferral took effect. The fakes answer a launch per leg, a
// listing whose rows the case bends (a run that never ends, one the listing does
// not know), a record per run (another run's, one that is not a document, a mode
// the step does not govern, no triage block, an entry a verdict moved, a
// deferral naming the floor's own key, a level-3 round that never ran) and a
// cancel that does not answer.
// Check 470's fakes are a fake `fugaro` that holds the three sources `issue
// mine` answers from (`shared`, `snapshot`, `local`), the snapshot file the real
// CLI writes on a successful read and the two cache files the check's Restore is
// about, and a fake `gcloud` holding the issue store's three prefixes, refusing
// them on a knob in the two shapes a case has to tell apart -- a permission
// denial, which is the reading U1 asks for, and a call that did not answer,
// which stops the run -- so a case says what the check read, in which directory
// it read it, and that a dry run made no call at all; the network the offline
// mode turns off is the fake's own switch, because no test and no agent turns a
// machine's network off.
// Check LC4's fakes are a fake `gcloud` and a fake `fugaro`: the fake `fugaro`
// answers `doctor --json` from the case's own directory, and the fake `gcloud`
// serves the job list (applying the label filter, so a check that filtered on
// the wrong labels is caught), the job, each secret and each secret's policy,
// keyed by the ID the check asks about -- so a case can say the account's job
// was found by its labels, that its own token secret was read and that no
// other account's was mounted or made readable.
// Check 313's fakes are a fake `fugaro` and a fake `curl` that performs the
// handshake, beside a daemon of the test's own -- a real loopback server holding
// the id's lock and writing its registry file -- so the mode and the nonce, the
// handshake, the SIGKILL, the removed dead file and the pid a live process takes
// are all readings of processes and files the case owns; Check 314's are a fake
// `fugaro` whose `doctor --json` and `orchestrator ls --json` answer out of the
// case's own files and a fake `gcloud` that answers the account and the project's
// IAM policy, so a case says which project the policy was read on, which person's
// roles it carries, and that a run which stopped earlier called nothing more.
// Check 119's script is the one whose subject is a file rather than a cloud or
// an installation: it reads the installer a manual dispatch of
// `console-windows.yml` uploaded -- its name, its size, its SHA-256 and whether
// its PE header really says x64 -- so it needs no fake at all, and its test
// builds those bytes in a temporary directory and bends them one at a time (a
// text file, a DOS stub, a truncated download, a 32-bit image, an empty
// upload), asserting what the script printed and what it refused.
// Check 210's fakes are a fake `fugaro`, a fake `gcloud`, a fake `git` and a
// fake `gh`: the fake launcher is a counter, so the run IDs are the case's own,
// and the fake `git` and `gh` really delete a branch and really move a pull
// request from OPEN to CLOSED, so the teardown's own read-backs are readings of
// state that changed.
// Check 601's fakes are a fake `gcloud` that serves a job's own describe in the
// v1 shape a job really has -- spec.template.spec.template.spec, a secret as
// valueSource.secretKeyRef, a volume's as secret.secretName -- with and without
// a mounted secret and, for the case that pins it, in an execution's own shape
// instead; a project's IAM policy; the review run's signature and record as
// files the test writes; and the credentials service's own audit lines. A fake
// `fugaro` whose `doctor --json` carries the census entry the review job and its
// Claude account are read from, and a fake `curl` that mints a token sentinel
// per account, records the account, the route and the body it was handed and the
// authorization header of the `--config` file, and answers each route as a mode
// says -- with the type that route's own refusal carries, so a case can say
// which class a refusal was about (and that a body refusal, or a state the
// service could not read, is not one), and that the token travelled through a
// 0600 file and never into the check's output.
// Check 211's fakes are a fake `fugaro`, a fake `gcloud` and a fake `belong`:
// the fake `gcloud` serves the run's own objects at its own prefix in the runs
// bucket out of the case's own directory and refuses every other bucket and
// every other prefix, so the case bends the stored judge input one needle at a
// time (a run ID, a branch, the pair ID, an arm's model word, a run-ID-shaped
// string) and says the row FAILs and names the hit -- and a case per string the
// vocabulary deliberately leaves (the judge's own model, a word after a model's
// version, a line that looks like a commit trailer) says it does not, since a
// false FAIL there is a correct release exiting 1 after a billed judge run; the
// fake launcher is a counter, so the run ID is the case's own, and a
// case that rewrites a copy of the script with the U1 row's own test removed
// proves that row is what the FAIL elsewhere is read from. The fake `belong`
// logs any call and fails, so a case says the check never reached that
// installation.
// Check 212's are the same four, down to the counter and the
// real deletion and close, with the documents a pair, a judge run and its
// verdict are read through served out of the case's own directory and a mode for
// each state a case bends (a build without the command, a catalog without the
// judge recipe, a layer that samples nothing, a challenger on the direct or an
// unadmitted route, no pair within the bound, a shadow that published, a judge
// that published, a verdict that is missing, skipped, one-pass or another pair's,
// and a report that already draws a conclusion).
// Check 550's fakes are the
// same two, with the alias the installation holds kept in a file the fake
// `fugaro` reads and writes, so a case can say what a repoint moved and what the
// move back put back: its two runs' records are served from the case's own
// directory, and a case bends the review stage's account in one of them to show
// the check's claim failing in either direction.
// Check 301's fakes are a fake `fugaro`, a fake launchd and a fake systemd
// whose boot id and unit files the test writes, so a reboot, a second
// instance's refusal and T7's re-render are all readings of files the test
// owns; Check 302's are a fake `fugaro`, a real `uname` and a real terminal
// (a pty its helper types the phase's one confirmation into), and it rewrites
// a copy of its own script to prove the case that expects a quiet daemon log
// to be a finding is not vacuous. Check 310's fake is a fake `fugaro` that
// answers the four commands the check drives in the shapes internal/report and
// internal/initflow print them -- the line in flight, the step lines, the
// closing line, the plan view, the `--json` document on stdout and the account
// on stderr -- files a log with the sink's own name shape, mode and retention
// rule, and fails exactly when the check's own fake `terraform` is first on
// PATH, so the forced failure is the check's own shim end to end and a case
// bends the one thing it judges. Check 311's fake is none: its subject is a
// Windows console, so PowerShell and Windows both have to be there for its
// steps to run at all, and its test is mostly a reading of its own text -- the
// block keys, the phases, the guards, the readings and the ones it refuses to
// take -- beside the six cases that run nothing (`-SelfTest`, `-DryRun`, the
// three refusals that come before the first tool is looked for, and the stop a
// machine that is not Windows makes), which it RUNS wherever `pwsh` is on PATH
// and says so in one line where it is not. Its
// one case that is neither runs the self-test's own three rules again in bash,
// over the script's text, so the tree that carried the rule-1 defect is red on
// a machine without PowerShell too. Check 350's are a fake `gcloud` and a fake
// `fugaro` beside a REAL local checkout and its bare origin -- git stays on this
// machine -- with one shared call log for both fakes, so a case says what the
// check read AND in what order: the hash of the old record is taken before the
// first CLI read of the window, which is half of what step 4 is about, and a
// case that rewrites a copy of the script with the comparison removed proves
// the FAIL it reads elsewhere is the comparison's. Check 200's are a fake
// `gcloud` that serves the run's own record and the image's build record, a
// fake `fugaro` that answers `version`, `doctor`, the settings write -- which it
// REFUSES, the way the CLI does, since no command writes that key -- `run` and
// `runs ls`, and a fake `go` whose build writes a binary that answers a paid
// name with the core stub -- beside a REAL local git repository and its bare
// origin, so the throwaway branch, the commit, the push and the Restore's own
// deletion are readings of a repository the test owns rather than of a fake's
// log.
// Check 27's fakes are a fake `gcloud` and a fake `fugaro` that hold a throwaway
// project which exists only once something creates it, its billing link, its
// Firestore location, its buckets, its jobs, its secret containers and its
// images — and whose `setup` REFUSES the create path when it has no terminal, so
// a case says both what the check read back and that the wizard was really given
// a pty. No live step of it is run.
// Check C0's fake is a fake `fugaro` alone, serving each read out of a document
// the test writes -- so a case knows the document's exact length and can hold
// the byte count in the readings table against the file -- and recording every
// call with the directory it was made in and the FUGARO_PROJECT it carried, so
// a case says both where each read ran and that a dry run or a refused name
// makes no call at all.
// Checks 420 to 422 are the model-crafts owner-run checks: 420 and 421 drive a
// fake `fugaro` whose `craft ls --json`, `accounts pool ls --json`, `runs ls
// --json` and `run` answer out of the case's own files and whose `creds ls`
// names the credentials the check reads, over a fake `gcloud` and a fake
// credentials service behind a fake `curl`, and 422 drives a fake `fugaro`
// whose converge REALLY migrates the case's own local config the way the
// release does, a fake `gcloud` that prints an access token and a fake `curl`
// that serves the published policy node before and after it, with a mode for
// each state a case bends: a build without the crafts, another installation's
// config, a converge that fails or leaves a step, one that migrates nothing,
// an account left unconverted with and without the note it is owed, a second
// converge that moves the keys, and a node that moved, went or cannot be read.
// Checks 380 to 382 drive a
// fake `fugaro` over the no-prompt policy's three readings, each fake holding
// what its check can tell apart: 380's `update` plays the transcript a real run
// would print (a step left for the owner, a `[y/N]` question, a question left
// open, an enter-prompt, a cloud failure, an empty transcript, a run that never
// ends) and records what its own stdin turned out to be, so a case says the
// check really closed it; 381's holds the repository's caps node, the effective
// caps, a `budget set` that REALLY applies what it is handed -- so the check's
// read-back of its own lowering and of its own putting back are readings of
// state that changed -- and an `image build` that plays the refusal in each
// shape a case bends (another cap, a lost part, an empty transcript, a build
// that was not refused, an odd status, one that never ends); 382's refuses on
// the marker set in ITS OWN environment (agentMarker's list) and can be told to
// bend the sentence a raise prints (a missing part, the command naming another
// repository, another value, another project, an invented flag, another
// program), so a check that forgot to set the marker is caught by the caps
// moving rather than by a log line. 381's and 382's checkouts are real git work
// trees with an origin remote, because the repository whose caps are lowered or
// raised is the one that remote names. 139 drives a fake `fugaro` that serves
// the runs list and one run's record out of the case's own files and a fake
// `gcloud` that serves that run's `model call` entries, with a mode for each
// state a case bends: a listing whose only settled runs are oauth, one whose
// gateway runs carry no sub-agent, no entry at all, a stage name carrying two
// sessions (not a finding: the runner reuses stage names across sessions), a
// sub-agent id spanning two sessions or sharing a session with an entry whose
// agent_id is empty (in another stage: the comparison is run-wide), an id seen
// only on refused entries, a background model that carries an id, a background
// model the run never called, a background model that is also a stage's own
// model (the run's effective background, which the row refuses to count), an
// account, a run or a log view naming belong, a read that fails, hangs or
// answers something that is not a document, a record naming no execution, a
// record and a listing that disagree about the execution, a doctor that exits 1
// (an answer, #2719) and one that does not answer at all.
var liveCheckScripts = []struct {
	name string
	test string
}{
	{"check27.sh", "check27_test.sh"},
	{"check69.sh", "check69_test.sh"},
	{"check90.sh", "check90_test.sh"},
	{"check91.sh", "check91_test.sh"},
	{"check111.sh", "check111_test.sh"},
	{"check210.sh", "check210_test.sh"},
	{"check211.sh", "check211_test.sh"},
	{"check212.sh", "check212_test.sh"},
	{"check130.sh", "check130_test.sh"},
	{"check145.sh", "check145_test.sh"},
	{"check146.sh", "check146_test.sh"},
	{"check148.sh", "check148_test.sh"},
	{"check149.sh", "check149_test.sh"},
	{"check180.sh", "check180_test.sh"},
	{"check190.sh", "check190_test.sh"},
	{"check191.sh", "check191_test.sh"},
	{"check195.sh", "check195_test.sh"},
	{"check291.sh", "check291_test.sh"},
	{"check300.sh", "check300_test.sh"},
	{"check301.sh", "check301_test.sh"},
	{"check302.sh", "check302_test.sh"},
	{"check303.sh", "check303_test.sh"},
	{"check304.sh", "check304_test.sh"},
	{"check305.sh", "check305_test.sh"},
	{"check310.sh", "check310_test.sh"},
	{"check311.ps1", "check311_test.sh"},
	{"check313.sh", "check313_test.sh"},
	{"check314.sh", "check314_test.sh"},
	{"check330.sh", "check330_test.sh"},
	{"check340.sh", "check340_test.sh"},
	{"check96.sh", "check96_test.sh"},
	{"check361.sh", "check361_test.sh"},
	{"check401.sh", "check401_test.sh"},
	{"check-410.sh", "check-410_test.sh"},
	{"check-500.sh", "check-500_test.sh"},
	{"check-501.sh", "check-501_test.sh"},
	{"check-411.sh", "check-411_test.sh"},
	{"check-412.sh", "check-412_test.sh"},
	{"check-c0.sh", "check-c0_test.sh"},
	{"check510.sh", "check510_test.sh"},
	{"check511.sh", "check511_test.sh"},
	{"check520.sh", "check520_test.sh"},
	{"check521.sh", "check521_test.sh"},
	{"check440.sh", "check440_test.sh"},
	{"check420.sh", "check420_test.sh"},
	{"check421.sh", "check421_test.sh"},
	{"check422.sh", "check422_test.sh"},
	{"check490.sh", "check490_test.sh"},
	{"check491.sh", "check491_test.sh"},
	{"check492.sh", "check492_test.sh"},
	{"check350.sh", "check350_test.sh"},
	{"check470.sh", "check470_test.sh"},
	{"check-lc4.sh", "check-lc4_test.sh"},
	{"check119.sh", "check119_test.sh"},
	{"check57b.sh", "check57b_test.sh"},
	{"check124.sh", "check124_test.sh"},
	{"check-c39.sh", "check-c39_test.sh"},
	{"check-380-no-prompt-update.sh", "check-380-no-prompt-update_test.sh"},
	{"check-381-over-cap-build.sh", "check-381-over-cap-build_test.sh"},
	{"check-382-agent-cannot-raise-a-cap.sh", "check-382-agent-cannot-raise-a-cap_test.sh"},
	{"check550.sh", "check550_test.sh"},
	{"check139.sh", "check139_test.sh"},
	{"check200.sh", "check200_test.sh"},
}

// liveCheckTimeout is each shard's own deadline. The scripts are independent
// programs that share nothing but this test: one hanging fake must not decide
// another's result, so every shard runs CONCURRENTLY under its own deadline --
// and the deadline is here rather than left to the package's -timeout, whose
// message would name the package and not the script that hung. Measured on a
// Mac with 3 shards per script (6 side by side): the package took 479s, the
// longest shard 478s (check91 shard2); unsharded it took 854s (check90 503s
// alone). Twelve minutes is 1.5x that, so a loaded machine has room, and it
// fires well inside CI's -timeout 20m, so a hang is reported by name rather
// than as a package-wide panic. Run `go test ./scripts` with -timeout 30m or
// more locally: Go's default 10-minute package timeout is too short.
//
// The deadline is only worth having if it actually ends the run, and that is
// what procgroup.Run below is for: see the comment there.
const liveCheckTimeout = 12 * time.Minute

// liveCheckShards is how many parallel subtests each script's tests are split
// into (CHECK_TEST_SHARD=i/n, read by the scripts' own t() helper): every n-th
// test goes to shard i, each in its own temp dir, so the wall time of the
// package is about the longest shard instead of the sum.
const liveCheckShards = 3

// liveCheckWorkers is how many shards run at once. Splitting a script into
// shards only shortens the package if the shards really run side by side, and
// subtests under t.Parallel() are capped at -parallel, which defaults to
// GOMAXPROCS -- five on the machine this comment was written on. A shard spends
// most of its life waiting on the child processes it starts rather than on a
// CPU, so that cap left the machine half idle and the package ran to 848s: no
// room against the `go test -timeout 15m` of this repository's verify command,
// and three more scripts of the model-crafts plan (Checks 420 to 422, MC10)
// took it past the deadline. The same package with -parallel 20 ran in 573s, so
// what the cap limited was the shards and not the work. The pool below is that
// without the flag: the checks are independent programs that share nothing but
// this test, and each one still gets its own deadline and its own temp dir.
const liveCheckWorkers = 8

// syncBuffer collects a command's output and can be read while a copy
// goroutine may still be writing to it. procgroup.Run gives up on its drain
// after its own bound and returns anyway when something outside the killed
// group still holds the pipe, so the writer it was handed may be written to
// after Run has returned; a plain bytes.Buffer would then be read and written
// at the same time.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestLiveCheckScripts runs the bash test of each live check.
//
// The shards go through liveCheckWorkers goroutines rather than through
// t.Parallel() subtests, so that how much of the package runs at once is this
// file's decision and not -parallel's: see the comment on that constant.
func TestLiveCheckScripts(t *testing.T) {
	for _, tool := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	type shard struct {
		test string
		n    int
	}
	var shards []shard
	for _, c := range liveCheckScripts {
		for i := 0; i < liveCheckShards; i++ {
			shards = append(shards, shard{c.test, i})
		}
	}
	queue := make(chan shard)
	var (
		mu       sync.Mutex
		failures []string
		wg       sync.WaitGroup
	)
	for i := 0; i < liveCheckWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range queue {
				// One failure must not end the package: the message is kept and
				// the rest of the shards still run, so one run says what every
				// live check did rather than what the first failure hid.
				if msg := runLiveCheckShard(s.test, s.n); msg != "" {
					mu.Lock()
					failures = append(failures, msg)
					mu.Unlock()
				}
			}
		}()
	}
	for _, s := range shards {
		queue <- s
	}
	close(queue)
	wg.Wait()
	for _, msg := range failures {
		t.Error(msg)
	}
}

// runLiveCheckShard runs one shard of one live check's bash test and returns
// the message to report, or "" when it passed. The shard's name is in every
// message: the -run filter no longer reaches into a subtest, so the name is
// what says which check a hang or a failure is about.
func runLiveCheckShard(test string, shard int) string {
	name := fmt.Sprintf("%s/shard%d", test, shard)
	ctx, cancel := context.WithTimeout(context.Background(), liveCheckTimeout)
	defer cancel()
	path := filepath.Join(testutil.ModuleRoot(), "scripts", "live-checks", test)
	// procgroup, not exec.CommandContext: both suites deliberately leave
	// background processes holding the output pipe (Check 90's proxy and
	// upstream, Check 91's sleeping fake gcloud and its interrupt tests),
	// and a context kill reaches the direct child only -- bash -- so
	// CombinedOutput would keep reading a pipe its grandchild still holds
	// and block past the deadline: the hang would then surface as Go's own
	// package timeout, a panic naming the package, which is the one thing
	// the deadline above exists to avoid. procgroup.Run puts the run in
	// its own process group, SIGTERMs the whole group when the deadline
	// fires (the scripts trap it and clean up after themselves), SIGKILLs
	// whatever is left once the grace period is over, bounds its own drain
	// -- so this returns, by script name, however the child misbehaves.
	var out syncBuffer
	code, err := procgroup.Run(ctx, procgroup.Cmd{
		Name:   "bash",
		Args:   []string{path},
		Env:    append(os.Environ(), fmt.Sprintf("CHECK_TEST_SHARD=%d/%d", shard, liveCheckShards)),
		Stdout: &out,
		Stderr: &out,
	})
	if ctx.Err() != nil {
		return fmt.Sprintf("%s did not finish within %s and was killed:\n%s", name, liveCheckTimeout, out.String())
	}
	if err != nil {
		return fmt.Sprintf("%s: %v\n%s", name, err, out.String())
	}
	if code != 0 {
		return fmt.Sprintf("%s exited %d:\n%s", name, code, out.String())
	}
	return ""
}
