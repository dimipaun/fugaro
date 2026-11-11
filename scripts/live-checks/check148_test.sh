#!/usr/bin/env bash
# Tests for scripts/live-checks/check148.sh, against a fake gcloud, a fake
# fugaro and a fake Bitbucket on a loopback port: nothing here reaches the
# network, there is no cloud state, and no credential of anybody's is used. The
# consumer the suite's fixtures carry is a sentinel, and the assertion that it
# never reaches the block is one of the tests.
#
# The fake world is the drill's own subject: the fake `fugaro creds set` and the
# fake `gcloud secrets versions add` write the bundle DOCUMENT the check's
# fixture holds, and the fake Bitbucket's read mint answers out of that same
# document. So the stop step really does take the entry out of the bundle the
# mint reads, and a script that reported a stop without the write would be
# caught by the mint it then serves.
#
# Usage: bash scripts/live-checks/check148_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check148.sh
# The bash this suite runs the check with, resolved before any test narrows
# PATH: the case about a missing tool gives the run a PATH with one tool on it.
bash_bin=$(command -v bash)
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
pids=()
cleanup_all() {
  local pid
  for pid in ${pids[@]+"${pids[@]}"}; do kill "$pid" 2>/dev/null || true; done
  if [ -n "${CHECK148_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 700)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT ' | head -n 4 | sed 's/^/ ... last run: /')"
}

# The installation the whole suite runs against: a sandbox, never the owner's.
PROJECT=prj-sandbox-a
PROJECT_B=prj-sandbox-b
REGION=us-east5
CONTAINER=fugaro-credentials
REPO=acme/legacy
WORKSPACE=acme
JOB=fugaro-coordinator

# Every credential-shaped value in this suite is a sentinel: the consumer's key
# and secret, the identity token the fake gcloud prints, and every read token
# the fake Bitbucket mints. None of them may reach the block, and none may
# survive in a file the run left behind.
KEY_SENTINEL=KEY-SENTINEL-148-must-not-be-printed
SECRET_SENTINEL=SECRET-SENTINEL-148-must-not-be-printed
ROTATED_SENTINEL=ROTATED-SENTINEL-148-must-not-be-printed
SUCCESSOR_KEY_SENTINEL=SUCCESSORKEY-SENTINEL-148-must-not-be-printed
SUCCESSOR_SECRET_SENTINEL=SUCCESSORSECRET-SENTINEL-148-must-not-be-printed
IDTOKEN_SENTINEL=IDTOKEN-SENTINEL-148-must-not-be-printed
# A read token is minted with a random tail, so what the assertion looks for is
# this stem: the fake Bitbucket hands out tokens that all begin with it.
TOKEN_SENTINEL=READTOKEN-SENTINEL-148-must-not-be-printed

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(installation service read-consumer consumer-files repository confirm xtrace stop restore-consumer rotate old-secret u13 transfer restore)

# The arguments a case gives the check, which run148 empties between runs and
# default_args fills. Declared here so `${#RUN148_ARGS[@]}` is never an unset
# array.
RUN148_ARGS=()

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin" "$1/state"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check148.sh's test: it answers from $FAKE148_DIR and reaches
# nothing. The bundle document the stop drill writes is this suite's file, so
# the version the check adds really is the document it built, and the fake
# Bitbucket's mint reads the same file.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE148_DIR/calls"
bump_version() { printf '%s\n' "$(( $(cat "$FAKE148_DIR/version") + 1 ))" >"$FAKE148_DIR/version"; }
flag() { # flag NAME — the value of --NAME in ARGS
  local want="--$1" prev="" arg
  prev=""
  for arg in "${ARGS[@]}"; do
    if [ "$prev" = "$want" ]; then printf '%s' "$arg"; return 0; fi
    prev=$arg
  done
}
ARGS=("$@")
case "${1:-}" in
auth)
  [ "${2:-}" = print-identity-token ] || { printf 'fake gcloud: unexpected auth verb %s\n' "${2:-}" >&2; exit 64; }
  printf '%s\n' "$IDTOKEN_SENTINEL"
  ;;
secrets)
  case "${2:-}" in
  versions)
    case "${3:-}" in
    access)
      [ -s "$FAKE148_DIR/access.status" ] && exit "$(cat "$FAKE148_DIR/access.status")"
      cat "$FAKE148_DIR/bundle.yaml"
      ;;
    add)
      data=$(flag data-file)
      [ -n "$data" ] || { printf 'fake gcloud: versions add without --data-file\n' >&2; exit 64; }
      cp "$data" "$FAKE148_DIR/bundle.yaml"
      bump_version
      # The write above LANDS and the status is still non-zero: gcloud does
      # both, which is the partial write a case here is about.
      if [ -s "$FAKE148_DIR/add.status" ]; then exit "$(cat "$FAKE148_DIR/add.status")"; fi
      ;;
    disable)
      # Nothing to do: this suite's bundle is one file and the fake Bitbucket
      # reads the newest version's document, so disabling the version the stop
      # replaced has no state of its own to change. It is still recorded in
      # $FAKE148_DIR/calls, which is what the check's row is about.
      if [ -s "$FAKE148_DIR/disable.status" ]; then exit "$(cat "$FAKE148_DIR/disable.status")"; fi
      ;;
    *)
      printf 'fake gcloud: unknown versions verb %s\n' "${3:-}" >&2
      exit 64
      ;;
    esac
    ;;
  *)
    printf 'fake gcloud: unknown secrets verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
run)
  case "${2:-}" in
  jobs)
    case "${3:-}" in
    list) cat "$FAKE148_DIR/jobs.json" ;;
    describe)
      name=""
      prev=""
      for arg in "${ARGS[@]}"; do
        [ "$prev" = describe ] && name=$arg
        prev=$arg
      done
      [ -n "$name" ] || { printf 'fake gcloud: describe without a job\n' >&2; exit 64; }
      cat "$FAKE148_DIR/job.json"
      ;;
    *)
      printf 'fake gcloud: unknown jobs verb %s\n' "${3:-}" >&2
      exit 64
      ;;
    esac
    ;;
  *)
    printf 'fake gcloud: unknown run verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake gcloud: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
GCLOUD_EOF

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check148.sh's test: doctor's answer, the bundle's listing
# and the bundle's only writer. `creds set` rewrites the entry in the same
# document the fake Bitbucket's mint reads, so the drill's writes are what the
# mint answers from.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE148_DIR/calls"
case "${1:-}" in
doctor)
  cat "$FAKE148_DIR/doctor.json"
  # The real doctor prints its whole --json document and THEN exits non-zero
  # whenever any ordinary check fails, so a case can ask for that by hand.
  exit "${FAKE_DOCTOR_STATUS:-0}"
  ;;
creds)
  case "${2:-}" in
  ls)
    python3 "$FAKE148_DIR/bundle.py" ls
    ;;
  set)
    name=${3:-}
    [ "$name" = bitbucket-read-consumer ] || { printf 'fake fugaro: creds set %s is not this check'"'"'s entry\n' "$name" >&2; exit 64; }
    body=$(mktemp "$FAKE148_DIR/set.XXXXXX")
    cat >"$body"
    python3 "$FAKE148_DIR/bundle.py" set "$body"
    rm -f "$body"
    # The entry is written above and the status is still non-zero: the partial
    # write internal/cli/creds.go names, which a case here is about.
    if [ -s "$FAKE148_DIR/set.status" ]; then exit "$(cat "$FAKE148_DIR/set.status")"; fi
    ;;
  *)
    printf 'fake fugaro: unknown creds verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF

  cat >"$1/state/bundle.py" <<'BUNDLE_EOF'
#!/usr/bin/env python3
"""The bundle document this suite's fakes share.

`set` is `fugaro creds set`'s modify half for the read consumer's entry, and
`ls` is the listing the check reads: the container, the newest version and
whether the document holds the entry. The document is edited in place, so the
other entries keep their bytes -- which is what the real writer does, and what
the check's own strip has to leave alone.
"""
import json, os, sys

DIR = os.environ["FAKE148_DIR"]
ENTRY = "bitbucket-read-consumer"


def read():
    with open(os.path.join(DIR, "bundle.yaml"), encoding="utf-8") as fh:
        return fh.read()


def without_entry(raw):
    out, skipping = [], False
    for line in raw.splitlines(keepends=True):
        if line.startswith(ENTRY + ":"):
            skipping = True
            continue
        if skipping:
            if not line.strip() or line[:1].isspace():
                continue
            skipping = False
        out.append(line)
    return "".join(out)


def holds_entry(raw):
    return any(line.startswith(ENTRY + ":") for line in raw.splitlines())


def cmd_set(path):
    with open(path, encoding="utf-8") as fh:
        value = fh.read()
    if not value.endswith("\n"):
        value += "\n"
    body = "".join("  " + line for line in value.splitlines(keepends=True))
    raw = without_entry(read())
    with open(os.path.join(DIR, "bundle.yaml"), "w", encoding="utf-8") as fh:
        fh.write(raw + ENTRY + ":\n" + body)
    version = int(open(os.path.join(DIR, "version"), encoding="utf-8").read().strip())
    with open(os.path.join(DIR, "version"), "w", encoding="utf-8") as fh:
        fh.write("%d\n" % (version + 1))


def cmd_ls():
    raw = read()
    version = open(os.path.join(DIR, "version"), encoding="utf-8").read().strip()
    rows = [{"name": ENTRY, "present": holds_entry(raw), "changed": "2026-11-10T09:00:00Z"},
            {"name": "github-app-key", "present": True, "changed": "2026-11-01T09:00:00Z"}]
    print(json.dumps({"project": os.environ["FAKE148_PROJECT"], "secret": os.environ["FAKE148_SECRET"],
                      "version": version, "changed": "2026-11-10T09:00:00Z", "credentials": rows}))


if sys.argv[1] == "set":
    cmd_set(sys.argv[2])
else:
    cmd_ls()
BUNDLE_EOF

  cat >"$1/fake.py" <<'FAKE_EOF'
#!/usr/bin/env python3
"""A fake Bitbucket for check148.sh's test: the read mint, and the two roots the
rotation's readings go to.

The mint is served from the bundle document the fakes share, so taking the entry
out of the bundle is what stops it; and the mode file says what else this
"Bitbucket" does -- whether the replaced secret is still accepted, whether the
token minted before the rotation still reads, and how long the document is
cached, which is this fake's stand-in for credsvc.BundleCacheTTL (30 seconds
there, N mint calls here, so a test can drive the wait without waiting).
"""
import http.server, json, os, secrets, sys

DIR = os.environ["FAKE148_DIR"]
ENTRY = "bitbucket-read-consumer"
# The sentence internal/credsvc's read mint answers with when the bundle holds no
# read consumer (bitbucket.go's ErrReadNoConsumer, carried as the wire body's
# message). The fake writes it on the absent-consumer answer, so the drill's stop
# row is held to the real contract and not only to the real status: the service
# answers the kill switch, a bundle that fails to load, a failed Bitbucket
# exchange and a failed audit with that same 503 state_unavailable.
NO_CONSUMER_MESSAGE = "credsvc: the credentials bundle holds no Bitbucket read consumer"
# And the other 503 state_unavailable a mint can answer: the exchange with
# bitbucket.org refusing, which is what a bundle holding a secret Bitbucket will
# not exchange gets (bitbucket.go's ErrReadBitbucket). The two are different
# readings of the same status and type, and the section's stop step turns on
# telling them apart.
NO_TOKEN_MESSAGE = "credsvc: bitbucket.org did not mint a read token"


def mode():
    try:
        with open(os.path.join(DIR, "mode.json"), encoding="utf-8") as fh:
            return json.load(fh)
    except OSError:
        return {}


def bundle():
    with open(os.path.join(DIR, "bundle.yaml"), encoding="utf-8") as fh:
        return fh.read()


def holds_entry():
    return any(line.startswith(ENTRY + ":") for line in bundle().splitlines())


def consumer_field(name):
    inside, out = False, None
    for line in bundle().splitlines():
        if line.startswith(ENTRY + ":"):
            inside = True
            continue
        if inside:
            if not line[:1].isspace():
                break
            field, _, value = line.strip().partition(":")
            if field == name:
                out = value.strip().strip('"')
    return out


def consumer_secret():
    return consumer_field("secret")


def acceptable():
    """The consumer pairs this Bitbucket would exchange, which is what the owner
    has at Bitbucket and nothing else: its OAuth endpoint is asked with the
    bundle's key and secret on every mint, and a pair it does not hold is
    refused. The suite writes this file from its own consumer documents and
    flips it at the moment the owner's regeneration lands -- a secret that has
    been written into the bundle but not regenerated at Bitbucket is a pair this
    Bitbucket refuses, exactly as bitbucket.org would."""
    try:
        with open(os.path.join(DIR, "bitbucket.json"), encoding="utf-8") as fh:
            return set(json.load(fh).get("accepted") or [])
    except (OSError, ValueError):
        return set()


def serving_state():
    """What one mint does with the bundle's consumer right now. Three states, and
    the third is the one a check must not read as the second: no entry at all
    (the stop), an entry whose pair Bitbucket exchanges (the credential is live),
    and an entry whose pair it refuses -- a secret regenerated at Bitbucket and
    not yet written, or written and not yet regenerated."""
    if not holds_entry():
        return "absent"
    pair = "%s:%s" % (consumer_field("key") or "", consumer_secret() or "")
    return "ok" if pair in acceptable() else "refused"


def tokens():
    try:
        with open(os.path.join(DIR, "tokens"), encoding="utf-8") as fh:
            return [line.strip() for line in fh if line.strip()]
    except OSError:
        return []


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    cache = None
    cache_left = 0

    def log_message(self, *args):
        pass

    def answer(self, status, doc):
        body = json.dumps(doc).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length)
        m = mode()
        if self.path == "/v1/bitbucket/read-token":
            self.mint(body, m)
        elif self.path == "/site/oauth2/access_token":
            self.grant(body, m)
        else:
            self.answer(404, {"error": "not_found"})

    def mint(self, body, m):
        if not (self.headers.get("Authorization") or "").startswith("Bearer "):
            self.answer(401, {"error": "unauthorized"})
            return
        try:
            repo = json.loads(body.decode("utf-8")).get("repository")
        except (ValueError, UnicodeDecodeError):
            self.answer(400, {"error": "invalid_request"})
            return
        # The document is read through the fake's cache, which is what the real
        # service's BundleCacheTTL is: a mint taken right after a write can be
        # the OLD document's answer, and a check that trusted it would report a
        # stop that had not happened.
        if Handler.cache is None or Handler.cache_left <= 0:
            Handler.cache = serving_state()
            Handler.cache_left = int(m.get("mint_cache") or 0)
        Handler.cache_left -= 1
        present = Handler.cache
        if m.get("mint") == "always":
            present = "ok"
        if m.get("mint") == "never":
            present = "absent"
        # A 200 that is not the mint's document at all, for the bundle whose
        # consumer carries this secret: a proxy, a load balancer or anything else
        # in front of the service answering instead of it. A row that read the
        # status alone would call it the new credential being served.
        if m.get("mint_junk_for") and consumer_secret() == m["mint_junk_for"]:
            if m.get("mint_junk_body") == "json":
                self.answer(200, {"repository": repo})
                return
            body = b"<html>the read mint is not here</html>"
            self.send_response(200)
            self.send_header("Content-Type", "text/html")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if present == "refused":
            # The exchange failing: the service presents the bundle's key and
            # secret at Bitbucket's OAuth root on every mint and answers
            # ErrReadBitbucket's own sentence when bitbucket.org will not mint
            # from them (internal/credsvc/bitbucket.go, through refuseRead). It
            # is the same 503 state_unavailable as the missing consumer's, so
            # neither the status nor the code tells the two apart -- a bundle
            # holding a secret Bitbucket has not issued is refused here, and a
            # drill that ran the baseline on one would have had nothing to
            # measure.
            self.answer(int(m.get("mint_refused_status") or 503),
                        {"error": m.get("mint_refused_error") or "state_unavailable",
                         "message": m.get("mint_refused_message") or NO_TOKEN_MESSAGE})
            return
        if present == "absent":
            # The service's own refusal of a bundle with no read consumer: 503
            # state_unavailable (internal/credsvc/bitbucket.go's ErrReadNoConsumer
            # through refuseRead, wire.go's statuses) carrying that error's own
            # sentence, which is the only thing that tells it apart from the
            # service's other 503 state_unavailable answers. A case can answer
            # something else — a 5xx, another message, or nothing at all — which
            # is the run not knowing rather than the refusal a stop is read on.
            absent = m.get("mint_absent") or ""
            if absent == "none":
                self.close_connection = True
                self.wfile.flush()
                return
            body = {"error": m.get("mint_absent_error") or "state_unavailable"}
            message = m.get("mint_absent_message")
            if message is None:
                message = NO_CONSUMER_MESSAGE
            if message != "none":
                body["message"] = message
            self.answer(int(m.get("mint_absent_status") or 503), body)
            return
        token = "%s-%s" % ("READTOKEN-SENTINEL-148-must-not-be-printed", secrets.token_hex(4))
        with open(os.path.join(DIR, "tokens"), "a", encoding="utf-8") as fh:
            fh.write(token + "\n")
        self.answer(200, {"token": token, "expires_at": "2026-11-10T18:00:00Z", "repository": repo})

    def grant(self, body, m):
        # No answer at all: the endpoint dropped the connection, which is the
        # reading a row must not take for the endpoint refusing the secret.
        if m.get("grant_absent") == "none":
            self.close_connection = True
            self.wfile.flush()
            return
        fields = {}
        for pair in body.decode("utf-8", "replace").split("&"):
            name, _, value = pair.partition("=")
            fields[name] = value
        user = (self.headers.get("Authorization") or "")
        if fields.get("grant_type") != "client_credentials" or not user.startswith("Basic "):
            self.answer(400, {"error": "unsupported_grant_type"})
            return
        import base64
        try:
            decoded = base64.b64decode(user.split(" ", 1)[1]).decode("utf-8")
        except Exception:
            self.answer(401, {"error": "invalid_client"})
            return
        key, _, secret = decoded.partition(":")
        live = consumer_secret()
        if m.get("grant") == "all" or (live and secret == live):
            self.answer(200, {"access_token": "granted-%s" % secrets.token_hex(4), "token_type": "bearer"})
        else:
            self.answer(401, {"error": "invalid_client"})

    def do_GET(self):
        m = mode()
        if not self.path.startswith("/2.0/repositories/"):
            self.answer(404, {"error": "not_found"})
            return
        # And the same for the repository read U13 is measured with: an answer
        # there is none of, which is not the API refusing the token.
        if m.get("u13") == "none":
            self.close_connection = True
            self.wfile.flush()
            return
        token = (self.headers.get("Authorization") or "").removeprefix("Bearer ")
        if token and token in tokens() and m.get("u13", "live") == "live":
            self.answer(200, {"full_name": self.path.split("/2.0/repositories/", 1)[1]})
        else:
            self.answer(401, {"error": "invalid_token"})


def main():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    print("fake148 listening on 127.0.0.1:%d" % server.server_address[1], flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
FAKE_EOF
  chmod +x "$1/bin/gcloud" "$1/bin/fugaro"
}

# ---------------------------------------------------------------- the fixtures
write_doctor() { # dir PROJECT [NAME] [REGION]
  printf '{"ok": true, "checks": [], "project": {"name": "%s", "gcp_project": "%s", "region": "%s"}}\n' \
    "${3:-sandbox-a}" "$2" "${4:-$REGION}" >"$1/state/doctor.json"
}

# write_job dir [URL] — the coordinator job's document, with the credentials
# service's own address first. JOB_NAME and JOB_LABEL say what else its metadata
# carries, so a case can take its label away.
write_job() {
  local dir=$1 url=${2:-}
  JOB_URL="$url" JOB_NAME="${JOB_NAME:-$JOB}" JOB_LABEL="${JOB_LABEL-coordinator}" \
    python3 - "$dir/state/job.json" "$dir/state/jobs.json" <<'PY'
import json, os, sys

env = []
if os.environ["JOB_URL"]:
    env.append({"name": "FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL", "value": os.environ["JOB_URL"]})
labels = {}
if os.environ["JOB_LABEL"]:
    labels["fugaro_role"] = os.environ["JOB_LABEL"]
job = {"metadata": {"name": os.environ["JOB_NAME"], "labels": labels},
       "spec": {"template": {"template": {"containers": [{"env": env}]}}}}
open(sys.argv[1], "w", encoding="utf-8").write(json.dumps(job))
open(sys.argv[2], "w", encoding="utf-8").write(json.dumps([job]))
PY
}

# write_consumer FILE KEY SECRET WORKSPACE — one consumer file, in the shape
# `fugaro creds set` reads: the mapping of exactly key, secret and workspace.
write_consumer() {
  printf 'key: "%s"\nsecret: "%s"\nworkspace: "%s"\n' "$2" "$3" "$4" >"$1"
  chmod 600 "$1"
}

# write_bundle FILE — the bundle the sandbox's version holds: three entries, one
# of them the read consumer's mapping. It carries the sentinels on purpose: this
# is the document the stop drill rewrites, and the one the mint reads.
write_bundle() {
  local file=$1 secret=${2:-$SECRET_SENTINEL}
  cat >"$file" <<EOF
model-key: |-
  MODEL-SENTINEL-148-must-not-be-printed
bitbucket-read-consumer:
  key: "$KEY_SENTINEL"
  secret: "$secret"
  workspace: "$WORKSPACE"
github-app-key: |-
  PEM-SENTINEL-148-must-not-be-printed
EOF
  chmod 600 "$file"
}

write_mode() { # dir [JSON] — the fake Bitbucket's behaviour
  local json=${2:-'{}'}
  printf '%s\n' "$json" >"$1/state/mode.json"
}

# doc_field FILE NAME — one field of a consumer document, as the fake Bitbucket
# reads it out of the bundle.
doc_field() {
  sed -n "s/^$2:[[:space:]]*\"\{0,1\}\([^\"]*\)\"\{0,1\}$/\1/p" "$1"
}

# pair_of FILE — the pair a consumer document names, in the shape the fake's
# OAuth exchange matches on. The values never leave this suite's own state
# directory: they are its fixtures, and that none of the sentinels reaches the
# run's block is one of the tests.
pair_of() {
  printf '%s:%s' "$(doc_field "$1" key)" "$(doc_field "$1" secret)"
}

# write_bitbucket DIR — what this case's Bitbucket would exchange: the pair the
# bundle holds now, and the successor the transfer creates. It is the fake's own
# OAuth state and it is deliberately NOT derived from the bundle: a mint that
# answered on the entry's presence alone would serve a consumer whose secret
# bitbucket.org has already regenerated away, and that is exactly the reading
# the drill's baseline row would then report as the live credential.
write_bitbucket() {
  local dir=$1 pairs
  pairs=$(printf '"%s", "%s"' "$(pair_of "$dir/state/consumer.yaml")" "$(pair_of "$dir/state/successor.yaml")")
  printf '{"accepted": [%s]}\n' "$pairs" >"$dir/state/bitbucket.json"
}

# bitbucket_rotated DIR — the owner has regenerated the secret of the consumer
# the bundle holds, which is the moment a rotation leaves the pre-rotation
# secret dead: from here Bitbucket exchanges the consumer's key with the secret
# the case's rotated document carries, and the old secret is refused. A drill
# that wrote the rotated document without asking for that regeneration would
# therefore be writing a secret this Bitbucket has never issued.
bitbucket_rotated() {
  local dir=$1 pairs
  pairs=$(printf '"%s:%s", "%s"' "$(doc_field "$dir/state/consumer.yaml" key)" \
    "$(doc_field "$dir/state/rotated.yaml" secret)" "$(pair_of "$dir/state/successor.yaml")")
  printf '{"accepted": [%s]}\n' "$pairs" >"$dir/state/bitbucket.json"
}

# claim_entry_present DIR — the listing the check reads says the bundle holds the
# read consumer, whatever the document says, and the mint keeps serving. It is
# how a case reaches the stop drill with a document the check cannot strip an
# entry out of: the two reads disagreeing is exactly what the count is for.
claim_entry_present() {
  local dir=$1
  printf '{"project": "%s", "secret": "%s", "version": "9", "changed": "2026-11-10T09:00:00Z", "credentials": [{"name": "bitbucket-read-consumer", "present": true, "changed": "x"}]}\n' \
    "${RUN148_PROJECT:-$PROJECT}" "$CONTAINER" >"$dir/state/creds.json"
  cat >"$dir/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE148_DIR/calls"
case "${1:-}" in
doctor) cat "$FAKE148_DIR/doctor.json" ;;
creds) cat "$FAKE148_DIR/creds.json" ;;
*) printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2; exit 64 ;;
esac
FUGARO_EOF
  chmod +x "$dir/bin/fugaro"
}

# strip_entry FILE — the document with the read consumer's entry taken out, so a
# case can hand the check a document whose listing and whose bytes disagree.
strip_entry() {
  python3 - "$1" <<'PY'
import sys
lines = [l for l in open(sys.argv[1], encoding="utf-8").read().splitlines(keepends=True)
         if not l.startswith("bitbucket-read-consumer") and not l[:1].isspace()]
open(sys.argv[1], "w", encoding="utf-8").write("".join(lines))
PY
}

# setup_case NAME — a fresh case: the fakes, a sandbox installation, a
# coordinator job carrying the service's address, a bundle holding the read
# consumer, the three consumer files, and a fake Bitbucket that serves the mint
# out of that bundle and accepts only the rotated secret. A case changes what it
# is about.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  printf '7\n' >"$dir/state/version"
  : >"$dir/state/tokens"
  write_doctor "$dir" "$PROJECT"
  write_bundle "$dir/state/bundle.yaml"
  write_mode "$dir"
  write_consumer "$dir/state/consumer.yaml" "$KEY_SENTINEL" "$SECRET_SENTINEL" "$WORKSPACE"
  write_consumer "$dir/state/rotated.yaml" "$KEY_SENTINEL" "$ROTATED_SENTINEL" "$WORKSPACE"
  write_consumer "$dir/state/successor.yaml" "$SUCCESSOR_KEY_SENTINEL" "$SUCCESSOR_SECRET_SENTINEL" "$WORKSPACE"
  write_bitbucket "$dir"
  start_fake "$dir"
  write_job "$dir" "http://127.0.0.1:$FAKE_PORT"
  assert_fakes_first "$dir" "the case at hand"
}

FAKE_PORT=""
FAKE_PID=""
start_fake() { # start_fake DIR
  FAKE148_DIR="$1/state" python3 "$1/fake.py" >"$1/state/fake.out" 2>"$1/state/fake.err" &
  FAKE_PID=$!
  pids+=("$FAKE_PID")
  local i=0
  while [ "$i" -lt 200 ]; do
    FAKE_PORT=$(sed -n 's/^fake148 listening on 127\.0\.0\.1:\([0-9]\{1,\}\)$/\1/p' "$1/state/fake.out" | head -n 1)
    [ -n "$FAKE_PORT" ] && return 0
    kill -0 "$FAKE_PID" 2>/dev/null || { fail "the fake Bitbucket died: $(tail -n 3 "$1/state/fake.err" | tr '\n' ' ')"; return 1; }
    sleep 0.05
    i=$((i + 1))
  done
  fail "the fake Bitbucket did not report that it is listening"
  return 1
}

# answers_for DIR — the stream the check reads its answers from. RUN148_STDIN
# names a file the case wrote; otherwise the answers are produced as the drill
# asks for them, which is what a case about the rotation needs.
answers_for() {
  if [ -n "${RUN148_STDIN:-}" ]; then
    cat "$RUN148_STDIN"
    return 0
  fi
  owner_answers "$1" "${RUN148_CONFIRM:-}"
}

# owner_answers DIR [CONFIRMATION] — the two answers the drill reads, in the
# order it reads them, and the one place this suite plays the owner.
#
# The confirmation, when the case has one, is written at once (a run under --yes
# reads none). The rotation's answer is NOT: it waits for the drill's own prompt
# for it, and the moment that prompt appears is the moment this case's Bitbucket
# regenerates the consumer's secret -- the owner's half of a rotation, and the
# half no flag can do for them. So the flipped Bitbucket and the answer are the
# same event, which is what makes a drill that wrote the rotated document
# without asking visible: it writes a secret Bitbucket has not issued, and the
# mint refuses it.
owner_answers() {
  local dir=$1 confirm=${2:-} i=0
  if [ -n "$confirm" ]; then printf '%s\n' "$confirm"; fi
  while [ "$i" -lt 1500 ]; do
    if grep -q 'regenerate the secret of the read consumer' "$dir/err" 2>/dev/null; then
      bitbucket_rotated "$dir"
      printf 'ROTATED\n'
      return 0
    fi
    # A case that ends before the rotation leaves this producer with nothing to
    # answer, so the run's own end is what releases it rather than a timeout.
    if [ -e "$dir/state/done" ]; then return 0; fi
    sleep 0.02
    i=$((i + 1))
  done
}

# run148 [ARGS...] — runs the check on the case at hand. RUN148_ARGS adds
# arguments, RUN148_ENV names environment variables (NAME=value), RUN148_STDIN
# is a file of answers and RUN148_CONFIRM is the typed confirmation for a case
# that does not pass --yes. The fakes are FIRST on PATH, and BOTH
# Bitbucket roots -- the OAuth root and the API root, which the check would
# otherwise default to the real Bitbucket -- point at this case's fake, so
# nothing here leaves the machine. RUN148_ENV comes last: a case about an
# override names its own.
#
# CHECK148_CACHE_WAIT is 0 here: the wait is the real service's 30-second bundle
# cache, and this fake's stand-in for it is a call count rather than a clock, so
# every case would only sit out the real wait. The case about the wait sets its
# own.
run148() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE148_DIR="$dir/state" FAKE148_PROJECT="${RUN148_PROJECT:-$PROJECT}" \
    FAKE148_SECRET="$CONTAINER" IDTOKEN_SENTINEL="$IDTOKEN_SENTINEL" \
    PATH="$dir/bin:$PATH" \
    CHECK148_OAUTH_URL="http://127.0.0.1:$FAKE_PORT" \
    CHECK148_API_URL="http://127.0.0.1:$FAKE_PORT/2.0" \
    CHECK148_CACHE_WAIT=0 \
    ${RUN148_ENV[@]+"${RUN148_ENV[@]}"} \
    "$bash_bin" "$script" ${RUN148_ARGS[@]+"${RUN148_ARGS[@]}"} "$@" 2>"$dir/err" < <(answers_for "$dir")) || status=$?
  # The block is kept as well as captured, so a case kept for inspection has the
  # whole of what the check printed and not only the first line of each failure.
  printf '%s\n' "$out" >"$dir/out"
  # And the answers' producer is told the run is over: a case that stopped before
  # the rotation has nothing to answer, and this is what ends it rather than the
  # poll above running out.
  : >"$dir/state/done"
  RUN148_ARGS=()
  RUN148_ENV=()
  RUN148_STDIN=""
  RUN148_CONFIRM=""
  RUN148_PROJECT=""
}

# assert_fakes_first DIR WHAT — the two tools the check would WRITE with are the
# suite's fakes. Everything here runs on a machine that may have the real ones,
# and a run that reached them would touch a real project.
assert_fakes_first() {
  local dir=$1 what=$2 tool resolved
  for tool in gcloud fugaro; do
    resolved=$(PATH="$dir/bin:$PATH" type -P "$tool")
    [ "$resolved" = "$dir/bin/$tool" ] \
      || fail "$what: $tool resolves to '$resolved' rather than this case's fake — the fakes have to be first on PATH"
  done
}

# default_args: the arguments every case gives the check, which are the three
# consumer files, the two names and --yes. A case that is about one of them sets
# RUN148_ARGS itself before calling this, and then this leaves it alone.
default_args() {
  if [ ${#RUN148_ARGS[@]} -gt 0 ]; then return 0; fi
  RUN148_ARGS=(--installation "$PROJECT" --repo "$REPO"
    --consumer-file "$dir/state/consumer.yaml" --rotated-consumer-file "$dir/state/rotated.yaml"
    --successor-consumer-file "$dir/state/successor.yaml" --yes)
}

# assert_no_secret DIR WHAT — nothing the run printed, and no file it left
# behind, carries a consumer's key or secret, the identity token or a read
# token. The fixtures this TEST writes are its own input files, and they are the
# only ones a sentinel may be in.
assert_no_secret() {
  local dir=$1 what=$2 sentinel found
  for sentinel in "$KEY_SENTINEL" "$SECRET_SENTINEL" "$ROTATED_SENTINEL" \
    "$SUCCESSOR_KEY_SENTINEL" "$SUCCESSOR_SECRET_SENTINEL" "$IDTOKEN_SENTINEL"; do
    expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$sentinel" "$what: a credential in the block"
  done
  # A read token is minted with a random tail, so it is looked for by its stem.
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$TOKEN_SENTINEL" "$what: a minted token in the block"
  found=$(grep -rlE 'SENTINEL-148-must-not-be-printed' "$dir/tmp" 2>/dev/null || true)
  [ -n "$found" ] && fail "$what: a credential survived in $found"
  return 0
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 148 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

expect_scratch_gone() { # dir what
  local scratch
  scratch=$(scratch_of "$1")
  if [ -z "$scratch" ]; then
    fail "$2: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$2: the scratch directory $scratch survived the run"
  [ -z "$(find "$1/tmp" -mindepth 1 2>/dev/null)" ] || fail "$2: files were left in TMPDIR: $(find "$1/tmp" -mindepth 1)"
  return 0
}

# ------------------------------------------------------------- the assertions
expect_wrote_bundle() { # dir what
  grep -q '^gcloud secrets versions add ' "$1/state/calls" \
    || fail "$2: the stop drill never wrote a bundle version $(tr '\n' ';' <"$1/state/calls")"
}
expect_no_bundle_write() { # dir what
  grep -qE '^(gcloud secrets versions add|fugaro creds set) ' "$1/state/calls" \
    && fail "$2: the bundle was written before the check had a reason to" || true
}
expect_set_bundle() { # dir what
  grep -q '^fugaro creds set bitbucket-read-consumer' "$1/state/calls" \
    || fail "$2: no fugaro creds set ran $(tr '\n' ';' <"$1/state/calls")"
}
# expect_entry_gone FILE WHAT — the document the service reads really no longer
# holds the entry. A case about a write that LANDED and failed is only about
# that if the write landed.
expect_entry_gone() { # file what
  grep -q 'bitbucket-read-consumer' "$1" \
    && fail "$2: $1 still holds the entry" || true
}
# expect_calls FILE PATTERN N WHAT — the fakes' log carries that many of a call.
# The writes a drill makes before the step under test are counted as well as the
# ones after it: what a case about the rotation needs to say is how many times
# the bundle was set, not whether the drill wrote anything at all.
expect_calls() { # file pattern n what
  local got
  got=$(grep -cE "$2" "$1" 2>/dev/null || true)
  [ "${got:-0}" = "$3" ] \
    || fail "$4: expected $3 calls matching '$2', got ${got:-0}: $(tr '\n' ';' <"$1")"
}

# ------------------------------------------------------------------- the tests
test_happy_path() {
  setup_case happy
  default_args
  run148
  expect_status "$status" 0 "happy path"
  expect_block_keys "$out" "happy path"
  expect_contains "$out" "CHECK 148 RESULT END (exit 0)" "happy path: the end line"
  expect_contains "$out" "FACT installation: PASS — " "happy path: the installation"
  expect_contains "$out" "$PROJECT" "happy path: the project the config names"
  expect_contains "$out" "FACT service: PASS — " "happy path: the mint's address"
  expect_contains "$out" "FACT read-consumer: PASS — " "happy path: the bundle holds the entry"
  expect_contains "$out" "$CONTAINER" "happy path: the container it read"
  expect_contains "$out" "FACT consumer-files: PASS — " "happy path: the three files"
  expect_contains "$out" "FACT confirm: PASS — " "happy path: --yes stands for the confirmation"
  expect_contains "$out" "FACT repository: PASS — " "happy path: the baseline mint"
  expect_contains "$out" "$REPO" "happy path: the repository it minted for"
  expect_contains "$out" "FACT xtrace: PASS — " "happy path: not tracing"
  expect_contains "$out" "FACT stop: PASS — " "happy path: the stop"
  # The status AND the code: a bundle with no read consumer is 503
  # state_unavailable (internal/credsvc/bitbucket.go's ErrReadNoConsumer through
  # refuseRead, wire.go's statuses), and the stop is read on exactly that.
  expect_contains "$out" "503 (refused: state_unavailable)" "happy path: the refusal the service makes"
  expect_contains "$out" "FACT restore-consumer: PASS — " "happy path: the entry put back"
  expect_contains "$out" "FACT rotate: PASS — " "happy path: the rotation"
  expect_contains "$out" "FACT old-secret: PASS — " "happy path: the replaced secret is refused"
  expect_contains "$out" "FACT u13: INFO — " "happy path: U13 is measured, not decided"
  # The suite's fake Bitbucket answers the pre-rotation token, so what the row
  # records here is the live reading: an INFO either way, and the words say which
  # reading this was.
  expect_contains "$out" "the token minted BEFORE the rotation still reads" "happy path: the reading is the live one"
  expect_contains "$out" "FACT transfer: PASS — " "happy path: the transfer"
  expect_contains "$out" "FACT restore: PASS — " "happy path: the Restore"
  expect_wrote_bundle "$dir" "happy path"
  expect_set_bundle "$dir" "happy path"
  # The transfer leaves the successor in the bundle, and the restore row says
  # so rather than claiming the bundle was put back.
  expect_contains "$out" "the bundle holds the successor consumer" "happy path: what the bundle ends holding"
  expect_absent "$out" "the bundle is back the way it was" "happy path: no false Restore"
  assert_no_secret "$dir" "happy path"
  expect_scratch_gone "$dir" "happy path"
}

# THE OTHER RED STEP: the stop's own reading. Only the service's OWN refusal is
# a stop -- 503 state_unavailable (internal/credsvc/bitbucket.go's
# ErrReadNoConsumer) -- and a 5xx is this run not knowing. A check that counted
# any non-200 as a stop would report a stop that may never have happened, which
# is the one thing it exists to avoid.
test_a_5xx_after_the_stop_is_not_a_pass() {
  setup_case stop5xx
  write_mode "$dir" '{"mint_absent_status": 500, "mint_absent_error": "server_error"}'
  default_args
  RUN148_ENV=(CHECK148_MINT_WAIT=0)
  run148
  expect_status "$status" 1 "a 500 after the stop"
  expect_contains "$out" "FACT stop: FAIL — " "a 500 after the stop: the stop row fails"
  expect_absent "$out" "FACT stop: PASS" "a 500 after the stop: no PASS for the stop"
  expect_contains "$out" "did not answer with the service's own refusal" "a 500 after the stop: the row says what it did not get"
  expect_contains "$out" "500 (server_error)" "a 500 after the stop: the row names the answer"
  expect_contains "$out" "503 state_unavailable" "a 500 after the stop: the row names the refusal it needed"
  # The rest of the drill still runs and reports: the entry goes back and the
  # mint serves again, so the rows below are measured rather than guessed.
  expect_contains "$out" "FACT restore-consumer: PASS — " "a 500 after the stop: the drill went on"
  assert_no_secret "$dir" "a 500 after the stop"
}

# THE THIRD RED STEP, and the one the status and the code cannot catch: a 503
# state_unavailable that is not the missing read consumer. internal/credsvc
# answers an unreadable stop switch, the kill switch, a bundle that fails to load
# (a hand-stripped document the loader refuses), a failed Bitbucket exchange and
# a failed audit with that same status and type; only ErrReadNoConsumer's own
# sentence tells the missing consumer from them. A stop row read on the pair
# alone passes here, which is a stop reported for a service that is stopped for
# an entirely different reason.
test_a_503_that_is_not_the_no_consumer_refusal_is_not_a_pass() {
  setup_case stopkillswitch
  write_mode "$dir" '{"mint_absent_message": "credsvc: the credentials service is stopped by the operator kill switch"}'
  default_args
  RUN148_ENV=(CHECK148_MINT_WAIT=0)
  run148
  expect_status "$status" 1 "a kill-switch 503 after the stop"
  expect_contains "$out" "FACT stop: FAIL — " "a kill-switch 503: the stop row fails"
  expect_absent "$out" "FACT stop: PASS" "a kill-switch 503: no PASS for the stop"
  expect_contains "$out" "503 (state_unavailable)" "a kill-switch 503: the row names the answer it got"
  expect_contains "$out" "a message that is not ErrReadNoConsumer's" "a kill-switch 503: the row says what was missing"
  expect_absent "$out" "carrying ErrReadNoConsumer's message" "a kill-switch 503: the pass sentence is not written"
  assert_no_secret "$dir" "a kill-switch 503"
}

# The refusal vocabulary is the service's own (internal/credsvc/wire.go), and a
# code the reader does not know reaches the block with no code at all — which is
# how a row about a refusal stops saying which refusal it measured.
test_the_services_own_refusal_codes_reach_the_block() {
  setup_case refusecode
  write_mode "$dir" '{"mint": "never", "mint_absent_status": 403, "mint_absent_error": "unverified_job_identity"}'
  default_args
  run148
  expect_status "$status" 2 "an identity refusal"
  expect_contains "$out" "FACT repository: FAIL — " "an identity refusal: the baseline row fails"
  expect_contains "$out" "unverified_job_identity" "an identity refusal: the row carries the service's own code"
  # The shape a dropped code leaves behind: the status and no code at all.
  expect_absent "$out" "answered 403." "an identity refusal: the code is not dropped"
  assert_no_secret "$dir" "an identity refusal"
}

# And an answer there is none of: a timeout, a dropped connection, an error page
# that is not a document. It reads as 000 with no document at all, and it is not
# a refusal either.
test_an_unanswered_mint_after_the_stop_is_not_a_pass() {
  setup_case stopnone
  write_mode "$dir" '{"mint_absent": "none"}'
  default_args
  RUN148_ENV=(CHECK148_MINT_WAIT=0)
  run148
  expect_status "$status" 1 "no answer after the stop"
  expect_contains "$out" "FACT stop: FAIL — " "no answer after the stop: the stop row fails"
  expect_absent "$out" "FACT stop: PASS" "no answer after the stop: no PASS for the stop"
  expect_contains "$out" "the service answered 000" "no answer after the stop: the row says what it got"
  expect_contains "$out" "the answer was not a document at all" "no answer after the stop: the row says there was no document"
  assert_no_secret "$dir" "no answer after the stop"
}

# THE RED STEP, kept as a test: a mint that is still served after the entry is
# out of the bundle. Everything below the stop still runs and reports, and the
# stop row is what fails -- a check that took the first mint after the write
# would have called this a pass.
test_a_mint_that_outlives_the_stop_is_a_fail() {
  setup_case outlives
  write_mode "$dir" '{"mint": "always"}'
  default_args
  RUN148_ENV=(CHECK148_MINT_WAIT=0)
  run148
  expect_status "$status" 1 "mint outlives the stop"
  expect_contains "$out" "FACT stop: FAIL — " "mint outlives the stop: the stop row fails"
  expect_contains "$out" "was still served" "mint outlives the stop: the row says what it saw"
  expect_contains "$out" "FACT rotate: PASS — " "mint outlives the stop: the rest of the drill still ran"
  expect_contains "$out" "CHECK 148 RESULT END (exit 1)" "mint outlives the stop: the block says it failed"
  expect_wrote_bundle "$dir" "mint outlives the stop"
  assert_no_secret "$dir" "mint outlives the stop"
}

# The service's own cache, which is the reason the check waits: the fake answers
# the mint out of a document it re-reads every second call, so the first mint
# after the stop write is the OLD document's answer. A check that trusted it
# would report FAIL for a stop that did happen.
test_the_bundle_cache_is_waited_out() {
  setup_case cache
  write_mode "$dir" '{"mint_cache": 2}'
  default_args
  RUN148_ENV=(CHECK148_MINT_WAIT=5)
  run148
  expect_status "$status" 0 "bundle cache"
  expect_contains "$out" "FACT stop: PASS — " "bundle cache: the stop still passes"
  expect_contains "$(cat "$dir/err")" "waiting for the bundle cache" "bundle cache: the wait is what made it a PASS"
  assert_no_secret "$dir" "bundle cache"
}

# doctor's exit code is not its answer: it prints the whole --json document and
# exits non-zero whenever any ordinary check fails, and a sandbox installation
# routinely carries one (a stale plugin pin, a secret not stored yet). A check
# that read the status as "doctor did not answer" would stop before measuring
# anything, on exactly the installation the drill is for -- so the document is
# what is read, and the status is only a line.
test_a_doctor_that_fails_but_answers_is_still_read() {
  setup_case doctorfail
  default_args
  RUN148_ENV=(FAKE_DOCTOR_STATUS=1)
  run148
  expect_status "$status" 0 "doctor exits 1"
  expect_contains "$out" "FACT installation: PASS — " "doctor exits 1: the installation row is read off the document"
  expect_contains "$(cat "$dir/err")" "exited 1 and printed its document anyway" "doctor exits 1: the status is said out loud"
  expect_contains "$out" "FACT stop: PASS — " "doctor exits 1: the drill measured anyway"
  assert_no_secret "$dir" "doctor exits 1"
}

# The other half of the same rule: a status that is non-zero AND a document that
# is not the document is a stop, and the reason names the status so the owner
# knows which of the two they are looking at.
test_a_doctor_that_answers_nothing_stops() {
  setup_case doctorempty
  default_args
  : >"$dir/state/doctor.json"
  RUN148_ENV=(FAKE_DOCTOR_STATUS=1)
  run148
  expect_status "$status" 2 "doctor answers nothing"
  expect_contains "$out" "FACT aborted: FAIL — " "doctor answers nothing: the run is aborted"
  expect_contains "$out" "did not answer with the document this check reads" "doctor answers nothing: the reason says what was missing"
  expect_contains "$out" "fugaro exited 1" "doctor answers nothing: the reason names the status"
  expect_no_bundle_write "$dir" "doctor answers nothing"
  assert_no_secret "$dir" "doctor answers nothing"
}

# A version that LANDED and a gcloud that exited non-zero anyway -- the partial
# write this repo names (internal/cli/creds.go, "a version of this credential
# may exist"). The block must not tell the operator nothing was written: what is
# on the service's document is the stripped bundle, every read mint is refused,
# and the version the stop replaced is still enabled.
test_a_versions_add_that_landed_and_failed_is_not_reported_as_nothing() {
  setup_case addlanded
  default_args
  printf '1\n' >"$dir/state/add.status"
  run148
  expect_status "$status" 2 "the add landed and failed"
  expect_entry_gone "$dir/state/bundle.yaml" "the add landed and failed: the case really landed the write"
  expect_contains "$out" "MAY have been written" "the add landed and failed: the Restore row says so"
  expect_absent "$out" "the bundle was not written" "the add landed and failed: no Restore claiming nothing happened"
  expect_absent "$out" "nothing was stopped and nothing was disabled" "the add landed and failed: the old reason is gone"
  expect_contains "$out" "FACT aborted: FAIL — " "the add landed and failed: the run is aborted"
  expect_contains "$out" "FACT stop: FAIL — not reached" "the add landed and failed: the stop was never measured"
  assert_no_secret "$dir" "the add landed and failed"
}

# The disable's own version of it: gcloud can disable the version and still exit
# non-zero, so a sentence that says the version is still enabled states the
# opposite of what may be true. What the run knows is that it cannot tell.
test_a_versions_disable_that_failed_is_no_claim_about_the_version() {
  setup_case disablefailed
  default_args
  printf '1\n' >"$dir/state/disable.status"
  run148
  expect_status "$status" 2 "the disable failed"
  expect_contains "$out" "cannot tell whether the version it replaced is still enabled" "the disable failed: the reason says what the run knows"
  expect_absent "$out" "the version this run replaced is still enabled, so the consumer" "the disable failed: no claim the run cannot make"
  expect_contains "$out" "possibly still enabled" "the disable failed: the Restore row does not claim the disable either"
  expect_contains "$out" "FACT stop: FAIL — not reached" "the disable failed: the stop was never measured"
  assert_no_secret "$dir" "the disable failed"
}

# And the bundle's own writer, which ends its step with a cross for the same
# reason: the entry is in the document the service reads and the command
# reported a failure.
test_a_creds_set_that_landed_and_failed_is_not_reported_as_nothing() {
  setup_case setlanded
  default_args
  printf '1\n' >"$dir/state/set.status"
  run148
  expect_status "$status" 2 "the set landed and failed"
  expect_contains "$out" "a fugaro creds set of the consumer the bundle held before the stop may have landed" "the set landed and failed: the Restore row says so"
  expect_absent "$out" "Nothing was written by it" "the set landed and failed: no claim the run cannot make"
  expect_contains "$out" "FACT restore-consumer: FAIL — not reached" "the set landed and failed: the row was never measured"
  assert_no_secret "$dir" "the set landed and failed"
}

# A rotation that leaves the replaced secret alive at Bitbucket. The write is
# fine and the mint serves; what fails is the reading that says the old secret
# is dead, which is the half of a rotation a bundle write cannot do.
test_a_rotation_that_leaves_the_old_secret_alive_is_a_fail() {
  setup_case leftover
  write_mode "$dir" '{"grant": "all"}'
  default_args
  run148
  expect_status "$status" 1 "leftover secret"
  expect_contains "$out" "FACT rotate: PASS — " "leftover secret: the write landed"
  expect_contains "$out" "FACT old-secret: FAIL — " "leftover secret: the row fails"
  expect_contains "$out" "still exchanges for a token" "leftover secret: the row says what it found"
  assert_no_secret "$dir" "leftover secret"
}

# The served direction, and the half of the cache that a first-200-read gets
# wrong in silence: the document a write REPLACED answers a 200 too, so the mint
# this drill reads a moment after a write can be the OLD credential's mint. What
# tells them apart is the cache's own lifetime, so the row waits it out before
# it reads the write as served -- and the wait is what a row read on the status
# alone would never take.
test_the_served_direction_waits_the_bundle_cache_out() {
  setup_case okwaits
  default_args
  RUN148_ENV=(CHECK148_CACHE_WAIT=5 CHECK148_MINT_WAIT=30)
  run148
  expect_status "$status" 0 "the served direction waits"
  expect_contains "$out" "FACT rotate: PASS — " "the served direction waits: the rotation is read as served"
  expect_contains "$(cat "$dir/err")" "which the service can still be answering from the document this write replaced" "the served direction waits: the row says why it is waiting"
  expect_contains "$(cat "$dir/err")" "waiting for the bundle cache" "the served direction waits: the cache is what it waited for"
  assert_no_secret "$dir" "the served direction waits"
}

# And the other side of that wait: a 200 read INSIDE the cache's lifetime, which
# is a mint the service did serve and a document this run cannot say which of the
# two it was. The bound is the case here -- the mint answers 200 every time and
# the run still must not read it as the write being served, so the row says what
# it got rather than claiming a serve it did not measure. It is the branch a
# case that only ever saw the waited-out reading would leave untested, and the
# numbers in the row are the ones it actually waited and was configured for.
test_a_200_inside_the_cache_window_is_not_a_serve() {
  setup_case oktoosoon
  default_args
  RUN148_ENV=(CHECK148_CACHE_WAIT=30 CHECK148_MINT_WAIT=5)
  run148
  expect_status "$status" 1 "a 200 inside the cache"
  expect_contains "$out" "FACT restore-consumer: FAIL — " "a 200 inside the cache: the row fails rather than claiming a serve"
  expect_absent "$out" "FACT restore-consumer: PASS" "a 200 inside the cache: no PASS for the restored consumer"
  expect_contains "$out" "the credential the write replaced rather than the one it wrote" "a 200 inside the cache: the row says which two documents it cannot tell apart"
  expect_contains "$out" "200 5s after the write" "a 200 inside the cache: the row carries the wait it really took"
  expect_contains "$out" "the 30s this run waits" "a 200 inside the cache: the row carries the wait it was configured for"
  # Both numbers are expanded into the row, not left as the shell's own text: a
  # row that printed its placeholders would be a row nobody can act on.
  expect_absent "$out" '${MINT_WAITED}' "a 200 inside the cache: the wait is not printed as a placeholder"
  expect_absent "$out" '${CACHE_WAIT}' "a 200 inside the cache: the configured wait is not printed as a placeholder"
  # The bound is the mint's, not the drill's: the rows below still run, so the
  # owner gets the whole block and not one row about the first write.
  expect_contains "$out" "FACT transfer: FAIL — " "a 200 inside the cache: the rest of the drill still ran"
  assert_no_secret "$dir" "a 200 inside the cache"
}

# And an answer that is not the mint at all -- a proxy, a load balancer, anything
# else answering in front of the service with a 200 and a body that is not the
# mint's document. The status is 200 and there is no credential in it, so a row
# read on the status alone calls the wrong secret served.
test_a_200_that_is_not_the_mint_is_not_a_serve() {
  setup_case junk200
  write_mode "$dir" "{\"mint_junk_for\": \"$ROTATED_SENTINEL\"}"
  default_args
  run148
  expect_status "$status" 1 "a 200 that is not the mint"
  expect_contains "$out" "FACT rotate: FAIL — " "a 200 that is not the mint: the rotate row fails"
  expect_absent "$out" "FACT rotate: PASS" "a 200 that is not the mint: no PASS for the rotation"
  expect_contains "$out" "the answer was not a document at all" "a 200 that is not the mint: the row says what it got"
  # The successor's own secret is not the one this case breaks, so the drill goes
  # on: the failure is the rotated document's reading and nothing else.
  expect_contains "$out" "FACT transfer: PASS — " "a 200 that is not the mint: the rest of the drill still ran"
  assert_no_secret "$dir" "a 200 that is not the mint"
}

# The other shape of the same 200: a JSON document that parsed and carries no
# read token. The status is 200 and the body was readable, so a row that
# distinguishes only "a document" from "not a document" would call this a read
# of the mint. The row has to say which of the two it got, and this pins the
# second one.
test_a_200_without_a_token_is_not_a_serve() {
  setup_case notoken200
  write_mode "$dir" "{\"mint_junk_for\": \"$ROTATED_SENTINEL\", \"mint_junk_body\": \"json\"}"
  default_args
  run148
  expect_status "$status" 1 "a 200 without a token"
  expect_contains "$out" "FACT rotate: FAIL — " "a 200 without a token: the rotate row fails"
  expect_absent "$out" "FACT rotate: PASS" "a 200 without a token: no PASS for the rotation"
  expect_contains "$out" "the answer carried no read token" "a 200 without a token: the row says what it got"
  expect_absent "$out" "the answer was not a document at all" "a 200 without a token: a parsed document is not reported as unreadable"
  assert_no_secret "$dir" "a 200 without a token"
}

# U13, the other way: the token minted before the rotation is refused. It is an
# INFO either way -- the question is open, and the check records which reading
# it got rather than turning it into a verdict.
test_u13_that_ends_the_token_is_recorded_too() {
  setup_case u13dead
  write_mode "$dir" '{"u13": "dead"}'
  default_args
  run148
  expect_status "$status" 0 "u13 dead"
  expect_contains "$out" "FACT u13: INFO — " "u13 dead: the row is an INFO"
  expect_contains "$out" "is refused" "u13 dead: the row says what it measured"
  expect_absent "$out" "FACT u13: FAIL" "u13 dead: an open question is not a failure"
  assert_no_secret "$dir" "u13 dead"
}

# And U13 where the request never came back: a dropped connection is not the API
# refusing the token, and the row is the one that says a rotation ended it. It
# records the reading it did not get instead, which keeps the question open
# rather than answering it with the run's own failure to read.
test_u13_that_never_answered_is_not_a_refusal() {
  setup_case u13none
  write_mode "$dir" '{"u13": "none"}'
  default_args
  run148
  expect_status "$status" 0 "u13 unanswered"
  expect_contains "$out" "FACT u13: INFO — " "u13 unanswered: the row is an INFO"
  expect_contains "$out" "U13 NOT measured" "u13 unanswered: the row says the reading is not there"
  # The row itself, not the whole block: the rotation's own old-secret row says
  # "is refused" about Bitbucket's answer to the replaced secret, which is a
  # different reading.
  local row
  row=$(printf '%s\n' "$out" | grep '^FACT u13:' || true)
  expect_absent "$row" "is refused" "u13 unanswered: a non-answer is not recorded as a refusal"
  assert_no_secret "$dir" "u13 unanswered"
}

# The replaced secret's own grant, where the OAuth endpoint never answered. The
# row is the one a rotation is done on, so a request that returned no document
# cannot be read as the endpoint refusing the secret: the reading is one this run
# does not have, and the row says so.
test_a_grant_that_never_answered_is_not_a_pass() {
  setup_case grantnone
  write_mode "$dir" '{"grant_absent": "none"}'
  default_args
  run148
  expect_status "$status" 1 "a grant that never answered"
  expect_contains "$out" "FACT old-secret: FAIL — " "a grant that never answered: the row fails"
  expect_absent "$out" "the replaced secret is refused" "a grant that never answered: no claim the old secret was refused"
  expect_contains "$out" "no answer at all" "a grant that never answered: the row says what it got"
  assert_no_secret "$dir" "a grant that never answered"
}

# The transfer has to be a DIFFERENT consumer: the same key with another secret
# is a rotation, and a check that accepted it would report a transfer that never
# happened. It stops before writing anything.
test_a_successor_that_is_the_same_consumer_stops() {
  setup_case samenext
  write_consumer "$dir/state/successor.yaml" "$KEY_SENTINEL" "$SUCCESSOR_SECRET_SENTINEL" "$WORKSPACE"
  default_args
  run148
  expect_status "$status" 2 "same consumer"
  expect_contains "$out" "FACT consumer-files: FAIL — " "same consumer: the files row fails"
  expect_contains "$out" "the SAME consumer key" "same consumer: the row says why"
  expect_no_bundle_write "$dir" "same consumer"
  assert_no_secret "$dir" "same consumer"
}

# And a rotation that is not a rotation: a document with a different key cannot
# stand for the same consumer's new secret, because the key is what Bitbucket
# identifies the consumer by. The reading is the rotation's own, taken after the
# owner's regeneration: the rotated document is the one step of this drill that
# cannot exist before the run starts, so nothing about it is read up front.
test_a_rotation_that_changes_the_key_stops() {
  setup_case keymoved
  write_consumer "$dir/state/rotated.yaml" "$SUCCESSOR_KEY_SENTINEL" "$ROTATED_SENTINEL" "$WORKSPACE"
  default_args
  run148
  expect_status "$status" 2 "rotation changed the key"
  expect_contains "$out" "FACT aborted: FAIL — " "rotation changed the key: the run is aborted at the rotation"
  expect_contains "$out" "DIFFERENT consumer key" "rotation changed the key: the row says why"
  expect_contains "$(cat "$dir/err")" "regenerate the secret of the read consumer" "rotation changed the key: the owner was asked before it was read"
  # The stop and the restore have written by now; the rotation has not.
  expect_calls "$dir/state/calls" '^fugaro creds set bitbucket-read-consumer' 1 "rotation changed the key"
  expect_absent "$(cat "$dir/state/bundle.yaml")" "$ROTATED_SENTINEL" "rotation changed the key: the rotated secret was never written"
  assert_no_secret "$dir" "rotation changed the key"
}

# A consumer file that names another workspace than --repo: the mint refuses a
# repository outside the consumer's workspace by name (G11), so the two have to
# agree before anything is written.
test_a_consumer_for_another_workspace_stops() {
  setup_case workspace
  write_consumer "$dir/state/rotated.yaml" "$KEY_SENTINEL" "$ROTATED_SENTINEL" "other-workspace"
  default_args
  run148
  expect_status "$status" 2 "another workspace"
  expect_contains "$out" "FACT aborted: FAIL — " "another workspace: the run is aborted at the rotation"
  expect_contains "$out" "names the workspace" "another workspace: the row says what it found"
  expect_calls "$dir/state/calls" '^fugaro creds set bitbucket-read-consumer' 1 "another workspace"
  expect_absent "$(cat "$dir/state/bundle.yaml")" "$ROTATED_SENTINEL" "another workspace: the rotated secret was never written"
  assert_no_secret "$dir" "another workspace"
}

# THE ROTATION'S OWN ORDER, and the reason the drill cannot start with the
# rotated document: the owner regenerates the secret at Bitbucket, and that is
# what stops the secret the bundle holds from being exchangeable. So the drill
# ASKS for it, at the rotation, and writes nothing until the answer is there --
# a drill that wrote the rotated document first would be spending a dead secret
# from its baseline row, and the whole run would measure nothing. Here the owner
# never answers: the drill must stop with the bundle holding the consumer it
# restored and not a byte of the rotated document.
test_the_rotation_waits_for_the_owners_regeneration() {
  setup_case unrotated
  default_args
  RUN148_STDIN=/dev/null
  run148
  expect_status "$status" 2 "no answer at the rotation"
  expect_contains "$out" "FACT aborted: FAIL — " "no answer at the rotation: the run is aborted"
  expect_contains "$out" "the rotation did not run" "no answer at the rotation: the reason names the step that did not run"
  expect_contains "$out" "regenerate the secret at Bitbucket" "no answer at the rotation: the reason names what was asked for"
  expect_contains "$out" "FACT rotate: FAIL — not reached" "no answer at the rotation: the rotation is never measured"
  # The ask came before the write, and the write never came: the bundle holds the
  # consumer the restore put back, and the set that would carry the rotated
  # secret is not in the fakes' log.
  expect_contains "$(cat "$dir/err")" "regenerate the secret of the read consumer" "no answer at the rotation: the drill asked before it wrote"
  expect_calls "$dir/state/calls" '^fugaro creds set bitbucket-read-consumer' 1 "no answer at the rotation"
  expect_contains "$(cat "$dir/state/bundle.yaml")" "$SECRET_SENTINEL" "no answer at the rotation: the bundle holds the consumer it restored"
  expect_absent "$(cat "$dir/state/bundle.yaml")" "$ROTATED_SENTINEL" "no answer at the rotation: the rotated secret was never written"
  assert_no_secret "$dir" "no answer at the rotation"
}

# And the other half of the same fact: what the mint spends is the pair
# bitbucket.org would exchange, not the entry's presence. A bundle holding a
# secret this Bitbucket has never issued -- what a drill that wrote the rotated
# document without the regeneration leaves -- is refused by the exchange, with
# the same 503 state_unavailable the missing consumer answers with. The baseline
# row is the one that reads it, and its remedy has to name the exchange.
test_the_baseline_mint_needs_the_secret_bitbucket_accepts() {
  setup_case unexchangable
  write_bundle "$dir/state/bundle.yaml" "$ROTATED_SENTINEL"
  default_args
  run148
  expect_status "$status" 2 "a secret Bitbucket will not exchange"
  expect_contains "$out" "FACT repository: FAIL — " "a secret Bitbucket will not exchange: the baseline row fails"
  expect_absent "$out" "FACT repository: PASS" "a secret Bitbucket will not exchange: no PASS for a mint that never happened"
  expect_contains "$out" "503 (refused: state_unavailable)" "a secret Bitbucket will not exchange: the refusal is the service's own status and code"
  expect_contains "$out" "the consumer's own key and secret are the ones Bitbucket accepts" "a secret Bitbucket will not exchange: the row names the exchange"
  expect_absent "$out" "FACT stop: PASS" "a secret Bitbucket will not exchange: nothing below the baseline was measured"
  assert_no_secret "$dir" "a secret Bitbucket will not exchange"
}

# A bundle that holds no read consumer at all: there is nothing to stop, rotate
# or transfer, and the check says so instead of writing a version that would
# stop nothing.
test_a_bundle_without_the_consumer_stops() {
  setup_case noentry
  python3 - "$dir/state/bundle.yaml" <<'PY'
import sys
lines = [l for l in open(sys.argv[1], encoding="utf-8").read().splitlines(keepends=True)
         if not l.startswith("bitbucket-read-consumer") and not l[:1].isspace()]
open(sys.argv[1], "w", encoding="utf-8").write("".join(lines))
PY
  default_args
  run148
  expect_status "$status" 2 "no consumer"
  expect_contains "$out" "FACT read-consumer: FAIL — " "no consumer: the row fails"
  expect_contains "$out" "holds no bitbucket-read-consumer" "no consumer: the row names the entry"
  # And the Restore row about a run that wrote nothing: it says the bundle was
  # not written rather than that the bundle is not back the way it was, which is
  # the same sentence contradicting its own next clause.
  expect_contains "$out" "The bundle was NOT written" "no consumer: the Restore row says nothing was written"
  expect_absent "$out" "The bundle is NOT back the way it was" "no consumer: no claim that a write moved the bundle"
  expect_no_bundle_write "$dir" "no consumer"
  assert_no_secret "$dir" "no consumer"
}

# A job with no credentials service: there is no read mint to exercise, and the
# check stops before it writes anything.
test_a_job_without_the_service_url_stops() {
  setup_case noun
  write_job "$dir" ""
  default_args
  run148
  expect_status "$status" 2 "no service url"
  expect_contains "$out" "FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL" "no service url: the row names the variable"
  expect_no_bundle_write "$dir" "no service url"
  assert_no_secret "$dir" "no service url"
}

# A mint that is refused before anything is written: the drill has no baseline
# to compare its four writes against, so it stops rather than measuring a
# difference from nothing.
test_a_refused_baseline_stops() {
  setup_case refused
  write_mode "$dir" '{"mint": "never"}'
  default_args
  run148
  expect_status "$status" 2 "refused baseline"
  expect_contains "$out" "FACT repository: FAIL — " "refused baseline: the row fails"
  expect_contains "$out" "refused before anything was written" "refused baseline: the row says why"
  expect_contains "$out" "one of the installation's operators" "refused baseline: the person class"
  expect_no_bundle_write "$dir" "refused baseline"
  assert_no_secret "$dir" "refused baseline"
}

# The confirmation: without --yes the run asks once and writes nothing until it
# is answered. The answer is the project ID, and anything else ends the run with
# the bundle untouched.
test_no_confirmation_is_no_write() {
  setup_case unconfirmed
  RUN148_ARGS=(--installation "$PROJECT" --repo "$REPO"
    --consumer-file "$dir/state/consumer.yaml" --rotated-consumer-file "$dir/state/rotated.yaml"
    --successor-consumer-file "$dir/state/successor.yaml")
  printf 'yes\n' >"$dir/state/answer.txt"
  RUN148_STDIN="$dir/state/answer.txt"
  run148
  expect_status "$status" 2 "unconfirmed"
  expect_contains "$out" "FACT aborted: FAIL — " "unconfirmed: the abort row"
  expect_contains "$out" "the confirmation was not given" "unconfirmed: the row says what was missing"
  expect_contains "$out" "nothing was written" "unconfirmed: the row says nothing was written"
  expect_no_bundle_write "$dir" "unconfirmed"
  # Every key is in the block whatever happened, so the rows of a drill that
  # never ran read as not reached rather than as measurements.
  expect_contains "$out" "FACT stop: FAIL — not reached" "unconfirmed: the stop row reads as not reached"
  expect_absent "$out" "FACT stop: PASS" "unconfirmed: no stop was reported"
  assert_no_secret "$dir" "unconfirmed"
}

# And the typed answer is what it asked for: the project ID, which runs the
# drill. The one line is what the owner goes by, so it has to name the project.
test_the_typed_confirmation_runs_it() {
  setup_case confirmed
  RUN148_ARGS=(--installation "$PROJECT" --repo "$REPO"
    --consumer-file "$dir/state/consumer.yaml" --rotated-consumer-file "$dir/state/rotated.yaml"
    --successor-consumer-file "$dir/state/successor.yaml")
  # The typed confirmation, and then the answer the rotation asks for: the two
  # are one stream, in the order the drill reads them.
  RUN148_CONFIRM="$PROJECT"
  run148
  expect_status "$status" 0 "confirmed"
  expect_contains "$out" "FACT confirm: PASS — " "confirmed: the confirm row passes"
  expect_contains "$out" "FACT transfer: PASS — " "confirmed: the drill ran"
  expect_wrote_bundle "$dir" "confirmed"
  assert_no_secret "$dir" "confirmed"
}

# `belong` is refused before anything is read: this check WRITES the bundle it
# is pointed at, and the owner's own installation is not a sandbox.
test_belong_is_refused() {
  setup_case belong
  default_args
  run148 --installation belong
  expect_status "$status" 2 "belong"
  expect_contains "$out" "FACT aborted: FAIL — " "belong: the abort row"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "never runs against 'belong'" "belong: the guard names it"
  expect_contains "$out" "FACT installation: FAIL — not reached" "belong: the installation was never verified"
  expect_no_bundle_write "$dir" "belong"
  expect_absent "$(cat "$dir/state/calls")" "gcloud" "belong: no cloud tool was called"
  assert_no_secret "$dir" "belong"
}

# The name typed and the config in force are two things, and this check writes
# whatever it is pointed at: a config naming another installation is the same
# refusal as naming the owner's own.
# AND THE CONFIG'S OWN NAME, which is the shape that hides a project ID: the
# owner's installation is named `belong` and its project is `fugaro-belong`, so a
# guard that read the ID alone passes here — the ID does not start with `belong`
# — and the drill writes the owner's live bundle four times. Doctor's answer is
# what has to be refused, and before any write: no cloud tool is reached at all.
test_a_config_that_names_belong_stops() {
  setup_case configbelong
  write_doctor "$dir" "fugaro-belong" "belong"
  default_args
  run148 --installation "fugaro-belong"
  expect_status "$status" 2 "a config naming belong"
  expect_contains "$out" "FACT aborted: FAIL — " "a config naming belong: the run is aborted"
  expect_contains "$out" "FACT installation: FAIL — " "a config naming belong: the installation row fails"
  expect_contains "$out" "name belong, project fugaro-belong" "a config naming belong: the row names what the config answered"
  expect_contains "$out" "the bundle it is pointed at" "a config naming belong: the row says why it is refused"
  expect_contains "$(cat "$dir/err")" "never runs against the owner's own installation" "a config naming belong: the guard names it"
  expect_no_bundle_write "$dir" "a config naming belong"
  expect_absent "$(cat "$dir/state/calls")" "gcloud" "a config naming belong: no cloud tool was called"
  assert_no_secret "$dir" "a config naming belong"
}

test_the_name_and_the_config_have_to_agree() {
  setup_case disagree
  write_doctor "$dir" "$PROJECT_B"
  default_args
  run148
  expect_status "$status" 2 "disagreeing name"
  expect_contains "$out" "FACT installation: FAIL — " "disagreeing name: the row fails"
  expect_contains "$out" "is not the one the local config in force describes" "disagreeing name: the row says why"
  expect_no_bundle_write "$dir" "disagreeing name"
  assert_no_secret "$dir" "disagreeing name"
}

# A project with no coordinator job: the check has nowhere to read the mint's
# address from, and it stops rather than guessing.
test_a_project_without_a_coordinator_job_stops() {
  setup_case nojob
  printf '[]\n' >"$dir/state/jobs.json"
  default_args
  run148
  expect_status "$status" 2 "no job"
  expect_contains "$out" "FACT aborted: FAIL — " "no job: the abort row"
  expect_contains "$out" "no single coordinator job is deployed" "no job: the row says what it found"
  expect_contains "$out" "the project lists 0 coordinator jobs" "no job: the reader's own count reaches the row"
  # The fix has to be the command that does it: --coordinator is the flag that
  # creates the job whose environment this check reads the mint's address from,
  # so a row naming `fugaro update` alone sends the operator back with the same
  # reading and no job (Check 146's own row names it too).
  expect_contains "$out" "--coordinator" "no job: the row names the flag that turns the coordinator on"
  expect_no_bundle_write "$dir" "no job"
  assert_no_secret "$dir" "no job"
}

# Two coordinator jobs: a project that lists two is one this check cannot read
# the service's address out of, so it stops instead of taking one of them.
test_two_coordinator_jobs_stop() {
  setup_case twojobs
  python3 - "$dir/state/jobs.json" <<'PY'
import json, sys
job = json.load(open(sys.argv[1], encoding="utf-8"))[0]
json.dump([job, job], open(sys.argv[1], "w", encoding="utf-8"))
PY
  default_args
  run148
  expect_status "$status" 2 "two jobs"
  expect_contains "$out" "FACT aborted: FAIL — " "two jobs: the abort row"
  expect_contains "$out" "no single coordinator job is deployed" "two jobs: the row says which read answered"
  expect_contains "$out" "the project lists 2 coordinator jobs" "two jobs: the reader's own count reaches the row"
  expect_no_bundle_write "$dir" "two jobs"
  assert_no_secret "$dir" "two jobs"
}

# The two Bitbucket roots are overridable for this test, and an override that is
# neither https nor loopback is refused rather than called: these requests carry
# the consumer's secret and the tokens this run mints.
test_a_cleartext_endpoint_override_is_refused() {
  setup_case cleartext
  default_args
  RUN148_ENV=(CHECK148_OAUTH_URL=http://bitbucket.example.invalid CHECK148_API_URL=http://api.example.invalid)
  run148
  expect_status "$status" 2 "cleartext endpoint"
  expect_contains "$out" "FACT aborted: FAIL — " "cleartext endpoint: the row "
  expect_contains "$out" "neither https nor a loopback http address" "cleartext endpoint: the row says why"
  expect_no_bundle_write "$dir" "cleartext endpoint"
  assert_no_secret "$dir" "cleartext endpoint"
}

# A bundle that cannot be read back at all: the stop drill builds its version
# from the document, and it stops rather than writing one it did not read.
test_a_bundle_that_cannot_be_read_stops() {
  setup_case unreadable
  printf '1\n' >"$dir/state/access.status"
  default_args
  run148
  expect_status "$status" 2 "unreadable bundle"
  expect_contains "$out" "FACT aborted: FAIL — " "unreadable bundle: the abort row"
  expect_contains "$out" "cannot build the version that omits the entry" "unreadable bundle: the row says why"
  expect_no_bundle_write "$dir" "unreadable bundle"
  assert_no_secret "$dir" "unreadable bundle"
}

# The listing and the document are two reads, and this check builds its version
# from the document: a listing that says the entry is there and a document that
# does not carry it is what the strip is for, and the count it makes is read off
# the two documents so a strip that removed nothing is caught before the write.
test_a_document_with_no_entry_to_strip_stops() {
  setup_case nothingtostrip
  claim_entry_present "$dir"
  strip_entry "$dir/state/bundle.yaml"
  write_mode "$dir" '{"mint": "always"}'
  default_args
  run148
  expect_status "$status" 2 "nothing to strip"
  expect_contains "$out" "FACT aborted: FAIL — " "nothing to strip: the abort row"
  expect_contains "$out" "want exactly one" "nothing to strip: the row says what it found"
  expect_no_bundle_write "$dir" "nothing to strip"
  assert_no_secret "$dir" "nothing to strip"
}

# A bundle document that is not the mapping this check strips an entry from: a
# document it cannot read is a document it will not write a version of.
test_a_document_that_is_not_a_mapping_stops() {
  setup_case notamapping
  claim_entry_present "$dir"
  printf -- '- one\n- two\n' >"$dir/state/bundle.yaml"
  write_mode "$dir" '{"mint": "always"}'
  default_args
  run148
  expect_status "$status" 2 "not a mapping"
  expect_contains "$out" "FACT aborted: FAIL — " "not a mapping: the abort row"
  expect_contains "$out" "want exactly one" "not a mapping: the row says what it found"
  expect_no_bundle_write "$dir" "not a mapping"
  assert_no_secret "$dir" "not a mapping"
}

# bash -x prints every command, and the commands this check runs carry the
# bundle document and the tokens it mints. A traced run is refused by name.
test_xtrace_is_refused() {
  setup_case xtrace
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE148_DIR="$dir/state" PATH="$dir/bin:$PATH" \
    "$bash_bin" -x "$script" --installation "$PROJECT" --repo "$REPO" \
    --consumer-file "$dir/state/consumer.yaml" --rotated-consumer-file "$dir/state/rotated.yaml" \
    --successor-consumer-file "$dir/state/successor.yaml" --yes 2>"$dir/err") || status=$?
  expect_status "$status" 2 "xtrace"
  expect_contains "$out" "FACT xtrace: FAIL — " "xtrace: the row fails"
  expect_contains "$out" "is tracing" "xtrace: the row says the shell was tracing"
  expect_no_bundle_write "$dir" "xtrace"
  assert_no_secret "$dir" "xtrace"
}

# The confirmation is one line and it names what will be written, because the
# drill's own subject is that a bundle write is what stops a credential.
test_the_confirmation_line_names_the_writes() {
  setup_case confirmline
  default_args
  RUN148_ARGS=(--installation "$PROJECT" --repo "$REPO"
    --consumer-file "$dir/state/consumer.yaml" --rotated-consumer-file "$dir/state/rotated.yaml"
    --successor-consumer-file "$dir/state/successor.yaml")
  printf 'no\n' >"$dir/state/answer.txt"
  RUN148_STDIN="$dir/state/answer.txt"
  run148
  expect_contains "$(cat "$dir/err")" "will WRITE the $PROJECT bundle FOUR times" "confirmation line: what will be written"
  expect_contains "$(cat "$dir/err")" "deletes nothing" "confirmation line: what will not"
  expect_contains "$(cat "$dir/err")" "Type $PROJECT to go ahead" "confirmation line: the one answer"
  expect_no_bundle_write "$dir" "confirmation line"
  assert_no_secret "$dir" "confirmation line"
}

# The two paths the register pins: a run that is refused because the shell was
# told to trace, and one that stops because a tool is missing. Both still print
# a block, which is what an owner has to send back.
test_a_missing_tool_still_prints_a_block() {
  setup_case notool
  # A PATH with bash alone on it: the check reaches its own requirements list and
  # names the first tool it cannot find, rather than failing on something else.
  mkdir -p "$dir/empty-bin"
  ln -s "$bash_bin" "$dir/empty-bin/bash"
  status=0
  out=$(env PATH="$dir/empty-bin" TMPDIR="$dir/tmp" FAKE148_DIR="$dir/state" \
    "$bash_bin" "$script" --installation "$PROJECT" --repo "$REPO" --yes 2>"$dir/err") || status=$?
  expect_status "$status" 2 "missing tool"
  expect_contains "$out" "CHECK 148 RESULT" "missing tool: the block"
  expect_contains "$out" "FACT aborted: FAIL — " "missing tool: the abort row"
  expect_contains "$out" "needs 'mktemp'" "missing tool: the tool it names"
  expect_contains "$out" "FACT stop: FAIL — not reached" "missing tool: no verdict about a drill that never ran"
  expect_absent "$out" "FACT stop: PASS" "missing tool: no stop was reported"
}

test_usage() {
  usage_run() { # the arguments, with the fakes first so the requirements list is
    # satisfied by this suite's tools rather than by whatever the machine has.
    status=0
    out=$(env PATH="$work/usage/bin:$PATH" "$bash_bin" "$script" "$@" 2>&1) || status=$?
  }
  mkdir -p "$work/usage/bin"
  write_fakes "$work/usage"
  usage_run --help
  expect_status "$status" 0 "usage: --help"
  expect_contains "$out" "--rotated-consumer-file" "usage: the argument the drill turns on"
  usage_run --installation "$PROJECT" --nope
  expect_status "$status" 2 "usage: an unknown argument"
  expect_contains "$out" "unknown argument" "usage: the refusal"
  usage_run --installation "$PROJECT" --repo "$REPO" --consumer-file c
  expect_status "$status" 2 "usage: a missing argument"
  expect_contains "$out" "--rotated-consumer-file is required" "usage: the missing one is named"
  usage_run --installation "$PROJECT" --repo "acme/legacy/extra" --consumer-file c --rotated-consumer-file r --successor-consumer-file s
  expect_status "$status" 2 "usage: a repository that is not owner/name"
  expect_contains "$out" "not a repository in the form owner/name" "usage: the refusal"
}

# The assertion above is only worth having if it can fail: a block that echoed a
# credential, and a file left in TMPDIR holding one, both have to be caught by
# it. The assertion is run with a `fail` of its own that counts, so what is being
# tested is the assertion rather than the run.
test_the_secret_assertion_catches_an_echoing_script() {
  setup_case echoing
  default_args
  run148
  local caught
  caught=$(
    local caught_failures=0
    fail() { caught_failures=$((caught_failures + 1)); }
    printf '%s\n' "$SECRET_SENTINEL" >>"$dir/out"
    assert_no_secret "$dir" "detector"
    printf '%s' "$caught_failures"
  )
  [ "${caught:-0}" -ge 1 ] \
    || fail "detector: a block carrying the consumer's secret raised $caught of the assertions that hold credentials out of it, so they are vacuous"
  caught=$(
    local caught_failures=0
    fail() { caught_failures=$((caught_failures + 1)); }
    printf '%s\n' "$SECRET_SENTINEL" >"$dir/tmp/leftover"
    assert_no_secret "$dir" "detector"
    printf '%s' "$caught_failures"
  )
  [ "${caught:-0}" -ge 1 ] \
    || fail "detector: a credential left in TMPDIR raised $caught of the assertions, so they are vacuous"
  rm -f "$dir/tmp/leftover"
}

# ------------------------------------------------------------------ the shards
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

t test_happy_path
t test_a_5xx_after_the_stop_is_not_a_pass
t test_a_503_that_is_not_the_no_consumer_refusal_is_not_a_pass
t test_the_services_own_refusal_codes_reach_the_block
t test_an_unanswered_mint_after_the_stop_is_not_a_pass
t test_a_mint_that_outlives_the_stop_is_a_fail
t test_the_bundle_cache_is_waited_out
t test_a_doctor_that_fails_but_answers_is_still_read
t test_a_doctor_that_answers_nothing_stops
t test_a_versions_add_that_landed_and_failed_is_not_reported_as_nothing
t test_a_versions_disable_that_failed_is_no_claim_about_the_version
t test_a_creds_set_that_landed_and_failed_is_not_reported_as_nothing
t test_the_served_direction_waits_the_bundle_cache_out
t test_a_200_inside_the_cache_window_is_not_a_serve
t test_a_200_that_is_not_the_mint_is_not_a_serve
t test_a_200_without_a_token_is_not_a_serve
t test_a_rotation_that_leaves_the_old_secret_alive_is_a_fail
t test_a_grant_that_never_answered_is_not_a_pass
t test_u13_that_ends_the_token_is_recorded_too
t test_u13_that_never_answered_is_not_a_refusal
t test_a_successor_that_is_the_same_consumer_stops
t test_a_rotation_that_changes_the_key_stops
t test_a_consumer_for_another_workspace_stops
t test_the_rotation_waits_for_the_owners_regeneration
t test_the_baseline_mint_needs_the_secret_bitbucket_accepts
t test_a_bundle_without_the_consumer_stops
t test_a_job_without_the_service_url_stops
t test_a_refused_baseline_stops
t test_no_confirmation_is_no_write
t test_the_typed_confirmation_runs_it
t test_belong_is_refused
t test_a_config_that_names_belong_stops
t test_the_name_and_the_config_have_to_agree
t test_a_project_without_a_coordinator_job_stops
t test_two_coordinator_jobs_stop
t test_a_cleartext_endpoint_override_is_refused
t test_a_bundle_that_cannot_be_read_stops
t test_a_document_with_no_entry_to_strip_stops
t test_a_document_that_is_not_a_mapping_stops
t test_xtrace_is_refused
t test_the_confirmation_line_names_the_writes
t test_a_missing_tool_still_prints_a_block
t test_usage
t test_the_secret_assertion_catches_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check148_test: $failures failure(s)" >&2
  exit 1
fi
echo "check148_test: all tests passed" >&2
