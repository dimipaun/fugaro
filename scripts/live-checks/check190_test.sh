#!/usr/bin/env bash
# Tests for scripts/live-checks/check190.sh, with fake `gcloud`, `git`, `curl`
# and `fugaro` on PATH: NOTHING here reaches a cloud, a project, a Bitbucket
# repository or the network, no agent runs a live command, and the fake tools
# record every argument they are given so the test can assert that no command
# ever named anything but the sandbox installation's project and that the token
# never appears in an argument or in the output.
#
# Usage: bash scripts/live-checks/check190_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check190.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK190_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 600)" ;;
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
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check190.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
case "${1:-}" in
version) echo "fugaro 0.13.0 (test)" ;;
doctor)
  # Faithful to internal/cli/doctor.go's emitDoctor: the checks first, then the
  # project: line, then secrets: — all on stdout — and exit 1 as soon as any
  # check of its own failed, whatever the project: line said.
  case "${FAKE_FUGARO_MODE:-ok}" in
  ok) echo "project: fugaro (GCP fugaro-dev, us-east5)" ;;
  other) echo "project: fugaro (GCP someone-else-prod, us-east5)" ;;
  *) exit 1 ;;
  esac
  ;;
*) exit 1 ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
"""A stand-in for `gcloud`, for scripts/live-checks/check190.sh's test only.

It keeps a little state in $FAKE_GCLOUD_STATE -- whether the service, the
repository and the secret exist -- so the guards, the deploy and the Restore see
a project that changes the way a real one would. It never talks to anything.
"""
import os, sys

STATE = os.environ["FAKE_GCLOUD_STATE"]
LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")

with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]


def flag(name, default=""):
    for a in args:
        if a.startswith(name + "="):
            return a[len(name) + 1:]
    return default


def has(name):
    return name in args


def state_get(k):
    path = os.path.join(STATE, "gcloud." + k)
    if os.path.exists(path):
        return open(path).read().strip()
    return "0"


def state_set(k, v):
    with open(os.path.join(STATE, "gcloud." + k), "w") as fh:
        fh.write(v)


# Like the real CLI, a flag a subcommand does not have is an error. EVERY
# subcommand this script calls is listed with the flags it may pass; a call to a
# subcommand that is not here, or with a flag that is not in its list, fails
# with exit 2. A misspelled or invented flag therefore cannot hide in any call.
ALLOWED = {
    ("auth", "print-identity-token"): (),
    ("projects", "describe"): ("--format",),
    ("iam", "service-accounts", "list"): ("--project", "--filter", "--format"),
    ("storage", "rm"): ("-r", "--quiet"),
    ("storage", "buckets", "create"): ("--project", "--location", "--uniform-bucket-level-access", "--quiet"),
    ("storage", "buckets", "list"): ("--project", "--filter", "--format"),
    ("storage", "buckets", "add-iam-policy-binding"): ("--member", "--role", "--quiet"),
    ("secrets", "create"): ("--project", "--replication-policy", "--data-file", "--quiet"),
    ("secrets", "add-iam-policy-binding"): ("--project", "--member", "--role", "--quiet"),
    ("secrets", "list"): ("--project", "--filter", "--format"),
    ("secrets", "delete"): ("--project", "--quiet"),
    ("artifacts", "repositories", "create"): ("--project", "--location", "--repository-format", "--description", "--quiet"),
    ("artifacts", "repositories", "list"): ("--project", "--location", "--filter", "--format"),
    ("artifacts", "repositories", "delete"): ("--project", "--location", "--quiet"),
    ("artifacts", "repositories", "add-iam-policy-binding"): ("--project", "--location", "--member", "--role", "--quiet"),
    ("artifacts", "docker", "images", "delete"): ("--project", "--delete-tags", "--quiet"),
    ("builds", "submit"): ("--project", "--tag", "--config", "--service-account", "--gcs-source-staging-dir",
                           "--gcs-log-dir", "--async", "--format"),
    ("builds", "describe"): ("--project", "--format"),
    ("builds", "cancel"): ("--project", "--quiet"),
    ("run", "deploy"): ("--project", "--region", "--image", "--no-allow-unauthenticated", "--timeout",
                        "--min-instances", "--max-instances", "--set-secrets", "--set-env-vars", "--quiet"),
    ("run", "services", "list"): ("--project", "--region", "--filter", "--format"),
    ("run", "services", "describe"): ("--project", "--region", "--format"),
    ("run", "revisions", "describe"): ("--project", "--region", "--format"),
    ("logging", "read"): ("--project", "--freshness", "--limit", "--format"),
    ("run", "services", "get-iam-policy"): ("--project", "--region", "--format"),
    ("run", "services", "delete"): ("--project", "--region", "--quiet"),
}

positionals = [a for a in args if not a.startswith("-")]
path = tuple(positionals[:4])
sub = None
for n in (4, 3, 2):
    if tuple(positionals[:n]) in ALLOWED:
        sub, path = tuple(positionals[:n]), tuple(positionals[:n])
        break
if sub is None:
    sys.stderr.write("ERROR: (gcloud) unknown command: %s\n" % " ".join(args))
    sys.exit(2)
allowed = ALLOWED[sub]
for a in args:
    if a.startswith("--") and a.split("=")[0] not in allowed:
        sys.stderr.write("ERROR: (gcloud) unrecognized argument: %s\n" % a)
        sys.exit(2)
    if a.startswith("-") and not a.startswith("--") and a not in allowed:
        sys.stderr.write("ERROR: (gcloud) unrecognized argument: %s\n" % a)
        sys.exit(2)
if has("--quiet"):
    pass

PROJECT = flag("--project")
if PROJECT and PROJECT != "fugaro-dev":
    sys.stderr.write("ERROR: (gcloud) this check runs on fugaro-dev only\n")
    sys.exit(1)


def emit(*lines):
    sys.stdout.write("\n".join(lines) + ("\n" if lines else ""))


if sub == ("auth", "print-identity-token"):
    emit("FAKE-IDENTITY-TOKEN")
elif sub == ("projects", "describe"):
    emit("123456789012")
elif sub == ("iam", "service-accounts", "list"):
    emit("fugaro-b-1@fugaro-dev.iam.gserviceaccount.com")
elif sub == ("storage", "buckets", "list"):
    if mode == "bucket-there" or state_get("bucket") == "1":
        emit("fugaro-dev-chk190-staging")
elif sub == ("storage", "buckets", "create"):
    if positionals[3:4] != ["gs://fugaro-dev-chk190-staging"]:
        sys.stderr.write("ERROR: (gcloud) this check creates its own staging bucket only\n")
        sys.exit(1)
    state_set("bucket", "1")
elif sub == ("storage", "buckets", "add-iam-policy-binding"):
    if positionals[3:4] == ["gs://fugaro-dev_cloudbuild"]:
        sys.stderr.write("ERROR: (gcloud) the installation's staging bucket is not this check's\n")
        sys.exit(1)
    if mode != "grants-ignored" and flag("--role") == "roles/storage.objectViewer" and state_get("bucket") == "1":
        state_set("bucketgrant", flag("--member"))
elif sub == ("storage", "rm"):
    if mode != "bucket-delete-fails" and positionals[2:3] == ["gs://fugaro-dev-chk190-staging"]:
        state_set("bucket", "0")
        state_set("bucketgrant", "0")
elif sub == ("secrets", "create"):
    data = flag("--data-file")
    if not data or not os.path.exists(data):
        sys.stderr.write("ERROR: (gcloud) no such file: %s\n" % data)
        sys.exit(1)
    state_set("secret", "1")
elif sub == ("secrets", "add-iam-policy-binding"):
    pass
elif sub == ("secrets", "list"):
    if state_get("secret") == "1":
        emit("projects/fugaro-dev/secrets/fugaro-chk190-token")
elif sub == ("secrets", "delete"):
    state_set("secret", "0")
elif sub == ("artifacts", "repositories", "create"):
    state_set("repo", "1")
elif sub == ("artifacts", "repositories", "list"):
    if state_get("repo") == "1":
        emit("projects/fugaro-dev/locations/us-east5/repositories/fugaro-chk190")
elif sub == ("artifacts", "repositories", "delete"):
    state_set("repo", "0")
    state_set("repogrant", "0")
elif sub == ("artifacts", "repositories", "add-iam-policy-binding"):
    if mode == "grant-fails":
        sys.stderr.write("ERROR: (gcloud.artifacts.repositories.add-iam-policy-binding) PERMISSION_DENIED\n")
        sys.exit(1)
    if mode != "grants-ignored" and flag("--role") == "roles/artifactregistry.writer" and state_get("repo") == "1":
        state_set("repogrant", flag("--member"))
elif sub == ("artifacts", "docker", "images", "delete"):
    pass
elif sub == ("builds", "submit"):
    # The real API's own refusal, which ended the owner's first real run: a
    # build that names its service account must say where its logs go, and the
    # default logs bucket is not an answer. --tag cannot say it (there is no
    # logging flag), so only --config with options.logging or --gcs-log-dir
    # can. --tag and --config are one mutually exclusive group, as in gcloud.
    if flag("--tag") and flag("--config"):
        sys.stderr.write("ERROR: (gcloud.builds.submit) argument --tag: At most one of --config | --pack | --tag can be specified.\n")
        sys.exit(2)
    logging = ""
    cfg = flag("--config")
    if cfg:
        if not os.path.exists(cfg):
            sys.stderr.write("ERROR: (gcloud.builds.submit) Unable to read file [%s]\n" % cfg)
            sys.exit(1)
        text = open(cfg).read()
        with open(os.path.join(LOG, "cloudbuild.yaml"), "w") as fh:
            fh.write(text)
        for word in ("CLOUD_LOGGING_ONLY", "NONE"):
            if "logging: " + word in text:
                logging = word
    if flag("--service-account") and not logging and not flag("--gcs-log-dir"):
        sys.stderr.write("ERROR: (gcloud.builds.submit) INVALID_ARGUMENT: invalid argument: if 'build.service_account' is specified, the build must either (a) specify 'build.logs_bucket', (b) use the REGIONAL_USER_OWNED_BUCKET default logs bucket, or (c) use CLOUD_LOGGING_ONLY / NONE logging.\n")
        sys.exit(1)
    # The source goes to this check's own bucket and nowhere else: without
    # --gcs-source-staging-dir gcloud would stage in (and create)
    # gs://<project>_cloudbuild, which is the installation's.
    staging = flag("--gcs-source-staging-dir")
    if not staging.startswith("gs://fugaro-dev-chk190-staging/") or state_get("bucket") != "1":
        sys.stderr.write("ERROR: (gcloud.builds.submit) the source must be staged in this check's own existing bucket, not %r\n" % staging)
        sys.exit(1)
    # The build runs as the named account, which can read the source and push
    # the image only through the two grants this check makes on its own bucket
    # and registry; the installation grants it neither.
    member = "serviceAccount:" + flag("--service-account").split("/serviceAccounts/")[-1]
    if mode == "build-fails":
        state_set("build", "FAILURE\tBuild step 0 \"gcr.io/cloud-builders/docker\" failed: step exited with non-zero status: 1")
    elif state_get("bucketgrant") != member:
        state_set("build", "FAILURE\t" + member + " does not have storage.objects.get access to the Google Cloud Storage object.")
    elif state_get("repogrant") != member:
        state_set("build", "FAILURE\tPermission \"artifactregistry.repositories.uploadArtifacts\" denied on resource")
    else:
        state_set("build", "SUCCESS\t")
    # The image's Dockerfile, kept so the deploy below can start (or not start)
    # the container it describes.
    src = positionals[-1] if len(positionals) > 2 else ""
    if os.path.exists(os.path.join(src, "Dockerfile")):
        with open(os.path.join(LOG, "Dockerfile"), "w") as fh:
            fh.write(open(os.path.join(src, "Dockerfile")).read())
    if has("--async"):
        emit("fake-build-1")
elif sub == ("builds", "describe"):
    emit(state_get("build"))
elif sub == ("builds", "cancel"):
    pass
elif sub == ("run", "deploy"):
    if mode == "deploy-fails":
        sys.stderr.write("ERROR: (gcloud.run.deploy) the deploy failed\n")
        sys.exit(1)
    # Like Cloud Run: the service is created first, then the revision must
    # start. A secret volume mounted over the image's entrypoint (the volume is
    # the directory of the --set-secrets path) is a container that never starts.
    state_set("svc", "1")
    mount = os.path.dirname(flag("--set-secrets").split("=")[0])
    entry = ""
    dockerfile = os.path.join(LOG, "Dockerfile")
    if os.path.exists(dockerfile):
        for line in open(dockerfile):
            if line.startswith("ENTRYPOINT"):
                entry = line.split('"')[1]
    over = bool(mount) and (entry == mount or entry.startswith(mount + "/"))
    if mode == "deploy-not-ready" or over:
        state_set("revfail", "1")
        sys.stderr.write("Deploying container to Cloud Run service [fugaro-chk190]\n")
        sys.stderr.write("Creating Revision... failed (auth Bearer FAKE-BEARER-VALUE)\n")
        sys.stderr.write("ERROR: (gcloud.run.deploy) Revision 'fugaro-chk190-00001-abc' is not ready and cannot serve traffic. The user-provided container failed to start.\n")
        sys.exit(1)
elif sub == ("run", "services", "list"):
    if state_get("svc") == "1":
        emit("fugaro-chk190")
elif sub == ("run", "services", "describe"):
    fmt = flag("--format")
    if "status.conditions" in fmt:
        if state_get("revfail") == "1":
            emit("status:", "  conditions:", "  - lastTransitionTime: '2026-10-30T00:00:00Z'",
                 "    message: Revision 'fugaro-chk190-00001-abc' is not ready and cannot serve traffic.",
                 "    reason: RevisionFailed", "    status: 'False'", "    type: Ready")
        else:
            emit("status:", "  conditions:", "  - status: 'True'", "    type: Ready")
    elif "latestCreatedRevisionName" in fmt:
        emit("fugaro-chk190-00001-abc")
    elif "status.url" in fmt:
        emit("https://fugaro-chk190-fake.a.run.app")
    else:
        emit("us-east5-docker.pkg.dev/fugaro-dev/fugaro-chk190/fugaro-chk190:chk190")
        emit("3600")
elif sub == ("run", "revisions", "describe"):
    if state_get("revfail") == "1":
        emit("status:", "  conditions:", "  - lastTransitionTime: '2026-10-30T00:00:00Z'",
             "    message: 'The user-provided container failed to start: exec /chk190: is a directory'",
             "    reason: ContainerMissing", "    status: 'False'", "    type: ContainerReady")
    else:
        emit("status:", "  conditions:", "  - status: 'True'", "    type: Ready")
elif sub == ("logging", "read"):
    if state_get("revfail") == "1" and 'revision_name="fugaro-chk190-00001-abc"' in positionals[2]:
        emit("chk190: the mounted token cannot be read from CHK190_TOKEN_FILE: read /chk190/token: is a directory")
    elif 'service_name="fugaro-chk190"' in positionals[2] and os.environ.get("FAKE_GIT_MODE") == "clone-401-upstream":
        # The proxy's own lines for the refused request, newest first. The
        # second is a line no proxy of this check's writes, there so the test
        # can see the excerpt redacts a credential whatever its source.
        emit("chk190 method=GET status=401 in=0 out=0 ms=80",
             "chk190 upstream refused method=GET status=401 path=/workspace-a/repo-a.git/info/refs challenge=Basic",
             "debug Authorization: Basic FAKE-BASIC-VALUE")
elif sub == ("run", "services", "get-iam-policy"):
    if mode == "public-invoker":
        emit('{"bindings": [{"role": "roles/run.invoker", "members": ["allUsers"]}]}')
    else:
        emit('{"bindings": [{"role": "roles/run.invoker", "members": ["serviceAccount:fugaro-b-1@fugaro-dev.iam.gserviceaccount.com"]}]}')
elif sub == ("run", "services", "delete"):
    if mode == "delete-fails":
        sys.stderr.write("ERROR: (gcloud.run.services.delete) could not delete\n")
        sys.exit(1)
    state_set("svc", "0")
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env python3
"""A stand-in for `git`, for scripts/live-checks/check190.sh's test only: no
socket is opened and no repository exists. It records every argument, the mode
of the config file the identity token is in, and the branch refs a push would
have made on the remote, so the Restore's branch delete can be asserted.
"""
import os, sys

STATE = os.environ["FAKE_GIT_STATE"]
LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GIT_MODE", "ok")
args = sys.argv[1:]

with open(os.path.join(LOG, "git.calls"), "a") as fh:
    fh.write("git " + " ".join(args) + "\n")

# The identity token arrives in an 0600 file named by GIT_CONFIG_GLOBAL: a token
# on a command line is visible in `ps` to every user of the machine for as long
# as the process lives, and the slow push would hold it there for an hour. The
# mode is logged so the test can assert the file really is 0600.
conf = os.environ.get("GIT_CONFIG_GLOBAL", "")
if conf:
    try:
        with open(os.path.join(LOG, "git.calls"), "a") as fh:
            fh.write("git config %s mode=%04o\n" % (conf, os.stat(conf).st_mode & 0o777))
    except OSError as exc:
        with open(os.path.join(LOG, "git.calls"), "a") as fh:
            fh.write("git config %s unreadable: %s\n" % (conf, exc))

sub, rest, i = None, [], 0
while i < len(args):
    a = args[i]
    if a == "-C":
        i += 2
        continue
    if a == "-c":
        i += 2
        continue
    if a.startswith("-"):
        i += 1
        continue
    sub, rest = a, args[i + 1:]
    break

positionals = [a for a in rest if not a.startswith("-")]


def branches():
    path = os.path.join(STATE, "git.branches")
    if not os.path.exists(path):
        return []
    return [b for b in open(path).read().split() if b]


def set_branches(bs):
    with open(os.path.join(STATE, "git.branches"), "w") as fh:
        fh.write("\n".join(bs))


def forwarded():
    """One forwarded request: what the proxy's own counters count, which is the
    requests it passes upstream and never its own /chk190/stats and
    /chk190/health."""
    path = os.path.join(STATE, "git.forwarded")
    n = int(open(path).read().strip()) if os.path.exists(path) else 0
    with open(path, "w") as fh:
        fh.write(str(n + 1))
    return n + 1


def pushes():
    path = os.path.join(STATE, "git.pushes")
    n = int(open(path).read().strip()) if os.path.exists(path) else 0
    with open(path, "w") as fh:
        fh.write(str(n + 1))
    return n + 1


def ref_of(name):
    if ":" in name:
        name = name.split(":", 1)[1]
    if not name.startswith("refs/"):
        name = "refs/heads/" + name
    return name


def trace(*exchanges):
    """What GIT_TRACE_CURL writes, one request and its response headers per
    exchange. GIT_TRACE_REDACT is deliberately NOT honoured here: the check's
    own excerpt must redact the credential headers itself, whatever git did."""
    path = os.environ.get("GIT_TRACE_CURL", "")
    if not path:
        return
    with open(path, "a") as fh:
        for request, response in exchanges:
            fh.write("12:00:00.000000 http.c:700              => Send header: %s\n" % request)
            fh.write("12:00:00.000000 http.c:700              => Send header: Host: fugaro-chk190-fake.a.run.app\n")
            fh.write("12:00:00.000000 http.c:700              => Send header: Authorization: Bearer FAKE-IDENTITY-TOKEN\n")
            for line in response:
                fh.write("12:00:00.000000 http.c:700              <= Recv header: %s\n" % line)


INFO_REFS = "GET /workspace-a/repo-a.git/info/refs?service=git-upload-pack HTTP/1.1"

if sub == "clone" and mode in ("clone-401-upstream", "clone-401-frontend"):
    forwarded()
    if mode == "clone-401-upstream":
        # Bitbucket's own refusal, forwarded by the proxy: a Basic challenge,
        # and the proxy's own mark that the answer came from upstream.
        trace((INFO_REFS, ["HTTP/1.1 401 Unauthorized", 'WWW-Authenticate: Basic realm="Bitbucket.org HTTP"',
                           "Set-Cookie: bb_session=FAKE-COOKIE-VALUE; Path=/", "X-Chk190-Upstream: 401"]))
    else:
        # The front end's own refusal: the request never reached the proxy, so
        # there is no mark of it.
        trace((INFO_REFS, ["HTTP/1.1 401 Unauthorized", 'WWW-Authenticate: Bearer error="invalid_token"',
                           "Server: Google Frontend"]))
    sys.stderr.write("Cloning into '/tmp/full'...\n")
    sys.stderr.write("fatal: could not read Username for 'https://fugaro-chk190-fake.a.run.app': terminal prompts disabled\n")
    sys.exit(128)
elif sub == "clone":
    forwarded()
    trace((INFO_REFS, ["HTTP/1.1 200 OK", "Content-Type: application/x-git-upload-pack-advertisement", "X-Chk190-Upstream: 200"]))
    if mode == "clone-fails":
        sys.stderr.write("fatal: unable to access the repository\n")
        sys.exit(128)
    dest = positionals[-1]
    os.makedirs(os.path.join(dest, ".git"), exist_ok=True)
elif sub == "ls-remote":
    forwarded()
    lspath = os.path.join(STATE, "git.lsremotes")
    lsn = (int(open(lspath).read().strip()) if os.path.exists(lspath) else 0) + 1
    with open(lspath, "w") as fh:
        fh.write(str(lsn))
    if mode == "coldstart-ls-fails" and lsn == 1:
        # The first git request after the idle wait, refused: the cold-start
        # row used to time it anyway and report the figure as a cold start.
        trace((INFO_REFS, ["HTTP/1.1 401 Unauthorized", 'WWW-Authenticate: Basic realm="Bitbucket.org HTTP"',
                           "X-Chk190-Upstream: 401"]))
        sys.stderr.write("fatal: could not read Username for 'https://fugaro-chk190-fake.a.run.app': terminal prompts disabled\n")
        sys.exit(128)
    if mode == "ls-remote-fails":
        # What an expired identity token looks like: the Restore runs an hour
        # after the token in GIT_CONF was minted.
        sys.stderr.write("fatal: could not read Username for 'https://bitbucket.org': terminal prompts disabled\n")
        sys.exit(128)
    for b in branches():
        sys.stdout.write("0000000000000000000000000000000000000000\t%s\n" % b)
elif sub == "push":
    forwarded()
    n = pushes()
    if "--delete" in rest:
        gone = [ref_of(p) for p in positionals]
        keep = [b for b in branches() if b not in gone]
        if mode == "delete-fails":
            keep = branches()
        set_branches(keep)
    elif n == 2 and mode == "bigpush-fails":
        sys.stderr.write("error: RPC failed; HTTP 413 curl 22 The requested URL returned error: 413\n")
        sys.stderr.write("error: failed to push some refs to 'https://fugaro-chk190-fake.a.run.app/workspace-a/repo-a.git'\n")
        sys.exit(128)
    elif n == 3:
        # The slow push: the GET of info/refs is answered through the proxy's
        # own route for it, and the receive-pack that follows is what the front
        # end holds and then ends — a 504 with no X-Chk190-Upstream, which is
        # the only place the check can read who ended the request (git reports
        # every failure of this kind in its own words).
        get_exchange = ("GET /chk190-slow/workspace-a/repo-a.git/info/refs?service=git-receive-pack HTTP/1.1",
                        ["HTTP/1.1 200 OK", "X-Chk190-Upstream: 200"])
        post_exchange = ("POST /chk190-slow/workspace-a/repo-a.git/git-receive-pack HTTP/1.1",
                         ["HTTP/1.1 200 OK", "X-Chk190-Upstream: 200"] if mode == "slow-completes"
                         else ["HTTP/1.1 504 Gateway Timeout", "Server: Google Frontend"])
        trace(get_exchange, post_exchange)
        if mode == "slow-completes":
            sys.exit(0)
        sys.stderr.write("error: RPC failed; HTTP 504 curl 22 The requested URL returned error: 504\n")
        sys.exit(128)
    else:
        set_branches(branches() + [ref_of(positionals[-1])])
elif sub == "commit":
    if mode == "commit-fails":
        sys.stderr.write("fatal: could not commit\n")
        sys.exit(1)
elif sub == "rev-parse":
    # The scratch directory is not inside a work tree, which is what the check's
    # own guard asks: a scratch directory inside a repository could be committed.
    sys.exit(1)
elif sub in ("checkout", "add", "config"):
    pass
GIT_EOF
  chmod +x "$1/bin/git"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env python3
"""A stand-in for `curl` (and for the Cloud Run front end), for
scripts/live-checks/check190.sh's test only: no socket is opened.

It answers the check's own two routes on the throwaway service -- /chk190/stats
and /chk190/health -- and refuses anything that carries no credential, which is
what the unauthenticated probe must see.
"""
import os, sys

LOG = os.environ["FAKE_LOG_DIR"]
STATE = os.environ["FAKE_CURL_STATE"]
mode = os.environ.get("FAKE_CURL_MODE", "ok")
args = sys.argv[1:]

with open(os.path.join(LOG, "curl.calls"), "a") as fh:
    fh.write("curl " + " ".join(args) + "\n")


def flag(name, default=""):
    if name in args:
        return args[args.index(name) + 1]
    for a in args:
        if a.startswith(name + "="):
            return a[len(name) + 1:]
    return default


conf = flag("--config")
conf_text = ""
if conf:
    try:
        conf_text = open(conf).read()
        with open(os.path.join(LOG, "curl.calls"), "a") as fh:
            fh.write("curl config %s mode=%04o\n" % (conf, os.stat(conf).st_mode & 0o777))
    except OSError as exc:
        with open(os.path.join(LOG, "curl.calls"), "a") as fh:
            fh.write("curl config %s unreadable: %s\n" % (conf, exc))
authed = "authorization:" in conf_text.lower()

url = [a for a in args if not a.startswith("-")][-1]
out = flag("-o")
fmt = flag("-w")
status, body = 200, ""


def counter(name):
    path = os.path.join(STATE, "curl." + name)
    n = int(open(path).read().strip()) if os.path.exists(path) else 0
    with open(path, "w") as fh:
        fh.write(str(n + 1))
    return n + 1


def forwarded():
    path = os.path.join(STATE, "git.forwarded")
    return int(open(path).read().strip()) if os.path.exists(path) else 0


if not authed:
    # Cloud Run with IAM required: no credential, no request. `public` is the
    # service a project-level allUsers/run.invoker binding would leave behind,
    # which the service's own get-iam-policy cannot show.
    if mode == "public":
        status, body = 200, "start=1 instance=chk190-1\n"
    else:
        status, body = 401, ""
elif mode == "idt-refused":
    # The front end refusing the identity token itself: an error page, which
    # is not empty, and which the counters must not be read out of.
    status, body = 401, "<html><body>Unauthorized</body></html>"
elif "/chk190/stats" in url:
    # The proxy counts the requests it forwards upstream, so the counters here
    # are the fake git's own forwarded requests and never this call itself.
    n = forwarded()
    body = "requests=%d in=%d out=%d" % (n, 4096 * n, 65536 * n)
elif "/chk190/health" in url:
    n = counter("health")
    if mode == "same-instance":
        n = 1
    body = "start=%d instance=chk190-%d" % (1000 * n, n)
else:
    status, body = 404, ""

if out and out != "/dev/null":
    with open(out, "w") as fh:
        fh.write(body)
if fmt:
    sys.stdout.write(fmt.replace("%{http_code}", str(status)))
elif not out or out == "/dev/null":
    sys.stdout.write(body)
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------- the runs
run190() { # dir extra-env...
  local dir=$1
  shift
  mkdir -p "$dir/state" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  : >"$dir/logs/git.calls"
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    FAKE_CURL_STATE="$dir/state" FAKE_GIT_STATE="$dir/state" \
    CHECK190_REPO=workspace-a/repo-a CHECK190_TOKEN_FILE="$dir/token" \
    CHECK190_IDLE_SECONDS=0 CHECK190_SLOW_SECONDS=3 CHECK190_BIG_MIB=1 \
    CHECK190_CURL_SECONDS=3 CHECK190_GRANT_SECONDS=0 CHECK190_BUILD_WAIT_SECONDS=0 CHECK190_LOG_WAIT_SECONDS=0 \
    ${1+"$@"} \
    bash "$script" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# run_copy DIR SCRIPT extra-env...: the same run, but against a copy of the
# script (the public-invoker case mutates the copy rather than the real file).
run_copy() { # dir script extra-env...
  local dir=$1 copy=$2
  shift 2
  mkdir -p "$dir/state" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  : >"$dir/logs/git.calls"
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    FAKE_CURL_STATE="$dir/state" FAKE_GIT_STATE="$dir/state" \
    CHECK190_REPO=workspace-a/repo-a CHECK190_TOKEN_FILE="$dir/token" \
    CHECK190_IDLE_SECONDS=0 CHECK190_SLOW_SECONDS=3 CHECK190_BIG_MIB=1 \
    CHECK190_CURL_SECONDS=3 CHECK190_GRANT_SECONDS=0 CHECK190_BUILD_WAIT_SECONDS=0 CHECK190_LOG_WAIT_SECONDS=0 \
    ${1+"$@"} \
    bash "$copy" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_fake_git "$1"
  write_fake_curl "$1"
  # The token file the owner writes with umask 077.
  (umask 077 && printf 'FAKE-BITBUCKET-TOKEN\n' >"$1/token")
}

# --------------------------------------------------------------- the tests
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run190 "$dir" CHECK190_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 190 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version"
  expect_contains "$out" "FACT project-region: PASS — fugaro-dev/us-east5" "dry run: the hard-coded project"
  expect_contains "$out" "FACT token-file: PASS" "dry run: the token file"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker fact"
  expect_contains "$out" "FACT restore: PASS — nothing of this check's was created" "dry run: nothing to delete"
  # The slow route, as the plan prints it: the proxy's own stall prefix on the
  # service, and the path the proxy strips it off to — the route the timeout row
  # is measured through, and the one the live run of 2026-10-30 got wrong.
  expect_contains "$err" "the slow push itself: POST https://<service host>/chk190-slow/workspace-a/repo-a.git/git-receive-pack" "dry run: the slow request goes through the proxy's own prefix"
  expect_contains "$err" "then forwarded to https://bitbucket.org/workspace-a/repo-a.git/git-receive-pack" "dry run: the path the proxy forwards the slow request with"
  expect_contains "$out" "CHECK 190 RESULT END (exit 0)" "dry run: the end line"

  # Nothing was created, built, deployed or deleted: only reads were made.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_absent "$calls" "secrets create" "dry run: no secret created"
  expect_absent "$calls" "repositories create" "dry run: no repository created"
  expect_absent "$calls" "builds submit" "dry run: no build"
  expect_absent "$calls" "run deploy" "dry run: no deploy"
  expect_absent "$calls" "delete" "dry run: nothing deleted"
  expect_absent "$(cat "$dir/logs/git.calls")" "push" "dry run: nothing pushed"
  [ -n "$scratch" ] || fail "dry run: the script did not say where its scratch directory is"
  if [ -d "$scratch" ]; then fail "dry run: the scratch directory $scratch survived"; fi
}

test_project_guard() {
  local dir=$work/other
  prepare "$dir"
  run190 "$dir" FAKE_FUGARO_MODE=other CHECK190_DRY_RUN=1
  expect_status "$status" 1 "project guard"
  expect_contains "$err" "not the fugaro-dev installation" "project guard: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "create" "project guard: nothing created"
  if [ -d "$scratch" ]; then fail "project guard: the scratch directory survived"; fi
}

# The token file is the one thing this check must never copy into a project
# without being sure of: a file another user can read is a token that has
# already leaked.
test_token_file_is_refused_unless_0600() {
  local dir=$work/tokenmode
  prepare "$dir"
  chmod 644 "$dir/token"
  run190 "$dir"
  expect_status "$status" 1 "token mode"
  expect_contains "$err" "not 0600" "token mode: the stop names the mode"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "create" "token mode: nothing created"

  local dir2=$work/notoken
  prepare "$dir2"
  rm -f "$dir2/token"
  run190 "$dir2"
  expect_status "$status" 1 "missing token file"
  expect_contains "$err" "not a regular file" "missing token file: the stop"
  expect_absent "$(cat "$dir2/logs/gcloud.calls")" "create" "missing token file: nothing created"

  local dir3=$work/norepo
  prepare "$dir3"
  run190 "$dir3" CHECK190_REPO=
  expect_status "$status" 1 "no CHECK190_REPO"
  expect_contains "$(cat "$dir3/err")" "CHECK190_REPO must be" "no CHECK190_REPO: the stop"
}

# THE RED STEP. The deploy line is the one place a throwaway service could be
# left answering the whole internet, and the token it attaches is a real one: a
# deploy without --no-allow-unauthenticated must be refused before anything is
# created. The case is a copy of the script with that line taken out, because
# the guard has to read the command line a real run would use and not a
# sentence about it.
test_a_deploy_line_without_no_allow_unauthenticated_is_refused() {
  local dir=$work/publicinv
  prepare "$dir"
  mkdir -p "$dir/copy"
  cp -r "$here/check190" "$dir/copy/check190"
  sed '/^  --no-allow-unauthenticated$/d' "$script" >"$dir/copy/check190.sh"
  run_copy "$dir" "$dir/copy/check190.sh"
  expect_status "$status" 1 "public invoker: the verdict"
  expect_contains "$err" "--no-allow-unauthenticated" "public invoker: the stop names the missing flag"
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_absent "$calls" "run deploy" "public invoker: nothing was deployed"
  expect_absent "$calls" "create" "public invoker: nothing was created"
}

# A knob the header advertises and nothing reads is worse than no knob at all: a
# reader -- or a test -- sets it, gets no effect, and gets no error either. The
# header is read up to the line that ends it, not to a fixed line number: this
# script's own header is longer than Check 91's, and a fixed number would stop
# before the knobs paragraph and check nothing.
test_every_advertised_knob_is_read() {
  local name reads knobs
  knobs=$(sed -n '1,/^set -euo pipefail/p' "$script" | grep -o 'CHECK190_[A-Z_]*' | sort -u)
  if [ -z "$knobs" ]; then
    fail "the header advertises no CHECK190_ knob at all, so this test reads nothing"
    return 0
  fi
  for name in $knobs; do
    # The READ, not a mention: every knob the header names appears in prose as
    # well, so counting mentions could never fire. `${NAME:-` is an expansion,
    # and an expansion cannot be a comment.
    reads=$(grep -cF "\${$name:-" "$script" || true)
    if [ "$reads" -lt 1 ]; then
      fail "$name is advertised in the header and read nowhere in the script (no \${$name:-...} expansion in it)"
    fi
  done
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run190 "$dir"
  expect_status "$status" 0 "happy path: the verdict"
  expect_contains "$out" "FACT clone-full: PASS" "happy path: the full clone"
  expect_contains "$out" "FACT clone-blobless: PASS" "happy path: the blobless clone"
  expect_contains "$out" "FACT push-small: PASS" "happy path: the small push"
  expect_contains "$out" "FACT push-big-http1: PASS" "happy path: the large push"
  expect_contains "$out" "FACT cold-start: PASS" "happy path: the cold start"
  # The row that cannot pass in a test: the front end's own 3600 s limit is what
  # it measures, and the knob shortens the stall to seconds. INFO is the honest
  # verdict for a run that ended early, and the row names its own cap.
  expect_contains "$out" "FACT timeout-3600: INFO" "happy path: the timeout row"
  expect_contains "$out" "FACT iam-policy: PASS" "happy path: the IAM read"
  expect_contains "$out" "FACT unauthenticated-request: PASS — an unauthenticated GET /chk190/health was refused with 401" "happy path: the unauthenticated probe"
  expect_contains "$out" "FACT branch-deleted: PASS" "happy path: the branch"
  expect_contains "$out" "FACT restore: PASS" "happy path: the Restore"
  expect_contains "$out" "FACT scratch-removed: PASS" "happy path: the scratch directory"
  expect_contains "$out" "CHECK 190 RESULT END (exit 0)" "happy path: the end line"

  # The token is in the secret container and nowhere else, and the identity
  # token is in a 0600 file rather than in an argument.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "secrets create fugaro-chk190-token --project=fugaro-dev --replication-policy=automatic --data-file=$dir/token --quiet" "happy path: the token reaches the secret by --data-file"
  expect_absent "$(cat "$dir/logs/git.calls" "$dir/logs/curl.calls" "$dir/logs/gcloud.calls" "$dir/out" "$dir/err")" "FAKE-IDENTITY-TOKEN" "happy path: the identity token is in no argument and no output"
  expect_absent "$(cat "$dir/logs/git.calls" "$dir/logs/curl.calls" "$dir/logs/gcloud.calls" "$dir/out" "$dir/err")" "FAKE-BITBUCKET-TOKEN" "happy path: the sandbox token is in no argument and no output"
  expect_contains "$(cat "$dir/logs/git.calls")" "mode=0600" "happy path: the git config file is 0600"
  expect_contains "$(cat "$dir/logs/curl.calls")" "mode=0600" "happy path: the curl config file is 0600"

  # The deploy line: IAM required, the timeout, one instance, and the token as a
  # mounted FILE rather than an environment variable.
  local deploy
  deploy=$(grep 'run deploy' "$dir/logs/gcloud.calls")
  expect_contains "$deploy" "--no-allow-unauthenticated" "happy path: the deploy requires IAM"
  expect_contains "$deploy" "--timeout=3600" "happy path: the deploy sets the timeout"
  expect_contains "$deploy" "--min-instances=0" "happy path: the deploy scales to zero"
  expect_contains "$deploy" "--max-instances=1" "happy path: the deploy keeps one instance"
  expect_contains "$deploy" "--set-secrets=/chk190/token=fugaro-chk190-token:latest" "happy path: the token is mounted as a file"
  expect_absent "$deploy" "CHK190_TOKEN=" "happy path: the token is in no environment variable"

  # The build names its own account, so it must also name where its logs go:
  # CLOUD_LOGGING_ONLY in a config file, as Fugaro's own image builds do.
  local build
  build=$(grep 'builds submit' "$dir/logs/gcloud.calls")
  expect_contains "$build" "--service-account=projects/fugaro-dev/serviceAccounts/fugaro-b-1@fugaro-dev.iam.gserviceaccount.com" "happy path: the build runs as the build account"
  expect_contains "$build" "--config=" "happy path: the build is a config file"
  expect_absent "$build" "--tag=" "happy path: the build is not --tag, which cannot set logging"
  expect_contains "$(cat "$dir/logs/cloudbuild.yaml")" "logging: CLOUD_LOGGING_ONLY" "happy path: the build logs to Cloud Logging only"
  expect_contains "$(cat "$dir/logs/cloudbuild.yaml")" "us-east5-docker.pkg.dev/fugaro-dev/fugaro-chk190/" "happy path: the build pushes the check's own image"
  expect_contains "$build" "--gcs-source-staging-dir=gs://fugaro-dev-chk190-staging/source/" "happy path: the source is staged in the check's own bucket"
  expect_contains "$build" "--async" "happy path: the build is read back by its status, not by streaming its log"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "builds describe fake-build-1" "happy path: the build's status was read back"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "_cloudbuild" "happy path: the installation's staging bucket is not touched"
  expect_contains "$out" "FACT build: PASS" "happy path: the build row"
  expect_contains "$out" "FACT deploy: PASS" "happy path: the deploy row"
  # The token's volume is mounted at /chk190, so the binary cannot live there.
  expect_absent "$(cat "$dir/logs/Dockerfile")" 'ENTRYPOINT ["/chk190' "happy path: the entrypoint is not under the secret's mount"

  # The check's own bucket and grants: made by it, on its own resources only.
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "storage buckets create gs://fugaro-dev-chk190-staging --project=fugaro-dev --location=us-east5 --uniform-bucket-level-access" "happy path: the bucket is the check's own, uniform access, in the region"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "artifacts repositories add-iam-policy-binding fugaro-chk190 --project=fugaro-dev --location=us-east5 --member=serviceAccount:fugaro-b-1@fugaro-dev.iam.gserviceaccount.com --role=roles/artifactregistry.writer" "happy path: writer on the check's own registry"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "storage buckets add-iam-policy-binding gs://fugaro-dev-chk190-staging --member=serviceAccount:fugaro-b-1@fugaro-dev.iam.gserviceaccount.com --role=roles/storage.objectViewer" "happy path: objectViewer on the check's own bucket"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "projects add-iam-policy-binding" "happy path: no project-wide grant"
  expect_contains "$out" "FACT staging-bucket: PASS" "happy path: the bucket row"
  expect_contains "$out" "FACT grants: PASS" "happy path: the grants row"
  expect_contains "$out" "FACT bucket-deleted: PASS" "happy path: the bucket is read back as gone"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "storage rm -r gs://fugaro-dev-chk190-staging" "happy path: the bucket deleted with its contents"

  # Everything this run created was deleted again.
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services delete fugaro-chk190" "happy path: the service deleted"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "repositories delete fugaro-chk190" "happy path: the repository deleted"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "secrets delete fugaro-chk190-token" "happy path: the secret deleted"
  if [ -d "$scratch" ]; then fail "happy path: the scratch directory survived"; fi
}

# The assertion the plan names: the service's IAM policy is read BEFORE any
# request is sent, and a public member in it is a stop -- not a warning, and not
# something the check discovers from a 200.
test_a_public_iam_policy_stops_before_any_request() {
  local dir=$work/publiciam
  prepare "$dir"
  run190 "$dir" FAKE_GCLOUD_MODE=public-invoker
  expect_status "$status" 1 "public IAM: the verdict"
  expect_contains "$err" "names a public member" "public IAM: the stop"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "get-iam-policy" "public IAM: the policy was read"
  expect_absent "$(cat "$dir/logs/git.calls")" "clone" "public IAM: no git request was sent"
  expect_absent "$(cat "$dir/logs/curl.calls")" "/chk190/health" "public IAM: no request was sent"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services delete" "public IAM: the service is still deleted"
}

# A push the front end does not cut is a FAIL and not a quiet INFO: the design
# assumes the 3600 s limit, and a push that completes after an hour says there is
# no such limit on this path.
test_a_slow_push_that_completes_is_a_failure() {
  local dir=$work/slowok
  prepare "$dir"
  run190 "$dir" FAKE_GIT_MODE=slow-completes
  expect_status "$status" 1 "slow push completed: the verdict"
  expect_contains "$out" "FACT timeout-3600: FAIL" "slow push completed: the row"
  expect_contains "$out" "did not end the request" "slow push completed: the reason"
}

# The slow push is the row the whole check exists for — what Cloud Run's front
# end does with a request held past its 3600 s timeout — and it goes through the
# prefix the proxy is DEPLOYED with. The route the check pushes through and the
# path the proxy strips off it before it forwards are asserted against each
# other here, because the live run of 2026-10-30 got neither: a path that loses
# its separator with the prefix is not a URL at all, so the proxy answered 502
# after 0 s and the hold was never measured while the row read as a figure.
test_the_slow_push_goes_through_the_deployed_stall_route() {
  local dir=$work/slowroute
  prepare "$dir"
  run190 "$dir"
  expect_status "$status" 0 "slow route: the verdict (a test cannot reach the front end's 3600 s limit)"

  # The prefix the check pushes through is the one the deploy line gives the
  # proxy, on the service's own host, over the repository every other step uses.
  expect_contains "$(grep 'run deploy' "$dir/logs/gcloud.calls")" "CHK190_STALL_PREFIX=/chk190-slow/" "slow route: the prefix the proxy is deployed with"
  local slow host
  slow=$(grep -o 'https://[^ ]*chk190-slow/[^ ]*' "$dir/logs/git.calls" | head -n 1)
  host=$(grep -o 'https://[^/]*/' "$dir/logs/git.calls" | head -n 1)
  expect_contains "$slow" "${host}chk190-slow/workspace-a/repo-a.git" "slow route: the push goes through the prefix the proxy was deployed with"

  # The path the proxy forwards the slow request with: the prefix off, the
  # repository's own path on. The check states it, so the block shows which path
  # Bitbucket is asked for.
  expect_contains "$err" "so Bitbucket sees https://bitbucket.org/workspace-a/repo-a.git" "slow route: the upstream path the slow route maps to"

  # And the row: this run's request was ended before any hold, so there is no
  # figure — the row has to say that, rather than read as a measurement of 0 s.
  expect_contains "$out" "FACT timeout-3600: INFO" "slow route: the row"
  expect_contains "$out" "not measured:" "slow route: the row says the hold was not measured"
  expect_contains "$out" "came from the front end or the proxy itself" "slow route: the row names what ended the request"
}

# The Restore deletes the branch this run pushed with the remote the pushes
# used, and it runs the delete from the clone: `git push` needs a repository to
# run in, and the scratch directory is deliberately not one. Run from it the
# delete dies with "fatal: not a git repository", which the Restore swallowed
# while the branch stayed on the sandbox repository.
test_the_branch_delete_runs_the_way_the_push_did() {
  local dir=$work/branchdel
  prepare "$dir"
  run190 "$dir"
  expect_status "$status" 0 "branch delete: the verdict"
  local name del
  name=${scratch##*.}
  [ -n "$name" ] || fail "branch delete: the script did not say where its scratch directory is"
  del=$(grep ' push .*--delete ' "$dir/logs/git.calls" | head -n 1)
  if [ -z "$del" ]; then
    fail "branch delete: the Restore attempted no delete at all"
    return 0
  fi
  expect_contains "$del" "git -C $scratch/full -c credential.helper= push" "branch delete: the delete runs in the clone, not in the scratch directory"
  expect_contains "$del" "https://fugaro-chk190-fake.a.run.app/workspace-a/repo-a.git --delete chk190/${name} chk190/${name}-slow" "branch delete: the remote the pushes used and the two branches they made"
  expect_contains "$out" "FACT branch-deleted: PASS" "branch delete: the row"
}

# A listing the Restore cannot read used to skip the delete altogether, so a
# branch this run pushed stayed on the sandbox repository. The delete is
# attempted whether or not the listing was read: the two names are this run's
# own, and deleting one that is not there is harmless. What the row claims is
# unchanged — a listing that fails is still not the answer "the branch is gone".
test_the_branch_delete_is_attempted_even_when_the_listing_fails() {
  local dir=$work/lsfaildelete
  prepare "$dir"
  run190 "$dir" FAKE_GIT_MODE=ls-remote-fails
  expect_status "$status" 1 "failed listing: the verdict"
  expect_contains "$(cat "$dir/logs/git.calls")" "push --quiet https://fugaro-chk190-fake.a.run.app/workspace-a/repo-a.git --delete chk190/" "failed listing: the delete was attempted anyway"
  expect_contains "$out" "FACT branch-deleted: FAIL" "failed listing: the row claims nothing"
  expect_absent "$out" "no branch named chk190/" "failed listing: the block does not claim the branch is gone"
}

# A large push the front end refuses is the gate this whole check exists for,
# and its row has to carry both halves: what the client was told, and whether the
# container saw the bytes.
test_a_refused_large_push_is_recorded_with_the_front_ends_answer() {
  local dir=$work/bigrefused
  prepare "$dir"
  run190 "$dir" FAKE_GIT_MODE=bigpush-fails
  # Exit 1: the row is a FAIL, and a FAILed fact must never come back as a pass.
  expect_status "$status" 1 "refused large push: the verdict"
  expect_contains "$out" "FACT push-big-http1: FAIL" "refused large push: the row"
  expect_contains "$out" "HTTP 413" "refused large push: the front end's answer"
  expect_contains "$out" "1 request(s)" "refused large push: the counters beside it"
}

# A resource the Restore could not delete is a FAIL, the scratch directory is
# kept, and the run does not come back reading as a pass.
test_restore_leftover() {
  local dir=$work/leftover
  prepare "$dir"
  run190 "$dir" FAKE_GCLOUD_MODE=delete-fails
  expect_status "$status" 1 "leftover: the verdict"
  expect_contains "$out" "FACT restore: FAIL" "leftover: the row"
  expect_contains "$out" "service there" "leftover: which count is not 0"
  expect_contains "$out" "FACT scratch-removed: FAIL" "leftover: the directory is kept"
  [ -n "$scratch" ] || fail "leftover: the script did not say where its scratch directory is"
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then
    kept+=("$scratch")
  else
    fail "leftover: the scratch directory was removed although a count was not 0"
  fi
}

# The Restore reads the branch back with a freshly minted identity token, and a
# listing that FAILS is not the answer "the branch is gone". That is the
# ordinary case rather than a corner: the token GIT_CONF holds was minted before
# the slow push, which is held open for an hour, so by the time the Restore runs
# every call answers 403 — and reading that as "gone" is how a branch carrying
# the large blob would stay on the sandbox repository while the block said it
# had been deleted.
test_a_branch_listing_that_fails_is_not_the_branch_is_gone() {
  local dir=$work/lsfail
  prepare "$dir"
  run190 "$dir" FAKE_GIT_MODE=ls-remote-fails
  expect_status "$status" 1 "failed branch listing: the verdict"
  expect_contains "$out" "FACT branch-deleted: FAIL — whether this run's branches" "failed branch listing: the row says the listing failed"
  expect_contains "$out" "cannot be read" "failed branch listing: the reason"
  expect_absent "$out" "no branch named chk190/" "failed branch listing: the block does not claim the branch is gone"
}

# The unauthenticated probe is a stop and not a row. A project-level allUsers or
# run.invoker binding does not appear in the service's own get-iam-policy, so
# this probe is the only thing that sees it — and a run that carried on would
# spend the next two hours cloning, pushing and holding a request open behind a
# public URL with the sandbox repository's token attached.
test_a_service_that_answers_unauthenticated_calls_stops_the_run() {
  local dir=$work/publicsvc
  prepare "$dir"
  run190 "$dir" FAKE_CURL_MODE=public
  expect_status "$status" 1 "public service: the verdict"
  expect_contains "$out" "FACT unauthenticated-request: FAIL — an unauthenticated GET /chk190/health answered 200" "public service: the row"
  expect_contains "$out" "FACT clone-full: FAIL — not reached" "public service: nothing was cloned"
  expect_absent "$(cat "$dir/logs/git.calls")" "clone" "public service: no git request was sent"
}

# The staging bucket is this check's own, and one of its name that was already
# there is a stop before anything is created: it is never adopted, written into
# or deleted.
test_a_staging_bucket_already_there_is_a_hard_stop() {
  local dir=$work/bucket
  prepare "$dir"
  run190 "$dir" FAKE_GCLOUD_MODE=bucket-there
  expect_status "$status" 1 "bucket already there: the verdict"
  expect_contains "$err" "a bucket named fugaro-dev-chk190-staging is already in fugaro-dev, and it is not this check's" "bucket already there: the stop"
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_absent "$calls" "create" "bucket already there: nothing was created"
  expect_absent "$calls" "storage rm" "bucket already there: the bucket is not deleted"
}

# The installation grants the build account nothing on what this check makes,
# so a build without the check's two grants is refused by Cloud Build, and the
# row says so rather than the run carrying on to a deploy.
test_a_build_without_the_grants_fails() {
  local dir=$work/nogrants
  prepare "$dir"
  run190 "$dir" FAKE_GCLOUD_MODE=grants-ignored
  expect_status "$status" 1 "no grants: the verdict"
  expect_contains "$out" "FACT build: FAIL" "no grants: the build row"
  expect_contains "$out" "storage.objects.get" "no grants: Cloud Build's own answer"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run deploy" "no grants: nothing was deployed"
  expect_contains "$out" "FACT bucket-deleted: PASS" "no grants: the bucket is still deleted"
  expect_contains "$out" "FACT restore: PASS" "no grants: the Restore"
}

test_a_grant_that_fails_stops_before_the_build() {
  local dir=$work/grantfails
  prepare "$dir"
  run190 "$dir" FAKE_GCLOUD_MODE=grant-fails
  expect_status "$status" 1 "grant fails: the verdict"
  expect_contains "$out" "FACT grants: FAIL" "grant fails: the row"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds submit" "grant fails: nothing was built"
  expect_contains "$out" "FACT restore: PASS" "grant fails: the Restore"
}

test_a_failed_build_is_read_from_its_status() {
  local dir=$work/buildfails
  prepare "$dir"
  run190 "$dir" FAKE_GCLOUD_MODE=build-fails
  expect_status "$status" 1 "failed build: the verdict"
  expect_contains "$out" "FACT build: FAIL — build fake-build-1" "failed build: the row names the build"
  expect_contains "$out" "step exited with non-zero status" "failed build: Cloud Build's failure detail"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run deploy" "failed build: nothing was deployed"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "builds cancel fake-build-1" "failed build: the Restore cancels the build"
}

# The owner's real run: the deploy created the service and then reported
# failure, and the block said nothing about why. The row now carries gcloud's
# own words (redacted) and the service's and revision's conditions.
test_a_deploy_that_fails_after_creating_the_service_says_why() {
  local dir=$work/deploynotready
  prepare "$dir"
  run190 "$dir" FAKE_GCLOUD_MODE=deploy-not-ready
  expect_status "$status" 1 "deploy not ready: the verdict"
  expect_contains "$out" "FACT deploy: FAIL — gcloud run deploy exited 1 after creating fugaro-chk190" "deploy not ready: the row"
  expect_contains "$out" "is not ready and cannot serve traffic. The user-provided container failed to start." "deploy not ready: gcloud's own error"
  expect_contains "$out" "reason: RevisionFailed" "deploy not ready: the service's condition"
  expect_contains "$out" "revision fugaro-chk190-00001-abc conditions:" "deploy not ready: the revision is named"
  expect_contains "$out" "exec /chk190: is a directory" "deploy not ready: the revision's condition"
  expect_contains "$out" "revision log: chk190: the mounted token cannot be read" "deploy not ready: the revision's own startup line"
  expect_absent "$out" "FAKE-BEARER-VALUE" "deploy not ready: a credential in gcloud's output is redacted"
  expect_contains "$err" "Deploying container to Cloud Run service" "deploy not ready: the deploy's output still reaches the terminal"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services delete fugaro-chk190" "deploy not ready: the Restore deletes the service"
  expect_contains "$out" "FACT restore: PASS" "deploy not ready: the Restore"
}

# The bucket is read back after its delete: a delete that did not take is a
# FAIL with the command to run by hand, and the scratch directory is kept.
test_a_bucket_left_behind_is_a_failure() {
  local dir=$work/bucketleft
  prepare "$dir"
  run190 "$dir" FAKE_GCLOUD_MODE=bucket-delete-fails
  expect_status "$status" 1 "bucket left: the verdict"
  expect_contains "$out" "FACT bucket-deleted: FAIL — gs://fugaro-dev-chk190-staging is still in fugaro-dev" "bucket left: the row"
  expect_contains "$out" "bucket there" "bucket left: the count that is not 0"
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then
    kept+=("$scratch")
  else
    fail "bucket left: the scratch directory was removed although a count was not 0"
  fi
}

# The owner's real run: the full clone failed with git's "could not read
# Username ... terminal prompts disabled", which is git's answer to ANY 401 and
# says neither who refused nor why. The row now carries the HTTP exchange git
# saw (status and the headers that tell the two refusals apart, every
# credential header redacted) and the proxy's own log lines.
test_a_clone_refused_by_bitbucket_says_who_refused() {
  local dir=$work/clone401up
  prepare "$dir"
  run190 "$dir" FAKE_GIT_MODE=clone-401-upstream
  expect_status "$status" 1 "upstream 401: the verdict"
  expect_contains "$out" "FACT clone-full: FAIL" "upstream 401: the row"
  expect_contains "$out" "could not read Username" "upstream 401: git's own words"
  expect_contains "$out" "HTTP/1.1 401 Unauthorized" "upstream 401: the status git saw"
  expect_contains "$out" 'Basic realm="Bitbucket.org HTTP"' "upstream 401: the challenge"
  expect_contains "$out" "X-Chk190-Upstream: 401" "upstream 401: the proxy's mark"
  expect_contains "$out" "Bitbucket refused the proxy's credential" "upstream 401: the reading"
  expect_contains "$out" "chk190 upstream refused method=GET status=401 path=/workspace-a/repo-a.git/info/refs challenge=Basic" "upstream 401: the proxy's own log line"
  local all
  all=$(cat "$dir/out" "$dir/err")
  expect_absent "$all" "FAKE-IDENTITY-TOKEN" "upstream 401: the identity token in git's trace is redacted"
  expect_absent "$all" "FAKE-COOKIE-VALUE" "upstream 401: a cookie in git's trace is redacted"
  expect_absent "$all" "FAKE-BASIC-VALUE" "upstream 401: a credential in the proxy's log is redacted"
  expect_absent "$all" "FAKE-BITBUCKET-TOKEN" "upstream 401: the sandbox token is nowhere"
}

test_a_clone_refused_by_the_front_end_says_who_refused() {
  local dir=$work/clone401fe
  prepare "$dir"
  run190 "$dir" FAKE_GIT_MODE=clone-401-frontend
  expect_status "$status" 1 "front-end 401: the verdict"
  expect_contains "$out" "FACT clone-full: FAIL" "front-end 401: the row"
  expect_contains "$out" 'Bearer error="invalid_token"' "front-end 401: the challenge"
  expect_contains "$out" "the Cloud Run front end refused the request before the proxy saw it" "front-end 401: the reading"
  expect_absent "$(cat "$dir/out" "$dir/err")" "FAKE-IDENTITY-TOKEN" "front-end 401: the identity token is redacted"
}

# The counters were read with `curl -sS` and taken as read when the body was
# not empty, so the front end's own error page passed for them. A refused
# identity token now stops the run with the HTTP status, before any git request.
test_counters_the_front_end_refuses_stop_the_run_with_the_status() {
  local dir=$work/idtrefused
  prepare "$dir"
  run190 "$dir" FAKE_CURL_MODE=idt-refused
  expect_status "$status" 1 "refused counters: the verdict"
  expect_contains "$out" "GET /chk190/stats answered HTTP 401" "refused counters: the status"
  expect_absent "$(cat "$dir/logs/git.calls")" "clone" "refused counters: no git request was sent"
}

# x-token-auth is Bitbucket's username for a repository, project or workspace
# ACCESS token only. An Atlassian API token or an app password pasted in its
# place is refused by Bitbucket with a 401 on the clone, after the build and
# the deploy; the prefix says which it is before anything is created.
test_a_token_that_is_not_an_access_token_is_refused() {
  local dir=$work/apitoken
  prepare "$dir"
  (umask 077 && printf 'ATATTxFAKE\n' >"$dir/token")
  run190 "$dir"
  expect_status "$status" 1 "API token: the verdict"
  expect_contains "$err" "Atlassian API token" "API token: the stop names the kind"
  expect_contains "$err" "access token" "API token: the stop names what to use"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "create" "API token: nothing created"
  expect_absent "$(cat "$dir/out" "$dir/err")" "ATATTxFAKE" "API token: the token is not printed"

  local dir2=$work/apppassword
  prepare "$dir2"
  (umask 077 && printf 'ATBBxFAKE\n' >"$dir2/token")
  run190 "$dir2"
  expect_status "$status" 1 "app password: the verdict"
  expect_contains "$(cat "$dir2/err")" "app password" "app password: the stop names the kind"
  expect_absent "$(cat "$dir2/logs/gcloud.calls")" "create" "app password: nothing created"
}

# The cold-start row timed the first git request after the idle wait and
# ignored its exit, so a refused request was reported as a cold-start figure.
test_a_refused_cold_start_request_is_a_failure() {
  local dir=$work/coldls
  prepare "$dir"
  run190 "$dir" FAKE_GIT_MODE=coldstart-ls-fails
  expect_status "$status" 1 "refused cold start: the verdict"
  expect_contains "$out" "FACT cold-start: FAIL — the first git request after" "refused cold start: the row"
  expect_contains "$out" "HTTP/1.1 401 Unauthorized" "refused cold start: the status git saw"
  expect_absent "$out" "FACT cold-start: PASS" "refused cold start: not a figure"
}

# This check is the sandbox installation's and nobody else's: the project is
# hard-coded, the doctor stop names it, and no executed command may ever name
# another installation's project.
test_never_belong() {
  local text calls cfg projects p
  [ -f "$work/happy/logs/gcloud.calls" ] || test_happy_path
  text=$(cat "$script")
  expect_contains "$text" "P=fugaro-dev" "never belong: the project is hard-coded"
  cfg=$(printf '%s\n' "$text" | grep -v '^[[:space:]]*#' | grep -n 'gcloud config' || true)
  if [ -n "$cfg" ]; then
    fail "never belong: the script runs a gcloud config command: $cfg"
  fi
  local outside
  outside=$(printf '%s\n' "$text" | grep -iv -e "never \`belong\`" -e "never belong" | grep -c "belong" || true)
  [ "$outside" = 0 ] || fail "never belong: 'belong' appears outside the lines that refuse it ($outside)"
  calls=$(cat "$work"/happy/logs/gcloud.calls "$work"/happy/logs/git.calls "$work"/happy/logs/curl.calls 2>/dev/null)
  expect_absent "$calls" "belong" "never belong: no command named it"
  projects=$(grep -o -- '--project=[^ ]*' "$work/happy/logs/gcloud.calls" | sort -u || true)
  [ -n "$projects" ] || fail "never belong: no gcloud --project argument was recorded, so this test checks nothing"
  for p in $projects; do
    [ "$p" = "--project=fugaro-dev" ] || fail "never belong: a gcloud command named $p"
  done
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each test works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one test alone (for a solo rerun of a
# flaky-looking one). Neither set: all run.
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

t test_dry_run
t test_project_guard
t test_token_file_is_refused_unless_0600
t test_a_deploy_line_without_no_allow_unauthenticated_is_refused
t test_every_advertised_knob_is_read
t test_happy_path
t test_a_public_iam_policy_stops_before_any_request
t test_a_slow_push_that_completes_is_a_failure
t test_the_slow_push_goes_through_the_deployed_stall_route
t test_the_branch_delete_runs_the_way_the_push_did
t test_the_branch_delete_is_attempted_even_when_the_listing_fails
t test_a_refused_large_push_is_recorded_with_the_front_ends_answer
t test_a_branch_listing_that_fails_is_not_the_branch_is_gone
t test_a_service_that_answers_unauthenticated_calls_stops_the_run
t test_a_staging_bucket_already_there_is_a_hard_stop
t test_a_build_without_the_grants_fails
t test_a_grant_that_fails_stops_before_the_build
t test_a_failed_build_is_read_from_its_status
t test_a_deploy_that_fails_after_creating_the_service_says_why
t test_a_bucket_left_behind_is_a_failure
t test_restore_leftover
t test_never_belong
t test_a_clone_refused_by_bitbucket_says_who_refused
t test_a_clone_refused_by_the_front_end_says_who_refused
t test_counters_the_front_end_refuses_stop_the_run_with_the_status
t test_a_token_that_is_not_an_access_token_is_refused
t test_a_refused_cold_start_request_is_a_failure

if [ "$failures" -ne 0 ]; then
  echo "check190_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check190_test.sh: ok"
