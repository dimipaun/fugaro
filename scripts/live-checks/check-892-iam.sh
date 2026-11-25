#!/usr/bin/env bash
# Check 892 — the IAM a member and an admin hold, against the Perms column of
# docs/repo-coupling-audit/report.md. Run by YOU, on fugaro. It settles U3 of
# that audit's reconciliation.
#
# The report names three permission sets a row of it needs:
#
#   TF-admin  an admin in the grants node `config/admin/access`
#             (internal/access/store.go PathNode), from which `fugaro update`
#             derives the local `terraform.admins` (internal/cli/update_access.go
#             deriveAccess): applies Terraform.
#   DB-admin  roles/firebasedatabase.admin on the Firebase project: the GCP
#             project's owners and editors (infra.ProjectAdmins) plus the
#             budget admins (modules/firebase/iam.tf).
#   operator  an operator or admin in the grants node (`terraform.operators`):
#             starts runs, and by design acts as a repository's build and job
#             service accounts (modules/repo/build.tf operator_build).
#
# It reads, and only reads:
#
#   - the installation's local config (`terraform.admins`, `operators`,
#     `budget_admins`, `budget.rtdb_url`, `budget.firebase_project`);
#   - the GCP project's IAM policy and the Firebase project's;
#   - the IAM policy of every per-repository build account ("Fugaro build
#     <slug>", gcp.BuildSADisplayName) and job account ("Fugaro job <slug>
#     <workflow>", gcp.JobSADisplayName);
#   - the grants node, with its values: they are the principals and roles this
#     check exists to compare, and they hold no secret.
#
# It prints, for each member you name, which of TF-admin, DB-admin and operator
# it holds and the report rows it can therefore exercise, and then the whole
# matrix (principal, role in the node, local lists, project and resource roles
# held) for the report's live columns.
#
# It FAILs on one rule: a principal the node places as `operator` or `viewer`,
# or scopes to repositories, that holds roles/owner, roles/editor or
# roles/resourcemanager.projectIamAdmin on the GCP project — an admin in all but
# name. Operators and viewers legitimately hold many other grants (custom roles,
# actAs on several accounts, secret version adders), so nothing else they hold is
# judged. An owner or editor the node does not place is an implicit admin
# (D1592) and is reported, never failed. A role in the node that is not viewer,
# operator or admin is a FAIL too: a role the report does not name is never a
# silent pass. A local `terraform.admins` that differs from the node's
# project admins is reported (DIFFERS), not failed: the node is the authority
# and the next `fugaro update` rewrites the local copy.
#
# Usage:      bash scripts/live-checks/check-892-iam.sh <fugaro-project> [member…]
#             member      an address (ada@example.com) or an IAM member
#                         (user:ada@example.com, group:…, serviceAccount:…)
# Environment CHECK892_CONFIG  the installation's local config, when it is not
#                         $XDG_CONFIG_HOME/fugaro/projects/<fugaro-project>.yaml
# Needs:      bash, mktemp, python3, gcloud, curl.
# Calls:      gcloud auth print-access-token; gcloud projects get-iam-policy;
#             gcloud iam service-accounts list | get-iam-policy; one curl GET of
#             <rtdb>/config/admin/access.json. Every call runs with stdin
#             closed and every gcloud call with --quiet. Nothing is written.
# Runs:       by YOU. It refuses to start in a coding agent's session and in a
#             traced shell. Its test (check-892-iam_test.sh) drives it against
#             fakes on PATH and touches no cloud.
# Token:      the access token goes into a 0600 curl config file in a 0700
#             scratch directory that the exit trap removes; it is never printed
#             and never on an argv.
# Output:     progress on stderr, ONE block on stdout headed CHECK 892 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        no principal is an admin in all but name
#               FAIL (exit 1)        at least one is, or the node holds a role
#                                    the report does not name
#               INCOMPLETE (exit 2)  the run stopped before it could compare
#               INCOMPLETE (exit 3)  it compared, and a read it needed for part
#                                    of the matrix did not answer (UNVERIFIED)
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
AUTH_CONF=""
MATRIX=""

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY STATE "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

usage() {
  cat <<'USAGE'
Check 892 — the IAM a member and an admin hold, against the report's Perms
column. Read-only; it asks nothing and writes nothing.

Usage:
  bash scripts/live-checks/check-892-iam.sh <fugaro-project> [member…]
  bash scripts/live-checks/check-892-iam.sh --help

It reads the GCP and Firebase projects' IAM, every per-repository build and job
account's IAM, the grants node config/admin/access and the local config's
terraform lists. For each member named it prints TF-admin, DB-admin and
operator and the report rows those allow, then the whole matrix. It FAILs only
when a principal the node places as operator or viewer (or scopes to
repositories) holds roles/owner, roles/editor or
roles/resourcemanager.projectIamAdmin on the GCP project.

  PASS (exit 0)  FAIL (exit 1)  INCOMPLETE (exit 2 stopped, exit 3 unverified)
USAGE
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs Check 892: the owner alone reads a project's IAM. Run it yourself: bash scripts/live-checks/check-892-iam.sh <fugaro-project> [member…]"
}

guard_xtrace() {
  [ "$XTRACE_AT_START" = no ] ||
    stop "this shell is tracing (bash -x), and its trace would print the access token this check mints. Run it without -x"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads the project with it"
}

PROJECT=""
MEMBERS=()
while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  -*) stop "unknown argument $1 (usage: check-892-iam.sh <fugaro-project> [member…], or --help)" ;;
  *)
    if [ -z "$PROJECT" ]; then PROJECT=$1; else MEMBERS+=("$1"); fi
    shift
    ;;
  esac
done

guard_xtrace
guard_agent

[ -n "$PROJECT" ] || stop "no Fugaro project was named (usage: check-892-iam.sh <fugaro-project> [member…])"
printf '%s' "$PROJECT" | grep -Eq '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$' ||
  stop "'$PROJECT' is not a Fugaro project name"
for m in ${MEMBERS[@]+"${MEMBERS[@]}"}; do
  printf '%s' "$m" | grep -Eq '^([A-Za-z]+:)?[!-~]+@[!-~]+$' ||
    stop "'$m' is not an address or an IAM member (ada@example.com, user:ada@example.com)"
done

need mktemp
need python3
need gcloud
need curl

CONFIG=${CHECK892_CONFIG:-${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/${PROJECT}.yaml}

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk892.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS=$SCRATCH/facts
: >"$FACTS"
AUTH_CONF=$SCRATCH/auth.conf

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

verdict() { # verdict <facts>
  if printf '%s\n' "$1" | grep -q '^aborted|'; then
    printf 'INCOMPLETE'
  elif printf '%s\n' "$1" | grep -q '|FAIL|'; then
    printf 'FAIL'
  elif printf '%s\n' "$1" | grep -q '|UNVERIFIED|'; then
    printf 'INCOMPLETE'
  else
    printf 'PASS'
  fi
}

print_block() { # print_block <facts> <status>
  printf '\nCHECK 892 RESULT\n'
  if [ -n "$MATRIX" ]; then printf '%s\n' "$MATRIX"; fi
  printf 'ROWS\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-10s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 892 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  if [ -f "$SCRATCH/matrix" ]; then MATRIX=$(cat "$SCRATCH/matrix"); fi
  cleanup
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT compared"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory, with the token's curl config file, is gone"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH}, and it holds an access token: remove it yourself"
  fi
  local v
  v=$(verdict "$facts")
  if [ -n "$ABORT" ] || [ "$REACHED_END" = no ]; then
    status=2
  elif [ "$v" = FAIL ]; then
    status=1
  elif [ "$v" = INCOMPLETE ]; then
    status=3
  else
    status=0
  fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

# read_config prints the config's needed members as JSON. yaml.v3 writes block
# style, so an indentation reader suffices and python3 needs no YAML module.
read_config() { # read_config <file>
  python3 - "$1" <<'PY'
import json, re, sys

def unquote(v):
    v = v.strip()
    if len(v) >= 2 and v[0] == v[-1] and v[0] in "'\"":
        return v[1:-1]
    return v

want_scalars = {"name", "gcp_project", "budget.rtdb_url", "budget.firebase_project"}
want_lists = {"terraform.admins", "terraform.operators", "terraform.budget_admins"}
out = {k: "" for k in want_scalars}
out.update({k: [] for k in want_lists})
stack = []
for raw in open(sys.argv[1], encoding="utf-8"):
    line = raw.rstrip("\n")
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    line = re.sub(r"\s+#.*$", "", line)
    ind = len(line) - len(line.lstrip(" "))
    body = line.strip()
    if body == "-" or body.startswith("- "):
        path = ".".join(k for _, k in stack)
        if path in want_lists:
            out[path].append(unquote(body[1:]))
        continue
    m = re.match(r"^([A-Za-z0-9_./-]+):(?:\s+(.*))?$", body)
    if not m:
        continue
    while stack and stack[-1][0] >= ind:
        stack.pop()
    key, val = m.group(1), (m.group(2) or "").strip()
    path = ".".join([k for _, k in stack] + [key])
    if val == "":
        stack.append((ind, key))
    elif val.startswith("[") and path in want_lists:
        out[path] = [unquote(x) for x in val.strip("[]").split(",") if x.strip()]
    elif path in want_scalars:
        out[path] = unquote(val)
json.dump(out, sys.stdout)
PY
}

cfg_get() { # cfg_get <key>
  python3 -c 'import json,sys; v=json.load(open(sys.argv[1])).get(sys.argv[2],""); print(v if isinstance(v,str) else "")' "$SCRATCH/config.json" "$1" </dev/null
}

mint_token() {
  local tok
  tok=$(gcloud auth print-access-token --quiet </dev/null 2>"$SCRATCH/token.err") || return 1
  [ -n "$tok" ] || return 1
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$AUTH_CONF") || return 1
  chmod 600 "$AUTH_CONF" || return 1
  unset tok
}

first_line() { head -1 "$1" 2>/dev/null | cut -c1-300 || true; }

# ------------------------------------------------------------------ the config
[ -f "$CONFIG" ] || stop "no local config for $PROJECT at $CONFIG: name it with CHECK892_CONFIG"
read_config "$CONFIG" >"$SCRATCH/config.json" 2>"$SCRATCH/config.err" ||
  stop "the local config $CONFIG could not be read: $(first_line "$SCRATCH/config.err")"
NAME=$(cfg_get name)
GCP=$(cfg_get gcp_project)
RTDB=$(cfg_get budget.rtdb_url)
RTDB=${RTDB%/}
FP=$(cfg_get budget.firebase_project)
[ -z "$NAME" ] || [ "$NAME" = "$PROJECT" ] || stop "$CONFIG is the config of '$NAME', not '$PROJECT'"
printf '%s' "$GCP" | grep -Eq '^[a-z][a-z0-9-]{4,28}[a-z0-9]$' ||
  stop "$CONFIG names no valid gcp_project ('$GCP')"
printf '%s' "$RTDB" | grep -Eq '^https://[a-z0-9-]+(\.[a-z0-9-]+)*\.(firebaseio\.com|firebasedatabase\.app)$' ||
  stop "$CONFIG names no Realtime Database URL (budget.rtdb_url '$RTDB'), and the grants node lives there. Run 'fugaro update' to write it"
if [ -n "$FP" ]; then
  printf '%s' "$FP" | grep -Eq '^[a-z][a-z0-9-]{4,28}[a-z0-9]$' || stop "$CONFIG names an invalid budget.firebase_project ('$FP')"
fi
fact config PASS "read $CONFIG: GCP project $GCP, Firebase project ${FP:-<none>}, database $RTDB"

# ------------------------------------------------------------------- the token
say "minting an access token (gcloud auth print-access-token)"
mint_token || stop "gcloud auth print-access-token did not answer ($(first_line "$SCRATCH/token.err")): run 'gcloud auth login' and this check again"
fact token PASS "an access token was written to a 0600 file in this run's 0700 scratch directory, never printed or put on an argv"

# --------------------------------------------------------------------- the IAM
say "reading the IAM policy of $GCP"
gcloud projects get-iam-policy "$GCP" --format=json --quiet </dev/null >"$SCRATCH/iam-gcp.json" 2>"$SCRATCH/iam-gcp.err" ||
  stop "the IAM policy of $GCP could not be read: $(first_line "$SCRATCH/iam-gcp.err")"
fact gcp-iam PASS "the IAM policy of $GCP was read"

if [ -z "$FP" ]; then
  fact firebase-iam UNVERIFIED "$CONFIG names no budget.firebase_project, so no DB-admin was read"
elif [ "$FP" = "$GCP" ]; then
  cp "$SCRATCH/iam-gcp.json" "$SCRATCH/iam-fb.json"
  fact firebase-iam PASS "the Firebase project is $GCP itself, whose policy was read above"
else
  say "reading the IAM policy of $FP"
  if gcloud projects get-iam-policy "$FP" --format=json --quiet </dev/null >"$SCRATCH/iam-fb.json" 2>"$SCRATCH/iam-fb.err"; then
    fact firebase-iam PASS "the IAM policy of the Firebase project $FP was read"
  else
    rm -f "$SCRATCH/iam-fb.json"
    fact firebase-iam UNVERIFIED "the IAM policy of $FP could not be read ($(first_line "$SCRATCH/iam-fb.err")), so no DB-admin is known"
  fi
fi

say "listing the service accounts of $GCP"
gcloud iam service-accounts list --project "$GCP" --format=json --quiet </dev/null >"$SCRATCH/sa-list.json" 2>"$SCRATCH/sa-list.err" ||
  stop "the service accounts of $GCP could not be listed: $(first_line "$SCRATCH/sa-list.err")"
python3 - "$SCRATCH/sa-list.json" >"$SCRATCH/sa-repo" <<'PY'
import json, re, sys
for a in json.load(open(sys.argv[1])) or []:
    dn, email = a.get("displayName") or "", a.get("email") or ""
    if not re.match(r"^[a-z0-9-]+@[a-z0-9.-]+\.iam\.gserviceaccount\.com$", email):
        continue
    if dn.startswith("Fugaro build "):
        print("build|" + email)
    elif dn.startswith("Fugaro job "):
        print("job|" + email)
PY
: >"$SCRATCH/sa-read"
sa_bad=0
sa_n=0
while IFS='|' read -r kind email; do
  [ -n "$email" ] || continue
  sa_n=$((sa_n + 1))
  if gcloud iam service-accounts get-iam-policy "$email" --project "$GCP" --format=json --quiet </dev/null \
    >"$SCRATCH/sa-$sa_n.json" 2>"$SCRATCH/sa-$sa_n.err"; then
    printf '%s|%s|%s\n' "$kind" "$email" "$SCRATCH/sa-$sa_n.json" >>"$SCRATCH/sa-read"
  else
    sa_bad=$((sa_bad + 1))
    fact sa-iam UNVERIFIED "the IAM policy of $kind account $email could not be read: $(first_line "$SCRATCH/sa-$sa_n.err")"
  fi
done <"$SCRATCH/sa-repo"
[ "$sa_bad" -gt 0 ] || fact sa-iam PASS "all $sa_n per-repository build and job accounts' IAM policies read"

# ------------------------------------------------------------- the grants node
say "reading the grants node config/admin/access"
code=$(curl -q -sS --max-time 30 --config "$AUTH_CONF" -o "$SCRATCH/grants.json" -w '%{http_code}' \
  "$RTDB/config/admin/access.json" </dev/null 2>"$SCRATCH/grants.err") || code=000
[ "$code" = 200 ] ||
  stop "the grants node $RTDB/config/admin/access could not be read (HTTP $code$([ -s "$SCRATCH/grants.err" ] && printf ': %s' "$(first_line "$SCRATCH/grants.err")")): it is the role each principal is compared against"

# ----------------------------------------------------------------- the compare
python3 - "$SCRATCH" "$GCP" "${FP:-}" ${MEMBERS[@]+"${MEMBERS[@]}"} >"$SCRATCH/analysis" 2>"$SCRATCH/analysis.err" <<'PY' ||
import json, os, sys

scratch, gcp, fp = sys.argv[1], sys.argv[2], sys.argv[3]
members = sys.argv[4:]
FORBIDDEN = ("roles/owner", "roles/editor", "roles/resourcemanager.projectIamAdmin")
DB_ROLES = ("roles/firebasedatabase.admin", "roles/owner", "roles/editor")
ROLES = ("viewer", "operator", "admin")
# The report's rows by its Perms column (report.md §2); a row naming two sets
# needs the other one besides, which the note says.
ROWS = {
    "TF-admin": "RC6 RC7 RC8 RC17(+repo-write) RC32(+repo-write) RC34(+repo-write) RC39(+local) RC44 RC48 "
                "RC54 RC55 RC56 RC57 RC59 RC60 RC61 RC62 RC63 RC64 RC65 RC66 RC67 RC68 RC69 RC70 RC71 "
                "RC72 RC73 RC74 RC75 RC77 RC78 RC89",
    "DB-admin": "RC11 RC40 RC41 RC45 RC46 RC47 RC51 RC58 RC95",
    "operator": "RC4 RC5 RC6 RC72 RC73 RC76 RC79",
}

def key(m):
    return m.strip().lower()

def load(name):
    p = os.path.join(scratch, name)
    if not os.path.exists(p):
        return None
    with open(p) as f:
        return json.load(f)

def bindings(policy):
    held = {}
    for b in (policy or {}).get("bindings") or []:
        for m in b.get("members") or []:
            held.setdefault(key(m), set()).add(b.get("role") or "")
    return held

def fact(k, state, detail):
    print("fact|%s|%s|%s" % (k, state, detail.replace("|", "/")))

cfg = load("config.json")
gcp_held = bindings(load("iam-gcp.json"))
fb_policy = load("iam-fb.json")
fb_held = bindings(fb_policy) if fb_policy is not None else None
sa_held = {}
with open(os.path.join(scratch, "sa-read")) as f:
    for line in f:
        kind, email, path = line.rstrip("\n").split("|", 2)
        with open(path) as pf:
            for m, roles in bindings(json.load(pf)).items():
                for r in roles:
                    sa_held.setdefault(m, set()).add("%s@%s" % (r, email.split("@")[0]))

doc = load("grants.json")
grants, bad = [], []
if doc is None:
    fact("grants", "UNVERIFIED", "the grants node is absent: nobody is placed, so every owner and editor is an implicit admin")
else:
    raw = doc.get("grants") if isinstance(doc, dict) else None
    if isinstance(raw, dict):
        raw = [raw[k] for k in sorted(raw, key=lambda s: int(s) if s.isdigit() else 0)]
    if not isinstance(raw, list):
        fact("grants", "FAIL", "the grants node holds no grants list: it is not the shape {grants: [{user, role, scope}]}")
        raw = None
    for g in raw or []:
        if not isinstance(g, dict) or not isinstance(g.get("user"), str) or not isinstance(g.get("scope"), str) or not g["scope"].strip():
            bad.append(json.dumps(g))
            continue
        # access.Decode refuses a member that is not "user:<address>", so one
        # here could never match an IAM member and would hide a finding.
        if not g["user"].lower().startswith("user:"):
            bad.append("%s is not a user: member" % g["user"])
            continue
        if g.get("role") not in ROLES:
            bad.append("%s as %r" % (g.get("user"), g.get("role")))
            continue
        grants.append((key(g["user"]), g["role"], g["scope"].strip()))
    if raw is not None:
        fact("grants", "PASS", "%d grants read from config/admin/access" % len(grants))
if bad:
    fact("grant-roles", "FAIL", "the node holds grants access.Decode would refuse or the report names no role for (viewer, operator, admin): " + "; ".join(bad))
else:
    fact("grant-roles", "PASS", "every grant's role is viewer, operator or admin")

placed = {}
for u, r, s in grants:
    placed.setdefault(u, []).append((r, s))

def project_admin(p):
    return any(r == "admin" and s == "project" for r, s in placed.get(p, []))

def operator(p):
    return any(r in ("operator", "admin") for r, _ in placed.get(p, []))

local = {n: [key(m) for m in cfg.get("terraform." + n) or []] for n in ("admins", "operators", "budget_admins")}

findings = 0
for p in sorted(placed):
    if project_admin(p):
        continue
    for role in FORBIDDEN:
        if role in gcp_held.get(p, set()):
            findings += 1
            where = ", ".join("%s@%s" % (r, s) for r, s in placed[p])
            fact("admin-in-name", "FAIL", "%s is placed as %s and holds %s on %s: an admin in all but name" % (p, where, role, gcp))
if not findings:
    fact("admin-in-name", "PASS", "no principal the node places as operator or viewer, or scopes to repositories, holds roles/owner, roles/editor or roles/resourcemanager.projectIamAdmin on " + gcp)

for p in sorted(gcp_held):
    if p in placed:
        continue
    for role in ("roles/owner", "roles/editor"):
        if role in gcp_held[p]:
            fact("implicit-admin", "INFO", "%s holds %s on %s and the node does not place it: an implicit admin (D1592)" % (p, role, gcp))

node_admins = {p for p in placed if project_admin(p)}
local_users = {m for m in local["admins"] if m.startswith("user:")}
diff = []
for m in sorted(local_users - node_admins):
    diff.append("terraform.admins names %s, which the node does not hold as admin@project" % m)
for m in sorted(node_admins - local_users):
    diff.append("the node holds %s as admin@project and terraform.admins does not name it" % m)
if diff:
    fact("local-admins", "DIFFERS", "; ".join(diff) + ": this machine's copy is stale, and the next fugaro update re-derives it from the node")
else:
    fact("local-admins", "PASS", "terraform.admins names exactly the node's project admins (%s)" % (", ".join(sorted(node_admins)) or "none"))

def holdings(p):
    tf = project_admin(p)
    db = None if fb_held is None else any(r in fb_held.get(p, set()) for r in DB_ROLES)
    return tf, db, operator(p)

def yn(v):
    return "unknown" if v is None else ("yes" if v else "no")

print("matrix|MEMBERS")
if not members:
    print("matrix|  (none named)")
for m in members:
    p = key(m if ":" in m.split("@")[0] else "user:" + m)
    tf, db, op = holdings(p)
    print("matrix|  %s: TF-admin=%s DB-admin=%s operator=%s" % (p, yn(tf), yn(db), yn(op)))
    rows = []
    for name, held in (("TF-admin", tf), ("DB-admin", db), ("operator", op)):
        if held:
            rows.append("%s: %s" % (name, ROWS[name]))
    print("matrix|    rows: %s" % ("; ".join(rows) if rows else "none of the TF-admin, DB-admin or operator rows"))

everyone = set(placed) | set(gcp_held) | set(fb_held or {}) | set(sa_held)
for lst in local.values():
    everyone |= set(lst)
print("matrix|MATRIX")
for p in sorted(everyone):
    node = ",".join("%s@%s" % (r, s) for r, s in placed.get(p, [])) or "-"
    lists = ",".join(n for n in ("admins", "operators", "budget_admins") if p in local[n]) or "-"
    fbr = "unread" if fb_held is None else (",".join(sorted(fb_held.get(p, set()))) or "-")
    print("matrix|  %s  node=%s  local=%s  gcp=%s  firebase=%s  accounts=%s" % (
        p, node, lists, ",".join(sorted(gcp_held.get(p, set()))) or "-", fbr,
        ",".join(sorted(sa_held.get(p, set()))) or "-"))
PY
  stop "the comparison could not be made: $(first_line "$SCRATCH/analysis.err")"

while IFS='|' read -r tag k state detail; do
  case "$tag" in
  fact) fact "$k" "$state" "$detail" ;;
  esac
done <"$SCRATCH/analysis"
sed -n 's/^matrix|//p' "$SCRATCH/analysis" >"$SCRATCH/matrix"

REACHED_END=yes
