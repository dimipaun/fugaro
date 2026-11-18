#!/usr/bin/env bash
# Tests for scripts/live-checks/check292.sh, with fake `fugaro`, `gcloud`,
# `docker` and `git` on PATH: NOTHING here reaches a cloud, a project, a bucket,
# a registry or a repository, no agent runs a live command, and the fakes record
# every call they are given — in which directory it was made — so a case can
# assert what the check did and did not do.
#
# The fakes are a small model of one installation: the fake `fugaro` writes the
# entry into a project config of its own and answers `doctor`, `image ls`,
# `image add`, `image rm`, `image build` and `project policy show` the way the
# real one does — a build stages the Dockerfile, the commit and a digest of the
# tree under the entry's context, writes the entry's record and prints the two
# sentences the real command prints; the fake `gcloud` serves the bucket, the
# builds and one run of the catalog check job, whose decision is computed from
# the staged objects and the record the way internal/imagecheck's is; the fake
# `docker` serves the image a build made — its revision label and the file its
# Dockerfile COPYed; the fake `git` is the checkout's branch, its staged paths
# and its commits.
#
# The cases that matter are the ones where that model is bent: a digest taken
# over the whole repository instead of the context, a decision that never fires
# or fires twice, a build that fails with the 0.25.0 flag, a record naming
# another commit, a staged hash that disagrees with the record, an image
# labelled with another commit, a build whose output names no image, an update
# that asks or that stages, a published node that drops the context, a check job
# that fails or whose line never lands, and a docker that refuses.
#
# Usage: bash scripts/live-checks/check292_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check292.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK292_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK292_TEST_HEAD:-1200}")" ;;
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

install=chk292-install
gcp_project=chk292-project
region=us-east5
bucket=chk292-runs
trace_entry=chk292-a1b2c3

# --------------------------------------------------------- the fakes' library
# One python module the four fakes share: the state they keep (a bucket of
# objects, the images a build made, the calls each was given), the project config
# they read and write, and the decision the fake catalog check job takes.
write_fakelib() { # dir
  cat >"$1/bin/fakelib.py" <<'FAKELIB_EOF'
import hashlib
import json
import os
import re
import sys
from datetime import datetime, timezone


def log_dir():
    return os.environ["FAKE_LOG_DIR"]


def state_dir():
    return os.environ["FAKE_STATE_DIR"]


def json_dir():
    return os.environ["FAKE_JSON_DIR"]


def log(name, argv):
    with open(os.path.join(log_dir(), name + ".calls"), "a", encoding="utf-8") as fh:
        fh.write(" ".join(str(a) for a in argv) + "\n")


def flag(argv, name, default=""):
    if name in argv:
        i = argv.index(name)
        if i + 1 < len(argv):
            return argv[i + 1]
    return default


def env(name, default=""):
    return os.environ.get(name, default) or default


def state_path(*parts):
    return os.path.join(state_dir(), *parts)


def read_state(name, default=""):
    try:
        with open(state_path(name), "r", encoding="utf-8") as fh:
            return fh.read().strip()
    except OSError:
        return default


def write_state(name, value):
    path = state_path(name)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(value)


def json_doc(name, default=None):
    path = os.path.join(json_dir(), name)
    if os.path.exists(path):
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    return default


def counter(name):
    n = int(read_state(name, "0") or "0") + 1
    write_state(name, str(n))
    return n


def git_head():
    return read_state("git/head", "0" * 39 + "1")


def digest_of(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        h.update(fh.read())
    return h.hexdigest()


def tree_digest(root, relative_paths):
    h = hashlib.sha256()
    for rel in sorted(relative_paths):
        h.update(rel.encode("utf-8") + b"\0")
        h.update(open(os.path.join(root, rel), "rb").read())
    return h.hexdigest()


def config_path():
    doc = json_doc("doctor.json") or {}
    name = ((doc.get("project") or {}).get("name")) or "project"
    home = os.environ.get("HOME", "")
    return os.path.join(home, ".config", "fugaro", "projects", name + ".yaml")


ENTRY_RE = re.compile(r"^(\s*)- name: (.*)$")


def read_entries(path):
    """The catalog the project config states: name, tags and the source members.

    A line reader rather than a yaml parse: the config here is written by the
    fake `image add` and edited by the check itself, and what both need is the
    members, not the document.
    """
    entries = []
    cur = None
    in_source = False
    try:
        with open(path, "r", encoding="utf-8") as fh:
            lines = fh.read().split("\n")
    except OSError:
        return entries
    for line in lines:
        stripped = line.strip()
        m = ENTRY_RE.match(line)
        if m:
            cur = {"name": m.group(2).strip().strip("\"'"), "tags": [], "source": {}}
            entries.append(cur)
            in_source = False
            continue
        if cur is None or not stripped or stripped.startswith("#"):
            continue
        if stripped == "source:":
            in_source = True
            continue
        if stripped.startswith("- ") and not in_source:
            cur["tags"].append(stripped[2:].strip().strip("\"'"))
            continue
        key, _, value = stripped.partition(":")
        key = key.strip()
        value = value.strip().strip("\"'")
        if key == "entries" or not value:
            if key != "name":
                in_source = False
            continue
        if key in ("repo", "path", "branch", "context", "kind", "image"):
            cur["source"][key] = value
        elif key == "tags" and value.startswith("[") and value.endswith("]"):
            cur["tags"] = [v.strip().strip("\"'") for v in value[1:-1].split(",") if v.strip()]
    return entries


def entry_of(path, name):
    for e in read_entries(path):
        if e["name"] == name:
            return e
    return None


def add_entry(path, name, repo, docker_path, branch, tags):
    block = ["    - name: %s" % name]
    if tags:
        block.append("      tags:")
        for tag in tags.split(","):
            block.append("        - %s" % tag.strip())
    block += ["      source:", "        repo: %s" % repo,
              "        path: %s" % docker_path, "        branch: %s" % branch]
    with open(path, "r", encoding="utf-8") as fh:
        lines = fh.read().split("\n")
    out = []
    inserted = False
    for line in lines:
        out.append(line)
        if not inserted and line.strip() == "entries:":
            out.extend(block)
            inserted = True
    if not inserted:
        return False
    with open(path, "w", encoding="utf-8") as fh:
        fh.write("\n".join(out))
    return True


def remove_entry(path, name):
    with open(path, "r", encoding="utf-8") as fh:
        lines = fh.read().split("\n")
    out = []
    dropping = False
    dropped = False
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("- name: "):
            dropping = stripped[len("- name: "):].strip().strip("\"'") == name
            if dropping:
                dropped = True
                while out and out[-1].strip() == "":
                    out.pop()
                continue
        elif dropping and stripped and not line.startswith((" ", "\t")):
            dropping = False
        if not dropping:
            out.append(line)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write("\n".join(out))
    return dropped


def ls_path():
    return state_path("ls.json")


def ls_doc():
    try:
        with open(ls_path(), "r", encoding="utf-8") as fh:
            return json.load(fh)
    except OSError:
        return {"entries": []}


def ls_add(name, tags, source, digest):
    doc = ls_doc()
    doc["entries"] = [e for e in doc.get("entries", []) if e.get("name") != name]
    doc["entries"].append({"name": name, "tags": [t for t in tags.split(",") if t],
                           "source": source, "digest": digest, "default": False})
    os.makedirs(state_dir(), exist_ok=True)
    with open(ls_path(), "w", encoding="utf-8") as fh:
        json.dump(doc, fh)


def ls_remove(name):
    doc = ls_doc()
    doc["entries"] = [e for e in doc.get("entries", []) if e.get("name") != name]
    with open(ls_path(), "w", encoding="utf-8") as fh:
        json.dump(doc, fh)


SALT = "saltsalt"


def bucket_root():
    return state_path("bucket")


def object_path(uri):
    rel = uri[len("gs://"):] if uri.startswith("gs://") else uri
    rel = rel.split("/", 1)[1] if "/" in rel else rel
    return os.path.join(bucket_root(), rel)


def glob_objects(pattern):
    """The objects a `gcloud storage ls <pattern>` matches, as real URIs."""
    import glob
    root = bucket_root()
    rel = pattern[len("gs://"):] if pattern.startswith("gs://") else pattern
    rel = rel.split("/", 1)[1] if "/" in rel else rel
    if rel.endswith("/"):
        found = sorted(p for p in glob.glob(os.path.join(root, rel, "**"), recursive=True)
                       if os.path.isfile(p))
    else:
        found = sorted(p for p in glob.glob(os.path.join(root, rel)) if os.path.isfile(p))
    return ["gs://" + pattern[len("gs://"):].split("/", 1)[0] + "/" + os.path.relpath(p, root)
            for p in found]


def entry_prefix(entry):
    return state_path("bucket", "builds", "catalog", entry)


def record_path(slug, entry):
    return state_path("bucket", "builds", slug, "catalog", entry, "image.json")


def record_glob(entry):
    import glob
    return sorted(glob.glob(os.path.join(bucket_root(), "builds", "*", "catalog", entry, "image.json")))


def read_record(entry):
    found = record_glob(entry)
    if not found:
        return None, ""
    with open(found[0], "r", encoding="utf-8") as fh:
        return json.load(fh), found[0]


def write_record(slug, entry, rec):
    path = record_path(slug, entry)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(rec, fh, indent=1)


def stamp():
    forced = env("FAKE_LOG_STAMP")
    if forced:
        return forced
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def log_line(entry, decision, reasons, build_id, message):
    row = {"timestamp": stamp(),
           "jsonPayload": {"event": "catalog-check", "entry": entry, "decision": decision,
                           "reasons": reasons, "build_id": build_id, "message": message}}
    path = state_path("logs.jsonl")
    os.makedirs(state_dir(), exist_ok=True)
    with open(path, "a", encoding="utf-8") as fh:
        fh.write(json.dumps(row) + "\n")


def log_rows():
    try:
        with open(state_path("logs.jsonl"), "r", encoding="utf-8") as fh:
            rows = [json.loads(line) for line in fh if line.strip()]
    except OSError:
        return []
    return list(reversed(rows))


def image_key(ref):
    return ref.split("@", 1)[1] if "@" in ref else ref


def image_dir(ref):
    return state_path("images", image_key(ref))


def store_image(ref, label, copied, dest):
    """The image a build made: its revision label and the file its COPY reached."""
    key = image_key(ref)
    os.makedirs(state_path("images", key, "files"), exist_ok=True)
    write_state(os.path.join("images", key, "label"), label)
    if dest:
        target = os.path.join(state_path("images", key, "files"), dest.lstrip("/"))
        os.makedirs(os.path.dirname(target), exist_ok=True)
        with open(target, "wb") as fh:
            fh.write(copied)


def copy_lines(dockerfile):
    """The COPY pairs of the staged Dockerfile, as (source, destination)."""
    pairs = []
    try:
        with open(dockerfile, "r", encoding="utf-8") as fh:
            text = fh.read()
    except OSError:
        return pairs
    for line in text.split("\n"):
        parts = line.split()
        if len(parts) >= 3 and parts[0].upper() == "COPY":
            pairs.append((parts[1], parts[2]))
    return pairs


def context_files(root, context):
    """Every file under the entry's context, as paths relative to the checkout."""
    base = os.path.join(root, context) if context else root
    out = []
    for dirpath, _dirnames, filenames in os.walk(base):
        if ".git" in dirpath.split(os.sep):
            continue
        for name in filenames:
            out.append(os.path.relpath(os.path.join(dirpath, name), root))
    return out


def staged_digest(root, context):
    if env("FAKE_DIGEST_WHOLE_REPO"):
        return tree_digest(root, context_files(root, ""))
    if env("FAKE_DIGEST_CONSTANT"):
        return "f" * 64
    files = context_files(root, context)
    return tree_digest(root, files) if files else ""


def clip(value, limit=80):
    text = str(value)
    return text[:limit]
FAKELIB_EOF
}

write_fakelib_more() { # dir
  cat >>"$1/bin/fakelib.py" <<'FAKELIB2_EOF'


def doctor():
    return json_doc("doctor.json") or {}


def gcp_project():
    return env("FAKE_GCP_PROJECT") or ((doctor().get("project") or {}).get("gcp_project") or "project")


def region():
    return env("FAKE_REGION") or ((doctor().get("project") or {}).get("region") or "us-east5")


def image_ref(entry, digest):
    return "%s-docker.pkg.dev/%s/fugaro/%s@%s" % (region(), gcp_project(), entry, digest)


def record_for(entry, commit, context_digest, definition, image, digest, build_id):
    return {"version": 1, "entry": entry, "source_commit": commit, "context_commit": commit,
            "context_sha256": context_digest, "definition_generation": 1,
            "definition_sha256": definition, "template_salt": SALT,
            "base_ref": "base:1", "base_digest": "sha256:" + "b" * 64,
            "image": image, "content_tag": "context-check", "image_digest": digest,
            "build_id": build_id, "built_at": stamp()}


def default_slug():
    origin = env("FAKE_GIT_ORIGIN", "git@github.com:chk292-sandbox/sandbox-repo.git")
    tail = origin.rstrip("/")
    tail = tail[:-4] if tail.endswith(".git") else tail
    tail = tail.split(":")[-1].split("/")[-2:]
    return "-".join(tail)


def make_build(entry, root, dockerfile_rel, context, build_id, waited):
    """One build, as the fake `fugaro image build` and the fake check job make it.

    The staging, the record and the image are the model of what the real command
    writes: the Dockerfile, the commit and a digest of the tree under the entry's
    context are staged before anything is paid for, and the record is written by
    the build's own last step — so a build cancelled before that step leaves the
    staged objects ahead of the record, which is the state the check reads.
    """
    definition = digest_of(os.path.join(root, dockerfile_rel))
    context_digest = staged_digest(root, context)
    commit = env("FAKE_RECORD_COMMIT") or git_head()
    slug = env("FAKE_SLUG") or default_slug()
    image = "%s-docker.pkg.dev/%s/fugaro/%s:latest" % (region(), gcp_project(), entry)
    digest = "sha256:" + hashlib.sha256((entry + context_digest + build_id).encode()).hexdigest()
    prefix = entry_prefix(entry)
    os.makedirs(prefix, exist_ok=True)
    with open(os.path.join(prefix, "Dockerfile"), "wb") as fh:
        fh.write(open(os.path.join(root, dockerfile_rel), "rb").read())
    write_state(os.path.join("bucket", "builds", "catalog", entry, "source-commit"), commit)
    staged_hash = env("FAKE_STAGED_HASH") or context_digest
    write_state(os.path.join("bucket", "builds", "catalog", entry, "context-sha256"), staged_hash)
    label = commit
    if env("FAKE_LABEL_OTHER"):
        label = env("FAKE_LABEL_OTHER")
    copied, dest = b"", ""
    for src, dst in copy_lines(os.path.join(root, dockerfile_rel)):
        source = os.path.join(root, context, src) if context else os.path.join(root, src)
        if os.path.exists(source):
            with open(source, "rb") as fh:
                copied = fh.read()
            dest = dst.lstrip("/")
            break
    store_image(image_ref(entry, digest), label, copied, dest)
    print("fugaro: Cloud Build build %s of %s submitted; log: https://example.invalid/%s"
          % (build_id, image, build_id))
    if not waited:
        print("submitted Cloud Build build %s of %s; log: https://example.invalid/%s"
              % (build_id, image, build_id))
        return None
    if env("FAKE_BUILD_NO_DIGEST"):
        return None
    # A finished step is printed as the glyph and the result on one line
    # (internal/report's Step.Done), not as a "fugaro: ..." sentence.
    print("\u2713 built %s@%s (Cloud Build build %s)" % (image[:-len(":latest")], digest, build_id))
    if env("FAKE_NO_RECORD"):
        return None
    rec = record_for(entry, commit, context_digest, definition, image, digest, build_id)
    if env("FAKE_RECORD_NO_DIGEST"):
        rec.pop("context_sha256")
    if env("FAKE_RECORD_SAME_DIGEST"):
        rec["context_sha256"] = definition
    if env("FAKE_RECORD_NO_SALT"):
        rec.pop("template_salt")
    write_record(slug, entry, rec)
    return rec


def finish_staged_build(entry):
    """What a build does when it reaches its own last step: write the record.

    Called when a submitted build is NOT cancelled in time (the register's step
    5: the race is lost, the record moved, and the next decision is quiet).
    """
    prefix = entry_prefix(entry)
    commit = read_state(os.path.join("bucket", "builds", "catalog", entry, "source-commit"))
    context_digest = read_state(os.path.join("bucket", "builds", "catalog", entry, "context-sha256"))
    try:
        definition = digest_of(os.path.join(prefix, "Dockerfile"))
    except OSError:
        definition = ""
    n = counter("builds/n")
    build_id = "fake-build-%d" % n
    image = "%s-docker.pkg.dev/%s/fugaro/%s:latest" % (region(), gcp_project(), entry)
    digest = "sha256:" + hashlib.sha256((entry + context_digest + build_id).encode()).hexdigest()
    store_image(image_ref(entry, digest), commit, b"", "")
    write_state(os.path.join("builds", build_id), "SUCCESS")
    write_record(env("FAKE_SLUG") or default_slug(), entry,
                 record_for(entry, commit, context_digest, definition, image, digest, build_id))
    return build_id


def staged_entries():
    import glob
    found = glob.glob(os.path.join(bucket_root(), "builds", "catalog", "*"))
    return sorted(os.path.basename(p) for p in found if os.path.isdir(p))


def catalog_check_job(argv):
    """One run of the catalog check job: decide from the staged objects and the
    record (which is what the real job reads) and print one line per entry."""
    for entry in staged_entries():
        prefix = entry_prefix(entry)
        staged_context = read_state(os.path.join("bucket", "builds", "catalog", entry, "context-sha256"))
        try:
            staged_definition = digest_of(os.path.join(prefix, "Dockerfile"))
        except OSError:
            staged_definition = ""
        rec, rec_path = read_record(entry)
        reasons = []
        if env("FAKE_DECIDE_ALWAYS"):
            reasons = ["catalog-context"]
        elif env("FAKE_DECIDE_SKIP"):
            reasons = []
        elif rec is None:
            reasons = ["catalog-missing"]
        else:
            if rec.get("context_sha256", "") != staged_context:
                reasons.append("catalog-context")
            if rec.get("definition_sha256", "") != staged_definition:
                reasons.append("catalog-definition")
            if rec.get("template_salt", "") != SALT:
                reasons.append("catalog-template")
        decision = "rebuild" if reasons else "skip"
        build_id = ""
        message = "entry %s: %s (%s)" % (entry, decision, ",".join(reasons) or "no reason")
        if decision == "rebuild" and env("FAKE_JOB_NO_BUILD_ID"):
            decision, build_id = "rebuild", ""
            message = "entry %s: rebuild (%s)" % (entry, ",".join(reasons))
        elif decision == "rebuild":
            status = env("FAKE_REBUILD_STATUS", "SUCCESS")
            commit = read_state(os.path.join("bucket", "builds", "catalog", entry, "source-commit"))
            slug = os.path.basename(os.path.dirname(os.path.dirname(os.path.dirname(rec_path)))) if rec_path else default_slug()
            build_id = env("FAKE_REBUILD_ID") or ("fake-build-%d" % counter("builds/n"))
            write_state(os.path.join("builds", build_id), status)
            message = "entry %s: rebuild (%s), build %s" % (entry, ",".join(reasons), build_id)
            if status == "SUCCESS":
                image = "%s-docker.pkg.dev/%s/fugaro/%s:latest" % (region(), gcp_project(), entry)
                digest = "sha256:" + hashlib.sha256((entry + staged_context + build_id).encode()).hexdigest()
                label = env("FAKE_REBUILD_LABEL_OTHER") or commit
                copied, dest = b"", ""
                stated = entry_of(config_path(), entry) or {}
                context = (stated.get("source") or {}).get("context", "")
                for src, dst in copy_lines(os.path.join(prefix, "Dockerfile")):
                    source = os.path.join(env("CHECK292_SANDBOX", "."), context, src)
                    if os.path.exists(source):
                        with open(source, "rb") as fh:
                            copied = fh.read()
                        dest = dst.lstrip("/")
                        break
                store_image(image_ref(entry, digest), label, copied, dest)
                write_record(slug, entry, record_for(entry, commit, staged_context, staged_definition,
                                                     image, digest, build_id))
        log_line(entry, decision, reasons, build_id, message)
        print(message)
    return 0
FAKELIB2_EOF
}

write_fake_git() { # dir
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env python3
# A stand-in for `git`, for scripts/live-checks/check292_test.sh only: the
# checkout's branch, the paths staged into it, and the commits this check makes.
# Nothing here reaches a repository; what it keeps is written under
# $FAKE_STATE_DIR so a case can read when it was called and what it was asked.
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fakelib as F

argv = sys.argv[1:]
F.log("git", argv)
i = 0
while i < len(argv) and argv[i] in ("-C", "-c"):
    i += 2
sub = argv[i] if i < len(argv) else ""
rest = argv[i + 1:]

if sub in ("", "--version"):
    print("git version 2.45.0 (fake)")
    sys.exit(0)
if sub == "status":
    if F.env("FAKE_GIT_DIRTY"):
        print(" M fugaro.yaml")
        print("?? notes.txt")
    sys.exit(0)
if sub == "remote":
    if not F.env("FAKE_GIT_NO_ORIGIN"):
        print(F.env("FAKE_GIT_ORIGIN", "git@github.com:chk292-sandbox/sandbox-repo.git"))
    sys.exit(0)
if sub == "symbolic-ref":
    if F.env("FAKE_GIT_DETACHED"):
        sys.exit(1)
    print(F.read_state("git/branch", "main"))
    sys.exit(0)
if sub == "checkout":
    branch = rest[-1] if rest else ""
    if "-b" in rest and F.env("FAKE_GIT_BRANCH_EXISTS"):
        sys.stderr.write("fatal: a branch named '%s' already exists\n" % branch)
        sys.exit(128)
    if "-b" not in rest and F.env("FAKE_GIT_CHECKOUT_FAIL"):
        sys.stderr.write("error: pathspec '%s' did not match any file(s) known to git\n" % branch)
        sys.exit(1)
    F.write_state("git/branch", branch)
    sys.exit(0)
if sub == "add":
    F.write_state("git/staged", " ".join(rest))
    sys.exit(0)
if sub == "diff":
    sys.exit(1 if F.read_state("git/staged") else 0)
if sub == "commit":
    if F.env("FAKE_GIT_COMMIT_FAIL"):
        sys.stderr.write("fatal: unable to write new index file\n")
        sys.exit(1)
    n = F.counter("git/n")
    sha = "%040x" % n
    F.write_state("git/head", sha)
    F.write_state("git/staged", "")
    with open(F.state_path("git", "commits.log"), "a", encoding="utf-8") as fh:
        fh.write("%s %s\n" % (sha, " ".join(a for a in rest if a != "-q")))
    sys.exit(0)
if sub == "push":
    if F.env("FAKE_GIT_PUSH_FAIL"):
        sys.stderr.write("! [remote rejected] (pre-receive hook declined)\n")
        sys.exit(1)
    with open(F.state_path("git", "pushed.log"), "a", encoding="utf-8") as fh:
        fh.write(" ".join(rest) + "\n")
    sys.exit(0)
if sub == "rev-parse":
    print(F.git_head())
    sys.exit(0)
sys.stderr.write("fake git: no stand-in for '%s'\n" % " ".join(argv))
sys.exit(2)
GIT_EOF
  chmod +x "$1/bin/git"
}

write_fake_docker() { # dir
  cat >"$1/bin/docker" <<'DOCKER_EOF'
#!/usr/bin/env python3
# A stand-in for `docker`, for scripts/live-checks/check292_test.sh only: the
# image a build made — its `org.opencontainers.image.revision` label and the file
# its Dockerfile COPYed — read out of $FAKE_STATE_DIR. Nothing here reaches a
# registry or a daemon.
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fakelib as F

argv = sys.argv[1:]
F.log("docker", argv)
if not argv:
    sys.exit(2)
sub = argv[0]
ref = argv[-1] if sub == "inspect" else (argv[-2] if len(argv) >= 2 and sub == "run" else "")

if sub == "inspect":
    if F.env("FAKE_DOCKER_REFUSED"):
        sys.stderr.write("Error response from daemon: unauthorized: authentication required\n")
        sys.exit(1)
    if not os.path.isdir(F.image_dir(ref)):
        sys.stderr.write("Error: No such object: %s\n" % ref)
        sys.exit(1)
    label = F.read_state(os.path.join("images", F.image_key(ref), "label"))
    if F.env("FAKE_LABEL_EMPTY"):
        label = ""
    print(label)
    sys.exit(0)
if sub == "pull":
    if F.env("FAKE_PULL_REFUSED"):
        sys.stderr.write("Error response from daemon: unauthorized: authentication required\n")
        sys.exit(1)
    os.makedirs(F.image_dir(argv[-1]), exist_ok=True)
    print("Pulled %s" % argv[-1])
    sys.exit(0)
if sub == "run":
    if F.env("FAKE_DOCKER_REFUSED"):
        sys.stderr.write("Error response from daemon: unauthorized: authentication required\n")
        sys.exit(1)
    if F.env("FAKE_DOCKER_NO_FILE"):
        sys.stderr.write("cat: %s: No such file or directory\n" % argv[-1])
        sys.exit(1)
    path = os.path.join(F.image_dir(ref), "files", argv[-1].lstrip("/"))
    if not os.path.exists(path):
        sys.stderr.write("cat: %s: No such file or directory\n" % argv[-1])
        sys.exit(1)
    with open(path, "rb") as fh:
        sys.stdout.write(fh.read().decode("utf-8"))
    sys.exit(0)
sys.stderr.write("fake docker: no stand-in for '%s'\n" % " ".join(argv))
sys.exit(2)
DOCKER_EOF
  chmod +x "$1/bin/docker"
}

write_fake_gcloud() { # dir
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
# A stand-in for `gcloud`, for scripts/live-checks/check292_test.sh only: the
# runs bucket (a directory tree under $FAKE_STATE_DIR), the builds a build id
# names, one run of the catalog check job, and Cloud Logging as the job's own
# stdout. Nothing here reaches a project.
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fakelib as F

argv = sys.argv[1:]
F.log("gcloud", argv)
groups = [a for a in argv if not a.startswith("-")]
if not groups:
    sys.exit(2)

if groups[:2] == ["storage", "ls"]:
    pattern = argv[argv.index("ls") + 1]
    found = F.glob_objects(pattern)
    if not found:
        sys.stderr.write("ERROR: (gcloud.storage.ls) matched no objects\n")
        sys.exit(1)
    for uri in found:
        print(uri)
    sys.exit(0)
if groups[:2] == ["storage", "rm"]:
    pattern = argv[argv.index("rm") + 1]
    if F.env("FAKE_RM_REFUSED"):
        sys.stderr.write("ERROR: (gcloud.storage.rm) owner@example.invalid does not have "
                         "storage.objects.delete access to %s\n" % pattern)
        sys.exit(1)
    found = F.glob_objects(pattern)
    if not found:
        sys.stderr.write("ERROR: (gcloud.storage.rm) The following URLs matched no objects:\n  %s\n"
                         % pattern)
        sys.exit(1)
    for uri in found:
        os.remove(F.object_path(uri))
        print("Removing %s..." % uri)
    sys.exit(0)
if groups[:2] == ["storage", "cat"]:
    uri = argv[argv.index("cat") + 1]
    path = F.object_path(uri)
    if not os.path.exists(path):
        sys.stderr.write("ERROR: (gcloud.storage.cat) No URLs matched: %s\n" % uri)
        sys.exit(1)
    with open(path, "rb") as fh:
        sys.stdout.write(fh.read().decode("utf-8"))
    sys.exit(0)
if groups[:2] == ["builds", "describe"]:
    build_id = argv[argv.index("describe") + 1]
    doc = F.json_doc("describe.json")
    if doc is not None:
        import json
        print(json.dumps(doc))
        sys.exit(0)
    import json
    print(json.dumps({"id": build_id, "status": F.read_state(os.path.join("builds", build_id), "SUCCESS")}))
    sys.exit(0)
if groups[:2] == ["builds", "cancel"]:
    build_id = argv[argv.index("cancel") + 1]
    if F.env("FAKE_CANCEL_FAIL"):
        sys.stderr.write("ERROR: (gcloud.builds.cancel) build %s is already in a terminal state\n" % build_id)
        sys.exit(1)
    if F.env("FAKE_RACE_AT") and str(F.counter("cancel/n")) == F.env("FAKE_RACE_AT"):
        # The Nth cancel loses the race: the build reached its own last step
        # first, so the record moved with the staged digest.
        F.finish_staged_build(F.staged_entries()[0])
        F.write_state(os.path.join("builds", build_id), "SUCCESS")
    else:
        F.write_state(os.path.join("builds", build_id), "CANCELLED")
    print("Cancelled build %s" % build_id)
    sys.exit(0)
if groups[:3] == ["run", "jobs", "execute"]:
    if F.env("FAKE_JOB_FAIL"):
        sys.stderr.write("ERROR: (gcloud.run.jobs.execute) the job fugarochk-catalog failed\n")
        sys.exit(1)
    sys.exit(F.catalog_check_job(argv))
if groups[:2] == ["logging", "read"]:
    import json
    filter_text = argv[argv.index("read") + 1]
    m = re.search(r"jsonPayload\.entry=(\S+)", filter_text)
    entry = m.group(1) if m else ""
    rows = F.log_rows()
    if entry:
        rows = [r for r in rows if (r.get("jsonPayload") or {}).get("entry") == entry]
    print(json.dumps(rows))
    sys.exit(0)
if groups[:2] == ["config", "get-value"]:
    print("owner@example.invalid")
    sys.exit(0)
sys.stderr.write("fake gcloud: no stand-in for '%s'\n" % " ".join(argv))
sys.exit(2)
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env python3
# A stand-in for `fugaro`, for scripts/live-checks/check292_test.sh only: the
# reads the check makes (version, doctor, image ls, project policy show) and the
# writes it makes (image add, image rm, update) against a project config of its
# own under $HOME, plus `image build`, which stages the entry's Dockerfile, the
# commit and a digest of the tree under its context, writes the entry's record
# and prints the two sentences the real command prints. Nothing here reaches a
# project or a Cloud Build.
import hashlib
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fakelib as F

argv = sys.argv[1:]
F.log("fugaro", argv)
if not argv:
    sys.exit(2)
cmd = argv[0]

if cmd == "version":
    print("fugaro 0.26.0 (fake)")
    sys.exit(0)
if cmd == "doctor":
    status = int(F.env("FAKE_DOCTOR_STATUS", "0"))
    doc = F.json_doc("doctor.json")
    if doc is None:
        sys.stderr.write("fugaro: no project\n")
        sys.exit(status or 1)
    if F.env("FAKE_DOCTOR_NO_PROJECT"):
        doc = {"checks": []}
    print(json.dumps(doc))
    sys.exit(status)
if cmd == "update":
    if F.env("FAKE_UPDATE_ASKS"):
        print("Publish the policy now? [y/N] ")
    if F.env("FAKE_UPDATE_FAIL"):
        sys.stderr.write("fugaro: the project policy could not be published\n")
        sys.exit(1)
    cfg = F.config_path()
    entries = F.read_entries(cfg)
    if F.env("FAKE_UPDATE_DROP_CONTEXT"):
        for e in entries:
            e["source"].pop("context", None)
    if F.env("FAKE_UPDATE_NO_ENTRY"):
        entries = [e for e in entries if not e["name"].startswith("chk292-")]
    if F.env("FAKE_UPDATE_STAGES"):
        for e in entries:
            os.makedirs(F.entry_prefix(e["name"]), exist_ok=True)
            F.write_state(os.path.join("bucket", "builds", "catalog", e["name"], "Dockerfile"), "FROM base\n")
    if F.env("FAKE_UPDATE_NO_CATALOG"):
        doc = {"policy": {}}
    else:
        doc = {"policy": {"image_catalog": {"entries": entries}}}
    if F.env("FAKE_UPDATE_UNKNOWN_KEY"):
        doc["policy"]["image_catalog"]["entries"].append({"name": "x", "surprise": True})
    with open(os.path.join(F.state_dir(), "policy.json"), "w", encoding="utf-8") as fh:
        json.dump(doc, fh)
    print("fugaro: published the project policy")
    sys.exit(0)
if cmd == "project" and argv[1:2] == ["policy"]:
    path = os.path.join(F.state_dir(), "policy.json")
    if not os.path.exists(path):
        print(json.dumps({"policy": {}}))
        sys.exit(0)
    with open(path, "r", encoding="utf-8") as fh:
        sys.stdout.write(fh.read())
    sys.exit(0)
if cmd == "image":
    sub = argv[1] if len(argv) > 1 else ""
    cfg = F.config_path()
    if sub == "ls":
        doc = F.json_doc("ls.json")
        if doc is None:
            doc = F.ls_doc()
        print(json.dumps(doc))
        sys.exit(0)
    if sub == "add":
        name = argv[2]
        if F.env("FAKE_ADD_FAIL"):
            sys.stderr.write("fugaro: the entry could not be added\n")
            sys.exit(1)
        repo, _, docker_path = F.flag(argv, "--dockerfile").partition(":")
        branch = F.flag(argv, "--branch")
        tags = F.flag(argv, "--tags")
        if not F.env("FAKE_ADD_NO_WRITE"):
            if not F.add_entry(cfg, name, repo, docker_path, branch, tags):
                sys.stderr.write("fugaro: the project config states no image_catalog\n")
                sys.exit(1)
        if not F.env("FAKE_ADD_NOT_IN_LS"):
            F.ls_add(name, tags, "%s:%s" % (repo, docker_path), "sha256:" + hashlib.sha256(name.encode()).hexdigest())
        print("fugaro: added catalog entry %s to %s" % (name, cfg))
        sys.exit(0)
    if sub == "rm":
        name = argv[2]
        if F.env("FAKE_RM_FAIL"):
            sys.stderr.write("fugaro: a job is running the entry %s, so it was not removed\n" % name)
            sys.exit(1)
        F.remove_entry(cfg, name)
        F.ls_remove(name)
        print("fugaro: removed catalog entry %s (--no-apply: the steps that make it live are owed)"
              % name)
        sys.exit(0)
    if sub == "build":
        waited = "--no-wait" not in argv
        tail = [a for a in argv[2:] if not a.startswith("-")]
        if F.env("FAKE_BUILD_FAIL"):
            sys.stderr.write("unknown flag: --build-context\n")
            sys.stderr.write("fugaro: the build of %s was refused\n" % (tail[0] if tail else ""))
            sys.exit(1)
        if F.env("FAKE_BUILD_HANG"):
            import time
            time.sleep(60)
            sys.exit(0)
        if not tail:
            sys.stderr.write("fugaro: image build needs an entry\n")
            sys.exit(2)
        entry = tail[0]
        stated = F.entry_of(cfg, entry)
        if stated is None:
            sys.stderr.write("fugaro: no such entry: %s\n" % entry)
            sys.exit(1)
        context = (stated.get("source") or {}).get("context", "")
        dockerfile = (stated.get("source") or {}).get("path", "Dockerfile")
        if F.env("FAKE_BUILD_NO_SOURCE"):
            dockerfile = "somewhere/else/Dockerfile"
        if not os.path.exists(os.path.join(os.getcwd(), dockerfile)):
            sys.stderr.write("fugaro: the entry's Dockerfile is not in this checkout: %s\n" % dockerfile)
            sys.exit(1)
        build_id = F.env("FAKE_BUILD_ID") or ("fake-build-%d" % F.counter("builds/n"))
        F.make_build(entry, os.getcwd(), dockerfile, context, build_id, waited)
        sys.exit(0)
sys.stderr.write("fake fugaro: no stand-in for '%s'\n" % " ".join(argv))
sys.exit(2)
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------- the setup
write_fakes() { # dir
  mkdir -p "$1/bin"
  write_fakelib "$1"
  write_fakelib_more "$1"
  write_fake_git "$1"
  write_fake_docker "$1"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
}

write_config() { # dir
  mkdir -p "$1/home/.config/fugaro/projects"
  cat >"$1/home/.config/fugaro/projects/$install.yaml" <<CONFIG_EOF
runs_bucket: $bucket
image_catalog:
  entries:
    - name: other
      tags:
        - redis
      source:
        repo: someone/other
        path: docker/Dockerfile
        branch: main
CONFIG_EOF
}

write_doctor() { # dir
  cat >"$1/json/doctor.json" <<DOCTOR_EOF
{
  "checks": [{"name": "credentials", "ok": true}],
  "project": {
    "name": "$install",
    "gcp_project": "$gcp_project",
    "region": "$region",
    "base_images": {"base": "$region-docker.pkg.dev/$gcp_project/fugaro/base:1"}
  }
}
DOCTOR_EOF
}

prepare() { # prepare DIR
  local dir=$1
  mkdir -p "$dir/bin" "$dir/home" "$dir/tmp" "$dir/logs" "$dir/state" "$dir/json" "$dir/sandbox"
  write_fakes "$dir"
  printf 'project: %s\n' "$gcp_project" >"$dir/sandbox/fugaro.yaml"
  write_config "$dir"
  write_doctor "$dir"
  mkdir -p "$dir/state/git"
  printf 'main\n' >"$dir/state/git/branch"
  printf '%040x\n' 1 >"$dir/state/git/head"
  : >"$dir/logs/git.calls"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/docker.calls"
  : >"$dir/logs/fugaro.calls"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/git.calls"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/docker.calls"
  : >"$dir/logs/fugaro.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the
  # only way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # may itself be started from one. The case that measures that refusal sets the
  # marker again, and a variable given as an argument wins over `env -u`.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" XDG_CONFIG_HOME="$dir/home/.config" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_STATE_DIR="$dir/state" FAKE_JSON_DIR="$dir/json" \
    CHECK292_SANDBOX="$dir/sandbox" \
    CHECK292_DOCTOR_TIMEOUT_SECONDS=20 CHECK292_CLI_TIMEOUT_SECONDS=20 \
    CHECK292_UPDATE_TIMEOUT_SECONDS=20 CHECK292_BUILD_TIMEOUT_SECONDS=20 \
    CHECK292_REBUILD_TIMEOUT_SECONDS=30 CHECK292_CHECK_TIMEOUT_SECONDS=20 \
    CHECK292_LOG_WAIT_SECONDS="${CHECK292_LOG_WAIT_SECONDS:-2}" \
    CHECK292_LOG_POLL_SECONDS=1 CHECK292_STAGE_WAIT_SECONDS=5 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------------ assertions
row() { # row KEY — the key's FACT line out of the block
  printf '%s\n' "$out" | sed -n "s/^FACT $1: \(.*\)$/\1/p" | head -n 1
}
assert_row() { # KEY VERDICT what
  local line
  line=$(row "$1")
  case "$line" in
  "$2"*) ;;
  *) fail "$3: expected the $1 row to read '$2'; it reads '${line:-<none>}'" ;;
  esac
}
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}
assert_block_shape() { # what — every row of the block, in order
  local want="fugaro-version xtrace agent-session belong sandbox entry update build content record label staged quiet trigger rebuild converge restore scratch-removed"
  [ -n "${2:-}" ] && want="$want $2"
  local got key
  got=$(printf '%s\n' "$out" | sed -n 's/^FACT \([^:]*\):.*$/\1/p' | tr '\n' ' ')
  got=${got% }
  [ "$got" = "$want" ] || fail "$1: the block's rows are '$got', want '$want'"
  expect_contains "$out" "CHECK 292 RESULT" "$1: the block's opening line"
  for key in $want; do
    printf '%s\n' "$out" | grep -q "^FACT $key: " || fail "$1: no row for $key"
  done
}
assert_no_live_command() { # dir what
  local dir=$1 what=$2
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "$what: nothing is built"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run jobs execute" "$what: no check job is run"
  expect_absent "$(cat "$dir/logs/docker.calls")" "run --rm" "$what: nothing is read out of an image"
  expect_absent "$(cat "$dir/logs/git.calls")" "push" "$what: nothing is pushed"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check292.sh --sandbox <a sandbox repository checkout>" "--help: the command it documents"
  expect_contains "$out" "--dry-run" "--help: the flags"
  expect_absent "$out" "FACT " "--help: no block is printed"
  assert_no_live_command "$dir" "--help"
}

test_an_unknown_argument() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --nonsense" "an unknown argument is named"
  expect_absent "$out" "FACT sandbox:" "an unknown argument: no step is run"
}

test_dry_run() {
  local dir=$work/dryrun
  prepare "$dir"
  run_check "$dir" CHECK292_ENTRY=$trace_entry -- --dry-run
  expect_status "$status" 0 "--dry-run"
  expect_contains "$err" "CHECK 292 plan" "--dry-run: the plan is printed"
  expect_contains "$(row dry-run)" "no entry, no update, no build" "--dry-run: the dry-run row says what was not run"
  assert_block_shape "--dry-run" "dry-run"
  assert_row "fugaro-version" PASS "--dry-run: the version row"
  assert_row xtrace PASS "--dry-run: the xtrace row"
  assert_row "agent-session" PASS "--dry-run: the agent-session row"
  assert_row belong PASS "--dry-run: the belong row"
  assert_row sandbox INFO "--dry-run: the sandbox row is planned, not measured"
  assert_row converge INFO "--dry-run: the last measured row is planned too"
  assert_row "dry-run" PASS "--dry-run: the dry-run row"
  assert_scratch_removed "--dry-run"
  assert_no_live_command "$dir" "--dry-run"
  expect_contains "$err" "--branch chk292-" "--dry-run: the branch the plan names"
}

test_a_missing_sandbox() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK292_SANDBOX=""
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "needs --sandbox" "no --sandbox is named"
  expect_absent "$out" "FACT " "no --sandbox: the run never reaches a block"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "no --sandbox: nothing is read"
}

test_a_path_that_is_not_a_checkout() {
  local dir=$work/not-a-checkout
  prepare "$dir"
  rm -f "$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "the missing fugaro.yaml is named"
  expect_contains "$out" "CHECK 292 RESULT" "a directory that is not a checkout: the block is still printed"
  assert_row sandbox "FAIL — not reached" "a directory that is not a checkout: the sandbox row is not reached"
  assert_row entry "FAIL — not reached" "a directory that is not a checkout: no entry row is measured"
  expect_absent "$(cat "$dir/logs/git.calls")" "push" "nothing is pushed"
  assert_scratch_removed "a directory that is not a checkout"
}

test_a_checkout_whose_origin_cannot_be_read() {
  local dir=$work/no-origin
  prepare "$dir"
  run_check "$dir" FAKE_GIT_NO_ORIGIN=1
  expect_status "$status" 1 "a checkout with no origin"
  assert_row sandbox FAIL "a checkout with no origin"
  expect_contains "$(row sandbox)" "names no origin" "the row says the origin could not be read"
  assert_scratch_removed "a checkout with no origin"
}

test_a_checkout_with_uncommitted_changes() {
  local dir=$work/dirty
  prepare "$dir"
  run_check "$dir" FAKE_GIT_DIRTY=1
  expect_status "$status" 1 "a dirty checkout"
  expect_contains "$err" "has uncommitted changes" "the dirty tree is named"
  expect_contains "$(row sandbox)" "ommit or stash them" "the row says what to do"
  expect_absent "$(cat "$dir/logs/git.calls")" "push" "a dirty checkout: nothing is pushed"
  assert_scratch_removed "a dirty checkout"
}

test_a_checkout_on_no_branch() {
  local dir=$work/detached
  prepare "$dir"
  run_check "$dir" FAKE_GIT_DETACHED=1
  expect_status "$status" 1 "a detached HEAD"
  expect_contains "$(row sandbox)" "detached HEAD" "the detached HEAD is named"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "a traced shell"
  expect_contains "$err" "this shell is tracing" "the trace is refused with its reason"
  assert_row xtrace FAIL "a traced shell"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a traced shell: nothing is read"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set" "the marker is named"
  assert_row "agent-session" FAIL "a coding agent's session"
  assert_no_live_command "$dir" "a coding agent's session"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK292_SANDBOX="/tmp/belong-sandbox"
  expect_status "$status" 1 "a sandbox path naming belong"
  expect_contains "$err" "never runs against belong" "the refusal names belong"
  assert_row belong FAIL "a sandbox path naming belong"
  assert_no_live_command "$dir" "a sandbox path naming belong"
}

test_an_entry_naming_belong_is_refused() {
  local dir=$work/belong-entry
  prepare "$dir"
  run_check "$dir" CHECK292_ENTRY=belong-chk292
  expect_status "$status" 1 "an entry name naming belong"
  assert_row belong FAIL "an entry name naming belong"
  assert_no_live_command "$dir" "an entry name naming belong"
}

test_an_installation_naming_belong() {
  local dir=$work/belong-project
  prepare "$dir"
  python3 - "$dir/json/doctor.json" <<'PY'
import json, sys
path = sys.argv[1]
doc = json.load(open(path))
doc["project"]["gcp_project"] = "belong-prod"
json.dump(doc, open(path, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "an installation naming belong"
  assert_row belong FAIL "an installation naming belong"
  assert_no_live_command "$dir" "an installation naming belong"
}

test_doctor_that_does_not_answer() {
  local dir=$work/doctor-broken
  prepare "$dir"
  run_check "$dir" FAKE_DOCTOR_STATUS=7
  expect_status "$status" 2 "a doctor that does not answer"
  assert_row sandbox FAIL "a doctor that does not answer"
  expect_contains "$(row sandbox)" "fugaro doctor --json did not answer" "the row says what failed"
  assert_scratch_removed "a doctor that does not answer"
}

test_doctor_without_a_project() {
  local dir=$work/doctor-no-project
  prepare "$dir"
  run_check "$dir" FAKE_DOCTOR_NO_PROJECT=1
  expect_status "$status" 2 "a doctor with no project block"
  assert_row sandbox FAIL "a doctor with no project block"
  expect_contains "$(row sandbox)" "without a project block" "the row says what is missing"
}

test_no_project_config() {
  local dir=$work/no-config
  prepare "$dir"
  rm -f "$dir/home/.config/fugaro/projects/$install.yaml"
  run_check "$dir"
  expect_status "$status" 2 "no project config"
  assert_row sandbox FAIL "no project config"
  expect_contains "$(row sandbox)" "no project config at" "the row names the path"
}

test_a_config_stating_no_catalog() {
  local dir=$work/no-catalog
  prepare "$dir"
  printf 'runs_bucket: %s\n' "$bucket" >"$dir/home/.config/fugaro/projects/$install.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a config that states no catalog"
  assert_row sandbox FAIL "a config that states no catalog"
  expect_contains "$(row sandbox)" "states no image_catalog" "the row names what is missing"
}

test_the_context_directory_already_there() {
  local dir=$work/context-there
  prepare "$dir"
  mkdir -p "$dir/sandbox/ci"
  run_check "$dir"
  expect_status "$status" 2 "a checkout that already has the context directory"
  assert_row entry FAIL "a checkout that already has the context directory"
  expect_contains "$(row entry)" "already carries ci/" "the directory is named"
  expect_absent "$(cat "$dir/logs/git.calls")" "push" "nothing is pushed"
}

test_the_repository_root_is_not_a_context() {
  local dir=$work/root-context
  prepare "$dir"
  run_check "$dir" CHECK292_CONTEXT=.
  expect_status "$status" 2 "a root context"
  assert_row entry FAIL "a root context"
  expect_contains "$(row entry)" "repository root" "the row says why a root context is not this check's"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir" CHECK292_ENTRY=$trace_entry
  expect_status "$status" 0 "the happy path"
  assert_block_shape "the happy path"
  assert_row "fugaro-version" PASS "the happy path"
  assert_row xtrace PASS "the happy path"
  assert_row "agent-session" PASS "the happy path"
  assert_row belong PASS "the happy path"
  assert_row sandbox PASS "the happy path"
  assert_row entry PASS "the happy path"
  assert_row update PASS "the happy path"
  assert_row build PASS "the happy path"
  assert_row content PASS "the happy path"
  assert_row record PASS "the happy path"
  assert_row label PASS "the happy path"
  assert_row staged PASS "the happy path"
  assert_row quiet PASS "the happy path"
  assert_row trigger PASS "the happy path"
  assert_row rebuild PASS "the happy path"
  assert_row converge PASS "the happy path"
  assert_row restore PASS "the happy path"
  assert_row "scratch-removed" PASS "the happy path"
  assert_scratch_removed "the happy path"
  # The entry: a name no project has, added with --no-apply and taken back out.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image add $trace_entry --dockerfile chk292-sandbox/sandbox-repo:ci/Dockerfile --branch chk292-" "the entry is added from the checkout's repository"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "--tags context-check --no-apply" "the add uses --no-apply, so no catalog of the installation is built first"
  # The update publishes and stages nothing; the copy inside the build stages.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "update" "the update runs"
  expect_contains "$(row update)" "staged NOTHING" "the update staged nothing"
  expect_contains "$(row staged)" "source-commit" "the build staged the commit a rebuild is pinned to"
  # One build of the entry, then two staged-and-cancelled copies for the trigger.
  [ "$(grep -c 'image build' "$dir/logs/fugaro.calls")" = 3 ] || fail "three builds are driven (one real, two cancelled): $(grep -c 'image build' "$dir/logs/fugaro.calls")"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "builds cancel" "the staged builds are cancelled before their record step"
  [ "$(grep -c 'run jobs execute' "$dir/logs/gcloud.calls")" = 3 ] || fail "the check job is run three times (the quiet night, the trigger, the second night): $(grep -c 'run jobs execute' "$dir/logs/gcloud.calls")"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "logging read" "the decision is read back out of Cloud Logging"
  # Three commits: the entry's, one outside the context, one under it.
  [ "$(grep -c 'commit' "$dir/logs/git.calls")" = 3 ] || fail "three commits are made: $(grep -c 'commit' "$dir/logs/git.calls")"
  expect_contains "$(cat "$dir/logs/git.calls")" "$trace_entry-outside.txt" "a commit outside the context is made"
  expect_contains "$(row quiet)" "outside ci/" "the quiet row names the commit outside the context"
  expect_contains "$(row trigger)" "catalog-context" "the trigger row names the reason"
  # The copied file is read out of the image, not out of the checkout.
  expect_contains "$(cat "$dir/logs/docker.calls")" "run --rm --entrypoint cat" "the copied file is read out of the image"
  expect_contains "$(row content)" "carries the copied file" "the copied file is in the image"
  # The restore leaves three things named and takes the entry back out.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image rm $trace_entry --no-apply" "the entry is taken back out"
  # The entry's own objects go with it: the staged objects, the record and the
  # check state, so the next nightly run has nothing of this entry to decide.
  [ "$(grep -c 'storage rm' "$dir/logs/gcloud.calls")" = 2 ] || fail "the entry's own objects are removed in two commands: $(grep -c 'storage rm' "$dir/logs/gcloud.calls")"
  expect_contains "$(row restore)" "were removed from" "the restore says the entry's objects went"
  expect_absent "$(row restore)" "stay under" "the restore does not leave the staged objects behind"
  [ -z "$(find "$dir/state/bucket" -path "*$trace_entry*" -type f 2>/dev/null | head -n 1)" ] || fail "objects of $trace_entry are still in the runs bucket after the restore: $(find "$dir/state/bucket" -path "*$trace_entry*" -type f 2>/dev/null | head -n 3 | tr '\n' ' ')"
  expect_contains "$(row restore)" "git push origin --delete chk292-" "the restore names the branch it leaves"
  expect_contains "$(row restore)" "OWED" "the restore names the step --no-apply left owed"
  expect_contains "$(cat "$dir/logs/git.calls")" "checkout -q main" "the checkout goes back to the branch it was on"
  expect_absent "$(cat "$dir/logs/git.calls")" "--delete" "the check deletes no branch"
}

test_another_context_directory() {
  local dir=$work/other-context
  prepare "$dir"
  run_check "$dir" CHECK292_CONTEXT=docker CHECK292_ENTRY=$trace_entry
  expect_status "$status" 0 "a context that is not ci/"
  assert_row entry PASS "a context that is not ci/"
  assert_row quiet PASS "a context that is not ci/"
  assert_row trigger PASS "a context that is not ci/"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image add $trace_entry --dockerfile chk292-sandbox/sandbox-repo:docker/Dockerfile" "the entry's Dockerfile is read under the named context"
  expect_contains "$(row entry)" "context: docker/" "the member written is the named context"
}

test_update_fails_on_the_version_gate() {
  local dir=$work/update-fails
  prepare "$dir"
  run_check "$dir" FAKE_UPDATE_FAIL=1
  expect_status "$status" 1 "an update that fails"
  assert_row update FAIL "an update that fails"
  expect_contains "$(row update)" "exit" "the update row names the status"
  expect_contains "$(row update)" "0.26.0" "the update row names the release the gate came in with"
  assert_row build FAIL "an update that fails: the build is not reached"
}

test_update_that_asks_a_question() {
  local dir=$work/update-asks
  prepare "$dir"
  run_check "$dir" FAKE_UPDATE_ASKS=1
  expect_status "$status" 1 "an update that asks"
  assert_row update FAIL "an update that asks"
  expect_contains "$(row update)" "ask nothing new" "the row is about the question"
  assert_row build FAIL "an update that asks: the build is not reached"
}

test_update_that_stages_an_object() {
  local dir=$work/update-stages
  prepare "$dir"
  run_check "$dir" FAKE_UPDATE_STAGES=1
  expect_status "$status" 1 "an update that stages"
  assert_row update FAIL "an update that stages"
  expect_contains "$(row update)" "staged objects under the entry's own prefix" "the row names what the update staged"
  assert_row build PASS "an update that stages: the build below still runs"
  assert_row staged PASS "an update that stages: the objects the build's own copy staged are what the row reads"
}

test_the_published_node_drops_the_context() {
  local dir=$work/policy-no-context
  prepare "$dir"
  run_check "$dir" FAKE_UPDATE_DROP_CONTEXT=1
  expect_status "$status" 1 "a node that drops the context"
  assert_row update FAIL "a node that drops the context"
  expect_contains "$(row update)" "names no context" "the row says the member is missing"
}

test_the_published_node_omits_the_entry() {
  local dir=$work/policy-no-entry
  prepare "$dir"
  run_check "$dir" FAKE_UPDATE_NO_ENTRY=1
  expect_status "$status" 1 "a node without the entry"
  assert_row update FAIL "a node without the entry"
  expect_contains "$(row update)" "none of them is" "the row names the entry it did not find"
}

test_the_published_node_has_no_catalog() {
  local dir=$work/policy-no-catalog
  prepare "$dir"
  run_check "$dir" FAKE_UPDATE_NO_CATALOG=1
  expect_status "$status" 1 "a node with no catalog"
  assert_row update FAIL "a node with no catalog"
  expect_contains "$(row update)" "no image_catalog at all" "the row says the node has no catalog"
}

test_a_build_failing_with_the_build_context_flag() {
  local dir=$work/build-flag
  prepare "$dir"
  run_check "$dir" FAKE_BUILD_FAIL=1
  expect_status "$status" 1 "a build that fails"
  assert_row build FAIL "a build that fails"
  expect_contains "$(row build)" "unknown flag: --build-context" "the row quotes the build's own words"
  expect_contains "$(row build)" "#2995" "the row names the bug and the release that fixed it"
  expect_contains "$(row build)" "0.25.0" "the row names the release the bug was in"
  assert_row content FAIL "a build that fails: no image is read"
  assert_row record FAIL "a build that fails: no record is read"
}

test_a_build_whose_output_names_no_image() {
  local dir=$work/build-no-image
  prepare "$dir"
  run_check "$dir" FAKE_BUILD_NO_DIGEST=1
  expect_status "$status" 2 "a build that names no image"
  assert_row build FAIL "a build that names no image"
  expect_contains "$(row build)" "names no 'built " "the row says what the output is missing"
  assert_row content FAIL "a build that names no image: nothing is read out of an image"
}

test_a_record_that_is_not_there() {
  local dir=$work/no-record
  prepare "$dir"
  run_check "$dir" FAKE_NO_RECORD=1
  expect_status "$status" 1 "a build that left no record"
  assert_row record FAIL "a build that left no record"
  expect_contains "$(row record)" "no build record" "the row says the record is missing"
  assert_row content PASS "a build that left no record: the image the build's own line named is still read"
}

test_a_record_naming_another_commit() {
  local dir=$work/record-commit
  prepare "$dir"
  run_check "$dir" FAKE_RECORD_COMMIT=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef
  expect_status "$status" 1 "a record naming another commit"
  assert_row record FAIL "a record naming another commit"
  expect_contains "$(row record)" "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" "the row quotes the commit the record names"
  assert_row label FAIL "a record naming another commit: the image is not read against a record"
}

test_a_record_without_a_context_digest() {
  local dir=$work/record-no-digest
  prepare "$dir"
  run_check "$dir" FAKE_RECORD_NO_DIGEST=1
  expect_status "$status" 1 "a record without a context digest"
  assert_row record FAIL "a record without a context digest"
  expect_contains "$(row record)" "no context_sha256" "the row names the missing member"
}

test_a_record_whose_two_digests_are_one() {
  local dir=$work/record-one-digest
  prepare "$dir"
  run_check "$dir" FAKE_RECORD_SAME_DIGEST=1
  expect_status "$status" 1 "a record with one digest for both inputs"
  assert_row record FAIL "a record with one digest for both inputs"
  expect_contains "$(row record)" "SAME value" "the row is about the two digests being one"
}

test_a_record_without_a_template_salt() {
  local dir=$work/record-no-salt
  prepare "$dir"
  run_check "$dir" FAKE_RECORD_NO_SALT=1
  expect_status "$status" 1 "a record without a template salt"
  assert_row record FAIL "a record without a template salt"
  expect_contains "$(row record)" "no template_salt" "the row names the missing member"
}

test_a_staged_hash_that_disagrees_with_the_record() {
  local dir=$work/staged-hash
  prepare "$dir"
  run_check "$dir" FAKE_STAGED_HASH=cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebab
  expect_status "$status" 1 "a staged hash that disagrees"
  assert_row staged FAIL "a staged hash that disagrees"
  expect_contains "$(row staged)" "context-sha256 holds" "the row quotes the staged value"
  expect_contains "$(row staged)" "the same build wrote" "the row is about the two accounts of one build"
}

test_the_image_holds_other_bytes() {
  local dir=$work/content
  prepare "$dir"
  run_check "$dir" FAKE_DOCKER_NO_FILE=1
  expect_status "$status" 1 "an image without the copied file"
  assert_row content FAIL "an image without the copied file"
  expect_contains "$(row content)" "does not carry" "the row says the image lacks the file"
}

test_the_image_is_labelled_with_another_commit() {
  local dir=$work/label
  prepare "$dir"
  run_check "$dir" FAKE_LABEL_OTHER=cafebabecafebabecafebabecafebabecafebabca
  expect_status "$status" 1 "an image labelled with another commit"
  assert_row label FAIL "an image labelled with another commit"
  expect_contains "$(row label)" "cafebabecafebabecafebabecafebabecafebabca" "the row quotes the label"
}

test_the_image_has_no_label() {
  local dir=$work/label-empty
  prepare "$dir"
  run_check "$dir" FAKE_LABEL_EMPTY=1
  expect_status "$status" 1 "an image with no revision label"
  assert_row label FAIL "an image with no revision label"
  expect_contains "$(row label)" "no org.opencontainers.image.revision label" "the row names the missing label"
}

test_docker_refusing_the_image() {
  local dir=$work/docker-refused
  prepare "$dir"
  run_check "$dir" FAKE_DOCKER_REFUSED=1
  expect_status "$status" 2 "a docker that refuses"
  assert_row content FAIL "a docker that refuses"
  expect_contains "$(row content)" "configure-docker" "the row names the fix"
  assert_row label FAIL "a docker that refuses: the label is not read"
}

test_a_commit_outside_the_context_still_fires() {
  # The digest taken over the whole repository instead of the context: a commit
  # outside the context moves it, and the entry rebuilds on every commit.
  local dir=$work/digest-whole-repo
  prepare "$dir"
  run_check "$dir" FAKE_DIGEST_WHOLE_REPO=1
  expect_status "$status" 1 "a digest taken over the whole repository"
  assert_row quiet FAIL "a digest taken over the whole repository"
  expect_contains "$(row quiet)" "catalog-context" "the row quotes the reason the decision carried"
  expect_contains "$(row quiet)" "every commit of the repository" "the row says what the defect costs"
}

test_a_decision_that_never_fires() {
  local dir=$work/never-fires
  prepare "$dir"
  run_check "$dir" FAKE_DECIDE_SKIP=1
  expect_status "$status" 1 "a decision that never fires"
  assert_row quiet PASS "a decision that never fires: a commit outside is quiet anyway"
  assert_row trigger FAIL "a decision that never fires"
  expect_contains "$(row trigger)" "rebuild trigger that never fires" "the row says what the defect costs"
  assert_row rebuild FAIL "a decision that never fires: no rebuild follows"
}

test_a_decision_that_fires_without_rebuilding() {
  local dir=$work/fires-no-rebuild
  prepare "$dir"
  run_check "$dir" FAKE_JOB_NO_BUILD_ID=1
  expect_status "$status" 1 "a decision that names no build"
  assert_row trigger FAIL "a decision that names no build"
  expect_contains "$(row trigger)" "names no build id" "the row is about the missing build id"
  assert_row converge FAIL "a decision that names no build: the second decision is not read"
}

test_the_rebuild_ends_failed() {
  local dir=$work/rebuild-failed
  prepare "$dir"
  run_check "$dir" FAKE_REBUILD_STATUS=FAILURE
  expect_status "$status" 1 "a rebuild that fails"
  assert_row trigger PASS "a rebuild that fails: the decision fired"
  assert_row rebuild FAIL "a rebuild that fails"
  expect_contains "$(row rebuild)" "ended FAILURE" "the row names the status the build ended with"
}

test_the_rebuild_is_labelled_with_another_commit() {
  local dir=$work/rebuild-label
  prepare "$dir"
  run_check "$dir" FAKE_REBUILD_LABEL_OTHER=1111111111111111111111111111111111111111
  expect_status "$status" 1 "a rebuild whose image is labelled with another commit"
  assert_row rebuild FAIL "a rebuild whose image is labelled with another commit"
  expect_contains "$(row rebuild)" "1111111111111111111111111111111111111111" "the row quotes the label"
}

test_a_decision_that_fires_twice() {
  local dir=$work/fires-twice
  prepare "$dir"
  run_check "$dir" FAKE_DECIDE_ALWAYS=1
  expect_status "$status" 1 "a decision that fires twice"
  assert_row trigger PASS "a decision that fires twice: the first decision is right"
  assert_row rebuild PASS "a decision that fires twice: the rebuild ran"
  assert_row converge FAIL "a decision that fires twice"
  expect_contains "$(row converge)" "every run" "the row says what the defect costs"
}

test_a_check_job_that_fails() {
  local dir=$work/job-fails
  prepare "$dir"
  run_check "$dir" FAKE_JOB_FAIL=1
  expect_status "$status" 2 "a check job that fails"
  assert_row quiet FAIL "a check job that fails"
  expect_contains "$(row quiet)" "the check's own refusal is not this check's finding" "the row says whose refusal it is"
  assert_row trigger FAIL "a check job that fails: nothing is decided"
}

test_a_log_line_that_never_lands() {
  # The job ran and printed its line, but every line the reader can see is older
  # than the run: an older run's decision must never be read as this one's.
  local dir=$work/old-log
  prepare "$dir"
  run_check "$dir" FAKE_LOG_STAMP=2020-01-01T00:00:00Z
  expect_status "$status" 2 "a decision whose line is older than the run"
  assert_row quiet FAIL "a decision whose line is older than the run"
  expect_contains "$(row quiet)" "landed within" "the row says no line landed for this run"
}

test_a_submitted_build_that_had_already_recorded() {
  # The register's step 5: the cancel loses the race, the record moves with the
  # staged digest, and the next decision is quiet for the right reason. The check
  # pushes another commit and looks again.
  local dir=$work/race-lost
  prepare "$dir"
  run_check "$dir" FAKE_RACE_AT=2
  expect_status "$status" 0 "a cancel that lost the race"
  assert_row quiet PASS "a cancel that lost the race: the first attempt is quiet"
  assert_row trigger PASS "a cancel that lost the race: the trigger still fires"
  [ "$(grep -c 'commit' "$dir/logs/git.calls")" -ge 4 ] || fail "a lost race is pushed over with another commit"
}

test_the_scratch_directory_is_kept_when_asked() {
  local dir=$work/keep-scratch
  prepare "$dir"
  run_check "$dir" CHECK292_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  assert_row "scratch-removed" FAIL "a kept scratch directory"
  expect_contains "$(row scratch-removed)" "still there" "the row says the directory is still there"
  if [ -n "$scratch" ] && [ -e "$scratch" ]; then
    rm -rf "$scratch"
  else
    fail "a kept scratch directory: the scratch directory was not kept"
  fi
}

test_the_entry_is_taken_back_out_of_the_config() {
  local dir=$work/restore
  prepare "$dir"
  run_check "$dir" CHECK292_ENTRY=$trace_entry
  expect_status "$status" 0 "the restore"
  expect_absent "$(cat "$dir/home/.config/fugaro/projects/$install.yaml")" "$trace_entry" "the entry is out of the project config"
  expect_contains "$(row restore)" "stay in chk292-sandbox/sandbox-repo" "the restore names the repository it leaves commits in"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image rm $trace_entry" "the entry is removed through the CLI"
}

test_objects_that_could_not_be_removed() {
  # The entry goes back out of the catalog, and the objects it wrote are what the
  # next nightly run would read: a removal gcloud refused is the restore row's.
  local dir=$work/rm-refused
  prepare "$dir"
  run_check "$dir" FAKE_RM_REFUSED=1
  expect_status "$status" 1 "objects that could not be removed"
  assert_row restore FAIL "objects that could not be removed"
  expect_contains "$(row restore)" "storage.objects.delete" "the row quotes gcloud's own refusal"
  expect_contains "$(row restore)" "never remove builds/image-lock.json" "the row says what not to remove by hand"
  expect_absent "$(row restore)" "were removed from" "the row does not claim a removal that did not happen"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image rm" "the entry is still taken back out"
}

test_a_removal_that_fails_is_reported() {
  local dir=$work/restore-fails
  prepare "$dir"
  run_check "$dir" FAKE_RM_FAIL=1 CHECK292_ENTRY=$trace_entry
  assert_row restore FAIL "a removal that fails"
  expect_contains "$(row restore)" "still in" "the row says the entry is still in the config"
  expect_contains "$(row restore)" "remove it by hand" "the row says what to do"
}

# Sharding, so the Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_help
t test_an_unknown_argument
t test_dry_run
t test_a_missing_sandbox
t test_a_path_that_is_not_a_checkout
t test_a_checkout_whose_origin_cannot_be_read
t test_a_checkout_with_uncommitted_changes
t test_a_checkout_on_no_branch
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_an_entry_naming_belong_is_refused
t test_an_installation_naming_belong
t test_doctor_that_does_not_answer
t test_doctor_without_a_project
t test_no_project_config
t test_a_config_stating_no_catalog
t test_the_context_directory_already_there
t test_the_repository_root_is_not_a_context
t test_happy_path
t test_another_context_directory
t test_update_fails_on_the_version_gate
t test_update_that_asks_a_question
t test_update_that_stages_an_object
t test_the_published_node_drops_the_context
t test_the_published_node_omits_the_entry
t test_the_published_node_has_no_catalog
t test_a_build_failing_with_the_build_context_flag
t test_a_build_whose_output_names_no_image
t test_a_record_that_is_not_there
t test_a_record_naming_another_commit
t test_a_record_without_a_context_digest
t test_a_record_whose_two_digests_are_one
t test_a_record_without_a_template_salt
t test_a_staged_hash_that_disagrees_with_the_record
t test_the_image_holds_other_bytes
t test_the_image_is_labelled_with_another_commit
t test_the_image_has_no_label
t test_docker_refusing_the_image
t test_a_commit_outside_the_context_still_fires
t test_a_decision_that_never_fires
t test_a_decision_that_fires_without_rebuilding
t test_the_rebuild_ends_failed
t test_the_rebuild_is_labelled_with_another_commit
t test_a_decision_that_fires_twice
t test_a_check_job_that_fails
t test_a_log_line_that_never_lands
t test_a_submitted_build_that_had_already_recorded
t test_the_scratch_directory_is_kept_when_asked
t test_the_entry_is_taken_back_out_of_the_config
t test_a_removal_that_fails_is_reported
t test_objects_that_could_not_be_removed

if [ "$failures" != 0 ]; then
  echo "$failures case(s) failed" >&2
  exit 1
fi
echo "check292_test.sh: all cases passed" >&2
