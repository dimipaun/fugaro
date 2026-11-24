#!/usr/bin/env bash
# Check 610 — the review-only job: `fugaro review --pr N` on a repository whose
# project publishes no profiles and whose workflow is baked.
#
# Plan task RJ9 of docs/review-job/implementation-plan.md writes this check; the
# number is that work's own (its README.md reserves 610-619). What it settles is
# read from the outside and nowhere else: the four UNVERIFIED items of
# docs/review-job/reconciliation.md that only a live installation can answer —
# **U1**, the base-image review path for a baked-image repository (D2388's "yes
# for now"); **U2**, that a project with no profiles admits the default account to
# a repository (F4); **U3**, the default account when the project's auth is an API
# key or Vertex; and **U4**, a live `terraform apply` with no project jobs (the
# golden plan test exists, a real apply is the owner's). Everything the owner
# ruled on 2026-11-13 and 2026-11-14 is merged code; whether a review really works
# out of the box on such a repository is an answer of a live installation. So this
# check opens ONE throwaway pull request in a SANDBOX GitHub repository and
# launches ONE `fugaro review --pr N` on it, then reads back what the installation,
# the run and the credentials service wrote about it.
#
# What it measures:
#
#   * the sandbox checkout: it is a Fugaro repository's checkout and its own origin
#     is a GITHUB repository (anything else is refused by name: this is the GitHub
#     half, and the Bitbucket half is Check 611), the working tree is clean and the
#     project, region and installation it belongs to are read from the checkout
#     itself (`fugaro doctor --json`), so the check measures the installation you
#     work in rather than one it guessed;
#   * the checkout's own `fugaro.yaml`, resolved (`fugaro config show --json`):
#     EVERY workflow's `checkout` is `baked` (config.CheckoutBaked; a `clone` or
#     `shared` workflow clones at launch and is a different job), the file carries
#     no `profiles:` key at all (a project's profiles are the owner's, written in
#     the project's config and published, never in a repository's file), and the
#     `agent.auth` the review's model calls are authenticated with is read here for
#     U3. A baked, profile-less repository is the case this check exists for: it
#     resolves no shared coding job, so the launch has to reach the review-only job
#     instead of refusing;
#   * the published policy (`fugaro project policy show --json`): it carries NO
#     profiles block (the project is the profile-less one the plan names, and it is
#     what makes the run below an answer about the implicit review job), it admits
#     the repository to the project, its budget mode is one the gateway exists in
#     (`observe` or `enforce` — off is not a mode any more, D3009: the gateway is
#     the only route to the credentials service), and its `accounts` block does or
#     does not admit the default account
#     to this repository — which is U2, the row below;
#   * the installation's jobs, read back with `gcloud run jobs list` BEFORE
#     anything is launched: the implicit review-only job is deployed
#     (`fugaro-p-standard-github-default-rev-04644ca962ed`, infra.ImplicitReviewJobName
#     — the profile, provider and account are the constants, the readable part of
#     the name is cut to 36 characters before the twelve hex of the hash, and both
#     are spelled below because a shell script cannot compute that derivation), and
#     it carries `FUGARO_CREDENTIALS_SERVICE_URL` (the switch,
#     and the ONLY route to a credential for a job that mounts none), it carries no
#     `FUGARO_SECRET_ENVS` and no secret volume (a review variant mounts nothing:
#     its read-only installation token and its reviewer's model calls both come
#     from the service), and its container image is the installation's base image;
#   * ONE throwaway pull request this check opens in the sandbox repository: one
#     file on a branch of its own. `fugaro review --pr N` reviews a pull request,
#     so the check has to leave one behind, and every run it launches reviews it;
#   * ONE review run: `fugaro review --pr N` from the checkout. What the launch is
#     held to: it launched a run (its own `--json` document names it, because a
#     review's run id is minted by the launcher and cannot be computed here), the
#     JOB it landed on is the implicit review-only job read above (a review on a
#     coding job is the thing the launcher's own resolution must never do), and the
#     account it runs as is the default one (`config.DefaultClaudeAccount`, which is
#     the only account the implicit job serves, D2385);
#   * the run's own row, polled with `fugaro runs ls --repo <repo> --pr N --json`
#     until it is terminal: it ended `succeeded` with outcome `reviewed` and it is a
#     review of THIS pull request — the run a refusing launcher never gets as far as,
#     and the whole of what "works out of the box" means;
#   * what the review wrote (`fugaro review show <run> --json`, the artifact's own
#     bytes): the signed review artifact of that very review run and that very pull
#     request, with a head commit and a verdict. That is the review being recorded,
#     and it names the account and model that wrote it — U3's second reading;
#   * the credentials service's OWN lines (`gcloud logging read` over
#     `credsvc: audit`, filtered by the review run's id): the earliest served line
#     of purpose `forward` — the review's first model call — and the NAME of the
#     credential that answered it. A review job mounts nothing, so the default
#     account is the only credential that may answer; a line naming any other (the
#     project's paid key above all) is the finding U3 exists for.
#
# What it creates: ONE throwaway pull request in the sandbox repository (a branch
# `chk610-<timestamp>` and one file, left behind: this check holds no credential of
# its own and removes nothing) and ONE review run of it (**billable**: a review's
# model calls are real). It writes nothing into the installation and nothing into
# any other repository: no cloud resource, no record, no object and no node.
#
# Usage:      bash scripts/live-checks/check610.sh --sandbox <a sandbox GitHub repository checkout>
#             bash scripts/live-checks/check610.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check610.sh --help
# Needs:      bash, mktemp, python3 (every `--json` document is read with it), git
#             (the origin read, the throwaway branch and its push), gh (the pull
#             request you review has to exist), gcloud (authenticated as YOU: the
#             job list and the service's own lines are read with your credentials)
#             and fugaro (the installed release — the review this check launches is
#             the one you have).
# Where:      your own machine, and the **sandbox** only. A project, a region, an
#             account, a repository or a path naming belong is refused by name, and
#             so is a coding agent's session: every command this check runs is one
#             you could type yourself.
# Costs:      wall clock, not a fixed fee: about 25 minutes at the terminal, and
#             that review's model calls (**billable** — it is an ordinary review of
#             the installation's agent, on the sandbox).
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check610_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C, and the checkout is left on the branch it was
#             on. The throwaway branch and its pull request are NOT removed: this
#             check holds no credential of its own.
# Output:     progress on stderr, ONE block on stdout headed CHECK 610 RESULT, then
#             CHECK 610 RESULT END (exit N) — to copy back into the register's Check
#             610 section, and nothing else. Exit 0: every criterion that was
#             measured held. 1: a measured criterion did not hold, or a guard
#             stopped the check before it could run. 2: a row could not be measured
#             for a reason that is this check's own setup or the installation's
#             configuration rather than a break (the block says which, by name). A
#             key the run never reached reads `FAIL — not reached`, which is a row
#             that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value and no
# authorization header. Every fact below is a NAME — a profile's name, a
# credential's name, a job's name, a repository's name, a status — and every line
# the service writes and this check reads is reduced to a short plain-word shape
# before it is printed. This check holds no credential at all: the only identity in
# the path is the one Cloud Run gives the job it launched.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK610_SANDBOX (or --sandbox: the checkout of a SANDBOX GitHub repository —
# REQUIRED, and the repository this check runs for is read from it),
# CHECK610_DRY_RUN, CHECK610_KEEP_SCRATCH, CHECK610_RUN_ID (the base of the
# throwaway branch this check pushes, default `date -u +%Y%m%d-%H%M%S`; the four
# characters `-0610` are appended. The REVIEW's own run id is minted by the
# launcher and read back from `fugaro review --json`, never computed here),
# CHECK610_RUN_SECONDS (how long the review is waited for; 2400),
# CHECK610_POLL_SECONDS (20), CHECK610_LOG_AGE (2h: the least the read of the
# service's lines looks back — a review that outlasted it is read from its own
# launch, because the row that proves U3 is its FIRST model call),
# CHECK610_LOG_LIMIT (200), CHECK610_LOG_WAIT_SECONDS (120),
# CHECK610_LOG_POLL_SECONDS (5), CHECK610_DOCTOR_TIMEOUT_SECONDS (180) and
# CHECK610_CLI_TIMEOUT_SECONDS (180). None of them can point this check at another
# installation: the project and the region are read from the checkout.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check prints its own
# RESULT block, and a trace of a run that launches a run would copy that run's own
# output to the terminal.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is written
# to the saved descriptors rather than to whatever fd 1 is doing then.
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

# unmeasured: a row the check could NOT measure, for a reason that is not a finding
# about the release. The row reads FAIL in the block — it is not a pass — and the
# check ends as one that could not measure (exit 2).
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
# bound kills IS the command — a hung cloud read would otherwise outlive the check
# that gave up on it. The ERR trap is dropped in it: this check runs commands whose
# failing exit status is one of its own readings, and a trap inherited there would
# turn each of them into "an internal error" before the step could report it.
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
  command -v bash >/dev/null 2>&1 || die "Check 610 needs bash"
  local t
  for t in mktemp python3 git gh gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 610 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk610-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
PRINTED=""

INSTALL=""
PROJECT=""
REGION=""
ACCOUNT=""
REPO=""
BASE_IMAGE=""
AUTH=""
# IMPLICIT_JOB and IMPLICIT_BITBUCKET_JOB are the two derived constants assigned in
# the inputs block below (nothing here computes them); IMPLICIT_IMAGE is what the job
# list answers for the one of them that is deployed.
IMPLICIT_JOB=""
IMPLICIT_IMAGE=""
BASE_BRANCH=""
ORIG_BRANCH=""
BRANCH=""
PR=""
# The review's own run, read back from `fugaro review --json`: its id is minted by
# the launcher (a review takes no --run-id), so it is read and never computed here,
# and the rows below hold it to the job and the account the same document names.
RUN_ID=""
RUN_SLUG=""
REVIEW_JOB=""
REVIEW_ACCOUNT=""
REVIEW_ARTIFACT_ACCOUNT=""
# SANDBOX_STOP: the checkout could not be read far enough to name the
# installation — this check's own setup, or a doctor that did not answer — and every
# row below rests on that installation. CONFIG_STOP and POLICY_STOP are the two
# premises of the measurement (a baked, profile-less repository of a project that
# publishes no profiles): a sandbox that is not one answers a question this check
# was not written to ask, so nothing is launched. JOB_STOP likewise: a launch whose
# job was never read cannot be held to it, and a review is billable.
SANDBOX_STOP=""
CONFIG_STOP=""
POLICY_STOP=""
JOB_STOP=""
# LAUNCH_EPOCH is the second the review was launched in, and empty until then: it
# is what log_freshness measures the read of the service's lines from.
LAUNCH_EPOCH=""
RUN_ROW="no"

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk610.XXXXXXXX") || die "Check 610 cannot make a scratch directory"
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
    say "keeping $SCRATCH (CHECK610_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created, said rather than assumed. The pull request and
# the review run it launched are not undone — they live on the sandbox repository,
# and this check holds no credential to remove them with.
restore() {
  if [ -n "$DRY_RUN" ]; then
    fact restore PASS "this was a dry run: no pull request was opened, no command was given to fugaro, git, gh or gcloud, and nothing was created"
    return 0
  fi
  if [ -n "$PR" ]; then
    fact restore PASS "pull request #${PR} of ${REPO} was opened by this check (branch ${BRANCH}, one file, base ${BASE_BRANCH}) and is NOT removed, and the review run launched on it (${RUN_ID:-none}) is not undone: settle them yourself when you are done — 'gh pr close ${PR} --repo ${REPO} --delete-branch' (the review comment it carries goes with it). The checkout ${SANDBOX} is back on the branch it was on (${ORIG_BRANCH:-unknown}). This check created no cloud resource, wrote no record and touched no other repository"
  else
    fact restore PASS "no pull request was opened and no run was launched, so nothing was created: it stopped before the pull request. This check creates no cloud resource, writes no record and touches no repository"
  fi
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
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK610_KEEP_SCRATCH is set), at $SCRATCH"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  # A row that reads FAIL is one of two things, and the verdict says which: a
  # criterion that did not hold (exit 1), or a row this check could not measure
  # (exit 2). Every unmeasured row is recorded FAIL too, so that it is never read as
  # a pass, and telling them apart is what UNMEASURED_KEYS is for.
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

# redact: what a gcloud, gh or fugaro message may carry back into the block, which
# is pasted into a register. A credential-shaped run of characters goes, whatever
# it is — the second line of that defence rather than the first.
redact() {
  sed -E \
    -e 's/([Aa]uthorization)[[:space:]]*:[^|]*/\1: <redacted> /g' \
    -e 's/(Bearer|Basic)[[:space:]]+[A-Za-z0-9._~+/=-]{12,}/<redacted>/g' \
    -e 's/[A-Za-z0-9_]*(token|Token|TOKEN|secret|Secret|SECRET)[A-Za-z0-9_]*=[^[:space:]]+/<redacted>/g' \
    -e 's/((oauth|api)[_-]?key["=: ]+)[A-Za-z0-9._~+/=-]{12,}/\1<redacted>/Ig'
}

# strip_credentials: every `scheme://user:token@` in the text becomes `scheme://`.
# A token-based clone carries its credential in the origin's userinfo, and that URL
# is printed in the refusals below, into a block the register quotes. redact's rules
# do not reach it: nothing there says `token=`, and a value the size of a personal
# access token is not a shape it knows. The rule is Check 480's and Check 580's,
# character for character: the userinfo ends at the last @ before the host, the way
# git's own URL parser reads it, so a pattern that cannot cross a `/` leaves a
# credential holding one in place verbatim; the class stops at whitespace, so the
# match cannot run past the URL into an @ later in the line.
strip_credentials() {
  sed -E 's#([a-zA-Z][a-zA-Z0-9+.-]*://)[^@[:space:]]*@#\1#g'
}

# last_lines FILE [N]: the last N lines of a file, on one redacted line, cut short.
# The one shape every failure row reads a command's own stderr with.
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
# resolved config, the published policy, the job list, the launch, a run's row, the
# review artifact and the service's own lines — so that no reading of a JSON shape
# is spelled twice and none of them is done in bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 610's reader: one file, one reading per subcommand, no credential in any
of them.

origin  argv: a git remote URL. Prints the `owner/name` it names and the host it is
        on, or `-` for a URL that is not one: the repository this check runs for is
        the one the sandbox checkout's own origin names.
doctor  argv: a `fugaro doctor --json` document. Prints the installation's name, its
        GCP project, its region and the base image its local config records
        (project.base_images.base, localcfg.BaseImages[config.BaseKind] — the image
        a review-only job is deployed with).
config  argv: a `fugaro config show --json` document. Prints the `agent.auth` the
        checkout resolves (U3's first reading) and, for every workflow, the
        `checkout` kind it resolves to — `checkout=` as a sorted `name=kind` list
        and `nonbaked=` naming the workflows that are NOT `baked` (an absent key is
        the baked default, config's own defaulting).
policy  argv: the `fugaro project policy show --json` document, a repository and its
        provider. Prints how many of the document's PROFILES admit it (U1's and
        RJ9's premise: a project that publishes no `profiles:` block has none), the
        policy's own profile count, whether the PROJECT's allow_repos admits it,
        whether the ACCOUNT block admits the default account to it (U2, F4), and the
        published budget mode.
jobs    argv: the installation's job list, the name stem of the implicit review-only
        job and the two names themselves (infra.ImplicitReviewJobName over the two
        infra.ImplicitReviewProviders). Reads every `fugaro-p-*` job — how many
        there are, how many of them ARE one of those two names (a review variant,
        which the name cannot be read for: gcp.derive cuts the readable part to 36
        characters, so the account segment of the github one is `default-rev`, never
        `default-review`) and how many are not (a job of a profile), with their
        names — and, for the stem, how many jobs carry it and the implicit job's own
        environment: whether it carries the credentials service's URL, whether it
        carries FUGARO_SECRET_ENVS or a secret volume (a review variant mounts
        nothing) and its container image.
launch  argv: the `fugaro review --json` document. Prints the run it launched, the
        job the execution landed on and the account it runs as.
run     argv: the listing's file, a run id. Prints that run's row field by field,
        `found=no` when the listing does not hold it, and the JOB its execution path
        names (`projects/<project>/locations/<region>/jobs/<job>/executions/<name>`,
        internal/backend's Execution.Name), which is the only place a run's job is
        written down.
artifact argv: the `fugaro review show --json` document, a run id and a pull request
        number. Prints the review artifact's own fields: the review run it is about,
        the pull request, the head commit, the verdict and the account and model that
        wrote it. The `repository` field of that document is the run's SLUG and is
        printed as such, never compared with a repository name.
audit   argv: the service's lines' file, a run id. `audit=<status>|<credential>|
        <tier>|<purpose>|<job>|<caller>` for each `credsvc: audit` line of that run —
        the status is `ok` for a call the service served, the credential and tier are
        the NAME and TIER of the credential it used (never a value) — and
        `first_forward=<credential>`, the credential of the EARLIEST line of purpose
        `forward` with status ok, which is the review's first model call.
"""
import json
import re
import sys


def word(v, cap=80):
    if v is None:
        return "-"
    s = str(v)
    s = re.sub(r"[\x00-\x1f\x7f]", " ", s).strip()
    return s[:cap] if s else "-"


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


def rows(path):
    doc = load(path)
    if isinstance(doc, list):
        return doc
    if isinstance(doc, dict):
        for key in ("runs", "items", "rows"):
            if isinstance(doc.get(key), list):
                return doc[key]
    return []


def env(spec):
    out = {}
    if isinstance(spec, dict):
        for e in spec.get("env") or []:
            if isinstance(e, dict) and e.get("name"):
                out[e["name"]] = e
    return out


def spec_of(doc):
    """The container spec of a job.

    A Job carries spec.template.spec.template.spec (the v1 shape `gcloud run jobs
    list` answers), and the known paths are tried in order before the document is
    searched for the first containers list."""
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

    The presence of the NAME is the reading and never its value: a secret a job
    mounts is a valueSource reference and one it was handed plainly is a value, and
    both are the job holding it — a reading that only looked at one of the two would
    answer "no secret" for a job that had one."""
    for c in containers:
        if name in env(c):
            return True
    return False


def secret_volumes(doc):
    """The count of volumes a job's pod spec mounts from a Secret.

    A secret can reach a container as an env entry or as a mounted volume, and
    FUGARO_SECRET_ENVS is only the first route: a review variant that mounts nothing
    has neither, so both are read."""
    spec = spec_of(doc)
    if not isinstance(spec, dict):
        return 0
    n = 0
    for v in spec.get("volumes") or []:
        if isinstance(v, dict) and v.get("secret") is not None:
            n += 1
    return n


def image_of(doc):
    for c in containers_of(doc):
        if isinstance(c, dict) and c.get("image"):
            return c["image"]
    return ""


def inside(entry, canonical):
    """Whether a published repository entry admits a canonical repository.

    Both sides are canonical — `github/acme/app` and the wildcards `*` and
    `github/acme/*` (projectpolicy.Canonical, MatchRepo) — so they are compared
    segment against segment, exactly as the runner does: the provider segment is a
    segment like any other, so an entry naming another provider admits nothing here,
    and `*` alone admits every repository."""
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
        print("repo=-")
elif cmd == "doctor":
    doc = load(sys.argv[2]) or {}
    project = doc.get("project") or {}
    images = project.get("base_images") or {}
    print("install=%s" % word(project.get("name"), 60))
    print("project=%s" % word(project.get("gcp_project"), 80))
    print("region=%s" % word(project.get("region"), 40))
    # config.BaseKind is "base", the kind a review-only job is deployed with
    # (infra's implicitReviewVariant: lc.BaseImage(config.BaseKind)).
    print("base_image=%s" % text(images.get("base"), 300))
elif cmd == "config":
    doc = load(sys.argv[2]) or {}
    values = doc.get("values") or []
    auth = ""
    checkouts = {}
    if isinstance(values, list):
        for v in values:
            if not isinstance(v, dict):
                continue
            p = v.get("path") or ""
            if p == "agent.auth":
                auth = v.get("value")
            if p.startswith("workflows.") and p.endswith(".checkout"):
                checkouts[p[len("workflows."):-len(".checkout")]] = v.get("value")
    print("auth=%s" % word(auth, 40))
    print("workflows=%d" % len(checkouts))
    print("checkout=%s" % text(",".join(
        "%s=%s" % (n, w if isinstance(w, str) and w else "(default)")
        for n, w in sorted(checkouts.items())), 300))
    print("nonbaked=%s" % text(",".join(
        n for n, w in sorted(checkouts.items()) if isinstance(w, str) and w and w != "baked"), 200))
elif cmd == "policy":
    repo = sys.argv[3]
    repo_provider = (sys.argv[4] if len(sys.argv) > 4 else "").strip()
    canonical = ("%s/%s" % (repo_provider, repo)) if repo_provider else repo
    doc = load(sys.argv[2]) or {}
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
        # (projectpolicy.Admits, which compares the canonical's provider against the
        # profile's): a profile serving another provider says nothing about this
        # repository, and one that writes no list of its own falls back to the
        # project's, which is nowhere near enough to count it.
        if repo_provider and pr.get("provider") != repo_provider:
            continue
        eff = pr.get("allow_repos") or project
        if any_admits(eff, canonical):
            names.append(name)
    accounts = policy.get("accounts") or {}
    if not isinstance(accounts, dict):
        accounts = {}
    # config.DefaultClaudeAccount, which is the account the implicit review-only job
    # serves (the axis's empty account, D2385).
    default_entry = accounts.get("default")
    default_entry = default_entry if isinstance(default_entry, dict) else {}
    default_repos = default_entry.get("allow_repos") or []
    admitting = [n for n in sorted(accounts)
                 if any_admits(((accounts.get(n) or {}).get("allow_repos") or []), canonical)]
    print("proj_admits=%s" % ("yes" if any_admits(project, canonical) else "no"))
    print("profiles=%s" % len(names))
    print("profile_names=%s" % text(",".join(sorted(names)), 300))
    print("policy_profiles=%s" % len(profiles))
    print("policy_profile_names=%s" % text(",".join(sorted(profiles)), 300))
    print("accounts=%s" % len(accounts))
    print("account_names=%s" % text(",".join(sorted(accounts)), 300))
    print("account_admits=%s" % text(",".join(admitting), 300))
    print("default_admits=%s" % ("yes" if any_admits(default_repos, canonical) else "no"))
    budget = policy.get("budget")
    budget = budget if isinstance(budget, dict) else {}
    print("mode=%s" % word(budget.get("mode"), 20))
elif cmd == "jobs":
    prefix = sys.argv[3]
    # The installation's implicit review-only jobs, by their own derived names
    # (infra.ImplicitReviewJobName over the two ImplicitReviewProviders): a
    # project job that IS one of them is a review variant, and one that is not
    # is a job of a profile — its coding job or its review variant. The name
    # cannot be read for that: gcp's derive cuts the readable part to 36
    # characters before the hash, and that cut leaves the github job's own
    # account segment as `default-rev`, so the `-review-` a reader of the name
    # would look for is not in it at all.
    review_jobs = set(sys.argv[4:])
    proj = other = review = implicit_jobs = 0
    proj_names = other_names = review_names = ""
    for doc in load(sys.argv[2]) or []:
        if not isinstance(doc, dict):
            continue
        name = ((doc.get("metadata") or {}).get("name")) or ""
        if not name.startswith("fugaro-p-"):
            continue
        proj += 1
        proj_names = "%s%s " % (proj_names, name)
        if name in review_jobs:
            review += 1
            review_names = "%s%s " % (review_names, name)
        else:
            other += 1
            other_names = "%s%s " % (other_names, name)
        if name.startswith(prefix):
            implicit_jobs += 1
            if implicit_jobs == 1:
                print("pjob=%s url=%s secrets=%s secretvolumes=%d image=%s" % (
                    word(name, 120),
                    "present" if carries(containers_of(doc), "FUGARO_CREDENTIALS_SERVICE_URL") else "absent",
                    "present" if carries(containers_of(doc), "FUGARO_SECRET_ENVS") else "absent",
                    secret_volumes(doc),
                    text(image_of(doc), 300)))
            else:
                print("pjob=%s (a second job of the same prefix)" % word(name, 120))
    print("proj_total=%d" % proj)
    print("proj_review=%d" % review)
    print("proj_other=%d" % other)
    print("proj_names=%s" % text(proj_names.strip(), 400))
    print("other_names=%s" % text(other_names.strip(), 400))
    print("review_names=%s" % text(review_names.strip(), 400))
    print("implicit_jobs=%d" % implicit_jobs)
elif cmd == "launch":
    doc = load(sys.argv[2]) or {}
    print("run=%s" % word(doc.get("run"), 160))
    print("run_id=%s" % word(doc.get("run_id"), 80))
    print("repo=%s" % word(doc.get("repo"), 120))
    print("status=%s" % word(doc.get("status"), 40))
    print("job=%s" % word(doc.get("job"), 160))
    print("account=%s" % word(doc.get("account"), 60))
    print("execution=%s" % text(doc.get("execution"), 200))
    print("pr=%s" % word(doc.get("pr"), 20))
elif cmd == "run":
    want = sys.argv[3]
    for row in rows(sys.argv[2]):
        if not isinstance(row, dict) or row.get("run_id") != want:
            continue
        print("found=yes")
        for key in ("repo", "workflow", "recipe", "status", "terminal", "outcome",
                    "pushed", "pr", "pr_url"):
            if key in row:
                v = row[key]
                if isinstance(v, bool):
                    v = "yes" if v else "no"
                # A pull request URL is the one field here that is long by nature and
                # is quoted whole in the register: a cut one is a URL the owner cannot
                # open.
                print("%s=%s" % (key, word(v, 200 if key == "pr_url" else 60)))
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
    want = sys.argv[3]
    wantpr = sys.argv[4] if len(sys.argv) > 4 else ""
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        print("schema_version=%s" % word(doc.get("schema_version"), 20))
        print("review_run=%s" % word(doc.get("review_run"), 80))
        # The artifact's own `repository` is the run's SLUG (runs/<slug>/<run>/
        # artifacts/), never a repository name: it is printed and not compared.
        print("slug=%s" % word(doc.get("repository"), 40))
        print("pr=%s" % word(doc.get("pr"), 20))
        print("pr_matches=%s" % ("yes" if str(doc.get("pr")) == str(wantpr) else "no"))
        print("run_matches=%s" % ("yes" if str(doc.get("review_run")) == str(want) else "no"))
        print("head_sha=%s" % word(doc.get("head_sha"), 60))
        print("base=%s" % word(doc.get("base"), 60))
        print("verdict=%s" % word(doc.get("verdict"), 40))
        reviewer = doc.get("reviewer")
        reviewer = reviewer if isinstance(reviewer, dict) else {}
        print("reviewer_account=%s" % word(reviewer.get("account"), 60))
        print("reviewer_model=%s" % word(reviewer.get("model"), 80))
        issues = doc.get("issues")
        print("issues=%d" % (len(issues) if isinstance(issues, list) else 0))
elif cmd == "audit":
    want = sys.argv[3]
    forwards = []
    for i, entry in enumerate(load(sys.argv[2]) or []):
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
            # The earliest line by its own timestamp, and by the document's order
            # where a line carries none: the review's FIRST model call is what U3's
            # row reads, and a later switch must not be read as it.
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check610.sh --sandbox <a sandbox GitHub repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a command's output to the terminal"
}

# guard_agent_session: the check is the owner's and it launches a billable review of
# a pull request in a real repository. Refusing here rather than letting the launch
# refuse one at a time is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 610 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_belong: this check opens a pull request and launches a review in a real
# repository, so every value it was given is read for the installation it must never
# touch before anything else runs. The values read from the checkout are guarded
# again where they are read.
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

# blocks_launch: the reason no pull request is opened and nothing is launched,
# "" when nothing blocks it. Every premise below is read before the launch on
# purpose: a review is billable and it commits to a real repository, so a premise
# that did not hold is a run nobody should pay for.
blocks_launch() {
  if [ -n "$SANDBOX_STOP" ]; then
    printf '%s' "the checkout could not be read far enough to name the installation; the sandbox row says why"
    return 0
  fi
  if [ -n "$CONFIG_STOP" ]; then
    printf '%s' "the checkout is not the baked, profile-less repository this check is for; the config row says why"
    return 0
  fi
  if [ -n "$POLICY_STOP" ]; then
    printf '%s' "the project's own policy is not one this check can measure a review in; the policy row says why"
    return 0
  fi
  if [ -n "$JOB_STOP" ]; then
    printf '%s' "the installation's review-only job was not read; the job row says why"
    return 0
  fi
  printf ''
}

# ------------------------------------------------------------------ the steps
# step_sandbox: the premise. The repository, the installation, the project and the
# region are all read from the checkout itself, so the check measures the
# installation you work in rather than one it guessed.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a sandbox GitHub repository)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the reviews this check launches are launched on the repository the checkout belongs to"
  fi
  local url="" rc=0
  url=$(git -C "$SANDBOX" remote get-url origin 2>"$SCRATCH/origin.err") || rc=$?
  if [ "$rc" -ne 0 ] || [ -z "$url" ]; then
    stop "the checkout at $SANDBOX names no origin this check could read (git exited ${rc}): $(last_lines "$SCRATCH/origin.err"). The repository this check runs for is the one the checkout's own origin names"
  fi
  # The credential goes before the URL is matched, recorded or printed: a
  # credentialled origin reaches the refusals below, the scratch file and the reader
  # alike, and the reader's patterns cannot cross a `/` inside a userinfo, so it
  # reads the host of one as `https` — a refusal naming a host that is not there,
  # which is what the header's never-print rule is the smaller half of.
  url=$(printf '%s\n' "$url" | strip_credentials)
  printf '%s\n' "$url" >"$SCRATCH/origin.txt"
  local got host
  got=$(reader origin "$url" 2>"$SCRATCH/origin.reader.err") || stop "the origin of $SANDBOX could not be read: $(last_lines "$SCRATCH/origin.reader.err")"
  host=$(printf '%s\n' "$got" | sed -n 's/^host=//p')
  REPO=$(printf '%s\n' "$got" | sed -n 's/^repo=//p')
  if [ -z "$REPO" ] || [ "$REPO" = - ]; then
    stop "the origin of $SANDBOX is '$url', which names no repository: the repository this check runs for is named by the origin, and a URL this check cannot read is one it will not guess at"
  fi
  if [ "$host" != github.com ]; then
    stop "the origin of $SANDBOX is $url, which is not a GitHub repository (its host is '${host:-none}'): this check is the GITHUB half — the implicit review-only job for provider github, and \`fugaro review --pr N\` resolving onto it — and the Bitbucket half is Check 611. Name a sandbox GITHUB repository checkout"
  fi
  # The working tree has to be clean before this check commits to it: it cuts a
  # branch from the base branch and puts one file on it, and a dirty tree would
  # either refuse that checkout or carry your uncommitted work onto the throwaway
  # branch. Nothing here stashes, resets or commits anything of yours.
  local dirty
  dirty=$(git -C "$SANDBOX" status --porcelain 2>"$SCRATCH/status.err" || true)
  if [ -n "$dirty" ]; then
    stop "the checkout at $SANDBOX has uncommitted changes, and this check commits one file to it (a throwaway branch and pull request): $(printf '%s' "$dirty" | redact | cut -c1-300). Commit, stash or discard them and run this check again — it will not touch them"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$FUGARO_BIN" doctor --json
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and still
  # prints its document; only a timeout or another status is no answer.
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
  BASE_IMAGE=$(printf '%s\n' "$got2" | sed -n 's/^base_image=//p')
  if [ "$INSTALL" = - ]; then INSTALL=""; fi
  if [ "$REGION" = - ]; then REGION=""; fi
  if [ -z "$PROJECT" ] || [ "$PROJECT" = - ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox belongs to, and every row below is read in it. What it printed is in $SCRATCH/doctor.out"
    SANDBOX_STOP=yes
    return 0
  fi
  case "${INSTALL}${PROJECT}${REGION}${REPO}" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${INSTALL:-<no name>} (${PROJECT}, region ${REGION}) and ${REPO}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout of the GITHUB repository ${REPO} with a clean working tree, and it belongs to the ${INSTALL:-<no name>} installation (${PROJECT}, region ${REGION}): the pull request this check opens and the review it launches are that repository's, read in that installation"
}

# step_config: the repository half of the premise — a BAKED workflow and no
# `profiles:` block — read from the checkout's own resolved config rather than by
# parsing YAML here. A baked repository resolves no shared coding job, which is
# exactly why a review of it has to reach the implicit review-only job.
step_config() {
  if [ -n "$SANDBOX_STOP" ]; then
    unmeasured config "there is no checkout to read a workflow of: the row above says why"
    CONFIG_STOP=yes
    return 0
  fi
  # The literal premise, before the resolved one: a project's profiles are the
  # owner's, written in the project's config and published, never in a repository's
  # file (localcfg's Profiles says so in one sentence: "They are the owner's to
  # write, here, never in fugaro.yaml"). A repository that writes the key anyway is
  # not the sandbox this check is written for.
  local stray
  stray=$(grep -nE '^[[:space:]]*profiles:' "$SANDBOX/fugaro.yaml" 2>/dev/null | head -n 1 || true)
  if [ -n "$stray" ]; then
    unmeasured config "the checkout's own fugaro.yaml carries a \`profiles:\` key (${stray}): this check is written for the sandbox repository of a project that publishes no profiles — a repository whose own file carries the key is a different case. Name a sandbox repository whose fugaro.yaml has no profiles: block and run this check again"
    CONFIG_STOP=yes
    return 0
  fi
  say "reading the checkout's resolved config: fugaro config show --json"
  run_bounded "$CLI_TIMEOUT" config "$FUGARO_BIN" config show --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured config "fugaro config show --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the workflow this repository resolves is the premise of every row below, and a config nobody read is not a baked workflow. Its output is in $SCRATCH/config.out and $SCRATCH/config.err — a checkout whose fugaro.yaml is not valid answers here. Refused: $(last_lines "$SCRATCH/config.err")"
    CONFIG_STOP=yes
    return 0
  fi
  if ! reader config "$SCRATCH/config.out" >"$SCRATCH/config.txt" 2>"$SCRATCH/config.reader.err"; then
    unmeasured config "fugaro config show --json printed a document this check could not read: what it printed is in $SCRATCH/config.out"
    CONFIG_STOP=yes
    return 0
  fi
  AUTH=$(field "$SCRATCH/config.txt" auth)
  local n nonbaked workflows
  n=$(field "$SCRATCH/config.txt" workflows)
  nonbaked=$(field "$SCRATCH/config.txt" nonbaked)
  workflows=$(field "$SCRATCH/config.txt" checkout)
  if [ "${n:-0}" -lt 1 ]; then
    unmeasured config "the checkout's resolved config carries no workflow at all ($SCRATCH/config.out): this check reviews a repository whose workflow is baked, and a repository with no workflow is one it cannot name as baked. Name the sandbox repository RJ9 names and run this check again"
    CONFIG_STOP=yes
    return 0
  fi
  if [ -n "$nonbaked" ]; then
    unmeasured config "the checkout resolves ${n} workflow(s) and ${nonbaked} is not baked (${workflows}): a workflow that is not 'baked' clones the repository's source at launch or runs a shared dependency layer, which is a different review path from the one D2388 and U1 are about. ${REPO} is not the sandbox this check is written for: name a repository every workflow of which resolves checkout 'baked', or set the workflow's checkout: baked in its own fugaro.yaml, and run this check again"
    CONFIG_STOP=yes
    return 0
  fi
  case "$AUTH" in
  - | '') AUTH="" ;;
  esac
  fact config PASS "${REPO}'s own fugaro.yaml carries no \`profiles:\` key and every one of its ${n} workflow(s) resolves checkout 'baked' (${workflows}) — config.CheckoutBaked, the derived image holding a clone the runner moves into place — and agent.auth resolves '${AUTH:-<none>}' (U3's first reading). A baked, profile-less repository is the case this check exists for: it resolves NO shared coding job, so a review of it has to reach the installation's implicit review-only job instead of being refused. A clone or shared workflow would be a different path, which is why one is not measured here"
}

# step_policy: the project half of the premise, and U2's own evidence. The project
# has to publish NO profiles block (that is what makes the run below an answer about
# the implicit review-only job rather than about a profile's shared job), it has to
# admit the repository, and the gateway has to exist — a review job mounts nothing,
# so the credentials service is its only route to a credential and the gateway is
# the only route to the service.
step_policy() {
  if [ -n "$SANDBOX_STOP" ] || [ -z "$INSTALL" ]; then
    unmeasured policy "there is no installation to read the published policy of: the row above says why"
    POLICY_STOP=yes
    return 0
  fi
  say "reading the published policy: fugaro project policy show --project ${INSTALL} --json"
  run_bounded "$CLI_TIMEOUT" policy "$FUGARO_BIN" project policy show --project "$INSTALL" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured policy "fugaro project policy show --project ${INSTALL} --json did not answer (exit ${BOUNDED_STATUS}): the policy is what admits this repository and what the review job is deployed from, and nothing is launched on a policy nobody read. Its output is in $SCRATCH/policy.out and $SCRATCH/policy.err"
    POLICY_STOP=yes
    return 0
  fi
  if ! reader policy "$SCRATCH/policy.out" "$REPO" github >"$SCRATCH/policy.txt" 2>"$SCRATCH/policy.reader.err"; then
    unmeasured policy "fugaro project policy show --json printed a document this check could not read: what it printed is in $SCRATCH/policy.out"
    POLICY_STOP=yes
    return 0
  fi
  if [ "$(field "$SCRATCH/policy.txt" proj_admits)" != yes ]; then
    unmeasured policy "the published policy of ${INSTALL} does not admit ${REPO} to the PROJECT (its own allow_repos does not cover it), so no profile may admit it either and a launch would be refused before any execution exists: add it with 'fugaro project repos add <the repository's canonical name>' and publish ('fugaro project policy publish'), then run this check again"
    POLICY_STOP=yes
    return 0
  fi
  local policy_profiles names
  policy_profiles=$(field "$SCRATCH/policy.txt" policy_profiles)
  names=$(field "$SCRATCH/policy.txt" policy_profile_names)
  if [ "${policy_profiles:-0}" -ne 0 ]; then
    unmeasured policy "${INSTALL}'s published policy carries a profiles block (${policy_profiles}: ${names}), so this project is not the profile-less one the plan's RJ9 names and a review of ${REPO} would answer a different question — U2 is about a project with NO profiles, and the implicit review-only job is what a profile-less project's review resolves onto. Name a sandbox repository of a project that publishes no profiles: block and run this check again"
    POLICY_STOP=yes
    return 0
  fi
  local mode
  mode=$(field "$SCRATCH/policy.txt" mode)
  case "$mode" in
  - | '')
    unmeasured policy "${REPO} is admitted with no profiles block and the policy document carries no budget mode (it reads '${mode:-<none>}'), so whether the gateway exists for a review of this repository is NOT measured: the gateway exists in observe and enforce (D2071, GatewayOn), and a mode the document does not carry is neither, while the review job's only route to a credential turns on it. Publish the project policy so it carries one ('fugaro project policy publish', observe or enforce) and run this check again"
    POLICY_STOP=yes
    return 0
    ;;
  esac
  local account_n account_names admitting
  account_n=$(field "$SCRATCH/policy.txt" accounts)
  account_names=$(field "$SCRATCH/policy.txt" account_names)
  admitting=$(field "$SCRATCH/policy.txt" account_admits)
  fact policy PASS "${INSTALL}'s published policy carries NO profiles block and admits ${REPO} to the project (its budget mode is '${mode}'), and its accounts block names ${account_n:-0} account(s) (${account_names:-<none>}), the one(s) admitting this repository reading '${admitting:-<none>}': whether the DEFAULT account is one of them is U2's own row below, read from this same document. A project with no profiles is what makes the run below an answer about the implicit review-only job rather than about a profile's shared job"
}

# step_jobs: the installation's jobs, read back BEFORE anything is launched. Two
# things are settled here: the implicit review-only job is deployed and holds what a
# review job has to hold (the service's address, no mounts, the installation's base
# image), and how many project jobs the installation deploys at all — U4's reading,
# kept for its row below.
step_jobs() {
  if [ -n "$SANDBOX_STOP" ] || [ -z "$PROJECT" ]; then
    unmeasured job "there is no installation to read the jobs of: the row above says why"
    JOB_STOP=yes
    return 0
  fi
  say "reading the installation's jobs: gcloud run jobs list --project=${PROJECT} --region=${REGION}"
  run_bounded "$CLI_TIMEOUT" jobs gcloud run jobs list --project="$PROJECT" --region="$REGION" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured job "gcloud run jobs list did not answer (exit ${BOUNDED_STATUS}): the review-only job is what the review has to land on, and a read that did not answer is not an installation without one. Its output is in $SCRATCH/jobs.out and $SCRATCH/jobs.err"
    JOB_STOP=yes
    return 0
  fi
  if ! reader jobs "$SCRATCH/jobs.out" "$IMPLICIT_PREFIX" "$IMPLICIT_JOB" "$IMPLICIT_BITBUCKET_JOB" >"$SCRATCH/jobs.txt" 2>"$SCRATCH/jobs.reader.err"; then
    unmeasured job "gcloud run jobs list printed a document this check could not read: what it printed is in $SCRATCH/jobs.out"
    JOB_STOP=yes
    return 0
  fi
  local n
  n=$(field "$SCRATCH/jobs.txt" implicit_jobs)
  case "${n:-0}" in
  1) ;;
  0)
    unmeasured job "no job of the installation is named '${IMPLICIT_PREFIX}-<hash>…' in 'gcloud run jobs list', so the implicit review-only job is NOT deployed: a review of a baked, profile-less repository resolves onto that job and nothing else (infra's implicitReviewVariant, which deploys it under the name infra.ImplicitReviewJobName derives from the triple whenever the credentials service is on and one GitHub App ID is recorded), so a launch would refuse and every row below would be a row about nothing. Run 'fugaro update' (it deploys the job without a profile) and run this check again"
    JOB_STOP=yes
    return 0
    ;;
  *)
    fact job FAIL "${n} jobs of the installation start with '${IMPLICIT_PREFIX}-': the implicit review-only job's name is one (profile, provider, account) triple plus a hash (gcp.ProjectJobName, whose readable part — here 'fugaro-p-standard-github-default-review', the triple sanitized into a name — is cut to 36 characters before the hash, and that cut is why the account segment reads 'default-rev' rather than 'default-review'), so two jobs of that stem are two deployments of one row, and which of them a review landed on would not be a fact of the policy. The jobs this check read: $(field "$SCRATCH/jobs.txt" proj_names). Run 'fugaro update' and read the job list yourself ('gcloud run jobs list --project=${PROJECT} --region=${REGION}')"
    JOB_STOP=yes
    return 0
    ;;
  esac
  # The job of that stem has to BE the name infra derives, not merely one that starts
  # like it: the stem is the derived name minus its twelve hex of hash, and both the
  # launch row and U4's classification below hold the installation to the derivation.
  local found
  found=$(field "$SCRATCH/jobs.txt" pjob | awk '{print $1}')
  if [ "$found" != "$IMPLICIT_JOB" ]; then
    unmeasured job "the installation's job of the implicit review-only stem is '${found}', and the name infra.ImplicitReviewJobName derives from the triple (profile 'standard', provider 'github', account 'default') is '${IMPLICIT_JOB}': a job whose hash is not the one the derivation gives is not the job this check's rows are about — its derivation here and the installation's may be two versions of one name — and a launch cannot be held to a job read under another name. Read 'gcloud run jobs list --project=${PROJECT} --region=${REGION}', compare the two names and run this check from the version the installation runs"
    JOB_STOP=yes
    return 0
  fi
  IMPLICIT_IMAGE=$(printf '%s' "$(field "$SCRATCH/jobs.txt" pjob)" | sed -n 's/.*image=//p')
  local url secrets volumes
  url=$(printf '%s' "$(field "$SCRATCH/jobs.txt" pjob)" | sed -n 's/.*url=\([a-z]*\).*/\1/p')
  secrets=$(printf '%s' "$(field "$SCRATCH/jobs.txt" pjob)" | sed -n 's/.*secrets=\([a-z]*\).*/\1/p')
  volumes=$(printf '%s' "$(field "$SCRATCH/jobs.txt" pjob)" | sed -n 's/.*secretvolumes=\([0-9]*\).*/\1/p')
  if [ "$url" != present ]; then
    fact job FAIL "the implicit review-only job '${IMPLICIT_JOB}' carries no FUGARO_CREDENTIALS_SERVICE_URL, and a review job mounts NOTHING: its read-only installation token and its reviewer's model calls both come from the credentials service, so a job that names no service has no route to a credential at all and every review it runs would fail at its first model call. This is the installation's own switch and it is written by the launcher alone ('fugaro update', from this machine's local config: the credentials service must be on). Re-apply and run this check again"
    JOB_STOP=yes
    return 0
  fi
  if [ "$secrets" = present ] || [ "${volumes:-0}" -gt 0 ]; then
    fact job FAIL "the implicit review-only job '${IMPLICIT_JOB}' names FUGARO_SECRET_ENVS (${secrets}) and mounts ${volumes:-0} secret volume(s): a review variant holds NO secret and NO secret volume (infra's reviewVariant deletes the variable and sets an empty SecretEnv, D2302's review half) — a job that mounts one is a job whose review runs with credentials this check's U3 row would then be reading the wrong thing about. What the job carries is read with 'gcloud run jobs describe ${IMPLICIT_JOB} --project=${PROJECT} --region=${REGION}'. Re-apply with 'fugaro update' so the variant is rewritten without them, then run this check again"
    JOB_STOP=yes
    return 0
  fi
  fact job PASS "the installation's implicit review-only job '${IMPLICIT_JOB}' is deployed and carries FUGARO_CREDENTIALS_SERVICE_URL (the switch, which nothing but a launcher writes) with NO secret: it names no FUGARO_SECRET_ENVS and mounts no secret volume — a review variant mounts nothing, so its read-only installation token and its reviewer's model calls both come from the credentials service. Its container image is '${IMPLICIT_IMAGE}', compared with the installation's base image in U1's own row below"
}

# git_in_sandbox KEY ARGS...: one git command in the sandbox checkout, under the
# same bound every other command gets and read back through the same .out/.err pair.
# Every one of them runs with -C, so nothing here depends on the shell's directory.
git_in_sandbox() { # git_in_sandbox KEY GIT-ARGS...
  local key=$1
  shift
  run_bounded "$CLI_TIMEOUT" "$key" git -C "$SANDBOX" "$@"
}

# pull_back: the checkout back on the branch it was on. The tree was verified clean
# before this check wrote its one file into it and cut its branch, so --force can
# only ever discard this check's own CHK610.md — never anything of yours.
pull_back() {
  [ -n "$ORIG_BRANCH" ] || return 0
  git -C "$SANDBOX" checkout --quiet --force "$ORIG_BRANCH" 2>/dev/null || true
}

# step_pull: the pull request the review reviews. `fugaro review --pr N` reviews a
# pull request, so the check has to leave one behind: one file on a branch cut from
# the base branch, pushed, and opened with gh. The checkout goes back to the branch
# it was on as soon as the push is done — the review is launched from the checkout,
# not from the branch, and your tree is left as it was found.
step_pull() {
  local why
  why=$(blocks_launch)
  if [ -n "$why" ]; then
    unmeasured pull "${why}, so no pull request is opened: it would be a change to a real repository and a review of it is billable"
    return 0
  fi
  ORIG_BRANCH=$(git -C "$SANDBOX" symbolic-ref --short -q HEAD 2>/dev/null || true)
  if [ -z "$ORIG_BRANCH" ]; then
    ORIG_BRANCH=$(git -C "$SANDBOX" rev-parse --short HEAD 2>/dev/null || true)
  fi
  say "reading ${REPO}'s default branch: gh repo view"
  run_bounded "$CLI_TIMEOUT" ghbase gh repo view "$REPO" --json defaultBranchRef -q .defaultBranchRef.name
  BASE_BRANCH=$(tr -d '\r\n' <"$SCRATCH/ghbase.out" 2>/dev/null || true)
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ] || [ -z "$BASE_BRANCH" ]; then
    unmeasured pull "gh could not read ${REPO}'s default branch (exit ${BOUNDED_STATUS}): this check cuts its throwaway branch from it, and a base branch it guessed at is a pull request against the wrong branch. gh says: $(last_lines "$SCRATCH/ghbase.err")"
    return 0
  fi
  BRANCH="chk610-${RUN_BASE}"
  say "opening a throwaway pull request: one file on ${BRANCH}, cut from ${BASE_BRANCH}"
  {
    printf '# Check 610\n\n'
    printf 'A throwaway pull request, opened by scripts/live-checks/check610.sh to give its\n'
    printf 'review something to review: `fugaro review --pr N` reviews a pull request, and this\n'
    printf 'repository is that check'"'"'s sandbox.\n\n'
    printf 'It carries this file and nothing else. Its branch (%s) and the review comment\n' "$BRANCH"
    printf 'the check leaves on this pull request stay here: the check holds no credential of\n'
    printf 'its own to remove them. Close it and delete the branch when you are done.\n'
  } >"$SCRATCH/pr-body.md"
  local rc=0
  git_in_sandbox git_fetch fetch --quiet origin "$BASE_BRANCH"
  if [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured pull "git could not fetch ${BASE_BRANCH} from ${REPO}'s origin (exit ${BOUNDED_STATUS}): $(last_lines "$SCRATCH/git_fetch.err"). The branch this check pushes is cut from that commit, and one cut from a guess is a pull request nobody asked for"
    return 0
  fi
  git_in_sandbox git_branch checkout --quiet -B "$BRANCH" FETCH_HEAD
  if [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured pull "git could not cut the throwaway branch ${BRANCH} in $SANDBOX (exit ${BOUNDED_STATUS}): $(last_lines "$SCRATCH/git_branch.err")"
    return 0
  fi
  cp "$SCRATCH/pr-body.md" "$SANDBOX/CHK610.md"
  git_in_sandbox git_add add -- CHK610.md
  # An identity of the check's own, so the commit does not depend on one being
  # configured in your checkout: the branch and its pull request are throwaway, and
  # the commit should read as the check's rather than as yours.
  git_in_sandbox git_commit -c user.name="Fugaro Check 610" -c user.email="check610@fugaro.invalid" \
    commit --quiet -m "Check 610: the pull request this check reviews"
  if [ "$BOUNDED_STATUS" != 0 ]; then
    pull_back
    unmeasured pull "git could not commit the throwaway file in $SANDBOX (exit ${BOUNDED_STATUS}): $(last_lines "$SCRATCH/git_commit.err"). The checkout is back on ${ORIG_BRANCH:-the branch it was on}"
    return 0
  fi
  git_in_sandbox git_push push --quiet origin "HEAD:refs/heads/$BRANCH"
  rc=$BOUNDED_STATUS
  # The checkout goes back to the branch it was on, whatever the push did: the
  # review below resolves its recipe from this checkout and does not care which
  # branch it is on, and your tree is left as it was found.
  pull_back
  if [ "$rc" != 0 ]; then
    unmeasured pull "git could not push the throwaway branch ${BRANCH} to ${REPO} (exit ${rc}): $(last_lines "$SCRATCH/git_push.err"). Nothing is reviewed on a branch that is not there. The checkout is back on ${ORIG_BRANCH:-the branch it was on}"
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" ghpr gh pr create --repo "$REPO" --base "$BASE_BRANCH" --head "$BRANCH" \
    --title "Check 610: a throwaway pull request to review" --body-file "$SCRATCH/pr-body.md"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured pull "gh could not open the pull request for ${BRANCH} (exit ${BOUNDED_STATUS}): $(last_lines "$SCRATCH/ghpr.err"). The branch IS on ${REPO} — delete it ('git -C ${SANDBOX} push origin --delete ${BRANCH}') or open the pull request yourself ('gh pr create --repo ${REPO} --head ${BRANCH}') and run this check again"
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" ghprview gh pr view "$BRANCH" --repo "$REPO" --json number -q .number
  # The number is held aside until it has been read as one: PR is what the launch
  # is given, and a value that reached it unread would be a review of whatever
  # `--pr` made of it.
  local got
  got=$(tr -d '\r\n' <"$SCRATCH/ghprview.out" 2>/dev/null || true)
  case "$got" in
  '' | *[!0-9]*)
    unmeasured pull "gh opened the pull request for ${BRANCH} but could not read its number back (exit ${BOUNDED_STATUS}, it printed '${got}'): the review is launched with --pr, and a number this check guessed at is a review of another pull request. Read it yourself ('gh pr list --repo ${REPO} --head ${BRANCH}')"
    return 0
    ;;
  esac
  PR=$got
  fact pull PASS "a throwaway pull request of ${REPO} was opened by this check: #${PR}, branch ${BRANCH} (base ${BASE_BRANCH}, one file, CHK610.md), and the checkout ${SANDBOX} is back on ${ORIG_BRANCH:-the branch it was on}. A review reviews a PULL REQUEST, so the check has to leave one behind; it is not removed — this check holds no credential of its own"
}

# step_launch: the ONE command the plan's RJ9 names. `fugaro review --pr N`, run
# from the checkout (the repository comes from its origin and the recipe from its
# own config). What it is held to: it launched a run, that run's JOB is the implicit
# review-only job read above, and the account is the default one — a launcher that
# put the review on a coding job, or on an account the review job does not serve,
# would be the very refusal the plan removed arriving in another shape.
step_launch() {
  local why
  why=$(blocks_launch)
  if [ -n "$why" ]; then
    unmeasured launch "${why}, so no review is launched"
    return 0
  fi
  if [ -z "$PR" ]; then
    unmeasured launch "no pull request of ${REPO} was opened, so there is nothing to review: the row above says why"
    return 0
  fi
  say "launching ONE review of ${REPO} pull request #${PR}: fugaro review --pr ${PR}"
  # The second the review is launched in, recorded HERE and not inside the launch:
  # that call is read through a command substitution elsewhere, which is a subshell,
  # and a value set there is the subshell's alone. log_freshness measures the read of
  # the service's lines from it.
  LAUNCH_EPOCH=$(date -u +%s)
  run_bounded "$CLI_TIMEOUT" launch "$FUGARO_BIN" review --pr "$PR" --project "$INSTALL" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured launch "fugaro review did not answer inside ${CLI_TIMEOUT}s and was ended: it may or may not have launched a review, so nothing about that review is read. Read the listing yourself ('fugaro runs ls --repo ${REPO} --pr ${PR} --project ${INSTALL}') before launching another one"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured launch "fugaro review --pr ${PR} exited ${BOUNDED_STATUS} and launched nothing: $(last_lines "$SCRATCH/launch.err"). A review refused before Cloud Run is the launcher's own sentence, and it is the reading this row carries — the refusal the plan's option C removes is the one that reads 'review-only variant of a shared project job, and this repository has none'. Read it above, fix what it names ('fugaro update' deploys the review-only job; 'fugaro project policy publish' the admission) and run this check again"
    return 0
  fi
  if ! reader launch "$SCRATCH/launch.out" >"$SCRATCH/launch.txt" 2>"$SCRATCH/launch.reader.err"; then
    unmeasured launch "fugaro review --json printed a document this check could not read: what it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  RUN_SLUG=$(field "$SCRATCH/launch.txt" run)
  REVIEW_JOB=$(field "$SCRATCH/launch.txt" job)
  REVIEW_ACCOUNT=$(field "$SCRATCH/launch.txt" account)
  local status
  status=$(field "$SCRATCH/launch.txt" status)
  if [ -z "$RUN_ID" ] || [ -z "$RUN_SLUG" ] || [ "$RUN_ID" = - ]; then
    unmeasured launch "fugaro review --pr ${PR} answered without naming the run it launched (its document is in $SCRATCH/launch.out): the rows below are about that run and its own id is the only handle on it"
    return 0
  fi
  if [ -z "$REVIEW_JOB" ] || [ "$REVIEW_JOB" = - ]; then
    unmeasured launch "the launch of the review run (${RUN_ID}) names no job in its own document ($SCRATCH/launch.out): a review on a job nobody read is a review this check cannot hold to the review-only job"
    return 0
  fi
  if [ "$REVIEW_JOB" != "$IMPLICIT_JOB" ]; then
    fact launch FAIL "fugaro review --pr ${PR} launched the run ${RUN_ID} on '${REVIEW_JOB}', which is NOT the installation's implicit review-only job '${IMPLICIT_JOB}': a review has to resolve onto that job for a baked, profile-less repository (infra.ImplicitReviewJobName, the triple this check read back from the job list), and a review on any other job is the launch selecting a CODING job or a profile's job — the safety the launcher's own resolution exists for. Read 'fugaro runs show ${RUN_ID} --repo ${REPO}' and the resolution ('fugaro review --pr ${PR}' prints the job it chose) before anything else"
    return 0
  fi
  if [ "$REVIEW_ACCOUNT" != default ]; then
    fact launch FAIL "fugaro review --pr ${PR} launched the run ${RUN_ID} on the review-only job '${REVIEW_JOB}' as the account '${REVIEW_ACCOUNT}', and the implicit job serves the DEFAULT account only (config.DefaultClaudeAccount, the axis's empty account: D2385 rules one row for it and no named-account rows). A review running as another account is one the job's own service account cannot be admitted as, so the service would refuse its first model call. Name no account (fugaro review has no such flag: the default is what a profile-less project resolves) and read 'fugaro project policy show --json' for the accounts block"
    return 0
  fi
  fact launch PASS "fugaro review --pr ${PR} launched ONE review run of ${REPO} (${RUN_ID}, status '${status:-launched}') on '${REVIEW_JOB}' — the installation's implicit REVIEW-ONLY job, the exact name this check read back from the job list, not a recomputation — as the Claude account 'default': no profile of the published policy was needed, and the review did not start on a coding job"
}

# step_run: the run's own row, polled until it is terminal. A review that ends
# `succeeded` with outcome `reviewed` is a review that ran to the end: it read the
# pull request, called a model through the service and wrote its verdict. A run that
# stopped anywhere before that names why in its own row.
step_run() {
  if [ -z "$RUN_ID" ]; then
    unmeasured run "no review run was launched, so there is no row to read: the row above says why"
    return 0
  fi
  say "waiting for the review run (${RUN_ID}) to end: fugaro runs ls --repo ${REPO} --pr ${PR} --json, every ${POLL_SECONDS}s"
  local waited=0 row="" found terminal
  while :; do
    row=""
    if "$FUGARO_BIN" runs ls --repo "$REPO" --pr "$PR" --project "$INSTALL" --no-cache --json >"$SCRATCH/rows.json" 2>>"$SCRATCH/run.err"; then
      row=$(reader run "$SCRATCH/rows.json" "$RUN_ID" 2>>"$SCRATCH/run.err" || true)
    fi
    found=$(printf '%s\n' "$row" | sed -n 's/^found=//p')
    terminal=$(printf '%s\n' "$row" | sed -n 's/^terminal=//p')
    if [ "$found" = yes ] && [ "$terminal" = yes ]; then break; fi
    if [ "$waited" -ge "$RUN_SECONDS" ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  printf '%s\n' "$row" >"$SCRATCH/row.txt"
  if [ "$found" != yes ]; then
    unmeasured run "the review run (${RUN_ID}) does not appear in 'fugaro runs ls --repo ${REPO} --pr ${PR}' after ${waited}s: it was launched but its row is not in the listing, so nothing about it is read. Read 'fugaro runs show ${RUN_ID} --repo ${REPO}' yourself"
    return 0
  fi
  if [ "$terminal" != yes ]; then
    unmeasured run "the review run (${RUN_ID}) had not reached a final status after ${waited}s: status=$(field "$SCRATCH/row.txt" status). A run still in flight is not an answer about the review-only job — raise CHECK610_RUN_SECONDS, or read that run's row yourself ('fugaro runs show ${RUN_ID} --repo ${REPO}')"
    return 0
  fi
  RUN_ROW=yes
  local status outcome pr
  status=$(field "$SCRATCH/row.txt" status)
  outcome=$(field "$SCRATCH/row.txt" outcome)
  pr=$(field "$SCRATCH/row.txt" pr)
  if [ "$status" != succeeded ] || [ "$outcome" != reviewed ]; then
    fact run FAIL "the review run (${RUN_ID}) of ${REPO} did not end as a review: status=${status:-unread}, outcome=${outcome:-none}, pr=${pr:-none}$(printf '%s' "$(field "$SCRATCH/row.txt" halt_detail)" | sed 's/^/, halt: /')$(printf '%s' "$(field "$SCRATCH/row.txt" reason)" | sed 's/^/, reason: /'). A review reaches 'succeeded' with outcome 'reviewed' when it read the pull request, called a model through the credentials service and wrote its verdict, so a run that stopped is the service, the default account's admission (U2's row below) or the review job not serving it. Read 'fugaro runs show ${RUN_ID} --repo ${REPO}' before anything else"
    return 0
  fi
  if [ "$pr" != "$PR" ]; then
    fact run FAIL "the review run (${RUN_ID}) of ${REPO} ended '${status}' (outcome=${outcome}) and its own row names pull request ${pr:-none} rather than the #${PR} this check opened: a review that ended about another pull request is not a review of this one"
    return 0
  fi
  fact run PASS "the review run (${RUN_ID}) of ${REPO} ended '${status}' with outcome '${outcome}' on the pull request this check opened (#${pr}, recipe '$(field "$SCRATCH/row.txt" recipe)'): it launched from a baked, profile-less repository, ran in the installation's review-only job, read the pull request, called a model and recorded its review — which is 'review works out of the box' in one row, and the run a refusing launcher never gets as far as"
}

# step_review: what the review wrote, read from the artifact's own bytes. That the
# artifact exists, is about THIS review run and THIS pull request, and carries a head
# commit and a verdict is the review being recorded rather than merely run.
step_review() {
  if [ "$RUN_ROW" != yes ]; then
    unmeasured review "no terminal row of this check's own review run was read, so its artifact is not read: the row above says why"
    return 0
  fi
  say "reading what the review wrote: fugaro review show ${RUN_SLUG} --json"
  run_bounded "$CLI_TIMEOUT" show "$FUGARO_BIN" review show "$RUN_SLUG" --project "$INSTALL" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact review FAIL "fugaro review show ${RUN_SLUG} --json did not answer (exit ${BOUNDED_STATUS}) although the run ended 'succeeded' with outcome 'reviewed': $(last_lines "$SCRATCH/show.err"). The run says it reviewed and its artifact is not readable, which is the review NOT being recorded. Read 'fugaro review show ${RUN_SLUG}' yourself"
    return 0
  fi
  if ! reader artifact "$SCRATCH/show.out" "$RUN_ID" "$PR" >"$SCRATCH/artifact.txt" 2>"$SCRATCH/artifact.reader.err"; then
    unmeasured review "fugaro review show --json printed a document this check could not read: what it printed is in $SCRATCH/show.out"
    return 0
  fi
  if [ "$(field "$SCRATCH/artifact.txt" found)" != yes ]; then
    fact review FAIL "fugaro review show ${RUN_SLUG} --json printed no artifact: the run (${RUN_ID}) ended 'succeeded' with outcome 'reviewed' and its review.json is not there, so the review the run recorded is one this installation cannot read back. Read 'fugaro review show ${RUN_SLUG}' yourself and 'fugaro runs show ${RUN_ID} --repo ${REPO}' for what the run wrote"
    return 0
  fi
  local matches run_matches head verdict
  matches=$(field "$SCRATCH/artifact.txt" pr_matches)
  run_matches=$(field "$SCRATCH/artifact.txt" run_matches)
  head=$(field "$SCRATCH/artifact.txt" head_sha)
  verdict=$(field "$SCRATCH/artifact.txt" verdict)
  if [ "$run_matches" != yes ] || [ "$matches" != yes ] || [ -z "$head" ] || [ "$head" = - ] || [ -z "$verdict" ] || [ "$verdict" = - ]; then
    fact review FAIL "the artifact of the review run (${RUN_ID}) does not state this review: review_run matches the run '${run_matches}', pr matches #${PR} '${matches}', head_sha '${head:-none}', verdict '${verdict:-none}'. The artifact is what the service signs and what every reader takes the review from (its first four fields after the version are what the signature binds), so one that is about another run, another pull request or no commit is not this review being recorded. Read 'fugaro review show ${RUN_SLUG} --json'"
    return 0
  fi
  REVIEW_ARTIFACT_ACCOUNT=$(field "$SCRATCH/artifact.txt" reviewer_account)
  fact review PASS "the review recorded its artifact for the run ${RUN_ID} and the pull request #${PR}: review.json (schema_version $(field "$SCRATCH/artifact.txt" schema_version), the run's slug $(field "$SCRATCH/artifact.txt" slug)) states this very review run, this very pull request, the head commit ${head} and the verdict '${verdict}', written by the account '${REVIEW_ARTIFACT_ACCOUNT}' on the model '$(field "$SCRATCH/artifact.txt" reviewer_model)' — the bytes the credentials service signs. That is the review being recorded on the pull request, not merely a run that ended"
}

# log_freshness: how far back the service's lines are read. The review's FIRST model
# call is the oldest forward line the service wrote for it, so the window has to
# reach back to the run's own start: CHECK610_LOG_AGE is the floor and this widens it
# by the time since the launch, which is what a review that outlasted the knob —
# CHECK610_RUN_SECONDS waits 2400s for one — would otherwise lose the head of.
log_freshness() {
  local since=$LOG_AGE mins
  if [ -n "$LAUNCH_EPOCH" ] && [ -n "$LOG_AGE_MINUTES" ]; then
    mins=$(( ($(date -u +%s) - LAUNCH_EPOCH) / 60 + 5 ))
    [ "$mins" -gt "$LOG_AGE_MINUTES" ] && since="${mins}m"
  fi
  printf '%s' "$since"
}

# audit_read RID OUT: the review run's service lines, retried while Cloud Logging
# catches up — ingestion lags the request by seconds. It returns 0 only when the last
# read ANSWERED: `gcloud logging read` exiting non-zero (no permission on the log, an
# expired session, a network answer) is NOT the service having written no line, and a
# row that took a failed read for silence would FAIL this check on nothing. The
# reason is written beside the file it is about (OUT.unread).
audit_read() {
  local rid=$1 out=$2 waited=0 filter rc=0
  filter='resource.type="cloud_run_revision" resource.labels.service_name="fugaro-credentials" jsonPayload.run="'"$rid"'"'
  while :; do
    rc=0
    # --order=asc, because gcloud answers the NEWEST --limit lines by default and a
    # review writes more than that: the review's first model call — the row U3 reads
    # — is the OLDEST forward line, and read newest-first it is not in the answer at
    # all, so the row would be decided on a later call's credential.
    gcloud logging read "$filter" --project="$PROJECT" --freshness="$(log_freshness)" \
      --limit="$LOG_LIMIT" --order=asc --format=json >"$out" 2>"$SCRATCH/audit.err" || rc=$?
    if [ "$rc" -eq 0 ] && reader audit "$out" "$rid" 2>/dev/null | grep -qE '^audit='; then return 0; fi
    if [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then
      [ "$rc" -eq 0 ] || {
        printf '%s' "the service's own lines for the review run (${rid}) could not be read: 'gcloud logging read' exited ${rc} and printed: $(last_lines "$SCRATCH/audit.err"). Every row read off those lines is read off what the service wrote for that run, and a read that did not answer is NOT the service having written no line. Read the log yourself ('gcloud logging read' with that run's id, and 'gcloud logging read --help' for the filter above) and run this check again" >"$out.unread"
        return 1
      }
      return 0
    fi
    sleep "$LOG_POLL_SECONDS"
    waited=$((waited + LOG_POLL_SECONDS))
  done
}

audit_unread() { cat "$1.unread" 2>/dev/null || true; }

# step_answers: the plan's four UNVERIFIED items, one row each, from the readings
# above. U1 is the baked-image review path, U2 the default account's admission, U3
# the account the review really ran as, U4 the live apply with no project jobs.
step_answers() {
  local why
  why=$(blocks_launch)
  if [ -n "$why" ]; then
    unmeasured u1 "${why}, so the job's image is not compared"
    unmeasured u2 "${why}, so the project's accounts block is not read"
    unmeasured u3 "${why}, so the review's own account is not read"
    unmeasured u4 "${why}, so the installation's project jobs are not counted"
    return 0
  fi
  answer_u1
  answer_u2
  answer_u3
  answer_u4
}

# answer_u1: the base-image review path for a baked-image repository (D2388's "yes
# for now"). The workflow is baked and the review ran in the installation's base
# image: the two readings are the config row's and the job list's.
answer_u1() {
  if [ -z "$BASE_IMAGE" ]; then
    unmeasured u1 "fugaro doctor --json carried no base image for this installation (project.base_images.base), so the review-only job's image '${IMPLICIT_IMAGE}' cannot be compared with it: U1 is about the review running in the installation's own base image, and an image this check has no other side to compare against is not that row"
  elif [ "$IMPLICIT_IMAGE" = "$BASE_IMAGE" ]; then
    fact u1 PASS "U1: ${REPO}'s workflow is BAKED (its checkout resolves 'baked') and the review ran in the implicit review-only job '${IMPLICIT_JOB}', whose container image is the installation's own base image '${BASE_IMAGE}' — fugaro doctor --json's project.base_images.base and infra's lc.BaseImage(config.BaseKind), byte for byte the same string, not a second reading of the same file. That is 'a baked repository is reviewed in the base image' (D2388's 'yes for now'), read from outside: the review of a repository that is never built ran in the image the installation ships"
  else
    fact u1 FAIL "U1: ${REPO}'s workflow is baked and the review-only job '${IMPLICIT_JOB}' runs the image '${IMPLICIT_IMAGE}', which is NOT the installation's base image '${BASE_IMAGE}' (fugaro doctor --json's project.base_images.base). D2388 answers U1 'yes for now' on the ground that the review recipe only READS the checkout, so the base image is sufficient — a review job on another image is a base image that was not what reviewed the repository. Compare the two reads yourself ('fugaro doctor --json' and 'gcloud run jobs describe ${IMPLICIT_JOB} --project=${PROJECT} --region=${REGION}') and re-apply with 'fugaro update'"
  fi
}

# answer_u2: whether a project with no profiles admits the DEFAULT account to a
# repository (F4). The policy row read the same document; this is the reading the
# plan's U2 asks for.
answer_u2() {
  local default_admits accounts admitting
  default_admits=$(field "$SCRATCH/policy.txt" default_admits)
  accounts=$(field "$SCRATCH/policy.txt" accounts)
  admitting=$(field "$SCRATCH/policy.txt" account_admits)
  if [ "$default_admits" = yes ]; then
    fact u2 PASS "U2: ${INSTALL}'s published policy carries no profiles block and its accounts block admits the DEFAULT account ('default', config.DefaultClaudeAccount) to ${REPO} — read from what 'fugaro project policy publish' writes for a profile-less project, the same document the launch was admitted by. That is F4's other gate: with no profile to carry claude_accounts, the admission a review's service call is checked against comes from the policy's accounts block, and the default account is in it. The launch above was admitted on exactly this reading, and the review's model call was served on it"
  else
    fact u2 FAIL "U2: ${INSTALL}'s published policy carries no profiles block and its accounts block does NOT admit the default account ('default') to ${REPO}: the accounts that admit it are '${admitting:-<none>}' out of ${accounts:-0} in the block. The credentials service pairs a review caller by the repository's admissions (credsvc's admittedAccounts), so a default account that is not admitted there is a review whose first model call the service refuses — F4's gap, and the reason the plan's U2 is an UNVERIFIED item at all. Read 'fugaro project policy show --json' and the service's own lines for the review run ('gcloud logging read' with its id); admit the default account to this repository and publish ('fugaro project policy publish')"
  fi
}

# answer_u3: the default account when the project's auth is an API key or Vertex —
# the checkout's auth, the service's own credential name and the artifact's reviewer
# account, all three read.
answer_u3() {
  case "$AUTH" in
  vertex | api-key) ;;
  # The reader prints `-` for an auth the checkout does not resolve at all.
  "" | -)
    unmeasured u3 "the checkout resolves no agent.auth at all, so the authentication this installation's model calls are made under is not one U3 is about ('an API key or Vertex'): the row cannot say which authentication the default account was used under. Set agent.auth in ${REPO}'s fugaro.yaml ('vertex', or 'api-key') and run this check again"
    return 0
    ;;
  *)
    unmeasured u3 "the checkout resolves agent.auth '${AUTH}', which is not one of the two U3 is about (an API key or Vertex): this row is about the default account when the project's auth is one of those, and an installation authenticating another way answers a different question. Name a sandbox repository whose fugaro.yaml resolves agent.auth 'vertex' or 'api-key' and run this check again"
    return 0
    ;;
  esac
  if [ "$RUN_ROW" != yes ]; then
    unmeasured u3 "the review run's own row was not read, so the credential the service served its model calls with is not read either: the row above says why"
    return 0
  fi
  say "reading the credentials service's own lines for the review run (${RUN_ID}): gcloud logging read (credsvc: audit)"
  local lines=$SCRATCH/audit.json got first_forward purposes triples
  if ! audit_read "$RUN_ID" "$lines"; then
    unmeasured u3 "$(audit_unread "$lines")"
    return 0
  fi
  got=$(reader audit "$lines" "$RUN_ID" 2>"$SCRATCH/audit.reader.err" || true)
  first_forward=$(printf '%s\n' "$got" | sed -n 's/^first_forward=//p')
  # What the service's lines DID carry, on one sorted line each: a row that found
  # nothing has to say what it read instead, and "nothing at all" is written out
  # rather than left as an empty gap.
  purposes=$(printf '%s\n' "$got" | sed -n 's/^audit=[^|]*|[^|]*|[^|]*|\([^|]*\)|.*/\1/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
  triples=$(printf '%s\n' "$got" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|\([^|]*\)|.*/\1|\2|\3/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
  if [ -z "$first_forward" ]; then
    fact u3 FAIL "U3: the checkout resolves agent.auth '${AUTH}' and the review run (${RUN_ID}) has NO served model line (purpose 'forward', status ok) in the credentials service's own audit, so nothing shows which credential answered the review's model calls — while a review-only job mounts nothing and the service is its only route to one. The credential|tier|purpose the service's lines for this run name: ${triples:-<none>}. Check the job's own environment ('gcloud run jobs describe ${IMPLICIT_JOB} --project=${PROJECT} --region=${REGION}') and the service ('fugaro creds set')"
    return 0
  fi
  if [ "$first_forward" != default ]; then
    fact u3 FAIL "U3: the review run's first model call (${RUN_ID}) was served with the credential '${first_forward}', not the DEFAULT account ('default'): the implicit review-only job serves that account and no other (config.DefaultClaudeAccount, D2385), and a review-only job holds no Claude credential of its own, so a call answered as another credential is one the job should not have been able to make. The credential|tier|purpose the service's lines for this run name: ${triples:-<none>}. Read 'fugaro project policy show --json' (its accounts block) and the service's bundle ('fugaro creds set')"
    return 0
  fi
  if [ "$REVIEW_ARTIFACT_ACCOUNT" != default ]; then
    fact u3 FAIL "U3: the review's model calls were served as the default account ('${first_forward}' in the credentials service's own audit for the run ${RUN_ID}) and the artifact the review wrote names the reviewer account '${REVIEW_ARTIFACT_ACCOUNT:-<none>}': the account the review states it ran as and the account the service really served are two readings of one fact, and they disagree. The artifact's own bytes are the review; read 'fugaro review show ${RUN_SLUG} --json'"
    return 0
  fi
  fact u3 PASS "U3: the checkout resolves agent.auth '${AUTH}' and the review's model calls were served by the installation's credentials service as the credential 'default' — the earliest served line of purpose 'forward' the service wrote for the run ${RUN_ID}, read by NAME and never by value — with the artifact naming the reviewer account '${REVIEW_ARTIFACT_ACCOUNT}': on an installation whose repositories authenticate with ${AUTH}, the default account is the one a review runs as, and it reaches the model through the service rather than through anything the review job holds. The purposes the service's lines for this run carry: ${purposes:-<none>}"
}

# answer_u4: a live terraform apply with no project jobs. Read off the job list: the
# installation deploys the review-only jobs and NO job of a profile — which is where a
# coding job comes from — and the review above ran in one of them: the golden plan
# test's real apply.
answer_u4() {
  local proj other other_names review_names
  proj=$(field "$SCRATCH/jobs.txt" proj_total)
  other=$(field "$SCRATCH/jobs.txt" proj_other)
  other_names=$(field "$SCRATCH/jobs.txt" other_names)
  review_names=$(field "$SCRATCH/jobs.txt" review_names)
  if [ "${other:-0}" -eq 0 ]; then
    fact u4 PASS "U4: ${INSTALL} (${PROJECT}, region ${REGION}) deploys ${proj:-0} project job(s) and EVERY one of them is an implicit review-only job — $(printf '%s' "${review_names:-<none>}" | tr ' ' ',') — with no job of a profile at all, and the review above ran in one of them: this is the live 'terraform apply' with no project jobs the golden plan test stands in for, and the review working on it is what U4 asks for (the plan test proves the plan, the apply is yours, and the run it produced is here)"
  else
    fact u4 FAIL "U4: ${INSTALL} deploys ${other} project job(s) which are NOT an implicit review-only job — ${other_names:-<none>} out of ${proj:-0} job(s) named 'fugaro-p-…'. U4 is 'a live terraform apply with NO project jobs', and a project job that is neither of the installation's two implicit review-only names (infra.ImplicitReviewJobName over infra.ImplicitReviewProviders) is a job of a profile: a coding job, or the profile's own review variant. The review above still ran in the review-only job, which is U1's and U2's subject, but the apply this row is about is not the one measured. Point this check at a profile-less project that deploys no job of a profile, or record that U4 is answered elsewhere"
  fi
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 610 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation, the project, the region and the base image the checkout belongs to"
  say "  [read] fugaro config show --json — the checkout's workflows (every one has to resolve checkout: baked), the absence of a profiles: key and agent.auth"
  say "  [read] fugaro project policy show --json — no profiles block, the project's admission, the accounts block's admission of the default account (U2) and the gateway's mode"
  say "  [network, read-only] gcloud run jobs list — the implicit review-only job, what it carries and what it mounts, and every fugaro-p-… job the installation deploys (U4)"
  say "  [network, WRITE] one throwaway pull request of the sandbox repository: a branch chk610-<timestamp> cut from its default branch, one file, pushed and opened with gh — LEFT BEHIND"
  say "  [network, BILLABLE] ONE review: fugaro review --pr <that pull request> — about 25 minutes, its model calls billed"
  say "  [read] fugaro runs ls --repo <repo> --pr <N> --json — the review run's own row, polled until it is terminal: status succeeded, outcome reviewed"
  say "  [read] fugaro review show <run> --json — the artifact the review wrote: this run, this pull request, a head commit and a verdict (and the account and model that wrote it)"
  say "  [network, read] gcloud logging read — the credentials service's own lines for that run: the earliest served purpose 'forward' line, which is the review's first model call and the credential that answered it (U3)"
  fact sandbox INFO "planned: a real run measures this row; --dry-run did not"
  fact config INFO "planned: a real run measures this row; --dry-run did not"
  fact policy INFO "planned: a real run measures this row; --dry-run did not"
  fact job INFO "planned: a real run measures this row; --dry-run did not"
  fact pull INFO "planned: a real run measures this row; --dry-run did not"
  fact launch INFO "planned: a real run measures this row; --dry-run did not"
  fact run INFO "planned: a real run measures this row; --dry-run did not"
  fact review INFO "planned: a real run measures this row; --dry-run did not"
  fact u1 INFO "planned: a real run measures this row; --dry-run did not"
  fact u2 INFO "planned: a real run measures this row; --dry-run did not"
  fact u3 INFO "planned: a real run measures this row; --dry-run did not"
  fact u4 INFO "planned: a real run measures this row; --dry-run did not"
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no config read, no policy read, no job list, no pull request, no launch and no log read"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong sandbox config policy job pull launch run review u1 u2 u3 u4 restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 610 RESULT\n' >&3
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
  printf 'CHECK 610 RESULT END (exit %s)\n' "$status" >&3
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
    die "Check 610 needs 'fugaro' on PATH: the check drives the review you have installed, and it is not there"
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
  step_config
  step_policy
  step_jobs
  step_pull
  step_launch
  step_run
  step_review
  step_answers
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
SANDBOX_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a sandbox GitHub repository, which is what this check runs for"
    SANDBOX_CLI=$2
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

SANDBOX=${CHECK610_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 610 needs --sandbox: the checkout of a sandbox GitHub repository whose project publishes no profiles and whose workflow is baked. The repository this check reviews, and the installation it reads it in, are both taken from that checkout. Usage: bash scripts/live-checks/check610.sh --sandbox <a sandbox GitHub repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
DRY_RUN=${CHECK610_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK610_KEEP_SCRATCH:-}
RUN_BASE=${CHECK610_RUN_ID:-$(date -u +%Y%m%d-%H%M%S)}
RUN_SECONDS=${CHECK610_RUN_SECONDS:-2400}
POLL_SECONDS=${CHECK610_POLL_SECONDS:-20}
LOG_AGE=${CHECK610_LOG_AGE:-2h}
# The knob in minutes, for the comparison log_freshness makes against the time since
# the launch. A spelling this does not convert — anything but <digits>m, <digits>h or
# <digits>d — leaves it empty, and the knob is then used exactly as written and never
# widened: a unit this check cannot read is not one it may quietly shorten.
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
LOG_LIMIT=${CHECK610_LOG_LIMIT:-200}
LOG_WAIT_SECONDS=${CHECK610_LOG_WAIT_SECONDS:-120}
LOG_POLL_SECONDS=${CHECK610_LOG_POLL_SECONDS:-5}
DOCTOR_TIMEOUT=${CHECK610_DOCTOR_TIMEOUT_SECONDS:-180}
CLI_TIMEOUT=${CHECK610_CLI_TIMEOUT_SECONDS:-180}
# The implicit review-only job's names, spelled rather than derived: a shell script
# cannot compute gcp's derivation (sanitize, cut to 36 characters, then twelve hex
# of sha256 over the profile, provider and account), and a live check that guessed
# at it would read an installation as one that deploys no review-only job at all.
#
# IMPLICIT_PREFIX is infra.ImplicitReviewJobName("github") without its trailing
# "-<twelve hex>", and is the stem this check counts the jobs of: two jobs of it
# are two deployments of one row. IMPLICIT_JOB and IMPLICIT_BITBUCKET_JOB are the
# two names themselves (infra.ImplicitReviewProviders, the profile-value,
# provider-value and account-value constants the deployer builds them from), and
# they are what tells a review-only job from a profile's job below — a project job
# that is neither of them is a job of a profile. These three strings are pinned to
# infra.ImplicitReviewJobName by TestLiveCheck610NamesTheImplicitReviewJobs, so a
# naming change breaks the pin rather than this check's reading of a real
# installation.
IMPLICIT_PREFIX="fugaro-p-standard-github-default-rev"
IMPLICIT_JOB="fugaro-p-standard-github-default-rev-04644ca962ed"
IMPLICIT_BITBUCKET_JOB="fugaro-p-standard-bitbucket-default-491922f654ef"
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
