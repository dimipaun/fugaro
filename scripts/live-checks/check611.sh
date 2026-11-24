#!/usr/bin/env bash
# Check 611 — the implicit Bitbucket review-only job: one `fugaro review --pr N`
# of a sandbox pull request, launched on the installation's own review-only job
# of provider bitbucket, ended `succeeded` with its review recorded, and that job
# holding no Bitbucket token at all (the credentials service's git route carries
# its git traffic).
#
# Plan task RJ10 of docs/review-job/implementation-plan.md writes this check; the
# number is that work's own reservation (its plan reserves 610-619, and its
# register paragraph records 610 and 611 as used). RJ9's Check 610 is the same
# reading on a GitHub repository: what RJ9 measures there, this measures here,
# with the Bitbucket half's own provider — a job whose git traffic can only be
# the credentials service's.
#
# What it settles, read from the outside and nowhere else:
#
#   * **RJ8**, by its behaviour: with the credentials service on and a Bitbucket
#     repository recorded, an installation with no profiles deploys ONE implicit
#     review-only job for provider bitbucket, `fugaro review --pr N` resolves
#     onto it (and not onto a per-repository job, which a review must never run
#     on), the run finishes with its review recorded, and the job it ran on
#     mounts no credential of any kind — no Bitbucket token above all, because
#     its clone of the pull request's head can only be the service's git proxy.
#   * **U1** — the base-image review path for baked-image repositories: the job
#     an implicit review runs on is deployed with the installation's BASE image
#     (infra's implicitReviewVariant takes it from lc.BaseImage), and the review
#     of a repository that carries an image of its own still ran there.
#   * **U2** — that a project with no profiles admits the default account to a
#     repository (F4): the published policy defines no profile at all, and its
#     accounts block carries the default account with this repository in its
#     list, so the service's admitted set for the repository is not empty.
#   * **U3** — the default account when the project's auth is an API key or
#     Vertex: the account the review runs as is the default one (the implicit
#     job's account segment is fixed where it was deployed), and the reading of
#     the credential that answered its model calls — the service's own forward
#     line — is taken whatever that auth is.
#   * **U4** — a live `terraform apply` with no project jobs: the installation
#     holds project jobs, and every one of them is the implicit review-only job
#     this release's own naming derives, while the policy defines no profile a
#     coding job could have been built from. The golden plan test is the offline
#     half; this is the live one.
# None of the five is answerable offline: which job a launch resolves, whether
# the run it starts finishes and records a verdict, what the deployed job mounts
# and what the service served are all answers of a live installation.
#
# What it measures:
#
#   * the sandbox checkout: it is a Fugaro repository's checkout, its own origin
#     is a BITBUCKET repository (a GitHub one is refused by name: this check is
#     the Bitbucket half and there is nothing here for it to measure), and the
#     project, region and installation it belongs to are read from the checkout
#     itself (`fugaro doctor --json`), so the check measures the installation you
#     work in rather than one it guessed;
#   * the published policy (`fugaro project policy show --json`): the PROJECT
#     admits the repository, NO profile of the document admits it (a profile
#     that did would give the repository a shared project job, and its review
#     variant is what a review of it would run on — not the implicit job this
#     check is about), the budget mode is one the gateway exists in (`observe` or
#     `enforce` — off is not a mode any more, D3009: the gateway is what reaches
#     the service), and the node's
#     `bitbucket_token` block admits the repository with `proxy: true` — which is
#     the launcher's own gate (nodeBrokersBitbucket) and the only way a job that
#     mounts no token can reach bitbucket.org at all;
#   * ONE review, launched with `fugaro review --pr N --json` from the checkout.
#     What the launch document is held to: it answered `launched`, its own `job`
#     is the installation's implicit review-only job of provider bitbucket — the
#     name this release derives for (config.DefaultJobProfile, provider,
#     config.ReviewVariantAccount + config.ReviewVariantSuffix) — its `account` is
#     the default one, which is the only account that job serves, and the pull
#     request it is about is the one this check asked for (every row below is read
#     by the run id the document names, so a document about another pull request
#     is a launch this check cannot say it asked for);
#   * the run's own row, polled out of `fugaro runs ls --pr N --json` until it is
#     terminal: it ended `succeeded` with outcome `reviewed`, it pushed nothing
#     and it names the pull request it reviewed;
#   * the job itself, out of `gcloud run jobs list`: the job the launch named
#     carries `FUGARO_CREDENTIALS_SERVICE_URL`, and it carries NO
#     `FUGARO_BITBUCKET_TOKEN`, no `FUGARO_SECRET_ENVS` and no secret reference
#     of any kind (RJ8, CSR7: the review-only variant mounts nothing at all) —
#     so its clone of the pull request's head can only have gone through the
#     service. The same document is where the installation's own project jobs
#     are read for U4, and the job's container image is what U1 compares against
#     the base image the doctor reports;
#   * the verdict, read back with `fugaro review show <slug>/<run-id> --json`:
#     the artifact is there, and it names the repository, the pull request, the
#     head it read and the account and model of the reviewer. A review run that
#     ended `succeeded` with no artifact is a review that recorded nothing;
#   * the credentials service's OWN lines for that run (`gcloud logging read`
#     over `credsvc: audit`, filtered by the run id): at least one line of
#     purpose `git` with status `ok`, which is the run's clone and fetch of the
#     pull request's head arriving at the service rather than at a token the job
#     holds — and the earliest line of purpose `forward` with status `ok`, whose
#     credential NAME is the answer U3's model half is about: an account the
#     node admits for this repository, or one of the two names this release uses
#     for the project's own keys.
#
# What it creates: ONE review run of the sandbox repository (**billable**: its
# model calls are real) and the verdict objects it writes into the run's own
# record. It pushes nothing, opens nothing and comments on nothing — a review
# never touches the repository (spec §5) — so, unlike a coding run, this check
# leaves no branch and no pull request behind. It writes nothing into the
# installation: no cloud resource, no record, no object and no node.
#
# Usage:      bash scripts/live-checks/check611.sh --sandbox <a sandbox Bitbucket repository checkout> --pr <the pull request to review>
#             bash scripts/live-checks/check611.sh --sandbox <path> --pr <N> --dry-run
#             bash scripts/live-checks/check611.sh --help
# Needs:      bash, mktemp, python3 (every `--json` document is read with it),
#             git (the one remote read that names the repository), gcloud
#             (authenticated as YOU: the job list and the service's own lines are
#             read with your credentials) and fugaro (the installed release — the
#             review this check launches is the one you have).
# Where:      your own machine, and the **sandbox** only. A project, a region, an
#             account, a repository or a path naming belong is refused by name,
#             and so is a coding agent's session: every command this check runs
#             is one you could type yourself.
# Costs:      wall clock, not a fixed fee: a review run is shorter than a coding
#             run and it pushes nothing — the same `timeouts.total` class, and its
#             model calls (**billable**) are real, on the sandbox.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check611_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's own record stays, as every run's
#             does; nothing is left ON the repository, because a review pushes
#             nothing.
# Output:     progress on stderr, ONE block on stdout headed CHECK 611 RESULT,
#             then CHECK 611 RESULT END (exit N) — to copy back into the
#             register's Check 611 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold, or a guard stopped the check before it could run. 2: a row
#             could not be measured for a reason that is this check's own setup
#             or the installation's configuration rather than a break (the block
#             says which, by name). A key the run never reached reads `FAIL —
#             not reached`, which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value and no
# authorization header. Every fact below is a NAME — an account's name, a
# credential's name, a job's name, a repository's name, a status — and every line
# the service writes and this check reads is reduced to a short plain-word shape
# before it is printed. This check holds no credential at all: it makes no
# Bitbucket API call of its own, and the only identity in the path is the one
# Cloud Run gives the job it launched.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK611_SANDBOX (or --sandbox: the checkout of a SANDBOX Bitbucket repository
# — REQUIRED, and the repository this check reviews is read from it), CHECK611_PR
# (or --pr: the pull request to review — REQUIRED), CHECK611_DRY_RUN,
# CHECK611_KEEP_SCRATCH, CHECK611_RUN_SECONDS (how long the review run is waited
# for; 2400), CHECK611_POLL_SECONDS (20), CHECK611_LOG_AGE (1h: the least the
# read of the service's lines looks back — a run that outlasted it is read from
# its own launch, because the row that proves the model half is its FIRST call),
# CHECK611_LOG_LIMIT (200), CHECK611_LOG_WAIT_SECONDS (120),
# CHECK611_LOG_POLL_SECONDS (5), CHECK611_DOCTOR_TIMEOUT_SECONDS (180) and
# CHECK611_CLI_TIMEOUT_SECONDS (180). None of them can point this check at
# another installation: the project and the region are read from the checkout.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check prints its
# own RESULT block, and a trace of a run that launches a run would copy that
# run's own output to the terminal.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory.
stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass
# — and the check ends as one that could not measure (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

# need SECONDS: run one command under a bound. The child execs, so the pid the
# bound kills IS the command — a hung cloud read would otherwise outlive the
# check that gave up on it. The ERR trap is dropped in it: this check runs
# commands whose failing exit status is one of its own readings, and a trap
# inherited there would turn each of them into "an internal error" before the
# step could report it.
run_bounded() { # run_bounded SECONDS KEY CMD... (run from $SANDBOX, and the KEY names the .out/.err pair it is read back through)
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  ( trap - ERR; if [ -d "${SANDBOX:-}" ]; then cd "$SANDBOX" || exit 1; fi; exec "$@" ) \
    >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$secs" ] || break
    sleep 1
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    BOUNDED_TIMED_OUT=yes
    kill -TERM "$pid" 2>/dev/null || true
    sleep 2
    kill -KILL "$pid" 2>/dev/null || true
  fi
  BOUNDED_STATUS=0
  wait "$pid" 2>/dev/null || BOUNDED_STATUS=$?
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 611 needs bash"
  local t
  for t in mktemp python3 git gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 611 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk611-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
PRINTED=""

INSTALL=""
PROJECT=""
REGION=""
REPO=""
REPO_PROVIDER=""
PR=""
BASE_IMAGE=""
CHECKOUT_KIND=""
# SANDBOX_STOP: the checkout could not be read far enough to name the
# installation — this check's own setup, or a doctor that did not answer — and
# every row below rests on that installation. POLICY_STOP: the published policy
# admits neither the repository nor the brokering a review on the implicit job
# needs, so nothing is launched. RUN_ROW is yes only when a TERMINAL row of this
# check's own run was read: the rows read off the run, its job, its verdict and
# the service's lines for it are rows about that row, and one that was never read
# leaves them unmeasured rather than guessed at.
SANDBOX_STOP=""
POLICY_STOP=""
RUN_ROW="no"
LAUNCHED="no"
# LAUNCH_EPOCH is the second the review was launched in, and empty until then: it
# is what log_freshness measures the read of the service's lines from.
LAUNCH_EPOCH=""
# What the rows below are read against, each set by the step that read it.
PROFILE_COUNT=""
DEFAULT_ADMITTED=""
ACCOUNT_ADMITS=""
TOKEN_PROXY=""
TOKEN_ADMITS=""
MODE=""
LAUNCH_JOB=""
LAUNCH_RUN=""
LAUNCH_RID=""
LAUNCH_ACCOUNT=""
LAUNCH_PR=""
LAUNCH_STATUS=""
JOB_IMAGE=""
JOB_URL=""
JOB_TOKEN=""
JOB_SECRETENVS=""
JOB_SECRETREFS=""
JOB_VERTEX=""
PJOB_COUNT=""
PJOB_NAMES=""
FIRST_FORWARD=""
GIT_LINES=""
GIT_CREDS=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk611.XXXXXXXX") || die "Check 611 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "${HOME:-}" | "${HOME:-}"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  if [ -n "$KEEP_SCRATCH" ]; then
    SCRATCH_KEPT=yes
    say "keeping $SCRATCH (CHECK611_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created, said rather than assumed. ONE review run and
# its own record: a review pushes nothing, opens no pull request and writes no
# comment, so there is nothing on the repository to undo — and the run's record
# is the installation's to keep, exactly as every other run's is.
restore() {
  if [ -n "$DRY_RUN" ]; then
    fact restore PASS "this was a dry run: no command was given to fugaro or gcloud, no review was launched and nothing was created"
    return 0
  fi
  if [ "$LAUNCHED" = yes ]; then
    fact restore PASS "one throwaway review of ${REPO} was launched (${LAUNCH_RID}) and is NOT undone: its own record and its verdict objects stay in the installation, exactly as every run's do — and it left nothing ON the repository, because a review pushes no branch, opens no pull request and writes no comment. This check creates no cloud resource and writes no record of its own"
    return 0
  fi
  fact restore PASS "no review was launched, so nothing was created: it stopped before the launch. This check creates no cloud resource, writes no record and touches no repository"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory, so there is none to remove"
  elif [ "$SCRATCH_KEPT" = yes ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK611_KEEP_SCRATCH is set), at $SCRATCH"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  # A row that reads FAIL is one of two things, and the verdict says which: a
  # criterion that did not hold (exit 1), or a row this check could not measure
  # (exit 2). Every unmeasured row is recorded FAIL too, so that it is never read
  # as a pass, and telling them apart is what UNMEASURED_KEYS is for.
  local key hard=no
  for key in $(printf '%s\n' "$facts" | sed -n 's/^\([^|]*\)|FAIL|.*/\1/p'); do
    case " ${UNMEASURED_KEYS} " in
    *" $key "*) ;;
    *) hard=yes ;;
    esac
  done
  if [ "$hard" = yes ]; then
    if [ "$status" = 0 ]; then status=1; fi
  elif [ -n "$UNMEASURED_KEYS" ]; then
    if [ "$status" = 0 ]; then status=2; fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# redact: what a gcloud or fugaro message may carry back into the block, which is
# pasted into a register. A credential-shaped run of characters goes, whatever it
# is — the second line of that defence rather than the first.
redact() {
  sed -E \
    -e 's/([Aa]uthorization)[[:space:]]*:[^|]*/\1: <redacted> /g' \
    -e 's/(Bearer|Basic)[[:space:]]+[A-Za-z0-9._~+/=-]{12,}/<redacted>/g' \
    -e 's/[A-Za-z0-9_]*(token|Token|TOKEN|secret|Secret|SECRET)[A-Za-z0-9_]*=[^[:space:]]+/<redacted>/g' \
    -e 's/((oauth|api)[_-]?key["=: ]+)[A-Za-z0-9._~+/=-]{12,}/\1<redacted>/Ig'
}

# strip_credentials: every `scheme://user:token@` in the text becomes `scheme://`.
# A token-based clone — a CI checkout, or the GitHub checkout the header tells the
# owner not to point this at — carries its credential in the origin's userinfo,
# and that URL is printed in the refusals below, into a block the register quotes.
# The rule is Check 480's, character for character: the userinfo ends at the last
# @ before the host, and the class stops at whitespace so the match cannot run
# past the URL into an @ later in the line.
strip_credentials() {
  sed -E 's#([a-zA-Z][a-zA-Z0-9+.-]*://)[^@[:space:]]*@#\1#g'
}

# last_lines FILE [N]: the last N lines of a file, on one redacted line, cut
# short. The one shape every failure row reads a command's own stderr with.
last_lines() {
  local f=$1 n=${2:-2}
  [ -f "$f" ] || {
    printf 'no output'
    return 0
  }
  tail -n "$n" "$f" | tr -d '\r' | tr '\n' ' ' | tr -s ' ' | redact | cut -c1-400
}

# ------------------------------------------------------------------ the reader
# One file reads every document this check takes — the doctor document, the
# published policy, the job list, the launch's own document, a run's row, the
# verdict and the service's own lines — so that no reading of a JSON shape is
# spelled twice and none of them is done in bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 611's reader: one file, one reading per subcommand, no credential in
any of them.

origin argv: a git remote URL. Prints the `owner/name` it names and the host it
       is on, or `-` for a URL that is not one: the repository this check reviews
       is the one the sandbox checkout's own origin names.
doctor argv: a `fugaro doctor --json` document. Prints the installation's name,
       its GCP project, its region, and the base image the installation's own
       config carries (project.base_images.base, localcfg's BaseImage(BaseKind)) —
       which is the image the implicit review-only job is deployed with (infra's
       implicitReviewVariant).
policy argv: the `fugaro project policy show --json` document, a repository and
       the repository's provider. Prints whether the PROJECT's allow_repos admits
       it; how many profiles the document DEFINES at all and how many of them
       admit the repository (a profile admits only repositories of its own
       provider, projectpolicy.Admits); the profile's provider and
       `claude_accounts` where one does; the budget mode; the node's
       `bitbucket_token` block (its `proxy` switch and whether its allow_repos
       admits the canonical repository, projectpolicy.AdmitRepo — the launcher's
       own gate, nodeBrokersBitbucket); and the accounts block: every key it
       holds and the ones whose own allow_repos admit the canonical repository.
jobname argv: a profile, a provider and an account. Prints the name
       gcp.ProjectJobName derives for the triple — `fugaro-p-<profile>-<provider>
       [-<account>]` sanitized, truncated to fit 49 characters together with a
       12-hex hash of the triple, which is the derivation ImplicitReviewJobName
       goes through. It is computed here rather than guessed at so a row can say
       the launch resolved the name the release deploys.
jobs   argv: the installation's job list and a job name. Prints the matched
       job's own fields — its container image, whether its containers name
       FUGARO_CREDENTIALS_SERVICE_URL, FUGARO_BITBUCKET_TOKEN and
       FUGARO_SECRET_ENVS at all, how many secret references of any kind it
       carries (a valueFrom.secretKeyRef in an env entry, or a volume whose
       source is a secret), and whether it carries the two Vertex names an
       agent.auth: vertex run needs — and then every project job of the
       installation (a name beginning `fugaro-p-`), which is what U4 reads.
launch argv: a `fugaro review --json` document. Prints the launch's own result —
       its status, the run's slug and id, the repository, the pull request, the
       job it resolved and the account it runs as.
run    argv: the listing's file, a run id. Prints that run's row field by field,
       `found=no` when the listing does not hold it, and the JOB its execution
       path names (`projects/<project>/locations/<region>/jobs/<job>/executions/
       <name>`, internal/backend's Execution.Name), which is the only place a
       run's job is written down.
artifact argv: the `fugaro review show RUN --json` document — the verdict the
       run wrote. Prints the repository, the pull request, the head and base it
       read, the verdict word, the reviewer's account and model, the schema
       version and how many findings it carries. No finding's own text is
       printed: it is the review's prose, not this check's reading.
audit  argv: the service's lines' file, a run id. `audit=<status>|<credential>|
       <tier>|<purpose>|<job>|<caller>` for each `credsvc: audit` line of that
       run — the status is `ok` for a call the service served, the credential and
       tier are the NAME and TIER of the credential it used (never a value), the
       job is the one the RUN REPORTED and the caller is the account the service
       VERIFIED (audit.go's AuditEvent) — and `first_forward=<credential>`, the
       credential of the EARLIEST line of purpose `forward` with status ok.
"""
import hashlib
import json
import re
import sys


def word(v, cap=80):
    """A value as one plain shape, or the empty string when there is none.

    Absent reads as empty and never as a word: a field the document does not
    carry is not a field whose value is "-", and the rows read an empty field as
    one they can say nothing about."""
    if v is None:
        return ""
    s = str(v)
    s = re.sub(r"[\x00-\x1f\x7f]", " ", s).strip()
    return s[:cap]


def text(v, cap=300):
    if v is None:
        return ""
    s = str(v)
    s = re.sub(r"[\x00-\x1f\x7f]", " ", s).strip()
    return s[:cap]


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None


def must_load(path, what):
    """The document, or a failure the check reports as a read that did not answer.

    A command that printed something that is not a document is not a command
    that answered `no`, and the caller has to be able to tell: exiting non-zero
    with a line on stderr is what turns it into "this could not be read" rather
    than into an installation that carries nothing."""
    doc = load(path)
    if doc is None:
        sys.stderr.write("%s is not a JSON document: %s\\n" % (what, path))
        sys.exit(2)
    return doc


def env(spec):
    out = {}
    if isinstance(spec, dict):
        for e in spec.get("env") or []:
            if isinstance(e, dict) and e.get("name"):
                out[e["name"]] = e
    return out


def spec_of(doc):
    """The container spec of a job.

    A Job carries spec.template.spec.template.spec (the v1 shape `gcloud run
    jobs list` answers), and the known paths are tried in order before the
    document is searched for the first containers list."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec"),):
        node = doc
        for key in path:
            node = node.get(key) if isinstance(node, dict) else None
            if node is None:
                break
        if isinstance(node, dict) and isinstance(node.get("containers"), list):
            return node
    return None


def containers_of(doc):
    spec = spec_of(doc)
    return (spec.get("containers") or []) if isinstance(spec, dict) else []


def carries(containers, name):
    """Whether any of these containers names that variable at all.

    The presence of the NAME is the reading and never its value: a token a job
    mounts is a valueSource reference and one it was handed plainly is a value,
    and both are the job holding it — a reading that only looked at one of the
    two would answer "no token" for a job that had one."""
    for c in containers:
        if name in env(c):
            return True
    return False


def secret_refs(containers):
    """How many secret references these containers carry.

    Two shapes are a secret and both are counted: an environment entry whose
    value comes from a secret (valueFrom.secretKeyRef, which is how a mounted
    credential is handed to a container) and a volume whose source is a secret
    (which is how a key file is). A review-only variant mounts nothing at all,
    so the reading is zero, and anything above it is the finding."""
    n = 0
    for c in containers:
        if not isinstance(c, dict):
            continue
        for e in c.get("env") or []:
            if not isinstance(e, dict):
                continue
            from_ = e.get("valueFrom")
            if isinstance(from_, dict) and any(
                    isinstance(from_.get(k), dict) for k in ("secretKeyRef", "secretRef")):
                n += 1
    return n


def volumes_of(doc):
    spec = spec_of(doc)
    if not isinstance(spec, dict):
        return []
    return spec.get("volumes") or []


def secret_volume_count(doc):
    n = 0
    for v in volumes_of(doc):
        if isinstance(v, dict) and isinstance(v.get("secret"), dict):
            n += 1
    return n


def inside(entry, canonical):
    """Whether a published repository entry admits a canonical repository.

    Both sides are canonical — `bitbucket/acme/web`, `github/acme/app` and the
    wildcards `*` and `bitbucket/acme/*` (projectpolicy.Canonical, MatchRepo) —
    so they are compared segment against segment, exactly as the runner does:
    the provider segment is a segment like any other, so an entry naming another
    provider admits nothing here, and `*` alone admits every repository."""
    if entry == "*":
        return True
    e, c = entry.split("/"), canonical.split("/")
    if canonical == "" or len(e) != len(c):
        return False
    for a, b in zip(e, c):
        if a == b:
            continue
        if "*" in a and re.match("^" + re.escape(a).replace(r"\*", "[^/]*") + "$", b):
            continue
        return False
    return True


def any_admits(entries, canonical):
    return any(inside(e, canonical) for e in entries if isinstance(e, str))


def job_of_execution(execution):
    """The job an execution resource path names (`…/jobs/<job>/executions/<e>`)."""
    if "/jobs/" not in execution:
        return ""
    rest = execution.split("/jobs/", 1)[1]
    return rest.split("/", 1)[0]


def sanitize(s):
    s = re.sub(r"[^a-z0-9-]+", "-", s.lower())
    while "--" in s:
        s = s.replace("--", "-")
    return s.strip("-")


def project_job_name(profile, provider, account):
    """gcp.ProjectJobName, character for character (backend/gcp/names.go).

    The readable stem is sanitized and truncated to fit the 49-character limit
    together with a 12-hex hash of the triple, and the hash is over the triple's
    own NUL-separated spelling — so this is the name ImplicitReviewJobName
    derives, computed rather than guessed at."""
    stem = "fugaro-p-%s-%s" % (profile, provider)
    if account:
        stem += "-" + account
    readable = sanitize(stem)
    suffix = "-" + hashlib.sha256(
        (profile + "\x00" + provider + "\x00" + account).encode()).hexdigest()[:12]
    limit = 49
    if len(readable) > limit - len(suffix):
        readable = readable[:limit - len(suffix)]
    readable = readable.rstrip("-")
    if not readable:
        return suffix[1:]
    return readable + suffix


cmd = sys.argv[1] if len(sys.argv) > 1 else ""
if cmd == "origin":
    url = (sys.argv[2] if len(sys.argv) > 2 else "").strip()
    host = ""
    path = ""
    m = re.match(r"^[a-z+]+://(?:[^@/]+@)?([^/:]+)(?::\d+)?/(.+)$", url)
    if m:
        host, path = m.group(1), m.group(2)
    else:
        m = re.match(r"^(?:[^@]+@)?([^:]+):(.+)$", url)
        if m:
            host, path = m.group(1), m.group(2)
    path = re.sub(r"\.git$", "", path.strip("/"))
    parts = [p for p in path.split("/") if p]
    print("host=%s" % word(host, 80))
    if len(parts) >= 2 and host:
        print("repo=%s" % word("/".join(parts[-2:]), 120))
    else:
        print("repo=")
elif cmd == "doctor":
    doc = must_load(sys.argv[2], "the doctor document")
    project = doc.get("project") or {}
    print("install=%s" % word(project.get("name"), 60))
    print("project=%s" % word(project.get("gcp_project"), 80))
    print("region=%s" % word(project.get("region"), 40))
    images = project.get("base_images") or {}
    print("base=%s" % text(images.get("base") if isinstance(images, dict) else "", 200))
elif cmd == "policy":
    repo = sys.argv[3]
    repo_provider = (sys.argv[4] if len(sys.argv) > 4 else "").strip()
    canonical = ("%s/%s" % (repo_provider, repo)) if repo_provider else repo
    doc = must_load(sys.argv[2], "the published policy")
    policy = doc.get("policy") if isinstance(doc, dict) else None
    if not isinstance(policy, dict):
        policy = {}
    project = policy.get("allow_repos") or []
    profiles = policy.get("profiles") or {}
    if not isinstance(profiles, dict):
        profiles = {}
    names = []
    for name in sorted(profiles):
        pr = profiles.get(name) or {}
        if not isinstance(pr, dict):
            continue
        # A profile admits only repositories of ITS OWN provider
        # (projectpolicy.Admits, which compares the canonical's provider against
        # the profile's), and one that writes no list of its own falls back to
        # the project's.
        if repo_provider and pr.get("provider") != repo_provider:
            continue
        if any_admits(pr.get("allow_repos") or project, canonical):
            names.append(name)
    print("proj_admits=%s" % ("yes" if any_admits(project, canonical) else "no"))
    print("profile_count=%s" % len(profiles))
    print("profiles=%s" % len(names))
    print("profile=%s" % (word(names[0], 60) if names else "-"))
    budget = policy.get("budget")
    budget = budget if isinstance(budget, dict) else {}
    print("mode=%s" % word(budget.get("mode"), 20))
    block = policy.get("bitbucket_token")
    block = block if isinstance(block, dict) else {}
    print("proxy=%s" % ("yes" if block.get("proxy") is True else "no"))
    print("token_admits=%s" % (
        "yes" if any_admits(block.get("allow_repos") or [], canonical) else "no"))
    accounts = policy.get("accounts") or {}
    if not isinstance(accounts, dict):
        accounts = {}
    admits = []
    for name in sorted(accounts):
        entry = accounts.get(name) or {}
        if isinstance(entry, dict) and any_admits(entry.get("allow_repos") or [], canonical):
            admits.append(name)
    print("account_names=%s" % text(",".join(sorted(accounts)), 300) or "-")
    print("account_admits=%s" % (text(",".join(admits), 300) or "-"))
    print("default_admitted=%s" % ("yes" if "default" in admits else "no"))
elif cmd == "jobname":
    print("name=%s" % project_job_name(sys.argv[2], sys.argv[3],
                                       sys.argv[4] if len(sys.argv) > 4 else ""))
elif cmd == "jobs":
    want = sys.argv[3]
    listed = must_load(sys.argv[2], "the installation's job list")
    if not isinstance(listed, list):
        sys.stderr.write("the installation's job list is not a list of jobs: %s\\n" % sys.argv[2])
        sys.exit(2)
    docs = [d for d in listed if isinstance(d, dict)]
    pjobs = []
    for doc in docs:
        name = ((doc.get("metadata") or {}).get("name")) or ""
        if name.startswith("fugaro-p-"):
            pjobs.append(name)
    found = None
    for doc in docs:
        if (((doc.get("metadata") or {}).get("name")) or "") == want:
            found = doc
            break
    if found is None:
        print("found=no")
    else:
        containers = containers_of(found)
        image = ""
        for c in containers:
            if isinstance(c, dict) and c.get("image"):
                image = c["image"]
                break
        print("found=yes")
        print("job=%s" % word(want, 120))
        print("image=%s" % text(image, 200))
        print("url=%s" % ("present" if carries(containers, "FUGARO_CREDENTIALS_SERVICE_URL") else "absent"))
        print("token=%s" % ("present" if carries(containers, "FUGARO_BITBUCKET_TOKEN") else "absent"))
        print("secretenvs=%s" % ("present" if carries(containers, "FUGARO_SECRET_ENVS") else "absent"))
        refs = secret_refs(containers) + secret_volume_count(found)
        print("secretrefs=%s" % refs)
        vertex = carries(containers, "CLOUD_ML_REGION") and carries(containers, "ANTHROPIC_VERTEX_PROJECT_ID")
        print("vertexenv=%s" % ("yes" if vertex else "no"))
    print("pjobs=%s" % len(pjobs))
    print("pjob_names=%s" % (text(",".join(sorted(pjobs)), 400) or "-"))
elif cmd == "launch":
    doc = must_load(sys.argv[2], "the launch document")
    if not isinstance(doc, dict):
        sys.stderr.write("the launch document is not an object: %s\\n" % sys.argv[2])
        sys.exit(2)
    print("status=%s" % word(doc.get("status"), 40))
    run = doc.get("run") or ""
    print("run=%s" % word(run, 160))
    print("run_id=%s" % word(doc.get("run_id") or (run.split("/")[-1] if run else ""), 80))
    print("repo=%s" % word(doc.get("repo"), 120))
    print("pr=%s" % word(doc.get("pr"), 20))
    print("job=%s" % word(doc.get("job"), 120))
    print("account=%s" % word(doc.get("account"), 60))
elif cmd == "run":
    want = sys.argv[3]
    listed = must_load(sys.argv[2], "the run listing")
    if isinstance(listed, dict):
        listed = next((listed[k] for k in ("runs", "items", "rows") if isinstance(listed.get(k), list)), [])
    if not isinstance(listed, list):
        sys.stderr.write("the run listing is not a list of runs: %s\\n" % sys.argv[2])
        sys.exit(2)
    for row in listed:
        if not isinstance(row, dict) or row.get("run_id") != want:
            continue
        print("found=yes")
        for key in ("repo", "recipe", "status", "terminal", "outcome", "pushed", "oauth_account"):
            if key in row:
                v = row[key]
                if isinstance(v, bool):
                    v = "yes" if v else "no"
                print("%s=%s" % (key, word(v, 60)))
        if "pr" in row:
            print("pr=%s" % word(row["pr"], 20))
        execution = row.get("execution") or ""
        print("execution=%s" % text(execution, 200))
        print("job=%s" % word(job_of_execution(execution), 120))
        halt = row.get("halt")
        if isinstance(halt, dict) and halt.get("detail"):
            print("halt_detail=%s" % text(halt.get("detail"), 240))
        if row.get("reason"):
            print("reason=%s" % text(row.get("reason"), 240))
        break
    else:
        print("found=no")
elif cmd == "artifact":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("readable=no")
    else:
        reviewer = doc.get("reviewer") or {}
        reviewer = reviewer if isinstance(reviewer, dict) else {}
        findings = doc.get("findings")
        n = len(findings) if isinstance(findings, list) else 0
        print("readable=yes")
        print("repository=%s" % word(doc.get("repository"), 120))
        print("pr=%s" % word(doc.get("pr"), 20))
        print("head=%s" % word(doc.get("head_sha"), 40))
        print("base=%s" % word(doc.get("base"), 60))
        print("verdict=%s" % word(doc.get("verdict"), 60))
        print("reviewer_account=%s" % word(reviewer.get("account"), 60))
        print("reviewer_model=%s" % word(reviewer.get("model"), 80))
        print("schema=%s" % word(doc.get("schema_version"), 20))
        print("findings=%s" % n)
        print("created=%s" % word(doc.get("created_at"), 40))
elif cmd == "audit":
    want = sys.argv[3]
    lines = must_load(sys.argv[2], "the service's own lines")
    if not isinstance(lines, list):
        sys.stderr.write("the service's own lines are not a list: %s\\n" % sys.argv[2])
        sys.exit(2)
    forwards = []
    for i, entry in enumerate(lines):
        if not isinstance(entry, dict):
            continue
        p = entry.get("jsonPayload") or {}
        if p.get("run") != want:
            continue
        msg = entry.get("textPayload") or p.get("msg") or p.get("message") or ""
        if msg != "credsvc: audit":
            continue
        status = p.get("status")
        credential = p.get("credential")
        purpose = p.get("purpose")
        print("audit=%s|%s|%s|%s|%s|%s" % (
            word(status, 40),
            word(credential, 80),
            word(p.get("tier"), 8),
            word(purpose, 40),
            word(p.get("job"), 120),
            word(p.get("caller"), 200)))
        if purpose == "forward" and status == "ok" and credential:
            # The earliest line by its own timestamp, and by the document's
            # order where a line carries none: the run's FIRST model call is
            # what U3's model half reads, and a later switch must not be read as
            # it.
            forwards.append((str(entry.get("timestamp") or ""), i, word(credential, 80)))
    if forwards:
        forwards.sort(key=lambda t: (t[0] == "", t[0], t[1]))
        print("first_forward=%s" % forwards[0][2])
else:
    sys.stderr.write("reader.py: unknown query %s\n" % cmd)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/reader.py"
}

reader() { python3 "$SCRATCH/reader.py" "$@"; }

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check611.sh --sandbox <a sandbox Bitbucket repository checkout> --pr <N> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a command's output to the terminal"
}

# guard_agent_session: the check is the owner's and it launches a billable review
# of the installation's agent. Refusing here rather than letting the launch refuse
# one at a time is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 611 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_belong: this check reviews a real repository's pull request and spends the
# installation's budget, so every value it was given is read for the installation
# it must never touch before anything else runs. The values read from the checkout
# are guarded again where they are read.
guard_belong() {
  local all="${ACCOUNT:-}${SANDBOX:-}${INSTALL:-}${PROJECT:-}${REGION:-}${REPO:-}"
  case "$all" in
  *belong*) stop "a value this check was given or read names belong: this check is never run there" ;;
  esac
  fact belong PASS "nothing this check reads or was given names belong"
}

version_row() {
  local v=""
  if [ -x "$FUGARO_BIN" ] || command -v "$FUGARO_BIN" >/dev/null 2>&1; then
    v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fi
  fact fugaro-version PASS "${v:-fugaro version printed nothing} (the build this check drives: the review it launches is the one this release makes)"
}

# ------------------------------------------------------------------ the steps
# step_sandbox: the premise. The repository, the installation, the project and the
# region are all read from the checkout itself, so the check measures the
# installation you work in rather than one it guessed.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a sandbox Bitbucket repository)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the pull request this check reviews is one of the repository the checkout belongs to"
  fi
  local url="" rc=0
  url=$(git -C "$SANDBOX" remote get-url origin 2>"$SCRATCH/origin.err") || rc=$?
  if [ "$rc" -ne 0 ] || [ -z "$url" ]; then
    stop "the checkout at $SANDBOX names no origin this check could read (git exited ${rc}): $(last_lines "$SCRATCH/origin.err"). The repository this check reviews is the one the checkout's own origin names, and a checkout without one is a repository this check cannot name"
  fi
  # The credential goes before the URL is matched, recorded or printed: a
  # credentialled origin reaches the refusals below, the scratch file and the
  # reader alike, and the reader's patterns cannot cross a `/` inside a userinfo.
  url=$(printf '%s\n' "$url" | strip_credentials)
  printf '%s\n' "$url" >"$SCRATCH/origin.txt"
  local got host
  got=$(reader origin "$url" 2>"$SCRATCH/origin.reader.err") || stop "the origin of $SANDBOX could not be read: $(last_lines "$SCRATCH/origin.reader.err")"
  host=$(printf '%s\n' "$got" | sed -n 's/^host=//p')
  REPO=$(printf '%s\n' "$got" | sed -n 's/^repo=//p')
  if [ -z "$REPO" ]; then
    stop "the origin of $SANDBOX is '$url', which names no repository: the pull request this check reviews is named by the repository, and a URL this check cannot read is one it will not guess at"
  fi
  if [ "$host" != bitbucket.org ]; then
    stop "the origin of $SANDBOX is $url, which is not a Bitbucket repository (its host is '${host:-none}'): this check is the BITBUCKET half — the review runs on a job that mounts no Bitbucket token and reaches bitbucket.org through the credentials service — and a GitHub or GitLab checkout has nothing here for it to measure"
  fi
  # The repository's provider, in the policy's own canonical spelling
  # (projectpolicy.Canonical: "<provider>/<owner>/<name>").
  REPO_PROVIDER=bitbucket
  # The workflow the checkout declares, as the line it is written on: it is the
  # premise U1 reads (a repository that carries an image of its own is the case a
  # base-image review path is a question about), and it is stated rather than
  # judged.
  CHECKOUT_KIND=$(sed -n 's/^[[:space:]]*checkout:[[:space:]]*//p' "$SANDBOX/fugaro.yaml" | head -n 1 | tr -d '"' | tr -d "'")
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$FUGARO_BIN" doctor --json
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the installation the sandbox belongs to, and every row below is about it. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    SANDBOX_STOP=yes
    return 0
  fi
  local got2
  if ! got2=$(reader doctor "$SCRATCH/doctor.out" 2>"$SCRATCH/doctor.reader.err"); then
    unmeasured sandbox "the doctor document in $SCRATCH/doctor.out could not be read: $(last_lines "$SCRATCH/doctor.reader.err")"
    SANDBOX_STOP=yes
    return 0
  fi
  INSTALL=$(printf '%s\n' "$got2" | sed -n 's/^install=//p')
  PROJECT=$(printf '%s\n' "$got2" | sed -n 's/^project=//p')
  REGION=$(printf '%s\n' "$got2" | sed -n 's/^region=//p')
  BASE_IMAGE=$(printf '%s\n' "$got2" | sed -n 's/^base=//p')
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox belongs to, and every row below is read in it. What it printed is in $SCRATCH/doctor.out"
    SANDBOX_STOP=yes
    return 0
  fi
  case "${INSTALL}${PROJECT}${REGION}${REPO}" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${INSTALL:-<no name>} (${PROJECT}, region ${REGION}) and ${REPO}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout of the BITBUCKET repository ${REPO} (its own \`checkout:\` line reads '${CHECKOUT_KIND:-<none>}'), and it belongs to the ${INSTALL:-<no name>} installation (${PROJECT}, region ${REGION}): the review this check launches is that repository's, read in that installation, and the pull request it reviews is #${PR}"
}

# step_policy: the premise the launch itself is held to. A repository a profile
# admits has a shared project job, and a review of it runs on that profile's
# review-only variant — not on the installation's implicit job, which is what this
# check is about. So a profile that admits the repository is a FAIL and not a
# finding: the sandbox this check is run on is a profile-less one.
step_policy() {
  if [ -n "$SANDBOX_STOP" ] || [ -z "$INSTALL" ]; then
    unmeasured policy "there is no installation to read the published policy of: the row above says why"
    POLICY_STOP=yes
    return 0
  fi
  say "reading the published policy: fugaro project policy show --project ${INSTALL} --json"
  run_bounded "$CLI_TIMEOUT" policy "$FUGARO_BIN" project policy show --project "$INSTALL" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured policy "fugaro project policy show --project ${INSTALL} --json did not answer (exit ${BOUNDED_STATUS}): whether the repository is brokered and whether a profile admits it is what the launch resolves against, and nothing is launched on a policy nobody read. Its output is in $SCRATCH/policy.out and $SCRATCH/policy.err"
    POLICY_STOP=yes
    return 0
  fi
  if ! reader policy "$SCRATCH/policy.out" "$REPO" "$REPO_PROVIDER" >"$SCRATCH/policy.txt" 2>"$SCRATCH/policy.reader.err"; then
    unmeasured policy "fugaro project policy show --json printed a document this check could not read: what it printed is in $SCRATCH/policy.out"
    POLICY_STOP=yes
    return 0
  fi
  PROFILE_COUNT=$(field "$SCRATCH/policy.txt" profile_count)
  DEFAULT_ADMITTED=$(field "$SCRATCH/policy.txt" default_admitted)
  ACCOUNT_ADMITS=$(field "$SCRATCH/policy.txt" account_admits)
  TOKEN_PROXY=$(field "$SCRATCH/policy.txt" proxy)
  TOKEN_ADMITS=$(field "$SCRATCH/policy.txt" token_admits)
  MODE=$(field "$SCRATCH/policy.txt" mode)
  if [ "$(field "$SCRATCH/policy.txt" proj_admits)" != yes ]; then
    unmeasured policy "the published policy of ${INSTALL} does not admit ${REPO} to the PROJECT (its own allow_repos does not cover it), so no profile may admit it either and a launch would be refused before any execution exists: add it with 'fugaro project repos add <the repository's canonical name>' and publish ('fugaro project policy publish'), then run this check again"
    POLICY_STOP=yes
    return 0
  fi
  local n
  n=$(field "$SCRATCH/policy.txt" profiles)
  if [ "${n:-0}" -gt 0 ]; then
    fact policy FAIL "${n} profile(s) of ${INSTALL}'s published policy admit ${REPO} ('$(field "$SCRATCH/policy.txt" profile)' among them): a repository a profile admits has a shared project job, and a review of it resolves that profile's review-only variant — not the installation's implicit review-only job, which is what this check is about (RJ8's launcher resolves the implicit job only where no shared coding job was resolved). Name a sandbox Bitbucket repository NO profile admits — the plan's premise, the sandbox repository with no profiles block — and run this check again"
    POLICY_STOP=yes
    return 0
  fi
  if [ "$TOKEN_PROXY" != yes ] || [ "$TOKEN_ADMITS" != yes ]; then
    fact policy FAIL "${REPO} is on no profile (which is this check's premise), and the published node does not broker its Bitbucket access: its bitbucket_token block reads proxy=${TOKEN_PROXY:-none} and allow_repos ${TOKEN_ADMITS:-no} for this repository. The implicit review-only job mounts no Bitbucket token at all, so the token it clones with can only come from the credentials service, and the service forwards it for a repository only where the node admits it with proxy: true — a review launched there would open the repository with no token and stop at its first git call, inside the container. Set proxy: true on the project_secrets.bitbucket-token entry covering ${REPO}, run 'fugaro project policy publish', then 'fugaro update --repo ${REPO}', and run this check again"
    POLICY_STOP=yes
    return 0
  fi
  case "$MODE" in
  - | '')
    unmeasured policy "${REPO} is on no profile and the policy document carries no budget mode (it reads '${MODE:-<none>}'), so whether the gateway exists for a review of this repository is NOT measured: the gateway exists in observe and enforce (D2071, GatewayOn), and a mode the document does not carry is neither — the gateway is what reaches the credentials service. Publish the project policy so it carries one ('fugaro project policy publish', observe or enforce) and run this check again"
    POLICY_STOP=yes
    return 0
    ;;
  esac
  fact policy PASS "${REPO} is admitted to the project and to NO profile of ${INSTALL}'s published policy (the document defines ${PROFILE_COUNT} profile(s) and none of them admits it), the published budget mode is '${MODE}', and the node's bitbucket_token block admits it with proxy: true — so the credentials service forwards the token for this repository, which is the only way the job a review runs on can reach bitbucket.org. The launch below resolves the implicit review-only job rather than a profile's variant"
}

# step_launch: the ONE review. The launcher resolves the job, the account and the
# run id itself, and its own document is where all three are read — a name this
# check recomputed could not disagree with the one the run really landed in. What
# the document is held to is the name the release derives for the implicit
# review-only job of provider bitbucket, and the default account, which is the
# only account that job serves.
step_launch() {
  if [ -n "$POLICY_STOP" ]; then
    unmeasured launch "nothing is launched without the repository's own brokering and a policy that admits it: the row above says why"
    return 0
  fi
  LAUNCH_EPOCH=$(date -u +%s)
  say "launching the review of ${REPO}#${PR} in ${INSTALL}: fugaro review --pr ${PR} --json (from ${SANDBOX})"
  run_bounded "$CLI_TIMEOUT" review "$FUGARO_BIN" review --pr "$PR" --project "$INSTALL" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured launch "fugaro review --pr ${PR} did not answer inside ${CLI_TIMEOUT}s and was ended: it may or may not have launched the review, so nothing about it is read. Read the listing yourself ('fugaro runs ls --pr ${PR} --repo ${REPO} --project ${INSTALL}') before launching another one"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured launch "fugaro review --pr ${PR} for ${REPO} exited ${BOUNDED_STATUS} and launched nothing: $(last_lines "$SCRATCH/review.err"). A launch refused before Cloud Run — the credentials service, the class the service admits, a named account, a repository the policy does not broker, a pull request whose run is still going — is the launcher's own sentence, and it is the reading this row carries"
    return 0
  fi
  local got
  if ! got=$(reader launch "$SCRATCH/review.out" 2>"$SCRATCH/review.reader.err"); then
    unmeasured launch "fugaro review --pr ${PR} --json printed a document this check could not read: what it printed is in $SCRATCH/review.out"
    return 0
  fi
  LAUNCH_STATUS=$(printf '%s\n' "$got" | sed -n 's/^status=//p')
  LAUNCH_RUN=$(printf '%s\n' "$got" | sed -n 's/^run=//p')
  LAUNCH_RID=$(printf '%s\n' "$got" | sed -n 's/^run_id=//p')
  LAUNCH_JOB=$(printf '%s\n' "$got" | sed -n 's/^job=//p')
  LAUNCH_ACCOUNT=$(printf '%s\n' "$got" | sed -n 's/^account=//p')
  LAUNCH_PR=$(printf '%s\n' "$got" | sed -n 's/^pr=//p')
  LAUNCH_REPO=$(printf '%s\n' "$got" | sed -n 's/^repo=//p')
  case "$LAUNCH_STATUS" in
  launched | already-launched) ;;
  *)
    unmeasured launch "the launch document reads status='${LAUNCH_STATUS:-<none>}' and its run is '${LAUNCH_RUN:-<none>}': this check reads a review it launched, and a document it cannot read as one is nothing about that review. What it printed is in $SCRATCH/review.out"
    return 0
    ;;
  esac
  if [ -z "$LAUNCH_RID" ]; then
    unmeasured launch "the launch document names no run id ('${LAUNCH_RUN:-<none>}'): the run's own row, its job, its verdict and the service's lines for it are all read by the id the launcher minted, and a document without one leaves every row below unmeasured. What it printed is in $SCRATCH/review.out"
    return 0
  fi
  LAUNCHED=yes
  local expected
  expected=$(reader jobname standard bitbucket default-review | sed -n 's/^name=//p')
  if [ "$LAUNCH_JOB" != "$expected" ]; then
    fact launch FAIL "the launch resolved the job '${LAUNCH_JOB:-<none>}' for the review of ${REPO}#${PR}, and the installation's IMPLICIT review-only job of provider bitbucket is named '${expected:-<none>}' (the name this release derives for the default profile, provider bitbucket and the default account's review variant — ImplicitReviewJobName over DefaultProfileName, ReviewVariantAccount and ReviewVariantSuffix — computed here from the same rule rather than read back). A review that resolved another job ran on a per-repository job or on a profile's variant: this check is about the implicit job, and every row below would be about something else. Deploy it ('fugaro update') and check that no profile of the published policy admits ${REPO}, then run this check again"
    return 0
  fi
  if [ -n "$LAUNCH_PR" ] && [ "$LAUNCH_PR" != "$PR" ]; then
    fact launch FAIL "the launch document is about ${LAUNCH_REPO:-${REPO}}#${LAUNCH_PR}, and this check asked for a review of ${REPO}#${PR}: every row below is read by the run id this document names, so a document whose own pull request is another one is a launch this check cannot say it asked for — the review may be a review of the other pull request, or the launch may have echoed it wrongly. The rows below are read all the same, because a review that exists is worth reading; read 'fugaro runs show ${LAUNCH_RID} --repo ${REPO}' yourself and run this check again with the pull request you mean"
    return 0
  fi
  if [ "$LAUNCH_ACCOUNT" != default ]; then
    fact launch FAIL "the launch resolved the account '${LAUNCH_ACCOUNT:-<none>}' for the review of ${REPO}#${PR}, and the implicit review-only job of provider bitbucket serves ONE account: the default one (its account segment is fixed where it is deployed, and the credentials service pairs the caller it verifies with the account the job was deployed for). Launch the review without --token (this check never passes one) and read the launcher's own sentence in $SCRATCH/review.err"
    return 0
  fi
  fact launch PASS "fugaro review --pr ${PR} answered '${LAUNCH_STATUS}' and resolved the run ${LAUNCH_RUN} on the job '${LAUNCH_JOB}' as the account '${LAUNCH_ACCOUNT}': that is the installation's IMPLICIT review-only job of provider bitbucket — the name this release derives for (standard, bitbucket, default-review) — and the default account, which is the only account it serves. It pushes nothing and opens nothing: a review reads a head and writes a verdict"
}

# step_run: the run's own row, polled until it is terminal. Three things are one
# fact here: it ended `succeeded`, its outcome is `reviewed` (the word the runner
# writes for a review that finished), and it pushed nothing — a review never
# touches the repository, and a review row that says it pushed is a run that was
# not one.
step_run() {
  if [ "$LAUNCHED" != yes ]; then
    unmeasured run "no review was launched, so there is no row to read: the row above says why"
    return 0
  fi
  say "waiting for the review run (${LAUNCH_RID}) to reach a final status: fugaro runs ls --pr ${PR} --json"
  local waited=0 row="" listing_rc=0
  while :; do
    row=""
    listing_rc=0
    "$FUGARO_BIN" runs ls --repo "$REPO" --pr "$PR" --project "$INSTALL" --no-cache --json >"$SCRATCH/rows.json" 2>>"$SCRATCH/rows.err" || listing_rc=$?
    if [ "$listing_rc" -eq 0 ]; then
      row=$(reader run "$SCRATCH/rows.json" "$LAUNCH_RID" 2>>"$SCRATCH/rows.err" || true)
    fi
    if [ "$(printf '%s\n' "$row" | sed -n 's/^found=//p')" = yes ] &&
      [ "$(printf '%s\n' "$row" | sed -n 's/^terminal=//p')" = yes ]; then break; fi
    if [ "$waited" -ge "$RUN_SECONDS" ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  printf '%s\n' "$row" >"$SCRATCH/row.txt"
  if [ "$(printf '%s\n' "$row" | sed -n 's/^found=//p')" != yes ]; then
    unmeasured run "the review run (${LAUNCH_RID}) does not appear in 'fugaro runs ls --pr ${PR} --repo ${REPO}' after ${waited}s (the last read exited ${listing_rc}): it was launched but its row is not in the listing, so nothing about it is read. What the read answered: $(last_lines "$SCRATCH/rows.err"). Read 'fugaro runs show ${LAUNCH_RID} --repo ${REPO}' yourself"
    return 0
  fi
  if [ "$(printf '%s\n' "$row" | sed -n 's/^terminal=//p')" != yes ]; then
    unmeasured run "the review run (${LAUNCH_RID}) had not reached a final status after ${waited}s: status=$(printf '%s\n' "$row" | sed -n 's/^status=//p'). A row still in flight is not an answer about the review — raise CHECK611_RUN_SECONDS, or read that run's row yourself ('fugaro runs show ${LAUNCH_RID} --repo ${REPO}')"
    return 0
  fi
  # The row was read in the shell and this is where its arrival is recorded: the
  # rows below are rows about that row, and one that was never read leaves them
  # unmeasured (RUN_ROW).
  RUN_ROW=yes
  local status outcome pushed pr recipe
  status=$(field "$SCRATCH/row.txt" status)
  outcome=$(field "$SCRATCH/row.txt" outcome)
  pushed=$(field "$SCRATCH/row.txt" pushed)
  pr=$(field "$SCRATCH/row.txt" pr)
  recipe=$(field "$SCRATCH/row.txt" recipe)
  if [ "$status" != succeeded ] || [ "$outcome" != reviewed ] || [ "$pushed" != no ] || [ "$pr" != "$PR" ]; then
    fact run FAIL "the review run (${LAUNCH_RID}) of ${REPO}#${PR} did not end as a review that finished: status=${status:-unread}, outcome=${outcome:-none} (a finished review reads 'reviewed'), pushed=${pushed:-none} (a review pushes nothing), pr=${pr:-none}. A review launched onto the implicit review-only job holds no credential of its own — its git traffic and its model calls both go through the credentials service — so a run that did not finish is the service, the account the policy admits or the job's own environment not serving it, and the rows below read the service's own lines for the calls it DID serve. Read 'fugaro runs show ${LAUNCH_RID} --repo ${REPO}' before anything else$(printf '%s' "$(field "$SCRATCH/row.txt" halt_detail)" | sed 's/^/, halt: /')$(printf '%s' "$(field "$SCRATCH/row.txt" reason)" | sed 's/^/, reason: /')"
    return 0
  fi
  fact run PASS "the review run (${LAUNCH_RID}) of ${REPO}#${PR} ended '${status}' with outcome '${outcome}', pushed nothing (pushed=no) and names the pull request #${pr} it read — recipe='${recipe:-<none>}', which is the catalog's review-only recipe unless a project or repository recipe shadows it. A review that finished is one whose clone, whose model calls and whose verdict all went through: that is the positive half of this check"
}

# step_job: the job the review ran on, read back from the platform. RJ8's whole
# point is that this job mounts NOTHING — no Bitbucket token above all, because a
# Bitbucket review's clone can only be the credentials service's git proxy. Both
# halves are settled here, before the verdict and the service's lines are read,
# because a job that holds a token would make every row below a row about
# something else.
step_job() {
  if [ "$LAUNCHED" != yes ] || [ -z "$LAUNCH_JOB" ]; then
    unmeasured job "no launch named a job to read back: the row above says why"
    return 0
  fi
  say "reading the job the review ran on: gcloud run jobs list --project=${PROJECT} --region=${REGION}"
  run_bounded "$CLI_TIMEOUT" jobs gcloud run jobs list --project="$PROJECT" --region="$REGION" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured job "gcloud run jobs list did not answer (exit ${BOUNDED_STATUS}): the job '${LAUNCH_JOB}' is what the review ran on and its mounts are what this row reads, and a read that did not answer is not a job that mounts nothing. Its output is in $SCRATCH/jobs.out and $SCRATCH/jobs.err"
    return 0
  fi
  if ! reader jobs "$SCRATCH/jobs.out" "$LAUNCH_JOB" >"$SCRATCH/jobs.txt" 2>"$SCRATCH/jobs.reader.err"; then
    unmeasured job "gcloud run jobs list printed a document this check could not read: what it printed is in $SCRATCH/jobs.out"
    return 0
  fi
  PJOB_COUNT=$(field "$SCRATCH/jobs.txt" pjobs)
  PJOB_NAMES=$(field "$SCRATCH/jobs.txt" pjob_names)
  if [ "$(field "$SCRATCH/jobs.txt" found)" != yes ]; then
    fact job FAIL "the job '${LAUNCH_JOB}' the review ran on is not in the installation's job list ('gcloud run jobs list --project=${PROJECT} --region=${REGION}'): the project jobs it did list are ${PJOB_NAMES:-<none>}. A review ran in that job, so a job that is not there is a reading of the wrong project or region rather than a job that mounts nothing — read the job out of the row's own execution ('fugaro runs show ${LAUNCH_RID}') and list that installation's jobs yourself"
    return 0
  fi
  JOB_IMAGE=$(field "$SCRATCH/jobs.txt" image)
  JOB_URL=$(field "$SCRATCH/jobs.txt" url)
  JOB_TOKEN=$(field "$SCRATCH/jobs.txt" token)
  JOB_SECRETENVS=$(field "$SCRATCH/jobs.txt" secretenvs)
  JOB_SECRETREFS=$(field "$SCRATCH/jobs.txt" secretrefs)
  JOB_VERTEX=$(field "$SCRATCH/jobs.txt" vertexenv)
  if [ "$JOB_URL" != present ]; then
    fact job FAIL "the job '${LAUNCH_JOB}' the review ran on carries no FUGARO_CREDENTIALS_SERVICE_URL: a review-only job mounts no credential at all (RJ8, CSR7), so the service's address is the ONLY way its clone and its model calls reach anything — a job that names no service is a review that could not have run, whatever its row says. Re-apply with 'fugaro update' so the variant carries the installation's address, then run this check again"
    return 0
  fi
  if [ "$JOB_TOKEN" = present ]; then
    fact job FAIL "the job '${LAUNCH_JOB}' the review ran on names FUGARO_BITBUCKET_TOKEN: a Bitbucket review runs on a job that mounts NO Bitbucket token (D2302, RJ8) — its clone of the pull request's head is the credentials service's git proxy, and nothing else — so a job that carries one is a job whose run's git traffic would not be the service's. Re-apply with 'fugaro update' so the review-only variant is rewritten without it, then run this check again"
    return 0
  fi
  if [ "$JOB_SECRETENVS" = present ] || [ "${JOB_SECRETREFS:-0}" -gt 0 ] 2>/dev/null; then
    fact job FAIL "the job '${LAUNCH_JOB}' the review ran on carries a SECRET (FUGARO_SECRET_ENVS is named, or ${JOB_SECRETREFS:-0} secret reference(s) — a valueFrom.secretKeyRef, or a volume whose source is a secret): the review-only variant mounts nothing at all, by construction (RJ8, CSR7: no App key, no Claude token, no signing key, no Bitbucket token), so the only credentials its run can have are the ones the credentials service hands it per call. Re-apply with 'fugaro update' so the variant is deployed without them, then run this check again"
    return 0
  fi
  fact job PASS "the job '${LAUNCH_JOB}' the review ran on carries FUGARO_CREDENTIALS_SERVICE_URL and no credential of any kind: NO FUGARO_BITBUCKET_TOKEN, no FUGARO_SECRET_ENVS and no secret reference at all (${JOB_SECRETREFS:-0}) — the review-only variant mounts nothing (RJ8, CSR7), so its clone of the pull request's head can only have gone through the service's git proxy, and the service's own lines for the run are read below"
}

# step_review: the verdict, read back from the run's own record — the artifact the
# review job writes when it finishes. It is the only place a review's own reading
# is written down, and a review run that ended succeeded with no artifact is a
# review that recorded nothing.
step_review() {
  if [ "$RUN_ROW" != yes ]; then
    unmeasured review "no terminal row of this check's own review run was read, so there is no verdict to read back: the row above says why"
    return 0
  fi
  say "reading the verdict back: fugaro review show ${LAUNCH_RUN} --project ${INSTALL} --json"
  # The run's slug the launcher printed, which is what `review show` takes; the
  # bare id is the fallback when the document carried the id and no slug.
  local ref=$LAUNCH_RUN
  if [ -z "$ref" ]; then ref=$LAUNCH_RID; fi
  run_bounded "$CLI_TIMEOUT" artifact "$FUGARO_BIN" review show "$ref" --project "$INSTALL" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured review "fugaro review show ${ref} did not answer inside ${CLI_TIMEOUT}s: the verdict is read out of the run's own record, and a read that did not answer is not a review that recorded nothing"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured review "fugaro review show ${ref} exited ${BOUNDED_STATUS}: $(last_lines "$SCRATCH/artifact.err"). A review's artifact is written when the job finishes, and the command's own sentence says which of the cases it is (a run that is not a review, a review whose signature could not be made — which writes nothing — or a record that could not be read). Read 'fugaro diagnose ${ref}' and the run's own row ('fugaro runs show ${LAUNCH_RID} --repo ${REPO}') yourself: this check will not read a missing artifact as a verdict"
    return 0
  fi
  if ! reader artifact "$SCRATCH/artifact.out" >"$SCRATCH/artifact.txt" 2>"$SCRATCH/artifact.reader.err"; then
    unmeasured review "fugaro review show --json printed a document this check could not read: what it printed is in $SCRATCH/artifact.out"
    return 0
  fi
  if [ "$(field "$SCRATCH/artifact.txt" readable)" != yes ]; then
    unmeasured review "fugaro review show --json exited 0 and printed a document that is not a verdict object (it is in $SCRATCH/artifact.out): the artifact's own claims are what the row below quotes, and a document without them is nothing to read"
    return 0
  fi
  local repository pr head verdict account model schema findings created
  repository=$(field "$SCRATCH/artifact.txt" repository)
  pr=$(field "$SCRATCH/artifact.txt" pr)
  head=$(field "$SCRATCH/artifact.txt" head)
  verdict=$(field "$SCRATCH/artifact.txt" verdict)
  account=$(field "$SCRATCH/artifact.txt" reviewer_account)
  model=$(field "$SCRATCH/artifact.txt" reviewer_model)
  schema=$(field "$SCRATCH/artifact.txt" schema)
  findings=$(field "$SCRATCH/artifact.txt" findings)
  created=$(field "$SCRATCH/artifact.txt" created)
  if [ "$repository" != "$REPO" ] || [ "$pr" != "$PR" ]; then
    fact review FAIL "the verdict read back from the review run (${LAUNCH_RID}) is about ${repository:-<none>}#${pr:-none}, and this check reviewed ${REPO}#${PR}: a verdict is acted on against the pull request and the head it names, so one about another repository or another pull request is not this review's. Read 'fugaro review show ${LAUNCH_RUN} --project ${INSTALL}' yourself"
    return 0
  fi
  fact review PASS "the review run (${LAUNCH_RID}) recorded its verdict: the artifact reads repository=${repository}, pr=#${pr}, head=${head:-<none>}, verdict='${verdict:-<none>}', reviewer account='${account:-<none>}' model='${model:-<none>}', schema_version=${schema:-<none>}, ${findings:-0} finding(s), written ${created:-<none>}. That is the reading this check exists for on the Bitbucket side: a review launched by \`fugaro review\` reaches a verdict and leaves it where \`fugaro review show\` reads it"
}

# log_freshness: how far back the service's lines are read. The run's FIRST model
# call is the oldest forward line the service wrote for it, so the window has to
# reach back to the run's own start: CHECK611_LOG_AGE is the floor and this widens
# it by the time since the launch, which is what a run that outlasted the knob —
# CHECK611_RUN_SECONDS waits 2400s for one — would otherwise lose the head of.
log_freshness() {
  local since=$LOG_AGE mins
  if [ -n "$LAUNCH_EPOCH" ] && [ -n "$LOG_AGE_MINUTES" ]; then
    mins=$(( ($(date -u +%s) - LAUNCH_EPOCH) / 60 + 5 ))
    [ "$mins" -gt "$LOG_AGE_MINUTES" ] && since="${mins}m"
  fi
  printf '%s' "$since"
}

# audit_read RID OUT: the run's service lines, retried while Cloud Logging catches
# up — ingestion lags the request by seconds. It returns 0 only when the last read
# ANSWERED: `gcloud logging read` exiting non-zero (no permission on the log, an
# expired session, a network answer) is NOT the service having written no line,
# and a row that took a failed read for silence would FAIL this check on nothing.
# audit_unread_write: the one sentence both rows carry when the service's lines
# for this run could not be read. Three reads can produce it — gcloud refusing,
# gcloud answering with something the reader cannot read, and the wait running out
# — and all three are the same thing to the two rows: a read that did not answer.
audit_unread_write() { # OUT RID WHY
  printf '%s' "the service's own lines for the run ($2) could not be read: $3. Every row read off those lines is read off what the service wrote for that run, and a read that did not answer is NOT the service having written no line. Read the log yourself ('gcloud logging read' with that run's id, and 'gcloud logging read --help' for the filter above) and run this check again" >"$1.unread"
}

audit_read() {
  local rid=$1 out=$2 waited=0 filter rc=0
  filter='resource.type="cloud_run_revision" resource.labels.service_name="fugaro-credentials" jsonPayload.run="'"$rid"'"'
  while :; do
    rc=0
    # --order=asc, because gcloud answers the NEWEST --limit lines by default and
    # a review run writes more than that: the run's FIRST model call is the
    # OLDEST forward line, and read newest-first it is not in the answer at all.
    # The git line the row above counts is the run's earliest one too (the clone
    # of the head), so the old end is the end both rows are read from.
    gcloud logging read "$filter" --project="$PROJECT" --freshness="$(log_freshness)" \
      --limit="$LOG_LIMIT" --order=asc --format=json >"$out" 2>"$SCRATCH/audit.err" || rc=$?
    local rows_rc=0
    reader audit "$out" "$rid" >"$out.rows" 2>"$SCRATCH/audit.reader.err" || rows_rc=$?
    if [ "$rc" -eq 0 ] && [ "$rows_rc" -eq 0 ] && grep -qE '^audit=' "$out.rows"; then return 0; fi
    # gcloud's own refusal is reported before the reader's: when it exited non-zero
    # the document the reader was handed is whatever it left behind, and the
    # reader's complaint about that says nothing about why the read failed. A
    # non-zero exit is not a read still in flight either, so it is not retried.
    if [ "$rc" -ne 0 ]; then
      audit_unread_write "$out" "$rid" "'gcloud logging read' exited ${rc} and printed: $(last_lines "$SCRATCH/audit.err")"
      return 1
    fi
    if [ "$rows_rc" -ne 0 ]; then
      # A document the reader cannot read is not a read that is still in flight:
      # retrying it would only wait LOG_WAIT_SECONDS to report the same thing.
      audit_unread_write "$out" "$rid" "$(last_lines "$SCRATCH/audit.reader.err")"
      return 1
    fi
    if [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then
      return 0
    fi
    sleep "$LOG_POLL_SECONDS"
    waited=$((waited + LOG_POLL_SECONDS))
  done
}

audit_unread() { cat "$1.unread" 2>/dev/null || true; }

# step_served: the service's own lines for the review run, and the two rows they
# carry. The GIT row is the clone and the fetch of the pull request's head — the
# route a job that mounts no token has to take; the MODEL row is the run's first
# model call, whose credential name is read against what the node admits for this
# repository (U3).
step_served() {
  if [ "$RUN_ROW" != yes ]; then
    unmeasured git "no terminal row of this check's own review run was read, so the service's lines for it are not read: the row above says why"
    unmeasured model "no terminal row of this check's own review run was read, so the service's lines for it are not read: the row above says why"
    return 0
  fi
  local lines=$SCRATCH/audit.json got
  say "reading the service's own lines for ${LAUNCH_RID}: gcloud logging read (credsvc: audit)"
  if ! audit_read "$LAUNCH_RID" "$lines"; then
    unmeasured git "$(audit_unread "$lines")"
    unmeasured model "$(audit_unread "$lines")"
    return 0
  fi
  # audit_read left the reader's own rows beside the document it read: the rows
  # are read once, and the read that produced them is the one the two rows below
  # are about.
  got=$(cat "$lines.rows" 2>/dev/null || true)
  local purposes triples
  GIT_LINES=$(printf '%s\n' "$got" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|git|.*/\1/p' | grep -c . || true)
  GIT_CREDS=$(printf '%s\n' "$got" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|git|.*/\1/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
  FIRST_FORWARD=$(printf '%s\n' "$got" | sed -n 's/^first_forward=//p')
  purposes=$(printf '%s\n' "$got" | sed -n 's/^audit=[^|]*|[^|]*|[^|]*|\([^|]*\)|.*/\1/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
  triples=$(printf '%s\n' "$got" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|\([^|]*\)|.*/\1|\2|\3/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
  if [ "${GIT_LINES:-0}" -lt 1 ]; then
    fact git FAIL "the service wrote NO served git line (purpose 'git', status ok) for the review run (${LAUNCH_RID}) of ${REPO}, while that run finished and its verdict was read back: the job it ran on mounts no Bitbucket token at all (the job row above), so its clone of the pull request's head reaches bitbucket.org through this service or not at all — a run whose git traffic was served by something else is a job that still holds a token, or a route that did not go through the service. The purposes the service's own lines for this run carry: ${purposes:-<none>}. Check the job's own environment ('gcloud run jobs describe ${LAUNCH_JOB}') and the service's lines for this run"
    return 0
  fi
  fact git PASS "${GIT_LINES} served git line(s) of the credentials service's own audit for the review run (${LAUNCH_RID}) — purpose 'git', status ok — naming the credential(s) ${GIT_CREDS:-<none>}, which is the bundle's Bitbucket token and never one of the cascade's accounts (a git line names a credential and no tier). That is the clone and the fetch of the pull request's head arriving at the service: the job holds no token, so its git traffic could not have gone anywhere else. The purpose does not tell a fetch from a push (credsvc's PurposeGit is one request of a run's git traffic) — and a review pushes nothing"
  local vertex=$JOB_VERTEX
  if [ -z "$FIRST_FORWARD" ]; then
    if [ "$vertex" = yes ]; then
      fact model PASS "the service wrote no served model line (purpose 'forward') for the review run (${LAUNCH_RID}), and the job it ran on ('${LAUNCH_JOB}') carries the two names an \`agent.auth: vertex\` run needs (CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID): on that auth the agent's model calls do not go through the credentials service at all, so no forward line is the expected reading — U3's Vertex half is exactly this, read from the job's own environment and the run's own row. The purposes the service's lines for this run carry: ${purposes:-<none>}"
      return 0
    fi
    fact model FAIL "the service wrote no served model line (purpose 'forward', status ok) for the review run (${LAUNCH_RID}) of ${REPO}, while that run finished and recorded a verdict: the job it ran on mounts no credential at all (the job row above), so its model calls can only have been answered by the credentials service's own forward — a run whose calls were answered somewhere else is a credential this job does not have. The credential|tier|purpose the service's lines for this run name: ${triples:-<none>}. Check the job's own environment ('gcloud run jobs describe ${LAUNCH_JOB}') and the service's bundle"
    return 0
  fi
  local wrapped=",$FIRST_FORWARD,"
  case "$wrapped" in
  *",${ACCOUNT_ADMITS},"*)
    fact model PASS "the review run's first model call (${LAUNCH_RID}) was served with the credential '${FIRST_FORWARD}', one of the accounts the published node admits for ${REPO} (${ACCOUNT_ADMITS:-<none>}) — the earliest served line of purpose 'forward' the service wrote for that run, read by name and never by value. The job mounts nothing, so the account is the service's own choice for this repository: the review's model access is the node's admission and nothing the job holds"
    ;;
  *,anthropic-api-key--project,* | *,provider-key--project,*)
    fact model PASS "the review run's first model call (${LAUNCH_RID}) was served with the credential '${FIRST_FORWARD}', which is not a Claude account but the project's own key — the project's Anthropic API key or its provider key, the two names this release uses for them (config.AnthropicKeyAccount, config.ProviderKeyAccount). Those are not admitted per repository, so the accounts-list reading above does not apply to them: the call was answered by the project's own credential through the same service, which is U3's API-key case read from the outside. The accounts the node admits for ${REPO}: ${ACCOUNT_ADMITS:-<none>}"
    ;;
  *)
    fact model FAIL "the review run's first model call (${LAUNCH_RID}) was served with the credential '${FIRST_FORWARD}', which is neither one of the accounts the node admits for ${REPO} (${ACCOUNT_ADMITS:-<none>}) nor one of the two names this release uses for the project's own keys (anthropic-api-key--project, provider-key--project): the service offers a call only a credential the policy admits (the accounts block, the project's own keys), so a line naming another one is a call served by something this repository is not admitted to. The credential|tier|purpose the service's lines for this run name: ${triples:-<none>}. Read the policy's accounts block ('fugaro project policy show --json') and the service's bundle ('fugaro creds set')"
    ;;
  esac
}

# step_answers: the four UNVERIFIED items the review-job plan sends here, each read
# off the readings above rather than measured by a command of its own. A row whose
# reading was never taken is left unmeasured — the block says which row to read
# first — so none of them is guessed at.
step_answers() {
  # U1: the base-image review path. The job an implicit review runs on is deployed
  # with the installation's base image (infra's implicitReviewVariant takes it from
  # lc.BaseImage), and this is the review of a repository that carries an image of
  # its own: the two readings are the job's own image and the doctor's base image.
  if [ -z "$JOB_IMAGE" ] || [ -z "$BASE_IMAGE" ]; then
    unmeasured U1 "the job's image or the installation's base image was not read: the job row and the sandbox row above say which. U1 needs both"
  elif [ "$JOB_IMAGE" = "$BASE_IMAGE" ]; then
    fact U1 PASS "the review ran on '${LAUNCH_JOB}', whose container image is the installation's BASE image (${JOB_IMAGE}) — and the repository is one whose own \`checkout:\` line reads '${CHECKOUT_KIND:-<none>}'. So a review of this repository needed no image built from the repository at all: the base image carries the review recipe (which only reads the checkout), which is what U1 asks. The two names are the doctor's base_images.base and the job's own container image, read from the platform and compared here"
  else
    fact U1 FAIL "the review ran on '${LAUNCH_JOB}', whose container image is ${JOB_IMAGE}, while the installation's base image (the doctor's base_images.base) is ${BASE_IMAGE}: the implicit review-only job is deployed with the base image (infra's implicitReviewVariant takes lc.BaseImage(BaseKind)), so a review job on another image is the base-image review path NOT holding. Read 'gcloud run jobs describe ${LAUNCH_JOB}' and 'fugaro doctor --json' on this machine"
  fi
  # U2: a project with no profiles admits the default account to a repository. The
  # two readings are the profiles block (how many the document defines at all) and
  # the accounts block's own entry for the default account.
  if [ -z "$PROFILE_COUNT" ] || [ -z "$DEFAULT_ADMITTED" ]; then
    unmeasured U2 "the published policy's profiles and accounts blocks were not read: the policy row above says why"
  elif [ "$DEFAULT_ADMITTED" != yes ]; then
    fact U2 FAIL "the default account's entry of ${INSTALL}'s published accounts block does NOT admit ${REPO} (the block admits it to: ${ACCOUNT_ADMITS:-<none>}): the credentials service intersects the accounts a job's caller may be served by with the ones a repository's node admits, so an empty intersection is a review whose model calls have no candidate at all — the gap F4 names. Publish an accounts entry for the default account covering ${REPO} ('fugaro project policy publish', from a local config whose project_secrets admits it) and run this check again"
  else
    fact U2 PASS "the published policy of ${INSTALL} defines ${PROFILE_COUNT} profile(s) — ${PROFILE_COUNT} being 'a project with no profiles', the case U2 names, wherever it reads 0 — and its accounts block admits ${REPO} to 'default' (the accounts that admit it: ${ACCOUNT_ADMITS:-<none>}). That is F4 from the outside: a repository no profile serves is still admitted to the default account, so the review's model calls had a candidate, and the model row above is where the service's own answer is read"
  fi
  # U3: the default account when the project's auth is an API key or Vertex. The
  # readings are the account the launch resolved (the job's fixed segment), the
  # job's own environment, and the credential the service's line names.
  if [ "$LAUNCHED" != yes ] || [ -z "$JOB_VERTEX" ]; then
    unmeasured U3 "the launch and the review job's own environment were not both read: the rows above say why"
  elif [ "$LAUNCH_ACCOUNT" != default ]; then
    fact U3 FAIL "the review ran as the account '${LAUNCH_ACCOUNT:-<none>}' and not as the default one: U3 is about the DEFAULT account, which is the only one the implicit review-only job serves (its account segment is fixed where it is deployed). Read the launcher's own sentence in $SCRATCH/review.err"
  else
    local auth=the-credentials-service
    [ "$JOB_VERTEX" = yes ] && auth=vertex
    fact U3 PASS "the review ran as the account '${LAUNCH_ACCOUNT}' — the default one, which is the account the implicit review-only job of provider bitbucket is deployed for (${LAUNCH_JOB}), and the account the published accounts block admits this repository to (${DEFAULT_ADMITTED}). The job's own environment carries the two Vertex names (CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID) reading '${JOB_VERTEX}', so the auth this installation's reviews run under is ${auth}: on vertex the job's own environment names the project and its calls go straight there, and otherwise they go through the credentials service, whose forward line the model row above reads (${FIRST_FORWARD:-no forward line — see that row}). Either way the account is the DEFAULT one, which is U3's question, and neither reading of it is a credential this job holds"
  fi
  # U4: a live terraform apply with no project jobs. The readings are the
  # installation's own project jobs and the policy's profiles: with no profile to
  # build a coding job from, every `fugaro-p-…` job present is one the release
  # deploys of its own accord — the implicit review-only jobs.
  if [ -z "$PJOB_COUNT" ] || [ -z "$PROFILE_COUNT" ]; then
    unmeasured U4 "the installation's job list or the policy's profiles block was not read: the rows above say why"
  else
    local expected_bb expected_gh odd=no name
    expected_bb=$(reader jobname standard bitbucket default-review | sed -n 's/^name=//p')
    expected_gh=$(reader jobname standard github default-review | sed -n 's/^name=//p')
    local oldifs=$IFS
    IFS=,
    for name in ${PJOB_NAMES:-,}; do
      case "$name" in
      "$expected_bb" | "$expected_gh" | - | '') ;;
      *) odd=yes ;;
      esac
    done
    IFS=$oldifs
    if [ "${PJOB_COUNT:-0}" -lt 1 ]; then
      fact U4 FAIL "the installation holds NO project job at all ('gcloud run jobs list' names none beginning 'fugaro-p-'), while the policy defines ${PROFILE_COUNT} profile(s): the implicit review-only job of provider bitbucket exists without a profile — it is what this check's own review ran on — so a job list without it is a reading of another project or region. Read 'gcloud run jobs list --project=${PROJECT} --region=${REGION}' yourself"
    elif [ "$odd" = yes ]; then
      fact U4 FAIL "the installation's project jobs are ${PJOB_NAMES:-<none>}, and not all of them are the implicit review-only job this release's naming derives (${expected_bb} for bitbucket, ${expected_gh} for github) while the published policy defines ${PROFILE_COUNT} profile(s): a project job with no profile behind it is either a job this release does not name or a leftover of a profile that is gone (a retirement, which D2303 keeps behind a check of its own). Read 'gcloud run jobs list' and the policy's profiles block"
    else
      fact U4 PASS "the installation holds ${PJOB_COUNT} project job(s) — ${PJOB_NAMES} — and every one of them is the IMPLICIT review-only job this release derives (${expected_bb} for provider bitbucket, ${expected_gh} for github), while the published policy defines ${PROFILE_COUNT} profile(s): the apply that deployed them had no profile job to build, and deployed the implicit review-only job all the same. That is U4's live half — the golden plan test is the offline one — and the review this check ran landed on the bitbucket one of them"
    fi
  fi
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 611 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation and the project the checkout belongs to, and the base image"
  say "  [read] fugaro project policy show --json — no profile admits the repository, the budget mode, and the bitbucket_token block's proxy admission"
  say "  [network, BILLABLE] ONE review of ${REPO}#${PR}: fugaro review --pr ${PR} — a review run in the installation's implicit review-only job, its model calls billed, about as long as a review takes"
  say "  [read] fugaro runs ls --pr ${PR} --json — the run's own row, polled until it is terminal: status, outcome, pushed, and the job its execution names"
  say "  [network, read] gcloud run jobs list — the job the launch resolved: the service's address, no Bitbucket token and no secret at all"
  say "  [read] fugaro review show <slug>/<run-id> --json — the verdict the run recorded"
  say "  [network, read] gcloud logging read — the credentials service's own lines for that run: purpose 'git' (the clone of the head) and the first purpose 'forward' line (which credential answered the review's first model call)"
  fact sandbox INFO "planned: a real review measures this row; --dry-run did not"
  fact policy INFO "planned: a real review measures this row; --dry-run did not"
  fact launch INFO "planned: a real review measures this row; --dry-run did not"
  fact run INFO "planned: a real review measures this row; --dry-run did not"
  fact job INFO "planned: a real review measures this row; --dry-run did not"
  fact review INFO "planned: a real review measures this row; --dry-run did not"
  fact git INFO "planned: a real review measures this row; --dry-run did not"
  fact model INFO "planned: a real review measures this row; --dry-run did not"
  fact U1 INFO "planned: a real review measures this row; --dry-run did not"
  fact U2 INFO "planned: a real review measures this row; --dry-run did not"
  fact U3 INFO "planned: a real review measures this row; --dry-run did not"
  fact U4 INFO "planned: a real review measures this row; --dry-run did not"
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no policy read, no launch and no log read"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong sandbox policy launch run job review git model U1 U2 U3 U4 restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 611 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 611 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

main() {
  need_tools
  if [ -z "$FUGARO_BIN" ]; then
    die "Check 611 needs 'fugaro' on PATH: the check drives the release you have installed, and it is not there"
  fi
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_sandbox
  step_policy
  step_launch
  step_run
  step_job
  step_review
  step_served
  step_answers
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
PR_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a sandbox Bitbucket repository, which is what this check reviews"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --pr)
    [ $# -ge 2 ] || die "--pr needs the pull request to review: the review this check launches is one fugaro review --pr N, on a pull request of the sandbox repository"
    PR_CLI=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    say "unknown argument: $1"
    exit 2
    ;;
  esac
done

SANDBOX=${CHECK611_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 611 needs --sandbox: the checkout of a sandbox Bitbucket repository. The repository this check reviews, and the installation it reads it in, are both taken from that checkout. Usage: bash scripts/live-checks/check611.sh --sandbox <a sandbox Bitbucket repository checkout> --pr <N>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
PR=${CHECK611_PR:-${PR_CLI:-}}
case "$PR" in
"" ) die "Check 611 needs --pr: the pull request to review, as --pr 7. It is the ONE command this check launches — fugaro review --pr N — and a review of nothing is not one" ;;
*[!0-9]*) die "--pr reads '${PR}', which is not a number: give the pull request as --pr 7" ;;
esac
if [ "$PR" -lt 1 ]; then
  die "--pr reads ${PR}: a pull request number is positive, and a review of #0 is not one"
fi
DRY_RUN=${CHECK611_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK611_KEEP_SCRATCH:-}
RUN_SECONDS=${CHECK611_RUN_SECONDS:-2400}
POLL_SECONDS=${CHECK611_POLL_SECONDS:-20}
LOG_AGE=${CHECK611_LOG_AGE:-1h}
# The knob in minutes, for the comparison log_freshness makes against the time
# since the launch. A spelling this does not convert — anything but <digits>m,
# <digits>h or <digits>d — leaves it empty, and the knob is then used exactly as
# written and never widened: a unit this check cannot read is not one it may
# quietly shorten.
LOG_AGE_MINUTES=""
LOG_AGE_NUM=${LOG_AGE%?}
case "$LOG_AGE" in
*m | *h | *d)
  case "$LOG_AGE_NUM" in
  "" | *[!0-9]*) ;;
  *)
    case "${LOG_AGE#"$LOG_AGE_NUM"}" in
    m) LOG_AGE_MINUTES=$LOG_AGE_NUM ;;
    h) LOG_AGE_MINUTES=$(( LOG_AGE_NUM * 60 )) ;;
    d) LOG_AGE_MINUTES=$(( LOG_AGE_NUM * 1440 )) ;;
    esac
    ;;
  esac
  ;;
esac
LOG_LIMIT=${CHECK611_LOG_LIMIT:-200}
LOG_WAIT_SECONDS=${CHECK611_LOG_WAIT_SECONDS:-120}
LOG_POLL_SECONDS=${CHECK611_LOG_POLL_SECONDS:-5}
DOCTOR_TIMEOUT=${CHECK611_DOCTOR_TIMEOUT_SECONDS:-180}
CLI_TIMEOUT=${CHECK611_CLI_TIMEOUT_SECONDS:-180}
# Resolved before anything runs, and by absolute path: this check drives a real
# `fugaro` that reaches the cloud, so it must be the one on PATH when the check
# started rather than one a later step could put there.
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
