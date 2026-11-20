#!/usr/bin/env bash
# Check 196 — a repository's image build and its daily image check, served by the
# credentials service, and the flip that takes the build account's accessor away.
#
# It settles S4's reading (plan B4.7, its "What it measures"; the
# reconciliation's **D2076** (a) and **D2077** (a), **R5**, **R6**, **G18**,
# **G19**, **G20**, **F28**, **F29** and **U9**; the build route is B4.3, the
# grant is B4.4 and the fetch is B4.5). Until B4.9's flip a switched repository's
# build still mounts its git credential and its workflow build secrets, so an
# offline reading settles nothing: whether `fugaro image refresh` and the daily
# check really bake with a credential the BUILD ACCOUNT fetched from the service
# with its own metadata ID token, whether the service serves a build only its
# OWN repository's credential (G20), and — after the flip — whether the accessor
# and the `availableSecrets` are really gone while a build still succeeds, are
# answers of a live installation. So this check reads what real builds did: the
# request each build carried (`gcloud builds describe`), the job the daily check
# runs as (`gcloud run jobs`), the account that may invoke the service
# (`gcloud run services get-iam-policy`) and the service's OWN lines
# (`credsvc: audit`, purpose `build`) — and it never calls the service itself.
# A FAIL in the switch mode stops S4 and returns to the owner as a decision
# (B4.8 is the gate, B4.10 the flip's).
#
# Usage:      bash scripts/live-checks/check196.sh
#             bash scripts/live-checks/check196.sh --dry-run      (prints the
#             plan and calls NOTHING: no gcloud, no fugaro)
#             bash scripts/live-checks/check196.sh --after-flip   (Check 196's
#             second half: the accessor and availableSecrets gone, a build that
#             still succeeds)
# Needs:      bash, mktemp, python3, gcloud (authenticated as you: read Cloud
#             Build's own records and one of the two repositories' builds, read
#             Cloud Run jobs and one execution, read the service's own IAM
#             policy, and read the service's own lines out of Cloud Logging) and
#             fugaro (doctor and version). Nothing is installed and nothing is
#             fetched.
# Where:      installation `fugaro` ONLY — project fugaro-dev, region us-east5.
#             Both are HARD-CODED in this file and never read from `gcloud
#             config get-value project`, and `fugaro doctor --project fugaro
#             --all` must print its `project: … (GCP fugaro-dev,` line: a check
#             that read the project from the environment could be pointed at
#             another installation by a stray config, and this one must never
#             be. Never belong.
# Creates:    nothing in the installation, the project or any repository. This
#             check WRITES nothing and DELETES nothing: it reads Cloud Build's
#             own records, Cloud Run's jobs and the service's own lines, and the
#             other repository's builds. The builds it reads are the ones YOU
#             submitted with `fugaro image refresh` (B4.8), and they are the
#             project's own log rather than this check's.
# Costs:      wall clock, not money: minutes at the terminal. No build is
#             submitted by the check itself.
# Runs:       by YOU. No agent runs any step of it, and no agent has: what it
#             measures is a live installation. Its own test (check196_test.sh)
#             drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else — this check creates no
#             cloud resource. It runs from the exit trap too, so a Ctrl-C leaves
#             nothing of the check's behind.
# Output:     progress on stderr, ONE block on stdout headed CHECK 196 RESULT,
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
# authorization header. The build's `availableSecrets` entries this check reads
# are Secret Manager CONTAINER IDs (a name, never a value, G18), the audit line
# names the CREDENTIAL and never its value, and the check holds no credential of
# its own beyond the gcloud access token it mints to read the project's own
# records.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK196_REPO (REQUIRED: the GitHub sandbox repository under test, as
# <owner>/<repository>); CHECK196_BITBUCKET (REQUIRED: the Bitbucket sandbox
# repository, which the same installation serves); CHECK196_BUILDS (how many of
# the project's recent builds to read; default 50), CHECK196_LOG_AGE,
# CHECK196_LOG_LIMIT, CHECK196_LOG_WAIT_SECONDS, CHECK196_LOG_POLL_SECONDS,
# CHECK196_DRY_RUN, CHECK196_KEEP_SCRATCH. None of them can point this check at
# another project or another region: those two are constants in this file.
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
need() { command -v "$1" >/dev/null 2>&1 || die "Check 196 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  need bash
  need mktemp
  need python3 "every gcloud document below is read by the reader in the scratch directory"
  need gcloud
  need fugaro
}

# ------------------------------------------------------------------- constants
# The project and the region are HARD-CODED, never read from `gcloud config`:
# see the header. The service's Cloud Run name is a literal in internal/infra
# (CredentialsServiceName) rather than a per-installation value, which is what
# makes the log filter below the same on every installation.
P=fugaro-dev
L=us-east5
SVC=fugaro-credentials
# A repository's build service account is `fugaro-b-…@<project>.iam.gserviceaccount.com`
# (gcp.BuildServiceAccountID), so a caller is recognised as a repository's build
# by this prefix and the installation's own domain. The daily check's job name
# starts `fugarochk-` (gcp.CheckJobName).
BUILD_ACCOUNT_PREFIX=fugaro-b-
CHECK_JOB_PREFIX=fugarochk-

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk196-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
PRINTED=""
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
AT=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk196.XXXXXXXX") || die "Check 196 cannot make a scratch directory"
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
    # The knob docs/secrets-brokering/check196-steps.md tells the owner to set
    # to read what the check wrote is not a directory that could not be removed:
    # the row below says which of the two it is, and only the second is a FAIL.
    SCRATCH_KEPT=yes
    say "keeping $SCRATCH (CHECK196_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: what this check created, undone. There is nothing of this check's in
# the installation, the project or any repository — it writes nothing and
# deletes nothing, and the builds it reads are the project's own log rather than
# resources it made — so this row is about the one thing that COULD have been
# left: the scratch directory, which is removed by remove_scratch and read back
# there.
restore() {
  fact restore PASS "this check created no cloud resource, submitted no build and touched no repository: the only thing it made is the scratch directory, whose row is below"
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
    facts="${facts}"$'\n'"scratch-removed|PASS|kept on request (CHECK196_KEEP_SCRATCH is set), at $SCRATCH"
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

# redact: what a gcloud message may carry back into the block, which is pasted
# into a pull request. A credential-shaped run of characters goes, whatever it
# is. No token of this check's ever reaches a message it prints, and this is the
# second line of that defence rather than the first.
redact() {
  sed -E \
    -e 's/([Aa]uthorization)[[:space:]]*:[^|]*/\1: <redacted> /g' \
    -e 's/(Bearer|Basic)[[:space:]]+[A-Za-z0-9._~+/=-]{12,}/<redacted>/g' \
    -e 's/[A-Za-z0-9_]*(token|Token|TOKEN|secret|Secret|SECRET)[A-Za-z0-9_]*=[^[:space:]]+/<redacted>/g' \
    -e 's/((oauth|api)[_-]?key["=: ]+)[A-Za-z0-9._+=-]{12,}/\1<redacted>/Ig'
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
# — the project's recent builds, the repository's own Cloud Run jobs, one
# execution, the service's own IAM policy and the service's own log lines — so
# that no reading of a JSON shape is spelled twice in this script and none of
# them is done in bash.
write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 196's reader: one file, one reading per subcommand, no credential in
any of them.

builds  argv: `gcloud builds list --format=json`'s file, a repository. Prints the
        repository's OWN newest derived-image build field by field: its id, its
        status, the account it ran as (a resource name, reduced to the address
        it ends in), the substitutions this check reads (`_SLUG`, `_WORKFLOW`,
        `_SECRET_ENVS`, `_CREDENTIALS_SERVICE_URL`), the Secret Manager
        CONTAINER ids its `availableSecrets` names (a container is a name and
        never a value, G18) and the env names its build step carries as
        `secretEnv`. `found=no` when the project's last builds hold none of the
        repository's.
job     argv: `gcloud run jobs list --format=json`'s file, a repository's slug.
        Prints the repository's own daily CHECK job (named `fugarochk-…`,
        gcp.CheckJobName): its name, the account its containers run as, whether
        it carries FUGARO_CREDENTIALS_SERVICE_URL at all, and each Secret
        Manager container its env is fed from (`mount=<ENV>|<id>`), which is the
        mount the flip takes away. `found=no` when this installation deploys no
        such job for the repository.
exec    argv: `gcloud run jobs executions list --format=json`'s file. Prints the
        newest execution's name and status, and how many the listing holds.
iam     argv: one `gcloud secrets get-iam-policy --format=json` document. Prints
        one `accessor=<member>` line per member of a binding whose role can read
        a version, and the counts. Nothing of it is a value.
svciam  argv: `gcloud run services get-iam-policy --format=json`'s file, as JSON.
        Prints every binding's role and members, so the row can say who may
        invoke the credentials service — which is B4.4's grant and the front
        door a build account that holds it nowhere else is refused at.
audit   argv: the service's lines' file. Prints one `audit=<status>|<credential>|
        <purpose>|<caller>|<repository>` line per `credsvc: audit` line of
        purpose `build`, and `refused=<status>|<sentence>` per `credsvc: refused`
        line: the credential is a NAME and never a value.
"""
import json
import re
import sys

WORD = re.compile(r"[^A-Za-z0-9._:@/+-]")
ACCESSOR_ROLES = ("roles/secretmanager.secretAccessor",)
BUILD_ACCOUNT_PREFIX = "fugaro-b-"
CHECK_JOB_PREFIX = "fugarochk-"


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


def bare_member(m):
    """An IAM member with its type prefix dropped (`serviceAccount:`, `user:`,
    `group:` or `domain:`) — and with nothing else touched. A prefix outside that
    set (`deleted:`, a `principalSet:` URL) is left alone: none of them is an
    account, and a member that is not one must not become equal to one by a
    rewrite."""
    s = str(m)
    for prefix in ("serviceAccount:", "user:", "group:", "domain:"):
        if s.startswith(prefix):
            return s[len(prefix):]
    return s


def env(spec):
    out = {}
    for e in (spec.get("env") or []) if isinstance(spec, dict) else []:
        if isinstance(e, dict) and e.get("name"):
            out[e["name"]] = e
    return out


def spec_of(doc):
    """The container spec of a job (`spec.template.spec.template.spec`), or one
    execution's (`spec.template.spec`). The known paths are tried in order and
    the document is finally searched for the first containers list, which is the
    one fact both shapes carry."""
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
    """What feeds one environment variable: the Secret Manager CONTAINER it is
    read from, a plain value, or nothing at all. A mount is
    `valueSource.secretKeyRef.secret` (a DERIVED id), and its short name is the
    last path segment with any version suffix dropped."""
    src = entry.get("valueSource") or {}
    ref = ((src.get("secretKeyRef") or {}).get("secret")
           or (src.get("secretKeyRef") or {}).get("name") or "")
    if ref:
        return "secret", ref.split("@")[-1].split(":")[-1].split("/")[-1]
    if entry.get("value") not in (None, ""):
        return "value", ""
    return "absent", ""


def commit_short_name(v):
    """A Secret Manager version reference (`projects/<p>/secrets/<id>/versions/<v>`)
    reduced to the container's short id, the last readable segment before
    `/versions`."""
    s = str(v or "")
    i = s.find("/versions/")
    if i >= 0:
        s = s[:i]
    return s.split("/")[-1]


def builds_of(path, repo):
    """Every build of one repository, newest first. The repository is matched by
    the request's own `_REPO_URL` substitution (the clone URL without
    credentials), which every derived-image build carries and no other build
    does — a reading that leaned on anything else would count another
    repository's build, or the project's own catalog build, as this one's."""
    out = []
    for b in load(path) or []:
        if not isinstance(b, dict):
            continue
        subs = b.get("substitutions") or {}
        url = subs.get("_REPO_URL") or ""
        if not (url == "https://" + repo + ".git" or url.endswith("/" + repo + ".git")):
            continue
        out.append(b)
    out.sort(key=lambda b: str(b.get("startTime") or b.get("createTime") or ""), reverse=True)
    return out


def containers_of_build(b):
    """The Secret Manager containers a build's `availableSecrets` names, one per
    env, in the order the request writes them. Empty for a served repository
    after the flip, where the request carries none (B4.9)."""
    out = []
    av = b.get("availableSecrets") or {}
    for s in av.get("secretManager") or []:
        if isinstance(s, dict):
            out.append((s.get("env") or "-", commit_short_name(s.get("versionName"))))
    return out


def secret_envs_of_build(b):
    """The env names any build step carries from a Secret Manager container
    (`secretEnv`), which is the second half of what the flip takes away."""
    out = []
    for st in b.get("steps") or []:
        if isinstance(st, dict):
            for name in st.get("secretEnv") or []:
                out.append(str(name))
    out.sort()
    return out


def account_address(v):
    """A Cloud Build `serviceAccount` field, reduced to the address it ends in.
    The API reports a resource name (`projects/<p>/serviceAccounts/<a>`), and
    `<a>` is the bare address where it kept one and the numeric uniqueId where
    it resolved one; this reading keeps whatever it found so a reader sees it,
    and the caller judges the prefix."""
    return str(v or "").split("/")[-1]


cmd = sys.argv[1] if len(sys.argv) > 1 else ""
if cmd == "builds":
    repo = sys.argv[3]
    bs = builds_of(sys.argv[2], repo)
    if not bs:
        print("found=no")
    else:
        b = bs[0]
        print("found=yes")
        print("count=%s" % len(bs))
        subs = b.get("substitutions") or {}
        print("id=%s" % word(b.get("id"), 60))
        print("status=%s" % word(b.get("status"), 40))
        print("account=%s" % word(account_address(b.get("serviceAccount")), 160))
        print("slug=%s" % word(subs.get("_SLUG"), 100))
        print("workflow=%s" % word(subs.get("_WORKFLOW"), 80))
        print("secret_envs=%s" % word(subs.get("_SECRET_ENVS"), 200))
        url = subs.get("_CREDENTIALS_SERVICE_URL") or ""
        print("url=%s" % ("present" if url else "absent"))
        # The URL is the service's own address, which is a name and not a
        # credential: it is printed whole so a reader can compare it with the
        # installation's service.
        print("url_value=%s" % text(url, 200) if url else "url_value=-")
        print("started=%s" % word(b.get("startTime"), 40))
        print("finished=%s" % word(b.get("finishTime"), 40))
        cons = containers_of_build(b)
        print("containers=%s" % len(cons))
        for name, ident in cons:
            print("container=%s|%s" % (word(name, 80), word(ident, 200)))
        envs = secret_envs_of_build(b)
        print("secretenv=%s" % len(envs))
        for name in envs:
            print("secretenv_name=%s" % word(name, 80))
elif cmd == "job":
    slug = sys.argv[3]
    doc = load(sys.argv[2]) or []
    found = None
    for job in doc:
        if not isinstance(job, dict):
            continue
        name = ((job.get("metadata") or {}).get("name")) or ""
        if not name.startswith(CHECK_JOB_PREFIX):
            continue
        # The check job of ONE repository: matched by the `fugaro_repo` label the
        # installation writes (which is the repository's slug) or by a container's
        # own FUGARO_REMOTE, so a job whose label the installation had not written
        # is still found.
        label = ((job.get("metadata") or {}).get("labels") or {}).get("fugaro_repo") or ""
        remote = ""
        for c in containers_of(job):
            remote = (env(c).get("FUGARO_REMOTE") or {}).get("value") or remote
        owns = label == slug or remote.endswith("/" + slug.split("-")[0] + ".git")
        if not owns:
            continue
        found = job
        break
    if found is None:
        print("found=no")
    else:
        print("found=yes")
        spec = spec_of(found)
        print("name=%s" % word((found.get("metadata") or {}).get("name"), 80))
        sa = (spec.get("serviceAccountName") if isinstance(spec, dict) else "") or ""
        print("account=%s" % word(sa, 160) if sa else "account=-")
        url, mounts = "absent", []
        for c in containers_of(found):
            e = env(c)
            if "FUGARO_CREDENTIALS_SERVICE_URL" in e:
                url = "present"
            for name, entry in e.items():
                kind, ident = source_of(entry)
                if kind == "secret":
                    mounts.append((name, ident))
        print("url=%s" % url)
        print("mounts=%s" % len(mounts))
        for name, ident in mounts:
            print("mount=%s|%s" % (word(name, 80), word(ident, 200)))
elif cmd == "exec":
    doc = load(sys.argv[2])
    rows = doc if isinstance(doc, list) else []
    rows = [r for r in rows if isinstance(r, dict)]
    rows.sort(key=lambda r: str((r.get("metadata") or {}).get("creationTimestamp") or ""), reverse=True)
    print("count=%s" % len(rows))
    if rows:
        r = rows[0]
        print("name=%s" % word((r.get("metadata") or {}).get("name"), 120))
        status = ((r.get("status") or {}).get("conditions") or [{}])
        state = "-"
        for c in status:
            if isinstance(c, dict) and c.get("type") == "Completed":
                state = str(c.get("status") or "-")
        print("state=%s" % word(state, 40))
    else:
        print("name=-")
        print("state=-")
elif cmd == "iam":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        accessors = 0
        for b in doc.get("bindings") or []:
            if not isinstance(b, dict):
                continue
            role = b.get("role") or ""
            for m in b.get("members") or []:
                if role in ACCESSOR_ROLES:
                    accessors += 1
                    print("accessor=%s" % word(bare_member(m), 200))
        print("accessors=%s" % accessors)
elif cmd == "svciam":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        print("found=no")
    else:
        print("found=yes")
        n = 0
        for b in doc.get("bindings") or []:
            if not isinstance(b, dict):
                continue
            role = b.get("role") or ""
            for m in b.get("members") or []:
                n += 1
                print("binding=%s|%s" % (word(role, 120), word(bare_member(m), 200)))
        print("bindings=%s" % n)
elif cmd == "audit":
    for entry in load(sys.argv[2]) or []:
        if not isinstance(entry, dict):
            continue
        p = entry.get("jsonPayload") or {}
        msg = entry.get("textPayload") or p.get("msg") or p.get("message") or ""
        if msg == "credsvc: audit":
            print("audit=%s|%s|%s|%s|%s" % (
                word(p.get("status"), 40),
                word(p.get("credential"), 120),
                word(p.get("purpose"), 40),
                word(p.get("caller"), 160),
                word(p.get("repository"), 100)))
        elif msg == "credsvc: refused":
            print("refused=%s|%s" % (word(p.get("status"), 40), text(p.get("message"), 300)))
PY
  chmod 700 "$SCRATCH/reader.py"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    say "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print every command this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check196.sh -- the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the access token or a command's environment"
}

guard_project() {
  # The project and the region are hard-coded, never read from `gcloud config`.
  # Check 196 is a reading on ONE installation: run anywhere else and every line
  # below is about builds, jobs and a service of another project, which is why
  # this stops rather than warns.
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
    fact "fugaro-version" FAIL "$v — a development build is not the release this check is about: B4.7's check runs on the release that carries B4.3–B4.7, and a checkout build resolves no released credentials-service image to deploy (internal/cli's resolveCredentialsImage). Install the release under test and run it again"
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
  [ -n "$REPO" ] || stop "CHECK196_REPO must name the GitHub sandbox repository under test, as <workspace>/<repository>"
  [ -n "$BB_REPO" ] || stop "CHECK196_BITBUCKET must name the Bitbucket sandbox repository this installation serves: the check reads one repository of each provider, and G20's boundary — a build of one repository is served its own provider's credential and never the other's — needs both"
  guard_one_repo "$REPO" CHECK196_REPO
  guard_one_repo "$BB_REPO" CHECK196_BITBUCKET
  [ "$REPO" != "$BB_REPO" ] || stop "CHECK196_REPO and CHECK196_BITBUCKET name the same repository ($REPO): the two halves are a GitHub repository and a Bitbucket one, and one repository cannot be both"
  fact inputs PASS "GitHub ${REPO}, Bitbucket ${BB_REPO} (sandbox repositories of the fugaro-dev installation; never belong)"
}

# ---------------------------------------------------------------- the reading
BUILDS_FILE=""
JOBS_FILE=""
SERVICE_URL=""

# read_builds: the project's recent builds, once, for both repositories.
read_builds() {
  BUILDS_FILE=$SCRATCH/builds.json
  if ! gcloud builds list --project="${P:?}" --limit="${BUILDS:?}" --format=json >"$BUILDS_FILE" 2>"$SCRATCH/builds.err"; then
    stop "the project's recent builds cannot be read: 'gcloud builds list --project=${P} --limit=${BUILDS} --format=json' failed with $(last_lines "$SCRATCH/builds.err"). YOUR account needs cloudbuild.builds.list, which a project Owner has"
  fi
  case "$(head -c 1 "$BUILDS_FILE" 2>/dev/null || true)" in
  "[") ;;
  *) stop "the project's recent builds are not the JSON array this check reads (is this gcloud too old for --format=json?): $(last_lines "$SCRATCH/builds.err")" ;;
  esac
}

# read_jobs: the installation's Cloud Run jobs, once, for both repositories.
read_jobs() {
  JOBS_FILE=$SCRATCH/jobs.json
  if ! gcloud run jobs list --project="${P:?}" --region="${L:?}" --format=json >"$JOBS_FILE" 2>"$SCRATCH/jobs.err"; then
    stop "the installation's Cloud Run jobs cannot be read: 'gcloud run jobs list --project=${P} --region=${L} --format=json' failed with $(last_lines "$SCRATCH/jobs.err"). YOUR account needs run.jobs.list, which a project Owner has"
  fi
}

# read_build REPO KEY — read the repository's newest build into the scratch
# directory, and print the reader's answer. Cached so the two rows about one
# repository read it once.
read_build() { # read_build REPO KEY
  local repo=$1 key=$2 f="$SCRATCH/builds-$2"
  if [ ! -f "$f" ]; then
    reader builds "$BUILDS_FILE" "$repo" >"$f" 2>"$SCRATCH/builds-$key.err" \
      || stop "the project's own builds could not be read (the reader failed): $(last_lines "$SCRATCH/builds-$key.err")"
  fi
  cat "$f"
}

# is_build_account ACCOUNT — whether a Cloud Build serviceAccount field names a
# repository's build account of THIS installation. The field is reduced to the
# address it ends in by the reader; the account is a repository's build
# (gcp.BuildServiceAccountID) when it carries the `fugaro-b-` prefix and the
# installation's own domain.
is_build_account() {
  case "$1" in
  "${BUILD_ACCOUNT_PREFIX}"*"@${P}.iam.gserviceaccount.com") return 0 ;;
  esac
  return 1
}

# audit_read SLUG — the service's own `credsvc: audit` and `credsvc: refused`
# lines for ONE repository, retried while Cloud Logging catches up. Ingestion
# lags the call by seconds, and a reading taken immediately is an empty answer
# about a build that did report.
audit_read() { # audit_read SLUG
  local slug=$1 waited=0 ans=""
  local filter="resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${SVC}\" jsonPayload.purpose=\"build\" jsonPayload.repository=\"${slug}\""
  while :; do
    if gcloud logging read "$filter" --project="${P:?}" --freshness="${LOG_AGE:?}" \
      --limit="${LOG_LIMIT:?}" --format=json >"$SCRATCH/audit-$slug.json" 2>"$SCRATCH/audit-$slug.err"; then
      ans=$(reader audit "$SCRATCH/audit-$slug.json" 2>/dev/null || true)
      # Ingestion lags the call by seconds: an empty answer right after a build
      # is an answer about Cloud Logging rather than about the service, so the
      # read is repeated while there is time left.
      if [ -n "$ans" ] || [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then
        printf '%s\n' "$ans"
        return 0
      fi
    elif [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then
      # A read that FAILED is not the answer "there is no line": the caller
      # tells them apart by this status and the sentence beside it.
      return 1
    fi
    sleep "$LOG_POLL_SECONDS"
    waited=$((waited + LOG_POLL_SECONDS))
  done
}

# ------------------------------------------------------------------- the steps
# step_service: the address the service is at, read off the GitHub repository's
# newest build. The switch is B4.9's replacement of the build's mounts: a
# switched repository's build names the service in `_CREDENTIALS_SERVICE_URL`,
# and a build that names none is one whose root has not been re-applied with the
# switch on.
step_service() {
  local ans url
  ans=$(read_build "$REPO" github)
  if [ "$(field_of found "$ans")" != yes ]; then
    return 0
  fi
  url=$(field_of url_value "$ans")
  if [ -z "$url" ] || [ "$url" = "-" ]; then
    fact service FAIL "the GitHub repository's newest build names no credentials service (its substitution _CREDENTIALS_SERVICE_URL is empty), so the build holds its own git credential and its workflow secrets as mounts and nothing was served. Re-apply the repository's root with the switch on ('fugaro update --repo' in a checkout of ${REPO}), refresh the image ('fugaro image refresh'), and run this check again"
    return 0
  fi
  SERVICE_URL=$url
  fact service PASS "the credentials service is at ${url}, read off ${REPO}'s own newest build's _CREDENTIALS_SERVICE_URL substitution (the address B4.5 hands the credential step)"
}

# step_repo LABEL REPO PROVIDER PROVIDER_CREDENTIAL — one sandbox repository's
# build, its daily check and the service's own lines for it.
step_repo() { # step_repo LABEL REPO CREDENTIAL
  local label=$1 repo=$2 cred=$3 ans
  ans=$(read_build "$repo" "$label")
  local found id status account slug workflow containers url
  found=$(field_of found "$ans")
  if [ "$found" != yes ]; then
    unmeasured "build-$label" "no build of ${repo} is among the project's last ${BUILDS} builds, so nothing about its build could be read. Submit one with 'fugaro image refresh' in a checkout of ${repo} (B4.8 does this for each sandbox checkout) and run this check again"
    unmeasured "mounts-$label" "no build of ${repo} was read, so its request's availableSecrets could not be measured"
    unmeasured "served-$label" "no build of ${repo} was read, so the service's own lines for it could not be measured"
    return 0
  fi
  id=$(field_of id "$ans")
  status=$(field_of status "$ans")
  account=$(field_of account "$ans")
  slug=$(field_of slug "$ans")
  workflow=$(field_of workflow "$ans")
  containers=$(field_of containers "$ans")
  url=$(field_of url "$ans")

  # build-LABEL: the build baked with a credential the service fetched, rather
  # than one the request mounted. A SUCCESS build that ran as the repository's
  # own build account and names the service is exactly that.
  if [ "$status" != SUCCESS ]; then
    fact "build-$label" FAIL "the newest build of ${repo} (${id}) ended ${status}, not SUCCESS: $(read_audit_said "$slug") . Read it with 'gcloud builds log ${id} --project=${P}'. A build of a served repository reads its credential from the service at the credential step, so a build that cannot finish is the service, the switch or the request"
  elif ! is_build_account "$account"; then
    fact "build-$label" FAIL "the newest build of ${repo} (${id}) ended SUCCESS but its own record says it ran as ${account:-nobody this reading can name}, not as a build account of ${P} (${BUILD_ACCOUNT_PREFIX}…@${P}.iam.gserviceaccount.com): a build that ran as another identity is not a statement about this repository's build account"
  elif [ "$url" != present ]; then
    fact "build-$label" FAIL "the newest build of ${repo} (${id}) ended SUCCESS but its request names no credentials service: it holds its own git credential and workflow secrets as mounts, so B4.9's flip has not reached this repository's builds. Re-apply the root ('fugaro update --repo') and refresh the image"
  else
    fact "build-$label" PASS "the newest build of ${repo} (${id}, workflow ${workflow}) ended SUCCESS, ran as ${account}, and names the credentials service in its request: it baked with a credential fetched from the service and not one it mounted"
  fi

  # mounts-LABEL: before the flip a served repository's build still MOUNTS every
  # secret it read before — the fetch reads what the mount read (F33, D1267) —
  # so a request that names the service and still carries availableSecrets is
  # the pre-flip state this mode reads. A request that carries none is the flip
  # already landed.
  if [ "$url" != present ]; then
    unmeasured "mounts-$label" "the newest build of ${repo} names no credentials service, so whether it mounts its secrets says nothing about the switch"
  else
    case "$containers" in
    "" | *[!0-9]*) unmeasured "mounts-$label" "the reader did not answer how many containers the build's availableSecrets names: $(last_lines "$SCRATCH/builds-$label.err")" ;;
    0)
      fact "mounts-$label" INFO "the newest build of ${repo} names the service and carries no availableSecrets: the S4 flip (B4.9) has already landed for it, so this mode is reading a post-flip installation. The --after-flip mode is the one to run"
      ;;
    *)
      fact "mounts-$label" PASS "the newest build of ${repo} names the service and still carries ${containers} availableSecrets entries: the build mounts its git credential and its workflow secrets while the credential step fetches the same ones from the service (F33, D1267 — the fetch reads what the mount read)"
      ;;
    esac
  fi

  # served-LABEL: the service's own lines for this repository. A build of a
  # served repository asks the build route once, and the line names the caller
  # the service VERIFIED (the account Cloud Run minted the build's ID token for,
  # G20 — the one field a call cannot claim for itself), the credential it was
  # served and the purpose.
  if [ -z "$slug" ] || [ "$slug" = "-" ]; then
    unmeasured "served-$label" "the newest build of ${repo} carries no _SLUG substitution, so the key its record is written under — and the repository the service's own lines name — cannot be read"
  else
    step_served "$label" "$repo" "$slug" "$account" "$cred"
  fi
}

# read_audit_said SLUG — the service's own refusal sentence for a repository, on
# one line, redacted, or nothing.
read_audit_said() { # read_audit_said SLUG
  [ -f "$SCRATCH/audit-$1.json" ] || return 0
  reader audit "$SCRATCH/audit-$1.json" 2>/dev/null | sed -n 's/^refused=[^|]*|//p' | head -n 1 | redact | cut -c1-300
}

# step_served LABEL REPO SLUG ACCOUNT CREDENTIAL — the service's own lines.
step_served() {
  local label=$1 repo=$2 slug=$3 account=$4 cred=$5 ans lines
  if ! ans=$(audit_read "$slug"); then
    unmeasured "served-$label" "the service's own lines for ${repo} could not be read (the log read failed): yours needs roles/logging.viewer on ${P}. Read them by hand with 'gcloud logging read \"resource.labels.service_name=${SVC} jsonPayload.repository=${slug}\" --project=${P}'"
    return 0
  fi
  lines=$(printf '%s\n' "$ans" | sed -n 's/^audit=ok|//p' | wc -l | tr -d ' ')
  if [ "${lines:-0}" -eq 0 ]; then
    local refused
    refused=$(printf '%s\n' "$ans" | sed -n 's/^refused=[^|]*|//p' | head -n 1 | redact | cut -c1-300)
    fact "served-$label" FAIL "the service wrote no 'credsvc: audit' line of purpose build for ${repo} (slug ${slug}): the build's credential was answered by something else, or the build's calls were refused.${refused:+ Its own refusal line reads: ${refused}.} Read the service's lines with 'gcloud logging read \"resource.labels.service_name=${SVC} jsonPayload.repository=${slug}\" --project=${P}'"
    return 0
  fi
  local got
  got=$(printf '%s\n' "$ans" | sed -n 's/^audit=ok|//p' | head -n 1)
  local acred acaller arepo
  acred=$(printf '%s' "$got" | cut -d'|' -f1)
  acaller=$(printf '%s' "$got" | cut -d'|' -f3)
  arepo=$(printf '%s' "$got" | cut -d'|' -f4)
  if [ "$acaller" != "$account" ]; then
    fact "served-$label" FAIL "the service's own line for ${repo} names the caller ${acaller:-nobody}, not this repository's build account ${account}: the credential was served to another identity, which is the one field a call cannot claim for itself (G20). Read the lines by hand"
  elif [ "$acred" != "$cred" ]; then
    fact "served-$label" FAIL "the service's own line for ${repo} names the credential ${acred:-none}, not the ${cred} this provider's build is served: a build got a credential that is not its own repository's"
  elif [ -n "$arepo" ] && [ "$arepo" != "$slug" ]; then
    fact "served-$label" FAIL "the service's own line for ${repo} names the repository ${arepo}, not this repository's slug ${slug}: a build was served another repository's record"
  else
    fact "served-$label" PASS "the service wrote ${lines} 'credsvc: audit' line(s) of purpose build for ${repo} (slug ${slug}), served to ${account} as ${cred}: the build's credential came from the service and the caller the service verified is this repository's own build account"
  fi
}

# step_check LABEL REPO SLUG ACCOUNT — the daily image check: the repository's
# own check job runs as the build account and is handed the service's address
# (B4.5's imagecheck half), and its newest execution succeeded.
step_check() { # step_check LABEL REPO SLUG ACCOUNT
  local label=$1 repo=$2 slug=$3 account=$4 ans
  ans=$(reader job "$JOBS_FILE" "$slug" 2>"$SCRATCH/job-$label.err") \
    || { unmeasured "check-$label" "the installation's Cloud Run jobs could not be read: $(last_lines "$SCRATCH/job-$label.err")"; return 0; }
  if [ "$(field_of found "$ans")" != yes ]; then
    unmeasured "check-$label" "this installation deploys no daily check job (a job named ${CHECK_JOB_PREFIX}…) for ${repo} (slug ${slug}): the image check is what re-bakes the repository daily, and no job of this repository could be read. Deploy it with 'fugaro update --repo' in a checkout of ${repo}"
    return 0
  fi
  local name sa url mounts
  name=$(field_of name "$ans")
  sa=$(field_of account "$ans")
  url=$(field_of url "$ans")
  mounts=$(field_of mounts "$ans")
  if [ "$url" != present ]; then
    fact "check-$label" FAIL "the daily check job ${name} of ${repo} carries no FUGARO_CREDENTIALS_SERVICE_URL: the check's own rebuild fetches its credential in-job rather than from the service, so B4.5 has not reached this repository's check job. Re-apply the root ('fugaro update --repo')"
  elif [ "$sa" != "$account" ]; then
    fact "check-$label" FAIL "the daily check job ${name} of ${repo} runs as ${sa:-nobody}, not as the repository's build account ${account}: the check that reads the service is not the account the service admits"
  else
    local m note=""
    case "$mounts" in
    "" | *[!0-9]*) note=" (how many secret mounts it carries could not be read)" ;;
    *) note=" and mounts ${mounts} Secret Manager container(s)" ;;
    esac
    # The newest execution: the daily check succeeded through the service.
    local ex
    if ex=$(gcloud run jobs executions list --project="${P:?}" --region="${L:?}" --job="$name" --limit=1 --format=json 2>"$SCRATCH/exec-$label.err"); then
      ex=$(printf '%s' "$ex" | reader exec /dev/stdin)
      local est
      est=$(field_of state "$ex")
      if [ "$est" = True ] || [ "$est" = true ]; then
        fact "check-$label" PASS "the daily check job ${name} of ${repo} runs as the repository's build account ${account}, carries FUGARO_CREDENTIALS_SERVICE_URL${note}, and its newest execution ended completed: the image check fetches from the service and succeeds"
      else
        fact "check-$label" FAIL "the daily check job ${name} of ${repo} carries the service's address and runs as ${account}, but its newest execution's Completed condition reads ${est:-nothing}: the image check did not succeed. Read it with 'gcloud run jobs executions list --job=${name} --region=${L} --project=${P}'"
      fi
    else
      unmeasured "check-$label" "the check job ${name} carries the service's address and runs as ${account}${note}, but its executions could not be read: $(last_lines "$SCRATCH/exec-$label.err")"
    fi
  fi
}

# step_boundary: the service's own IAM policy — B4.4's grant is the front door,
# and a build account that is not a repository's build of this installation (an
# identity OTHER than a repository's build) is refused there before the route's
# own check. This is the plan's "another repository's build account is refused"
# read where it can be read from the outside: the grant names repository build
# accounts and no public member.
step_boundary() {
  local doc="$SCRATCH/serviceiam.json"
  if ! gcloud run services get-iam-policy "${SVC:?}" --project="${P:?}" --region="${L:?}" --format=json >"$doc" 2>"$SCRATCH/serviceiam.err"; then
    unmeasured boundary "the credentials service's own IAM policy could not be read: $(last_lines "$SCRATCH/serviceiam.err"). YOUR account needs run.services.getIamPolicy, which a project Owner has"
    return 0
  fi
  local ans ok=yes bad="" members=0
  ans=$(reader svciam "$doc")
  local line
  while IFS= read -r line; do
    case "$line" in
    binding=*) ;;
    *) continue ;;
    esac
    local member
    member=$(printf '%s' "$line" | cut -d'|' -f2)
    members=$((members + 1))
    case "$member" in
    allUsers | allAuthenticatedUsers)
      ok=no
      bad="${bad}${bad:+, }${member} (public)"
      ;;
    "${BUILD_ACCOUNT_PREFIX}"*"@${P}.iam.gserviceaccount.com") ;;
    *)
      # Every other member is reported, and only the PUBLIC ones are a FAIL: a
      # person or the coordinator holding the invoker role is not a build
      # account and not a public member, and this row is about the front door a
      # build account reaches — not about who else may invoke the service.
      bad="${bad}${bad:+, }${member}"
      ;;
    esac
  done <<<"$ans"
  if [ "$ok" = yes ]; then
    fact boundary PASS "the credentials service's own IAM policy admits repository build accounts of ${P} (${BUILD_ACCOUNT_PREFIX}…@${P}.iam.gserviceaccount.com) and names no allUsers and no allAuthenticatedUsers member: an identity that is not a repository's build account — another repository's build that holds no invoker here, or any caller that is not a build at all — is refused at Cloud Run's front door before the service's own check (B4.4). Members read: ${bad:-none outside the build accounts}${members:+ (${members} binding member(s))}"
  else
    fact boundary FAIL "the credentials service's own IAM policy names a public member (allUsers or allAuthenticatedUsers), so an identity that is not a repository's build account is not refused at the front door: ${bad}. Read it with 'gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L}'"
  fi
}

# ------------------------------------------------------------ the flip's steps
# step_flip_repo LABEL REPO CREDENTIAL — the flip: the build request names no
# availableSecrets, the check job mounts nothing, and a build still succeeds.
step_flip_repo() { # step_flip_repo LABEL REPO CREDENTIAL
  local label=$1 repo=$2 cred=$3 ans
  ans=$(read_build "$repo" "$label")
  if [ "$(field_of found "$ans")" != yes ]; then
    unmeasured "noavailable-$label" "no build of ${repo} is among the project's last ${BUILDS} builds: submit one with 'fugaro image refresh' after the flipping 'fugaro update' and run this mode again"
    unmeasured "build-flip-$label" "no build of ${repo} was read"
    unmeasured "served-flip-$label" "no build of ${repo} was read"
    unmeasured "noaccessor-$label" "no build of ${repo} was read, so the repository's slug and its check job could not be read"
    return 0
  fi
  local id status account slug url containers envcount
  id=$(field_of id "$ans")
  status=$(field_of status "$ans")
  account=$(field_of account "$ans")
  slug=$(field_of slug "$ans")
  url=$(field_of url "$ans")
  containers=$(field_of containers "$ans")
  envcount=$(field_of secretenv "$ans")

  # noavailable-LABEL: the flip's whole point — the build request carries no
  # availableSecrets and no secretEnv, because the build account holds no
  # accessor on any of them once the class list carries the build's classes.
  case "$containers:$envcount" in
  ""*|*[!0-9]*) unmeasured "noavailable-$label" "the reader did not answer how many containers and secretEnv entries the build carries: $(last_lines "$SCRATCH/builds-$label.err")" ;;
  0:0)
    if [ "$url" != present ]; then
      fact "noavailable-$label" FAIL "the newest build of ${repo} carries no availableSecrets but also names no credentials service: a build with neither a mount nor a service has no credential at all and could only fail. The switch and the flip have moved apart — re-apply the root ('fugaro update --repo') and refresh the image"
    else
      fact "noavailable-$label" PASS "the newest build of ${repo} (${id}) names the credentials service and carries 0 availableSecrets and 0 secretEnv entries: the request asks Cloud Build for no secret, whose accessor the build account has given up (B4.9, D2077)"
    fi
    ;;
  *)
    fact "noavailable-$label" FAIL "the newest build of ${repo} (${id}) still carries ${containers} availableSecrets entries and ${envcount} secretEnv entries, so the build request names the secrets the flip was to take away. The flip has not reached this repository's builds — check the class list (config.BuildServedSecretClasses) and re-apply the root"
    ;;
  esac

  # build-flip-LABEL: a build after the flip still succeeds.
  if [ "$status" = SUCCESS ] && is_build_account "$account" && [ "$url" = present ]; then
    fact "build-flip-$label" PASS "the newest build of ${repo} (${id}) ended SUCCESS, ran as ${account} and names the credentials service: with the mounts gone, the service is the build's only credential path, and the build still bakes"
  else
    fact "build-flip-$label" FAIL "the newest build of ${repo} (${id}) did not read as a success of the served build: status ${status}, account ${account:-none}, service ${url}. A build after the flip whose request names no secret must get every credential from the service, so a build that could not finish is the flip having broken the path"
  fi

  # served-flip-LABEL: the service's own lines for that build.
  if [ -z "$slug" ] || [ "$slug" = "-" ]; then
    unmeasured "served-flip-$label" "the newest build of ${repo} carries no _SLUG substitution, so the service's own lines for it cannot be read"
  else
    step_served "$label-flip" "$repo" "$slug" "$account" "$cred"
  fi

  # noaccessor-LABEL: the daily check job mounts nothing, which is the flip's
  # other half (B4.9 drops the check job's secret_key_ref mounts). The build
  # account's own accessor leaves the containers with them; what is read here is
  # the mount the job carries, which is where the address used to be.
  local jans
  if [ -z "$slug" ] || [ "$slug" = "-" ]; then
    unmeasured "noaccessor-$label" "the newest build of ${repo} carries no _SLUG substitution, so the repository's check job cannot be found"
  elif jans=$(reader job "$JOBS_FILE" "$slug" 2>"$SCRATCH/job-$label.err"); then
    if [ "$(field_of found "$jans")" != yes ]; then
      unmeasured "noaccessor-$label" "no daily check job of ${repo} (slug ${slug}) could be read, so whether it still mounts a secret is unmeasured"
    else
      local jm
      jm=$(field_of mounts "$jans")
      case "$jm" in
      "" | *[!0-9]*) unmeasured "noaccessor-$label" "how many secret mounts the check job ${slug} carries could not be read" ;;
      0) fact "noaccessor-$label" PASS "the daily check job of ${repo} carries 0 Secret Manager mounts: with the build account's accessor gone (B4.9) the check's own rebuild reads its credential, like the repository's builds, from the service" ;;
      *) fact "noaccessor-$label" FAIL "the daily check job of ${repo} still mounts ${jm} Secret Manager container(s): the flip has not dropped the check job's secret_key_ref mounts (B4.9, check.tf), so the build account still needs an accessor on them. Re-apply the root" ;;
      esac
    fi
  else
    unmeasured "noaccessor-$label" "the installation's Cloud Run jobs could not be read: $(last_lines "$SCRATCH/job-$label.err")"
  fi
}

# print_plan: what a real run would read, in order, and what its Restore would
# delete. Printed by --dry-run, which runs NOTHING: no gcloud and no fugaro is
# called, so this mode is safe on a machine with no credentials at all.
print_plan() {
  say "dry run: NOTHING below was run, and no tool was called at all — not gcloud, not fugaro. A real run would first check that this is the fugaro-dev installation (fugaro doctor --project fugaro --all) and that fugaro version is not a development build, then read, in this order:"
  say "  [network] gcloud builds list --project=${P} --limit=${BUILDS} --format=json  (the project's recent builds; the two repositories' own are picked out by their _REPO_URL substitution)"
  say "  [network] gcloud run jobs list --project=${P} --region=${L} --format=json  (the installation's Cloud Run jobs; each repository's daily check job is the fugarochk-… one)"
  say "  [network] gcloud logging read 'resource.labels.service_name=${SVC} jsonPayload.purpose=build' --project=${P}  (the service's OWN credsvc: audit lines for each repository, retried while Cloud Logging catches up)"
  say "  [network] gcloud run jobs executions list --job=<the check job> --project=${P} --region=${L} --format=json  (the daily check's newest execution)"
  say "  [network] gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L} --format=json  (who may invoke the service: B4.4's grant to the build accounts, and no public member)"
  if [ "$MODE" = flip ]; then
    say "and, for each repository, it would read its NEWEST build's request for the two things the flip takes away: that it carries no availableSecrets and no secretEnv, that its check job mounts nothing, and that the build still ended SUCCESS with the service named."
  else
    say "and, for each repository, it would read its newest build (status, the account it ran as, its _CREDENTIALS_SERVICE_URL, and the availableSecrets it still carries) and the service's own lines for it."
  fi
  say "This check creates nothing, submits no build and deletes nothing: its Restore removes the scratch directory and nothing else. The builds it reads are the ones YOU submitted with 'fugaro image refresh'."
}

main() {
  need_tools
  scratch_up
  guard_xtrace
  guard_inputs

  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "the plan above was printed and NOTHING was called: no gcloud, no fugaro. The guards that read the cloud (the installation's project, its version and its builds) run only in a real run"
    return 0
  fi

  guard_project
  read_builds
  read_jobs

  if [ "$MODE" = flip ]; then
    step_flip_repo github "$REPO" github-app-key
    step_flip_repo bitbucket "$BB_REPO" bitbucket-token
  else
    step_service
    step_repo github "$REPO" github-app-key
    step_repo bitbucket "$BB_REPO" bitbucket-token
    step_check github "$REPO" "$(field_of slug "$(read_build "$REPO" github)")" "$(field_of account "$(read_build "$REPO" github)")"
    step_check bitbucket "$BB_REPO" "$(field_of slug "$(read_build "$BB_REPO" bitbucket)")" "$(field_of account "$(read_build "$BB_REPO" bitbucket)")"
    step_boundary
  fi
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS_SWITCH=(fugaro-version project-region xtrace inputs service
  build-github mounts-github served-github check-github
  build-bitbucket mounts-bitbucket served-bitbucket check-bitbucket
  boundary restore scratch-removed)
BLOCK_KEYS_FLIP=(fugaro-version project-region xtrace inputs
  noavailable-github noaccessor-github build-flip-github served-github-flip
  noavailable-bitbucket noaccessor-bitbucket build-flip-bitbucket served-bitbucket-flip
  restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local keys=("${BLOCK_KEYS_SWITCH[@]}")
  if [ "$MODE" = flip ]; then keys=("${BLOCK_KEYS_FLIP[@]}"); fi
  local key printed=""
  printf '\nCHECK 196 RESULT\n' >&3
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
  printf 'CHECK 196 RESULT END (exit %s)\n' "$status" >&3
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed.
# This check has no cleanup that writes, so a second Ctrl-C during it is safe;
# the handler is here only so the RESULT block is printed on an interrupt.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

MODE=switch
case "${1:-}" in
--dry-run) DRY_RUN=1 ;;
--after-flip)
  MODE=flip
  case "${2:-}" in
  --dry-run) DRY_RUN=1 ;;
  "") ;;
  *) die "usage: $PROG [--dry-run|--after-flip [--dry-run]]" ;;
  esac
  ;;
"") ;;
*) die "usage: $PROG [--dry-run|--after-flip [--dry-run]]" ;;
esac

REPO=${CHECK196_REPO:-}
BB_REPO=${CHECK196_BITBUCKET:-}
BUILDS=${CHECK196_BUILDS:-50}
LOG_AGE=${CHECK196_LOG_AGE:-90m}
LOG_LIMIT=${CHECK196_LOG_LIMIT:-200}
LOG_WAIT_SECONDS=${CHECK196_LOG_WAIT_SECONDS:-60}
LOG_POLL_SECONDS=${CHECK196_LOG_POLL_SECONDS:-10}
DRY_RUN=${CHECK196_DRY_RUN:-${DRY_RUN:-}}
KEEP_SCRATCH=${CHECK196_KEEP_SCRATCH:-}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
