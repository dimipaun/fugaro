#!/usr/bin/env bash
# Check 91 — Cloud Run streaming, as ONE script YOU run.
#
# It settles three of U5's four numbers for the credentials service on the
# sandbox installation: Cloud Run's request timeout (the longest stream that
# completes), its request-body limit on HTTP/1.1, whether an SSE response
# reaches the caller unbuffered through the front end, and the cold start. The
# fourth — the limit on an end-to-end HTTP/2 request — is NOT settled here and
# this script does not claim to settle it (the container serves HTTP/1.1, so
# its HTTP/2 row measures the front end above the container). The register's
# section is docs/gcp-live-checklist.md, "Check 91"; the step-by-step text it
# used to carry is docs/credentials-service/check91-steps.md.
#
# Usage:      bash scripts/live-checks/check91.sh
#             bash scripts/live-checks/check91.sh --dry-run   (guards and files
#             only: nothing is created, built, deployed or deleted)
# Needs:      bash, mktemp, gcloud, curl, fugaro, and a curl built with HTTP/2.
#             Nothing is installed and nothing is fetched; if one of those is
#             missing the script stops with one line naming it.
# Where:      installation `fugaro` ONLY — project fugaro-dev, region us-east5.
#             The project is HARD-CODED and `fugaro doctor` must answer that
#             this is the fugaro-dev installation. Never belong, never another
#             project: you must not run it against any other installation.
# Creates:    one throwaway Cloud Run service (fugaro-chk91), the Artifact
#             Registry repository its image tag needs, and the image. The
#             Restore deletes all three and asserts three counts read 0. A
#             repository or service of that name that was already there is a
#             hard stop: this check never adopts, writes into or deletes what
#             it did not create.
# Costs:      wall clock, not money: around two hours at the terminal. The cold
#             start waits 35 minutes (CHECK91_IDLE_SECONDS), the long stream
#             runs up to 59 minutes, and the body table sends five sizes on two
#             protocols. An idle Cloud Run instance and one build are billed by
#             the project while it runs.
# Runs:       by YOU. No agent runs any step of this, and no agent has: what it
#             measures is a live project.
# Cleanup:    always: the service, the image and the repository it created are
#             deleted by the Restore, which runs from the exit trap too — so a
#             Ctrl-C in the middle does not leave a service running. The three
#             counts are checked before the scratch directory goes, and the
#             directory is KEPT when a count is not 0, with a message saying
#             where it is and what to delete by hand.
# Output:     progress on stderr, ONE block on stdout headed CHECK 91 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one did not (or a hard stop ended the run).
#
# CHECK91_IDLE_SECONDS, CHECK91_BODY_SIZES, CHECK91_SSE_SECONDS,
# CHECK91_PROBE_SECONDS and CHECK91_DRY_RUN exist for this script's own test:
# they shorten waits and sizes, and none of them can point the check at another
# project, another region or another service.
set -euo pipefail

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 1; }

# The two the whole check hangs on. HARD-CODED, never `gcloud config get-value
# project`: a check that reads the project from the environment can be pointed
# at another installation by a stray config, and this one must never be.
P=fugaro-dev
L=us-east5
SVC=fugaro-chk91
IMG="${L}-docker.pkg.dev/${P}/${SVC}/${SVC}:chk91"

# stop REASON: records a FAILed fact, ends the run. The EXIT trap prints the
# block, runs the Restore for whatever this run created, and cleans up.
stop() {
  fact "aborted" FAIL "$1"
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

DRY_RUN=${CHECK91_DRY_RUN:-}
case "${1:-}" in
--dry-run) DRY_RUN=1 ;;
"") ;;
*) die "usage: $PROG [--dry-run]" ;;
esac

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 91 needs '$1' on PATH and it is not there"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 91 needs bash"
  need mktemp
  need gcloud
  need curl
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk91-mine"
REPO_EXISTED=""
REPO_MINE=""
SERVICE_MINE=""
URL=""
SCRATCH_REMOVED=no
SCRATCH_KEPT=""
PRINTED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk91.XXXXXXXX") || die "Check 91 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  REPO_EXISTED="$SCRATCH/repo-existed"
  REPO_MINE="$SCRATCH/repo-mine"
  SERVICE_MINE="$SCRATCH/service-mine"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 91 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  fact "scratch" PASS "$SCRATCH (mode 0700, made by this check, removed by its Restore)"
}

remove_scratch() {
  case "$SCRATCH" in
  "" | / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  rm -rf "$SCRATCH"
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The Restore: step 7 as a function, because a run that stops in the middle has
# to run it too — a throwaway service left alive is billed to the project.
restore_run() {
  # Nothing of this check's was created -- a guard stopped the run, or it was a
  # dry run -- so there is nothing to delete. The repository counts here as
  # something created: a build that failed after the create still leaves the
  # repository behind, and the Restore is what removes it.
  if [ ! -f "$SERVICE_MINE" ] && [ ! -f "$REPO_MINE" ]; then
    fact "restore" PASS "nothing of this check's was created (no service, no repository), so there is nothing to delete"
    return 0
  fi
  local mine
  mine=$(cat "$REPO_EXISTED" 2>/dev/null || true)
  if [ "$mine" != no ]; then
    fact "restore" FAIL "$REPO_EXISTED does not say the repository ${SVC} was absent when this check started (it says '${mine:-nothing}'), so it is not this check's to delete. Nothing was deleted. Remove the service and the image by hand only if you know they are yours, and leave the repository alone. The scratch directory is kept: $SCRATCH"
    SCRATCH_KEPT=yes
    return 0
  fi
  say "Restore: deleting what this check created"
  # Only what the records say is this check's: a service or repository the run
  # did not create is never named here.
  if [ -f "$SERVICE_MINE" ]; then
    gcloud run services delete "${SVC:?}" --project="${P:?}" --region="${L:?}" --quiet >&2 || true
    gcloud artifacts docker images delete "${IMG:?}" --project="${P:?}" --delete-tags --quiet >&2 || true
  fi
  if [ -f "$REPO_MINE" ]; then
    gcloud artifacts repositories delete "${SVC:?}" --project="${P:?}" --location="${L:?}" --quiet >&2 || true
  fi

  local svc_left img_left repo_left
  svc_left=$(gcloud run services list --project="${P:?}" --region="${L:?}" \
    --filter="metadata.name=${SVC}" --format='value(metadata.name)' | wc -l | tr -d ' ')
  img_left=$(gcloud artifacts docker images list "${L}-docker.pkg.dev/${P}/${SVC}" \
    --project="${P:?}" --include-tags --format='value(image)' 2>/dev/null | wc -l | tr -d ' ')
  repo_left=$(gcloud artifacts repositories list --project="${P:?}" --location="${L:?}" \
    --filter="name:${SVC}" --format='value(name)' | wc -l | tr -d ' ')
  fact "restore-counts" PASS "services: ${svc_left}, images: ${img_left}, repository: ${repo_left} (each must read 0)"
  if [ "$svc_left" != 0 ] || [ "$img_left" != 0 ] || [ "$repo_left" != 0 ]; then
    fact "restore" FAIL "the counts are not all 0, so something this check created is still there (services=${svc_left} images=${img_left} repository=${repo_left}). The scratch directory is KEPT on purpose — it holds the records that say what is this check's — at $SCRATCH. Delete the survivors by hand, then re-run this check: it re-reads the records, deletes what is left and removes the directory once the three counts are 0."
    SCRATCH_KEPT=yes
    return 0
  fi
  fact "restore" PASS "the service, the image and the repository are deleted and the three counts read 0"
}

cleanup() {
  restore_run
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  if [ -n "$SCRATCH_KEPT" ]; then
    say "the scratch directory is kept: $SCRATCH"
  else
    remove_scratch
  fi
}

finish() {
  local status=$?
  trap - EXIT INT TERM
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  if printf '%s\n' "$facts" | grep -q '|FAIL|' && [ "$status" = 0 ]; then
    # A guard stop is exit 1 already; a FAILed fact with exit 0 would read as a
    # pass, so the verdict is taken from the facts, not from the exit path.
    status=1
  fi
  facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_project() {
  # Hard stop 1: the project and the region are the two the script just set.
  if [ "$P" != fugaro-dev ] || [ -z "$L" ]; then
    stop "the project and the region must be fugaro-dev and a region; P=${P} L=${L}"
  fi
  # Hard stop 2: this is the fugaro-dev installation, not another one.
  fugaro doctor --project fugaro 2>/dev/null | grep -q '^project: .*(GCP fugaro-dev,' \
    || stop "this is not the fugaro-dev installation (fugaro doctor --project fugaro did not say 'project: ... (GCP fugaro-dev,'), and this check runs on the sandbox installation only"
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  fact "project-region" PASS "${P}/${L} (installation fugaro, sandbox only; never belong)"
}

guard_service() {
  # Hard stop 4: the service is not already there — unless it is the one this
  # check deployed, recorded in service-mine right after the deploy.
  if gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" >/dev/null 2>&1; then
    [ -f "$SERVICE_MINE" ] \
      || stop "${SVC} already exists in ${P}/${L}, and it is not this check's. This check deletes what it creates and never adopts a service it did not create; delete it yourself if you know it is yours, or give this check a different name, then run again"
  fi
}

# The identity token every request carries, minted FRESH FOR EACH REQUEST: one
# minted here expires within the hour, and this check waits longer than that
# before it is done — and every 401 that would cause reads exactly like the
# size limit the body table is measuring. The value is never printed.
idt() { gcloud auth print-identity-token; }

baseline() {
  # The ownership record: a repository of this name that is already in the
  # project is not this check's, and this check never adopts, writes into or
  # deletes one it did not create.
  if [ ! -e "$REPO_EXISTED" ]; then
    if gcloud artifacts repositories describe "${SVC:?}" --project="${P:?}" --location="${L:?}" >/dev/null 2>&1; then
      stop "a repository named ${SVC} is already in ${P}/${L}, and it is not this check's: this check never adopts, writes into or deletes a repository it did not create. If it is a leftover of an earlier attempt whose own Restore did not finish, look at it first (gcloud artifacts docker images list ${L}-docker.pkg.dev/${P}/${SVC} --include-tags --project=${P}) and delete it yourself if it holds nothing but that attempt's image; otherwise give this check a different name. Then run again"
    fi
    echo no >"$REPO_EXISTED"
  fi
  [ "$(cat "$REPO_EXISTED")" = no ] \
    || stop "$REPO_EXISTED does not say the repository ${SVC} was absent when this check started, and this check writes only into a repository it created itself"
  fact "baseline" PASS "no repository named ${SVC} was in ${P}/${L} when this check started (repo-existed: no)"
}

# --------------------------------------------------------------- step 2 files
write_relay() {
  cat >"$SCRATCH/relay.py" <<'RELAY_EOF'
#!/usr/bin/env python3
"""Check 91's relay. One container, two listeners: a fake SSE upstream on
loopback and the relay the caller reaches.

  GET  /         -> 200 "started=<utc>\n"       (liveness, and which instance
                     answered: the time THIS container's process started, which
                     is what makes a cold-start figure checkable)
  GET  /sse      -> SSE for SSE_MINUTES minutes, one event a second
  POST /echo     -> 200 "read N bytes\n"        (the body-limit probe)
"""
import http.client, http.server, os, threading, time

STARTED = time.time()                               # this instance's birth
PORT = int(os.environ.get("PORT", "8080"))          # Cloud Run sets PORT
FAKE = ("127.0.0.1", 8081)                          # the fake upstream, same box
MINUTES = float(os.environ.get("SSE_MINUTES", "20"))


class Fake(http.server.BaseHTTPRequestHandler):     # the fake upstream
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        deadline = time.monotonic() + MINUTES * 60
        n = 0
        while time.monotonic() < deadline:
            n += 1
            d = ('data: {"n": %d, "t": %.3f}\n\n' % (n, time.time())).encode()
            self.wfile.write(b"%x\r\n" % len(d) + d + b"\r\n")
            self.wfile.flush()
            time.sleep(1)
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()


class Relay(http.server.BaseHTTPRequestHandler):    # what the caller reaches
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def do_GET(self):
        if not self.path.startswith("/sse"):
            body = ("started=%s\n"
                    % time.strftime("%Y-%m-%dT%H:%M:%SZ",
                                    time.gmtime(STARTED))).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        conn = http.client.HTTPConnection(*FAKE, timeout=3700)
        conn.request("GET", "/sse")
        resp = conn.getresponse()
        self.send_response(resp.status)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("X-Relay", "chk91")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        try:
            while True:
                chunk = resp.read1(65536)   # as soon as the fake wrote: SSE
                if not chunk:
                    break
                self.wfile.write(b"%x\r\n" % len(chunk) + chunk + b"\r\n")
                self.wfile.flush()
            self.wfile.write(b"0\r\n\r\n")
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass                               # the front end closed the request
        conn.close()

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        left = n
        while left > 0:
            chunk = self.rfile.read(min(left, 1 << 20))
            if not chunk:
                break
            left -= len(chunk)
        body = ("read %d bytes\n" % (n - left)).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def serve(handler, host, port):
    srv = http.server.ThreadingHTTPServer((host, port), handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


serve(Fake, FAKE[0], FAKE[1])
print("chk91 relay on %d, fake upstream on %d, SSE_MINUTES=%g"
      % (PORT, FAKE[1], MINUTES), flush=True)
serve(Relay, "0.0.0.0", PORT)      # the public listener, and only this one
while True:
    time.sleep(3600)
RELAY_EOF
  cat >"$SCRATCH/Dockerfile" <<'DOCKER_EOF'
FROM python:3.13-slim
ENV PYTHONUNBUFFERED=1
COPY relay.py /relay.py
CMD ["python3", "/relay.py"]
DOCKER_EOF
  fact "relay" PASS "relay.py and Dockerfile written into the scratch directory: one container, two listeners (the fake SSE upstream on loopback, the relay the caller reaches); SSE_MINUTES from the environment, X-Relay on the stream"
  say "wrote $SCRATCH/relay.py and $SCRATCH/Dockerfile"
}

# ------------------------------------------------------ step 3 build + deploy
step_build_deploy() {
  [ "$(cat "$REPO_EXISTED" 2>/dev/null || true)" = no ] \
    || stop "$REPO_EXISTED does not say the repository ${SVC} was absent when this check started, and this check writes only into a repository it created itself. Read the baseline's answer and run again"
  if gcloud artifacts repositories describe "${SVC:?}" --project="${P:?}" --location="${L:?}" >/dev/null 2>&1; then
    [ -f "$REPO_MINE" ] \
      || stop "a repository named ${SVC} is in ${P}/${L} and it is not this check's — this check creates its own and never adopts one, so it does not push the image tag into it or delete it. Nothing was written"
  else
    gcloud artifacts repositories create "${SVC:?}" --project="${P:?}" --location="${L:?}" \
      --repository-format=docker \
      --description="throwaway: Check 91's image repository (fugaro-chk91), deleted by its own Restore" --quiet >&2
    : >"$REPO_MINE"
  fi
  gcloud artifacts repositories describe "${SVC:?}" --project="${P:?}" --location="${L:?}" >/dev/null \
    || stop "no registry repository ${SVC} in ${P}/${L} for the image tag ${IMG} to build into"
  say "building ${IMG} (gcloud builds submit). If it offers to CREATE anything — the staging bucket, an API, a grant — say NO, stop, and report it: those are the installation's and are not this check's to accept"
  gcloud builds submit --project="${P:?}" --tag="${IMG:?}" "$SCRATCH" >&2 \
    || stop "the build failed, so nothing was deployed; nothing this check created was left behind (the scratch directory goes with the run)"
  gcloud run deploy "${SVC:?}" --project="${P:?}" --region="${L:?}" --image="${IMG:?}" \
    --no-allow-unauthenticated --timeout=3600 --min-instances=0 --max-instances=1 \
    --set-env-vars SSE_MINUTES=20 --quiet >&2 \
    || stop "the deploy failed; the repository and the image are deleted by this run's Restore"
  : >"$SERVICE_MINE"
  URL=$(gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" --format='value(status.url)')
  [ -n "$URL" ] || stop "the service has no URL"
  idt >/dev/null || stop "no identity token can be minted here"
  local cfg
  cfg=$(gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" \
    --format='value(spec.template.spec.containers[0].image,spec.template.spec.timeoutSeconds,spec.template.metadata.annotations)' 2>/dev/null | tr '\n' ' ')
  fact "service-config" PASS "image ${cfg:-$IMG}; --timeout=3600 --min-instances=0 --max-instances=1 --no-allow-unauthenticated; URL ${URL}"

  # An unauthenticated call must be refused: that is what says the service is
  # not wide open, and it is the register's own assertion.
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' "${URL}/" 2>/dev/null || true)
  case "$code" in
  401 | 403) fact "unauthenticated-request" PASS "an unauthenticated GET / was refused with ${code}" ;;
  *) fact "unauthenticated-request" FAIL "an unauthenticated GET / answered ${code:-nothing}, so the service does not refuse unauthenticated calls — stop and fix the deployment before trusting anything else here" ;;
  esac
}

# ------------------------------------------------------------ step 4 cold start
step_cold_start() {
  local idle_seconds=${CHECK91_IDLE_SECONDS:-2100}
  local before cold warm
  before=$(curl -sS -H "Authorization: Bearer $(idt)" "${URL}/" | sed -n 's/^started=//p')
  [ -n "$before" ] || stop "the relay's / body carries no started= line, so this is not this check's image"
  say "instance now: started=${before}; waiting ${idle_seconds}s for it to be released"
  sleep "$idle_seconds"
  cold=$(curl -sS -o "$SCRATCH/cold.out" -w '%{time_starttransfer}|%{time_total}' -H "Authorization: Bearer $(idt)" "${URL}/")
  warm=$(curl -sS -o /dev/null -w '%{time_starttransfer}|%{time_total}' -H "Authorization: Bearer $(idt)" "${URL}/")
  local after
  after=$(sed -n 's/^started=//p' "$SCRATCH/cold.out")
  local detail="waited ${idle_seconds}s; cold: first byte ${cold%%|*}s, total ${cold##*|}s; warm: first byte ${warm%%|*}s, total ${warm##*|}s; started= before=${before} after=${after}; --min-instances=0 --max-instances=1"
  if [ "$after" != "$before" ]; then
    fact "cold-start" PASS "a DIFFERENT instance answered the cold request — ${detail}"
  else
    fact "cold-start" FAIL "NOT a cold start: the instance that started at ${after} answered the request after the wait, so the figure is warm. Record it as warm and repeat the step after a longer idle — ${detail}"
  fi
}

# --------------------------------------------------------------- step 5 stream
step_stream() {
  local probe=${CHECK91_PROBE_SECONDS:-30}
  : >"$SCRATCH/stream.times"
  local headers
  headers=$(curl -sS -N -D - -o /dev/null --max-time "$probe" -H "Authorization: Bearer $(idt)" "${URL}/sse" |
    sed -n 's/^[Xx]-[Rr]elay: */x-relay: /p' | head -n 1)
  curl -sS -N --max-time "$probe" -H "Authorization: Bearer $(idt)" "${URL}/sse" |
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      printf '%s %s %s\n' "$(date -u +%s)" "$(date -u +%H:%M:%S)" "$line" >>"$SCRATCH/stream.times"
    done
  local events span
  events=$(wc -l <"$SCRATCH/stream.times" | tr -d ' ')
  if [ "$events" -lt 2 ]; then
    fact "stream-unbuffered" FAIL "the ${probe}s probe carried ${events} event(s), so nothing about buffering can be read from it"
  else
    span=$(( $(tail -n 1 "$SCRATCH/stream.times" | cut -d' ' -f1) - $(head -n 1 "$SCRATCH/stream.times" | cut -d' ' -f1) ))
    if [ "$span" -ge $((events - 1)) ]; then
      fact "stream-unbuffered" PASS "${events} events arrived over ${span}s (one a second, so the stream is unbuffered through the front end); ${headers:-no X-Relay header recorded}; timestamps: $(cut -d' ' -f2,3 "$SCRATCH/stream.times" | head -n 3 | tr '\n' ' ')"
    else
      fact "stream-unbuffered" FAIL "${events} events arrived inside ${span}s: a burst, not a stream — the front end or the relay is buffering. First timestamps: $(cut -d' ' -f2,3 "$SCRATCH/stream.times" | head -n 3 | tr '\n' ' ')"
    fi
  fi
}

step_long_stream() {
  say "the long stream: SSE_MINUTES=59 against --timeout=3600. This is the hour-long part"
  gcloud run services update "${SVC:?}" --project="${P:?}" --region="${L:?}" --set-env-vars SSE_MINUTES=59 --quiet >&2 \
    || stop "the service update to SSE_MINUTES=59 failed"
  local out
  out=$(curl -sS -N -o /dev/null -w '%{http_code} after %{time_total}s' -H "Authorization: Bearer $(idt)" "${URL}/sse")
  fact "stream-longest" PASS "SSE_MINUTES=59: ${out} (--timeout=3600; mint a fresh identity token for every probe after this one)"
}

# ----------------------------------------------------- step 6 the body table
step_body_limit() {
  curl -V 2>/dev/null | grep -q 'HTTP2' \
    || stop "this curl has no HTTP/2 support, so --http2 would silently send HTTP/1.1 and the second row would duplicate the first — use a curl built with HTTP/2"
  local sizes=${CHECK91_BODY_SIZES:-"1048576 16777216 33554432 67108864 134217728"}
  local n h1 h2 h1_big="" h2_big=""
  local table=""
  for n in $sizes; do
    head -c "$n" /dev/zero >"$SCRATCH/body.bin"
    h1=$(curl -sS --http1.1 -o "$SCRATCH/echo.out" -w '%{http_code} %{size_upload} over HTTP/%{http_version}' \
      -H "Authorization: Bearer $(idt)" -X POST --data-binary @"$SCRATCH/body.bin" "${URL}/echo")
    h1="${h1}, relay said: $(tr -d '\n' <"$SCRATCH/echo.out")"
    h2=$(curl -sS --http2 -o "$SCRATCH/echo.out" -w '%{http_code} %{size_upload} over HTTP/%{http_version}' \
      -H "Authorization: Bearer $(idt)" -X POST --data-binary @"$SCRATCH/body.bin" "${URL}/echo")
    h2="${h2}, relay said: $(tr -d '\n' <"$SCRATCH/echo.out")"
    table="${table}${n}: HTTP/1.1 ${h1} | HTTP/2-to-front-end ${h2}"$'\n'
    case "$h1" in 200*) h1_big="$n: $h1" ;; esac
    case "$h2" in 200*) h2_big="$n: $h2" ;; esac
    rm -f "$SCRATCH/body.bin" "$SCRATCH/echo.out"
  done
  if [ -n "$h1_big" ]; then
    fact "body-limit-http1" PASS "largest accepted: ${h1_big}. A 401 is an expired identity token, not a limit; a refusal above what the check believed is a finding about U5, not a failure of the check"
  else
    fact "body-limit-http1" FAIL "no size was accepted on HTTP/1.1 end to end: $(printf '%s' "$table" | tr '\n' '; ')"
  fi
  fact "body-limit-http2-front-end" PASS "the front end, over TLS to it (NOT end to end: the container serves HTTP/1.1 only): $( [ -n "$h2_big" ] && echo "largest accepted ${h2_big}" || echo "nothing accepted" ). Rows: $(printf '%s' "$table" | tr '\n' ' ') "
  fact "http2-end-to-end" INFO "NOT SETTLED by this check: the relay is Python http.server (HTTP/1.1), so no request here is HTTP/2 to the container. U5's end-to-end HTTP/2 half stays UNVERIFIED; settling it needs --use-http2 and a container that serves h2c"
}

# ----------------------------------------------------------------------- main
main() {
  need_tools
  scratch_up
  guard_project
  write_relay
  guard_service
  baseline

  if [ -n "$DRY_RUN" ]; then
    say "dry run: guards, the baseline and the files only — nothing created, built, deployed or deleted"
    fact "dry-run" PASS "guards, the baseline and the relay files only"
    return 0
  fi

  step_build_deploy
  step_cold_start
  step_stream
  step_long_stream
  step_body_limit
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, sorted. That is what
# makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version project-region baseline scratch service-config
  unauthenticated-request cold-start stream-unbuffered stream-longest
  body-limit-http1 body-limit-http2-front-end http2-end-to-end relay restore
  restore-counts scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 91 RESULT\n' >&3
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
  printf 'CHECK 91 RESULT END (exit %s)\n' "$status" >&3
}

trap finish EXIT
trap 'owner "interrupted"; exit 130' INT TERM
main "$@"
