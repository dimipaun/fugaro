#!/usr/bin/env bash
# Check 194 — a repository's workflow secrets, read by the credentials service
# instead of mounted into its job.
#
# It settles S3's reading (plan B3.6, its "What it measures"; the
# reconciliation's **D2073**, **D2074**, **D2075**, **D1262**, **D1264**,
# **D1267**, **D2313**, **G18**, **G19**, **F33**, **F4** and **R6**;
# UNVERIFIED **U9**). Until B3.8's flip a switched repository still mounts every
# workflow secret its job holds, so an offline reading settles nothing: whether
# `fugaro update --repo` really wrote the credentials service a record of the
# containers a run may fetch from, whether the containers that record names are
# the ones the job mounts (F33, D1267 — the fetch reads what the mount read, and
# a record naming another container is a value the run never had), whether a run
# is refused the workflow secret of another repository, and — after the flip —
# whether the mount and the job account's accessor are really gone while a run
# still finishes with both values, are all answers of a live installation. So
# this check launches a throwaway run of one sandbox repository and reads the
# service's OWN lines back: `credsvc: audit` for the acts, which names the
# CONTAINER a workflow secret was read from and the purpose it was served for,
# and `credsvc: refused` for a refusal, which carries the sentence the boundary
# answers with. A FAIL stops S3 and returns to the owner as a decision (B3.7 is
# the gate, B3.9 the flip's).
#
# Usage:      bash scripts/live-checks/check194.sh
#             bash scripts/live-checks/check194.sh --dry-run      (prints the
#             plan and calls NOTHING: no gcloud, no fugaro, no curl)
#             bash scripts/live-checks/check194.sh --after-flip   (Check 194's
#             second half: the mounts and the accessor, and the run that was in
#             flight across the re-apply)
# Needs:      bash, mktemp, python3, gcloud (authenticated as you: read Cloud
#             Logging, read `run jobs` and one of its executions — the spec a
#             run in flight across the re-apply was created with — read a
#             secret's own IAM policy, and read the installation's Realtime
#             Database) and fugaro. Nothing is installed and nothing is fetched.
# Where:      installation `fugaro` ONLY — project fugaro-dev, region us-east5.
#             Both are HARD-CODED in this file and never read from `gcloud
#             config get-value project`, and `fugaro doctor` must answer that
#             this is the fugaro-dev installation: a check that read the project
#             from the environment could be pointed at another installation by a
#             stray config, and this one must never be. Never belong.
# Creates:    one throwaway run in the SANDBOX repository you name (and, with
#             --after-flip, one more), its branch and its pull request, which
#             **stay on that repository**: this check deletes no branch and
#             closes no pull request, and its row is yours to settle. It writes
#             nothing into any repository and nothing into the installation: the
#             record it reads (`creds/workflowsecrets/<slug>`) is READ, never
#             written, for the same reason Check 193 reads its own and never
#             writes it.
# Costs:      wall clock, not money: about 40 minutes per run at the terminal.
#             Every model call of that run is answered by the credentials
#             service, which is the subject of the check.
# Runs:       by YOU. No agent runs any step of it, and no agent has: what it
#             measures is a live installation. Its own test (check194_test.sh)
#             drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else — this check creates no
#             cloud resource. It runs from the exit trap too, so a Ctrl-C leaves
#             nothing of the check's behind.
# Output:     progress on stderr, ONE block on stdout headed CHECK 194 RESULT,
#             then ... RESULT END (exit N) — to copy back to the coordinator, and
#             nothing else. Exit 0: every criterion that was measured held. 1: a
#             measured criterion did not hold, or a guard stopped the check
#             before it could run. 2: a row could not be measured for a reason
#             that is this check's own setup rather than the installation (the
#             block says which, by name). In the block, a key the run never
#             reached reads `FAIL — not reached`, which is a row that was NOT
#             measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value, no
# authorization header and **no workflow secret's value**. That last one is this
# check's own subject and it is not printed because it is never read: the record
# holds container IDs (a container in Secret Manager never a value, G18), the
# audit line holds the container's name, and the job's own spec holds the secret
# REFERENCE its variable is fed from. The one credential this check holds at all
# is the gcloud access token it mints to READ the installation's Realtime
# Database record, and it is used where it is minted.
#
# Two of the plan's own words are read here as the merged code reads them, and
# the difference is D2313's (2026-11-13, after this check was planned): a
# repository-level secret is accepted and ignored, so the container a workflow
# secret's value comes from is the PROJECT's copy and never the repository's own
# (internal/cli/update_workflowsecrets.go's workflowSecretMountID). "One
# per-repository and one project workflow secret" is therefore the operator's
# ALLOW-LIST read the two ways it can be written: one entry that lists the one
# repository this check reads (that repository's own secret, and a container of
# the installation's project like the other one), and one that admits the
# workspace. The record names both, both are mounted before the flip and both
# are fetched after it, and a repository the first entry does not list is
# refused that one and served the other — which is the per-repository half of
# "another repository's job is refused them" as it stands.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK194_REPO (REQUIRED: the sandbox repository under test, whose workflow
# declares the two secrets); CHECK194_OTHER_REPO (REQUIRED: a second sandbox
# repository, which the per-repository entry does NOT list and the project-wide
# one does); CHECK194_ENV_ONE (the variable the per-repository secret becomes,
# default DEPLOY_TOKEN), CHECK194_ENV_TWO (the project-wide one, default
# PROJECT_TOKEN); CHECK194_INFLIGHT (REQUIRED with --after-flip: the id of the
# run of CHECK194_REPO that was already going when you re-applied it);
# CHECK194_RUN_SECONDS (how long a run is waited for; default 2400),
# CHECK194_POLL_SECONDS, CHECK194_LOG_WAIT_SECONDS, CHECK194_LOG_POLL_SECONDS,
# CHECK194_LOG_AGE, CHECK194_LOG_LIMIT, CHECK194_CONFIG (the installation's
# local config file, read for budget.rtdb_url), CHECK194_DRY_RUN and
# CHECK194_KEEP_SCRATCH. None of them can point this check at another project or
# another region: those two are constants in this file.
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
need() { command -v "$1" >/dev/null 2>&1 || die "Check 194 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  need bash
  need mktemp
  need python3 "every gcloud and fugaro document below is read by the reader in the scratch directory"
  need gcloud
  need fugaro
  need curl "the installation's workflow-secrets record is read out of its own Realtime Database"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk194-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
PRINTED=""
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
AT=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk194.XXXXXXXX") || die "Check 194 cannot make a scratch directory"
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
    # The knob docs/secrets-brokering/check194-steps.md tells the owner to set
    # to read what the check wrote is not a directory that could not be removed:
    # the row below says which of the two it is, and only the second is a FAIL.
    SCRATCH_KEPT=yes
    say "keeping $SCRATCH (CHECK194_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created, undone. There is nothing of this check's in
# the installation or the project — the run it launches lives on the sandbox
# repository and its row is the owner's to settle, exactly as Check 193's are —
# so this row is about the one thing that COULD have been left: the scratch
# directory, which is removed by remove_scratch and read back there.
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
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK194_KEEP_SCRATCH is set), at $SCRATCH"
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
# this is the second line of that defence rather than the first. A workflow
# secret's VALUE is the thing this check exists to see read without ever reading
# it: it is redacted here by the same patterns as any other credential, which is
# why a message that somehow quoted one would still not print it.
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
# — the service's own log lines, a run's row, the repository's workflow-secrets
# record, a job's own spec, a secret's own IAM policy and the installation's own
# job list — so that no reading of a JSON shape is spelled twice in this script
# and none of them is done in bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 194's reader: one file, one reading per subcommand, no credential in
any of them.

audit  argv: the service's lines' file, a run id. Two kinds of line for that
       run, and nothing else: `audit=<status>|<credential>|<tier>|<purpose>|
       <job>|<caller>` for each `credsvc: audit` line — the status is `ok` for a
       call the service served and a refusal's status otherwise, the credential
       is the NAME of what it served and never a value, and the purpose is what
       tells a workflow secret's line from the git token's or the model
       credential's — and `refused=<status>|<sentence>` for each of the
       service's OWN refusal lines (`credsvc: refused`), whose `message` field
       is the sentence the run was sent.
run    argv: the listing's file, a run id. Prints that run's row field by
       field, `found=no` when the listing does not hold it, and the execution
       the run's own record names (`projects/<project>/locations/<region>/
       jobs/<job>/executions/<name>`), which is what names the job the run ran
       on and the spec it was created with.
wfs    argv: the credentials service's record for one repository. Prints its
       version, one `workflow=<workflow>|<env>|<id>` line per entry, the number
       of entries, and a `stray=` line for EVERY key the record carries that
       this release does not model — at the top or inside an entry. That last
       one is how "no value, ever" is read (G18): the record's shape is closed,
       a member that could carry a value would be a key nobody here knows, and a
       reading that ignored unknown members would read a value-bearing record as
       a clean one.
job    argv: one `gcloud run jobs describe --format=json` document (or one
       execution's), and the environment variable names of interest. Prints the
       job's name, the account its containers run as, whether the credentials
       service's URL is in its env at all, the names the plan states as ignored
       (`FUGARO_IGNORED_SECRETS`, D2313), and for each variable of interest
       whether it is fed from a Secret Manager container (`env=NAME|secret|<id>`
       — the container's own short name), carries a plain value
       (`env=NAME|value`) or is not there at all (`env=NAME|absent`).
jobs   argv: `gcloud run jobs list --format=json`'s file, a repository, and the
       names of interest. Prints one `job=<name>|<account>|<url>` line per job of
       that repository (matched by the job's own FUGARO_REMOTE, and by the label
       the installation writes — gcloud's `run jobs list` forces the v1 API, so
       a container and its account sit at spec.template.spec.template.spec),
       then one `mount=<ENV>|<id>|<job>` line per (job, variable) fed from a
       container and one `present=<ENV>|<job>` line per (job, variable) the job
       carries a value for in any form, and finally the counts.
slug   argv: the same job list's file, a repository. Prints the key that
       repository's `creds/workflowsecrets` record is written under, which is
       the `fugaro_repo` label of its own jobs — the repository's SLUG
       (`<readable>-<16 hex>`, task.Slug) and never its name.
iam    argv: one `gcloud secrets get-iam-policy --format=json` document. Prints
       one `accessor=<member>` line per member of every binding whose role can
       read a version (`roles/secretmanager.secretAccessor`), one `role=<role>|
       <member>` line for every other binding, and the counts. Nothing of it is
       a value: a binding is a name and a role.
inflight argv: the job list's file, a repository, a run's started_at. Prints the
       repository's LATEST job updateTime and whether the run started before it.
"""
import datetime
import json
import re
import sys

WORD = re.compile(r"[^A-Za-z0-9._:@/+-]")
# The roles that can read a version of a secret. secretVersionManager adds and
# destroys versions rather than reading one, and it is listed as its own shape
# because the plan's row is about the ACCESSOR the job's account holds.
ACCESSOR_ROLES = ("roles/secretmanager.secretAccessor",)


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


def spec_of(doc):
    """The container spec of a job or of one execution of it.

    The two live at different depths — a Job carries
    spec.template.spec.template.spec and an Execution spec.template.spec — and a
    reading that pinned one of them would answer `absent` for every variable of
    the other. So the known paths are tried in order and the document is finally
    searched for the first containers list, which is the one fact both shapes
    carry. The node this returns is also where the account the containers run as
    is written, which is the account the accessor row is about."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec")):
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


def source_of(entry):
    """What feeds one environment variable: the container it is read from, a
    plain value, or nothing at all.

    A mount is `valueSource.secretKeyRef.secret` — the deployment mounts
    `var.secrets[env.value]`, which is a DERIVED Secret Manager ID — and its
    short name is the last segment with any version suffix dropped: the API
    spells a version reference as `name:version` and a resource path as
    `projects/<p>/secrets/<id>`, and both are the same container."""
    src = entry.get("valueSource") or {}
    ref = ((src.get("secretKeyRef") or {}).get("secret")
           or (src.get("secretKeyRef") or {}).get("name") or "")
    if ref:
        return "secret", ref.split("@")[-1].split(":")[-1].split("/")[-1]
    if entry.get("value") not in (None, ""):
        return "value", ""
    return "absent", ""


def jobs_of(path, repo):
    """The jobs of one repository, as (name, containers, updateTime, label,
    account) — matched by the repository's own FUGARO_REMOTE first, and by the
    label the installation writes (`fugaro_repo: <slug>-<16 hex>`) where no
    remote is readable, because a reading that leaned on the label alone would
    miss a job whose label the installation had not written and one that leaned
    on the remote alone would miss a job whose env could not be read back. The
    label is the repository's own slug, which is the key its
    creds/workflowsecrets record is written under (label_of), so it is returned
    with the rest, and the account its containers run as is returned with them:
    an accessor binding on a container is read against THAT account (the flip's
    no-accessors row), and a caller that had to look the job up again by name
    would be reading a second document for a fact this one already carries."""
    slug = repo.lower().replace("/", "-")
    out = []
    for job in load(path) or []:
        if not isinstance(job, dict):
            continue
        name = ((job.get("metadata") or {}).get("name")) or ""
        if not name:
            continue
        containers = containers_of(job)
        spec = spec_of(job)
        account = (spec.get("serviceAccountName") if isinstance(spec, dict) else "") or ""
        label = ((job.get("metadata") or {}).get("labels") or {}).get("fugaro_repo") or ""
        owns = (label.startswith(slug + "-")
                and re.fullmatch(r"[0-9a-f]{16}", label[len(slug) + 1:]) is not None)
        remote = ""
        for c in containers:
            remote = (env(c).get("FUGARO_REMOTE") or {}).get("value") or remote
        if not owns and remote != "https://" + repo + ".git" and not remote.endswith("/" + repo + ".git"):
            continue
        out.append((name, containers, ((job.get("metadata") or {}).get("updateTime")) or "",
                    label, account))
    return out


def label_of(path, repo):
    """The key `fugaro update --repo` writes this repository's
    creds/workflowsecrets record under.

    The record is keyed by the repository's SLUG and never by its name: the slug
    is the readable path plus "-" and 16 hex of sha256, which is also what the
    installation writes as its jobs' `fugaro_repo` label — so the label IS the
    key. A path spelled out of `owner/name` is a node nobody writes: the GET
    answers 404 on every installation, and a check reading one would call a
    record that is there "not there" and measure nothing."""
    for _name, _containers, _up, label, _account in jobs_of(path, repo):
        if re.fullmatch(r"[a-z0-9][a-z0-9-]*-[0-9a-f]{16}", label):
            return label
    return ""


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


def bare_member(m):
    """An IAM member with its type prefix dropped (`serviceAccount:`, `user:`,
    `group:` or `domain:`) — and with nothing else touched.

    The account a job's containers run as is the bare address, while a binding's
    member is the same address with its type in front of it, so the two have to
    be brought to one spelling somewhere. Here is where: a row that compared
    them as they are written would find no accessor the job holds and would PASS
    a container the job can still read — the one direction this row may not fail
    in. A prefix outside that set (`deleted:`, a `principalSet:` URL) is left
    alone: none of them is an account, and a member that is not one must not
    become equal to one by a rewrite."""
    s = str(m)
    for prefix in ("serviceAccount:", "user:", "group:", "domain:"):
        if s.startswith(prefix):
            return s[len(prefix):]
    return s


def spec_facts(doc, want):
    """The four things a job's own document answers: its name, the account its
    containers run as, whether the credentials service's URL is in its env, and
    the names the plan states as ignored."""
    spec = spec_of(doc)
    name = ((doc.get("metadata") or {}).get("name")) or "-"
    sa = (spec.get("serviceAccountName") if isinstance(spec, dict) else "") or ""
    url, ignored = "absent", ""
    for c in containers_of(doc):
        e = env(c)
        if "FUGARO_CREDENTIALS_SERVICE_URL" in e:
            url = "present"
        if "FUGARO_IGNORED_SECRETS" in e:
            ignored = (e["FUGARO_IGNORED_SECRETS"].get("value") or "")
    return word(name, 120), word(sa, 160) if sa else "-", url, word(ignored, 300) if ignored else "-"


def sources(doc, want):
    """Whether each variable of interest is fed from a container, carries a
    plain value, or is not there at all — read for EVERY container of the job
    and not for the first one, because a job's env is a list of containers and a
    reading that stopped at the first would answer `absent` for a mount the
    second one carries."""
    out = {}
    for c in containers_of(doc):
        for name, entry in env(c).items():
            if name not in want or name in out:
                continue
            kind, ident = source_of(entry)
            out[name] = (kind, ident)
    return {name: out.get(name, ("absent", "")) for name in want}


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
                word(p.get("credential"), 120),
                word(p.get("tier"), 8),
                word(p.get("purpose"), 40),
                word(p.get("job"), 120),
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
                    "started_at", "execution"):
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
elif cmd == "wfs":
    # argv: the repository's own record, as JSON. Its shape is CLOSED and that is
    # the reading: version and workflows, and per entry env and id. Every key
    # this release does not model is printed as a stray, so a record a later
    # release gave a value-bearing member answers a FAIL rather than a clean
    # shape — the check cannot know what an unknown member holds, and G18 says
    # what a record may not.
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        print("version=%s" % word(doc.get("version"), 8))
        wfs = doc.get("workflows")
        wfs = wfs if isinstance(wfs, dict) else {}
        n = 0
        for key in sorted(doc.keys()):
            if key not in ("version", "workflows"):
                print("stray=%s" % word(key, 60))
        for workflow in sorted(wfs.keys()):
            entries = wfs[workflow] if isinstance(wfs[workflow], list) else []
            for e in entries:
                if not isinstance(e, dict):
                    print("stray=entry:%s" % word(workflow, 60))
                    continue
                for key in sorted(e.keys()):
                    if key not in ("env", "id"):
                        print("stray=%s" % word(key, 60))
                n += 1
                print("workflow=%s|%s|%s" % (
                    word(workflow, 60), word(e.get("env"), 80), word(e.get("id"), 200)))
        print("rows=%s" % n)
elif cmd == "job":
    doc = load(sys.argv[2]) or {}
    want = [w for w in (sys.argv[3].split(",") if len(sys.argv) > 3 else []) if w]
    name, sa, url, ignored = spec_facts(doc, want)
    print("name=%s" % name)
    print("serviceaccount=%s" % sa)
    print("url=%s" % url)
    print("ignored=%s" % ignored)
    found = sources(doc, want)
    for w in want:
        kind, ident = found.get(w, ("absent", ""))
        if kind == "secret":
            print("env=%s|secret|%s" % (w, word(ident, 200)))
        else:
            print("env=%s|%s" % (w, kind))
elif cmd == "jobs":
    repo = sys.argv[3]
    want = [w for w in (sys.argv[4].split(",") if len(sys.argv) > 4 else []) if w]
    n = 0
    urls = 0
    present = 0
    for name, containers, _up, _label, account in jobs_of(sys.argv[2], repo):
        n += 1
        # The account is put back where spec_facts reads it from: the row
        # `job=<name>|<account>|<url>` is what the flip's no-accessors row takes
        # the account to compare with a container's policy out of, and a document
        # synthesized without it would answer "-" for every job of every
        # installation — a row that could never be measured.
        doc = {"metadata": {"name": name},
               "spec": {"template": {"spec": {"template": {"spec": {
                   "containers": containers, "serviceAccountName": account}}}}}}
        jname, sa, url, ignored = spec_facts(doc, want)
        if url == "present":
            urls += 1
        print("job=%s|%s|%s" % (jname, sa, url))
        if ignored != "-":
            print("ignored=%s|%s" % (jname, ignored))
        found = sources(doc, want)
        for w in want:
            kind, ident = found.get(w, ("absent", ""))
            if kind == "secret":
                print("mount=%s|%s|%s" % (w, word(ident, 200), jname))
                present += 1
            elif kind == "value":
                print("present=%s|%s" % (w, jname))
                present += 1
    print("jobs=%s" % n)
    print("urls=%s" % urls)
    print("carried=%s" % present)
elif cmd == "slug":
    # argv: the job list's file, a repository. Prints the key the repository's
    # creds/workflowsecrets record is written under — the `fugaro_repo` label of
    # its own jobs, which is the repository's slug and not its name (label_of).
    print("slug=%s" % word(label_of(sys.argv[2], sys.argv[3]), 100))
elif cmd == "iam":
    # argv: one secret's own IAM policy, as JSON. `accessor=` is every member
    # that can read a version of that container; `role=` carries every other
    # binding, so the row can say what it found rather than only what it did not.
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        bindings = doc.get("bindings") or []
        bindings = bindings if isinstance(bindings, list) else []
        accessors = 0
        for b in bindings:
            if not isinstance(b, dict):
                continue
            role = b.get("role") or ""
            for m in b.get("members") or []:
                if role in ACCESSOR_ROLES:
                    accessors += 1
                    print("accessor=%s" % word(bare_member(m), 200))
                else:
                    print("role=%s|%s" % (word(role, 120), word(m, 200)))
        print("bindings=%s" % len(bindings))
        print("accessors=%s" % accessors)
elif cmd == "inflight":
    # argv: the job list's file, a repository, a run's started_at. Prints the
    # repository's LATEST job updateTime and whether the run started before it.
    # `fugaro update --repo` re-applies the repository and updates every job of
    # it, so the latest updateTime among them is the moment the re-apply reached
    # that repository — which is the instant the run has to be on the far side of
    # for U9 to be about it at all.
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check194.sh -- the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the access token or a command's environment"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`.
  # Check 194 is a reading on ONE installation: run anywhere else and every line
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
    fact "fugaro-version" FAIL "$v — a development build is not the release this check is about: B3.6's check runs on the release that carries B3.1–B3.6, and a checkout build resolves no released credentials-service image to deploy (internal/cli's resolveCredentialsImage). Install the release under test and run it again"
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
  [ -n "$REPO" ] || stop "CHECK194_REPO must name the sandbox repository under test, as <workspace>/<repository>"
  [ -n "$OTHER_REPO" ] || stop "CHECK194_OTHER_REPO must name a second sandbox repository — one the per-repository workflow_secrets entry does NOT list and the project-wide one does: the check reads the same record against a second repository, which is the only place the plan's own phrase about another repository being refused them can be read at all"
  guard_one_repo "$REPO" CHECK194_REPO
  guard_one_repo "$OTHER_REPO" CHECK194_OTHER_REPO
  [ "$REPO" != "$OTHER_REPO" ] || stop "CHECK194_REPO and CHECK194_OTHER_REPO are the same repository (${REPO}): the check reads a record against two repositories and one of them alone settles nothing"
  case "$ENV_ONE" in
  "" | *[!A-Z0-9_]*) stop "CHECK194_ENV_ONE must be an upper-case environment variable name and it is '$ENV_ONE'" ;;
  esac
  case "$ENV_TWO" in
  "" | *[!A-Z0-9_]*) stop "CHECK194_ENV_TWO must be an upper-case environment variable name and it is '$ENV_TWO'" ;;
  esac
  [ "$ENV_ONE" != "$ENV_TWO" ] || stop "CHECK194_ENV_ONE and CHECK194_ENV_TWO are the same variable (${ENV_ONE}): the check reads two workflow secrets of one workflow and one variable cannot become two"
  case "$MODE" in
  flip)
    [ -n "$INFLIGHT" ] || stop "CHECK194_INFLIGHT must name the run of ${REPO} that was already going when you re-applied it (<run id>, the shape 'fugaro runs ls' prints): U9's reading is that THAT run kept the spec it was created with and still finished, and a run this check picked itself could be one that ended hours before the re-apply"
    ;;
  esac
  fact repos PASS "${REPO} (under test) and ${OTHER_REPO} (the second repository), sandbox repositories of the fugaro-dev installation only and never belong; the two variables of the workflow under test are ${ENV_ONE} and ${ENV_TWO}"
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
  flip-one) printf '%s-0198' "$RUN_BASE" ;;
  *) die "Check 194 has no run id for the label '$1'" ;;
  esac
}

# task_write FILE LABEL RID: the throwaway task. Its first line is the `# Task:`
# line `fugaro watch` and the pull request title come from, and what it asks for
# is the smallest thing that makes a run commit and open a pull request — which
# is what puts its model calls through the credentials service, the subject of
# this check, and what makes it reach the stages whose command reads the
# workflow's secrets. It is written by the FILE argument and never by a
# here-document on a command line (CLAUDE.md: an unquoted heredoc runs any
# backticked text).
task_write() { # task_write FILE LABEL RID
  {
    printf '# Task: Check 194 — the %s run (%s)\n\n' "$2" "$3"
    printf 'Add one line to `CHK194.md` at the repository root:\n\n'
    printf '    check 194 %s %s\n\n' "$2" "$3"
    printf 'Commit it on your own branch and open a pull request for it, then stop.\n'
    printf 'Change nothing else in the repository.\n'
  } >"$1"
}

# launch_into LABEL RID TASKFILE: one throwaway run of the repository under
# test, printed as its terminal row. `fugaro run` returns AT LAUNCH, so the row
# is polled for by run id until the listing's own `terminal` word is yes — a
# reading taken straight after a launch is an in-flight row, which is a FAIL no
# run earned. A failure to launch or to read the row is not printed as a row:
# the reason goes to the launch-err file, which the caller's fact reads with
# launch_err. It is a FILE and not a variable because the callers read the row
# through a command substitution — `row=$(launch_into ...)` — and a variable set
# in there is set in a subshell: the caller would print a FAIL row with an empty
# reason, which is a row the owner cannot act on.
launch_into() {
  local label=$1 rid=$2 task=$3 rc=0 waited=0 row=""
  printf '%s' "" >"$SCRATCH/launch-err"
  say "launching the ${label} run (${rid}) in ${REPO} — it goes on in the cloud"
  fugaro run --repo "$REPO" --project fugaro --run-id "$rid" \
    --title "Check 194: ${label}" \
    --task-file "$task" --json >"$SCRATCH/run-$label.json" 2>"$SCRATCH/run-$label.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    printf '%s' "fugaro run for the ${label} run (${rid}) exited ${rc} and never launched one: $(last_lines "$SCRATCH/run-$label.err")" >"$SCRATCH/launch-err"
    return 1
  fi
  while :; do
    row=""
    if fugaro runs ls --repo "$REPO" --project fugaro --no-cache --json >"$SCRATCH/rows-$label.json" 2>>"$SCRATCH/run-$label.err"; then
      row=$(reader run "$SCRATCH/rows-$label.json" "$rid" || true)
    fi
    if [ "$(field_of found "$row")" = yes ] && [ "$(field_of terminal "$row")" = yes ]; then break; fi
    if [ "$waited" -ge "$RUN_SECONDS" ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  printf '%s\n' "$row" >"$SCRATCH/row-$label"
  if [ "$(field_of found "$row")" != yes ]; then
    printf '%s' "the ${label} run (${rid}) does not appear in 'fugaro runs ls --repo ${REPO}' after ${waited}s: it was launched but its row is not in the listing, so nothing about it is read" >"$SCRATCH/launch-err"
    return 1
  fi
  if [ "$(field_of terminal "$row")" != yes ]; then
    printf '%s' "the ${label} run (${rid}) had not reached a final status after ${waited}s: status=$(field_of status "$row"). A row still in flight is not an answer about the credentials service — raise CHECK194_RUN_SECONDS, or read that run's row yourself" >"$SCRATCH/launch-err"
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
# line are the flip's `served` row (no line of the run's own is exactly what a
# run that fetched nothing looks like) and its U9 row, so a failed read taken
# for silence would PASS the check on nothing.
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

# audit_fetched FILE RID: the containers the service served this run a WORKFLOW
# SECRET from, one per line, deduplicated — the `credential` of every served
# line whose purpose is `workflow-secret` (internal/credsvc's audit.go names the
# container the value was read from and no tier, which is what tells one of
# these lines from the git token's or a model credential's). A line of another
# purpose is not read here at all: a switched run's git and model calls are
# served too, and a row that counted them would call the record's containers
# served when they were not.
audit_fetched() {
  reader audit "$1" "$2" | sed -n 's/^audit=ok|\([^|]*\)|[^|]*|workflow-secret|.*/\1/p' | sort -u
}

# audit_refused FILE RID: the sentences the service refused this run with, one
# per line (`credsvc: refused`). It is the boundary's own text and the only
# place a refusal says WHAT it refused for.
audit_refused() {
  reader audit "$1" "$2" | sed -n 's/^refused=[^|]*|//p'
}

# record_key REPO: the key this repository's record is written under — the
# `fugaro_repo` label of its own jobs, which is the repository's SLUG
# (`<readable>-<16 hex>`, task.Slug) and NOT its name. `creds/workflowsecrets`
# is keyed by the slug (creds.PathWorkflowSecrets, written by `fugaro update
# --repo` from the spec it applied), so a path spelled out of `owner/name` is a
# node nobody writes: the GET would answer 404 on every installation, and this
# check would report a record that is there as one that is not.
record_key() { # record_key REPO
  reader slug "$SCRATCH/jobs.json" "$1" | sed -n 's/^slug=//p'
}

# record_read SLUG OUT: the repository's workflow-secrets record, read from the
# installation's own Realtime Database at the path `fugaro update --repo` writes
# it to (creds.PathWorkflowSecrets, D2073 (a)), keyed by the repository's SLUG
# and not its name (record_key). It is READ and never written: the record is
# what the service serves a run from, and a check that rewrote one and failed to
# put it back would point every fetch of that repository at another container.
#
# On stdout it prints the REASON it could not read the record, and the caller is
# what puts it in the block: the body curl wrote to OUT is a database answer and
# never a diagnosis (a 404's body is `null`, a 000's file is empty), so a caller
# that built the row out of OUT would print `FAIL — null` and the reason would
# land above the block, outside it.
record_read() { # record_read SLUG OUT
  local slug=$1 out=$2 code
  code=$(curl -sS -o "$out" -w '%{http_code}' --max-time 30 \
    -H "Authorization: Bearer ${AT}" \
    "${RTDB%/}/creds/workflowsecrets/${slug}.json" 2>"$SCRATCH/rtdb.err") || code=000
  case "$code" in
  200) return 0 ;;
  404)
    printf 'the record is not there (HTTP 404): this repository has no creds/workflowsecrets/%s node, which is the state of an installation whose "fugaro update --repo" has not run there since the release that writes one. Run it in that repository and this check again' "$slug"
    return 1
    ;;
  000) printf 'the record could not be read: %s' "$(last_lines "$SCRATCH/rtdb.err")" ;;
  *) printf 'the record could not be read (HTTP %s)' "$code" ;;
  esac
  printf ' at %s/creds/workflowsecrets/%s.json' "$RTDB" "$slug"
  return 1
}

# record_env_id FILE ENV: the container the record names for one environment
# variable, or "" when it names none — the reading the whole check rests on,
# because every other row compares a job's own mount with it.
record_env_id() { # record_env_id FILE ENV
  reader wfs "$1" | sed -n "s/^workflow=[^|]*|$2|//p" | head -n 1
}

# record_strays FILE: the record's keys this release does not model, one per
# line (G18's reading). Empty is the only clean answer.
record_strays() { reader wfs "$1" | sed -n 's/^stray=//p' | sort -u; }

# record_envs FILE: every environment variable the record names, one per line.
record_envs() { reader wfs "$1" | sed -n 's/^workflow=[^|]*|\([^|]*\)|.*/\1/p' | sort -u; }

# ------------------------------------------------------------------ the steps
# step_jobs: the installation's job list, once, for every row below that reads a
# job — the record's key (a repository's slug is its own jobs' label, task.Slug),
# the mounts, the variables a job hands out and the re-apply's clock all come out
# of it. A read that did not answer leaves an empty document, which is what
# JOBS_OK says: every row that needed it says so by name rather than reading
# mounts off a list nobody wrote.
step_jobs() {
  if ! gcloud run jobs list --project="$P" --region="$L" --format=json >"$SCRATCH/jobs.json" 2>"$SCRATCH/jobs.err"; then
    : >"$SCRATCH/jobs.json"
    JOBS_OK=no
    JOBS_WHY="gcloud run jobs list exited non-zero: $(last_lines "$SCRATCH/jobs.err")"
    return 0
  fi
  JOBS_OK=yes
}

# step_record: the record the service serves this repository's runs from — the
# one row of this check that needs neither a run nor a flip to be read, and the
# row every other one is read against.
#
# Four things are read off it and they are one fact: the node is there (a
# repository whose `fugaro update --repo` predates B3.1 has none, and the service
# then serves its runs nothing — fail-closed, and a state in which nothing else
# here can be measured); its shape is CLOSED, because every member of it is a
# NAME and a member nobody here models is one that could carry a workflow
# secret's value (G18) — a stray key is a FAIL and not a shrug; it names a
# container for each of the two variables the sandbox declares; and each of those
# is a PROJECT container of this installation (`fugaro-project-<installation>-…`,
# gcp.ProjectSecretID) and never a repository's own
# (`fugaro-<readable slug>-…`, gcp.SecretID), which is D2313 read off the wire: a
# repository-level secret is accepted and ignored, so every legal workflow
# secret's value comes from the installation's project-wide copy and never from
# the repository's own container.
step_record() {
  local key got reason strays bad="" var id
  RECORD_STOP=no
  if [ "$JOBS_OK" != yes ]; then
    unmeasured record "${JOBS_WHY}. The record is keyed by the repository's own task slug, which is the 'fugaro_repo' label of its jobs, and the mounts it is read against are read from that same list: run 'gcloud run jobs list --project=${P} --region=${L}' yourself, then this check again"
    RECORD_STOP=yes
    return 0
  fi
  key=$(record_key "$REPO")
  if [ -z "$key" ] || [ "$key" = "-" ]; then
    unmeasured record "no job of ${REPO} in the installation's job list carries its 'fugaro_repo' label, which is the slug its creds/workflowsecrets record is keyed by (task.Slug: <readable>-<16 hex>), so the record cannot be read: run 'fugaro update --repo' in ${REPO}, then this check again"
    RECORD_STOP=yes
    return 0
  fi
  REC_KEY=$key
  if ! reason=$(record_read "$key" "$SCRATCH/wfs-$key.json"); then
    unmeasured record "${reason:-the record could not be read}"
    RECORD_STOP=yes
    return 0
  fi
  got=$(reader wfs "$SCRATCH/wfs-$key.json")
  if [ "$(field_of found "$got")" != yes ]; then
    unmeasured record "${REPO}'s creds/workflowsecrets/${key} node holds no object, so no workflow secret is recorded for it"
    RECORD_STOP=yes
    return 0
  fi
  strays=$(record_strays "$SCRATCH/wfs-$key.json" | tr '\n' ' ' | sed 's/ *$//')
  if [ -n "$strays" ]; then
    fact record FAIL "${REPO}'s record (creds/workflowsecrets/${key}) carries key(s) this release does not model: ${strays}. That record's shape is CLOSED — version and workflows, and per entry env and id — and every member of it is a NAME (internal/creds/workflowsecrets.go: no field of this record is a credential's value and none may ever be, G18). A member nobody here knows is a member that could hold a value, which is the one thing this check may not read past: do not edit the record by hand, read internal/creds/workflowsecrets.go against what wrote it, and treat this as a finding about the release"
    RECORD_STOP=yes
    return 0
  fi
  REC_ID_ONE=$(record_env_id "$SCRATCH/wfs-$key.json" "$ENV_ONE")
  REC_ID_TWO=$(record_env_id "$SCRATCH/wfs-$key.json" "$ENV_TWO")
  if [ -z "$REC_ID_ONE" ] || [ -z "$REC_ID_TWO" ]; then
    if [ -z "$REC_ID_ONE" ]; then
      fact record FAIL "${REPO}'s record (creds/workflowsecrets/${key}) names no container for ${ENV_ONE} ($([ -n "$REC_ID_TWO" ] && echo "it does name ${REC_ID_TWO} for ${ENV_TWO}" || echo "it names neither ${ENV_ONE} nor ${ENV_TWO}")), and the sandbox workflow declares both. A name the record does not carry is one no run of this repository can fetch: 'fugaro update --repo' writes a row for every declared secret whose container its plan resolved, and a name it leaves out is one whose project-wide copy the operator's workflow_secrets allow-list does not admit to this repository (D1262, D2313). Read the allow-list in this machine's local config (workflow_secrets.<name>.allow_data_to must list ${REPO}) and run 'fugaro update --repo' in ${REPO} again"
    else
      fact record FAIL "${REPO}'s record (creds/workflowsecrets/${key}) names ${REC_ID_ONE} for ${ENV_ONE} and no container for ${ENV_TWO}, and the sandbox workflow declares both. A name the record does not carry is one no run of this repository can fetch: 'fugaro update --repo' writes a row for every declared secret whose container its plan resolved, and a name it leaves out is one whose project-wide copy the operator's workflow_secrets allow-list does not admit to this repository (D1262, D2313). Read the allow-list in this machine's local config (workflow_secrets.<name>.allow_data_to must list ${REPO}) and run 'fugaro update --repo' in ${REPO} again"
    fi
    RECORD_STOP=yes
    return 0
  fi
  if [ "$REC_ID_ONE" = "$REC_ID_TWO" ]; then
    fact record FAIL "${REPO}'s record names the SAME container (${REC_ID_ONE}) for both ${ENV_ONE} and ${ENV_TWO}: the two are two workflow secrets of one workflow and one container for both would serve a run the same value under two names, which is not two secrets"
    RECORD_STOP=yes
    return 0
  fi
  for var in "$ENV_ONE" "$ENV_TWO"; do
    id=$(record_env_id "$SCRATCH/wfs-$key.json" "$var")
    case "$id" in
    fugaro-project-${INSTALL}-*-[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
    *) bad="$bad ${var} -> ${id};" ;;
    esac
  done
  if [ -n "$bad" ]; then
    fact record FAIL "${REPO}'s record names a container that is not a project secret of this installation (${INSTALL}):${bad:0:400} A derived container is either a repository's own, 'fugaro-<readable slug>-<logical>-<16 hex>' (gcp.SecretID), or the installation's project-wide copy, 'fugaro-project-<installation>-<logical>-<16 hex>' (gcp.ProjectSecretID) — and under D2313 the record may only ever name the second: a repository-level secret is accepted and ignored, so its own container is not one the plan mounts and not one a fetch may read (internal/cli/update_workflowsecrets.go's workflowSecretMountID). Read the allow-list entry and 'fugaro update --repo' in ${REPO}, then this check again"
    RECORD_STOP=yes
    return 0
  fi
  fact record PASS "${REPO}'s record (creds/workflowsecrets/${key}, version $(field_of version "$got"), $(field_of rows "$got") row(s)) names a project container of this installation for both variables — ${ENV_ONE} -> ${REC_ID_ONE}, ${ENV_TWO} -> ${REC_ID_TWO} — and carries no key this release does not model, so no member of it can hold a value (G18, D2073)"
}

# step_mounts: F33's row, and the switch mode's whole reading of the mounts. The
# fetch reads the container the mount reads (D1267), so the two have to be the
# same container: every variable of interest a job of this repository carries is
# fed from the container the record names for it, no job carries one as a plain
# value (a value the plan wrote into the job is one a fetch could never be seen
# to replace), and the execution the run itself ran on carries both — which is
# what makes the run's own verify a reading about these containers rather than
# about a job's spec somebody edited since.
step_mounts() {
  local got n line var id job want bad="" carried="" tmpl
  if [ "$JOBS_OK" != yes ]; then
    unmeasured mounts "${JOBS_WHY}. The job whose spec this row compares with the record is read from that list"
    return 0
  fi
  got=$(reader jobs "$SCRATCH/jobs.json" "$REPO" "$ENV_ONE,$ENV_TWO")
  n=$(field_of jobs "$got")
  if [ "${n:-0}" -lt 1 ]; then
    unmeasured mounts "no job of ${REPO} carries its FUGARO_REMOTE in 'gcloud run jobs list', so there is no job whose mount could be read: run 'fugaro update --repo' in ${REPO} and this check again"
    return 0
  fi
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    var=${line#mount=}
    var=${var%%|*}
    id=${line#mount=*|}
    id=${id%%|*}
    job=${line##*|}
    want=$(record_env_id "$SCRATCH/wfs-$REC_KEY.json" "$var")
    if [ "$id" != "$want" ]; then
      bad="$bad ${job} is fed ${var} from ${id} and the record names ${want:-nothing};"
    fi
  done < <(printf '%s\n' "$got" | grep '^mount=' || true)
  carried=$(printf '%s\n' "$got" | sed -n 's/^present=\([^|]*\)|.*/\1/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
  if [ -n "$carried" ]; then
    bad="$bad ${carried} is carried by a job of ${REPO} as a PLAIN value and not out of a container;"
  fi
  while IFS= read -r var; do
    [ -n "$var" ] || continue
    case "$var" in "$ENV_ONE" | "$ENV_TWO") ;; *) continue ;; esac
    if ! printf '%s\n' "$got" | grep -q "^mount=$var|"; then
      bad="$bad no job of ${REPO} mounts ${var}, and the record names a container for it;"
    fi
  done < <(record_envs "$SCRATCH/wfs-$REC_KEY.json")
  if [ -z "$EXEC_TEXT" ]; then
    unmeasured mounts "${EXEC_ERR:-the execution the run ran on was not read, and this row is the containers that execution reads from its own environment}"
    return 0
  fi
  for var in "$ENV_ONE" "$ENV_TWO"; do
    want=$(record_env_id "$SCRATCH/wfs-$REC_KEY.json" "$var")
    id=$(env_id "$EXEC_TEXT" "$var")
    if [ -z "$id" ] || { [ -n "$want" ] && [ "$id" != "$want" ]; }; then
      bad="$bad the execution the run itself ran on (${EXEC_NAME}) reads ${var} from ${id:-no container} and the record names ${want:-nothing};"
    fi
  done
  if [ -n "$bad" ]; then
    fact mounts FAIL "${n} job(s) of ${REPO} carry its remote, and the containers the variables of interest are fed from are not the containers ${REPO}'s record names:${bad:0:500} The fetch of S3 reads what the mount reads (F33, D1267: the record is written from the mounts the plan resolved, and the service reads the container the record names), so a run fed from another container is one whose fetched value would not be the value it holds — and the record is what the service serves it from. Run 'fugaro update --repo' in ${REPO} and this check again"
    return 0
  fi
  fact mounts PASS "every variable of interest a job of ${REPO} carries is fed from the container ${REPO}'s record names — ${ENV_ONE} and ${ENV_TWO} on the execution the run itself ran on (${EXEC_NAME}) — and no job carries either as a plain value (F33, D1267)"
}

# step_verify: the run's own verify, and the plan's "a run's verify sees both".
#
# The reading is the run's own row and not the agent's environment: the sandbox
# repository's verify command fails on a variable that is empty
# (check194-steps.md tells the owner to make it so), the run reached the end of
# its stages, and a repository fixed in that shape cannot have finished if either
# value was missing. Which container those values came from is the mounts row
# above, and it is the same container the record names.
step_verify() {
  local status
  status=$(field_of status "$RUN_ROW")
  case "$status" in
  succeeded)
    fact verify PASS "the run (${RUN_ID_ONE}) of ${REPO} ended '${status}' (outcome=$(field_of outcome "$RUN_ROW"), pull request $(field_of pr "$RUN_ROW")): it reached its verify stage, and the sandbox's own verify fails on an empty ${ENV_ONE} or ${ENV_TWO} (check194-steps.md), so both values reached the stage — out of the containers the record names, which the mounts row above reads off the execution the run itself ran on"
    ;;
  *)
    fact verify FAIL "the run (${RUN_ID_ONE}) of ${REPO} ended '${status}' and opened no pull request: $(field_of halt_detail "$RUN_ROW")$(field_of reason "$RUN_ROW") The sandbox's own verify fails on an empty ${ENV_ONE} or ${ENV_TWO}, so a run that could not finish is a value that did not reach it — and its job mounts both today (the mounts row above says out of which containers). Read 'fugaro runs show ${RUN_ID_ONE} --repo ${REPO}' and the run's own stages, then this check again"
    ;;
  esac
}

# step_nomounts: the flip's first row — no job of the repository carries either
# variable in any form. Not "is not fed from the container we expect" but "is not
# there": after the flip the value is in the service and nowhere in the job, and
# a job that still carried one — out of a container or as a plain value — is a
# repository the flip has not reached.
#
# The credentials service's URL is the other half of the same row and it fails
# the other way round: the flip takes the MOUNTS and keeps the URL (B3.8: a
# switched repository's runs still reach the service, and this fetch is one of
# the calls that needs it), so a job with no mount and no URL is a run that would
# fetch nothing and start with the variable empty.
step_nomounts() {
  local got n urls carried
  if [ "$JOBS_OK" != yes ]; then
    unmeasured no-mounts "${JOBS_WHY}. Nothing about the mounts is settled by it"
    return 0
  fi
  got=$(reader jobs "$SCRATCH/jobs.json" "$REPO" "$ENV_ONE,$ENV_TWO")
  n=$(field_of jobs "$got")
  urls=$(field_of urls "$got")
  carried=$(field_of carried "$got")
  if [ "${n:-0}" -lt 1 ]; then
    unmeasured no-mounts "no job of ${REPO} carries its FUGARO_REMOTE in 'gcloud run jobs list', so there is no job whose mounts could be read: run 'fugaro update --repo' in ${REPO} and this check again"
    return 0
  fi
  if [ "${carried:-0}" != 0 ]; then
    fact no-mounts FAIL "${n} job(s) of ${REPO} carry its remote, and ${carried} of them still hand a run a variable the flip should have taken: $(printf '%s\n' "$got" | grep -E '^(mount|present)=' | tr '\n' ' ' | sed 's/ *$//'). After the flip no job of a switched repository holds one (B3.8: the class 'workflow' joins the classes the service serves, and the mount goes with it — the value comes out of the record's container through the service instead). Run 'fugaro update' and 'fugaro update --repo' in ${REPO}, then this check again"
    return 0
  fi
  if [ "${urls:-0}" -ne "$n" ]; then
    fact no-mounts FAIL "${n} job(s) of ${REPO} carry its remote and only ${urls} of them carry FUGARO_CREDENTIALS_SERVICE_URL. The flip takes the mounts and KEEPS the URL (B3.8: the fetch of a switched repository's workflow secrets is a call to that service, and a job with no mount and no URL is a run that would fetch nothing and start with the variable empty — the state this check exists to catch). Run 'fugaro update --repo' in ${REPO} and this check again"
    return 0
  fi
  fact no-mounts PASS "${n} job(s) of ${REPO} carry its remote, none of them hands a run ${ENV_ONE} or ${ENV_TWO} in any form, and every one of them still carries FUGARO_CREDENTIALS_SERVICE_URL (B3.8: the mount goes, the URL stays)"
}

# step_served: the flip's second row — a run whose job mounts neither variable
# finishes all the same, and the service's OWN lines for it name the containers
# the record names under the workflow-secret purpose. Those two together ARE the
# fetch: the value reached the run (its verify demands it, step_verify's
# reasoning) and it can only have come from the service, because the job holds
# none — and the line names the container it was read from, which is the
# container the record names.
step_served() {
  local lines fetched id var misses="" still=""
  lines=$SCRATCH/audit-flip-one.json
  if [ -z "$RUN_ROW" ]; then
    unmeasured served "${RUN_ERR:-the run was not launched, so there is nothing of its to read a fetch off}"
    return 0
  fi
  if [ "$(field_of status "$RUN_ROW")" != succeeded ]; then
    fact served FAIL "the run (${RUN_ID_FLIP}) of ${REPO} — launched after the flip, on a job that mounts neither variable — ended '$(field_of status "$RUN_ROW")' and opened no pull request: $(field_of halt_detail "$RUN_ROW")$(field_of reason "$RUN_ROW") Its verify demands both values, so a run that could not finish is a fetch that did not happen or was refused, and the record is what the service serves it from. Read 'fugaro runs show ${RUN_ID_FLIP} --repo ${REPO}', the service's own lines for it (gcloud logging read with its id) and the run's reason, then this check again"
    return 0
  fi
  if [ -n "$EXEC_TEXT" ]; then
    # `env_of` answers `absent` for a variable the document does not carry, and
    # `absent` is not nothing: a row that read a non-empty answer as "it is
    # there" would report the execution as holding both variables whatever its
    # spec said, which is the one reading this row must never take.
    for var in "$ENV_ONE" "$ENV_TWO"; do
      case "$(env_of "$EXEC_TEXT" "$var")" in
      "" | absent) ;;
      *) still="$still ${var}" ;;
      esac
    done
    if [ -n "$still" ]; then
      fact served FAIL "the run (${RUN_ID_FLIP}) of ${REPO} finished, and the execution it ran on (${EXEC_NAME}) still carries${still} — so the values it read came out of its own job and not from the service, and this row would be reading a mount as a fetch. An execution created by a job template the flip has not reached (the no-mounts row above says the same) is not the reading this row is about: re-apply ${REPO} ('fugaro update --repo'), launch the run again and read it"
      return 0
    fi
  fi
  if ! audit_read "$RUN_ID_FLIP" "$lines"; then
    unmeasured served "$(audit_unread "$lines")"
    return 0
  fi
  fetched=$(audit_fetched "$lines" "$RUN_ID_FLIP")
  for id in "$REC_ID_ONE" "$REC_ID_TWO"; do
    printf '%s\n' "$fetched" | grep -qxF -- "$id" || misses="$misses ${id};"
  done
  if [ -n "$misses" ]; then
    fact served FAIL "the run (${RUN_ID_FLIP}) of ${REPO} finished on a job that mounts neither variable, and the service's own lines for it do NOT name every container ${REPO}'s record names:${misses:0:400} the workflow-secret lines it has name $(printf '%s\n' "$fetched" | tr '\n' ' ' | sed 's/ *$//'). The service audits the container a workflow secret was READ from, with purpose workflow-secret (internal/credsvc/audit.go), so a container the record names that no line names is a value the run did not fetch from it — and its verify demands both values, so either it was served one from somewhere else (its job still holds one: see the no-mounts row) or this run is not the fetch this row reads. Read the run's own lines and ${REPO}'s record, then this check again"
    return 0
  fi
  fact served PASS "the run (${RUN_ID_FLIP}) of ${REPO} finished on a job that mounts neither variable, and the service's own lines for it name BOTH containers ${REPO}'s record names under purpose workflow-secret: $(printf '%s\n' "$fetched" | tr '\n' ' ' | sed 's/ *$//') — so the values its verify read came out of the service, out of the containers the record names (B3.2, B3.4, F33)"
}

# step_noaccessors: the flip's third row — the job's own account holds no
# accessor on either container. It is read off each container's own IAM policy
# (`gcloud secrets get-iam-policy`) and compared with the account the job runs
# as, which the installation's job list names: after the flip the VALUE reaches
# the run through the service, so the account the run's containers run as must
# not be able to read the container itself — D2074's grants are the service's
# (B3.3), and the job's own accessor is what the flip takes away.
step_noaccessors() {
  local id got accessors bad="" found sa
  if [ -z "$REC_ID_ONE" ] || [ -z "$REC_ID_TWO" ]; then
    unmeasured no-accessors "the record's containers were not read (see the record row), so there is no container whose policy this row could read"
    return 0
  fi
  sa=$(run_account)
  if [ -z "$sa" ] || [ "$sa" = "-" ]; then
    unmeasured no-accessors "${ACCOUNT_ERR:-the account that the job of the run runs as was not read}, and this row is that account read against each container's own policy: 'gcloud run jobs describe <the job ${RUN_ID_FLIP} ran on> --project=${P} --region=${L} --format=json' names it"
    return 0
  fi
  for id in "$REC_ID_ONE" "$REC_ID_TWO"; do
    if ! gcloud secrets get-iam-policy "$id" --project="$P" --format=json >"$SCRATCH/iam-$id.json" 2>"$SCRATCH/iam-$id.err"; then
      unmeasured no-accessors "'gcloud secrets get-iam-policy ${id} --project=${P} --format=json' exited non-zero and printed: $(last_lines "$SCRATCH/iam-$id.err"). A read that did not answer is not a container nobody can read, and that is exactly the question this row asks"
      return 0
    fi
    got=$(reader iam "$SCRATCH/iam-$id.json")
    if [ "$(field_of found "$got")" != yes ]; then
      unmeasured no-accessors "the policy of ${id} came back as no document at all, so nothing is read of who may read it"
      return 0
    fi
    accessors=$(printf '%s\n' "$got" | sed -n 's/^accessor=//p')
    if printf '%s\n' "$accessors" | grep -qxF -- "$sa"; then
      bad="$bad ${id} still lists ${sa} as a secretAccessor;"
    fi
    found=$(printf '%s\n' "$got" | sed -n 's/^role=\([^|]*\)|.*/\1/p' | sort -u | tr '\n' ' ' | sed 's/ *$//')
    say "${id}: $(field_of accessors "$got") secretAccessor binding(s)$([ -n "$found" ] && printf ', other roles: %s' "$found")"
  done
  if [ -n "$bad" ]; then
    fact no-accessors FAIL "the account the run's job runs as (${sa}) can still read a container ${REPO}'s record names:${bad:0:500} After the flip the job's own account holds NO accessor on a workflow secret's container — D2074's grants are the credentials service's, one per container the job mounts (B3.3), so the value reaches the run through the service and the job reads nothing itself. Re-apply the repository ('fugaro update --repo'), read the containers' policies again, and treat a grant that comes back as a finding about the release"
    return 0
  fi
  fact no-accessors PASS "neither container ${REPO}'s record names (${REC_ID_ONE}, ${REC_ID_TWO}) lists ${sa} — the account the job ${RUN_ID_FLIP} ran on runs as — as a secretAccessor: the job's own account can read neither, and the values it used came through the credentials service (D2074, B3.3)"
}

# step_other: the plan's "another repository's job is refused them", read where
# it can be: the OTHER repository's own record and its own jobs.
#
# The service decides every call from the record of the repository the VERIFIED
# job account belongs to (G19) and is handed nothing else — the call names one
# environment variable and no repository (internal/runner/workflowsecrets.go). So
# the boundary is exactly "what is in the caller's own record", and the reading
# here is those same two names against a second repository: the entry of the
# allow-list that lists ONE repository is refused to the other — no row of its
# record, no mount on its jobs, no variable in their environment — while the
# entry that admits the workspace is served to it too. A second repository that
# knows the per-repository name at all is the row this check exists to catch.
step_other() {
  local key reason got n names served bad=""
  if [ "$JOBS_OK" != yes ]; then
    unmeasured other "${JOBS_WHY}. Nothing about another repository's record is read from it"
    return 0
  fi
  key=$(record_key "$OTHER_REPO")
  if [ -z "$key" ] || [ "$key" = "-" ]; then
    unmeasured other "no job of ${OTHER_REPO} in the installation's job list carries its 'fugaro_repo' label, which is the slug its creds/workflowsecrets record is keyed by, so the second repository's record cannot be read: run 'fugaro update --repo' in ${OTHER_REPO}, then this check again"
    return 0
  fi
  if ! reason=$(record_read "$key" "$SCRATCH/wfs-$key.json"); then
    unmeasured other "the second repository's record could not be read: ${reason:-no reason given}"
    return 0
  fi
  got=$(reader jobs "$SCRATCH/jobs.json" "$OTHER_REPO" "$ENV_ONE,$ENV_TWO")
  n=$(field_of jobs "$got")
  if [ "${n:-0}" -lt 1 ]; then
    unmeasured other "no job of ${OTHER_REPO} carries its FUGARO_REMOTE in 'gcloud run jobs list', so there is no job of it whose environment could be read: run 'fugaro update --repo' in ${OTHER_REPO} and this check again"
    return 0
  fi
  names=$(reader wfs "$SCRATCH/wfs-$key.json")
  if printf '%s\n' "$names" | grep -q "^workflow=[^|]*|$ENV_ONE|"; then
    bad="$bad its record names a row for ${ENV_ONE};"
  fi
  if printf '%s\n' "$got" | grep -qE "^(mount|present)=$ENV_ONE\|"; then
    bad="$bad one of its jobs carries ${ENV_ONE} at all;"
  fi
  if [ -n "$REC_ID_ONE" ]; then
    if printf '%s\n' "$names" | grep -qF "|$REC_ID_ONE"; then
      bad="$bad its record names the container ${REC_ID_ONE}, which is ${REPO}'s;"
    fi
    if printf '%s\n' "$got" | grep -qF "|$REC_ID_ONE|"; then
      bad="$bad one of its jobs is fed a value from ${REC_ID_ONE}, which is ${REPO}'s;"
    fi
  fi
  if [ -n "$bad" ]; then
    fact other FAIL "${OTHER_REPO} is the second sandbox repository — the one the per-repository entry of the workflow_secrets allow-list does NOT list — and it is not refused ${REPO}'s own workflow secret:${bad:0:400} The service decides a call from the record of the repository the verified job account belongs to and from nothing else (G19: the call names one environment variable and no repository), so a repository whose record names another repository's container, or whose job is fed out of it, or which carries the variable at all, is a boundary that does not hold: remove the entry that admits it (this machine's local config, workflow_secrets.<name>.allow_data_to), run 'fugaro update --repo' in ${OTHER_REPO} and this check again"
    return 0
  fi
  served=$(printf '%s\n' "$names" | sed -n "s/^workflow=[^|]*|$ENV_TWO|//p" | head -n 1)
  if [ -n "$served" ]; then
    served="it IS served ${ENV_TWO} (container ${served})"
  else
    served="it is served ${ENV_TWO} neither"
  fi
  fact other PASS "${OTHER_REPO} is refused ${REPO}'s per-repository workflow secret: its record (creds/workflowsecrets/${key}) names no container for ${ENV_ONE} and none of its ${n} job(s) carries ${ENV_ONE} at all, while the entry that admits the workspace means ${served} — so the two entries of the allow-list are read apart, per repository, which is the only thing the service reads a call by (G19, D1262)"
}

# step_inflight: U9 — a run that was ALREADY GOING when the repository was
# re-applied keeps the container spec it was created with, and still finishes.
# Its execution was created before the re-apply, so its spec is the one the old
# job template wrote, and the reading is exactly that: its row's started_at is
# before the job's own updateTime, its row is terminal and it succeeded, and the
# EXECUTION's own spec — read off `gcloud run jobs executions describe` — still
# names the record's containers while the job template the flip wrote names none.
# A run that did not finish kept nothing worth keeping; a run that started after
# the re-apply was launched by the template the flip wrote and is not the U9 case
# at all.
#
# The service's own lines for that run are deliberately NOT this row's reading.
# The flip takes the mounts and keeps the URL (B3.8), so a run in flight across
# the re-apply is served by the service before and after it, and a reading that
# took a line of its own for the re-apply having reached into the run would fail
# every run that behaved correctly.
step_inflight() { # step_inflight RID
  local rid row started_at got listed=$SCRATCH/jobs.json execpath bare spec
  local mounts tmounted tmpl var id want name bad=""
  rid=$1
  if [ -z "$rid" ]; then
    unmeasured inflight-run "CHECK194_INFLIGHT is empty, so there is no run to read"
    unmeasured inflight-mount "CHECK194_INFLIGHT is empty, so there is no run whose spec could be read"
    return 0
  fi
  row=""
  if fugaro runs ls --repo "$REPO" --project fugaro --no-cache --json >"$SCRATCH/rows-inflight.json" 2>"$SCRATCH/rows-inflight.err"; then
    row=$(reader run "$SCRATCH/rows-inflight.json" "$rid")
  fi
  if [ -z "$row" ] || [ "$(field_of found "$row")" != yes ]; then
    unmeasured inflight-run "the run named by CHECK194_INFLIGHT (${rid}) is not in ${REPO}'s listing: a run this check cannot find is one whose spec it cannot read. Read 'fugaro runs ls --repo ${REPO}' and name the id again"
    unmeasured inflight-mount "the run CHECK194_INFLIGHT names (${rid}) could not be read, so nothing about the spec it kept is measured"
    return 0
  fi
  if [ "$(field_of terminal "$row")" != yes ]; then
    fact inflight-run FAIL "the run named by CHECK194_INFLIGHT (${rid}) had not reached a final status when this check read it: status=$(field_of status "$row"). U9 is about a run that was going ACROSS the re-apply, and this reading is taken once it has finished — wait for it to end ('fugaro runs show ${rid} --repo ${REPO}'), then run this check again"
    unmeasured inflight-mount "the run ${rid} had not finished when this check read it, so the spec it kept is not measured"
    return 0
  fi
  if [ "$JOBS_OK" != yes ]; then
    unmeasured inflight-run "${JOBS_WHY}, and the re-apply's own clock is the latest updateTime in that list: whether this run was going across the re-apply is not measured"
    unmeasured inflight-mount "the run's start could not be placed against the re-apply, so the spec it kept is not read"
    return 0
  fi
  started_at=$(field_of started_at "$row")
  got=$(reader inflight "$listed" "$REPO" "$started_at")
  case "$(field_of order "$got")" in
  after)
    fact inflight-run FAIL "the run named by CHECK194_INFLIGHT (${rid}) was started at ${started_at:-unread}, which is not before ${REPO}'s jobs were last updated ($(field_of update "$got"), job $(field_of job "$got")): it was launched by the template the re-apply wrote, so it is not a run that was in flight ACROSS the re-apply and its spec settles nothing about U9. Name the run that was already going when you ran 'fugaro update --repo'"
    unmeasured inflight-mount "the run ${rid} started after the re-apply, so the spec it held is not the one U9 is about"
    return 0
    ;;
  unknown)
    unmeasured inflight-run "the run ${rid} started at ${started_at:-nothing readable}, and the time ${REPO}'s jobs were last updated ($(field_of update "$got")) could not be read either: whether it was going across the re-apply is not measured. Read 'gcloud run jobs describe $(field_of job "$got") --project=${P} --region=${L} --format=json' and the run's own row, then this check again"
    unmeasured inflight-mount "the run's start could not be placed against the re-apply, so the spec it kept is not read"
    return 0
    ;;
  esac
  if [ "$(field_of status "$row")" != succeeded ]; then
    fact inflight-run FAIL "the run named by CHECK194_INFLIGHT (${rid}) started at ${started_at} — before the re-apply ($(field_of update "$got")) — and ended '$(field_of status "$row")' without opening a pull request: $(field_of halt_detail "$row")$(field_of reason "$row") (outcome=$(field_of outcome "$row")) An execution created before the re-apply keeps the container spec it was created with, which is the whole of U9, so the re-apply cannot have taken a value out from under it: a run of that shape that could not finish is the re-apply having reached INTO a run that was already going"
    unmeasured inflight-mount "the run ${rid} did not finish, so nothing is read about the spec it kept"
    return 0
  fi
  fact inflight-run PASS "the run named by CHECK194_INFLIGHT (${rid}) started at ${started_at}, before ${REPO}'s jobs were last updated ($(field_of update "$got"), job $(field_of job "$got")), and finished all the same: status=succeeded, outcome=$(field_of outcome "$row"), pull request $(field_of pr "$row")"
  execpath=$(field_of execution "$row")
  if [ -z "$execpath" ]; then
    unmeasured inflight-mount "the row of the run ${rid} names no execution, so the container spec the run was created with cannot be read — and that spec is U9's whole reading: read 'fugaro runs show ${rid} --repo ${REPO}'"
    return 0
  fi
  bare=${execpath##*/}
  spec=$SCRATCH/execution-inflight.json
  if ! gcloud run jobs executions describe "$bare" --project="$P" --region="$L" --format=json >"$spec" 2>"$SCRATCH/execution-inflight.err"; then
    unmeasured inflight-mount "'gcloud run jobs executions describe ${bare} --project=${P} --region=${L}' exited non-zero and printed: $(last_lines "$SCRATCH/execution-inflight.err"). U9's reading is the container spec that execution was created with, and a read that did not answer leaves nothing to read it against"
    return 0
  fi
  mounts=$(reader job "$spec" "$ENV_ONE,$ENV_TWO")
  tmpl=$(reader jobs "$listed" "$REPO" "$ENV_ONE,$ENV_TWO")
  tmounted=$(field_of carried "$tmpl")
  if [ "${tmounted:-0}" -ge 1 ]; then
    unmeasured inflight-mount "the execution ${execpath} of the run ${rid} carries $([ -n "$(env_id "$mounts" "$ENV_ONE")" ] && echo "${ENV_ONE} ") $([ -n "$(env_id "$mounts" "$ENV_TWO")" ] && echo "${ENV_TWO} "), and the jobs of ${REPO} hand a workflow secret out too (in ${tmounted} place(s)): with the template still handing one out, a mount on that execution is not the RETENTION U9 is about — the flip has not reached that repository's jobs, which is the no-mounts row above"
    return 0
  fi
  for name in "$ENV_ONE" "$ENV_TWO"; do
    case "$name" in
    "$ENV_ONE") want=$REC_ID_ONE ;;
    *) want=$REC_ID_TWO ;;
    esac
    id=$(env_id "$mounts" "$name")
    if [ -z "$id" ]; then
      bad="$bad the execution does not read ${name} out of a container at all;"
    elif [ -n "$want" ] && [ "$id" != "$want" ]; then
      bad="$bad the execution reads ${name} from ${id} and the record names ${want};"
    fi
  done
  if [ -n "$bad" ]; then
    fact inflight-mount FAIL "the execution ${execpath} of the run ${rid} — created before the re-apply and finished all the same — does not hold what the flip takes away:${bad:0:400} An execution created before the re-apply keeps the container spec it was created with (U9), and this one's spec has to be the one the old template wrote: the two containers ${REPO}'s record names, held in its own environment. Name the run that was already going when you re-applied the release that drops the mount, or read that execution's own spec"
    return 0
  fi
  fact inflight-mount PASS "the execution ${execpath} of the run ${rid} was created before the re-apply and its own container spec still reads ${ENV_ONE} from ${REC_ID_ONE} and ${ENV_TWO} from ${REC_ID_TWO} — the containers ${REPO}'s record names — while the job template of ${REPO} hands a run none of them: the execution kept the spec it was created with (U9), and the values it read are the ones the record names (F33)"
}

# run_account: the account the job of the run that just finished runs as. The
# execution's own document does not repeat it (its serviceAccountName sits above
# the revision spec an execution carries), so it is read off the job template in
# the list `step_jobs` already fetched, by the job name in the execution's path:
# the account a run's containers run as is the JOB's account, which is the one
# an accessor binding on a container has to be compared with.
run_account() {
  local jobname
  ACCOUNT_ERR=""
  jobname=$(printf '%s' "$EXEC_PATH" | sed -n 's#.*/jobs/\([^/]*\)/executions/.*#\1#p')
  if [ -z "$jobname" ]; then
    ACCOUNT_ERR="the run's execution path (${EXEC_PATH:-unread}) names no job, so the account it ran as is not read"
    return 1
  fi
  if [ "$JOBS_OK" != yes ]; then
    ACCOUNT_ERR="${JOBS_WHY}, and the account a run's containers run as is on the job template that list carries"
    return 1
  fi
  local line
  line=$(reader jobs "$SCRATCH/jobs.json" "$REPO" "$ENV_ONE,$ENV_TWO" | sed -n "s/^job=$jobname|\([^|]*\)|.*/\1/p" | head -n 1)
  if [ -z "$line" ]; then
    ACCOUNT_ERR="the job ${jobname}, which the run's execution names, is not in the installation's job list, so the account its containers run as is not read"
    return 1
  fi
  printf '%s\n' "$line"
}

# step_run LABEL: one throwaway run of the repository under test, and its row. It
# leaves RUN_ROW, RUN_ERR and the label's run id set for the rows that read the
# run — the verify, the fetch and the account — and it returns non-zero (with
# RUN_ERR written) when no row could be read at all, which its callers turn into
# their own unmeasured rows rather than a FAIL nothing earned.
step_run() { # step_run LABEL
  local label=$1 task rid
  task=$SCRATCH/task-$label.md
  rid=$(run_id "$label")
  case "$label" in
  one) RUN_ID_ONE=$rid ;;
  flip-one) RUN_ID_FLIP=$rid ;;
  esac
  RUN_ROW=""
  RUN_ERR=""
  task_write "$task" "$label" "$rid"
  if ! RUN_ROW=$(launch_into "$label" "$rid" "$task"); then
    RUN_ROW=""
    RUN_ERR=$(launch_err)
    return 1
  fi
  return 0
}

# step_execution: the container spec the run itself ran on, read off the
# execution its own row names. Everything this check reads about what a run HELD
# is read here and never off the job template: a template is what the NEXT run
# gets, and the run that finished ran on the spec it was created with (U9) — the
# same reason the mount a run in flight kept is read off its execution.
step_execution() {
  local execpath bare
  EXEC_TEXT=""
  EXEC_PATH=""
  EXEC_ERR=""
  if [ -z "$RUN_ROW" ]; then
    EXEC_ERR="the run was not launched, so there is no execution of its to read"
    return 1
  fi
  execpath=$(field_of execution "$RUN_ROW")
  if [ -z "$execpath" ]; then
    EXEC_ERR="the row of the run $(field_of run_id "$RUN_ROW") names no execution, so the spec it ran on cannot be read: read 'fugaro runs show $(field_of run_id "$RUN_ROW") --repo ${REPO}'"
    return 1
  fi
  EXEC_PATH=$execpath
  bare=${execpath##*/}
  EXEC_NAME=$bare
  if ! gcloud run jobs executions describe "$bare" --project="$P" --region="$L" --format=json >"$SCRATCH/execution-run.json" 2>"$SCRATCH/execution-run.err"; then
    EXEC_ERR="'gcloud run jobs executions describe ${bare} --project=${P} --region=${L}' exited non-zero and printed: $(last_lines "$SCRATCH/execution-run.err")"
    return 1
  fi
  EXEC_TEXT=$(reader job "$SCRATCH/execution-run.json" "$ENV_ONE,$ENV_TWO")
  return 0
}

# env_of DOC_TEXT ENV: what a job document's reading says about one variable —
# `secret|<container>` when it is fed out of a container, `value` when it carries
# a plain one, `absent` when it is not there at all.
env_of() { # env_of DOC_TEXT ENV
  printf '%s\n' "$1" | sed -n "s/^env=$2|//p" | head -n 1
}

# env_id DOC_TEXT ENV: the container that variable is fed from, or "" when it is
# fed from none (a plain value, or not there at all).
env_id() { # env_id DOC_TEXT ENV
  env_of "$1" "$2" | sed -n 's/^secret|//p'
}

# print_plan: what a real run would do, in order. Printed by --dry-run, which
# runs NOTHING: no gcloud, no fugaro and no curl is called, so this mode is safe
# on a machine with no credentials at all.
print_plan() {
  say "dry run: NOTHING below was run, and no tool was called at all — not gcloud, not fugaro, not curl. A real run first checks that this is the fugaro-dev installation (fugaro doctor --project fugaro --all, and that fugaro version is not a development build), then reads the installation's Realtime Database URL out of the local config, then:"
  say "  [network] gcloud run jobs list --project=${P} --region=${L} --format=json  (the installation's jobs: ${REPO}'s own 'fugaro_repo' label is the key its record is written under, and ${OTHER_REPO}'s, the job environments and the re-apply's clock all come out of this one read)"
  say "  [network, read only] gcloud auth print-access-token, then GET ${RTDB:-<budget.rtdb_url>}/creds/workflowsecrets/<slug>.json for each repository — the slug being that repository's own task slug ('<readable>-<16 hex>'), which the check reads off its jobs' 'fugaro_repo' label and never spells out of the repository's name — the record 'fugaro update --repo' writes; READ, never written, and this check narrows no record"
  say "  [local] the record's shape: version and workflows, per entry env and id, and no key this release does not model (a member that could hold a value is a FAIL, G18)"
  say "  [local] its containers: a project container of this installation ('fugaro-project-<installation>-<name>-<16 hex>', the installation here being ${INSTALL:-unread}) for ${ENV_ONE} and for ${ENV_TWO}, and never a repository's own container (D2313)"
  if [ "$MODE" = flip ]; then
    say "  [local] no job of ${REPO} hands a run ${ENV_ONE} or ${ENV_TWO} in any form, and every one of them still carries FUGARO_CREDENTIALS_SERVICE_URL (B3.8: the mount goes, the URL stays)"
    say "  [network, read only] gcloud secrets get-iam-policy <each container the record names> --project=${P} --format=json  (no binding lists the account the run's job runs as as a secretAccessor, D2074)"
    say "  [network, read only] fugaro runs ls --repo ${REPO} --no-cache --json, for CHECK194_INFLIGHT  (that run's row: terminal, succeeded, started before the job's own updateTime, and the execution it names)"
    say "  [network, read only] gcloud run jobs executions describe <that execution> --project=${P} --region=${L} --format=json  (its OWN container spec, which is the one it was created with: it still reads both variables out of the record's containers while the template the flip wrote reads none, U9)"
  else
    say "  [local] every job of ${REPO} that carries ${ENV_ONE} or ${ENV_TWO} is fed it out of the container the record names for it, and none of them carries one as a plain value (F33, D1267)"
  fi
  say "  [network, writes] fugaro run --repo ${REPO} --project fugaro --run-id <today>-$([ "$MODE" = flip ] && echo 0198 || echo 0197) --title 'Check 194: $([ "$MODE" = flip ] && echo flip-one || echo one)' --task-file <task> --json  (one throwaway run: it writes a line to CHK194.md, commits and opens a pull request)"
  say "  [network, read only] fugaro runs ls --repo ${REPO} --no-cache --json  (its row, polled until terminal) and gcloud run jobs executions describe <the execution it names>  (the spec it ran on)"
  if [ "$MODE" = flip ]; then
    say "  [network, read only] gcloud logging read 'resource.type=\"cloud_run_revision\" resource.labels.service_name=\"fugaro-credentials\" jsonPayload.run=\"<run id>\"'  (the credsvc: audit lines: purpose workflow-secret, naming BOTH containers the record names — the fetch of B3.4)"
  else
    say "  [local] its own verify: the sandbox's verify command fails on an empty ${ENV_ONE} or ${ENV_TWO}, so a run that ended succeeded read both values out of the containers its job was fed from"
  fi
  say "  [local] the second repository, ${OTHER_REPO}: its record names no container for ${ENV_ONE} and no job of it carries that variable — the per-repository entry of the allow-list does not list it (G19, D1262). This one is read and not launched: a run there would cost a commit and a pull request and add nothing the two documents do not already say"
  say "and its cleanup is nothing: this check creates no cloud resource and writes no record. The only thing it makes is the scratch directory, which its own EXIT trap removes."
}

main() {
  need_tools
  scratch_up
  write_reader
  trap on_error ERR
  guard_xtrace
  guard_inputs

  # The dry run comes BEFORE the two guards that read the cloud (the
  # installation's own identity, and the access token the record is read with): a
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
    stop "this check needs the installation's Realtime Database URL and read none out of ${CONFIG_FILE} (budget.rtdb_url). The record it reads each repository's workflow secrets from lives in that database, and without it nothing below can be measured. Check that ${CONFIG_FILE} is this machine's local config for installation fugaro, or name it with CHECK194_CONFIG"
  fi
  # The installation's own name, and it is checked HERE rather than left to the
  # record row to trip over: the containers that record names are derived from it
  # (gcp.ProjectSecretID), and a name this check cannot read is a pattern that
  # would refuse every container a real installation writes. The charset is
  # checked with it, because that pattern is a shell one and a name carrying a
  # '*' would widen it.
  case "$INSTALL" in
  "") stop "this check needs the installation's own Fugaro project name and read none out of ${CONFIG_FILE} (name:). It is NOT the GCP project: the project-wide secret containers a workflow secret's value comes from are derived from the Fugaro project name (gcp.ProjectSecretID), so without it no container the record names can be told from a repository's own. Check that ${CONFIG_FILE} is this machine's local config for installation fugaro, or name it with CHECK194_CONFIG" ;;
  *[!a-z0-9-]*) stop "the installation's Fugaro project name read out of ${CONFIG_FILE} is '${INSTALL}', which is not a project name this check can read a container by (lower-case letters, digits and dashes). Check that ${CONFIG_FILE} is this machine's local config, or name another file with CHECK194_CONFIG" ;;
  esac

  case "$MODE" in
  flip) run_194_flip ;;
  *) run_194 ;;
  esac
}

# run_194: the switch mode's steps, in order, and the two rows that need the
# record before they need a run.
run_194() {
  step_jobs
  step_record
  step_other
  if [ "$RECORD_STOP" = yes ]; then
    unmeasured mounts "the record was not settled (see the record row), and every row about a mount is that record's containers read against a job's own spec: with no record there is nothing to compare a mount with"
    unmeasured verify "the record was not settled (see the record row), so no run is launched — each run costs a commit and a pull request on ${REPO}, and nothing about it would be read against a record"
    return 0
  fi
  if ! step_run one; then
    unmeasured mounts "${RUN_ERR:-the run was not launched, so no execution of its is read}"
    unmeasured verify "${RUN_ERR:-the run was not launched, so no verify stage of its is read}"
    return 0
  fi
  step_execution || true
  step_mounts
  step_verify
}

# run_194_flip: the flip's steps, in order. The rows about the job's own
# template (no-mounts) and a run that was already going (inflight) are read
# before the run this mode launches, and they cost nothing: a record that could
# not be settled leaves the two rows that read the service's answer and a
# container's policy unmeasured rather than launching a run nothing would be
# read against.
run_194_flip() {
  step_jobs
  step_record
  step_other
  step_nomounts
  step_inflight "$INFLIGHT"
  if [ "$RECORD_STOP" = yes ]; then
    unmeasured served "the record was not settled (see the record row), and this row reads the service's own lines against the containers that record names"
    unmeasured no-accessors "the record was not settled (see the record row), so there is no container whose policy this row could read"
    return 0
  fi
  if ! step_run flip-one; then
    unmeasured served "${RUN_ERR:-the run was not launched, so nothing is read of what the service served it}"
    unmeasured no-accessors "${RUN_ERR:-the run was not launched, so the account it ran as is not read}"
    return 0
  fi
  step_execution || true
  step_served
  step_noaccessors
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs. The
# two modes print two DIFFERENT key sets — before the flip there is a mount to
# read where after it there is an accessor — so a block from one is never read as
# a block from the other.
BLOCK_KEYS=(fugaro-version project-region repos xtrace scratch record mounts verify other
  restore scratch-removed)

BLOCK_KEYS_FLIP=(fugaro-version project-region repos xtrace scratch record no-mounts
  no-accessors served other inflight-run inflight-mount restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed="" keys
  if [ "$MODE" = flip ]; then keys=("${BLOCK_KEYS_FLIP[@]}"); else keys=("${BLOCK_KEYS[@]}"); fi
  printf '\nCHECK 194 RESULT\n' >&3
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
  printf 'CHECK 194 RESULT END (exit %s)\n' "$status" >&3
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
REPO=${CHECK194_REPO:-}
OTHER_REPO=${CHECK194_OTHER_REPO:-}
ENV_ONE=${CHECK194_ENV_ONE:-DEPLOY_TOKEN}
ENV_TWO=${CHECK194_ENV_TWO:-PROJECT_TOKEN}
INFLIGHT=${CHECK194_INFLIGHT:-}
RUN_BASE=${CHECK194_RUN_ID:-$(date -u +%Y%m%d-%H%M%S)}
RUN_SECONDS=${CHECK194_RUN_SECONDS:-2400}
POLL_SECONDS=${CHECK194_POLL_SECONDS:-20}
LOG_WAIT_SECONDS=${CHECK194_LOG_WAIT_SECONDS:-60}
LOG_POLL_SECONDS=${CHECK194_LOG_POLL_SECONDS:-5}
LOG_AGE=${CHECK194_LOG_AGE:-1h}
LOG_LIMIT=${CHECK194_LOG_LIMIT:-200}
KEEP_SCRATCH=${CHECK194_KEEP_SCRATCH:-}
JOBS_OK=no
JOBS_WHY="the installation's job list was not read"
# Set by step_record once the record itself could not be settled: every row about
# a mount, a fetch or an accessor is that record's containers read against
# something, so nothing is launched off a record nobody read — and each of those
# rows says which part of it was missing.
RECORD_STOP=no
REC_KEY=""
REC_ID_ONE=""
REC_ID_TWO=""
RUN_ROW=""
RUN_ERR=""
RUN_ID_ONE=""
RUN_ID_FLIP=""
EXEC_TEXT=""
EXEC_NAME=""
EXEC_PATH=""
EXEC_ERR=""
ACCOUNT_ERR=""
CONFIG_FILE=${CHECK194_CONFIG:-${HOME:-}/.config/fugaro/projects/fugaro.yaml}
# The Realtime Database URL and the installation's own Fugaro project name, out
# of the installation's own local config (budget.rtdb_url and name): the one
# place a machine already set up for this installation names them, and a file
# this check only reads. The NAME is not the GCP project (the two are `fugaro`
# and `fugaro-dev` here) and it is the one that matters for the containers this
# check reads: an installation's project-wide secret containers are derived from
# it (gcp.ProjectSecretID takes lc.Name), so a pattern spelled out of the GCP
# project would refuse every container a real installation writes.
RTDB=""
INSTALL=""
if [ -f "$CONFIG_FILE" ]; then
  RTDB=$(sed -n 's/^[[:space:]]*rtdb_url:[[:space:]]*//p' "$CONFIG_FILE" 2>/dev/null | head -n 1 | tr -d '[:space:]"' | tr -d "\047" || true)
  INSTALL=$(sed -n 's/^[[:space:]]*name:[[:space:]]*//p' "$CONFIG_FILE" 2>/dev/null | head -n 1 | tr -d '[:space:]"' | tr -d "\047" || true)
fi

for arg in "$@"; do
  case "$arg" in
  --after-flip) MODE=flip ;;
  --dry-run) DRY_RUN_CLI=1 ;;
  -h | --help)
    # The header comment and nothing else: the range ends on the first line of
    # the script itself, which is filtered out with it — a help that printed
    # `set -euo pipefail` under the usage would read as though that were part of it.
    sed -n '2,/^set -euo/{/^#/p}' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *)
    say "unknown argument: $arg"
    exit 2
    ;;
  esac
done
DRY_RUN=${CHECK194_DRY_RUN:-$DRY_RUN_CLI}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
