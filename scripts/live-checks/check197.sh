#!/usr/bin/env bash
# Check 197 — coding runs on SHARED project jobs, GitHub and (after D2079)
# Bitbucket, through the credentials service, and after the flip the model
# mounts gone.
#
# It settles S5's two halves (plan B5.5, its "What it measures"; the
# reconciliation's **D2078**, **D2079**, **D2080**, **G21**, **F40** and
# UNVERIFIED **U9**). A shared project job (`fugaro-p-<profile>-<provider>`,
# plus the account axis and a hash, gcp.ProjectJobName) is the one job several
# repositories of a profile run in, and what makes that shape possible is the
# credentials service: the job holds the profile's own credentials, the run's
# git and model traffic is forwarded to the service, and the service binds each
# call to the repository the RUN IDENTITY names, admitted to the calling job's
# own profile (pairShared, G21). None of that is visible offline: whether the
# launch lands the run on the shared job at all, whether the service serves it
# from the job's own verified identity, whether a repository outside the profile
# is refused, and — after the flip — whether the mounts are really gone while a
# run in flight across the re-apply still finishes, are all answers of a live
# installation. So this check launches throwaway runs in SANDBOX repositories
# and reads the service's OWN lines back: `credsvc: audit` for the acts, which
# name the credential, its tier, the job the run reported and the ACCOUNT THE
# SERVICE VERIFIED.
#
# The Bitbucket half is gated on **D2079**'s T2.2 (a shared Bitbucket job exists
# only for a profile whose every repository the published block proxies, and it
# mounts no token): this installation's own shared jobs are GitHub ones until
# that lands (`infra.SharedJobProvider`), so an unnamed Bitbucket repository —
# or one whose profile has no shared job yet — leaves those rows UNMEASURED
# with that reason, never a FAIL.
#
# Usage:      bash scripts/live-checks/check197.sh
#             bash scripts/live-checks/check197.sh --dry-run      (prints the
#             plan and calls NOTHING: no gcloud, no fugaro)
#             bash scripts/live-checks/check197.sh --after-flip   (Check 197's
#             second half: the shared job's mounts, and the run that was in
#             flight across the re-apply)
# Needs:      bash, mktemp, python3, gcloud (authenticated as you: read
#             `run jobs` and one of its executions, and read Cloud Logging) and
#             fugaro. Nothing is installed, nothing is fetched, and no access
#             token is minted: this check reads no Realtime Database record (a
#             shared job's credential is the service's own bundle, and the one
#             record it could read — creds/repoauth — belongs to a
#             per-repository job, which is Check 193's subject and not this
#             one's).
# Where:      installation `fugaro` ONLY — project fugaro-dev, region us-east5.
#             Both are HARD-CODED in this file and never read from `gcloud
#             config get-value project`, and `fugaro doctor` must answer that
#             this is the fugaro-dev installation: a check that read the project
#             from the environment could be pointed at another installation by a
#             stray config, and this one must never be. Never belong.
# Creates:    throwaway runs in the SANDBOX repositories you name (one per
#             shared job, and with --after-flip one more), their branches and
#             pull requests, which **stay on those repositories**: this check
#             deletes no branch and closes no pull request, and their rows are
#             yours to settle. It writes nothing into any repository and nothing
#             into the installation: it created no cloud resource and no record.
# Costs:      wall clock, not money: about 40 minutes per run at the terminal.
#             Every model call of those runs is answered by the credentials
#             service, which is the subject of the check.
# Runs:       by YOU. No agent runs any step of it, and no agent has: what it
#             measures is a live installation. Its own test (check197_test.sh)
#             drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else — this check creates no
#             cloud resource. It runs from the exit trap too, so a Ctrl-C leaves
#             nothing of the check's behind.
# Output:     progress on stderr, ONE block on stdout headed CHECK 197 RESULT,
#             then ... RESULT END (exit N) — to copy back to the coordinator, and
#             nothing else. Exit 0: every criterion that was measured held. 1: a
#             measured criterion did not hold, or a guard stopped the check
#             before it could run. 2: a row could not be measured for a reason
#             that is this check's own setup rather than the installation (the
#             block says which, by name). In the block, a key the run never
#             reached reads `FAIL — not reached`, which is a row that was NOT
#             measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value and no
# authorization header. Every fact below is a NAME — a credential's name, an
# account's name, a job's name — and every line the service writes and this
# check reads is reduced to a short plain-word shape before it is printed. This
# check holds no credential at all: the only identity in the path is the one
# Cloud Run gives the job it launched.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK197_REPO (REQUIRED: `<owner>/<repository>`, the sandbox GitHub repository
# whose own fugaro.yaml SELECTS the job profile — that selection is what puts
# the run on the shared job, and a repository whose file names no profile stays
# on its per-repository job and stops this check with a FAIL that says so);
# CHECK197_OUTSIDE (REQUIRED: a sandbox repository whose fugaro.yaml selects the
# SAME profile and which that profile's own allow_repos does not admit, while
# the project's does — the launcher's refusal of it is a row of this check);
# CHECK197_BITBUCKET (optional: a sandbox Bitbucket repository the published
# block proxies and whose profile has a shared job; unset, the Bitbucket rows
# read unmeasured, which is their state until D2079's T2.2 lands);
# CHECK197_INFLIGHT (REQUIRED with --after-flip: the id of the run on a shared
# job that was already going when you re-applied with `fugaro update`), and
# CHECK197_RUN_ID (the base of this check's own run ids, default `date -u
# +%Y%m%d-%H%M%S`), CHECK197_RUN_SECONDS (how long a run is waited for; default
# 2400), CHECK197_POLL_SECONDS, CHECK197_LOG_WAIT_SECONDS,
# CHECK197_LOG_POLL_SECONDS, CHECK197_LOG_AGE, CHECK197_LOG_LIMIT,
# CHECK197_DRY_RUN and CHECK197_KEEP_SCRATCH. None of them can point this check
# at another project or another region: those two are constants in this file.
#
# -E (errtrace) as well as -e: without it a command that fails inside a command
# substitution ends the shell with a bare exit 1 and NO error trap — the block
# would print later rows as `FAIL — not reached` and the owner would get no line
# saying where the run broke. With it, every such failure reaches on_error and
# ends as one that could not measure (exit 2), which is what its own contract
# says of that.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the block this check
# prints is pasted into a pull request, and `bash -x` would print every command
# of the run into the log it is read from. BASH_XTRACEFD is unset so a trace
# redirected to a file this script knows nothing about cannot be reopened, and
# guard_xtrace REFUSES the run when the shell arrived with tracing on.
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

# unmeasured: a row the check could NOT measure, for a reason that is the
# check's own setup or the installation's configuration rather than a break. The
# row still reads FAIL in the block — it is not a pass and must not be read as
# one — but it does not by itself make the verdict "a criterion did not hold":
# the check ends as one that could not measure (exit 2), which is what its own
# contract says of a row that needed a state it did not find.
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

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 197 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  need bash
  need mktemp
  need python3 "every gcloud and fugaro document below is read by the reader in the scratch directory"
  need gcloud
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk197-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
PRINTED=""
FACTS_CAPTURED=""
CLEANUP_RUNNING=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk197.XXXXXXXX") || die "Check 197 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  fact scratch PASS "$SCRATCH (mode 0700, made by this check, removed by its EXIT trap)"
}

remove_scratch() {
  case "$SCRATCH" in
  "")
    # A guard stop leaves this empty and there is nothing to refuse: saying
    # "refusing to remove a scratch directory of ''" for a run that never made
    # one reads like a cleanup that went wrong. The guard stays for the two
    # paths that do mean trouble: a bare "/" and the owner's home directory.
    return 0
    ;;
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
    # The knob docs/secrets-brokering/check197-steps.md tells the owner to set to
    # read what the check wrote is not a directory that could not be removed:
    # the row below says which of the two it is, and only the second is a FAIL.
    SCRATCH_KEPT=yes
    say "keeping $SCRATCH (CHECK197_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created, undone. There is nothing of this check's in
# the installation or the project — the runs it launches live on the sandbox
# repositories and their rows are the owner's to settle, exactly as Check 193's
# are — so this row is about the one thing that COULD have been left: the
# scratch directory, which is removed by remove_scratch and read back there.
restore() {
  fact restore PASS "this check created no cloud resource, wrote no record and touched no repository: the only thing it made is the scratch directory, whose row is below"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to run, and a second
  # Ctrl-C during it would kill this shell with the reading half done.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  # The scratch directory's row is added before the verdict is taken, because
  # the verdict is taken from the facts: a directory this check could not remove
  # is a FAIL, and a block that printed `scratch-removed: FAIL` beside `END
  # (exit 0)` would read as a pass.
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory, so there is none to remove"
  elif [ "$SCRATCH_KEPT" = yes ]; then
    # A directory the owner asked to keep is not one this check failed to remove:
    # the row says it was kept, and the verdict stays whatever the reading found.
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK197_KEEP_SCRATCH is set), at $SCRATCH"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  # A row that reads FAIL is one of two things, and the verdict says which: a
  # criterion that did not hold (exit 1), or a row this check could not measure
  # — every unmeasured row is recorded FAIL as well, so that it is never read as
  # a pass, and its own contract names exit 2 ("could not measure"). Telling
  # them apart by the keys UNMEASURED_KEYS names is the only way to reach the
  # second: a block with any other FAILed row in it is the first, and it wins.
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

# redact: what a gcloud or fugaro message may carry back into the block, which
# is pasted into a pull request. A credential-shaped run of characters goes,
# whatever it is. No token of this check's ever reaches a message it prints, and
# this is the second line of that defence rather than the first.
redact() {
  sed -E \
    -e 's/([Aa]uthorization)[[:space:]]*:[^|]*/\1: <redacted> /g' \
    -e 's/(Bearer|Basic)[[:space:]]+[A-Za-z0-9._~+/=-]{12,}/<redacted>/g' \
    -e 's/[A-Za-z0-9_]*(token|Token|TOKEN|secret|Secret|SECRET)[A-Za-z0-9_]*=[^[:space:]]+/<redacted>/g' \
    -e 's/((oauth|api)[_-]?key["=: ]+)[A-Za-z0-9._~+/=-]{12,}/\1<redacted>/Ig'
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
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# write_reader installs the one file that reads every document this check takes
# — the service's own log lines, a run's row, the published policy, the
# installation's job list and one job's or execution's own spec — so that no
# reading of a JSON shape is spelled twice in this script and none of them is
# done in bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 197's reader: one file, one reading per subcommand, no credential in
any of them.

audit  argv: the service's lines' file, a run id. Two kinds of line for that
       run, and nothing else: `audit=<status>|<credential>|<tier>|<purpose>|
       <job>|<caller>` for each `credsvc: audit` line — the status is `ok` for a
       call the service served and a refusal's status otherwise, the credential
       and tier are the NAME and the TIER of the credential it used (never a
       value), the job is the one the RUN REPORTED and the caller is the service
       account the service VERIFIED (audit.go's AuditEvent: Job is Identity.Job,
       Caller is Identity.JobAccount) — and `refused=<status>|<sentence>` for
       each of the service's own refusal lines (`credsvc: refused`), whose
       `message` field is the sentence the run was sent. That sentence is the
       boundary's own text (credsvc's pairShared) and it is the only place a
       shared caller's refusal says WHAT it refused for.
run    argv: the listing's file, a run id. Prints that run's row field by field,
       `found=no` when the listing does not hold it, and the execution the run's
       own record names (`projects/<project>/locations/<region>/jobs/<job>/
       executions/<name>`, internal/backend's Execution.Name) — which is where
       this check reads the JOB its run landed on from, since the execution path
       is the only place the job's name is written down.
policy argv: the `project policy show --json` document, a repository. Prints
       whether the project's own allow_repos admits it (proj_admits), how many
       of the document's PROFILES admit it and their names (profiles, profile),
       the provider and repositories of the one that does, the published budget
       mode, and the published Bitbucket block's own admission of the repository
       and its proxy switch (bitbucket_admits, proxy) — the same two facts
       check191 and check193 read, because they are the same switch.
shared argv: the installation's job list, a NAME PREFIX. Prints one line per
       job whose name starts with the prefix — `job=<name> mounts=<the model
       containers its env is fed from, or none> url=<present|absent>
       token=<present|absent> sa=<the service account the job runs as>
       git=<github|bitbucket|other>` — and then the totals. A shared job's name
       is `fugaro-p-<profile>-<provider>[-<account>]` plus a hash
       (gcp.ProjectJobName, and projectStem is written and never parsed back,
       R2-5), so the prefix is how the jobs of one profile are read and `git` is
       a reading of that name and nothing else: it is used to PERMIT one row
       (the flip's token row) and never to decide what a job is.
job    argv: one job's or one execution's own document. Prints its name, when it
       was last updated, the model containers its env is fed from, the model
       variables it carries, how many provider keys it carries, whether
       FUGARO_CREDENTIALS_SERVICE_URL is in its env, whether
       FUGARO_BITBUCKET_TOKEN is, and the service account it runs as.
inflight argv: the job list's file, a job's own name, a run's started_at. Prints
       that job's updateTime and whether the run started before it: `fugaro
       update` rewrites the job of a profile it reaches, so the job's own
       updateTime is the moment the re-apply reached it, which is the instant a
       run in flight has to be on the far side of for U9 to be about it.
"""
import datetime
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


MODEL_ENVS = ("ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN")
MODEL_SECRETS = ("anthropic-api-key", "claude-oauth-token")
SECRET_HASH_RE = re.compile(r"-[0-9a-f]{16}$")
TOKEN_ENV = "FUGARO_BITBUCKET_TOKEN"
SERVICE_ENV = "FUGARO_CREDENTIALS_SERVICE_URL"
JOB_HASH_RE = re.compile(r"-[0-9a-f]{16}$")


def spec_of(doc):
    """The container spec of a job or of one execution of it.

    The two live at different depths — a Job carries
    spec.template.spec.template.spec and an Execution spec.template.spec — and a
    reading that pinned one of them would answer `mounts=` for every job of the
    other. So the known paths are tried in order and the document is finally
    searched for the first containers list, which is the one fact both shapes
    carry."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec"),
                 ("spec", "template", "spec", "containers")):
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


def salt_of(doc):
    spec = spec_of(doc)
    return ((spec or {}).get("serviceAccountName")) or ""


def model_container(base):
    """Whether a mounted secret's ID is one of the model containers.

    The ID a deployment writes is the DERIVED one, and it is the only one a job
    can be fed from: gcp.SecretID(slug, logical) builds
    `fugaro-<readable slug>-<logical>-<16 hex>` and gcp.ProjectSecretID builds
    `fugaro-project-<name>-<logical>-<16 hex>` (internal/backend/gcp/names.go:
    derive), so the logical name is the LAST readable segment of the ID and
    never the first. A named Claude account's own container is the same shape
    with the account after the logical name
    (`-claude-oauth-token-<account>-<hash>`, config.ClaudeOAuthTokenPrefix),
    which is why the name is looked for with a dash after it as well. So the two
    spellings are read off the hash-stripped ID, and a reading that matched the
    ID against the logical names by PREFIX — where those names never are — would
    answer "no mount" for every job a real installation has: a PASS on the
    flip's central row for a job that still holds the credential."""
    stem = SECRET_HASH_RE.sub("", base)
    return any(stem.endswith("-" + s) or ("-" + s + "-") in stem
               for s in MODEL_SECRETS)


def mounts_of(containers):
    """The MODEL secret containers these containers are fed from, as names.

    The env name binds the mount and the secret it points at is the DERIVED
    Secret Manager ID (the deployment mounts var.secrets[env.value]), so the two
    are read together: an env whose SECRET REFERENCE names one of the model
    secrets is a mount, whatever the variable is called, and a variable named
    ANTHROPIC_API_KEY that carries a plain value is one too.

    The reference is read in BOTH spellings the APIs carry it in, because
    `gcloud run jobs list` and `describe` answer the v1 Job and Execution
    documents while the v2 spelling is the other one the same mount is written
    in: run/v1's EnvVar names the secret valueFrom.secretKeyRef.name
    (google.golang.org/api's run/v1 EnvVarSource) and run/v2's
    GoogleCloudRunV2EnvVar names the same secret valueSource.secretKeyRef.secret.
    A reader that knew the v2 spelling only would see every mount of a real job
    as a plain variable: this check's central flip row would read PASS on a
    shared job that still holds the model credential."""
    out = []
    for c in containers:
        for name, e in env(c).items():
            ref = ((e.get("valueFrom") or {}).get("secretKeyRef") or {}).get("name") or ""
            if not ref:
                ref = (((e.get("valueSource") or {}).get("secretKeyRef") or {}).get("secret")
                       or ((e.get("valueSource") or {}).get("secretKeyRef") or {}).get("name") or "")
            base = ref.split(":")[-1].split("/")[-1]
            named = model_container(base)
            if named or (name in MODEL_ENVS and (e.get("value") or "")):
                short = base if named else name
                if short not in out:
                    out.append(short)
    return out


def model_envs_of(containers):
    out = []
    for c in containers:
        for name in env(c):
            if name in MODEL_ENVS and name not in out:
                out.append(name)
    return out


def provider_keys_of(containers):
    n = 0
    for c in containers:
        for name in env(c):
            if name.startswith("FUGARO_PROVIDER_KEY_"):
                n += 1
    return n


def carries(containers, name):
    """Whether any of these containers names that variable at all.

    The presence of the NAME is the reading and never its value: the Bitbucket
    token a shared job holds today is a mount (valueSource) and the flip's row
    is about the name being gone, so a variable carrying a plain value counts
    too — a reading that only looked at secretKeyRef would answer "no token" for
    a job that had one."""
    for c in containers:
        if name in env(c):
            return True
    return False


def inside(entry, repo):
    """Whether a published repository entry admits a repository. The entries are
    spelled with the host in front of the path — `bitbucket/acme/web`,
    `github/acme/app` (projectpolicy's allow_repos, and check191's reader reads
    the same shape) — and the knob this check is handed is `<workspace>/
    <repository>` without one, so the host segment is dropped before the two are
    compared. A segment may be `*`, which is how the published node spells a
    whole workspace."""
    e = entry.split("/")
    if e and e[0] in ("github", "bitbucket", "gitlab"):
        e = e[1:]
    r = repo.split("/")
    if len(e) != len(r):
        return False
    for a, b in zip(e, r):
        if a == b or a == "*":
            continue
        if "*" in a:
            if not re.match("^" + re.escape(a).replace(r"\*", "[^/]*") + "$", b):
                return False
        else:
            return False
    return True


def any_admits(entries, repo):
    return any(inside(e, repo) for e in entries if isinstance(e, str))


def git_of(name):
    """Which provider a shared job's NAME reads as: the name is
    `fugaro-p-<profile>-<provider>[-<account>]` plus a hash
    (gcp.ProjectJobName), so the provider is the readable segment after the
    profile and before the account. It is written and never parsed back (R2-5),
    and this reading is used to permit exactly one row — the flip's token row,
    which must never be satisfied by a GitHub job — so a name whose provider
    segment cannot be read is `other` and permits nothing."""
    stem = JOB_HASH_RE.sub("", name)
    if not stem.startswith("fugaro-p-"):
        return "other"
    rest = stem[len("fugaro-p-"):]
    if rest.endswith("-github") or "-github-" in rest:
        return "github"
    if rest.endswith("-bitbucket") or "-bitbucket-" in rest:
        return "bitbucket"
    return "other"


def parse_time(v):
    """An RFC 3339 stamp as an aware datetime, or None. Both shapes this check
    compares are UTC — a run record's started_at (runstore: RFC 3339 in UTC) and
    a Cloud Run job's metadata.updateTime — so the comparison is real arithmetic
    on the two instants and never a string comparison that would be wrong the
    moment one of them carried an offset."""
    if not isinstance(v, str) or not v.strip():
        return None
    t = v.strip()
    if t.endswith("Z"):
        t = t[:-1] + "+00:00"
    try:
        d = datetime.datetime.fromisoformat(t)
    except Exception:
        return None
    return d if d.tzinfo else d.replace(tzinfo=datetime.timezone.utc)


cmd = sys.argv[1] if len(sys.argv) > 1 else ""
if cmd == "audit":
    want = sys.argv[3]
    for entry in load(sys.argv[2]) or []:
        if not isinstance(entry, dict):
            continue
        p = entry.get("jsonPayload") or {}
        if p.get("run") != want:
            continue
        msg = entry.get("textPayload") or p.get("msg") or p.get("message") or ""
        if msg == "credsvc: audit":
            print("audit=%s|%s|%s|%s|%s|%s" % (
                word(p.get("status"), 40),
                word(p.get("credential"), 80),
                word(p.get("tier"), 8),
                word(p.get("purpose"), 40),
                word(p.get("job"), 120),
                word(p.get("caller"), 200)))
        elif msg == "credsvc: refused":
            print("refused=%s|%s" % (word(p.get("status"), 40), text(p.get("message"), 300)))
elif cmd == "run":
    want = sys.argv[3]
    for row in rows(sys.argv[2]):
        if not isinstance(row, dict) or row.get("run_id") != want:
            continue
        print("found=yes")
        for key in ("run_id", "status", "terminal", "outcome", "pushed", "pr", "repo",
                    "started_at", "execution", "job", "oauth_account"):
            if key in row:
                v = row[key]
                if isinstance(v, bool):
                    v = "yes" if v else "no"
                # The execution is a resource path of about a hundred
                # characters, so it is carried whole: a truncated one names an
                # execution nobody can describe.
                print("%s=%s" % (key, text(v) if key in ("started_at", "execution") else word(v, 60)))
        halt = row.get("halt")
        if isinstance(halt, dict):
            if halt.get("reason"):
                print("halt_reason=%s" % word(halt.get("reason"), 40))
            if halt.get("detail"):
                print("halt_detail=%s" % text(halt.get("detail"), 240))
        if row.get("reason"):
            print("reason=%s" % text(row.get("reason"), 240))
        break
    else:
        print("found=no")
elif cmd == "policy":
    repo = sys.argv[3]
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
        # The profile's own list is the narrower of the two and an absent or
        # empty one falls back to the project's (projectpolicy.ProfileAllowRepos
        # states it), which is what makes a repository admitted to a profile
        # whose list was never written.
        eff = pr.get("allow_repos") or project
        if any_admits(eff, repo):
            names.append(name)
    print("proj_admits=%s" % ("yes" if any_admits(project, repo) else "no"))
    print("profiles=%s" % len(names))
    print("profile=%s" % word(names[0], 60) if names else "profile=-")
    provider = ""
    repos = []
    if names:
        pr = profiles.get(names[0]) or {}
        provider = (pr.get("provider") if isinstance(pr, dict) else "") or ""
        repos = [e for e in ((pr.get("allow_repos") or []) if isinstance(pr, dict) else []) if isinstance(e, str)]
    if not repos:
        repos = [e for e in project if isinstance(e, str)]
    print("provider=%s" % word(provider, 40))
    print("profile_repos=%s" % text(", ".join(repos), 300))
    budget = policy.get("budget")
    budget = budget if isinstance(budget, dict) else {}
    print("mode=%s" % word(budget.get("mode"), 20))
    block = policy.get("bitbucket_token")
    block = block if isinstance(block, dict) else {}
    allow = block.get("allow_repos") or []
    print("bitbucket_admits=%s" % ("yes" if any_admits(allow, repo) else "no"))
    print("proxy=%s" % ("yes" if block.get("proxy") is True else "no"))
elif cmd == "shared":
    prefix = sys.argv[3]
    n = mounted = urls = tokens = 0
    for doc in load(sys.argv[2]) or []:
        if not isinstance(doc, dict):
            continue
        name = ((doc.get("metadata") or {}).get("name")) or ""
        if not name or not name.startswith(prefix):
            continue
        containers = containers_of(doc)
        mounts = mounts_of(containers)
        n += 1
        if mounts:
            mounted += 1
        if carries(containers, SERVICE_ENV):
            urls += 1
        if carries(containers, TOKEN_ENV):
            tokens += 1
        print("job=%s mounts=%s url=%s token=%s sa=%s git=%s" % (
            word(name, 120), ",".join(mounts) if mounts else "none",
            "present" if carries(containers, SERVICE_ENV) else "absent",
            "present" if carries(containers, TOKEN_ENV) else "absent",
            word(salt_of(doc), 200), git_of(name)))
    print("jobs=%s" % n)
    print("mounted=%s" % mounted)
    print("urls=%s" % urls)
    print("tokens=%s" % tokens)
elif cmd == "job":
    doc = load(sys.argv[2]) or {}
    containers = containers_of(doc)
    print("name=%s" % word(((doc.get("metadata") or {}).get("name")) or "-", 120))
    print("update=%s" % word(((doc.get("metadata") or {}).get("updateTime")) or "-", 40))
    mounts = mounts_of(containers)
    print("modelmounts=%s" % (",".join(mounts) if mounts else "none"))
    menvs = model_envs_of(containers)
    print("modelenvs=%s" % (",".join(menvs) if menvs else "none"))
    print("providerkeys=%s" % provider_keys_of(containers))
    print("url=%s" % ("present" if carries(containers, SERVICE_ENV) else "absent"))
    print("token=%s" % ("present" if carries(containers, TOKEN_ENV) else "absent"))
    print("sa=%s" % word(salt_of(doc), 200))
elif cmd == "inflight":
    # argv: the job list's file, a job's own name, a run's started_at.
    want = sys.argv[3]
    started = sys.argv[4]
    best = None
    for doc in load(sys.argv[2]) or []:
        if not isinstance(doc, dict):
            continue
        name = ((doc.get("metadata") or {}).get("name")) or ""
        if name != want:
            continue
        best = parse_time(((doc.get("metadata") or {}).get("updateTime")) or "")
        break
    print("job=%s" % word(want, 120))
    print("update=%s" % word(best.isoformat().replace("+00:00", "Z") if best else "", 40))
    order = "unknown"
    s = parse_time(started)
    if s is not None and best is not None:
        order = "before" if s < best else "after"
    print("order=%s" % order)
PY
  chmod 700 "$SCRATCH/reader.py"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    say "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print every command this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check197.sh -- the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry a command's environment"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`.
  # Check 197 is a reading on ONE installation: run anywhere else and every line
  # below is about a service, jobs and repositories of another project, which is
  # why this stops rather than warns.
  local doctor_out v
  # --all, because the `project:` line is part of doctor's informational tail:
  # the default view prints its pointer lines and stops, so a guard reading the
  # plain view finds no project line at all and stops every run with "this is
  # not the fugaro-dev installation".
  doctor_out=$(fugaro doctor --project fugaro --all || true)
  if ! printf '%s\n' "$doctor_out" | grep '^project: .*(GCP fugaro-dev,' >/dev/null; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project fugaro --all printed no 'project: ... (GCP fugaro-dev,' line. Either this is not the fugaro-dev installation, or --project fugaro disagrees with this checkout's project:, or doctor itself failed (doctor is not offline: it needs usable credentials). Run 'fugaro doctor --project fugaro --all' here yourself and read its output before going on — this check runs on the sandbox installation only, never belong"
  fi
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  case "$v" in
  dev | *-dev* | *dirty*)
    fact "fugaro-version" FAIL "$v — a development build is not the release this check is about: B5.5's check runs on the release that carries B5.1–B5.5, and a checkout build runs no released service image to reach (internal/cli's resolveCredentialsImage). Install the release under test and run it again"
    ;;
  *)
    fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
    ;;
  esac
  fact "project-region" PASS "${P}/${L} (installation fugaro, sandbox repositories only; never belong)"
}

guard_one_repo() { # guard_one_repo REPO KNOB
  local repo=$1 knob=$2 owner
  case "$repo" in
  */*/* | /* | */ | "") stop "$knob must be <workspace>/<repository> and it is '$repo'" ;;
  */*) ;;
  *) stop "$knob must be <workspace>/<repository> and it is '$repo'" ;;
  esac
  owner=${repo%%/*}
  case "$owner" in
  belong) stop "$repo names the owner's own workspace ($owner): this check runs against sandbox repositories of the fugaro-dev installation and nothing else, never belong" ;;
  esac
}

guard_inputs() {
  [ -n "$REPO" ] || stop "CHECK197_REPO must name the sandbox GitHub repository whose own fugaro.yaml selects the job profile this check reads, as <workspace>/<repository>"
  guard_one_repo "$REPO" CHECK197_REPO
  [ -n "$OUTSIDE" ] || stop "CHECK197_OUTSIDE must name a sandbox repository whose fugaro.yaml selects the SAME profile as CHECK197_REPO and which that profile does not admit (the project does), as <workspace>/<repository>: the launcher's refusal of that repository is a row of this check"
  guard_one_repo "$OUTSIDE" CHECK197_OUTSIDE
  [ "$OUTSIDE" != "$REPO" ] || stop "CHECK197_OUTSIDE names ${REPO} and CHECK197_REPO does too: the outside-profile repository is another repository, one the profile this check reads does not admit"
  if [ -n "$BITBUCKET" ]; then guard_one_repo "$BITBUCKET" CHECK197_BITBUCKET; fi
  case "$MODE" in
  flip)
    [ -n "$INFLIGHT" ] || stop "CHECK197_INFLIGHT must name the run on a shared project job that was already going when you re-applied it with 'fugaro update' (<run id>, the shape 'fugaro runs ls' prints): U9's reading is that THAT run finished, and a run this check picked itself could be one that ended hours before the re-apply"
    ;;
  esac
  fact repos PASS "$(printf '%s (the sandbox repository on the shared job), %s (the repository outside its profile)%s' "$REPO" "$OUTSIDE" "${BITBUCKET:+ and $BITBUCKET (the Bitbucket one)}") (never belong)"
}

# ------------------------------------------------------------- the run helpers
# run_id LABEL: the id the LABEL run is launched under. `fugaro run` admits one
# shape and only one (task.ValidRunID: ^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$), so the
# base — today's UTC date and a time — is followed by four hex characters, one
# value per label, which is what tells this check's runs apart in the listing
# and in the service's own lines.
run_id() {
  case "$1" in
  one) printf '%s-0197' "$RUN_BASE" ;;
  bitbucket) printf '%s-0198' "$RUN_BASE" ;;
  outside) printf '%s-0199' "$RUN_BASE" ;;
  flip-one) printf '%s-019a' "$RUN_BASE" ;;
  *) die "Check 197 has no run id for the label '$1'" ;;
  esac
}

# task_write FILE LABEL RID: the throwaway task. Its first line is the
# `# Task:` line `fugaro watch` and the pull request title come from, and what
# it asks for is the smallest thing that makes a run commit and open a pull
# request — which is what puts its model calls through the credentials service,
# the subject of this check. It is written by the FILE argument and never by a
# here-document on a command line (CLAUDE.md: an unquoted heredoc runs any
# backticked text).
task_write() { # task_write FILE LABEL RID
  {
    printf '# Task: Check 197 — the %s run (%s)\n\n' "$2" "$3"
    printf 'Add one line to `CHK197.md` at the repository root:\n\n'
    printf '    check 197 %s %s\n\n' "$2" "$3"
    printf 'Commit it on your own branch and open a pull request for it, then stop.\n'
    printf 'Change nothing else in the repository.\n'
  } >"$1"
}

# launch_into REPO LABEL RID TASKFILE: one throwaway run, printed as its
# terminal row. `fugaro run` returns AT LAUNCH, so the row is polled for by run
# id until the listing's own `terminal` word is yes — a reading taken straight
# after a launch is an in-flight row, which is a FAIL no run earned. A failure
# to launch or to read the row is not printed as a row: the reason goes to the
# launch-err file, which the caller's fact reads with launch_err. It is a FILE
# and not a variable because the callers read the row through a command
# substitution — `row=$(launch_into ...)` — and a variable set in there is set
# in a subshell: the caller would print a FAIL row with an empty reason, which
# is a row the owner cannot act on.
launch_into() {
  local repo=$1 label=$2 rid=$3 task=$4 rc=0 waited=0 row=""
  printf '%s' "" >"$SCRATCH/launch-err"
  say "launching the ${label} run (${rid}) in ${repo} — it goes on in the cloud"
  fugaro run --repo "$repo" --project fugaro --run-id "$rid" \
    --title "Check 197: ${label}" \
    --task-file "$task" --json >"$SCRATCH/run-$label.json" 2>"$SCRATCH/run-$label.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    printf '%s' "fugaro run for the ${label} run (${rid}) exited ${rc} and never launched one: $(last_lines "$SCRATCH/run-$label.err")" >"$SCRATCH/launch-err"
    return 1
  fi
  while :; do
    row=""
    if fugaro runs ls --repo "$repo" --project fugaro --no-cache --json >"$SCRATCH/rows-$label.json" 2>>"$SCRATCH/run-$label.err"; then
      row=$(reader run "$SCRATCH/rows-$label.json" "$rid" || true)
    fi
    if [ "$(field_of found "$row")" = yes ] && [ "$(field_of terminal "$row")" = yes ]; then break; fi
    if [ "$waited" -ge "$RUN_SECONDS" ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  printf '%s\n' "$row" >"$SCRATCH/row-$label"
  if [ "$(field_of found "$row")" != yes ]; then
    printf '%s' "the ${label} run (${rid}) does not appear in 'fugaro runs ls --repo ${repo}' after ${waited}s: it was launched but its row is not in the listing, so nothing about it is read" >"$SCRATCH/launch-err"
    return 1
  fi
  if [ "$(field_of terminal "$row")" != yes ]; then
    printf '%s' "the ${label} run (${rid}) had not reached a final status after ${waited}s: status=$(field_of status "$row"). A row still in flight is not an answer about the credentials service — raise CHECK197_RUN_SECONDS, or read that run's row yourself" >"$SCRATCH/launch-err"
    return 1
  fi
  printf '%s\n' "$row"
}

row_of() { cat "$SCRATCH/row-$1" 2>/dev/null || true; }

# launch_err: the reason the last launch_into call could not produce a row, as
# that call wrote it (see launch_into).
launch_err() { cat "$SCRATCH/launch-err" 2>/dev/null || true; }

# audit_read RID OUT: the run's service lines, retried while Cloud Logging
# catches up — ingestion lags the request by seconds, and a reading taken
# immediately is an empty answer about a service that did refuse. The loop runs
# until at least one line of this run's own is in the file or LOG_WAIT_SECONDS
# is up, and the answer the caller reads is the last one taken. A run whose
# lines are empty after the wait is itself a reading, and the callers say so
# rather than treating it as "nothing happened" — but only a read that ANSWERED
# is a reading: `gcloud logging read` exiting non-zero (no permission on the
# log, an expired session, a network answer) leaves OUT empty or half written,
# and a caller that took that for the service's silence would read the absent
# line as the answer it is looking for. The rows that live off an absent line
# are the `served-*` rows — a shared job's run is served by this service and by
# nothing else, so a failed read taken for silence would FAIL the check on
# nothing. check193.sh, check-c39.sh and check420.sh/check421.sh report their own
# failed log reads as unmeasured for the same reason.
#
# So the function distinguishes the two: it returns 0 only when the last read
# answered, and 1 when it did not, writing the row text for it beside the file
# it is about (OUT.unread) — where the rows that read OUT find it (audit_unread)
# and say they could not measure, rather than reading a file nobody wrote.
audit_read() {
  local rid=$1 out=$2 waited=0 filter rc=0
  filter='resource.type="cloud_run_revision" resource.labels.service_name="fugaro-credentials" jsonPayload.run="'"$rid"'"'
  while :; do
    rc=0
    gcloud logging read "$filter" --project="$P" --freshness="$LOG_AGE" \
      --limit="$LOG_LIMIT" --format=json >"$out" 2>"$SCRATCH/audit.err" || rc=$?
    if [ "$rc" -eq 0 ] && reader audit "$out" "$rid" | grep -qE '^(audit|refused)='; then return 0; fi
    if [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then
      [ "$rc" -eq 0 ] || {
        printf '%s' "the service's own lines for the run (${rid}) could not be read: 'gcloud logging read' exited ${rc} and printed: $(last_lines "$SCRATCH/audit.err"). Every row read off those lines is read off what the service wrote for that run, and a read that did not answer is NOT the service having written no line: nothing here says whether that run was served or not served. Read the log yourself ('gcloud logging read' with that run's id, and 'gcloud logging read --help' for the filter above) and run this check again" >"$out.unread"
        return 1
      }
      return 0
    fi
    sleep "$LOG_POLL_SECONDS"
    waited=$((waited + LOG_POLL_SECONDS))
  done
}

# audit_unread OUT: the row text for a lines file whose read did not answer, or
# nothing at all when the read answered — a read that answered and found no line
# of the run's own IS the service's silence, which is a reading and not a failed
# read (audit_read).
audit_unread() { cat "$1.unread" 2>/dev/null || true; }

# audit_served FILE RID: how many of the run's lines are the one status that is
# not a refusal — a credential answered, which is what the route writes for a
# call it forwarded (StatusOK, internal/credsvc/audit.go).
audit_served() { reader audit "$1" "$2" | sed -n 's/^audit=ok|.*/x/p' | wc -l | tr -d ' '; }

# audit_named FILE RID: the `credential|tier|purpose` triples of the run's
# served lines, one per line. The credential is a NAME — anthropic-api-key for
# the paid key, an ACCOUNT for a Claude subscription credential (`default` for
# the repository's default account, the bare account name for a named one: that
# is the name internal/credsvc's bundle holds the token's credential under), and
# git-credential names for the git purposes — and the tier is what it is of,
# which is how a row says WHICH credential answered without ever touching its
# value.
audit_named() {
  reader audit "$1" "$2" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|\([^|]*\)|.*/\1|\2|\3/p' | sort -u
}

# audit_callers FILE RID: the service accounts the service VERIFIED on the run's
# served lines, one per line and de-duplicated (AuditEvent.Caller is
# Identity.JobAccount, and a shared job's own service account is what the
# service's verifier answered the class from). It is the one field on those
# lines that a run cannot claim for itself, which is why the shared row reads it.
audit_callers() {
  reader audit "$1" "$2" | sed -n 's/^audit=ok|.*|\([^|]*\)$/\1/p' | sort -u
}

# audit_jobs FILE RID: the jobs the run's own lines REPORT (AuditEvent.Job,
# which is the caller's claim and decides nothing — auth.go records it beside
# the verified identity rather than instead of it). The row reads it to say that
# the claim agrees with where the execution ran, and never as the proof.
audit_jobs() {
  reader audit "$1" "$2" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|\([^|]*\)|\([^|]*\)|.*/\4/p' | sort -u
}

# job_of_file FILE: the job a run's own row says its execution ran on, read out
# of the execution path (`projects/<p>/locations/<r>/jobs/<job>/executions/<e>`,
# internal/backend's Execution.Name). The NAME is all this reads and it is the
# only place a run's job is written down: a row that guessed the job from the
# profile's own list would read a job the run never used.
job_of_row() { # job_of_row ROWTEXT
  local e j
  e=$(field_of execution "$1")
  case "$e" in
  */jobs/*/executions/*) ;;
  *) printf '' ;;
  esac
  j=${e#*/jobs/}
  j=${j%%/*}
  printf '%s' "$j"
}

# jobs_list: the installation's whole job list into $SCRATCH/jobs.json, return 0
# only when the read answered. Every mount and switch row is read off that one
# document, so a read that did not answer leaves them unmeasured rather than
# reading an empty installation.
jobs_list() {
  gcloud run jobs list --project="$P" --region="$L" --format=json >"$SCRATCH/jobs.json" 2>"$SCRATCH/jobs.err"
}

shared_of() { # shared_of PREFIX — the reader's reading of the jobs named by PREFIX
  reader shared "$SCRATCH/jobs.json" "$1"
}

# ------------------------------------------------------------------ the steps
# step_policy: the published policy, and the ONE profile this check is about.
# Four facts and one reason to read them together: the policy is readable (a
# read that did not answer leaves everything below unmeasured), the PROJECT's
# own allow_repos admits the repository (the project gate the launcher applies
# before the profile's), exactly one of the document's profiles admits it too
# (that is the profile the repository's own fugaro.yaml selected — a repository
# admitted to two of them is one whose selection this check cannot read, and it
# says so rather than guessing), that profile serves the git provider whose
# shared job this half reads, and the published budget mode is one the gateway
# exists in (`observe` or `enforce`; off is not a mode any more, D3009, and no
# gateway is no path to the service: D2071, GatewayOn). The mode is not the
# credentials-service switch
# itself, and the row says so — the switch is the job's own environment, read
# next — but a run whose gateway does not exist cannot be served at all.
step_policy() {
  local rc=0 got n
  got=$(fugaro project policy show --project fugaro --json 2>"$SCRATCH/policy.err") || rc=$?
  printf '%s\n' "$got" >"$SCRATCH/policy.json"
  if [ "$rc" -ne 0 ]; then
    unmeasured policy "the published policy could not be read: 'fugaro project policy show --project fugaro --json' exited ${rc} and printed: $(last_lines "$SCRATCH/policy.err"). Every row below is about a state read off that document — the profile that admits the repository, and the budget mode the gateway needs — so nothing is launched on a policy nobody read. Read it yourself ('fugaro project policy show --project fugaro --json') and run this check again"
    POLICY_STOP=yes
    return 0
  fi
  got=$(reader policy "$SCRATCH/policy.json" "$REPO")
  if [ "$(field_of proj_admits "$got")" != yes ]; then
    fact policy FAIL "the published policy does not admit ${REPO} to the PROJECT (its own allow_repos does not cover it), so no profile can admit it either and no run of it may be launched: add it with 'fugaro project repos add <the repository's canonical name>' and publish ('fugaro project policy publish'), then run this check again"
    POLICY_STOP=yes
    return 0
  fi
  n=$(field_of profiles "$got")
  case "${n:-0}" in
  0)
    fact policy FAIL "${REPO} is admitted to the project and to NO profile of the published policy: the profile the repository's own fugaro.yaml selects has an allow_repos that does not cover it (or the policy defines no profile at all), so this launch would be refused before any execution exists and there is no shared job for this check to read. Add the repository to that profile's allow_repos and publish ('fugaro project policy publish'), then run this check again"
    POLICY_STOP=yes
    return 0
    ;;
  1) ;;
  *)
    unmeasured policy "${n} profiles of the published policy admit ${REPO}, and which one the repository's own fugaro.yaml selects is not a fact of the policy: this check reads the profile from the document alone, so a repository admitted to more than one of them leaves every row below unmeasured. Name a sandbox repository exactly one profile admits and run this check again"
    POLICY_STOP=yes
    return 0
    ;;
  esac
  PROFILE=$(field_of profile "$got")
  PROVIDER=$(field_of provider "$got")
  # The jobs of one profile read off their own name prefix: the profile name is
  # the published policy's own (1 to 40 of a-z, 0-9 and '-', config.ProjectNameRE)
  # and gcp.ProjectJobName puts it in the name as it stands, so the prefix is the
  # profile's own word and no sanitizing can move it.
  SHARED_PREFIX="fugaro-p-${PROFILE}-github"
  if [ "$PROVIDER" != github ]; then
    fact policy FAIL "${REPO} is admitted to the profile '${PROFILE}', which serves provider '${PROVIDER:-<none>}': a shared job serves exactly ONE provider (the provider is part of the job's name and of the git credential the job holds), and this half of the check reads the GitHub one. Name a sandbox GitHub repository of a GitHub profile"
    POLICY_STOP=yes
    return 0
  fi
  local mode
  mode=$(field_of mode "$got")
  case "$mode" in
  None | - | '')
    unmeasured policy "${REPO} is on the profile '${PROFILE}' and the policy 'fugaro project policy show --project fugaro --json' printed carries no budget mode (it reads '${mode:-<none>}'), so whether the gateway exists for a run of this repository is NOT measured: the gateway exists in observe and enforce (D2071, GatewayOn), and a mode the document does not carry is neither. Publish the project policy so it carries one ('fugaro project policy publish', observe or enforce) and run this check again"
    POLICY_STOP=yes
    return 0
    ;;
  esac
  fact policy PASS "${REPO} is admitted to the profile '${PROFILE}' (provider github), which is the one piece of the policy a shared job's name is built from, and the published budget mode is '${mode}' (the mode is the gateway's, not the credentials-service switch: the switch is the job's own environment, read next)"
}

# step_switch: the profile's own SHARED job is deployed and it is on the
# credentials-service switch — the URL in its environment, which is the one
# thing a launcher writes and nothing else can (B2.3, D2072), and which is what
# makes a shared job's run reach the service at all. Both halves are read before
# any run is launched: this check's runs commit to a sandbox repository and open
# a pull request, and a run on a job that names no service would be a row about
# a job the check is not about. The account axis is read with it — one job per
# (profile, provider, account) — because every one of them has to carry the URL
# and, after the flip, none of them may mount a credential.
step_switch() {
  local listed=no got n
  if ! jobs_list; then
    fact switch FAIL "gcloud run jobs list exited non-zero: $(last_lines "$SCRATCH/jobs.err"). Nothing about the shared job of the profile '${PROFILE}' is settled by it"
    SWITCH_STOP=yes
    return 0
  fi
  listed=yes
  got=$(shared_of "$SHARED_PREFIX")
  n=$(field_of jobs "$got")
  if [ "${n:-0}" -lt 1 ]; then
    unmeasured switch "no job of the installation is named '${SHARED_PREFIX}…' in 'gcloud run jobs list', so the profile '${PROFILE}' serves no shared project job: the jobs of the profile are the ones a run of ${REPO} would land on, and this check reads a run of a SHARED job (gcp.ProjectJobName: fugaro-p-<profile>-<provider>[-<account>]). Deploy the profile's job ('fugaro update', which deploys one per (profile, provider, account) the config's profiles list) and run this check again"
    SWITCH_STOP=yes
    return 0
  fi
  if [ "$(field_of urls "$got")" -lt "$n" ]; then
    fact switch FAIL "$(field_of urls "$got") of ${n} shared job(s) of the profile '${PROFILE}' carry FUGARO_CREDENTIALS_SERVICE_URL and the rest do not: the job that names no service holds whatever credential it mounts and reaches nothing, so a run launched onto it would be a row about a job the credentials service does not serve. This is the profile's own switch, and it is turned on for every job of the profile at once ('fugaro update', from this machine's local config: the credentials_service block and the repositories' own entries). Re-apply and run this check again"
    SWITCH_STOP=yes
    return 0
  fi
  fact switch PASS "${n} shared job(s) of the profile '${PROFILE}' carry FUGARO_CREDENTIALS_SERVICE_URL — its account axis: $(printf '%s\n' "$got" | sed -n 's/^job=\([^ ]*\) .*/\1/p' | tr '\n' ' ' | sed 's/ *$//') — so every account's job of this profile is on the credentials-service switch (D2072)"
}

# step_run REPO LABEL FACTKEY: one throwaway run, and the service's own lines
# for it. Three things are read and they are one fact: the run reached a FINAL
# status that is not a failure (it committed and opened its pull request, which
# is only possible if its model calls were answered), the service wrote at least
# one line for it at all (a run on a shared job is served by this service and by
# nothing else — the job holds no credential of its own for the model path once
# the flip has reached it, and before the flip its model calls are forwarded to
# the service because its job carries the service's URL), and the line the
# service wrote names the SHARED JOB's OWN verified identity — the service
# account Cloud Run gave that job, which is the signal a caller cannot forge and
# which is what the service's shared class is derived from (auth.go's verifier,
# pairShared).
step_run() { # step_run REPO LABEL
  local repo=$1 label=$2 task rid row status lines served named callers jobs shared_job sa
  task=$SCRATCH/task-$label.md
  rid=$(run_id "$label")
  task_write "$task" "$label" "$rid"
  if ! row=$(launch_into "$repo" "$label" "$rid" "$task"); then
    unmeasured "run-$label" "$(launch_err)"
    unmeasured "audited-$label" "$(launch_err)"
    unmeasured "served-$label" "$(launch_err)"
    unmeasured "job-$label" "$(launch_err)"
    return 0
  fi
  status=$(field_of status "$row")
  case "$status" in
  succeeded)
    fact "run-$label" PASS "the ${label} run (${rid}) of ${repo} ended '${status}' (outcome=$(field_of outcome "$row"), pull request $(field_of pr "$row"))"
    ;;
  *)
    fact "run-$label" FAIL "the ${label} run (${rid}) of ${repo} ended '${status}' and no pull request was opened: $(field_of halt_detail "$row")$(field_of reason "$row") — a run on a shared job reaches every credential through the credentials service, so a run that could not finish is the service, the profile's shared job or the switch not serving it. Read 'fugaro runs show ${rid} --repo ${repo}' and the service's own lines for it, then run this check again"
    ;;
  esac
  # The job the run landed on, read off its own execution path (job_of_row). It
  # is a row of its own because every row below is about a SHARED job: a run
  # that landed on a per-repository job was launched by a repository whose
  # fugaro.yaml selects no profile — or whose profile this installation deploys
  # no job for — and nothing after it would be about the shape S5 is.
  shared_job=$(job_of_row "$row")
  if [ -z "$shared_job" ]; then
    unmeasured "job-$label" "the row of the ${label} run (${rid}) names no execution, so the job it ran on cannot be read: the execution path is the only place that job is written down. Read 'fugaro runs show ${rid} --repo ${repo}' — a run whose own record carries no execution is one whose job this row cannot read"
  elif [ ! -f "$SCRATCH/jobs.json" ] || ! shared_of "$SHARED_PREFIX" | sed -n 's/^job=\([^ ]*\) .*/\1/p' | grep -qxF -- "$shared_job"; then
    fact "job-$label" FAIL "the ${label} run (${rid}) ran on the job '${shared_job}', which is not a shared project job of the profile '${PROFILE}' (their names are '${SHARED_PREFIX}…', and the installation's jobs of that profile are: $(shared_of "$SHARED_PREFIX" | sed -n 's/^job=\([^ ]*\) .*/\1/p' | tr '\n' ' ' | sed 's/ *$//' | sed 's/^$/<none>/')). A run lands on a shared job only when the repository's own fugaro.yaml SELECTS the profile — 'jobs.profile' in ${repo}'s fugaro.yaml — and this installation deploys that profile's job. Make the repository select the profile (and re-apply: 'fugaro update'), then run this check again"
  else
    fact "job-$label" PASS "the ${label} run (${rid}) of ${repo} ran on the shared project job '${shared_job}', whose name is built from one of the policy's profiles ('${SHARED_PREFIX}…'), and not on a per-repository job"
  fi
  lines=$SCRATCH/audit-$label.json
  # Both rows below are read off the service's own lines for this run, so a read
  # that did not answer leaves them unmeasured rather than FAILed: the run's own
  # row above already carries why it ended as it did, and nothing here is a
  # finding about the credentials service (audit_read).
  if ! audit_read "$rid" "$lines"; then
    unmeasured "audited-$label" "$(audit_unread "$lines")"
    unmeasured "served-$label" "$(audit_unread "$lines")"
    return 0
  fi
  served=$(audit_served "$lines" "$rid")
  named=$(audit_named "$lines" "$rid")
  jobs=$(audit_jobs "$lines" "$rid" | tr '\n' ' ' | sed 's/ *$//')
  if [ "${served:-0}" -lt 1 ]; then
    fact "audited-$label" FAIL "the service wrote NO line for the ${label} run (${rid}) of ${repo} at all, while the run finished and opened a pull request: a run on a shared job reaches its model calls through the credentials service (the job's own environment names it), so its calls were answered by something else — an upstream that is not this service, or a credential the job should not hold after the flip. Read 'fugaro runs show ${rid} --repo ${repo}' and the service's lines for it before anything else"
    return 0
  fi
  fact "audited-$label" PASS "${served} line(s) of the service's own audit for the ${label} run (${rid}); the credential|tier|purpose triples they name: $(printf '%s' "$named" | tr '\n' ' ' | sed 's/ *$//'); the job(s) those lines report: ${jobs:-<none>}"
  # The shared job's own service account, read off the job's spec: it is what
  # the service VERIFIED the caller as, and comparing it with the lines is how
  # this row says the SHARED job was served rather than a per-repository job of
  # some other shape.
  sa=""
  if [ -n "$shared_job" ]; then
    if gcloud run jobs describe "$shared_job" --project="$P" --region="$L" --format=json >"$SCRATCH/job-$shared_job.json" 2>"$SCRATCH/describe.err"; then
      sa=$(field_of sa "$(reader job "$SCRATCH/job-$shared_job.json")")
    else
      unmeasured "served-$label" "the shared job '${shared_job}' the ${label} run (${rid}) ran on could not be read: 'gcloud run jobs describe ${shared_job}' exited non-zero and printed: $(last_lines "$SCRATCH/describe.err"). This row reads the service account that job runs as — the identity the service verified the caller as — and a read that did not answer is not a job that has none"
      return 0
    fi
  fi
  if ! printf '%s\n' "$named" | grep -q '|forward$'; then
    fact "served-$label" FAIL "the ${label} run (${rid}) of ${repo} was answered by the credentials service (${served} line(s) of its own) and NO line of it is a proxied MODEL call (purpose 'forward'): the purposes its lines name are $(printf '%s' "$named" | tr '\n' ' ' | sed 's/ *$//' | sed 's/^$/<none>/'). S5's subject is the model path: a shared job's run makes its model calls through this service, which is what the flip's dropped mounts leave as the only path, and a run whose model calls were answered somewhere else is this check's finding. Read 'fugaro runs show ${rid} --repo ${repo}' and the service's lines for it"
    return 0
  fi
  callers=$(audit_callers "$lines" "$rid" | tr '\n' ' ' | sed 's/ *$//')
  if [ -n "$sa" ] && ! audit_callers "$lines" "$rid" | grep -qxF -- "$sa"; then
    fact "served-$label" FAIL "the ${label} run (${rid}) of ${repo} ran on the shared job '${shared_job}' (service account '${sa}') and the service's lines for it name the caller(s) '${callers:-<none>}': the caller field is the identity the service VERIFIED (audit.go: AuditEvent.Caller is Identity.JobAccount, the account Cloud Run minted the job's ID token for), so a call served under another account is a credential served to something other than the shared job this run was launched in. Read the service's lines for this run ('gcloud logging read') and the job's own service account before anything else"
    return 0
  fi
  fact "served-$label" PASS "the service served the ${label} run (${rid}) of ${repo}: $(printf '%s\n' "$named" | grep -c .) credential|tier|purpose triple(s) named, at least one of them a proxied model call (purpose 'forward'), and the verified caller is the shared job's own service account ${sa:-<unread>} — the identity a caller cannot forge"
}

# step_outside: the boundary's refusal half — a repository OUTSIDE the profile is
# refused. It is measured where this installation refuses it: the launcher reads
# the repository's own fugaro.yaml, resolves the profile it selects, and refuses
# the launch when the profile's allow_repos does not cover the repository
# (profileAdmissionRefusal, the launcher's half of the run's own admission
# check). The refusal happens before Cloud Run, so nothing is claimed, no budget
# token is minted and no execution exists — which the row checks against the
# listing as well, since the sentence says so in its own words.
#
# What this row does NOT measure, and says so in its block: the SERVICE's own
# half of the same gate (pairShared's live read of the published node, which
# refuses a shared caller whose run identity names a repository the profile does
# not admit). A call of that class needs BOTH the job's Google ID token and a
# run identity, so it cannot be made from this machine at all — the check reads
# the installation's refusal of the LAUNCH, which is the same admission decided
# earlier and by the same policy, and the service's own refusal stays an owner's
# reading of the lines a forged call would leave.
step_outside() {
  local rid=$1 task rc=0 got sent n
  if [ -z "$OUTSIDE" ]; then
    unmeasured outside-profile "CHECK197_OUTSIDE is empty, so there is no repository outside the profile to refuse"
    return 0
  fi
  got=$(reader policy "$SCRATCH/policy.json" "$OUTSIDE")
  if [ "$(field_of proj_admits "$got")" != yes ]; then
    fact outside-profile FAIL "the published policy does not admit ${OUTSIDE} to the PROJECT either (its own allow_repos does not cover it), so a launch of it would be refused by the project's gate and not by the profile's — which is a different row of a different check. Name a sandbox repository the PROJECT admits and the profile '${PROFILE}' does not (its fugaro.yaml still name the profile: that selection is what puts the launch on the profile's gate at all)"
    return 0
  fi
  n=$(field_of profiles "$got")
  if [ "${n:-0}" -ge 1 ]; then
    fact outside-profile FAIL "${OUTSIDE} is admitted to ${n} profile(s) of the published policy ('$(field_of profile "$got")'), so it is not outside the profile '${PROFILE}' this check reads: a repository the profile admits is one the launcher launches, and its row would be a row about nothing. Name a sandbox repository whose fugaro.yaml selects the profile '${PROFILE}' and which that profile's own allow_repos does not admit, while the project's does"
    return 0
  fi
  task=$SCRATCH/task-outside.md
  task_write "$task" outside "$rid"
  say "launching the outside run (${rid}) in ${OUTSIDE} — the launcher is expected to refuse it before any execution exists"
  fugaro run --repo "$OUTSIDE" --project fugaro --run-id "$rid" \
    --title "Check 197: outside" \
    --task-file "$task" --json >"$SCRATCH/run-outside.json" 2>"$SCRATCH/run-outside.err" || rc=$?
  # The refusal is read from the WHOLE stderr file and sent is what is shown:
  # the launcher prints it as ONE line (profileAdmissionRefusal, then
  # safetext.PrintFailure's "fugaro:" prefix) of the profile's whole allow_repos
  # list, the remedy and the stop sentence last, and on a real sandbox that line
  # runs past the 400 characters last_lines cuts its display to. Reading the cut
  # as if it were the refusal would answer "the stop sentence is missing" for a
  # launch that was refused correctly, and the gate run would end 2.
  sent=$(last_lines "$SCRATCH/run-outside.err" 6)
  if [ "$rc" -eq 0 ]; then
    fact outside-profile FAIL "the launcher LAUNCHED a run of ${OUTSIDE} (run id ${rid}) — a repository outside the profile '${PROFILE}' — and reached Cloud Run with it: the profile's own allow_repos is the gate that says which repositories a shared job serves, and a repository outside it was admitted to the job anyway. Read 'fugaro runs ls --repo ${OUTSIDE}' and ${OUTSIDE}'s own fugaro.yaml (the profile it selects) before anything else"
    return 0
  fi
  if ! grep -q "the job profile \"${PROFILE}\" does not admit" "$SCRATCH/run-outside.err" 2>/dev/null; then
    # Not a FAIL: the launch was refused, and the sentence is not the profile's
    # own — it could be the project's gate, another launch's claim, a missing
    # checkout. Which gate answered is not this row's question, and a row that
    # read any refusal as the profile's would pass on a refusal by something
    # else entirely.
    unmeasured outside-profile "the launch of ${OUTSIDE} (run id ${rid}) was refused and the refusal does NOT carry the profile's own sentence ('the job profile \"${PROFILE}\" does not admit …'), so which gate refused it is not measured: 'fugaro run' exited ${rc} and printed: ${sent}. Read that sentence and ${OUTSIDE}'s own fugaro.yaml — the repository must SELECT the profile '${PROFILE}' for this row to be about the profile's admission gate at all"
    return 0
  fi
  if ! grep -q "The launch stops before any execution exists" "$SCRATCH/run-outside.err" 2>/dev/null; then
    unmeasured outside-profile "the launch of ${OUTSIDE} was refused with the profile's own sentence, and the refusal does NOT carry the launcher's own line that it stops before any execution exists: 'fugaro run' exited ${rc} and printed: ${sent}. Read it and run this check again"
    return 0
  fi
  # The sentence says no execution exists; the listing is asked as well, because
  # the sentence is a claim and the listing is the answer.
  if fugaro runs ls --repo "$OUTSIDE" --project fugaro --no-cache --json >"$SCRATCH/rows-outside.json" 2>/dev/null &&
    [ "$(field_of found "$(reader run "$SCRATCH/rows-outside.json" "$rid" || true)")" = yes ]; then
    fact outside-profile FAIL "the launcher refused the launch of ${OUTSIDE} (a repository outside the profile '${PROFILE}') and the run it refused (${rid}) IS in that repository's listing: the refusal says it stops before any execution exists, and a row for it is an execution that does. Read 'fugaro runs ls --repo ${OUTSIDE}' and the service's lines for that run before anything else"
    return 0
  fi
  fact outside-profile PASS "a launch of ${OUTSIDE} — a repository the profile '${PROFILE}' does not admit, while the project does — was refused by the launcher's own profile gate ('the job profile \"${PROFILE}\" does not admit …'; 'fugaro run' exited ${rc}) and left no row in that repository's listing, so no execution exists. **The SERVICE's own half of this gate is NOT measured here**: a shared caller's call needs both the job's Google ID token and a run identity, so it cannot be made from this machine — what is measured is the installation's refusal of the LAUNCH, decided by the same published policy (profileAdmissionRefusal)"
}

# step_bitbucket: the Bitbucket half, which the plan makes conditional on D2079
# ("a GitHub profile and (after D2079) a Bitbucket profile of proxied sandbox
# repositories"). A shared Bitbucket job exists only for a profile whose every
# repository the published block proxies, and it mounts no token. The three
# states in which that job is not on this installation — no repository named,
# the repository not proxied, and the job not deployed — are recorded as INFO
# and never as a FAIL or as anything unmeasured: the plan's own words put this
# half after D2079's T2.2, so a row about it is not a criterion of this release
# and a run that measured the GitHub half perfectly must be able to end 0 with
# this half reading that it is not deployed. As soon as the job IS there the
# rows below it are criteria like any other: the URL its runs need, the run
# itself and the service's own lines for it.
bitbucket_absent() { # bitbucket_absent "the reason, in this installation's own words"
  local label="bitbucket" why=$1
  fact "job-$label" INFO "${why}"
  fact "run-$label" INFO "${why}"
  fact "audited-$label" INFO "${why}"
  fact "served-$label" INFO "${why}"
}

step_bitbucket() {
  local label="bitbucket" got n bprofile
  if [ -z "$BITBUCKET" ]; then
    bitbucket_absent "CHECK197_BITBUCKET is empty, so no Bitbucket repository was named and this half was not measured: it reads a shared Bitbucket job and the runs it serves, and no installation has one before D2079's T2.2 (a shared Bitbucket job is deployed only for a profile whose every repository the published block proxies). Name one with CHECK197_BITBUCKET when the release carries it"
    return 0
  fi
  got=$(reader policy "$SCRATCH/policy.json" "$BITBUCKET")
  if [ "$(field_of bitbucket_admits "$got")" != yes ]; then
    bitbucket_absent "${BITBUCKET} is not in the published Bitbucket block's allow_repos, so this installation deploys no shared Bitbucket job for it (D2079's T2.2 deploys one only for a profile whose every repository the published block proxies): nothing was measured. Add it ('fugaro secrets allow ${BITBUCKET} <repository>'), publish and re-apply, then run this check again"
    return 0
  fi
  bprofile=$(field_of profile "$got")
  if [ "$bprofile" = - ] || [ -z "$bprofile" ]; then
    bitbucket_absent "${BITBUCKET} is proxied and no profile of the published policy admits it, so the shared job a run of it would land on cannot be named (D2079's T2.2 deploys one for a profile whose every repository the block proxies, and its own allow_repos must cover it): nothing was measured"
    return 0
  fi
  BB_PREFIX="fugaro-p-${bprofile}-bitbucket"
  n=$(field_of jobs "$(shared_of "$BB_PREFIX")")
  if [ "${n:-0}" -lt 1 ]; then
    bitbucket_absent "the installation deploys no shared job of the profile '${bprofile}' for Bitbucket (no job is named '${BB_PREFIX}…'), so this half was not measured: that job is D2079's T2.2 and is not on this release. Re-run this check after it lands"
    return 0
  fi
  if [ "$(field_of urls "$(shared_of "$BB_PREFIX")")" -lt "$n" ]; then
    fact "job-$label" FAIL "$(field_of urls "$(shared_of "$BB_PREFIX")") of ${n} shared Bitbucket job(s) of the profile '${bprofile}' carry FUGARO_CREDENTIALS_SERVICE_URL and the rest do not: a proxied shared job names the service, and one that does not reaches nothing. Re-apply ('fugaro update') and run this check again"
    return 0
  fi
  fact "job-$label" PASS "${n} shared Bitbucket job(s) of the profile '${bprofile}' carry FUGARO_CREDENTIALS_SERVICE_URL — $(printf '%s\n' "$(shared_of "$BB_PREFIX")" | sed -n 's/^job=\([^ ]*\) .*/\1/p' | tr '\n' ' ' | sed 's/ *$//') — so a run of ${BITBUCKET} reaches the service on the profile's own shared job (D2079: a shared Bitbucket job is deployed for a profile whose every repository the published block proxies, and it mounts no token)"
  step_run "$BITBUCKET" "$label"
}

# step_mounts: the flip's central row — no shared job of the profile mounts a
# model credential any more. After B5.7 the model path of a shared job's run is
# the credentials service and nothing else (SharedJobServedClasses: {"model"}),
# so a mount left on any job of the profile is the flip not having reached it.
# The provider keys are counted beside it and are NOT a finding: R6 keeps every
# provider route a run's stages go out on, so a job that still holds one is the
# design.
step_mounts() {
  local got n bad="" job mounts line
  if ! [ -f "$SCRATCH/jobs.json" ] && ! jobs_list; then
    fact no-model-mounts FAIL "gcloud run jobs list exited non-zero: $(last_lines "$SCRATCH/jobs.err"). Nothing about the mounts is settled by it"
    return 0
  fi
  got=$(shared_of "$SHARED_PREFIX")
  n=$(field_of jobs "$got")
  if [ "${n:-0}" -lt 1 ]; then
    unmeasured no-model-mounts "no job of the installation is named '${SHARED_PREFIX}…', so the profile '${PROFILE}' deploys no shared project job whose mounts could be read: run 'fugaro update' to deploy the profile's jobs and run this check again"
    return 0
  fi
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    case "$line" in
    'job='*' mounts=none '*) ;;
    'job='*) bad="${bad} ${line#job=};" ;;
    esac
  done < <(printf '%s\n' "$got" | grep '^job=' || true)
  if [ -n "$bad" ]; then
    fact no-model-mounts FAIL "${n} shared job(s) of the profile '${PROFILE}' are deployed and these still MOUNT the model credential — <name> <the model containers its env is fed from> <whether the credentials service's URL is in its env>:${bad:0:600} After the flip no shared job of a profile the service serves the model path of mounts one (B5.7: the mounts go when the class does, and R6 keeps only the provider keys). Re-apply ('fugaro update') and run this check again"
    return 0
  fi
  fact no-model-mounts PASS "${n} shared job(s) of the profile '${PROFILE}' are deployed and none mounts a model credential: no job's env is fed from the anthropic-api-key or claude-oauth-token container, and no job carries ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN (B5.7, R6)"
  local provider=0
  while IFS= read -r job; do
    [ -n "$job" ] || continue
    gcloud run jobs describe "$job" --project="$P" --region="$L" --format=json >"$SCRATCH/job-$job.json" 2>/dev/null || continue
    provider=$((provider + $(field_of providerkeys "$(reader job "$SCRATCH/job-$job.json")")))
  done < <(printf '%s\n' "$got" | sed -n 's/^job=\([^ ]*\) .*/\1/p')
  fact no-model-mounts-detail INFO "${provider} provider-key variable(s) are left on those jobs (R6): the flip takes the model credentials and keeps every provider route a repository's stages go out on, so a job that still holds one is the design and not a finding"
}

# step_token_mounts: the Bitbucket half of the flip's mount row — no shared job
# that reads as a Bitbucket one carries FUGARO_BITBUCKET_TOKEN (D2079: it mounts
# no token, which is the whole point of the shared Bitbucket job being proxied).
# The row is read over the WHOLE job list and not over one profile's jobs,
# because which profiles a Bitbucket job is deployed for is D2079's answer and
# not this check's: a token on ANY shared job is the finding. An installation
# whose list holds no Bitbucket shared job at all leaves the row unmeasured with
# that reason — the state of every installation until T2.2 lands.
step_token_mounts() {
  local got n
  if ! [ -f "$SCRATCH/jobs.json" ] && ! jobs_list; then
    unmeasured no-token-mounts "gcloud run jobs list exited non-zero: $(last_lines "$SCRATCH/jobs.err"), so the shared jobs' environments were not read"
    return 0
  fi
  got=$(shared_of "fugaro-p-")
  n=$(printf '%s\n' "$got" | grep -c ' git=bitbucket$' || true)
  if [ "${n:-0}" -lt 1 ]; then
    fact no-token-mounts INFO "the installation deploys no shared job whose name reads as a Bitbucket one, so there is no job this row could find a token on and nothing was measured: D2079's T2.2 (a shared Bitbucket job for an all-proxied profile, mounting no token) is what deploys one, and it is not on this release"
    return 0
  fi
  local bad token
  bad=""
  while IFS= read -r token; do
    [ -n "$token" ] || continue
    case "$token" in
    *' token=present '*) bad="${bad} ${token#job=};" ;;
    esac
  done < <(printf '%s\n' "$got" | grep ' git=bitbucket$' || true)
  if [ -n "$bad" ]; then
    fact no-token-mounts FAIL "${n} shared Bitbucket job(s) are deployed and these carry FUGARO_BITBUCKET_TOKEN — <name> <the model containers its env is fed from> <whether the service's URL is in its env>:${bad:0:600} D2079's answer is that a shared Bitbucket job mounts NO token: it serves every repository of its profile through the proxy, and the token's whole reach in a job serving several repositories is what the proxy exists to end. Re-apply ('fugaro update') and run this check again"
    return 0
  fi
  fact no-token-mounts PASS "${n} shared Bitbucket job(s) are deployed and none carries FUGARO_BITBUCKET_TOKEN (D2079)"
}

# step_inflight: U9 — a run that was ALREADY GOING when the profile's job was
# re-applied keeps the mount it was launched with and still finishes. Its
# execution was created before the re-apply, so its container spec is the one
# the old job template wrote, and the reading is exactly that: its own row's
# started_at is before the JOB's own updateTime (the moment `fugaro update`
# rewrote that job), its row is terminal and it succeeded, and the EXECUTION's
# own container spec — read off `gcloud run jobs executions describe`, the run's
# row naming the execution — still mounts the model credential while the job
# template it was created from no longer does. A run that did not finish kept
# nothing worth keeping; a run that started after the re-apply was launched by
# the template the flip wrote and is not the U9 case at all.
#
# The service's own lines for that run are NOT this row's reading, for the
# reason check193.sh gives for the same row: a run in flight across the re-apply
# is served by the service before and after it, so a reading that failed on a
# line of its own would fail every run that behaved correctly.
step_inflight() {
  local rid=$1 row started_at order got listed=$SCRATCH/jobs.json
  local execpath bare job spec mounts tmpl tmounted jobline
  if [ -z "$rid" ]; then
    unmeasured inflight-run "CHECK197_INFLIGHT is empty, so there is no run to read"
    unmeasured inflight-mount "CHECK197_INFLIGHT is empty, so there is no run whose mount could be read"
    return 0
  fi
  row=""
  if fugaro runs ls --repo "$REPO" --project fugaro --no-cache --json >"$SCRATCH/rows-inflight.json" 2>/dev/null; then
    row=$(reader run "$SCRATCH/rows-inflight.json" "$rid" || true)
  fi
  if [ -z "$row" ] || [ "$(field_of found "$row")" != yes ]; then
    unmeasured inflight-run "the run CHECK197_INFLIGHT names (${rid}) is not in the listing of ${REPO}: a run this check cannot find is one whose mount it cannot read. Read 'fugaro runs ls --repo ${REPO}' and name the id again"
    unmeasured inflight-mount "the run CHECK197_INFLIGHT names (${rid}) could not be found, so nothing about its mount is measured"
    return 0
  fi
  if [ "$(field_of terminal "$row")" != yes ]; then
    fact inflight-run FAIL "the run named by CHECK197_INFLIGHT (${rid}) had not reached a final status when this check read it: status=$(field_of status "$row"). U9 is about a run that was going ACROSS the re-apply, and this reading is taken after it has finished — wait for it to end ('fugaro runs show ${rid} --repo ${REPO}') and run this check again"
    unmeasured inflight-mount "the run ${rid} had not finished when this check read it, so the mount it kept is not measured"
    return 0
  fi
  job=$(job_of_row "$row")
  if [ -z "$job" ]; then
    unmeasured inflight-run "the row of the run ${rid} names no execution, so the job it ran on — and the moment that job was re-applied — cannot be read: read 'fugaro runs show ${rid} --repo ${REPO}' and name the id of a run whose record carries its execution"
    unmeasured inflight-mount "the run ${rid} names no execution, so the spec it was created with is not read"
    return 0
  fi
  if ! [ -f "$listed" ] && ! jobs_list; then
    unmeasured inflight-run "gcloud run jobs list exited non-zero: $(last_lines "$SCRATCH/jobs.err"), so the moment the job '${job}' was last updated was not read and the run cannot be placed against the re-apply"
    unmeasured inflight-mount "the job list could not be read, so the template the execution was created from is not read"
    return 0
  fi
  started_at=$(field_of started_at "$row")
  got=$(reader inflight "$listed" "$job" "$started_at")
  order=$(field_of order "$got")
  case "$order" in
  after)
    fact inflight-run FAIL "the run named by CHECK197_INFLIGHT (${rid}) was started at ${started_at:-unread}, which is not before the job '${job}' was last updated ($(field_of update "$got")): it was launched by the template the re-apply wrote, so it is not a run that was in flight ACROSS the re-apply and its mount says nothing about U9. Name the run that was already going when you ran 'fugaro update'"
    unmeasured inflight-mount "the run ${rid} started after the re-apply, so the mount it held is not the one U9 is about"
    return 0
    ;;
  unknown)
    unmeasured inflight-run "the run ${rid} started at ${started_at:-nothing readable} and this check could not read the time '${job}' was last updated ($(field_of update "$got")): whether it was going across the re-apply is not measured. Read 'gcloud run jobs describe ${job} --project=${P} --region=${L}' and the run's own row and run this check again"
    unmeasured inflight-mount "the run's start could not be placed against the re-apply, so its mount is not read"
    return 0
    ;;
  esac
  if [ "$(field_of status "$row")" != succeeded ]; then
    fact inflight-run FAIL "the run named by CHECK197_INFLIGHT (${rid}) started at ${started_at} — before the re-apply ($(field_of update "$got")) — and ended '$(field_of status "$row")' without opening a pull request: $(field_of halt_detail "$row")$(field_of reason "$row") (outcome=$(field_of outcome "$row")). An execution created before the re-apply keeps the container spec it was created with, which is the whole of U9, so the re-apply cannot have taken a credential out from under it: a shared job's run of that shape that could not finish is the re-apply (or the service the flipped job now depends on) having reached INTO a run that was already going"
    unmeasured inflight-mount "the run ${rid} did not finish, so nothing is read about the mount it kept"
    return 0
  fi
  fact inflight-run PASS "the run named by CHECK197_INFLIGHT (${rid}) started at ${started_at}, before the job '${job}' was last updated ($(field_of update "$got")), and still finished: status=$(field_of status "$row"), outcome=$(field_of outcome "$row"), pull request $(field_of pr "$row"), pushed=$(field_of pushed "$row")"
  # The mount the run kept is read off the EXECUTION's own container spec and
  # never off the service's lines: the run's row names the execution, and the
  # spec an execution was created with is frozen for its whole life (U9), so
  # the mount is either on it or it never had one.
  execpath=$(field_of execution "$row")
  bare=${execpath##*/}
  spec=$SCRATCH/execution.json
  if ! gcloud run jobs executions describe "$bare" --project="$P" --region="$L" --format=json >"$spec" 2>"$SCRATCH/execution.err"; then
    unmeasured inflight-mount "the execution ${execpath} of the run ${rid} could not be read: 'gcloud run jobs executions describe ${bare}' exited non-zero and printed: $(last_lines "$SCRATCH/execution.err"). U9's reading is the container spec that execution was created with, and a read that did not answer is not an execution with no mount"
    return 0
  fi
  got=$(reader job "$spec")
  mounts=$(field_of modelmounts "$got")
  tmpl=$(shared_of "fugaro-p-")
  tmounted=0
  while IFS= read -r jobline; do
    [ -n "$jobline" ] || continue
    case "$jobline" in
    "job=$job "*) case "$jobline" in *' mounts=none '*) ;; *) tmounted=$((tmounted + 1)) ;; esac ;;
    esac
  done < <(printf '%s\n' "$tmpl" | grep '^job=' || true)
  if [ "${tmounted:-0}" -ge 1 ]; then
    unmeasured inflight-mount "the execution ${execpath} of the run ${rid} carries ${mounts}, and the job '${job}' mounts a model credential too: with the template still handing the mount out, the mount on that execution is not the RETENTION U9 is about — the flip has not reached that job, which is the no-model-mounts row above"
    return 0
  fi
  if [ "$mounts" = none ]; then
    fact inflight-mount FAIL "the execution ${execpath} of the run ${rid} — created before the re-apply and finished all the same — carries NO model mount: $(field_of modelenvs "$got") is not among its container's variables, and the job template '${job}' mounts none either. An execution created before the re-apply keeps the container spec it was created with (U9), and this one's spec has no mount to keep — so it is not a run the mount-dropping re-apply was in flight across. Name a run that was already going when you re-applied the release that drops the mount"
    return 0
  fi
  fact inflight-mount PASS "the execution ${execpath} of the run ${rid} was created before the re-apply and its own container spec still mounts the model credential — $(field_of modelmounts "$got") — while the job template '${job}' mounts none: the execution kept the spec it was created with, which is U9's reading. The service's URL is on it too ($(field_of url "$got")), and the flip keeps it, so the service's own lines for this run are expected and are not read here"
}

# ---------------------------------------------------------------------- 197
run_197() {
  step_policy
  # A policy this check could not read, or one whose profile admits no shared
  # job, is a state whose rows below would be rows about nothing, and each run it
  # launches commits to a sandbox repository and opens a pull request: the rows
  # below read `not reached` rather than costing one.
  [ -z "$POLICY_STOP" ] || return 0
  step_switch
  [ -z "$SWITCH_STOP" ] || return 0
  step_run "$REPO" one
  step_outside "$(run_id outside)"
  step_bitbucket
}

# ---------------------------------------------------------------------- 197 after the flip
run_197_flip() {
  step_policy
  [ -z "$POLICY_STOP" ] || return 0
  # The mount rows are the flip's whole reading and are readable whatever the
  # switch says — a job with no mount is the state the flip exists to reach,
  # switched or not — so only the run rows wait for the switch.
  step_mounts
  step_token_mounts
  step_switch
  step_inflight "$INFLIGHT"
  [ -z "$SWITCH_STOP" ] || {
    unmeasured "run-flip-one" "the profile's shared job is not on the credentials-service switch (see the switch row), so no run was launched on it"
    unmeasured "audited-flip-one" "the profile's shared job is not on the credentials-service switch (see the switch row)"
    unmeasured "served-flip-one" "the profile's shared job is not on the credentials-service switch (see the switch row)"
    unmeasured "job-flip-one" "the profile's shared job is not on the credentials-service switch (see the switch row)"
    return 0
  }
  # The flipped job's own run: the mounts are gone, so the service is the ONLY
  # path its model calls have, and a run that finishes is the flip having left
  # the path intact.
  step_run "$REPO" flip-one
}

# print_plan: what a real run would run, in order. Printed by --dry-run, which
# runs NOTHING: no gcloud and no fugaro is called, so this mode is safe on a
# machine with no credentials at all.
print_plan() {
  say "dry run: NOTHING below was run, and no tool was called at all — not gcloud, not fugaro. A real run would first check that this is the fugaro-dev installation (fugaro doctor --project fugaro --all, and that fugaro version is not a development build), then:"
  say "  [network] fugaro project policy show --project fugaro --json  (the project's allow_repos and the one profile that admits ${REPO:-<the repository>}, and the budget mode, which must be one the gateway exists in, observe or enforce: no gateway is no path to the service)"
  say "  [network] gcloud run jobs list --project=${P} --region=${L} --format=json  (the job list: the jobs named 'fugaro-p-<profile>-github…' — one per account of the profile — and whether each carries FUGARO_CREDENTIALS_SERVICE_URL)"
  say "  [network, writes] fugaro run --repo ${REPO:-<the repository>} --project fugaro --run-id <today>-0197 --title 'Check 197: one' --task-file <task> --json  (one throwaway run: it writes one line to CHK197.md, commits and opens a pull request)"
  say "  [network] fugaro runs ls --repo <that repository> --no-cache --json  (its row, polled until terminal; the job is read out of the execution path its row names)"
  say "  [network] gcloud run jobs describe <that job>  (the job's own service account — the identity the service verifies a shared caller as)"
  say "  [network] gcloud logging read 'resource.type=\"cloud_run_revision\" resource.labels.service_name=\"fugaro-credentials\" jsonPayload.run=\"<run id>\"'  (credsvc: audit lines: the credential, its tier, the purpose, the job the run reported and the account the service VERIFIED — at least one of them a proxied model call, purpose forward)"
  say "  [network, writes] fugaro run --repo ${OUTSIDE:-<the repository outside the profile>} --project fugaro --run-id <today>-0199 --json  (expected to be REFUSED before any execution exists: the profile's own allow_repos does not admit it, and the refusal is the launcher's profile gate)"
  say "  [network] fugaro runs ls --repo <the repository outside the profile>  (asked as well: the refusal says no execution exists, and the listing is the answer)"
  if [ -n "$BITBUCKET" ]; then
    say "  [network, writes] the same three calls for ${BITBUCKET} (<today>-0198): the shared Bitbucket job, one per profile of an all-proxied one, mounts no token (D2079)"
  else
    say "  [skipped] the Bitbucket half: CHECK197_BITBUCKET is empty, so its rows read unmeasured — no shared Bitbucket job is deployed before D2079's T2.2"
  fi
  if [ "$MODE" = flip ]; then
    say "  [network] gcloud run jobs list … (the shared jobs of the profile: no ANTHROPIC_API_KEY, no CLAUDE_CODE_OAUTH_TOKEN, no env fed from anthropic-api-key or claude-oauth-token*, and no FUGARO_BITBUCKET_TOKEN on any job that reads as a Bitbucket one)"
    say "  [network] fugaro runs ls --repo ${REPO:-<the repository>} --no-cache --json  (the row of the run named by CHECK197_INFLIGHT: terminal, succeeded, started before the JOB's own updateTime, and the execution it names)"
    say "  [network] gcloud run jobs executions describe <that execution> --project=${P} --region=${L} --format=json  (its OWN container spec, which is the one it was created with: it still mounts the model credential, while the job template the flip wrote mounts none)"
    say "  [network, writes] one more run on the flipped shared job (<today>-019a): after the flip the service is the only path its model calls have, and it must still finish"
  fi
  say "and its cleanup would run: nothing. This check creates no cloud resource and writes no record: the only thing it makes is the scratch directory, which its own EXIT trap removes."
}

main() {
  need_tools
  scratch_up
  write_reader
  trap on_error ERR
  guard_xtrace
  guard_inputs

  # The dry run comes BEFORE the guard that reads the cloud (the installation's
  # own identity): a plan that called `fugaro doctor` would not be the thing this
  # mode claims to be — one you can run on a machine with no credentials.
  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "the plan above was printed and NOTHING was called: no gcloud, no fugaro. The guard that reads the cloud (the installation's own identity) runs only in a real run"
    return 0
  fi

  guard_project

  case "$MODE" in
  flip) run_197_flip ;;
  *) run_197 ;;
  esac
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs. The
# two modes print two DIFFERENT key sets — Check 197's first half has no mount
# and no in-flight run to read and its second half has no repository outside the
# profile to refuse — so a block from one is never read as a block from the
# other.
BLOCK_KEYS=(fugaro-version project-region repos xtrace scratch policy switch
  job-one run-one audited-one served-one outside-profile
  job-bitbucket run-bitbucket audited-bitbucket served-bitbucket
  restore scratch-removed)

BLOCK_KEYS_FLIP=(fugaro-version project-region repos xtrace scratch policy switch
  no-model-mounts no-model-mounts-detail no-token-mounts
  inflight-run inflight-mount
  job-flip-one run-flip-one audited-flip-one served-flip-one
  restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed="" keys
  if [ "$MODE" = flip ]; then keys=("${BLOCK_KEYS_FLIP[@]}"); else keys=("${BLOCK_KEYS[@]}"); fi
  printf '\nCHECK 197 RESULT\n' >&3
  printf 'MODE: %s\n' "$MODE" >&3
  for key in "${keys[@]}"; do
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
  printf 'CHECK 197 RESULT END (exit %s)\n' "$status" >&3
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed.
# This check deletes nothing, but the block that says what it read is printed
# from the EXIT trap, and a second Ctrl-C during the cleanup would kill this
# shell before it printed.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------------- inputs
# Hard-coded, never `gcloud config get-value project`: a check that reads the
# project from the environment can be pointed at another installation by a stray
# config, and this one must never be.
P=fugaro-dev
L=us-east5
DRY_RUN_CLI=""
MODE=switch
REPO=${CHECK197_REPO:-}
OUTSIDE=${CHECK197_OUTSIDE:-}
BITBUCKET=${CHECK197_BITBUCKET:-}
INFLIGHT=${CHECK197_INFLIGHT:-}
RUN_BASE=${CHECK197_RUN_ID:-$(date -u +%Y%m%d-%H%M%S)}
RUN_SECONDS=${CHECK197_RUN_SECONDS:-2400}
POLL_SECONDS=${CHECK197_POLL_SECONDS:-20}
LOG_WAIT_SECONDS=${CHECK197_LOG_WAIT_SECONDS:-60}
LOG_POLL_SECONDS=${CHECK197_LOG_POLL_SECONDS:-5}
LOG_AGE=${CHECK197_LOG_AGE:-1h}
LOG_LIMIT=${CHECK197_LOG_LIMIT:-200}
KEEP_SCRATCH=${CHECK197_KEEP_SCRATCH:-}
# Set by step_policy and step_switch when the state they read does not hold: the
# runs this check launches are not free (each commits to a sandbox repository and
# opens a pull request), so nothing is launched on a state whose rows below could
# only read `not reached`.
POLICY_STOP=""
SWITCH_STOP=""
PROFILE=""
PROVIDER=""
# The name prefix every shared job of the profile starts with: one job per
# (profile, provider, account) is deployed (gcp.ProjectJobName over
# config.ProjectJobAccounts), so the prefix catches the whole account axis, and
# the profile name is the policy's own (1 to 40 of a-z, 0-9 and '-',
# config.ProjectNameRE), which no sanitizing can move.
SHARED_PREFIX=""
BB_PREFIX=""

for arg in "$@"; do
  case "$arg" in
  --after-flip) MODE=flip ;;
  --dry-run) DRY_RUN_CLI=1 ;;
  -h | --help)
    # The header comment and nothing else: the range ends on the first line of
    # the script itself, which is filtered out with it — a help that printed
    # `set -Eeuo pipefail` under the usage would read as if that were part of it.
    sed -n '2,/^set -Eeuo/{/^#/p}' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *) say "unknown argument: $arg"; exit 2 ;;
  esac
done
DRY_RUN=${CHECK197_DRY_RUN:-$DRY_RUN_CLI}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
