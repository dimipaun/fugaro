#!/usr/bin/env bash
# Check 193 — an `api-key` and an `oauth` repository on the credentials-service
# switch, and after the flip the model mounts gone.
#
# It settles S2's two halves (plan B2.5, its "What it measures"; the
# reconciliation's **D2070**, **D2071**, **D2072**, **G16**, **G17**, **F29**
# and **R6**; UNVERIFIED **U9**). Until B2.7's flip every repository of an
# installation kept its mounts and the credentials service served the model
# path of an `api-key` or `oauth` repository BESIDE them — the flip (B2.7, in
# 0.26.0) is what took those two mounts away — so an offline reading
# settles nothing: whether the service is handed to a job at all, whether it
# serves that job the credential its own recorded auth names and no other,
# whether a Bitbucket repository published with `proxy: true` pushes through it,
# and — after the flip — whether the two mounts are really gone from the jobs
# while a run still finishes, are all answers of a live installation. So this
# check launches throwaway runs of TWO sandbox repositories on the switch and
# reads the service's OWN lines back: `credsvc: audit` for the acts, which
# names the credential it served and the tier it is of, and `credsvc: refused`
# for a refusal, which carries the sentence the boundary answers with. A FAIL
# returns to the owner as a decision: the two modes are B2.6 (the switch) and
# B2.8 (`--after-flip`), and the owner's ruling of 2026-11-18 runs both on the
# release that carries the flip rather than gating it on the first.
#
# Usage:      bash scripts/live-checks/check193.sh
#             bash scripts/live-checks/check193.sh --dry-run      (prints the
#             plan and calls NOTHING: no gcloud, no fugaro, no curl)
#             bash scripts/live-checks/check193.sh --after-flip   (Check 193's
#             second half: the mounts, and the run that was in flight across the
#             re-apply)
# Needs:      bash, mktemp, python3, gcloud (authenticated as you: read Cloud
#             Logging, read `run jobs` and one of its executions — the spec a
#             run in flight across the re-apply was created with — read the
#             credentials service's own IAM policy, which is where a job
#             account's invoker grant is read, and read the installation's
#             Realtime Database) and fugaro. Nothing is installed and nothing is
#             fetched.
# Where:      installation `fugaro` ONLY — project fugaro-dev, region us-east5.
#             Both are HARD-CODED in this file and never read from `gcloud
#             config get-value project`, and `fugaro doctor` must answer that
#             this is the fugaro-dev installation: a check that read the project
#             from the environment could be pointed at another installation by a
#             stray config, and this one must never be. Never belong.
# Creates:    two or three throwaway runs in the SANDBOX repositories you name
#             (a run of each, and — with --after-flip — a run of each again),
#             their branches and pull requests, which **stay on those
#             repositories**: this check deletes no branch and closes no pull
#             request, and their rows are yours to settle. It writes nothing
#             into any repository and nothing into the installation: the record
#             it reads (`creds/repoauth/<slug>`) is READ, never written — a
#             check that narrowed a record and failed to put it back would
#             refuse every run of that repository until you re-applied it, which
#             is not a thing a check may be able to do.
# Costs:      wall clock, not money: about 40 minutes per run at the terminal.
#             Every model call of those runs is answered by the credentials
#             service, which is the subject of the check.
# Runs:       by YOU. No agent runs any step of it, and no agent has: what it
#             measures is a live installation. Its own test (check193_test.sh)
#             drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else — this check creates no
#             cloud resource. It runs from the exit trap too, so a Ctrl-C leaves
#             nothing of the check's behind.
# Output:     progress on stderr, ONE block on stdout headed CHECK 193 RESULT,
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
# check reads is reduced to a short plain-word shape before it is printed. The
# one credential this check holds at all is the gcloud access token it mints to
# READ the installation's Realtime Database record, and it is used where it is
# minted.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK193_REPOS (REQUIRED: `<api-key repository>,<oauth repository>`, the two
# sandbox repositories on the credentials-service switch. The order is a
# convenience and not a claim: the check reads each repository's OWN recorded
# auth and says which is which, so a swapped order is not a finding);
# CHECK193_PROXY_REPO (optional: the Bitbucket one of the two published with
# `proxy: true`. Unset, the check reads both published policies and takes the
# one the node proxies); CHECK193_INFLIGHT (REQUIRED with --after-flip: the id
# of the run of one of the two repositories that was already going when you
# re-applied that repository); CHECK193_RUN_SECONDS (how long a run is waited
# for; default 2400), CHECK193_POLL_SECONDS, CHECK193_LOG_WAIT_SECONDS,
# CHECK193_LOG_POLL_SECONDS, CHECK193_LOG_AGE, CHECK193_LOG_LIMIT,
# CHECK193_CONFIG (the installation's local config file, read for
# budget.rtdb_url), CHECK193_DRY_RUN and CHECK193_KEEP_SCRATCH. None of them can
# point this check at another project or another region: those two are constants
# in this file.
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
need() { command -v "$1" >/dev/null 2>&1 || die "Check 193 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  need bash
  need mktemp
  need python3 "every gcloud and fugaro document below is read by the reader in the scratch directory"
  need gcloud
  need fugaro
  need curl "the installation's recorded auth is read out of its own Realtime Database"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk193-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
PRINTED=""
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
AT=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk193.XXXXXXXX") || die "Check 193 cannot make a scratch directory"
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
    # The knob docs/secrets-brokering/check193-steps.md tells the owner to set to
    # read what the check wrote is not a directory that could not be removed:
    # the row below says which of the two it is, and only the second is a FAIL.
    SCRATCH_KEPT=yes
    say "keeping $SCRATCH (CHECK193_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created, undone. There is nothing of this check's in
# the installation or the project — the runs it launches live on the sandbox
# repositories and their rows are the owner's to settle, exactly as Check 191's
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
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK193_KEEP_SCRATCH is set), at $SCRATCH"
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

last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}

# write_reader installs the one file that reads every document this check takes
# — the service's own log lines, a run's row, the published policy, a job's own
# spec and the installation's recorded auth — so that no reading of a JSON shape
# is spelled twice in this script and none of them is done in bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 193's reader: one file, one reading per subcommand, no credential in
any of them.

audit  argv: the service's lines' file, a run id. Two kinds of line for that
       run, and nothing else: `audit=<status>|<credential>|<tier>|<purpose>|
       <job>|<caller>` for each `credsvc: audit` line — the status is `ok` for a
       call the service served and a refusal's status otherwise, and the
       credential and tier are the NAME and the TIER of the credential it used,
       never a value — and `refused=<status>|<sentence>` for each of the
       service's OWN refusal lines (`credsvc: refused`), whose `message` field is
       the sentence the run was sent. That sentence is the boundary's own text
       (internal/credsvc's narrowToRepoAuth) and it is the only place a
       repository-scoped refusal says WHAT it refused for.
run    argv: the listing's file, a run id. Prints that run's row field by field,
       `found=no` when the listing does not hold it, the execution the run's own
       record names (`projects/<project>/locations/<region>/jobs/<job>/
       executions/<name>`, internal/backend's Execution.Name) and the halt's own
       reason and detail for a run the record halted.
policy argv: the `project policy show --json` document, a repository. Prints
       whether the published Bitbucket block admits that repository
       (`bitbucket_token.allow_repos`), whether it is proxied
       (`bitbucket_token.proxy`), and the budget mode in force (`budget.mode`,
       D2071). An entry is matched the way the service matches a repository
       (projectpolicy.MatchRepo): it carries the host in front of the path
       (`bitbucket/acme/web`) and a segment may be a wildcard.
auth   argv: the Realtime Database record's file, a repository. Prints the auth
       the record names, and — under `oauth` — the accounts it names, which is
       the whole of what the service narrows a repository's calls to
       (creds.RepoAuth).
job    argv: one `gcloud run jobs describe --format=json` document (or one
       execution's). Prints the job's name, the MODEL secret containers its
       container mounts, the model env variable names it carries, how many
       provider-key variables it carries, and whether the credentials service's
       URL is in its env at all. The URL is there on an EXECUTION — the
       launcher writes it on each launch (internal/cli/run.go's
       credentialsServiceLaunchEnv) — and on a SHARED project job, whose
       template bakes it (internal/infra's projectJobEnv), and on NO
       per-repository job template: a repository's own job is told the address
       per launch, so a reading that required the URL of one answers "off the
       switch" for every switched repository there is.
jobs   argv: `gcloud run jobs list --format=json`'s file, a repository. Prints
       one line per job of that repository (matched by the job's own
       FUGARO_REMOTE, and by the label the installation writes — gcloud's
       `run jobs list` forces the v1 API, so a container and its account sit at
       spec.template.spec.template.spec) — the model containers its env is fed
       from, the model env names it carries, whether the URL is in its env, and
       the ACCOUNT its containers run as.
switch argv: the same job list's file, a repository, the credentials
       service's own `gcloud run services get-iam-policy --format=json`
       document, and the auth that repository's own record names
       (`api-key`, `oauth` or `cascade`; the record row reads it). Prints
       `found=yes` when that document is a policy at all, one
       `job=<name> on=<yes|no> why=<word> account=<email>` line per job of the
       repository — the per-repository credentials-service switch, read off the
       job TEMPLATE, the service's IAM policy and that auth (there is no URL on
       a per-repository job: see `job` above) — and the totals `jobs=`, `on=`,
       `off=`. `on=yes` requires BOTH that the job no longer holds the model
       credential the service serves for this repository's auth (mounts_of and
       model_envs_of both empty, D2072) and that the job's account is a member
       of a roles/run.invoker binding on that service (modules/workflow/sa.tf's
       google_cloud_run_v2_service_iam_member.credentials, planned exactly when
       the repository root's credentials_service is non-null — the same switch
       infra.CredentialsServiceServes answers the launcher with, which is what
       puts the address on each run's execution).
       The ONE model mount a switched job keeps is a mixed stage account's own
       CLAUDE_CODE_OAUTH_TOKEN (dropModelMounts' anthropic arm,
       config.IsServiceHeldModelEnv): the mix is `api-key`'s alone
       (mixedStageAccountJob), so a job of an API-KEY repository whose only
       model mount is the account's token is that variant and is `on=yes
       why=stage-account`, while the same mount on an `oauth` repository is the
       credential the flip takes away and is `on=no`. R12's
       FUGARO_STAGE_ACCOUNT_SERVED is NOT read: it says whether the service
       SELECTS the stage's account, and the mount stays whether it is set or
       not.
       A job whose template carries the URL is `on=yes why=url` on that alone:
       that is the shared project job's shape, where the address is baked.
slug   argv: the same job list's file, a repository. Prints the key that
       repository's creds/repoauth record is written under, which is the
       `fugaro_repo` label of its own jobs — the repository's SLUG
       (`<readable>-<16 hex>`, task.Slug) and never its name: the record is
       keyed by the slug, and a path spelled from `owner/name` is a node nobody
       writes.
"""
import datetime
import json
import re
import sys

WORD = re.compile(r"[^A-Za-z0-9._:@/+-]")

# MODEL_ENVS are the job variables a model credential is mounted as
# (config.ModelSecretEnvs): the two the flip takes off a switched repository.
MODEL_ENVS = ("ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN")
# MODEL_SECRETS are the Secret Manager containers those variables are fed from
# (config's reserved secret names: the paid key, and one container per Claude
# account). A job that mounts one of them holds the credential itself, whether
# or not the env name above is the one it reads it under — so both spellings
# are read, and a job whose container is named is one the flip has not reached.
MODEL_SECRETS = ("anthropic-api-key", "claude-oauth-token")
# A derived Secret Manager ID ends in the hash gcp.SecretID puts on it
# (internal/backend/gcp/names.go: secretHashHex, 16 hex of sha256), which is
# what tells the logical name the ID was built for from the slug in front of
# it.
SECRET_HASH_RE = re.compile(r"-[0-9a-f]{16}$")

# The credentials service's URL, the role its IAM policy grants a job's account
# (modules/workflow/sa.tf), and the one auth whose repository may build a mixed
# stage account's own job — which is the one arm in which a switched job keeps a
# model mount at all (config.IsServiceHeldModelEnv's anthropic arm,
# mixedStageAccountJob).
SERVICE_URL_ENV = "FUGARO_CREDENTIALS_SERVICE_URL"
INVOKER_ROLE = "roles/run.invoker"
API_KEY_AUTH = "api-key"


def word(v, cap=80):
    return WORD.sub("", str(v))[:cap] or "-"


def text(v, cap=300):
    """Free text, for what the check PRINTS: a sentence keeps its spaces, and
    only what the block's own shape cannot carry is taken out — the field
    separator, a newline and any other control character."""
    return " ".join(str(v).split())[:cap].replace("|", "/") or "-"


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None


def rows(path):
    doc = load(path)
    if isinstance(doc, dict):
        for key in ("runs", "Runs"):
            if isinstance(doc.get(key), list):
                return doc[key]
        return []
    return doc if isinstance(doc, list) else []


def env(spec):
    out = {}
    for e in (spec.get("env") or []) if isinstance(spec, dict) else []:
        if isinstance(e, dict) and e.get("name"):
            out[e["name"]] = e
    return out


def bare_member(m):
    """An IAM member with its type prefix dropped (`serviceAccount:`, `user:`,
    `group:` or `domain:`) — and with nothing else touched. The account a job's
    containers run as is the bare address, while a binding's member is the same
    address with its type in front of it, so the two are brought to one spelling
    here: a reading that compared them as they are written would find no invoker
    grant for any job and would call every switched repository off the switch.
    A prefix outside that set (`deleted:`, a `principalSet:` URL) is left alone:
    none of them is an account, and a member that is not one must not become
    equal to one by a rewrite."""
    s = str(m)
    for prefix in ("serviceAccount:", "user:", "group:", "domain:"):
        if s.startswith(prefix):
            return s[len(prefix):]
    return s


def invoker_emails(doc):
    """The accounts the credentials service's own IAM policy admits as callers:
    the members of every binding whose role is roles/run.invoker — one member
    per repository root applied with the switch on (modules/workflow/sa.tf's
    google_cloud_run_v2_service_iam_member.credentials), and no other role: a
    reader that swept every binding would read a secretAccessor as an invoker
    grant."""
    out = set()
    for b in doc.get("bindings") or []:
        if not isinstance(b, dict) or (b.get("role") or "") != INVOKER_ROLE:
            continue
        for m in b.get("members") or []:
            out.add(bare_member(m))
    return out


def account_of(doc):
    spec = spec_of(doc)
    return ((spec.get("serviceAccountName") if isinstance(spec, dict) else "") or "")


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
    are read together: an env whose valueSource names one of the model secrets
    is a mount, whatever the variable is called, and a variable named
    ANTHROPIC_API_KEY that carries a plain value is one too (the installation's
    own `--set-env-vars` does not exist, but a reading that only looked at
    secretKeyRef would call a job unmounted on the strength of where the value
    came from)."""
    out = []
    for c in containers:
        for name, e in env(c).items():
            src = (e.get("valueSource") or {})
            ref = ((src.get("secretKeyRef") or {}).get("secret")
                   or (src.get("secretKeyRef") or {}).get("name") or "")
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


def jobs_of(path, repo):
    """The jobs of one repository, as (name, containers, updateTime, label,
    account) — matched by the repository's own FUGARO_REMOTE first, and by the
    label the installation writes (`fugaro_repo: <slug>-<16 hex>`) where no
    remote is readable, because a reading that leaned on the label alone would
    miss a job whose label the installation had not written and one that leaned
    on the remote alone would miss a job whose env could not be read back. The
    label is the repository's own slug, which is the key its creds/repoauth
    record is written under (label_of), so it is returned with the rest; the
    account is the one its containers run as, which is the member the service's
    invoker grant names."""
    slug = repo.lower().replace("/", "-")
    out = []
    for job in load(path) or []:
        if not isinstance(job, dict):
            continue
        name = ((job.get("metadata") or {}).get("name")) or ""
        if not name:
            continue
        containers = containers_of(job)
        label = ((job.get("metadata") or {}).get("labels") or {}).get("fugaro_repo") or ""
        owns = (label.startswith(slug + "-")
                and re.fullmatch(r"[0-9a-f]{16}", label[len(slug) + 1:]) is not None)
        remote = ""
        for c in containers:
            remote = (env(c).get("FUGARO_REMOTE") or {}).get("value") or remote
        if not owns and remote != "https://" + repo + ".git" and not remote.endswith("/" + repo + ".git"):
            continue
        out.append((name, containers, ((job.get("metadata") or {}).get("updateTime")) or "", label,
                    account_of(job)))
    return out


def label_of(path, repo):
    """The key `fugaro update --repo` writes this repository's creds/repoauth
    record under.

    The record is keyed by the repository's SLUG and never by its name: the slug
    is the readable path plus "-" and 16 hex of sha256(provider NUL canonical
    path) (task.Slug: "acme/app" becomes "acme-app-0123456789abcdef"), which is
    also what the installation writes as its jobs' `fugaro_repo` label — so the
    label IS the key. A path spelled out of `owner/name` is a node nobody
    writes: the GET answers 404 on every installation, and a check reading one
    would call a record that is there "not there" and measure nothing."""
    for _name, _containers, _up, label, _account in jobs_of(path, repo):
        if re.fullmatch(r"[a-z0-9][a-z0-9-]*-[0-9a-f]{16}", label):
            return label
    return ""


def parse_time(v):
    """An RFC 3339 stamp as an aware datetime, or None. Both shapes this
    check compares are UTC — a run record's started_at (runstore: RFC 3339 in
    UTC) and a Cloud Run job's metadata.updateTime — so the comparison is real
    arithmetic on the two instants and never a string comparison that would be
    wrong the moment one of them carried an offset."""
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
                word(p.get("job"), 80),
                word(p.get("caller"), 120)))
        elif msg == "credsvc: refused":
            print("refused=%s|%s" % (word(p.get("status"), 40), text(p.get("message"), 300)))
elif cmd == "run":
    want = sys.argv[3]
    for row in rows(sys.argv[2]):
        if not isinstance(row, dict) or row.get("run_id") != want:
            continue
        print("found=yes")
        for key in ("run_id", "status", "terminal", "outcome", "pushed", "pr", "repo",
                    "started_at", "execution", "oauth_account"):
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
    # argv: the `fugaro project policy show --json` document, a repository. The
    # two facts F29's row is read from are the published Bitbucket block's own
    # admission of the repository and its proxy switch (projectpolicy's
    # BitbucketToken: allow_repos, proxy) — the same two check191 reads, because
    # they are the same switch — and the budget mode is D2071's third leg.
    repo = sys.argv[3]
    doc = load(sys.argv[2]) or {}
    policy = doc.get("policy") if isinstance(doc, dict) else None
    if not isinstance(policy, dict):
        policy = {}
    block = policy.get("bitbucket_token")
    block = block if isinstance(block, dict) else {}
    allow = block.get("allow_repos") or []
    print("admits=%s" % ("yes" if any(inside(e, repo) for e in allow if isinstance(e, str)) else "no"))
    print("proxy=%s" % ("yes" if block.get("proxy") is True else "no"))
    budget = policy.get("budget")
    budget = budget if isinstance(budget, dict) else {}
    print("mode=%s" % word(budget.get("mode"), 20))
elif cmd == "auth":
    repo = sys.argv[3]
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        print("auth=%s" % word(doc.get("auth"), 20))
        print("version=%s" % word(doc.get("version"), 8))
        accounts = doc.get("accounts") or []
        print("accounts=%s" % len(accounts))
        for a in accounts:
            print("account=%s" % word(a, 80))
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
    url = "absent"
    for c in containers:
        if "FUGARO_CREDENTIALS_SERVICE_URL" in env(c):
            url = "present"
    print("url=%s" % url)
elif cmd == "jobs":
    repo = sys.argv[3]
    n = 0
    mounted = 0
    urls = 0
    for name, containers, _up, _label, account in jobs_of(sys.argv[2], repo):
        n += 1
        mounts = mounts_of(containers)
        if mounts:
            mounted += 1
        url = any(SERVICE_URL_ENV in env(c) for c in containers)
        if url:
            urls += 1
        print("job=%s mounts=%s url=%s account=%s" % (
            word(name, 120), ",".join(mounts) if mounts else "none",
            "present" if url else "absent", word(account, 160) if account else "-"))
    print("jobs=%s" % n)
    print("mounted=%s" % mounted)
    print("urls=%s" % urls)
elif cmd == "switch":
    # argv: the job list's file, a repository, the service's IAM policy
    # document, and the auth that repository's own record names. The
    # per-repository switch, read off each job's own TEMPLATE, the service's own
    # policy and that auth (see the docstring's `switch`).
    doc = load(sys.argv[4])
    auth = sys.argv[5] if len(sys.argv) > 5 else ""
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        invokers = invoker_emails(doc)
        n = on = off = 0
        for name, containers, _up, _label, account in jobs_of(sys.argv[2], sys.argv[3]):
            n += 1
            mounts = mounts_of(containers)
            envs = model_envs_of(containers)
            # D2072's is_stage_account_token: the one mount that STAYS on a
            # switched job is a mixed stage account's own Claude token, which is
            # the stage's credential and not the run's. The mix is `api-key`'s
            # alone (mixedStageAccountJob reads the repository's auth and
            # answers false for every other: under `oauth` the key keeps the
            # meaning the accounts design gives it, and the account's token is
            # the run's own, served like every other), so the auth is what
            # decides it — the mount alone cannot, because the same single token
            # is a correct stage variant on an `api-key` repository and the
            # credential the flip takes away on an `oauth` one. R12's
            # FUGARO_STAGE_ACCOUNT_SERVED is deliberately not read: it is
            # whether the SERVICE selects that account, which is a different
            # fact, and the mount stays whether it is set or not.
            stage_only = (auth == API_KEY_AUTH
                          and envs == ["CLAUDE_CODE_OAUTH_TOKEN"]
                          and all("claude-oauth-token" in m for m in mounts))
            if any(SERVICE_URL_ENV in env(c) for c in containers):
                unmounted, why = True, "url"
            elif not mounts and not envs:
                unmounted, why = True, "no-mount"
            elif stage_only:
                unmounted, why = True, "stage-account"
            else:
                unmounted, why = False, "mounts:" + ",".join(mounts or envs)
            holds = bool(account) and account in invokers
            if not unmounted:
                why = "%s+%s" % (why, "invoker" if holds else "no-invoker")
            elif not holds:
                why = "%s+no-invoker" % why
            if unmounted and holds:
                on += 1
                print("job=%s on=yes why=%s account=%s" % (
                    word(name, 120), why, word(account, 160)))
            else:
                off += 1
                print("job=%s on=no why=%s account=%s" % (
                    word(name, 120), why, word(account, 160) if account else "-"))
        print("jobs=%s" % n)
        print("on=%s" % on)
        print("off=%s" % off)
elif cmd == "slug":
    # argv: the job list's file, a repository. Prints the key the repository's
    # creds/repoauth record is written under — the `fugaro_repo` label of its
    # own jobs, which is the repository's slug and not its name (label_of, and
    # the record is keyed by the slug: creds.PathRepoAuth).
    print("slug=%s" % word(label_of(sys.argv[2], sys.argv[3]), 100))
elif cmd == "inflight":
    # argv: the job list's file, a repository, a run's started_at. Prints the
    # repository's LATEST job updateTime and whether the run started before it.
    # `fugaro update --repo` re-applies the repository and updates every job of
    # it, so the latest updateTime among them is the moment the re-apply
    # reached that repository — which is the instant the run has to be on the
    # far side of for U9 to be about it at all.
    repo, started = sys.argv[3], sys.argv[4]
    best = None
    bestname = "-"
    for name, _containers, up, _label, _account in jobs_of(sys.argv[2], repo):
        d = parse_time(up)
        if d is not None and (best is None or d > best):
            best, bestname = d, name
    print("job=%s" % word(bestname, 120))
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check193.sh -- the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the access token or a command's environment"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`.
  # Check 193 is a reading on ONE installation: run anywhere else and every line
  # below is about a service, a record and repositories of another project,
  # which is why this stops rather than warns.
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
    fact "fugaro-version" FAIL "$v — a development build is not the release this check is about: B2.5's check runs on the release that carries B2.1–B2.5, and a checkout build resolves no released credentials-service image to deploy (internal/cli's resolveCredentialsImage). Install the release under test and run it again"
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
  esac
  owner=${repo%%/*}
  case "$owner" in
  belong) stop "$repo names the owner's own workspace ($owner): this check runs against sandbox repositories of the fugaro-dev installation and nothing else, never belong" ;;
  esac
}

guard_inputs() {
  [ -n "$REPOS_RAW" ] || stop "CHECK193_REPOS must name the two sandbox repositories on the credentials-service switch, as <api-key repository>,<oauth repository>"
  local a b rest
  a=${REPOS_RAW%%,*}
  rest=${REPOS_RAW#*,}
  if [ "$rest" = "$REPOS_RAW" ]; then
    stop "CHECK193_REPOS must name TWO repositories separated by a comma and it names one ('${REPOS_RAW}'): the check is about an api-key repository and an oauth repository, and one of them alone settles neither"
  fi
  b=${rest%%,*}
  case "$rest" in
  *,*) stop "CHECK193_REPOS names more than two repositories ('${REPOS_RAW}'): exactly two sandbox repositories are read, one whose recorded auth is api-key and one whose is oauth" ;;
  esac
  a=$(printf '%s' "$a" | tr -d '[:space:]')
  b=$(printf '%s' "$b" | tr -d '[:space:]')
  guard_one_repo "$a" CHECK193_REPOS
  guard_one_repo "$b" CHECK193_REPOS
  [ "$a" != "$b" ] || stop "CHECK193_REPOS names ${a} twice: the api-key repository and the oauth repository are two different repositories"
  REPO_ONE=$a
  REPO_TWO=$b
  [ -n "$PROXY_REPO" ] && guard_one_repo "$PROXY_REPO" CHECK193_PROXY_REPO
  case "$MODE" in
  flip)
    [ -n "$INFLIGHT" ] || stop "CHECK193_INFLIGHT must name the run of one of the two repositories that was already going when you re-applied it (<run id>, the shape 'fugaro runs ls' prints): U9's reading is that THAT run kept its mount and still finished, and a run this check picked itself could be one that ended hours before the re-apply"
    ;;
  esac
  fact repos PASS "$(printf '%s and %s' "$REPO_ONE" "$REPO_TWO") (the two sandbox repositories on the switch; never belong)"
}

# ------------------------------------------------------------- the run helpers
# run_id LABEL: the id the LABEL run is launched under. `fugaro run` admits one
# shape and only one (task.ValidRunID: ^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$), so the
# base — today's UTC date and a time — is followed by four hex characters, one
# value per label, which is what tells this check's runs apart in the listing
# and in the service's own lines.
run_id() {
  case "$1" in
  one) printf '%s-0193' "$RUN_BASE" ;;
  two) printf '%s-0194' "$RUN_BASE" ;;
  flip-one) printf '%s-0195' "$RUN_BASE" ;;
  flip-two) printf '%s-0196' "$RUN_BASE" ;;
  *) die "Check 193 has no run id for the label '$1'" ;;
  esac
}

# task_write FILE REPO LABEL RID: the throwaway task. Its first line is the
# `# Task:` line `fugaro watch` and the pull request title come from, and what
# it asks for is the smallest thing that makes a run commit and open a pull
# request — which is what puts its model calls through the credentials service,
# the subject of this check. It is written by the FILE argument and never by a
# here-document on a command line (CLAUDE.md: an unquoted heredoc runs any
# backticked text).
task_write() { # task_write FILE REPO LABEL RID
  {
    printf '# Task: Check 193 — the %s run (%s)\n\n' "$3" "$4"
    printf 'Add one line to `CHK193.md` at the repository root:\n\n'
    printf '    check 193 %s %s\n\n' "$3" "$4"
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
    --title "Check 193: ${label}" \
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
    printf '%s' "the ${label} run (${rid}) had not reached a final status after ${waited}s: status=$(field_of status "$row"). A row still in flight is not an answer about the credentials service — raise CHECK193_RUN_SECONDS, or read that run's row yourself" >"$SCRATCH/launch-err"
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
# line as the answer it is looking for. The two rows that live off an absent
# line are the flip's U9 row (`inflight-mount`: no line of the run's own is
# exactly what U9 says) and, in the switch mode, the two `served-*` rows, so a
# failed read taken for silence would PASS the check on nothing. check-c39.sh
# (sign-line) and check420.sh/check421.sh (pool) each report their own failed
# log and policy reads as unmeasured for the same reason.
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

# audit_named FILE RID: the `credential|tier` pairs of the run's served lines,
# one per line. The credential is a NAME — anthropic-api-key for the paid key,
# and an ACCOUNT for a Claude subscription credential (`default` for the
# repository's default account, the bare account name for a named one: that is
# the name internal/credsvc's bundle holds the token's credential under) — and
# the tier is what it is of, which is how a
# row says WHICH credential answered without ever touching its value.
audit_named() {
  reader audit "$1" "$2" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|.*/\1|\2/p' | sort -u
}

# record_key REPO: the key this repository's record is written under — the
# `fugaro_repo` label of its own jobs, which is the repository's SLUG
# (`<readable>-<16 hex>`, task.Slug) and NOT its name. `creds/repoauth` is keyed
# by the slug (creds.PathRepoAuth, written by `fugaro update --repo` from the
# spec it applied), so a path spelled out of `owner/name` is a node nobody
# writes: the GET would answer 404 on every installation, and this check would
# report a record that is there as one that is not.
record_key() { # record_key REPO
  reader slug "$SCRATCH/jobs.json" "$1" | sed -n 's/^slug=//p'
}

# record_accounts REPO: the account names the repository's own record names, one
# per line (creds.RepoAuth.Accounts). A tier-A audit line names the CREDENTIAL'S
# own name, and a Claude account credential's name IS the account — `default`
# for the unsuffixed claude-oauth-token and the bare name for
# claude-oauth-token-<account> (internal/credsvc's bundle loader returns the
# account as the credential's name, and narrowToRepoAuth keeps
# `tier A && Name == the declared account`) — never the secret container's name.
record_accounts() { # record_accounts REPO
  reader auth "$SCRATCH/auth-$(record_key "$1").json" "$1" | sed -n 's/^account=//p'
}

# auth_record SLUG OUT: the repository's recorded auth, read from the
# installation's own Realtime Database at the path `fugaro update` writes it to
# (creds.PathRepoAuth, D2070 (a)), keyed by the repository's SLUG and not its
# name (record_key). It is READ and never written: the record is the boundary
# this check exists to confirm, and a check that rewrote one and failed to put
# it back would refuse every run of that repository.
#
# On stdout it prints the REASON it could not read the record, and the caller is
# what puts it in the block: the body curl wrote to OUT is a database answer and
# never a diagnosis (a 404's body is `null`, a 000's file is empty), so a caller
# that built the row out of OUT would print `FAIL — null` and the reason would
# land above the block, outside it.
auth_record() { # auth_record SLUG OUT
  local slug=$1 out=$2 code
  code=$(curl -sS -o "$out" -w '%{http_code}' --max-time 30 \
    -H "Authorization: Bearer ${AT}" \
    "${RTDB%/}/creds/repoauth/${slug}.json" 2>"$SCRATCH/rtdb.err") || code=000
  case "$code" in
  200) return 0 ;;
  404)
    printf 'the record is not there (HTTP 404): this repository has no creds/repoauth/%s node, which is the state of an installation whose "fugaro update --repo" has not run since the release that writes one. Run it in that repository and this check again' "$slug"
    return 1
    ;;
  000) printf 'the record could not be read: %s' "$(last_lines "$SCRATCH/rtdb.err")" ;;
  *) printf 'the record could not be read (HTTP %s)' "$code" ;;
  esac
  printf ' at %s/creds/repoauth/%s.json' "$RTDB" "$slug"
  return 1
}

# ------------------------------------------------------------------ the steps
# step_jobs: the installation's own job list, which the record row and the
# switch row both stand on — the record row reads the slug each repository's jobs
# carry (record_key) and the switch row reads the jobs themselves. It is a step
# of its own, and the first of the two, because the switch now needs the auth the
# record row assigns (step_switch below).
step_jobs() {
  gcloud run jobs list --project="$P" --region="$L" --format=json >"$SCRATCH/jobs.json" 2>"$SCRATCH/jobs.err" || {
    fact switch FAIL "gcloud run jobs list exited non-zero: $(last_lines "$SCRATCH/jobs.err"). Nothing about the switch is settled by it"
    SWITCH_STOP=yes
  }
}

# repo_auth REPO: the auth that repository's own record names, as the record row
# left it. This reads the row's own answer and never decides one: a repository
# the record row could not assign has no auth here, and the switch row is what
# says so.
repo_auth() { # repo_auth REPO
  if [ "$1" = "$API_REPO" ]; then printf 'api-key'
  elif [ "$1" = "$OAUTH_REPO" ]; then printf 'oauth'
  fi
}

# step_switch: each of the two repositories is ON the credentials-service
# switch, and the block reads that with D2071's third leg (B2.3): the published
# budget mode is one the gateway exists in — `observe` or `enforce` (off is not
# a mode any more, D3009: the mode is read at `update` and a job that holds no
# mount is only reachable while the gateway exists). A repository off the switch
# would make every row below a row about nothing.
#
# WHAT A SWITCHED PER-REPOSITORY JOB REALLY CARRIES, and therefore what this row
# reads: NOT the service's URL. The launcher writes that on each run's
# EXECUTION (internal/cli/run.go's credentialsServiceLaunchEnv, and the plan's
# own words in docs/credentials-service/runbook.md put it there), no module
# under deploy/terraform/gcp/modules/workflow bakes it into the job template,
# and a row that asked the template for it answered "OFF the switch" for every
# switched repository there is — which is what it did before this reading. The
# template does carry the switch's own effects (internal/infra's RepoSpec, whose
# var.credentials_service is null for a repository the switch holds off), and
# the two this row reads per repository are:
#
#   * the model credential is GONE from the job (dropModelMounts, D2072/G17) —
#     the one exception being a mixed stage account's own CLAUDE_CODE_OAUTH_TOKEN,
#     which is the STAGE's credential and not the run's (config.IsServiceHeldModelEnv's
#     anthropic arm);
#   * the job's own account holds roles/run.invoker on the credentials service
#     (modules/workflow/sa.tf's google_cloud_run_v2_service_iam_member
#     .credentials), read off the service's own IAM policy — planned exactly
#     when the repository root's credentials_service is non-null, which is the
#     same switch (infra.CredentialsServiceServes) that makes the launcher write
#     the address on the execution. The reader answers this per job; a
#     repository is on the switch when EVERY job of it is.
#
# That exception is judged by the repository's AUTH and never by a flag on the
# job: the mix is `api-key`'s alone (mixedStageAccountJob reads cfg.Agent.Auth
# and answers false for every other repository, so under `oauth` an account's
# token is the run's own credential and the flip takes it away) — and R12's
# FUGARO_STAGE_ACCOUNT_SERVED says something else again, whether the SERVICE
# selects the stage's account, which is planned from a record of its own
# (local.StageAccountServedOn, spec.go) and leaves the mount in place whether it
# is set or not. So THIS ROW NEEDS THE RECORDS: the auth is what the record row
# reads (step_records, which runs first), and a switch judged before it would
# call a correct stage variant off the switch — the same false failure this
# reading exists to remove, one shape further in.
#
# The URL reading stays where the address really is baked: a SHARED project job
# (internal/infra's projectJobEnv) carries it in its template, and the reader
# counts such a job as switched on the URL alone. A per-repository job never
# does, so nothing below requires it.
step_switch() {
  local listed=$SCRATCH/jobs.json got n
  # The job list itself could not be read (step_jobs wrote this row and stopped
  # the check): there is nothing to add, and a second entry for the same key
  # would leave the switch key among the rows this check could not measure —
  # turning that read's own FAIL into "could not measure" (exit 2) where it is
  # a criterion that did not hold (exit 1).
  [ -z "$SWITCH_STOP" ] || return 0
  if [ -z "$API_REPO" ] || [ -z "$OAUTH_REPO" ]; then
    unmeasured switch "this row is read per repository off its jobs' template, ${SVC}'s own IAM policy AND the auth that repository's record names (creds/repoauth/<slug>, the record row): the one model mount a switched job keeps is a mixed stage account's own Claude token, and only an api-key repository's jobs have one (mixedStageAccountJob), so a job still mounting a token cannot be judged without the auth. The records were not assigned (see the record row) and neither run is launched on this row"
    SWITCH_STOP=yes
    return 0
  fi
  # The service's own IAM policy: the invoker grant is the per-repository half
  # the template cannot show, and a read that did not answer is not a policy
  # without the grant — it is this row unmeasured, because a repository called
  # off the switch on a policy nobody read would stop two runs for nothing.
  local iam=$SCRATCH/serviceiam.json
  if ! gcloud run services get-iam-policy "$SVC" --project="$P" --region="$L" --format=json \
    >"$iam" 2>"$SCRATCH/serviceiam.err"; then
    unmeasured switch "'gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L} --format=json' exited non-zero and printed: $(last_lines "$SCRATCH/serviceiam.err"). The per-repository switch is read off each job's own template (no model credential mounted) AND the service's IAM policy (the job's account holds roles/run.invoker on it), and YOUR account needs run.services.getIamPolicy for the second half — a project Owner has it. Read it yourself and run this check again"
    SWITCH_STOP=yes
    return 0
  fi
  local repo off
  for repo in "$REPO_ONE" "$REPO_TWO"; do
    got=$(reader switch "$listed" "$repo" "$iam" "$(repo_auth "$repo")")
    if [ "$(field_of found "$got")" != yes ]; then
      unmeasured switch "the credentials service's own IAM policy came back as no policy at all, so the invoker grant half of the switch is not read: read it yourself ('gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L}') and run this check again"
      SWITCH_STOP=yes
      return 0
    fi
    if [ "$(field_of jobs "$got")" -lt 1 ]; then
      unmeasured switch "no job of ${repo} carries its FUGARO_REMOTE (and none carries its 'fugaro_repo' label) in 'gcloud run jobs list', so this repository has no per-repository job whose template could be read. If it is served by a SHARED project job instead, its switch is not read here at all — this check reads per-repository jobs, and a shared job's own switch is Check 197's. Otherwise run 'fugaro update --repo' in ${repo} and run this check again"
      SWITCH_STOP=yes
      return 0
    fi
    off=$(field_of off "$got")
    if [ "${off:-0}" -ge 1 ]; then
      fact switch FAIL "$(field_of off "$got") of $(field_of jobs "$got") job(s) of ${repo} is OFF the credentials-service switch — <name> <on/off> <why> <the account it runs as>:$(printf '%s\n' "$got" | grep '^job=.* on=no ' | tr '\n' ';' | cut -c1-600) A switched per-repository job holds no model credential (the mount is gone, D2072/G17) and its own account holds roles/run.invoker on ${SVC} (modules/workflow/sa.tf, D135) — the template carries no FUGARO_CREDENTIALS_SERVICE_URL at all, the launcher writes that on each run's execution (internal/cli/run.go's credentialsServiceLaunchEnv). 'why=no-mount' or 'why=mounts:…' means the job still holds the credential the service serves for this repository's auth; 'why=…+no-invoker' means its account was not granted the service, so every run of it would halt at the service's door (D143). Re-apply this repository ('fugaro update --repo' in ${repo}), then this check again"
      SWITCH_STOP=yes
      return 0
    fi
    say "${repo}: $(field_of on "$got") of $(field_of jobs "$got") job(s) on the credentials-service switch (no model credential mounted, the job account granted ${SVC}'s invoker role)"
  done
  # The read's own status and never `|| true`: a failed read leaves an empty
  # document, the reader answers `mode=None` for it, and `None` is not a mode
  # the gateway exists in — so the row below would PASS D2071's third leg on a
  # mode nobody read. A switch row read off a policy this check never got is a
  # PASS on nothing, and
  # the rows below it (and, in the flip mode, the two throwaway runs) would be
  # rows about a state nobody settled. check420.sh and check421.sh treat this
  # same call as one their rows could not be measured from; check191.sh as a
  # FAIL. Here it is the switch that is not measured, and the runs wait.
  local mode rc=0
  got=$(fugaro project policy show --project fugaro --json 2>"$SCRATCH/policy.err") || rc=$?
  printf '%s\n' "$got" >"$SCRATCH/policy.json"
  if [ "$rc" -ne 0 ]; then
    unmeasured switch "both repositories are on the switch (every job of each mounts no model credential and its account holds the credentials service's invoker role), and 'fugaro project policy show --project fugaro --json' exited ${rc}: $(last_lines "$SCRATCH/policy.err"). D2071's third leg — the published budget mode, which 'update' reads and which must be one the gateway exists in, observe or enforce — is NOT measured by that read, and the runs below are not launched on a switch nobody settled. Read the policy yourself ('fugaro project policy show --project fugaro --json') and run this check again"
    SWITCH_STOP=yes
    return 0
  fi
  mode=$(reader policy "$SCRATCH/policy.json" "$REPO_ONE" | sed -n 's/^mode=//p')
  case "$mode" in
  # A mode the document does not carry: the reader answers `None` for a policy
  # with no budget block — an installation with nothing published, a document
  # read from a `show` that printed no policy — and `-` for an empty one. A row
  # that read either as a mode would PASS this leg on a value nobody published,
  # which is the same mistake as the failed read above, one step further in.
  # Every other value that reaches here IS a published mode, and a mode that is
  # not observe or enforce never gets this far: the CLI refuses to load it.
  None|-|'')
    unmeasured switch "both repositories are on the switch (every job of each mounts no model credential and its account holds the credentials service's invoker role), and the policy 'fugaro project policy show --project fugaro --json' printed carries no budget mode (the mode reads '${mode:-<none>}'), so D2071's third leg — the mode 'update' reads, which must be one the gateway exists in, observe or enforce — is NOT measured: a mode the document does not carry is neither. Publish the project policy so it carries one ('fugaro project policy publish', observe or enforce) and run this check again"
    SWITCH_STOP=yes
    return 0
    ;;
  esac
  fact switch PASS "both repositories are on the switch (every job of each mounts no model credential and its account holds the credentials service's invoker role) and the published budget mode is '${mode:-<none>}' (D2071: the mode is what 'update' reads, and the gateway exists in observe and enforce)"
}

# step_records: the auth each repository's own record names, and the assignment
# of the two knobs to the two auths. The record is `fugaro update --repo`'s
# answer and the service narrows every call of that repository's runs to it
# (creds/repoauth, D2070 (a), G16): a repository whose record is missing is one
# the service serves as the cascade, which is not what this check is about, and
# a pair of repositories whose records are not one api-key and one oauth is a
# pair this check cannot read its rows against. Each record is read at the key
# `fugaro update --repo` wrote it under — the repository's own task slug, which
# is the `fugaro_repo` label of its jobs and never its name (record_key).
step_records() {
  local repo key got reason ok=0
  if [ ! -s "$SCRATCH/jobs.json" ]; then
    unmeasured record "the installation's job list was not read (see the switch row), and a repository's record is keyed by its own task slug (creds/repoauth/<slug>, task.Slug), which is the 'fugaro_repo' label those jobs carry: without it the two records are not read at all"
    return 0
  fi
  for repo in "$REPO_ONE" "$REPO_TWO"; do
    key=$(record_key "$repo")
    if [ -z "$key" ] || [ "$key" = "-" ]; then
      unmeasured record "no job of ${repo} in the installation's job list carries its 'fugaro_repo' label, which is the slug its creds/repoauth record is keyed by (task.Slug: <readable>-<16 hex>), so the record cannot be read: run 'fugaro update --repo' in ${repo}, then this check again"
      return 0
    fi
    # The row is the REASON auth_record printed and never the file curl wrote:
    # the file is the database's answer (a 404's body is `null`), so a row built
    # from it would read `FAIL — null` and the reason would land above the block.
    if ! reason=$(auth_record "$key" "$SCRATCH/auth-$key.json"); then
      unmeasured record "${reason:-the record could not be read}"
      return 0
    fi
    got=$(reader auth "$SCRATCH/auth-$key.json" "$repo")
    if [ "$(field_of found "$got")" != yes ]; then
      unmeasured record "${repo}'s creds/repoauth/${key} node holds no object, so no auth is recorded for it"
      return 0
    fi
    case "$(field_of auth "$got")" in
    api-key) API_REPO=$repo ;;
    oauth) OAUTH_REPO=$repo ;;
    cascade)
      fact record FAIL "${repo}'s record says 'cascade' (creds/repoauth/${key}): this repository is served the whole cascade, and Check 193 is about a repository the service narrows to its OWN recorded auth. Put a sandbox repository whose agent.auth is api-key and one whose is oauth on the switch, and run this check again"
      return 0
      ;;
    *)
      fact record FAIL "${repo}'s record names the auth '$(field_of auth "$got")' (creds/repoauth/${key}), which the credentials service serves no call by: only cascade, oauth and api-key are served here (narrowToRepoAuth)"
      return 0
      ;;
    esac
    ok=$((ok + 1))
  done
  if [ "$ok" -ne 2 ] || [ -z "$API_REPO" ] || [ -z "$OAUTH_REPO" ]; then
    fact record FAIL "the two repositories' records are not one api-key and one oauth (CHECK193_REPOS named ${REPO_ONE} and ${REPO_TWO}): this check reads a run of each against the credential its own record names, and it cannot say which row is which without the pair. Name one api-key repository and one oauth repository"
    return 0
  fi
  local got
  got=$(reader auth "$SCRATCH/auth-$(record_key "$OAUTH_REPO").json" "$OAUTH_REPO")
  fact record PASS "${API_REPO}'s record says 'api-key' and ${OAUTH_REPO}'s says 'oauth', naming $(field_of accounts "$got") account(s) ($(record_accounts "$OAUTH_REPO" | tr '\n' ' '))"
}

# step_run REPO AUTH LABEL FACTKEY: one throwaway run of one repository, and the
# service's own lines for it. Three things are read and they are one fact: the
# run reached a FINAL status that is not a failure (it committed and opened its
# pull request, which is only possible if its model calls were answered), the
# service wrote at least one line for it at all (an installation whose runs are
# answered by the service writes one per call, and a run whose lines are empty
# was served by something else), and the credential those lines NAME is one of
# the auth's own — the paid key for `api-key` (tier B), one of the accounts the
# repository's own record names for `oauth` (tier A) — never a provider key
# (tier C, R6: a provider route is the one answer a switched repository may
# still take, and it does not come from the service this way).
step_run() { # step_run REPO AUTH LABEL FACTKEY
  local repo=$1 auth=$2 label=$3 key=$4 task rid row status lines served named bad=""
  task=$SCRATCH/task-$label.md
  rid=$(run_id "$label")
  task_write "$task" "$repo" "$label" "$rid"
  if ! row=$(launch_into "$repo" "$label" "$rid" "$task"); then
    unmeasured "$key" "$(launch_err)"
    return 0
  fi
  status=$(field_of status "$row")
  case "$status" in
  succeeded)
    fact "run-$auth" PASS "the ${auth} run (${rid}) of ${repo} ended '${status}' (outcome=$(field_of outcome "$row"), pull request $(field_of pr "$row"))"
    ;;
  *)
    fact "run-$auth" FAIL "the ${auth} run (${rid}) of ${repo} ended '${status}' and no pull request was opened: $(field_of halt_detail "$row")$(field_of reason "$row") — on a repository on the switch every model call of that run is answered by the credentials service, so a run that could not finish is the service not serving it. Read 'fugaro runs show ${rid} --repo ${repo}' and the service's own lines for it, then run this check again"
    ;;
  esac
  lines=$SCRATCH/audit-$label.json
  # Both rows below are read off the service's own lines for this run, so a read
  # that did not answer leaves them unmeasured rather than FAILed: the run's own
  # row above already carries why it ended as it did, and nothing here is a
  # finding about the credentials service (audit_read).
  if ! audit_read "$rid" "$lines"; then
    unmeasured "audited-$auth" "$(audit_unread "$lines")"
    unmeasured "$key" "$(audit_unread "$lines")"
    return 0
  fi
  served=$(audit_served "$lines" "$rid")
  named=$(audit_named "$lines" "$rid")
  local note=""
  if [ "${served:-0}" -lt 1 ]; then
    note=" — the service wrote no line for this run at all, so its model calls were answered by something else (the credential the job itself mounts, or an upstream that is not this service)"
  fi
  fact "audited-$auth" "$([ "${served:-0}" -ge 1 ] && echo PASS || echo FAIL)" \
    "$served line(s) of the service's own audit for the ${auth} run (${rid}); the credentials it NAMED: $(printf '%s' "$named" | tr '\n' ' ' | sed 's/ *$//')${note}"
  case "$auth" in
  api-key)
    printf '%s\n' "$named" | grep -q '^anthropic-api-key|B$' || bad="no line of this run names the paid key (anthropic-api-key) at tier B"
    ;;
  oauth)
    # The credential a tier-A line names is the ACCOUNT itself and never the
    # secret container: internal/credsvc's bundle loader names a Claude account
    # credential after the account — `default` for the unsuffixed
    # claude-oauth-token, the bare name for claude-oauth-token-<account> — and
    # the audit line writes that name. So the line has to name one of the
    # accounts the run's own repository records, which is the whole of what
    # narrowToRepoAuth lets through for it.
    local acc listed=no
    while IFS= read -r acc; do
      [ -n "$acc" ] || continue
      if printf '%s\n' "$named" | grep -qxF -- "${acc}|A"; then listed=yes; break; fi
    done <<<"$(record_accounts "$repo")"
    [ "$listed" = yes ] || bad="no line of this run names an account its own record names ($(record_accounts "$repo" | tr '\n' ' ')) at tier A"
    ;;
  esac
  if [ -n "$bad" ]; then
    fact "$key" FAIL "${bad}: the service served the ${auth} run (${rid}) of ${repo} and its lines name $(printf '%s' "$named" | tr '\n' ' '). The credential a switched repository is served is the one its RECORD names — the paid key for api-key, an account's token for oauth — and a run served anything else is the boundary not holding. Read 'gcloud logging read' for this run and run this check again"
    return 0
  fi
  fact "$key" PASS "the service served the ${auth} run (${rid}) of ${repo} and its audit lines name $(printf '%s' "$named" | tr '\n' ' ' | sed 's/ *$//')"
}

# step_declared_account: the oauth half of the boundary — the service serves a
# run the ONE account it declared and refuses it every other one its repository's
# record names.
#
# The account the run DECLARED comes from the launch and never from the run's
# row: that row's `oauth_account` is the credential the service's own answers
# carried (internal/runner's recordCredential, written from gateway.serviceAnswer:
# "the account the run used last, as the answers of its calls report it"), so a
# row that took the declaration from there would compare the answers with
# themselves — a service that served one account the run never declared, but
# which its repository's record names, would read as a pass. What the check knows
# instead is the launch it made itself: it runs `fugaro run --repo …` with no
# `--token`, and a launch that names no account declares the repository's DEFAULT
# account — the empty declaration IS that account's name everywhere the service
# speaks it (narrowToRepoAuth normalizes it with config.ReviewVariantAccount,
# which answers config.DefaultClaudeAccount, "default"). So `want` is that name,
# and every tier-A line the service wrote for the run is the OBSERVATION: it must
# name that account and no other. (A tier-A line names the ACCOUNT itself — the
# name the service's bundle holds that credential under, credsvc's loader — and
# never the secret container it is stored in.)
#
# It is read from the run's own lines and not from a call the check makes, and
# the reason is that the other half of the boundary cannot be reached from here:
# `narrowToRepoAuth`'s oauth branch refuses a call that declares an account the
# record does not name, and a run cannot declare one — the launch checks
# `--token` against config.AllowedClaudeAccounts and the record is written from
# infra.WorkflowAccounts over that same list (internal/cli/update_repoauth.go says
# so in as many words), so a launched run's account is always one its own record
# names. The row is measured only where the record names more than one account: a
# record that names one leaves nothing the service could have served this run
# besides the account it declared, and a row read there would be a row about
# nothing.
step_declared_account() {
  local lines rid want=default named others="" accounts n note
  accounts=$(record_accounts "$OAUTH_REPO")
  n=$(field_of accounts "$(reader auth "$SCRATCH/auth-$(record_key "$OAUTH_REPO").json" "$OAUTH_REPO")")
  if [ "${n:-0}" -lt 2 ]; then
    unmeasured declared-account "${OAUTH_REPO}'s own record names ${n:-0} account(s) (${accounts:-none}): a record that names one account leaves nothing this run could have been served besides the account it declared, so the row would be a row about nothing. Let the repository's runs name a second Claude account (the account's own allow_data_to for it in this machine's local config, then 'fugaro update --repo' there), so its record names two, and run this check again"
    return 0
  fi
  rid=$(run_id two)
  lines=$SCRATCH/audit-two.json
  note="the account a launch that names no account declares, which is the launch this check made (fugaro run, no --token)"
  # The row is read off the oauth run's OWN lines and off nothing else, so a run
  # that never launched — step_run writes that file after a launch and never
  # before it — leaves the row nothing to read at all. Read off a file nobody
  # wrote, every line below is empty and the row reports the boundary as broken
  # on the strength of nothing: a FAIL the verdict takes for a criterion that did
  # not hold (exit 1), where what happened is a row this check could not measure
  # (exit 2), and the launch's own row above already carries the reason. The
  # sibling row step_proxy reads its own run's file the same way and guards it
  # the same way.
  if [ ! -f "$lines" ]; then
    unmeasured declared-account "the oauth run (${rid}) of ${OAUTH_REPO} was never launched, so it left no lines of its own and the account the service served it is not read here: the launch's own row above says why it never launched. The row is read off that run's own audit lines and never off a call this check makes"
    return 0
  fi
  # The file that IS there but was never read: a `gcloud logging read` that did
  # not answer leaves it empty, and the arm below would take that emptiness for
  # "no served line of its own names that account" — the boundary reported as
  # broken on the strength of a read that never happened. It is the same
  # distinction the guard above makes, one step further in (audit_read).
  if [ -f "$lines.unread" ]; then
    unmeasured declared-account "$(audit_unread "$lines")"
    return 0
  fi
  named=$(audit_named "$lines" "$rid")
  # The accounts the run's own tier-A lines name, and then the ones of those that
  # are not the declared account: the service may only ever serve it the account
  # it declared (narrowToRepoAuth keeps `tier A && Name == the declared
  # account`), so a line naming anything else is the boundary not holding. The
  # lines are reduced to their accounts here so that a row speaks of accounts and
  # not of every credential the run's calls named — the git purpose's token has
  # no tier and is not an account at all. `|| true`: the arm below this one is the
  # run whose tier-A lines name the declared account and nothing else, and
  # `grep -v` finding no such other line is exactly that case — not a failure of
  # the pipeline.
  local tier_a
  tier_a=$(printf '%s\n' "$named" | sed -n 's/^\(.*\)|A$/\1/p' | sort -u)
  others=$(printf '%s\n' "$tier_a" | grep -vxF -- "${want}" | sort -u | tr '\n' ' ' | sed 's/ *$//' || true)
  if [ -n "$others" ]; then
    fact declared-account FAIL "the oauth run (${rid}) of ${OAUTH_REPO} declared ${want} (${note}) and the service served it ANOTHER account as well: the accounts its lines name are $(printf '%s\n' "$tier_a" | tr '\n' ' ' | sed 's/ *$//'), and the only account it declared is ${want}. The service narrows an oauth call to the account the call itself declared (narrowToRepoAuth, D2070 (a): keepCandidates keeps tier A and Name == the declared account), so a line naming another account — the record names $(printf '%s' "$accounts" | tr '\n' ' ') — is a run served an account it did not declare: this is the row Check 193 exists for. Read that run's lines ('gcloud logging read') and the service's own code path before anything else"
    return 0
  fi
  if ! printf '%s\n' "$named" | grep -qxF -- "${want}|A"; then
    fact declared-account FAIL "the oauth run (${rid}) of ${OAUTH_REPO} declared ${want} (${note}), and no served line of its own names that account at tier A: the accounts its lines name are $(printf '%s\n' "$tier_a" | tr '\n' ' ' | sed 's/ *$//' | sed 's/^$/<none>/'). An oauth repository's runs are served a Claude account's credential and nothing else — a tier-A line names the account itself (default for the repository's default account, the bare account name for a named one), the paid key is tier B, and this repository's record says oauth — so a run whose lines name no account its record names was not served by the account it declared. Read 'fugaro runs show ${rid} --repo ${OAUTH_REPO}' and the service's lines for it"
    return 0
  fi
  fact declared-account PASS "the oauth run (${rid}) of ${OAUTH_REPO} declared ${want} (${note}) and the service served it THAT account alone: every tier-A line of its own names ${want}, and ${n} account(s) are in its record ($(printf '%s' "$accounts" | tr '\n' ' ')) — so no line of the run names any other account the repository's own record would have allowed (narrowToRepoAuth, D2070)"
}

# step_proxy: F29's half of S2 — a Bitbucket repository published with
# `proxy: true` that is ON the switch pushes through the credentials service.
# The repository is one of the two (the plan's "a Bitbucket one of them
# published with proxy: true"), so its run is the one step_run already made and
# this row reads the published node for the proxy switch and the SAME run's
# lines for the traffic: a proxied run's git traffic is rewritten to the
# execution's own loopback forwarder and reaches the service, which writes a
# line of the run's own for it — and a repository off the switch, or one the
# node does not proxy, pushes with the credential its job mounts and writes
# none.
step_proxy() {
  local repo got admits proxy lines rid label key
  repo=$PROXY_REPO
  if [ -z "$repo" ]; then
    # BOTH, because the proxy is one switch for the whole published Bitbucket
    # block (projectpolicy's BitbucketToken.proxy) and it is the ADMISSION that
    # says which repositories that switch covers: a repository the node does not
    # admit is not proxied by it, whatever the switch reads, and picking one on
    # the switch alone would read a repository the policy never covered.
    for repo in "$REPO_ONE" "$REPO_TWO"; do
      got=$(reader policy "$SCRATCH/policy.json" "$repo")
      if [ "$(field_of proxy "$got")" = yes ] && [ "$(field_of admits "$got")" = yes ]; then break; fi
      repo=""
    done
  fi
  if [ -z "$repo" ]; then
    unmeasured proxy "neither ${REPO_ONE} nor ${REPO_TWO} is admitted by the published policy with proxy: true (fugaro project policy show --json), and the plan's row is about a Bitbucket one of the two: publish proxy: true for the Bitbucket sandbox repository you put on the switch, or name it with CHECK193_PROXY_REPO, and run this check again"
    return 0
  fi
  got=$(reader policy "$SCRATCH/policy.json" "$repo")
  admits=$(field_of admits "$got")
  proxy=$(field_of proxy "$got")
  case "$repo" in "$REPO_ONE") label=one ;; "$REPO_TWO") label=two ;; *) label="" ;; esac
  rid=""
  if [ -n "$label" ]; then rid=$(run_id "$label"); fi
  if [ -z "$rid" ] || [ ! -f "$SCRATCH/audit-$label.json" ]; then
    unmeasured proxy "${repo} is the repository published with proxy: true and this check made no run of it (CHECK193_PROXY_REPO names a repository that is not one of the two CHECK193_REPOS names, so no run of its own was launched): the row is read off that repository's own run, and this check launches no third run"
    return 0
  fi
  lines=$SCRATCH/audit-$label.json
  # The row is read off the run's own lines, so a read that did not answer is a
  # row this check cannot measure and never a repository whose push did not
  # reach the service (audit_read).
  if [ -f "$lines.unread" ]; then
    unmeasured proxy "$(audit_unread "$lines")"
    return 0
  fi
  # A run's own git traffic that the service forwarded is one line of the
  # purposes GIT_PURPOSES names, and the credential on it is the bundle's
  # Bitbucket token: a push is one such request (internal/credsvc/audit.go,
  # PurposeGit — a fetch, the receive-pack advertisement, or a push), and a
  # repository's Bitbucket REST calls are the other (PurposeBitbucketAPI). A
  # line of any OTHER purpose is a different act of the run's — its model calls
  # are `forward` — and a person's read mint is `bitbucket_read`, which carries
  # a repository and no run at all: matching a line by the word `bitbucket`
  # would let a mint that is not this run's answer this row.
  local purposes purposes_list creds
  # One purpose per line, because the match below is a whole-line one: joined
  # into a single line ('forward git') nothing would ever match it. purposes
  # is the same list for the messages the rows print.
  purposes_list=$(reader audit "$lines" "$rid" | sed -n 's/^audit=[^|]*|[^|]*|[^|]*|\([^|]*\)|.*/\1/p' | sort -u)
  purposes=$(printf '%s' "$purposes_list" | tr '\n' ' ')
  creds=$(reader audit "$lines" "$rid" | sed -n 's/^audit=ok|\([^|]*\)|\([^|]*\)|\(git\|bitbucket-api\)|.*/\1/p' | sort -u | tr '\n' ' ')
  if [ "$proxy" != yes ]; then
    unmeasured proxy "${repo} is NOT published with proxy: true (admits=${admits}), so nothing about the proxy is measured: a repository the published node does not proxy keeps its Bitbucket token mounted on its job and its runs push straight to the provider, which is the state F29 names. Publish proxy: true for it (fugaro project policy publish) and run this check again"
    return 0
  fi
  if ! printf '%s\n' "$purposes_list" | grep -qE '^(git|bitbucket-api)$'; then
    fact proxy FAIL "${repo} is published with proxy: true and on the credentials-service switch, and the service wrote no git line for its run (${rid}): the purposes its lines carry are '$(printf '%s' "$purposes" | sed 's/ *$//')' and none of them is 'git' or 'bitbucket-api'. F29's reading is that a switched Bitbucket repository published with proxy: true pushes THROUGH the service (internal/runner's project.go's proxiedBitbucket), which writes a line of that run's own for the traffic — a run whose push never reached the service pushed with the credential its job mounts, which is the state S2 exists to end. Read that run's lines ('gcloud logging read') and run this check again"
    return 0
  fi
  fact proxy PASS "${repo} is published with proxy: true and on the switch, and the service forwarded its run's (${rid}) own git traffic: the lines of its own carry the purposes '$(printf '%s' "$purposes" | sed 's/ *$//')' and name the credential $(printf '%s' "$creds" | sed 's/ *$//') — so the push went through the credentials service and not through a credential of the job's own (F29)"
}

# step_mounts: the flip's first half (B2.7, D2072, G17, R6) — after it, no job
# of either repository mounts the MODEL credential. Two spellings are read
# together, because the mount is the env variable AND the container it is fed
# from (config.ModelSecretEnvs, and the reserved secret names behind them): a
# job that still carries ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN, or an env
# fed from the anthropic-api-key / claude-oauth-token containers is one whose
# runs hold the credential the flip exists to take out. Provider keys are
# counted and never a finding: R6 keeps them, and a repository whose stages go
# out on a provider route still holds that key.
step_mounts() {
  local listed=$SCRATCH/jobs.json repo got n bad="" line name spec provider=0
  local jobs=0
  gcloud run jobs list --project="$P" --region="$L" --format=json >"$listed" 2>"$SCRATCH/jobs.err" || {
    fact no-model-mounts FAIL "gcloud run jobs list exited non-zero: $(last_lines "$SCRATCH/jobs.err"). Nothing about the mounts is settled by it"
    return 0
  }
  for repo in "$REPO_ONE" "$REPO_TWO"; do
    got=$(reader jobs "$listed" "$repo")
    n=$(field_of jobs "$got")
    if [ "${n:-0}" -lt 1 ]; then
      unmeasured no-model-mounts "no job of ${repo} carries its FUGARO_REMOTE in 'gcloud run jobs list', so there is no job whose mounts could be read: run 'fugaro update --repo' in ${repo} and this check again"
      return 0
    fi
    jobs=$((jobs + n))
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      case "$line" in
      'job='*' mounts=none url='*) ;;
      'job='*) bad="${bad} ${line#job=};" ;;
      esac
    done < <(printf '%s\n' "$got" | grep '^job=' || true)
  done
  if [ -n "$bad" ]; then
    fact no-model-mounts FAIL "${jobs} job(s) of the two repositories carry FUGARO_REMOTE and these still MOUNT the model credential — <name> <the model containers its env is fed from> <whether the credentials service's URL is in its env, which is absent on a per-repository job template: the launcher writes it on each run's execution> <the account its containers run as>:${bad:0:600} After the flip no job of a repository the service serves the model path of mounts it (D2072, G17: the mounts go when the switch does, and R6 keeps only the provider keys). Run 'fugaro update --repo' in each repository named here, then this check again"
    return 0
  fi
  fact no-model-mounts PASS "${jobs} job(s) of the two repositories carry FUGARO_REMOTE and none mounts a model credential: no job's env is fed from the anthropic-api-key or claude-oauth-token container, and no job carries ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN (D2072, G17)"
  for repo in "$REPO_ONE" "$REPO_TWO"; do
    while IFS= read -r name; do
      [ -n "$name" ] || continue
      spec="$SCRATCH/job-$name.json"
      gcloud run jobs describe "$name" --project="$P" --region="$L" --format=json >"$spec" 2>/dev/null || continue
      got=$(reader job "$spec")
      provider=$((provider + $(field_of providerkeys "$got")))
    done < <(reader jobs "$listed" "$repo" | sed -n 's/^job=\([^ ]*\) .*/\1/p')
  done
  fact no-model-mounts-detail INFO "${provider} provider-key variable(s) are left on those jobs (R6): the flip takes the Anthropic key and the OAuth tokens and keeps every provider route a repository's stages go out on, so a job that still holds one is the design and not a finding"
}

# step_inflight: U9 — a run that was ALREADY GOING when the repository was
# re-applied keeps the mount it was launched with and still finishes. Its
# execution was created before the re-apply, so its container spec is the one
# the old job template wrote, and the reading is exactly that: its own row's
# started_at is before the job's own updateTime, its row is terminal and it
# succeeded, and the EXECUTION's own container spec — read off `gcloud run jobs
# executions describe`, the run's row naming the execution — still mounts the
# model credential while the job template it was created from no longer does. A
# run that did not finish kept nothing worth keeping; a run that started after
# the re-apply was launched by the template the flip wrote and is not the U9
# case at all.
#
# The service's own lines for that run are NOT this row's reading. The flip
# takes the MOUNTS away, and a switched run reaches the service on
# FUGARO_CREDENTIALS_SERVICE_URL (internal/runner's serviceSwitched, which the
# switch mode's own rows depend on) — a variable the LAUNCHER writes on every
# execution and no job template bakes (internal/cli/run.go's
# credentialsServiceLaunchEnv; the address is on the shared project job's
# template alone) — so a run in flight across the re-apply is served by the
# service before and after it, and a reading that took a line of its own for the
# re-apply having reached into it would fail every run that behaved correctly.
step_inflight() {
  local rid=$1 row repo started_at order got listed=$SCRATCH/jobs.json
  local execpath bare spec mounts tmpl
  if [ -z "$rid" ]; then
    unmeasured inflight-run "CHECK193_INFLIGHT is empty, so there is no run to read"
    unmeasured inflight-mount "CHECK193_INFLIGHT is empty, so there is no run whose mount could be read"
    return 0
  fi
  row=""
  INFLIGHT_REPO=""
  for repo in "$REPO_ONE" "$REPO_TWO"; do
    if fugaro runs ls --repo "$repo" --project fugaro --no-cache --json >"$SCRATCH/rows-inflight.json" 2>/dev/null; then
      row=$(reader run "$SCRATCH/rows-inflight.json" "$rid" || true)
      if [ "$(field_of found "$row")" = yes ]; then INFLIGHT_REPO=$repo; break; fi
      row=""
    fi
  done
  if [ -z "$row" ]; then
    unmeasured inflight-run "the run CHECK193_INFLIGHT names (${rid}) is not in either repository's listing: a run this check cannot find is one whose mount it cannot read. Read 'fugaro runs ls' for the two repositories and name the id again"
    unmeasured inflight-mount "the run CHECK193_INFLIGHT names (${rid}) could not be found, so nothing about its mount is measured"
    return 0
  fi
  if [ "$(field_of terminal "$row")" != yes ]; then
    fact inflight-run FAIL "the run named by CHECK193_INFLIGHT (${rid}) had not reached a final status when this check read it: status=$(field_of status "$row"). U9 is about a run that was going ACROSS the re-apply, and this reading is taken after it has finished — wait for it to end ('fugaro runs show ${rid} --repo ${INFLIGHT_REPO}') and run this check again"
    unmeasured inflight-mount "the run ${rid} had not finished when this check read it, so the mount it kept is not measured"
    return 0
  fi
  started_at=$(field_of started_at "$row")
  # The re-apply's own clock, read off the repository's jobs: `fugaro update
  # --repo` updates every job of the repository, so the latest updateTime among
  # them is when it reached this repository. A run that started AFTER that was
  # launched by the flipped template and is not the U9 case at all — its row
  # would be read as "a mount it kept" when it never had one.
  gcloud run jobs list --project="$P" --region="$L" --format=json >"$listed" 2>/dev/null || : >"$listed"
  got=$(reader inflight "$listed" "$INFLIGHT_REPO" "$started_at")
  order=$(field_of order "$got")
  case "$order" in
  after)
    fact inflight-run FAIL "the run named by CHECK193_INFLIGHT (${rid}) was started at ${started_at:-unread}, which is not before ${INFLIGHT_REPO}'s jobs were last updated ($(field_of update "$got"), job $(field_of job "$got")): it was launched by the template the re-apply wrote, so it is not a run that was in flight ACROSS the re-apply and its mount says nothing about U9. Name the run that was already going when you ran 'fugaro update --repo'"
    unmeasured inflight-mount "the run ${rid} started after the re-apply, so the mount it held is not the one U9 is about"
    return 0
    ;;
  unknown)
    unmeasured inflight-run "the run ${rid} started at ${started_at:-nothing readable} and this check could not read the time ${INFLIGHT_REPO}'s jobs were last updated ($(field_of update "$got")): whether it was going across the re-apply is not measured. Read 'gcloud run jobs describe $(field_of job "$got") --project=${P} --region=${L}' and the run's own row and run this check again"
    unmeasured inflight-mount "the run's start could not be placed against the re-apply, so its mount is not read"
    return 0
    ;;
  esac
  if [ "$(field_of status "$row")" != succeeded ]; then
    fact inflight-run FAIL "the run named by CHECK193_INFLIGHT (${rid}) started at ${started_at} — before the re-apply ($(field_of update "$got")) — and ended '$(field_of status "$row")' without opening a pull request: $(field_of halt_detail "$row")$(field_of reason "$row") (outcome=$(field_of outcome "$row")). An execution created before the re-apply keeps the container spec it was created with, which is the whole of U9, so the re-apply cannot have taken a credential out from under it: a run of that shape that could not finish is the re-apply having reached INTO a run that was already going"
    unmeasured inflight-mount "the run ${rid} did not finish, so nothing is read about the mount it kept"
    return 0
  fi
  fact inflight-run PASS "the run named by CHECK193_INFLIGHT (${rid}) started at ${started_at}, before ${INFLIGHT_REPO}'s jobs were last updated ($(field_of update "$got"), job $(field_of job "$got")), and still finished: status=$(field_of status "$row"), outcome=$(field_of outcome "$row"), pull request $(field_of pr "$row"), pushed=$(field_of pushed "$row")"
  # The mount the run kept is read off the EXECUTION's own container spec and
  # never off the service's lines: the run's row names the execution, and the
  # spec an execution was created with is frozen for its whole life (U9), so
  # the mount is either on it or it never had one.
  execpath=$(field_of execution "$row")
  if [ -z "$execpath" ]; then
    unmeasured inflight-mount "the row of the run ${rid} names no execution, so the container spec the run was created with cannot be read: U9's reading is that spec. Read 'fugaro runs show ${rid} --repo ${INFLIGHT_REPO}' — a run whose own record carries no execution is one whose mount this row cannot read"
    return 0
  fi
  bare=${execpath##*/}
  spec=$SCRATCH/execution.json
  if ! gcloud run jobs executions describe "$bare" --project="$P" --region="$L" --format=json >"$spec" 2>"$SCRATCH/execution.err"; then
    unmeasured inflight-mount "the execution ${execpath} of the run ${rid} could not be read: 'gcloud run jobs executions describe ${bare}' exited non-zero and printed: $(last_lines "$SCRATCH/execution.err"). U9's reading is the container spec that execution was created with, and a read that did not answer is not an execution with no mount"
    return 0
  fi
  got=$(reader job "$spec")
  mounts=$(field_of modelmounts "$got")
  # The template the execution was created from, off the job list this step has
  # already read and whose updateTime the `before` above was read from — so the
  # file holds at least one job of this repository by the time this is read, and
  # `mounted` is a reading of that list and not of an empty one.
  tmpl=$(reader jobs "$listed" "$INFLIGHT_REPO")
  tmounted=$(field_of mounted "$tmpl")
  if [ "${tmounted:-0}" -ge 1 ]; then
    unmeasured inflight-mount "the execution ${execpath} of the run ${rid} carries ${mounts}, and the jobs of ${INFLIGHT_REPO} mount a model credential too (${tmounted} of $(field_of jobs "$tmpl")): with the template still handing the mount out, the mount on that execution is not the RETENTION U9 is about — the flip has not reached that repository's jobs, which is the no-model-mounts row above"
    return 0
  fi
  if [ "$mounts" = none ]; then
    fact inflight-mount FAIL "the execution ${execpath} of the run ${rid} — created before the re-apply and finished all the same — carries NO model mount: $(field_of modelenvs "$got") is not among its container's variables, and the job template of ${INFLIGHT_REPO} mounts none either. An execution created before the re-apply keeps the container spec it was created with (U9), and this one's spec has no mount to keep — so it is not a run the mount-dropping re-apply was in flight across. Name a run that was already going when you re-applied the release that drops the mount"
    return 0
  fi
  fact inflight-mount PASS "the execution ${execpath} of the run ${rid} was created before the re-apply and its own container spec still mounts the model credential — $(field_of modelmounts "$got") — while the job template of ${INFLIGHT_REPO} mounts none: the execution kept the spec it was created with, which is U9's reading. The credentials service's URL is on that execution too ($(field_of url "$got")): the launcher writes it on every launch (internal/cli/run.go's credentialsServiceLaunchEnv) and the flip does not take it away, so the service's own lines for this run are expected and are not read here"
}

# ---------------------------------------------------------------------- 193
run_193() {
  # The job list, then the records, then the switch: the switch reads each
  # repository's jobs out of the list and judges the one mount a switched job
  # keeps by the auth the record names (step_switch), so it is the LAST of the
  # three reads and not the first.
  step_jobs
  step_records
  step_switch
  if [ -z "$API_REPO" ] || [ -z "$OAUTH_REPO" ]; then
    unmeasured served-api-key "the two repositories could not be assigned to their auths (see the record row), so no run was launched"
    unmeasured served-oauth "the two repositories could not be assigned to their auths (see the record row), so no run was launched"
    unmeasured declared-account "the two repositories could not be assigned to their auths (see the record row), so no run was launched"
    unmeasured proxy "the two repositories could not be assigned to their auths (see the record row), so no run was launched"
    return 0
  fi
  # A switch that is off, unread, or on an installation whose budget mode is not
  # one the gateway exists in is a state this check cannot read a served run out
  # of, and each run it launches commits to a sandbox repository and opens a
  # pull request: the rows below read `not reached` rather than costing two of
  # them.
  [ -z "$SWITCH_STOP" ] || return 0
  step_run "$API_REPO" api-key one served-api-key
  step_run "$OAUTH_REPO" oauth two served-oauth
  step_declared_account
  step_proxy
}

# ---------------------------------------------------------------------- 193 after the flip
run_193_flip() {
  step_jobs
  step_records
  step_switch
  step_mounts
  step_inflight "$INFLIGHT"
  if [ -z "$API_REPO" ] || [ -z "$OAUTH_REPO" ]; then
    unmeasured served-api-key "the two repositories could not be assigned to their auths (see the record row), so no run was launched"
    unmeasured served-oauth "the two repositories could not be assigned to their auths (see the record row), so no run was launched"
    return 0
  fi
  # The mount rows above are the flip's whole reading and are readable whatever
  # the switch says — a job with no mount is the state the flip exists to reach,
  # switched or not — so only the two runs wait for the switch row.
  [ -z "$SWITCH_STOP" ] || return 0
  step_run "$API_REPO" api-key flip-one served-api-key
  step_run "$OAUTH_REPO" oauth flip-two served-oauth
}

# print_plan: what a real run would run, in order. Printed by --dry-run, which
# runs NOTHING: no gcloud, no fugaro and no curl is called, so this mode is safe
# on a machine with no credentials at all.
print_plan() {
  say "dry run: NOTHING below was run, and no tool was called at all — not gcloud, not fugaro, not curl. A real run would first check that this is the fugaro-dev installation (fugaro doctor --project fugaro --all, and that fugaro version is not a development build), then read the installation's Realtime Database URL out of the local config, then:"
  say "  [network] gcloud run jobs list --project=${P} --region=${L} --format=json  (each repository's own jobs, matched by their FUGARO_REMOTE: the account each runs as, and that no job mounts a model credential — a per-repository job template carries no FUGARO_CREDENTIALS_SERVICE_URL: the launcher writes that on each run's execution)"
  say "  [network, read only] gcloud auth print-access-token, then GET ${RTDB:-<budget.rtdb_url>}/creds/repoauth/<slug>.json for each repository — the slug being the repository's own task slug ('<readable>-<16 hex>'), which the check reads off its jobs' 'fugaro_repo' label and never spells out of the repository's name — the record 'fugaro update --repo' writes; READ, never written, the check narrows no record. It is read first, because the switch row judges the one model mount a switched job keeps by the auth that record names"
  say "  [network] gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L} --format=json  (who may invoke the credentials service: each job account's roles/run.invoker is the other half of the per-repository switch, D135)"
  say "  [network] fugaro project policy show --project fugaro --json  (the budget mode must be one the gateway exists in, observe or enforce, D2071)"
  if [ "$MODE" = flip ]; then
    say "  [network] gcloud run jobs list … (each job's container: no ANTHROPIC_API_KEY, no CLAUDE_CODE_OAUTH_TOKEN, no env fed from anthropic-api-key or claude-oauth-token*)"
    say "  [network] fugaro runs ls --repo <the repository of CHECK193_INFLIGHT> --no-cache --json  (that run's row: terminal, succeeded, started before the job's own updateTime, and the execution it names)"
    say "  [network] gcloud run jobs executions describe <that execution> --project=${P} --region=${L} --format=json  (its OWN container spec, which is the one it was created with: it still mounts the model credential, while the job template the flip wrote mounts none)"
  fi
  say "  [network, writes] fugaro run --repo ${API_REPO:-<the api-key repository>} --project fugaro --run-id <today>-0193 --title 'Check 193: one' --task-file <task> --json  (one throwaway run: it writes one line to CHK193.md, commits and opens a pull request)"
  say "  [network] fugaro runs ls --repo <that repository> --no-cache --json  (its row, polled until terminal)"
  say "  [network] gcloud logging read 'resource.type=\"cloud_run_revision\" resource.labels.service_name=\"fugaro-credentials\" jsonPayload.run=\"<run id>\"'  (credsvc: audit lines: ok, anthropic-api-key, tier B)"
  say "  [network, writes] the same two calls for the oauth repository (run id <today>-0194)"
  if [ "$MODE" = flip ]; then
    say "  [network, writes] the same two runs again (<today>-0195 and <today>-0196): after the flip the runs must still finish with NO mount on their jobs"
  else
    say "  [local] its audit lines must name the ONE account the run declared (the account's own name: 'default' for the repository's default account, else the bare account name, tier A) and no other account its own record names"
    say "  [local] the Bitbucket one of the two published with proxy: true must have a line of its own under a Bitbucket purpose (F29)"
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

  # The dry run comes BEFORE the two guards that read the cloud (the
  # installation's identity, and the access token the record is read with): a
  # plan that called `fugaro doctor` and minted a token would not be the thing
  # this mode claims to be — one you can run on a machine with no credentials.
  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "the plan above was printed and NOTHING was called: no gcloud, no fugaro, no curl. The guards that read the cloud (the installation's own identity, and the access token) run only in a real run"
    return 0
  fi

  guard_project
  if ! AT=$(gcloud auth print-access-token 2>"$SCRATCH/at.err"); then
    stop "gcloud auth print-access-token failed, so the installation's Realtime Database could not be read: $(last_lines "$SCRATCH/at.err"). Sign in with 'gcloud auth login' and run this check again"
  fi
  if [ -z "$RTDB" ]; then
    stop "this check needs the installation's Realtime Database URL and read none out of ${CONFIG_FILE} (budget.rtdb_url): the record it reads each repository's auth from lives in that database, and without it no row below can be measured. Check that ${CONFIG_FILE} is this machine's local config for installation fugaro, or name it with CHECK193_CONFIG"
  fi

  case "$MODE" in
  flip) run_193_flip ;;
  *) run_193 ;;
  esac
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs. The
# two modes print two DIFFERENT key sets — Check 193's first half has no mount
# to read and its second half has no account boundary to read — so a block from
# one is never read as a block from the other.
BLOCK_KEYS=(fugaro-version project-region repos xtrace scratch switch record
  run-api-key audited-api-key served-api-key run-oauth audited-oauth served-oauth
  declared-account proxy restore scratch-removed)

BLOCK_KEYS_FLIP=(fugaro-version project-region repos xtrace scratch switch record
  no-model-mounts no-model-mounts-detail run-api-key audited-api-key served-api-key
  run-oauth audited-oauth served-oauth inflight-run inflight-mount restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed="" keys
  if [ "$MODE" = flip ]; then keys=("${BLOCK_KEYS_FLIP[@]}"); else keys=("${BLOCK_KEYS[@]}"); fi
  printf '\nCHECK 193 RESULT\n' >&3
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
  printf 'CHECK 193 RESULT END (exit %s)\n' "$status" >&3
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
# The credentials service's own Cloud Run name, hard-coded like the two above and
# for the same reason (check196.sh's SVC): the invoker grant this check reads the
# per-repository switch off lives on THIS service's IAM policy, and a name taken
# from the environment could be another installation's service.
SVC=fugaro-credentials
DRY_RUN_CLI=""
MODE=switch
REPOS_RAW=${CHECK193_REPOS:-}
PROXY_REPO=${CHECK193_PROXY_REPO:-}
INFLIGHT=${CHECK193_INFLIGHT:-}
RUN_BASE=${CHECK193_RUN_ID:-$(date -u +%Y%m%d-%H%M%S)}
RUN_SECONDS=${CHECK193_RUN_SECONDS:-2400}
POLL_SECONDS=${CHECK193_POLL_SECONDS:-20}
LOG_WAIT_SECONDS=${CHECK193_LOG_WAIT_SECONDS:-60}
LOG_POLL_SECONDS=${CHECK193_LOG_POLL_SECONDS:-5}
LOG_AGE=${CHECK193_LOG_AGE:-1h}
LOG_LIMIT=${CHECK193_LOG_LIMIT:-200}
KEEP_SCRATCH=${CHECK193_KEEP_SCRATCH:-}
REPO_ONE=""
REPO_TWO=""
API_REPO=""
OAUTH_REPO=""
# Set by step_switch when the switch itself could not be read or does not hold:
# the runs this check launches are not free (each commits to a sandbox
# repository and opens a pull request), so nothing is launched on a state whose
# rows below could only read `not reached`.
SWITCH_STOP=""
CONFIG_FILE=${CHECK193_CONFIG:-${HOME:-}/.config/fugaro/projects/fugaro.yaml}
# The Realtime Database URL, read out of the installation's own local config
# (budget.rtdb_url): the one place a machine set up for this installation
# already names it.
RTDB=""
if [ -f "$CONFIG_FILE" ]; then
  RTDB=$(grep -m1 -E '^[[:space:]]*rtdb_url:[[:space:]]*' "$CONFIG_FILE" 2>/dev/null | sed 's/^[^:]*:[[:space:]]*//' | tr -d '"' | tr -d "'" | tr -d '[:space:]' || true)
fi

for arg in "$@"; do
  case "$arg" in
  --after-flip) MODE=flip ;;
  --dry-run) DRY_RUN_CLI=1 ;;
  -h | --help)
    # The header comment and nothing else: the range ends on the first line of
    # the script itself, which is filtered out with it — a help that printed
    # `set -euo pipefail` under the usage would read as if that were part of it.
    sed -n '2,/^set -euo/{/^#/p}' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *) say "unknown argument: $arg"; exit 2 ;;
  esac
done
DRY_RUN=${CHECK193_DRY_RUN:-$DRY_RUN_CLI}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
