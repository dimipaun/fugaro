package scripts_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// liveCheckScripts are the owner-run live checks' scripts and their tests.
// Each test drives its script against fake tools on PATH -- a fake `claude`
// and a fake upstream for Check 90, fake `gcloud`, `fugaro` and `curl` for
// Check 91, fake `gcloud` and `fugaro` for Checks 27, 111, 146 and 401, fake `gcloud`,
// `git`, `curl` and `fugaro` for Check 190, fake `gcloud`, `fugaro` and a fake
// Realtime Database behind a fake `curl` for Checks 191 and 193 (193's fake
// `gcloud` holds a Cloud Run job of each of two repositories — each running as
// its own account, one of them mounting a model credential when the case says
// the flip has not reached it, and the policy its `run services get-iam-policy`
// read is answered with, whose roles/run.invoker binding is the other half of
// the per-repository switch — and answers the check's Cloud Logging reads with the credentials service's
// own `credsvc: audit` lines for the throwaway runs the fake `fugaro` records,
// so a case can say what served a run, what the run declared and what it was
// served instead), fake `gcloud`, `fugaro`
// and `curl` for Check 194 (194's fake `gcloud` holds two sandbox repositories'
// Cloud Run jobs, each mounting the installation's project-wide secret
// container of a workflow secret, carrying an account of its own and answering
// the check's Cloud Logging reads with the credentials service's own
// `credsvc: audit` lines -- and it evaluates the IAM policy of one of those
// containers, so a case can say what an account holds an accessor on; its fake
// credentials service behind a fake `curl` serves the two
// `creds/workflowsecrets` records -- the one that lists the repository under
// test and the one that admits the whole workspace -- and refuses every write,
// so a case says both records' container ids, what a run was served, what
// another repository's job was refused and that no record ever carried a
// value), fake `gcloud`, `fugaro`
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
// `fugaro update` was answered for the owner and no run was launched. Check 580
// drives a fake `fugaro`, a fake `gcloud` and a fake `git` over one Bitbucket
// shared job's run: the fake `git` answers the sandbox checkout's own origin, the
// fake `fugaro` answers `version`, `doctor --json`, `project policy show --json`
// and `runs ls --json`, launches the run under the id it is given and prints the
// row template the case wrote, and the fake `gcloud` serves the job list and the
// credentials service's own `credsvc: audit` lines for that run -- so a case can
// bend the policy, the profile's jobs, the run's row and the service's lines and
// say what the check made of each, and can say that no run was launched at all
// where it must not be.
// Check 581 drives those same two fakes over the multi-repository catalog build: a fake
// `fugaro` that answers `version`, `doctor --json`, `image ls --json`, `image
// survey --json` and `image build`, printing the submitted line and the bake
// note the real one prints and marking the entry as built so the second catalog
// read is the one after it, and a fake `gcloud` that serves the build's own
// steps -- so a case can bend the map, the note and the steps and say what the
// check made of each. Checks 520
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
// Check 292 drives a fake `fugaro`, a fake `gcloud`, a fake `docker` and a
// fake `git` over the catalog build context: the fake `fugaro` answers
// `version`, `doctor --json`, `image add`, `image ls --json`, `image rm`,
// `project policy show --json` and `update`, and its `image build` stages the
// entry's Dockerfile, the commit and a digest of the tree under the entry's
// context before it prints the submitted line, writes the record the build's
// own last step writes and stamps the image's revision label -- so the case
// that cancels a submitted copy can decide whether the record moved with the
// staged digest or not. The fake `gcloud` serves the runs bucket, the two
// staged objects, the build's own steps and Cloud Logging (one JSON line per
// entry, stamped by the case, filtered as the check filters it) and removes
// what a `storage rm` names -- refusing one when the case asks, so the restore
// row that names a removal it could not make is pinned too -- the fake
// `docker` reads the copied file out of the image and the image's
// `org.opencontainers.image.revision`, and the fake `git` answers the
// checkout's origin, cuts the branch, commits and pushes what the check
// writes, logging every call -- so a case says which commit the record and
// the label name, what a decision carried, and that a refusal pushed nothing.
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
// (an answer, #2719) and one that does not answer at all. Check 127's fakes are
// a fake `fugaro`, a fake `git` and a fake `gh`, each answering out of the
// case's own documents and logging every call: the check reads a sandbox
// repository's runs, its config and its pull requests and launches nothing, so
// a case says which repository each read named and that the backlog index was
// regenerated in a copy rather than in the checkout.
// Check 197's fakes are a fake `fugaro` -- a published policy whose one
// GitHub profile admits one sandbox repository, a `run` that RECORDS the run it
// was asked for and refuses a repository outside that profile with the
// launcher's own sentence, and a `runs ls` whose rows carry the execution the
// job is read out of -- and a fake `gcloud` that serves the job list (a SHARED
// job of the profile beside the per-repository job a run lands on when its
// repository selects no profile, with a mode each for a mount that is still
// there, a named account's token, a plain value, a provider key, a Bitbucket
// job and a job whose env does not name the service), one job's or one
// execution's own spec, and the Cloud Logging read whose filter carries the run
// id and whose lines are the service's own `credsvc: audit` lines, so a case
// says what served a run, under which verified account, with which credential,
// tier and purpose, and what a run's own execution kept across a re-apply. Its
// Bitbucket half is the plan's own "(after D2079)": a case that names no
// Bitbucket repository, or one no shared Bitbucket job serves, reads those rows
// as INFO and the run still ends 0.
// Checks 590 and 591 are the Bitbucket token-types owner-run pair. Check 590's
// fakes are a fake `fugaro` whose `creds set` really answers `--json` (a mode
// per shape the add path can return: a document whose type is not what was
// asked for, `write_verified` true, an identity read for a type that has none,
// and its exit code for a refusal), a fake `git` whose `ls-remote` answers two
// heads unless the case says it was refused, and a fake
// Bitbucket Cloud on a loopback port whose `/2.0/user` carries a `kind` and a
// scope header per token type, one of them a 403 -- so a case bends one answer
// at a time and says what the block then reads; Check 591's are fake `fugaro`,
// `gcloud` and `git` (the last failing every call, so a check that ran git of
// its own shows up) beside one loopback server answering BOTH faces of the real
// thing: Bitbucket Cloud's API with a different answer for each token type, and
// the credentials service's front door on the proxy route, whose 403
// `unverified_run_identity` is the one refusal the check forces. The fake
// `gcloud` serves the service's own `credsvc: audit` lines for the runs the fake
// `fugaro` records, filtered by the run id the check put in its own filter, so a
// case says which token served which repository -- with a lagging mode for the
// seconds Cloud Logging takes to ingest a line. Both tests write only sentinels
// and assert that no sentinel survives a run, and both fake servers echo back
// what they were sent in a hostile mode, so an endpoint's own text can never
// reach the block.
// Check 610 is the review-only job's own owner-run check (RJ9 of the review-job
// plan): one `fugaro review --pr N` on a pull request of a sandbox repository
// whose `fugaro.yaml` carries no `profiles:` block and a baked workflow, which is
// the repository a review resolves onto the implicit review-only job for. Its
// fakes are a fake `fugaro`, `gh`, `gcloud` and `git`, each answering out of the
// case's own documents and logging every call, so a case says which command read
// what: the doctor that names the installation and its base image, the checkout's
// resolved config (a mode for a `profiles:` key of its own, a workflow that is
// not baked, no workflow at all and an auth that is neither an API key nor
// Vertex), the published policy (a profiles block, a default account that does
// not admit the repository, no budget mode at all), the job list
// (no implicit job, two of them, one without the service's address, one naming
// the secret env or mounting a secret volume, one on another image, one beside a
// coding project job), the launch document (another job, a named account, no run,
// no job), the run's own row (still running, failed, another pull request,
// another run) and the review's artifact (no artifact, another run, another pull
// request, no verdict, another reviewer account) -- plus the service's own
// `credsvc: audit` lines, which the fake `gcloud` serves for the run the fake
// `fugaro` recorded and which a case bends into no served line at all, a first
// call under another credential, two calls whose earliest by its own timestamp is
// not the document's first, a line of another run, and a read that fails. The
// four UNVERIFIED items of the plan are one row each, and every case asserts on
// that row's own sentence.
// Check 830 is Batch's own VM-to-job link, the probe the plan blocks BR3 on: one
// throwaway Batch job in `fugaro-dev` whose container reads the metadata server
// itself (the instance it is on, its project, the instance's attributes and an
// identity token in full format), a second job that tries to write the first
// job's identity into every launcher-controlled field it has (its own labels,
// the labels Batch puts on the VMs, and the task's variables), and one plain
// Compute Engine VM for O8's avenue. Its fake `gcloud` is a small model of Batch
// and Compute driven by files the case writes -- the jobs that exist, each job's
// uid, state and object, the instance each job ran on, the instance objects, one
// task-log answer per uid, the project-wide log answer, the CreateJob audit
// record, and every job configuration the check submits, so a case can read the
// body it sent, including the marker variable's stand-in value, which the test
// asserts never reaches the block or the progress log. A `compute instances
// create` builds the object Compute would report out of the labels and metadata
// the call carried, so the plain VM's row measures what a launcher can write
// rather than what the test chose. The tree is a fixture too: the case writes the
// log filter into internal/backend/batch/logs.go, the managed label pair into
// internal/backend/gcp/names.go and the wrapped variable's name into
// docs/batch-run-identity/, and two cases bend those files to prove the check
// reads them out of the tree rather than remembering them.

// Check 831 is the proof R11 asks for, the one check that runs the whole thing
// end to end: one real sandbox run launched on Batch, its job held to the run by
// the run's own FUGARO_RUN variable rather than by a name, the ledger that says
// it opened its session and was metered, the ticket the launcher put on the job
// and where Batch printed it (the job's own document, the logs and the CreateJob
// audit record -- D3115's rule measured rather than assumed) -- and then four
// attacks made as a SECOND launcher, every one of them running as the run's own
// job account so that a refusal is about the VM and not the account: the ticket
// the second launcher's own read of the run's job found, replayed into a job of
// its own, the same job with no ticket at all (the control), a SECOND launch's
// job deleted while it is still queued and re-created under the same name with
// that run's unspent ticket (D3108's id, F30 -- the queued window is the attack,
// and the row FAILs when the job was not queued when it was deleted), and the
// issuance record read straight out of the database with the second launcher's
// own token. Its fakes are a `gcloud`, a `curl` and a `fugaro` over a small
// model of Batch, the Realtime Database and Cloud Logging driven by files the
// case writes -- the
// jobs that exist, each job's uid, state and object, one task-log answer per
// uid, the project-wide log answer, the CreateJob audit record, the database's
// answers per path with a different answer from the second read on (so a row
// that waits for a value rather than reading once is measured), every job
// configuration the check submits, and a delete whose acceptance is listed for a
// while afterwards and whose name stays un-submittable (ALREADY_EXISTS) until
// the platform has finished with it -- so a case can read the body it sent,
// including the ticket, which the test asserts never reaches the block, the
// progress log or any file but the one of its own. The refusal a claim comes
// back with is what the two claim rows -- the replay and the re-creation -- are
// judged on, and its own sentence is
// what tells the VM's refusal from the ones the route answers WITHOUT asking the
// link -- the once-only fence's, the ticket's own life, and `not_served` -- so
// cases bend that sentence each of those ways and the rows are required to FAIL
// on every one of them: a row that read any refusal as its own would pass on a
// tree with no VM-to-job link at all. The tree is a fixture too, and four cases
// bend it: no ticket's variable, another path family, no run variable and a run
// variable of another shape.

// Checks 50, 780 and 781 are the keys-through-service work's owner-run checks,
// and the three are the ones whose fakes have to hold STATE rather than answer
// reads, because what each check measures is what its own commands changed.
// Check 50's fakes are a fake `gcloud`, `git` and `fugaro` over a small model
// of installation `fugaro` (GCP project `fugaro-dev`, the sandbox repository
// `edgeappinc/fugarosandbox`) that the check MUTATES: `models add` and `models
// rm` move a provider route's claim on a Claude ID through the operator's own
// config -- which is what the real `project policy publish` rebuilds the node
// from, so a publish naming a model no entry claims is REFUSED here the way it
// is there -- `project routing set` writes the switch, `update` writes
// `FUGARO_MODEL_PRICES` into the job's environment, `secrets versions
// disable/enable` move one numbered version, and `run` writes a record and the
// reviewer stage's own `model call` lines for the mode the case asked for
// (direct, routed, `auto` with and without the key), with a bend per way a case
// breaks one reading: a direct run that carries `route`, a routed run that
// carries none, a reviewer stage that produced no line at all, a priced pin
// that RUNS, a validate that accepts an unclaimed one, a safeguard naming no
// switch or another one, a publish that accepts the switch without the claim, a
// repository config that tolerates a routing key, a version that cannot be
// re-enabled or was never enabled, and each of step 7's three restore arms --
// so a case says what the check recorded, what it refused, and what it put
// back, on a project that is bent away from `fugaro-dev`, a checkout that is
// not the sandbox's and an installation that is not `fugaro` alike.
// Check 780's fakes are the same three over an installation still at the
// release before Release A: a repository whose jobs are fed from a model
// credential, the container behind that mount and no bundle, and a fake `fugaro
// update` that is the one thing which changes it -- writing the run's row
// terminal, the job without its mount, the container gone and a bundle version
// created now, and printing the lines the real update prints -- so a case bends
// an update that fails, one that copies nothing, a job that keeps its mount, a
// container left behind, one another job still mounts, a deletion the
// transcript never names, a run that never finishes and an operator's
// transcript that claims a deletion, and says which of them the check recorded
// as its row. Check 781's are a fake `gcloud` and a fake `fugaro` over one
// installation whose credentials service serves every model call: the fake
// `fugaro` answers `doctor --json`, launches a run and answers the runs list,
// and the fake `gcloud` serves the secrets list with the one bundle, the run's
// record, the job's environment and the two log reads (the gateway's `model
// call` lines and the service's own `credsvc: audit` line), so a case bends no
// bundle, a job that still mounts a provider key, a job with no service URL,
// calls that all went direct, a routed call nobody reported, an audit line
// whose forward was denied, a run that never ends and a record that is another
// run's, and says what the check charged the provider's reported cost against.
// Checks 628 to 632 are the model-crafts work's owner-run checks (its plan task
// MC26), and each drives the same two fakes over one small model of a sandbox
// installation: a fake `fugaro` whose reads and writes come out of the case's
// own files and whose every call is logged with the directory it was made in,
// and a fake `gcloud` serving the runs bucket and Cloud Logging. 628's fake
// `fugaro` answers the config view, the craft list, the published node, the
// launch and the runs list, and its fake `gcloud` the record and the gateway's
// own lines, so a case bends a layer that names no model, a craft that resolves
// another model, an allow-list that admits nothing, a stage that records
// another model and a gateway refusal that names a stage -- and says what the
// check made of each. 629 adds the pool's second member: its audit lines carry
// the credential that answered each call, so a case says which member served
// the run, whether the first was exhausted rather than skipped, and what the
// publish wrote to the node. 630 writes NO pool at all -- it asks the write
// door for one and reads the listing back to be sure it is absent -- and its
// fake `fugaro update` is the convergence, splitting the crossed pool in the
// order it listed its members and moving the node's version, so a case bends a
// refusal that names one member, a validate that is not the second door and a
// config the update finds already split. 631 is the one whose fake has to hold
// the value: the fake `fugaro` declares, lists and removes the credentials, its
// fake `gcloud` serves the record and the service's own audit line, and a pty
// the case starts (python3's pty.openpty, handed to the check through
// CHECK631_TTY) is the terminal the two pastes are made at -- with values
// written into the terminal's own input queue, so the check's hidden prompt
// reads the first and the command's own hidden prompt the second, and a case
// says what was echoed, what was written into the config directory and into a
// shell history, and what a session was refused. 632 holds the run and the
// update at once: its fake `fugaro` launches a run and answers the runs list,
// its fake `update` prints the one in-flight line and writes the converged
// reads, and its fake `gcloud` serves the record and the run's own ledger, so a
// case bends an update that fails, an update that asks for another command, a
// run that never ends and a record that is another run's.
// Checks 570 to 575 are the portal's live checks (P9.4), and each has fakes of
// its own: Check 570's are a fake `fugaro` and a fake portal behind a fake
// `curl`, over one installation and the licence it buys, so a case says what the
// check paid for, what it minted and what a renewal read. Check 571's is a fake
// `gcloud` serving one project's IAM policy and the run services it enumerates,
// so a case says which account reaches the signing key and which does not. Check
// 572's is a fake `curl` serving the portal's own anonymous 401, the admitted
// entitlements read and the two refusals the SSO rule gives an email-link
// account, recording whether an authorization config was carried. Check 573's is
// a fake `curl` that verifies a webhook delivery the way the service does --
// HMAC-SHA256 over `ts:<body>` under the case's own secret -- and answers the
// operations queue, so a case says what the check sent, what a redelivery left
// in the ledger and what a replay outside the window was refused with. Check
// 574's are a fake `gcloud` and a fake `fugaro` over one project's keyring and a
// released build that verifies a licence key the way the build does, recording
// whether FUGARO_LICENCE_KEY was in each child's environment, so a case says
// which id the build's ring holds and what a production build did with a
// staging-signed key. Check 575's are a fake `curl` that serves a reseller's own
// terms, customers, keys, statements, the operations area's workspaces and the
// credit limit's refusal, and a fake `gcloud` that serves the API's revision, the
// scheduler job, the fire and the run line api_statements.go logs -- so a case
// bends a sale the credit limit must refuse, a sale whose workspace the mint
// cannot resolve (the project id a key carries against the workspace document id
// the mint reads), a token that may not read the operations area, a month whose
// lines do not sum and a token minted for the wrong audience, and says what the
// check recorded of each.
// licence-key-v2's Checks 560 to 564 are the owner's, and each has fakes of its
// own. Check 560's are a fake `gcloud` serving the Scheduler job, the licence
// job, the fire, the execution it started and the job's own log lines over one
// installation's key: a case says what a tick that renewed the key reads, and
// what a fire that started no execution reads. Check 561's add a fake `fugaro`
// answering `doctor --json` and two `licence status --json` reads -- one before
// the tick and one after it -- so a case bends a revocation the server answered
// about another key, a tick that reached the server with the key still good, and
// each answer D2140's unreachable half must produce or refuse (a key given up on
// a failed check, a state that moved, a failure the record did not count, a
// last-ok time that moved, a check recorded as an answer). Check 562's are a
// fake `curl` serving the licence site's own 403 and the CLI's loopback refusal
// page, and a fake `fugaro` whose `licence login` prints an authorize address
// and holds a listener, so a case says what a wrong-`state` callback answered,
// whether the flow was still running after it, and that no key-shaped string
// reached the block. Check 563's are a fake `fugaro` whose `licence login
// --device` prints an address and a code and then ends either approved or
// expired, so a case says what each half holds and that a code which ran out
// wrote nothing. Check 564's are a fake `curl` over the portal's API and the
// Paddle SANDBOX API, a fake `fugaro` whose `licence login` prints an authorize
// address and whose `licence status --json` holds a check the case places before
// or after the refund, and a fake `gcloud` whose Scheduler job is in a location
// the installation's own Cloud Run region is NOT (Scheduler does not serve every
// region, which is what K4.2's module works around), together with the licence
// job's executions and both of the check's fires: so a case bends a transaction
// the sandbox does not hold, a renewal that did not move the term, a refund that
// withdrew only the key the event named rather than the whole licence, a fire
// that failed, a job in no location the project serves, a tick whose fire started
// no execution, and an installation that has recorded no check since the refund.
// Check 760's fakes are a fake `fugaro` answering `doctor --json` and the four
// `runs ls` reads it times, over a window that names two pull requests -- one of
// them with no index entry, so its read falls back to the scan -- and a fake
// `git` that fails every call: the fake can sleep a plan, so a case says the
// figure a row prints is the MIDDLE of the three samples it also prints, and a
// read that never answers is held to the check's own deadline and killed with its
// process group.
// Check 740's fakes are a fake `fugaro` and a fake `gcloud` over one run that is
// messaged while it is going: the fake `fugaro` answers `doctor --json`,
// `version` and `tell --help`, launches the run the case's own document names,
// walks `rows.1.json`, `rows.2.json` ... and stays on the last that exists (so a
// case can drive both a run that reaches implement and ends and one that never
// reaches implement at all), and prints the number each message took; the fake
// `gcloud` serves the run's own record, the job and the account it really ran
// as, the project's jobs, the Policy Troubleshooter's own answer and the
// bucket's lifecycle configuration, each with a status the case can bend -- so a
// case says what became of each message (the plain one delivered into a coder
// stage; the `--now` one delivered with NO reason on its fate, which is the
// runner's own statement that the stop found a quiet point and the same session
// resumed, settling U2 and U3) and WHICH principal the troubleshooter was asked
// about, and says that no message was sent into a stage that never ran. Every
// case holds the same two things wherever it is about something else: a
// message's own text never reaches the block, and no access token is asked for.
// Check 670's fakes are a fake `fugaro`, a fake `git`, a fake `gcloud` and a
// fake `gh` over one installation and the GitHub sandbox. The fake `fugaro`
// prints the six steps of an enrolment and reads the project's name it asks for,
// answers `doctor --json`, the credentials listing, `status` (whose screen and
// whose document both turn green only once the case's own fill has happened, so
// F11's empty container and the row that names its fix are read against a state
// that really changed) and `runs ls`, which reports the run under the removal
// terminal only once the removal has happened; it applies `secrets set` and
// `project repos rm` as writes on the case's own files, the removal modelling
// the CLI's own question -- with a terminal it asks for the project's name and
// reads it, and with none it refuses in the CLI's words, which is where a case
// reads D2407's strict confirmation. The fake `git` makes the throwaway checkout
// and holds the branch a run pushed, the fake `gcloud` the built image, the
// container with an enabled version and the run's objects in the runs bucket,
// and the fake `gh` the App's installation and the pull request the run opened.
// Every row of the block has a failing form and an unmeasurable one, and every
// case holds that the App's private key reaches neither the block, the output
// nor the scratch directory.
// Checks 881 and 882 are the model-credentials routing work's owner-run checks.
// 881's fakes are a fake `fugaro` (a routing table, a budget, a pool of
// accounts whose members the case sets a state on, a launch and a run listing)
// and a fake `gcloud` serving the run's own record out of the runs bucket, so a
// case says which source of the chain served the run, that the fall-through was
// flagged and counted, and that nothing was launched where the pool could still
// serve. Its belong refusal needs no fake: the guard is the script's own, and a
// case exercises it by pointing the check at an installation whose name carries
// belong. 882's are a fake `fugaro` (a table that routes
// two Claude models to Vertex, an `enforce` project with a per-run cap, a launch
// the case can refuse and a run listing), a fake `gcloud` that mints an
// impersonated token per job account, serves the job list, the IAM policy, a
// role's own permissions and the run's record, and a fake `curl` that answers
// the Vertex probe and records the URL, whether the Authorization header
// arrived through the `--config` file rather than a command line, and that no
// value ever reached the call log, so a case says what the service served and
// priced, what a job's account was refused, and what each finding is.
// Check 680's fakes are a fake `fugaro`, a fake `gcloud` and a fake `git` over
// the installation's own project config, written to hold STATE rather than
// answer: the fake `fugaro` judges the very file the check doctors -- the base
// image's release against the one the workflow's job has to know, then the
// credentials service's own switch, then the read the tier makes in its own
// project -- which is what lets a case arrange an installation by writing that
// file, and its `troubleshoot` answers the tier the judgement allows while
// appending an execution line the way a launched job would; the fake `gcloud`
// serves the project's executions, `projects describe` and a mini object store
// the bucket's reads come out of; the fake `git` gives a checkout's toplevel and
// its origin. So a case says what each of D2408's three parts read -- the
// deliberately refused launch and the absence of an execution behind it, the
// tier made unavailable by a project that is not there or a service this
// installation does not publish, and the cloud answer with its cost, its record,
// its class marker, its artifacts and the bundle that is gone -- that the launch
// is made only after the decline that proved it refused, that the config comes
// back byte-identical with its sidecar removed, and that no value of the
// installation's ever reaches the block.
// Checks 720 and 721 are the workspace-and-members owner-run checks, and 721
// makes one IAM write of its own. 720's fakes are a fake `fugaro` that records
// each command it is given together with the installation it was selected on,
// and a fake `gcloud` whose `projects describe` answers each project's PARENT
// from the marker the fake `fugaro` leaves, so a case says which command ran
// against which workspace and what the folder move and its return left behind;
// its fake `fugaro` answers a pty-holding `setup --create-project` the way a
// real terminal does. 721's are a fake `gcloud` that holds the member's project,
// makes the binding its own writes ask for and serves Google's Admin Activity
// -- one entry per binding ADDED, the member's own update's, the check's own and
// a third party's when the case asks for one -- and a fake `fugaro` whose
// `update` records that it ran and whose `workspace show` and `workspace
// project status` answer with a member table and a drift row built out of what
// that gcloud did, so a case says which entry the row names and which it leaves
// out.
// Checks 890 to 892 are the repo-coupling audit's owner-run checks, and each
// only reads. 890's fakes are a fake `gcloud` and `curl` answering out of the
// case's own Realtime Database tree, bucket listing and Firestore ids, so a case
// says which listed path a storage-path row of the audit's report names and
// which it leaves UNNAMED. 891's are a fake `fugaro` whose `update --plan-only
// --json --events` feed differs by the directory it ran in, and a fake `gcloud`
// serving each repository's launch.json, so a case says which stage differs
// between an empty directory and a checkout and whether that stage is a §4
// blocker. 892's are a fake `gcloud` holding the projects' and the build and
// job accounts' IAM policies and a fake `curl` serving the grants node, so a
// case says which principal is an admin in all but name.
// Check 920 is the image-secrets work's owner-run check. Its fakes are a fake
// `gcloud` (the jobs of two repositories, a run's own executions, the service's
// `credsvc: audit` lines and the run's record out of the runs bucket, the
// build identity's account and the project's IAM policy, the containers'
// accessors and the catalog build's steps), a fake `curl` answering the
// projectsecrets record only through the 0600 config file the check hands over,
// and a fake `fugaro` whose `version`, `doctor`, `runs ls`, `run` and `review`
// the case bends -- so a case says which container a job and a run's own pods
// mount, what the service served a run and its review, what an unlisted
// repository was refused with, and that a catalog build resolves no accessor
// for the build identity.
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
	{"check181.sh", "check181_test.sh"},
	{"check190.sh", "check190_test.sh"},
	{"check191.sh", "check191_test.sh"},
	{"check193.sh", "check193_test.sh"},
	{"check194.sh", "check194_test.sh"},
	{"check195.sh", "check195_test.sh"},
	{"check196.sh", "check196_test.sh"},
	{"check197.sh", "check197_test.sh"},
	{"check291.sh", "check291_test.sh"},
	{"check292.sh", "check292_test.sh"},
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
	{"check-580.sh", "check-580_test.sh"},
	{"check-581.sh", "check-581_test.sh"},
	{"check-411.sh", "check-411_test.sh"},
	{"check-412.sh", "check-412_test.sh"},
	{"check-c0.sh", "check-c0_test.sh"},
	{"check510.sh", "check510_test.sh"},
	{"check511.sh", "check511_test.sh"},
	{"check520.sh", "check520_test.sh"},
	{"check521.sh", "check521_test.sh"},
	{"check540.sh", "check540_test.sh"},
	{"check541.sh", "check541_test.sh"},
	{"check542.sh", "check542_test.sh"},
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
	{"check127.sh", "check127_test.sh"},
	{"check590.sh", "check590_test.sh"},
	{"check591.sh", "check591_test.sh"},
	{"check610.sh", "check610_test.sh"},
	{"check611.sh", "check611_test.sh"},
	{"check628.sh", "check628_test.sh"},
	{"check629.sh", "check629_test.sh"},
	{"check630.sh", "check630_test.sh"},
	{"check631.sh", "check631_test.sh"},
	{"check632.sh", "check632_test.sh"},
	{"check640.sh", "check640_test.sh"},
	{"check640-queue-probe.sh", "check640-queue-probe_test.sh"},
	{"check770.sh", "check770_test.sh"},
	{"check771.sh", "check771_test.sh"},
	{"check772.sh", "check772_test.sh"},
	{"check773.sh", "check773_test.sh"},
	{"check50.sh", "check50_test.sh"},
	{"check780.sh", "check780_test.sh"},
	{"check781.sh", "check781_test.sh"},
	{"check570.sh", "check570_test.sh"},
	{"check571.sh", "check571_test.sh"},
	{"check572.sh", "check572_test.sh"},
	{"check573.sh", "check573_test.sh"},
	{"check574.sh", "check574_test.sh"},
	{"check575.sh", "check575_test.sh"},
	{"check560.sh", "check560_test.sh"},
	{"check561.sh", "check561_test.sh"},
	{"check562.sh", "check562_test.sh"},
	{"check563.sh", "check563_test.sh"},
	{"check564.sh", "check564_test.sh"},
	{"check-760-runs-ls-speed.sh", "check-760-runs-ls-speed_test.sh"},
	{"check790.sh", "check790_test.sh"},
	{"check791.sh", "check791_test.sh"},
	{"check810.sh", "check810_test.sh"},
	{"check811.sh", "check811_test.sh"},
	{"check-830.sh", "check-830_test.sh"},
	{"check-831.sh", "check-831_test.sh"},
	{"check-840.sh", "check-840_test.sh"},
	{"check-740-run-messages.sh", "check-740-run-messages_test.sh"},
	{"check-750-review-against-task.sh", "check-750-review-against-task_test.sh"},
	{"check-720.sh", "check-720_test.sh"},
	{"check-721.sh", "check-721_test.sh"},
	{"check670.sh", "check670_test.sh"},
	{"check-881-chain-falls-through.sh", "check-881-chain-falls-through_test.sh"},
	{"check-882-vertex-capped.sh", "check-882-vertex-capped_test.sh"},
	{"check680.sh", "check680_test.sh"},
	{"check-883-pause-resume.sh", "check-883-pause-resume_test.sh"},
	{"check-884-update-migrates.sh", "check-884-update-migrates_test.sh"},
	{"check-885-retired-keys.sh", "check-885-retired-keys_test.sh"},
	{"check-890-data-layout.sh", "check-890-data-layout_test.sh"},
	{"check-891-update-trace.sh", "check-891-update-trace_test.sh"},
	{"check-892-iam.sh", "check-892-iam_test.sh"},
	{"check920.sh", "check920_test.sh"},
	{"check-390.sh", "check-390_test.sh"},
	{"check-391.sh", "check-391_test.sh"},
	{"check-392.sh", "check-392_test.sh"},
	{"check732.sh", "check732_test.sh"},
	{"check733.sh", "check733_test.sh"},
	{"check-910-lift-snapshot.sh", "check-910-lift-snapshot_test.sh"},
	{"check-911-lift-compare.sh", "check-911-lift-compare_test.sh"},
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

// liveCheckCIShardEnv is how CI's `scripts-live-*` runners split the checks
// between them: `<index>/<of>`, the shape of the `-run` filter that used to
// stand in for it. It has to be a variable read here because `-run` cannot do
// it: the shards below are goroutines in this test's body rather than
// subtests, and Go runs a top-level test whose name matches the first element
// of a multi-element pattern even when no subtest of it matches -- so the
// filter `^TestLiveCheckScripts$/^[^/]+$/^shard0$` selected this test on all
// three runners and each one ran every check.
const liveCheckCIShardEnv = "LIVE_CHECK_SHARD"

// parseLiveCheckCIShard reads that variable. An empty value is the local case
// and runs everything; a value it cannot read is an error rather than a
// fallback to the whole suite, because a typo would otherwise put every check
// on every runner again -- the state this variable exists to end, and one that
// costs the runner its whole budget rather than reporting itself.
func parseLiveCheckCIShard(env string) (index, of int, set bool, err error) {
	if env == "" {
		return 0, 0, false, nil
	}
	i, o, ok := strings.Cut(env, "/")
	if !ok {
		return 0, 0, false, fmt.Errorf("%s=%q is not <index>/<of>", liveCheckCIShardEnv, env)
	}
	index, err = strconv.Atoi(i)
	if err != nil {
		return 0, 0, false, fmt.Errorf("%s=%q: %v is not a shard index", liveCheckCIShardEnv, env, i)
	}
	of, err = strconv.Atoi(o)
	if err != nil {
		return 0, 0, false, fmt.Errorf("%s=%q: %v is not a shard count", liveCheckCIShardEnv, env, o)
	}
	if of < 1 || index < 0 || index >= of {
		return 0, 0, false, fmt.Errorf("%s=%q: a shard is 0 <= <index> < <of>, so no runner would own this one", liveCheckCIShardEnv, env)
	}
	return index, of, true, nil
}

// TestLiveCheckCIShardCoversTheSuiteOnce: the split CI makes is a partition of
// this file's items -- every one of them on exactly one runner, each item
// unchanged -- and a variable it cannot read is refused rather than run whole.
// The suite is one item per script per shard -- every liveCheckScripts entry on
// each of liveCheckShards shards, and under a minute each, so the figures are
// derived from those two lists rather than spelled here; a split that dropped
// one, ran one twice or fell back to everything would show up as a green run
// that tested less, which is the failure this pins.
func TestLiveCheckCIShardCoversTheSuiteOnce(t *testing.T) {
	var items []liveCheckShard
	for _, c := range liveCheckScripts {
		for i := 0; i < liveCheckShards; i++ {
			items = append(items, liveCheckShard{c.test, i})
		}
	}
	for _, of := range []int{1, 2, 3, 4, 6, 8} {
		seen := map[liveCheckShard]int{}
		for index := 0; index < of; index++ {
			shard := ciLiveCheckShards(items, index, of)
			if len(shard) == 0 {
				t.Fatalf("of %d: runner %d is given nothing: one runner of a split this size would report success having run no check at all", of, index)
			}
			// In the file's order, so that the split is a property of this
			// file rather than of the runner: two runs of the same commit
			// hand a runner the same list.
			next := 0
			for _, s := range shard {
				for next < len(items) && items[next] != s {
					next++
				}
				if next == len(items) {
					t.Fatalf("of %d: runner %d is given %v after %v, and this file lists them the other way round", of, index, s, shard[0])
				}
				seen[s]++
				next++
			}
		}
		if len(seen) != len(items) {
			t.Fatalf("of %d: the runners are given %d distinct items and this file lists %d: the split has to cover the suite, and a drop is a green run that tested less", of, len(seen), len(items))
		}
		for s, n := range seen {
			if n != 1 {
				t.Fatalf("of %d: %v is given to %d runners: an item run twice spends a runner's budget on work another runner is doing", of, s, n)
			}
		}
	}

	for _, c := range []struct {
		env        string
		index, of  int
		set        bool
		wantRefuse bool
	}{
		{env: ""},
		{env: "0/1", index: 0, of: 1, set: true},
		{env: "2/3", index: 2, of: 3, set: true},
		{env: " 0/3", wantRefuse: true},
		{env: "0 /3", wantRefuse: true},
		{env: "0", wantRefuse: true},
		{env: "0/", wantRefuse: true},
		{env: "/3", wantRefuse: true},
		{env: "x/3", wantRefuse: true},
		{env: "0/x", wantRefuse: true},
		{env: "0/0", wantRefuse: true},
		{env: "-1/3", wantRefuse: true},
		{env: "3/3", wantRefuse: true},
		{env: "4/3", wantRefuse: true},
		{env: "0/3/4", wantRefuse: true},
	} {
		index, of, set, err := parseLiveCheckCIShard(c.env)
		switch {
		case c.wantRefuse:
			if err == nil {
				t.Errorf("%s=%q was read as shard %d of %d: a runner told to run something that is not a shard would run the whole suite or none of it, and neither says so", liveCheckCIShardEnv, c.env, index, of)
			}
		case err != nil:
			t.Errorf("%s=%q: %v", liveCheckCIShardEnv, c.env, err)
		case set != c.set || (set && (index != c.index || of != c.of)):
			t.Errorf("%s=%q was read as shard %d of %d (set %v), want %d of %d (set %v)", liveCheckCIShardEnv, c.env, index, of, set, c.index, c.of, c.set)
		}
	}
}

// liveCheckShard is one item of the live suite: one script's test, and which
// of that script's own CHECK_TEST_SHARD shards it is.
type liveCheckShard struct {
	test string
	n    int
}

// ciLiveCheckShards is the shard of this file's items a CI runner runs: item i
// goes to runner i%of, so every item runs on exactly one runner, the assignment
// is stable across runs, and a check added to liveCheckScripts lands on a
// runner on its own. The items are numbered in the order this file lists them,
// which is what makes that stability a property of the file rather than of the
// runner.
func ciLiveCheckShards(shards []liveCheckShard, index, of int) []liveCheckShard {
	var mine []liveCheckShard
	for i, s := range shards {
		if i%of == index {
			mine = append(mine, s)
		}
	}
	return mine
}

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
// file's decision and not -parallel's: see the comment on that constant. The
// same choice is why CI splits this test by liveCheckCIShardEnv instead of by
// `-run`: what a runner is given is the list it starts from, and the list is
// built here.
func TestLiveCheckScripts(t *testing.T) {
	for _, tool := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	var shards []liveCheckShard
	for _, c := range liveCheckScripts {
		for i := 0; i < liveCheckShards; i++ {
			shards = append(shards, liveCheckShard{c.test, i})
		}
	}
	// What this runner was given of them, when CI says so. It is read after
	// the whole list is built rather than left to the workflow's `-run`
	// filter, which cannot reach into this test: see liveCheckCIShardEnv.
	if index, of, set, err := parseLiveCheckCIShard(os.Getenv(liveCheckCIShardEnv)); err != nil {
		t.Fatal(err)
	} else if set {
		all := len(shards)
		if shards = ciLiveCheckShards(shards, index, of); len(shards) == 0 {
			t.Fatalf("%s=%d/%d takes none of this file's %d shards, so this runner would report success having run nothing", liveCheckCIShardEnv, index, of, all)
		}
	}
	queue := make(chan liveCheckShard)
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

// TestLiveCheck610NamesTheImplicitReviewJobs pins Check 610's spelled-out job names
// to the derivation that produces them. check610.sh cannot compute a job name --
// gcp's derivation sanitizes the profile, provider and account into a readable part,
// cuts that part to thirty-six characters and appends twelve hex of a hash -- so it
// carries the two implicit review-only names as constants, and it tells a review
// variant from a job of a profile by the derivation rather than by a segment of the
// name: the cut is why the github job's account segment reads `default-rev` and never
// `default-review`, so the substring a reader would look for is not in the name at
// all. A rename in infra has to break here instead of making the live check report
// that a real installation deploys no review-only job.
func TestLiveCheck610NamesTheImplicitReviewJobs(t *testing.T) {
	dir := filepath.Join(testutil.ModuleRoot(), "scripts", "live-checks")
	scripts := map[string]string{}
	for _, name := range []string{"check610.sh", "check610_test.sh"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		scripts[name] = string(b)
	}
	for _, provider := range infra.ImplicitReviewProviders {
		name := infra.ImplicitReviewJobName(provider)
		for _, script := range []string{"check610.sh", "check610_test.sh"} {
			if !strings.Contains(scripts[script], name) {
				t.Errorf("%s does not carry the implicit review-only job name infra.ImplicitReviewJobName(%q) derives, %q, so the check would read a real installation as one that deploys no such job", script, provider, name)
			}
		}
	}
	// The stem is what the job-list reader counts the jobs of, and it is also what a
	// message about two deployments names: it has to be the github name without its
	// trailing hash, never a name of its own.
	const assignment = `IMPLICIT_PREFIX="`
	i := strings.Index(scripts["check610.sh"], assignment)
	if i < 0 {
		t.Fatalf("check610.sh carries no %s assignment", assignment)
	}
	rest := scripts["check610.sh"][i+len(assignment):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("check610.sh's %s assignment is not closed", assignment)
	}
	stem, github := rest[:j], infra.ImplicitReviewJobName("github")
	if github == stem || !strings.HasPrefix(github, stem) {
		t.Errorf("check610.sh counts jobs of the stem %q, which is not the github implicit review-only job's name %q with its hash taken off", stem, github)
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
