#!/usr/bin/env bash
# Tests for scripts/live-checks/check127.sh, with fake `fugaro`, `git` and `gh`
# on PATH: NOTHING here reaches a cloud, a project, a repository or the
# network, no agent runs a live command, and the fake tools record every
# argument they are given so the test can assert that no command ever named
# anything but the sandbox repository's own project and that nothing the check
# reads is ever written back.
#
# The documents the fakes answer with are fixtures this test writes (python3
# renders them, so a case is one variant away from the happy path): the check's
# every reading is a comparison against a document, and a check that reads a
# document correctly is what these cases are about.
#
# Usage: bash scripts/live-checks/check127_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check127.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK127_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
# The failure text is the block's own FACT and CHECK lines, clipped: the whole
# output of a run is long and its interesting part is one row.
block_rows() {
  printf '%s\n' "$1" | grep -E '^(CHECK|FACT|aborted)' | cut -c1-220 | head -30
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; the rows were:
$(block_rows "$1")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env python3
"""A stand-in for `fugaro`, for scripts/live-checks/check127.sh's test only.

Every answer is a fixture the test wrote: the subcommand chooses which one, so
the check's reading of a document is what a case moves. A subcommand or a flag
this fake does not know exits 2 rather than answering something, so a call the
check got wrong cannot hide behind a plausible document.
"""
import json, os, sys

DOCS = os.environ["FAKE_DOCS"]
LOG = os.environ["FAKE_LOG_DIR"]
args = sys.argv[1:]

with open(os.path.join(LOG, "fugaro.calls"), "a") as fh:
    fh.write("fugaro " + " ".join(args) + "\n")


def slug(text):
    return text.replace("/", "__")


def pick(base):
    """A fixture, with the second checkout's own variant when there is one."""
    if os.path.basename(os.getcwd()) == "second":
        other = os.path.join(DOCS, base + "-second.json")
        if os.path.exists(other):
            return other
    return os.path.join(DOCS, base + ".json")


def emit(path):
    if not os.path.exists(path):
        sys.stderr.write("fugaro(fake): no fixture %s\n" % path)
        sys.exit(1)
    sys.stdout.write(open(path, encoding="utf-8").read())


def flag(name):
    if name in args:
        i = args.index(name)
        if i + 1 < len(args):
            return args[i + 1]
    return ""


cmd = args[0] if args else ""

if cmd == "doctor":
    emit(pick("doctor"))
elif cmd == "runs" and len(args) > 1 and args[1] == "ls":
    repo = flag("--repo")
    named = os.path.join(DOCS, "ls-%s.json" % slug(repo))
    emit(named if os.path.exists(named) else os.path.join(DOCS, "ls.json"))
elif cmd == "diagnose":
    emit(os.path.join(DOCS, "diagnose-%s.json" % slug(args[1])))
elif cmd == "budget" and len(args) > 1 and args[1] == "preview":
    emit(os.path.join(DOCS, "preview-critical.json" if flag("--mode") else "preview-own.json"))
elif cmd == "config" and len(args) > 1 and args[1] == "show":
    emit(pick("config"))
elif cmd == "security" and len(args) > 1 and args[1] == "paths":
    emit(os.path.join(DOCS, "paths.json"))
elif cmd == "backlog" and len(args) > 1 and args[1] == "list":
    emit(pick("backlog"))
elif cmd == "backlog" and len(args) > 1 and args[1] == "index":
    folder = flag("--dir")
    if not folder or not os.path.isdir(folder):
        sys.stderr.write("fugaro(fake): backlog index needs a --dir that is a folder\n")
        sys.exit(2)
    src = os.path.join(DOCS, "index.md")
    if not os.path.exists(src):
        sys.stderr.write("fugaro(fake): no index.md fixture\n")
        sys.exit(1)
    open(os.path.join(folder, "README.md"), "w", encoding="utf-8").write(
        open(src, encoding="utf-8").read())
    sys.stdout.write("wrote %s/README.md\n" % folder)
elif cmd == "watch":
    emit(os.path.join(DOCS, "watch.json"))
else:
    sys.stderr.write("fugaro(fake): %s is not a call this check makes\n" % " ".join(args))
    sys.exit(2)
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env python3
"""A stand-in for `git`, for scripts/live-checks/check127.sh's test only.

It answers three reads — the origin remote, the branch checked out and a file
at a ref — and fails everything else, `rev-parse --show-toplevel` included: the
check asks that one of its scratch directory, and an answer would refuse every
run.
"""
import os, sys

LOG = os.environ["FAKE_LOG_DIR"]
args = sys.argv[1:]
with open(os.path.join(LOG, "git.calls"), "a") as fh:
    fh.write("git " + " ".join(args) + "\n")

second = os.path.basename(os.getcwd()) == "second"
rest = args
if args[:1] == ["-C"] and len(args) > 2:
    second = os.path.basename(args[1]) == "second"
    rest = args[2:]

if rest[:3] == ["remote", "get-url", "origin"]:
    if second:
        print(os.environ.get("FAKE_GIT_ORIGIN_SECOND", "git@github.com:sandbox-org/second.git"))
    else:
        print(os.environ.get("FAKE_GIT_ORIGIN", "git@github.com:sandbox-org/scratch.git"))
elif rest[:2] == ["rev-parse", "--abbrev-ref"]:
    print(os.environ.get("FAKE_GIT_BRANCH", "main"))
elif rest[:1] == ["show"]:
    path = os.path.join(os.environ["FAKE_DOCS"], "branch-fugaro.yaml")
    if not os.path.exists(path):
        sys.exit(128)
    sys.stdout.write(open(path, encoding="utf-8").read())
else:
    sys.exit(1)
GIT_EOF
  chmod +x "$1/bin/git"
}

write_fake_gh() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gh" <<'GH_EOF'
#!/usr/bin/env python3
"""A stand-in for `gh`, for scripts/live-checks/check127.sh's test only.

`pr view N` answers `pr-N.json`, `run list` answers `runs.json`, and the two
`api` shapes the check reads answer `commit-<sha>.json` and
`contents-<path>.json`. Anything else fails: a command the check got wrong is
not answered by a plausible document.
"""
import os, sys

DOCS = os.environ["FAKE_DOCS"]
LOG = os.environ["FAKE_LOG_DIR"]
args = sys.argv[1:]
with open(os.path.join(LOG, "gh.calls"), "a") as fh:
    fh.write("gh " + " ".join(args) + "\n")


def emit(path):
    if not os.path.exists(path):
        sys.stderr.write("gh(fake): no fixture %s\n" % path)
        sys.exit(1)
    sys.stdout.write(open(path, encoding="utf-8").read())


if args[:1] == ["pr"] and args[1:2] == ["view"]:
    emit(os.path.join(DOCS, "pr-%s.json" % args[2]))
elif args[:2] == ["run", "list"]:
    emit(os.path.join(DOCS, "runs.json"))
elif args[:1] == ["api"] and len(args) > 1:
    ref = args[1]
    if "/commits/" in ref:
        emit(os.path.join(DOCS, "commit-%s.json" % ref.rsplit("/", 1)[1]))
    elif "/contents/" in ref:
        path = ref.split("/contents/", 1)[1].split("?", 1)[0]
        emit(os.path.join(DOCS, "contents-%s.json" % path.replace("/", "__")))
    else:
        sys.exit(2)
else:
    sys.stderr.write("gh(fake): %s is not a call this check makes\n" % " ".join(args))
    sys.exit(2)
GH_EOF
  chmod +x "$1/bin/gh"
}

# ------------------------------------------------------------------ fixtures
# fixtures DIR [VARIANT] — every document the fakes answer with. A variant
# moves ONE thing off the happy path, so a case reads as the reading it breaks.
fixtures() { # fixtures DIR VARIANT
  python3 - "$1" "${2:-ok}" <<'PY'
import base64, json, os, sys

root, variant = sys.argv[1], sys.argv[2]
docs = os.path.join(root, "docs")
os.makedirs(docs, exist_ok=True)


def w(name, doc, raw=False):
    path = os.path.join(docs, name)
    if raw:
        open(path, "w", encoding="utf-8").write(doc)
    else:
        open(path, "w", encoding="utf-8").write(json.dumps(doc, indent=2))


SLUG = "scratch-ab12cd34"
POC = SLUG + "/20261117-120000-0001"
CRITICAL = SLUG + "/20261117-130000-0002"
HOTFIX = SLUG + "/20261117-140000-0003"
REF = SLUG + "/20261117-150000-0004"
AREAS = SLUG + "/20261117-160000-0005"
SECOND_SLUG = "second-cd34ef56"
RUN2 = SECOND_SLUG + "/20261117-170000-0006"


def row(run, mode, **kw):
    r = {
        "run": run,
        "repo": "sandbox-org/scratch" if run.startswith(SLUG) else "sandbox-org/second",
        "rigor": {"mode": mode, "from": kw.pop("frm", "request"), "at": "bootstrap"},
        "recipe": "default",
        "status": "succeeded",
        "outcome": "ready",
        "terminal": True,
        "settled": True,
        "pr": kw.pop("pr", 0),
        "branch": kw.pop("branch", "fugaro/" + run.split("/")[1]),
        "base_branch": kw.pop("base", "main"),
        "cost": {"model_usd": kw.pop("cost", 1.5), "total_usd": 1.8},
        "changed_files": {"total": 3},
    }
    r["rigor"].update(kw.pop("rigor", {}))
    r.update(kw)
    return r


rows = [
    row(POC, "poc"),
    row(CRITICAL, "critical", cost=2.5),
    row(HOTFIX, "hotfix"),
    row(REF, "pro", frm="repo", branch="fugaro/feature-rigor",
        rigor={"revision": "origin/main", "from": "repo"}),
    row(AREAS, "critical", frm="area",
        rigor={"areas": [{"name": "terraform", "mode": "critical", "at": "bootstrap"},
                         {"name": "workflows", "mode": "critical", "at": "round"}]}),
    row(RUN2, "pro", frm="repo"),
]
if variant == "poc-not-poc":
    rows[0]["rigor"]["mode"] = "critical"
if variant == "no-poc-row":
    rows = [r for r in rows if r["run"] != POC]
if variant == "ref-own-branch":
    rows[3]["rigor"]["revision"] = "origin/fugaro/feature-rigor"
if variant == "ref-request":
    rows[3]["rigor"]["from"] = "request"
if variant == "no-areas":
    rows[4]["rigor"]["areas"] = rows[4]["rigor"]["areas"][:1]
w("ls.json", {"project": "sandbox", "runs": rows, "totals": {"runs": len(rows)}})

DIAG = {
    POC: {"rounds": [1], "levels": ["l1", "l2"], "tolerated": 1},
    CRITICAL: {"rounds": [1, 2], "levels": ["l1", "l2", "l3"], "tolerated": 0},
    HOTFIX: {"rounds": [1], "levels": ["l1"], "tolerated": 0},
    REF: {"rounds": [1], "levels": ["l2"], "tolerated": 0},
    AREAS: {"rounds": [1, 2], "levels": ["l1", "l2"], "tolerated": 0},
    RUN2: {"rounds": [1], "levels": ["l2"], "tolerated": 0},
}
if variant == "critical-tolerates-more":
    DIAG[CRITICAL]["tolerated"] = 4
if variant == "floor-tolerated":
    DIAG[HOTFIX]["tolerated"] = 2
for ref, spec in DIAG.items():
    mode = {"poc": "poc", "critical": "critical", "hotfix": "hotfix"}.get(
        [r for r in rows if r["run"] == ref][0]["rigor"]["mode"] if any(
            r["run"] == ref for r in rows) else "poc", "critical")
    doc = {
        "project": "sandbox",
        "row": {"run": ref, "repo": "sandbox-org/scratch",
                "rigor": {"mode": mode, "from": "request"}},
        "reviews": [{"round": n, "tier": "senior", "verdict": "approved", "findings": 0}
                    for n in spec["rounds"]],
        "triage": [{"round": 1, "mode": mode, "level": lv} for lv in spec["levels"]],
        "failure_class": "",
    }
    if spec["tolerated"]:
        doc["tolerated"] = {"total": spec["tolerated"], "findings": [
            {"title": "a finding the mode tolerated", "mode": mode, "severity": "low"}]}
    w("diagnose-%s.json" % ref.replace("/", "__"), doc)

w("doctor.json", {"project": {"name": "sandbox", "gcp_project": "sandbox-proj",
                              "region": "us-east1"}})
if variant == "second-project":
    w("doctor-second.json", {"project": {"name": "other", "gcp_project": "other-proj",
                                         "region": "us-east1"}})


def config(value):
    return {"config_sha256": "0" * 64, "values": [
        {"path": "agent.recipe", "value": "default", "source": "default"},
        {"path": "git.base_branch", "value": "main", "source": "project"},
        {"path": "rigor.default", "value": value, "source": "repo"},
        {"path": "rigor.areas", "value": [], "source": "repo"}]}


w("config.json", config("poc"))
w("config-second.json", config("pro"))
if variant == "same-default":
    w("config-second.json", config("poc"))
if variant == "no-default":
    w("config.json", {"config_sha256": "0" * 64, "values": [
        {"path": "git.base_branch", "value": "main", "source": "project"},
        {"path": "rigor.default", "value": None, "source": "default"}]})

w("preview-critical.json", {"ceiling_usd": 25.0, "per_run_usd": 5.0,
                            "knobs": {"first_line_rounds": 3, "review_rounds": 5},
                            "verdict": "bounded", "bounded": True})
w("preview-own.json", {"ceiling_usd": 25.0, "per_run_usd": 5.0,
                       "knobs": {"first_line_rounds": 3, "review_rounds": 3},
                       "verdict": "bounded", "bounded": True})
if variant == "preview-below":
    w("preview-critical.json", {"ceiling_usd": 0.5, "per_run_usd": 0.5,
                                "knobs": {"first_line_rounds": 1, "review_rounds": 1},
                                "verdict": "bounded", "bounded": True})
if variant == "preview-lower-knobs":
    w("preview-critical.json", {"ceiling_usd": 25.0, "per_run_usd": 5.0,
                                "knobs": {"first_line_rounds": 3, "review_rounds": 2},
                                "verdict": "bounded", "bounded": True})

w("paths.json", {"config_sha256": "0" * 64, "project": "sandbox", "repo": "sandbox-org/scratch",
                 "base": "main", "checked": [{"path": "deploy/terraform/check.tf",
                                              "sensitive": True,
                                              "matched": {"name": "terraform", "category": "infra",
                                                          "source": "repo"}}]})
if variant == "path-not-sensitive":
    w("paths.json", {"config_sha256": "0" * 64, "project": "sandbox", "repo": "sandbox-org/scratch",
                     "base": "main", "checked": [{"path": "deploy/terraform/check.tf",
                                                  "sensitive": False, "matched": None}]})

ITEMS = [
    {"id": "2026-11-17-1", "title": "the first item", "mode": "none", "area": "repo",
     "status": "closed", "filed_by": "sandbox", "filed_in": "hand",
     "date": "2026-11-17", "path": "docs/backlog/2026-11-17-1-first.md"},
    {"id": "2026-11-17-2", "title": "the second item", "mode": "none", "area": "repo",
     "status": "open", "filed_by": "sandbox", "filed_in": "hand",
     "date": "2026-11-17", "path": "docs/backlog/2026-11-17-2-second.md"},
]
w("backlog.json", {"dir": "docs/backlog", "items": ITEMS, "open": 1, "closed": 1, "dropped": 0})

w("watch.json", {"project": "sandbox", "runs": [
    {"run": SLUG + "/20261117-180000-0007", "mode": "critical", "from": "repo",
     "stage": "implement", "health": "ok"}]})
if variant == "watch-nomode":
    w("watch.json", {"project": "sandbox", "notice": "the registry entry carries no rigor",
                     "runs": [{"run": SLUG + "/20261117-180000-0007", "mode": "-", "from": "-",
                               "stage": "implement", "health": "ok"}]})
if variant == "watch-empty":
    w("watch.json", {"project": "sandbox", "runs": []})

MERGE = "b" * 40
w("pr-123.json", {"state": "MERGED", "mergedAt": "2026-11-17T12:00:00Z",
                  "mergeCommit": {"oid": MERGE}, "headRefName": "fugaro/hotfix-1",
                  "baseRefName": "main", "url": "https://example.invalid/123",
                  "files": [{"path": "src/app.go"}],
                  "commits": [{"oid": "c" * 40}]})
w("runs.json", [{"databaseId": 1, "headSha": MERGE, "event": "push",
                 "status": "completed", "conclusion": "success"},
                {"databaseId": 2, "headSha": "d" * 40, "event": "push",
                 "status": "completed", "conclusion": "success"}])
if variant == "no-post-merge-ci":
    w("runs.json", [{"databaseId": 2, "headSha": "d" * 40, "event": "push",
                     "status": "completed", "conclusion": "success"}])

ITEM_A = "docs/backlog/2026-11-17-1-first.md"
ITEM_B = "docs/backlog/2026-11-17-2-second.md"
INDEX = "docs/backlog/README.md"
A_COMMIT, B_COMMIT = "e" * 40, "f" * 40

w("pr-410.json", {"state": "MERGED", "mergedAt": "2026-11-17T13:00:00Z",
                  "mergeCommit": {"oid": "1" * 40}, "headRefName": "fugaro/item-a",
                  "baseRefName": "main", "url": "https://example.invalid/410",
                  "files": [{"path": ITEM_A}, {"path": INDEX}],
                  "commits": [{"oid": A_COMMIT}]})
w("pr-411.json", {"state": "MERGED", "mergedAt": "2026-11-17T14:00:00Z",
                  "mergeCommit": {"oid": "2" * 40}, "headRefName": "fugaro/item-b",
                  "baseRefName": "main", "url": "https://example.invalid/411",
                  "files": [{"path": ITEM_B}, {"path": INDEX}],
                  "commits": [{"oid": B_COMMIT}]})
if variant == "item-no-index":
    w("pr-410.json", {"state": "MERGED", "mergedAt": "2026-11-17T13:00:00Z",
                      "mergeCommit": {"oid": "1" * 40}, "headRefName": "fugaro/item-a",
                      "baseRefName": "main", "url": "https://example.invalid/410",
                      "files": [{"path": ITEM_A}], "commits": [{"oid": A_COMMIT}]})
if variant == "item-same-name":
    w("pr-411.json", {"state": "MERGED", "mergedAt": "2026-11-17T14:00:00Z",
                      "mergeCommit": {"oid": "2" * 40}, "headRefName": "fugaro/item-b",
                      "baseRefName": "main", "url": "https://example.invalid/411",
                      "files": [{"path": ITEM_A}, {"path": INDEX}],
                      "commits": [{"oid": B_COMMIT}]})
if variant == "item-not-merged":
    w("pr-411.json", {"state": "OPEN", "mergedAt": None, "mergeCommit": {},
                      "headRefName": "fugaro/item-b", "baseRefName": "main",
                      "url": "https://example.invalid/411",
                      "files": [{"path": ITEM_B}, {"path": INDEX}],
                      "commits": [{"oid": B_COMMIT}]})

w("commit-%s.json" % A_COMMIT, {"files": [{"filename": ITEM_A}, {"filename": INDEX}]})
w("commit-%s.json" % B_COMMIT, {"files": [{"filename": ITEM_B}, {"filename": INDEX}]})
if variant == "item-split-commits":
    w("commit-%s.json" % A_COMMIT, {"files": [{"filename": ITEM_A}]})
    w("pr-410.json", {"state": "MERGED", "mergedAt": "2026-11-17T13:00:00Z",
                      "mergeCommit": {"oid": "1" * 40}, "headRefName": "fugaro/item-a",
                      "baseRefName": "main", "url": "https://example.invalid/410",
                      "files": [{"path": ITEM_A}, {"path": INDEX}],
                      "commits": [{"oid": A_COMMIT}, {"oid": "9" * 40}]})
    w("commit-%s.json" % ("9" * 40), {"files": [{"filename": INDEX}]})

for path, status in ((ITEM_A, "closed"), (ITEM_B, "open")):
    text = "---\nid: %s\nstatus: %s\ntitle: an item\n---\n\nbody\n" % (
        path.rsplit("/", 1)[1].removesuffix(".md"), status)
    w("contents-%s.json" % path.replace("/", "__"),
      {"path": path, "encoding": "base64",
       "content": base64.b64encode(text.encode()).decode()})
if variant == "item-not-flipped":
    text = "---\nid: 2026-11-17-1-first\nstatus: open\ntitle: an item\n---\n\nbody\n"
    w("contents-%s.json" % ITEM_A.replace("/", "__"),
      {"path": ITEM_A, "encoding": "base64",
       "content": base64.b64encode(text.encode()).decode()})

INDEX_TEXT = "# the backlog\n\n- [2026-11-17-1](2026-11-17-1-first.md) closed\n- [2026-11-17-2](2026-11-17-2-second.md) open\n"
w("index.md", INDEX_TEXT, raw=True)
for checkout in ("scratch", "second"):
    folder = os.path.join(root, checkout, "docs", "backlog")
    os.makedirs(folder, exist_ok=True)
    open(os.path.join(folder, "2026-11-17-1-first.md"), "w").write("---\nid: 2026-11-17-1\nstatus: closed\n---\n")
    open(os.path.join(folder, "2026-11-17-2-second.md"), "w").write("---\nid: 2026-11-17-2\nstatus: open\n---\n")
    body = INDEX_TEXT
    if variant == "item-stale-index" and checkout == "scratch":
        body = "# the backlog\n\n- [2026-11-17-1](2026-11-17-1-first.md) open\n"
    open(os.path.join(folder, "README.md"), "w").write(body)
PY
}

# ------------------------------------------------------------------- the runs
# The check refuses a coding agent's session by design, and this test itself
# runs in one: every case clears the markers, and the one case about the refusal
# sets one back on its own.
MARKERS=(CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT)
unmark=(env)
for m in "${MARKERS[@]}"; do unmark+=(-u "$m"); done

logs_clear() { # dir
  mkdir -p "$1/logs"
  : >"$1/logs/fugaro.calls"
  : >"$1/logs/git.calls"
  : >"$1/logs/gh.calls"
}

run127() { # dir args...
  local dir=$1
  shift
  logs_clear "$dir"
  status=0
  "${unmark[@]}" PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_DOCS="$dir/docs" \
    CHECK127_TIMEOUT=30 \
    bash "$script" "$@" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
}

# full_args DIR — the arguments of a run that has every step's evidence, so a
# case moves one fixture and nothing else.
full_args() {
  local dir=$1
  printf '%s\n' \
    --sandbox "$dir/scratch" \
    --run 'scratch-ab12cd34/20261117-120000-0001' \
    --critical 'scratch-ab12cd34/20261117-130000-0002' \
    --hotfix 'scratch-ab12cd34/20261117-140000-0003' \
    --path 'deploy/terraform/check.tf' \
    --pr 123 \
    --checkout2 "$dir/second" \
    --run2 'second-cd34ef56/20261117-170000-0006' \
    --ref-run 'scratch-ab12cd34/20261117-150000-0004' \
    --ref 'fugaro/feature-rigor' \
    --areas-run 'scratch-ab12cd34/20261117-160000-0005' \
    --item-pr 410 \
    --item-pr2 411
}

# prepare DIR [VARIANT] — the directory a case runs in: the fakes, the two
# checkouts and every fixture.
prepare() { # prepare DIR [VARIANT]
  local dir=$1
  mkdir -p "$dir/home" "$dir/tmp" "$dir/scratch" "$dir/second" "$dir/logs"
  write_fake_fugaro "$dir"
  write_fake_git "$dir"
  write_fake_gh "$dir"
  fixtures "$dir" "${2:-ok}"
}

# ------------------------------------------------------------------ the tests
test_help_and_argument_guards() {
  local dir status=0
  dir=$work/args
  prepare "$dir"
  run127 "$dir" --help
  expect_status "$status" 0 "help: exits 0"
  expect_contains "$out" "bash scripts/live-checks/check127.sh --sandbox <checkout>" "help: the plan's flags are listed"
  expect_contains "$out" "--item-pr2" "help: the second branch's flag is listed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "help: no tool is called"

  run127 "$dir" --sandbox "$dir/scratch"
  expect_status "$status" 2 "no --run: refused"
  expect_contains "$err" "--run is required" "no --run: the refusal names the flag"

  run127 "$dir" --run x/y
  expect_status "$status" 2 "no --sandbox: refused"
  expect_contains "$err" "--sandbox is required" "no --sandbox: the refusal names the flag"

  run127 "$dir" --sandbox "$dir/scratch" --run x/y --nonsense
  expect_status "$status" 2 "unknown flag: refused"
  expect_contains "$err" "unknown argument '--nonsense'" "unknown flag: the refusal names it"
}

test_dry_run_prints_the_plan_and_reaches_nothing() {
  local dir
  dir=$work/dry
  prepare "$dir"
  run127 "$dir" --sandbox "$dir/scratch" --run 'scratch-ab12cd34/20261117-120000-0001' --dry-run
  expect_status "$status" 0 "dry run: exits 0"
  expect_contains "$out" "CHECK 127 RESULT" "dry run: the block is printed"
  expect_contains "$out" "dry-run: INFO" "dry run: the row says nothing was run"
  expect_contains "$out" "restore: PASS" "dry run: nothing was created"
  expect_contains "$err" "the plan, step by step" "dry run: the plan is printed"
  expect_contains "$err" "fugaro watch --once --json" "dry run: step 9's command is in the plan"
  [ ! -s "$dir/logs/fugaro.calls" ] || fail "dry run: fugaro was called"
  [ ! -s "$dir/logs/gh.calls" ] || fail "dry run: gh was called"
  # The plan names the checkout's repository, which is one `git remote get-url`:
  # nothing else of git is asked and nothing is read out of the checkout.
  expect_contains "$(cat "$dir/logs/git.calls")" "remote get-url origin" "dry run: the plan names the repository"
  expect_absent "$(cat "$dir/logs/git.calls")" "show" "dry run: no file is read out of the checkout"
  expect_absent "$(cat "$dir/out")" "FACT poc-run: PASS" "dry run: no step reads anything"
}

# The owner's own installation and repository are refused before any tool is
# called, and the refusal is the same for a dry run as for a real one.
test_never_belong() {
  local dir
  dir=$work/belong
  prepare "$dir"
  mkdir -p "$dir/belong-scratch"
  run127 "$dir" --sandbox "$dir/belong-scratch" --run 'scratch-x/y' --dry-run
  expect_status "$status" 2 "belong: refused"
  expect_contains "$out" "FACT belong: FAIL" "belong: the row is FAIL"
  expect_contains "$err" "matches 'belong'" "belong: the stop names the word"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "belong: nothing was read"
}

test_refuses_an_agent_session() {
  local dir
  dir=$work/agent
  prepare "$dir"
  logs_clear "$dir"
  status=0
  env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" CLAUDECODE=1 \
    bash "$script" $(full_args "$dir") >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  expect_status "$status" 2 "agent session: refused"
  expect_contains "$out" "FACT agent-session: FAIL" "agent session: the row is FAIL"
  expect_contains "$err" "no agent runs any step of Check 127" "agent session: the stop says why"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "agent session: nothing was read"
}

test_refuses_a_traced_shell() {
  local dir
  dir=$work/xtrace
  prepare "$dir"
  logs_clear "$dir"
  status=0
  "${unmark[@]}" PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_DOCS="$dir/docs" \
    bash -x "$script" $(full_args "$dir") >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  expect_status "$status" 2 "xtrace: refused"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row is FAIL"
  expect_contains "$(cat "$dir/err")" "this shell is tracing" "xtrace: the stop says why"
}

test_happy_path() {
  local dir
  dir=$work/happy
  prepare "$dir"
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 0 "happy: exits 0"
  expect_contains "$out" "CHECK 127 RESULT END (exit 0)" "happy: the block ends clean"
  expect_contains "$out" "FACT installation: PASS" "happy: the installation row"
  expect_contains "$out" "belongs to the sandbox installation (sandbox-proj, us-east1)" "happy: the project is the checkout's"
  expect_contains "$out" "FACT sandbox: PASS" "happy: the repository is the checkout's"
  expect_contains "$out" "FACT poc-run: PASS" "happy: step 1"
  expect_contains "$out" "resolved mode poc, decided by the 'request' layer" "happy: step 1's layer"
  expect_contains "$out" "FACT diagnose: PASS" "happy: step 1's diagnose"
  expect_contains "$out" "FACT critical-run: PASS" "happy: step 2"
  expect_contains "$out" "FACT mode-diff: PASS" "happy: step 2's diff"
  expect_contains "$out" "POC tolerated 1 finding(s)" "happy: the diff's two figures"
  expect_contains "$out" "FACT hotfix-run: PASS" "happy: step 3"
  expect_contains "$out" "FACT floor: PASS" "happy: step 3's floor"
  expect_contains "$out" "row 'terraform'" "happy: the row that decided"
  expect_contains "$out" "FACT preview: PASS" "happy: step 4"
  expect_contains "$out" '$25.000000' "happy: the ceiling is printed"
  expect_contains "$out" "FACT post-merge: PASS" "happy: step 5"
  expect_contains "$out" "FACT second-repo: PASS" "happy: step 6"
  expect_contains "$out" "FACT project-default: PASS" "happy: step 6's key"
  expect_contains "$out" "sets rigor.default 'poc' and sandbox-org/second 'pro'" "happy: the two values"
  expect_contains "$out" "FACT ref-revision: PASS" "happy: step 7"
  expect_contains "$out" "from the 'repo' layer at revision 'origin/main'" "happy: the revision"
  expect_contains "$out" "FACT areas: PASS" "happy: step 8"
  expect_contains "$out" "matched 2 area rules" "happy: both rules"
  expect_contains "$out" "applied at the 'round' moment" "happy: step 8's second half"
  expect_contains "$out" "FACT watch: PASS" "happy: step 9"
  expect_contains "$out" "FACT item-pr: PASS" "happy: step 10"
  expect_contains "$out" "one commit carries both" "happy: D31's claim"
  expect_contains "$out" "FACT item-branches: PASS" "happy: step 11"
  expect_contains "$out" "FACT item-branches-index: PASS" "happy: step 11's index"
  expect_contains "$out" "FACT restore: PASS" "happy: nothing was created"
  expect_absent "$out" "FAIL" "happy: no row failed"
  expect_absent "$out" "not reached" "happy: every row was reached"
  # Nothing wrote into the checkouts, and the backlog index was regenerated in
  # the scratch copy alone.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "backlog index --dir" "happy: the index is regenerated in a copy"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "backlog index --dir $dir/scratch/docs" "happy: never in the checkout itself"
  [ -z "$(find "$dir/scratch" "$dir/second" -newer "$dir/docs/index.md" -type f 2>/dev/null)" ] ||
    fail "happy: a fixture in the checkout was written"
}

# The modes' own scale: the run that asked for POC has to say so, and the
# stricter run may not tolerate more than the lenient one over the same task.
test_a_run_that_is_not_poc_is_a_failure() {
  local dir
  dir=$work/notpoc
  prepare "$dir" poc-not-poc
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "not poc: the verdict"
  expect_contains "$out" "FACT poc-run: FAIL" "not poc: the row"
  expect_contains "$out" "the default is Critical (D19)" "not poc: the reading"
}

test_a_stricter_run_that_tolerates_more_is_a_failure() {
  local dir
  dir=$work/tolerates
  prepare "$dir" critical-tolerates-more
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "tolerates more: the verdict"
  expect_contains "$out" "FACT mode-diff: FAIL" "tolerates more: the row"
  expect_contains "$out" "must tolerate no more than a lower one" "tolerates more: the reading"
}

test_a_run_the_listing_does_not_hold_could_not_be_read() {
  local dir
  dir=$work/norow
  prepare "$dir" no-poc-row
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 2 "no row: the run could not tell"
  expect_contains "$out" "FACT poc-run: FAIL" "no row: the row is not a pass"
  expect_contains "$out" "does not hold the run" "no row: the reading"
  expect_contains "$err" "rows that could not be read: poc-run" "no row: the unmeasured keys are named"
}

# Step 3's floor is the one row that is written to FAIL rather than trusted: a
# path no row of the list in force matches is not a floor, and the reading says
# which in-tree work that would need.
test_a_path_the_list_does_not_hold_is_not_a_floor() {
  local dir
  dir=$work/notfloor
  prepare "$dir" path-not-sensitive
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "not a floor: the verdict"
  expect_contains "$out" "FACT floor: FAIL" "not a floor: the row"
  expect_contains "$out" "does NOT treat deploy/terraform/check.tf as security-sensitive" "not a floor: the reading"
  expect_contains "$out" "T1.5's built-in set" "not a floor: the work it waits on is named"
}

test_a_hotfix_that_tolerated_a_finding_on_the_path_is_a_failure() {
  local dir
  dir=$work/floortol
  prepare "$dir" floor-tolerated
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "floor tolerated: the verdict"
  expect_contains "$out" "FACT floor: FAIL" "floor tolerated: the row"
  expect_contains "$out" "tolerated 2 finding(s) anyway" "floor tolerated: the figure"
}

test_a_preview_below_the_finished_run_is_a_failure() {
  local dir
  dir=$work/below
  prepare "$dir" preview-below
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "below: the verdict"
  expect_contains "$out" "FACT preview: FAIL" "below: the row"
  expect_contains "$out" "is below what a real run of it cost" "below: the reading"
}

test_a_mode_that_prices_fewer_rounds_than_the_repository_is_a_failure() {
  local dir
  dir=$work/knobs
  prepare "$dir" preview-lower-knobs
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "knobs: the verdict"
  expect_contains "$out" "FACT preview: FAIL" "knobs: the row"
  expect_contains "$out" "may only raise what a run spends" "knobs: the reading"
}

test_two_repositories_that_do_not_differ_are_a_failure() {
  local dir
  dir=$work/same
  prepare "$dir" same-default
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "same default: the verdict"
  expect_contains "$out" "FACT project-default: FAIL" "same default: the row"
  expect_contains "$out" "both repositories set rigor.default to 'poc'" "same default: the reading"
}

test_two_checkouts_in_two_projects_are_a_failure() {
  local dir
  dir=$work/twoproj
  prepare "$dir" second-project
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "two projects: the verdict"
  expect_contains "$out" "FACT second-repo: FAIL" "two projects: the row"
  expect_contains "$out" "two installations are two projects" "two projects: the reading"
}

# D25: the mode is read from the BASE branch, so a record whose revision names
# the branch under review is the failure step 7 exists to catch.
test_a_mode_read_from_the_branch_under_review_is_a_failure() {
  local dir
  dir=$work/refown
  prepare "$dir" ref-own-branch
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "own branch: the verdict"
  expect_contains "$out" "FACT ref-revision: FAIL" "own branch: the row"
  expect_contains "$out" "D25 says the mode is read from the BASE branch" "own branch: the reading"
}

test_a_run_that_asked_for_its_own_mode_is_not_step_sevens_reading() {
  local dir
  dir=$work/refreq
  prepare "$dir" ref-request
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "request: the verdict"
  expect_contains "$out" "FACT ref-revision: FAIL" "request: the row"
  expect_contains "$out" "from the run's own request" "request: the reading"
}

test_a_run_matching_one_area_is_not_step_eight() {
  local dir
  dir=$work/onearea
  prepare "$dir" no-areas
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "one area: the verdict"
  expect_contains "$out" "FACT areas: FAIL" "one area: the row"
  expect_contains "$out" "matched 1 area rule(s)" "one area: the figure"
}

# F40's drop-and-warn path: a frame whose runs carry no mode reads as a run
# with no rigor at all, which is what the row has to catch.
test_a_watch_frame_with_no_mode_is_the_drop_and_warn_path() {
  local dir
  dir=$work/watchdrop
  prepare "$dir" watch-nomode
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "watch drop: the verdict"
  expect_contains "$out" "FACT watch: FAIL" "watch drop: the row"
  expect_contains "$out" "the drop-and-warn path F40 records" "watch drop: the reading"
  expect_contains "$out" "fugaro init --firebase-project" "watch drop: the fix is named"
}

test_a_watch_frame_with_no_live_run_could_not_be_read() {
  local dir
  dir=$work/watchempty
  prepare "$dir" watch-empty
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 2 "watch empty: could not tell"
  expect_contains "$out" "FACT watch: FAIL" "watch empty: the row is not a pass"
  expect_contains "$out" "read no live run" "watch empty: the reading"
}

test_an_item_pull_request_without_the_index_is_a_failure() {
  local dir
  dir=$work/noindex
  prepare "$dir" item-no-index
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "no index: the verdict"
  expect_contains "$out" "FACT item-pr: FAIL" "no index: the row"
  expect_contains "$out" "does not touch docs/backlog/README.md" "no index: the reading"
}

test_a_flip_and_an_index_in_two_commits_is_a_failure() {
  local dir
  dir=$work/split
  prepare "$dir" item-split-commits
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "split commits: the verdict"
  expect_contains "$out" "FACT item-pr: FAIL" "split commits: the row"
  expect_contains "$out" "no single commit carries both" "split commits: the reading"
}

test_an_item_the_pull_request_left_open_is_a_failure() {
  local dir
  dir=$work/notflipped
  prepare "$dir" item-not-flipped
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "not flipped: the verdict"
  expect_contains "$out" "FACT item-pr: FAIL" "not flipped: the row"
  expect_contains "$out" "reads closed (or dropped) at its head" "not flipped: the reading"
}

test_two_branches_that_named_their_item_the_same_are_a_failure() {
  local dir
  dir=$work/samename
  prepare "$dir" item-same-name
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "same name: the verdict"
  expect_contains "$out" "FACT item-branches: FAIL" "same name: the row"
  expect_contains "$out" "share a file name" "same name: the reading"
}

test_an_index_that_is_not_current_is_a_failure() {
  local dir
  dir=$work/staleindex
  prepare "$dir" item-stale-index
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 1 "stale index: the verdict"
  expect_contains "$out" "FACT item-branches-index: FAIL" "stale index: the row"
  expect_contains "$out" "is NOT what the item files beside it say" "stale index: the reading"
}

test_a_second_branch_that_was_not_merged_stops_the_run() {
  local dir
  dir=$work/notmerged
  prepare "$dir" item-not-merged
  run127 "$dir" $(full_args "$dir")
  expect_status "$status" 2 "not merged: could not tell"
  expect_contains "$err" "is not merged" "not merged: the stop says why"
  expect_contains "$out" "aborted" "not merged: the block carries the abort"
}

# Several rows are unanswered together: each is INFO with the command that
# would produce it, the verdict is still a verdict about what WAS read, and the
# block names the steps that stayed the owner's.
test_the_steps_that_were_not_given_their_evidence_are_info() {
  local dir
  dir=$work/partial
  prepare "$dir"
  run127 "$dir" --sandbox "$dir/scratch" --run 'scratch-ab12cd34/20261117-120000-0001'
  expect_status "$status" 0 "partial: what was measured passed"
  expect_contains "$out" "FACT poc-run: PASS" "partial: step 1 was read"
  expect_contains "$out" "FACT critical-run: INFO" "partial: step 2 stayed yours"
  expect_contains "$out" "not measured: step 2, the same task at Critical" "partial: the command is named"
  expect_contains "$out" "FACT item-branches: INFO" "partial: step 11 stayed yours"
  expect_contains "$out" "FACT watch: PASS" "partial: step 9 needs no flag"
  expect_contains "$out" "FACT restore: PASS" "partial: nothing was created"
  expect_absent "$out" "FAIL" "partial: nothing failed"
}

# Every call the check makes is scoped to the sandbox's own repository and
# project, and the second repository's listing is read under its own name: a
# check that read another repository's rows would pass on the wrong evidence.
test_every_read_stays_in_the_sandbox() {
  local calls
  [ -f "$work/happy/logs/fugaro.calls" ] || test_happy_path
  calls=$(cat "$work/happy/logs/fugaro.calls")
  expect_contains "$calls" "runs ls --json --since 0 --repo sandbox-org/scratch" "reads: the sandbox's listing"
  expect_contains "$calls" "runs ls --json --since 0 --repo sandbox-org/second" "reads: the second repository's listing"
  expect_absent "$calls" "belong" "reads: no call names the owner's own repository"
  expect_absent "$(cat "$work/happy/logs/gh.calls")" "belong" "reads: no gh call names it either"
  expect_contains "$(cat "$work/happy/logs/gh.calls")" "pr view 123" "reads: step 5's pull request"
  expect_contains "$(cat "$work/happy/logs/gh.calls")" "repos/sandbox-org/scratch/contents/docs/backlog/2026-11-17-1-first.md" "reads: the item is read out of the sandbox's repository"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each test works in its own directory under
# $work), CHECK127_TEST_ONLY=name runs that one test alone (for a solo rerun of
# a flaky-looking one). Neither set: all run.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK127_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK127_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_help_and_argument_guards
t test_dry_run_prints_the_plan_and_reaches_nothing
t test_never_belong
t test_refuses_an_agent_session
t test_refuses_a_traced_shell
t test_happy_path
t test_a_run_that_is_not_poc_is_a_failure
t test_a_stricter_run_that_tolerates_more_is_a_failure
t test_a_run_the_listing_does_not_hold_could_not_be_read
t test_a_path_the_list_does_not_hold_is_not_a_floor
t test_a_hotfix_that_tolerated_a_finding_on_the_path_is_a_failure
t test_a_preview_below_the_finished_run_is_a_failure
t test_a_mode_that_prices_fewer_rounds_than_the_repository_is_a_failure
t test_two_repositories_that_do_not_differ_are_a_failure
t test_two_checkouts_in_two_projects_are_a_failure
t test_a_mode_read_from_the_branch_under_review_is_a_failure
t test_a_run_that_asked_for_its_own_mode_is_not_step_sevens_reading
t test_a_run_matching_one_area_is_not_step_eight
t test_a_watch_frame_with_no_mode_is_the_drop_and_warn_path
t test_a_watch_frame_with_no_live_run_could_not_be_read
t test_an_item_pull_request_without_the_index_is_a_failure
t test_a_flip_and_an_index_in_two_commits_is_a_failure
t test_an_item_the_pull_request_left_open_is_a_failure
t test_two_branches_that_named_their_item_the_same_are_a_failure
t test_an_index_that_is_not_current_is_a_failure
t test_a_second_branch_that_was_not_merged_stops_the_run
t test_the_steps_that_were_not_given_their_evidence_are_info
t test_every_read_stays_in_the_sandbox

if [ "$failures" -ne 0 ]; then
  echo "check127_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check127_test.sh: ok"
