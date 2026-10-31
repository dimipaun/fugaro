# Check 91 — the step-by-step text (appendix)

This is the step-by-step text the register's Check 91 section used to carry, kept
because it is what explains a result. **It is the MANUAL procedure** — the blocks
below are meant to be pasted into your own shell, one step at a time — and it is
**not** a description of the script. What you actually run is
`scripts/live-checks/check91.sh`, which performs the same steps itself and prints
the `CHECK 91 RESULT` block. Read this when a fact in that block is not `PASS` and
you want to know what the step it names did; where the two differ, **the script is
what ran**, and the differences that matter when you are reading a block are
these:

- **The exit statuses.** Every stop in the blocks below is `exit 1`, because a
  pasted block stops the shell it is pasted into. The script exits `1` for a
  stop too, but it always prints the block first, with an `aborted` fact naming
  the reason, and `2` is never a stop of its own.
- **The scratch directory.** The blocks below remember it in `$CHK91_RECORD` (a
  file under `$HOME`), because each block is sourced separately. The script holds
  it in one shell and writes no such file; its scratch directory carries a marker
  file the script re-checks before removing it.
- **The service's state, and the repository's.** Absence is an **empty list**,
  never an error string: every existence probe in the script (the service, the
  repository, the build account) is a `gcloud ... list` filtered to one name,
  where an empty answer with exit 0 means "not there" and a non-zero exit (an
  expired session, a transient API error, a permission the account lacks), or an
  answer that is not the one name asked for, means the state **cannot be read**.
  The real gcloud says `Cannot find service [NAME]` for a describe of an absent
  service, with no `NOT_FOUND` in it, so nothing reads that text. A failed list is
  **not** the answer "it is not there": the guard stops rather than adopting a
  service it did not create, and a run that did create one keeps its ownership
  record so the Restore still deletes it. The blocks below do the same with the
  plain `list` a human runs.
- **The build's error.** The script keeps `gcloud builds submit`'s stderr and
  puts its last 30 lines in the block as a `build-error` fact, with anything
  credential-shaped replaced by `<redacted>`, because the block is pasted into a
  pull request. Below, you read that output yourself at the terminal.

The register's section is `docs/gcp-live-checklist.md`, "Check 91".

---

## Check 91: Cloud Run streaming — one long call through a throwaway service (installation `fugaro`, sandbox only, never `belong`; run by you; NOT RUN)

**NOT RUN.** Written by Task C0.3 of [credentials-service/implementation-plan.md](credentials-service/implementation-plan.md) (its Check 91, D151); **no step of it has been run, and nothing here is a claim that it works.** It settles **U5** and the reconciliation's F31 for **three of its four numbers**: Cloud Run's request timeout (the longest stream that completes), its request-body limit **on HTTP/1.1**, whether an SSE response reaches the caller unbuffered through the front end, and the cold start. **The fourth — the limit on an end-to-end HTTP/2 request — is not settled by this check as it stands** (step 6 says exactly why and what a check that settles it needs), so **U5's end-to-end HTTP/2 half stays UNVERIFIED** after this check runs, and the service's forwarding is written against a number this check leaves open. **No agent runs any step of it**, and **never `belong`**: this check is installation `fugaro` (project `fugaro-dev`, region `us-east5`), sandbox side only.

**What it creates, and what it deletes.** One throwaway Cloud Run **service** (`fugaro-chk91`), the Artifact Registry **repository** its image tag names (created by step 3, and deleted by the Restore — both only because the baseline found no repository of that name and said so; **a repository that is already there is a hard stop, because this check never adopts, writes into or deletes what it did not create**), and the container **image** pushed into it. **Nothing else in the project is touched:** not the installation's jobs, its secrets, its bucket or any repository, and no repository checkout is involved at all (the relay is written and built from a scratch directory outside every work tree). The Restore deletes the three things above, ends in an assertion whose three counts must each read `0`, and removes its scratch directory only once they do. **It costs wall clock rather than money:** step 4 waits 35 minutes for the idle instance to be released, step 5 runs one stream for up to 59 minutes, and step 6 sends five body sizes per protocol — plan for around two hours at the terminal, and for the identity token to have to be minted again at each step (which the guard's `idt` does).

### Check 91 step 1: setup and guard

```bash
# === Check 91: setup + guard. RE-RUN THIS WHOLE BLOCK at the start of every
# step: it is idempotent, and nothing below the hard stops runs. ===
P=fugaro-dev                 # HARD-CODED, never `gcloud config get-value project`
L=us-east5                   # the installation's region
# Hard stop 1: the project and the region are the two the block just set.
[ "$P" = fugaro-dev ] && [ -n "$L" ] \
  || { echo "Check 91 stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L not empty)" >&2; exit 1; }
# Hard stop 2: this is the fugaro-dev installation, not another one.
fugaro doctor --project fugaro --all | grep -q '^project: .*(GCP fugaro-dev,' \
  || { echo "Check 91 stopped: this is not the fugaro-dev installation" >&2; exit 1; }
SVC=fugaro-chk91
# Hard stop 3: the scratch directory is one THIS CHECK makes, and it is the
# only thing step 7's `rm -rf` is ever allowed to name — never a path you
# typed. It holds the relay, its Dockerfile and the probe bodies, so it has to
# stay the same one across steps: `mktemp -d` gives it a shape that is safe to
# remove (absolute, under $TMPDIR, not / and not $HOME) and a marker file this
# check writes, and the path is remembered in $CHK91_RECORD so re-running this
# block does not make a second one.
CHK91_RECORD=${CHK91_RECORD:-$HOME/.chk91-scratch-dir}
if [ -s "$CHK91_RECORD" ]; then
  CHK91=$(cat "$CHK91_RECORD")
  [ -f "$CHK91/.chk91-mine" ] \
    || { echo "Check 91 stopped: $CHK91_RECORD names $CHK91, which carries no marker from this check — delete $CHK91_RECORD and re-run" >&2; exit 1; }
else
  CHK91=$(mktemp -d "${TMPDIR:-/tmp}/chk91.XXXXXXXX") || exit 1
  : > "$CHK91/.chk91-mine"
  printf '%s\n' "$CHK91" > "$CHK91_RECORD"
fi
mkdir -p "$CHK91" && cd "$CHK91" || exit 1
chmod 700 "$CHK91"
# ... and outside every repository, so nothing written here can be committed.
git rev-parse --show-toplevel >/dev/null 2>&1 \
  && { echo "Check 91 stopped: $CHK91 is inside a git work tree" >&2; exit 1; }
# Hard stop 4: the service is not already there — unless it is the one this
# check deployed, which step 3 records in $CHK91/service-mine BEFORE the deploy
# (so that a deploy interrupted in the middle is still one this check owns and
# deletes), which is also what lets this block be re-run at the start of every
# later step.
# This check deletes what it creates; a service it did not create is not its to
# adopt or delete.
svc_there=$(gcloud run services list --project="${P:?}" --region="${L:?}" --filter="metadata.name=${SVC}" --format='value(metadata.name)') \
  || { echo "Check 91 stopped: whether ${SVC} is already in ${P}/${L} cannot be read (the list failed), and a failed call is not the answer 'it is not there'" >&2; exit 1; }
if [ -n "$svc_there" ]; then
  [ -f "$CHK91/service-mine" ] \
    || { echo "Check 91 stopped: ${SVC} already exists in ${P}/${L}, and it is not this check's" >&2; exit 1; }
fi
# The identity token every request carries, minted FRESH FOR EACH REQUEST:
# `gcloud auth print-identity-token` mints one that expires within the hour,
# and this check waits longer than that before it is done (step 5's 59-minute
# stream, then a probe per body size in step 6), so a token minted once in step
# 3 is expired by step 6 — and every 401 that would cause reads exactly like
# the request-body limit that step is measuring. Never paste the value.
#
# It is NEVER on a command line either: `ps` shows a process's argv to every
# user of the machine for as long as the process lives, and the hour-long
# stream would hold a token there for an hour. `idt` writes it into a 0600
# curl config file inside the scratch directory and every request names that
# file with `--config`, which curl reads the header from. The file is removed
# with the scratch directory, and by step 7 before it.
AUTH_CONF=$CHK91/idt.conf
idt() {
  local tok
  tok=$(gcloud auth print-identity-token) || return 1
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$AUTH_CONF") || return 1
  chmod 600 "$AUTH_CONF" || return 1
  unset tok
}
IMG=${L}-docker.pkg.dev/${P}/${SVC}/${SVC}:chk91
```

**Hard stops end the shell they are run in, and that is the intended outcome.** Every stop above and below is `exit 1`; pasted into an interactive shell it closes the window, and the steps after the stop never run — which is the fail-safe end, because nothing this check does may continue past a stop. Check 43's paragraphs apply here word for word ("Paste the block into a file, not straight into an interactive zsh"), and so does the way to keep the window: save this block to a file and run it in a subshell — `( . ./chk91-env.sh )` — read the message, fix what it names, and source it again in the shell the steps run in. Nothing after a stop may run; do not convert a stop into something that lets the rest of the block keep going.

**Baseline, once, before anything is created — and it is a hard stop.** The record it writes is what the Restore reads, and it is the check's own ownership record for the one repository it writes into. **A repository named `fugaro-chk91` that is already in the project is not this check's, and this check never adopts one:** it does not create it, does not push the image tag into it (which would move a `:chk91` tag somebody else's image may hold), and its Restore never deletes it or an image under it. So the baseline stops there rather than working around it:

```bash
if [ ! -e "$CHK91/repo-existed" ]; then
  repo_there=$(gcloud artifacts repositories list --project="${P:?}" --location="${L:?}" --filter="name~/repositories/${SVC}\$" --format='value(name)' 2>"$CHK91/list.err") \
    || { echo "Check 91 stopped: whether a repository named ${SVC} is already in ${P}/${L} cannot be read (the list failed), and a failed call is not the answer 'it is not there'" >&2; exit 1; }
  ! grep -q 'Failed to list' "$CHK91/list.err" \
    || { echo "Check 91 stopped: the repository list printed 'Failed to list repositories' and exited 0 — gcloud swallowed an API error, so its empty answer is not a reading of whether ${SVC} is in ${P}/${L}" >&2; exit 1; }
  if [ -n "$repo_there" ]; then
    echo "Check 91 stopped: a repository named ${SVC} is already in ${P}/${L}, and it is not this check's. This check never adopts, writes into or deletes a repository it did not create. If it is a leftover of an earlier attempt of this check whose own Restore did not finish, look at it first (gcloud artifacts docker images list ${L}-docker.pkg.dev/${P}/${SVC} --include-tags --project=${P}) and delete it yourself if it holds nothing but that attempt's image; otherwise give this check a different name (SVC in step 1). Then re-run this block." >&2
    exit 1
  fi
  echo no > "$CHK91/repo-existed"
fi
cat "$CHK91/repo-existed"
```

`no` is therefore the only answer this file ever holds, and it means what it says: **the repository the build and the Restore work on is one this check made.** Step 3 is the step that writes into it, and it reads this record back before it writes anything: the build's push happens only when the file says `no` **and** the repository the list found is one step 3 created itself, recorded in `$CHK91/repo-mine` — so a `fugaro-chk91` that appeared after the baseline stopped the check instead of being written into. The Restore reads the same record back before it deletes anything, and stops unless it says `no`. The Restore keeps the file (and the scratch directory with it) unless every one of its three counts is `0`.

**FACT:** `fugaro version`, the two `P`/`L` values, and the baseline's answer — `no`, or the stop, which is recorded and reported rather than worked around.

### Check 91 step 2: the relay, in full

Write `$CHK91/relay.py` and `$CHK91/Dockerfile`. The container runs **two listeners**: the fake SSE upstream on loopback, and the relay the caller reaches — so what this measures is Cloud Run's front end and the relay's own streaming, never an external upstream.

```python
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
```

```dockerfile
FROM python:3.13-slim
ENV PYTHONUNBUFFERED=1
COPY relay.py /relay.py
CMD ["python3", "/relay.py"]
```

### Check 91 step 3: build and deploy the throwaway service

```bash
# The image's tag names a repository in the project's Artifact Registry, and
# without it the build has nowhere to push.
# Hard stop 5: this check pushes only into a repository IT created. The
# baseline refused one that was already in the project when the check started —
# it is the ownership record, and reading it here is what says that premise
# still holds — but it looks ONCE, and the repository is created below, so a
# fugaro-chk91 that appeared after the baseline (or one the installation made
# after a Restore of this check's own had finished) would otherwise be picked up
# here: the build below would push its :chk91 tag into it, moving a tag that may
# be somebody else's image, and step 7's Restore would then delete it. So
# nothing is written into a repository whose presence this check cannot account
# for: create it, or — a re-run of this step, where the create below already
# ran — find the one THIS check made, recorded in $CHK91/repo-mine BEFORE the
# create, exactly as hard stop 4 records the service (a run killed between the
# record and the create still leaves the Restore a repository it owns).
[ "$(cat "$CHK91/repo-existed" 2>/dev/null)" = no ] \
  || { echo "Check 91 stopped: $CHK91/repo-existed does not say the repository ${SVC} was absent when this check started, and this check writes only into a repository it created itself. Re-run step 1's block and read the baseline's answer." >&2; exit 1; }
repo_there=$(gcloud artifacts repositories list --project="${P:?}" --location="${L:?}" --filter="name~/repositories/${SVC}\$" --format='value(name)' 2>"$CHK91/list.err") \
  || { echo "Check 91 stopped: whether a repository named ${SVC} is already in ${P}/${L} cannot be read (the list failed)" >&2; exit 1; }
! grep -q 'Failed to list' "$CHK91/list.err" \
  || { echo "Check 91 stopped: the repository list printed 'Failed to list repositories' and exited 0 — gcloud swallowed an API error, so its empty answer is not a reading of whether ${SVC} is in ${P}/${L}" >&2; exit 1; }
if [ -n "$repo_there" ]; then
  [ -f "$CHK91/repo-mine" ] \
    || { echo "Check 91 stopped: a repository named ${SVC} is in ${P}/${L} and it is not this check's — this check creates its own and never adopts one, so it does not push the image tag into it or delete it. Nothing was written. If it is a leftover of an earlier attempt of this check whose own Restore did not finish, look at it first (gcloud artifacts docker images list ${L}-docker.pkg.dev/${P}/${SVC} --include-tags --project=${P}) and delete it yourself if it holds nothing but that attempt's image; otherwise give this check a different name (SVC in step 1). Then re-run the step 1 block and this one." >&2; exit 1; }
else
  # The record goes down BEFORE the create, not after it: a run that dies
  # between the two — a Ctrl-C, a dropped connection, a kill — has still made a
  # repository, and this record is what tells the Restore it is this check's to
  # delete. A create that FAILED and really made nothing takes it back again
  # (ask the project with the list above; a repository that IS there keeps
  # its record and the Restore deletes it).
  : > "$CHK91/repo-mine"                    # hard stop 5 tolerates this repository from here on
  gcloud artifacts repositories create "${SVC:?}" --project="${P:?}" --location="${L:?}" \
    --repository-format=docker \
    --description="throwaway: Check 91's image repository (fugaro-chk91), deleted by its own Restore" --quiet \
    || { echo "Check 91 stopped: the repository create failed — see above whether ${SVC} is there now; if it is, the record above stands and step 7's Restore deletes it" >&2; exit 1; }
fi
[ -n "$(gcloud artifacts repositories list --project="${P:?}" --location="${L:?}" --filter="name~/repositories/${SVC}\$" --format='value(name)')" ] \
  || { echo "Check 91 stopped: no registry repository ${SVC} in ${P}/${L} for the image tag ${IMG} to build into" >&2; exit 1; }
gcloud builds submit --project="${P:?}" --config="$CHK91/cloudbuild.yaml" \
  --service-account="projects/${P}/serviceAccounts/${BUILD_SA:?}" "$CHK91"
# Same rule as the repository's record: an interrupted deploy is the ordinary
# way to be left with a billed service, so the record is written BEFORE the
# deploy, and a deploy that failed and left no service takes it back.
: > "$CHK91/service-mine"                   # hard stop 4 tolerates this service from here on
gcloud run deploy "${SVC:?}" --project="${P:?}" --region="${L:?}" --image="${IMG:?}" \
  --no-allow-unauthenticated --timeout=3600 --min-instances=0 --max-instances=1 \
  --set-env-vars SSE_MINUTES=20 --quiet \
  || { echo "Check 91 stopped: the deploy failed — check 'gcloud run services describe ${SVC}' before trusting the record above" >&2; exit 1; }
URL=$(gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" --format='value(status.url)')
gcloud auth print-identity-token >/dev/null || { echo "Check 91 stopped: no identity token can be minted here" >&2; exit 1; }
[ -n "$URL" ] || { echo "Check 91 stopped: no URL" >&2; exit 1; }
```

**FACT:** that the service is `--no-allow-unauthenticated` (an unauthenticated `curl` must be refused), its `--timeout`, its min/max instances, and the image digest Cloud Run reports. **The identity token is never pasted**, only used — and every request below mints its own (the guard's `idt`), because one minted here expires inside the hour.

**The repository in this step is hard stop 5's, and it is the only thing the push can land in.** The baseline wrote `no` because nothing of this name was in the project when the check started, and this step re-reads that record before the build: if it says anything else the step stops, and if a `fugaro-chk91` is there that this step did not create itself — the check records its own in `$CHK91/repo-mine` before the create — the step stops rather than pushing into it. That is what keeps the `list || create` from adopting a repository the installation made between the baseline and here. Record the stop and report it: nothing of this step's runs after it.

**`gcloud builds submit` can offer to create something, and the check does not accept.** Cloud Build stages the source in the project's own staging bucket, `gs://<project>_cloudbuild`, and it creates that bucket if it is not there; on a project whose infrastructure is Terraform-managed it may also offer to enable an API. **Both of those are the installation's, not this check's, and neither is in the Terraform that manages this project** — so if `gcloud builds submit` asks to create the `<project>_cloudbuild` bucket, to enable an API, or to grant anything, **say no, stop the check here and report it**: do not accept on the owner's behalf. Record what it asked for, verbatim, and nothing further in this step runs. An already-present staging bucket needs nothing from you — but the tarball the upload leaves in it is this check's litter, so the build is pointed at a prefix of this run's own inside that bucket and step 7's Restore deletes exactly that prefix:

```bash
STAGING=gs://${P}_cloudbuild/chk91-source/${CHK91##*/}/
gcloud storage ls "gs://${P}_cloudbuild/" >/dev/null 2>&1 \
  || { echo "Check 91 stopped: gs://${P}_cloudbuild/ cannot be listed, and 'gcloud builds submit' would CREATE it to stage this run's source. A bucket is the installation's own — Terraform-managed, not this check's — so this check creates none and has no record by which to remove one. Fix, per INSTALLATION fugaro (${P}), run by YOU: give YOUR account read on it (gcloud storage buckets add-iam-policy-binding gs://${P}_cloudbuild --member=user:YOUR-EMAIL --role=roles/storage.objectViewer), or, if it does not exist, create it once (gcloud storage buckets create gs://${P}_cloudbuild --project=${P} --location=${L} --uniform-bucket-level-access). Then run this check again. Nothing was created, built or deployed." >&2; exit 1; }
gcloud builds submit --project="${P:?}" --config="$CHK91/cloudbuild.yaml" --service-account="projects/${P}/serviceAccounts/${BUILD_SA:?}" --gcs-source-staging-dir="$STAGING" "$CHK91" >&2 2>"$CHK91/build.err" || BUILD_RC=$?
```

**A build that fails says why, and the run stops there.** `gcloud`'s stderr is kept in the scratch directory, and the block carries a `build-error` row: the last 30 lines of it, with anything credential-shaped — an `Authorization` header, a signed URL's signature, a token — replaced by `<redacted>` before it goes in, because the block is pasted into a PR. The likely causes are named in the order to check them: the identity the build ran as cannot read the source this run staged at `$STAGING` (`storage.objects.get`; `roles/storage.objectViewer` on the bucket); it cannot WRITE the image to Artifact Registry (`artifactregistry.repositories.uploadArtifacts`; `roles/artifactregistry.writer` on the project, since the repository is created by the run); your account lacks `roles/iam.serviceAccountUser` on the build account; a grant read before the build has not taken effect yet — the grants are the installation's IAM, not this check's to change; the Cloud Build API is not enabled in the project; or a step of the Dockerfile failed. Nothing is deployed after a failed build, and the Restore still deletes the repository, the image and the staged source. The run that prompted this row stopped on exactly the first of those causes, and before it existed the block could not tell it from a broken Dockerfile.

**The bucket is checked before the build, and an unreadable one is a STOP — the script does not fall back to gcloud's default staging directory.** `gcloud builds submit` creates `gs://<project>_cloudbuild` when it is not there, and that bucket is the installation's: it is in the project's Terraform and not this check's, so a run that let gcloud make one would have created a project-level resource with no ownership record and no Restore that could delete it. The fallback was also worse in its own right — the tarball then cannot be named afterwards and stays for a bucket lifecycle rule to sweep away. The prefix is never a bucket-wide delete: it is `chk91-source/<this run's scratch name>/` and nothing else.

**The build runs as the installation's own build service account, never the default Compute one, and its grants are read before anything is created.** On `fugaro-dev` the default Compute account has had `roles/editor` removed (Check 26 step 6), so it holds nothing on the staging bucket or on Artifact Registry. **The recommended account is a dedicated one, `fugaro-chk91-build@fugaro-dev.iam.gserviceaccount.com`**, made for this check, because granting a product `fugaro-b-*` account `roles/artifactregistry.writer` on the whole project would also let it write `fugaro-base` and every other repository (those images run holding the repositories' credentials). The script prints the commands, per installation `fugaro` (`fugaro-dev`), for YOU to run: `gcloud iam service-accounts create fugaro-chk91-build --project=fugaro-dev --display-name='Check 91 throwaway build account'`, `roles/artifactregistry.writer` on the project, `roles/storage.objectViewer` on `gs://fugaro-dev_cloudbuild`, optionally `roles/logging.logWriter`, and `roles/iam.serviceAccountUser` on the account for your own user unless you are a project owner (the script reads this too, from the account's policy and the project's, counting `roles/iam.serviceAccountUser`, `roles/owner` and `roles/editor`; `roles/iam.serviceAccountAdmin` and `roles/iam.serviceAccountTokenCreator` do not carry `iam.serviceAccounts.actAs`, and a conditional grant is not counted. When it cannot find the right it prints a WARNING with the exact grant command and carries on, because a grant through a group, folder or organization is not visible there; if the right is really missing, the `build-error` row at `builds submit` names it); then run `CHECK91_BUILD_SA=fugaro-chk91-build@fugaro-dev.iam.gserviceaccount.com bash scripts/live-checks/check91.sh`. Without `CHECK91_BUILD_SA` the script lists the project's `fugaro-b-*` accounts (the per-repository build accounts) and uses the one there is, printing the broader-grant warning; it stops if there is none — it never falls back to the Compute account. At the end it lists, as an OPTIONAL step for you that it never runs, `gcloud iam service-accounts delete fugaro-chk91-build@fugaro-dev.iam.gserviceaccount.com --project=fugaro-dev` (preceded by the removal of its bindings, with `--condition=None` on the project ones: the bindings go first, because after the account is deleted its members read `deleted:serviceAccount:...` and the removes fail). Before any write it prints which account that is and the roles it needs, reads the project's and the bucket's IAM policy, and, for each missing grant, prints a copy-paste command labelled per installation, for YOU to run: `roles/artifactregistry.writer` on the project (project-level, because the repository does not exist until the run creates it; revoke it afterwards, or delete the dedicated account), `roles/storage.objectViewer` on `gs://<project>_cloudbuild`, and `roles/logging.logWriter` (without it the build still runs, but its logs are not written). The build uses a small `cloudbuild.yaml` with `options.logging: CLOUD_LOGGING_ONLY`, as the product's own builds do, so no logs bucket is created. The script never grants IAM. `--dry-run` prints the commands a real run would run, the Restore's cleanup commands, and which parts needed the network.

### Check 91 step 4: cold start (U5)

`--min-instances=0` lets the instance go, but not on a five-minute timer: Cloud Run keeps an idle instance far longer than that, and this project's own cold-start check (Check 41) waits **at least 35 minutes** between runs to be sure the container is new. So this step waits 35 minutes too — and then **does not assume** the instance went: the relay's own `/` body carries the time its process started (`started=<utc>`), the step reads it before the wait and again from the cold request, and a cold figure is one where the two differ. If they do not, the request was served by the same instance and the figure is a **warm** one: it is recorded as warm, not as U5's cold start, and the step is repeated after a longer idle.

```bash
BEFORE=$(curl -sS --config "$AUTH_CONF" "${URL:?}/" | sed -n 's/^started=//p')
[ -n "$BEFORE" ] \
  || { echo "Check 91 stopped: the relay's / body carries no started= line, so this is not this check's image — redeploy step 3" >&2; exit 1; }
echo "instance now: started=${BEFORE}; waiting 35 minutes for it to be released"
sleep 2100                                   # 35 minutes: Check 41's own gap on an idle container
echo -n "cold: "; curl -sS -o "$CHK91/cold.out" -w '%{time_starttransfer}s to first byte, %{time_total}s total\n' --config "$AUTH_CONF" "${URL:?}/"
cat "$CHK91/cold.out"
echo -n "warm: "; curl -sS -o /dev/null -w '%{time_starttransfer}s to first byte, %{time_total}s total\n' --config "$AUTH_CONF" "${URL:?}/"
AFTER=$(sed -n 's/^started=//p' "$CHK91/cold.out")
[ "$AFTER" = "$BEFORE" ] \
  && echo "Check 91: NOT a cold start — the instance started at ${AFTER} answered the request after the wait, so the figure above is warm. Record it as warm and repeat this step after a longer idle." >&2
```

**FACT (U5):** both time-to-first-byte figures and both totals, with the wait you allowed before the first, the `started=` value before and after the wait, and the `--min-instances`/`--max-instances` values in force. **A cold figure is one whose two `started=` values differ** — a new instance served it; if they are equal, what was measured is warm, say so, and repeat. This is the number the spec's "about a second" is checked against.

### Check 91 step 5: a long stream, and whether it arrives unbuffered (U5)

```bash
curl -sS -N --max-time 30 --config "$AUTH_CONF" "${URL:?}/sse" \
  | while IFS= read -r line; do [ -n "$line" ] && printf '%s %s\n' "$(date -u +%H:%M:%S)" "$line"; done
```

**FACT:** the timestamps of the first events — one a second is the pass (the stream is unbuffered through the front end and through the relay), a single burst at the end is the failure — and the `X-Relay` header.

Then the longest stream that completes, raising `SSE_MINUTES` toward the service's own `--timeout=3600`. **This is the request that outlives a token** — mint a fresh one for it, as below, and mint another for each probe after it: a token minted once in step 3 is expired by the time this stream ends, and every request after it would answer `401`, which reads as the size limit step 6 is measuring rather than as an auth failure:

```bash
gcloud run services update "${SVC:?}" --project="${P:?}" --region="${L:?}" --set-env-vars SSE_MINUTES=59 --quiet
# --max-time of its own, longer than the SSE and shorter than nothing: without
# a cap this request outlives the run it belongs to, and a Ctrl-C has nothing
# to end. curl exits 18/56/52 when the front end cuts the stream, and that IS
# the measurement.
time curl -sS -N -o /dev/null -w '%{http_code} after %{time_total}s\n' --max-time 3840 --config "$AUTH_CONF" "${URL:?}/sse"
```

**FACT (U5):** the longest stream that completed, and the status and time of the first one that did not — whether the front end cuts at the service's timeout, at a fixed 60-minute cap, or at something else. **This step is the one that costs wall-clock time** (an hour at the top of the range), and it needs the SSE rather than a longer fake: the stream is the request.

### Check 91 step 6: the request-body limit, HTTP/1.1 against HTTP/2 to the front end (U5)

**Read this before the two rows: the second one is not end to end, and U5's end-to-end HTTP/2 half is not settled by this check.** Cloud Run speaks HTTP/2 to the **container** only when the service is deployed with **`--use-http2`** *and* the container serves cleartext HTTP/2 (**h2c**) on `$PORT`; this check deploys neither (the relay is Python's `http.server`, which speaks HTTP/1.1 only), so what its second row measures is the **front end** accepting an HTTP/2 request over TLS (ALPN) and translating it — the service's ingress path above the container, not a request that is HTTP/2 from `curl` to the relay. **A check that settles the other half has to deploy with `--use-http2` and replace the relay with a container that serves h2c** (an ASGI server with HTTP/2 enabled, e.g. `hypercorn`, in place of `http.server`), keeping the same `/echo` behaviour; until that is deployed and run, label these two rows as *HTTP/1.1 end to end* and *HTTP/2 only to the front end*, never as the end-to-end pair, and leave **U5's end-to-end HTTP/2 half UNVERIFIED**. The check believes 32 MiB on HTTP/1.1 and no limit on end-to-end HTTP/2; what this step can confirm or correct is the first of those, and the front end's own behaviour above it.

**The first row pins HTTP/1.1 (`--http1.1`) and the second pins HTTP/2 (`--http2`), and both rows print `%{http_version}`** — the version `curl` actually used. The second row is only an HTTP/2 row if the client can speak it, and a `curl` built without HTTP/2 does not fail on `--http2`: it degrades to HTTP/1.1 silently, and the row would then be a copy of the first while the FACT claimed to record the front end's HTTP/2 behaviour. So the step stops before the loop when the client has no HTTP/2, and the recorded `%{http_version}` is what proves which row is which. **The script settles that question FIRST, before a repository, a build or a deploy exists** — a curl without HTTP/2 makes the second row meaningless, and finding it out after the 35-minute cold-start wait and the hour-long stream would waste both; done by hand, run the check below the moment the guard passes.

```bash
curl -V | grep -q 'HTTP2' || { echo "Check 91 stopped: this curl has no HTTP/2 support — nothing was created, built or deployed" >&2; exit 1; }
```

```bash
curl -V | grep -q 'HTTP2' \
  || { echo "Check 91 stopped: this curl has no HTTP/2 support, so --http2 would silently send HTTP/1.1 and the second row would duplicate the first — use a curl built with HTTP/2, or record this step as HTTP/1.1 only" >&2; exit 1; }
for n in 1048576 16777216 33554432 67108864 134217728; do
  head -c "$n" /dev/zero > "$CHK91/body.bin"
  echo -n "$n bytes, HTTP/1.1 end to end:        "
  curl -sS --http1.1 -o "$CHK91/echo.out" -w '%{http_code} %{size_upload} uploaded over HTTP/%{http_version}, relay said: ' \
    --config "$AUTH_CONF" -X POST --data-binary @"$CHK91/body.bin" "${URL:?}/echo"
  cat "$CHK91/echo.out"
  echo -n "$n bytes, HTTP/2 to the front end:    "
  curl -sS --http2 -o "$CHK91/echo.out" -w '%{http_code} %{size_upload} uploaded over HTTP/%{http_version}, relay said: ' \
    --config "$AUTH_CONF" -X POST --data-binary @"$CHK91/body.bin" "${URL:?}/echo"
  cat "$CHK91/echo.out"
done
rm -f "$CHK91/body.bin" "$CHK91/echo.out"
```

**FACT (U5):** for each size, on **each protocol**, the status, the version `curl` actually used (`%{http_version}` — the first row reads `1.1`, and a second row that also reads `1.1` is the client degrading, not the front end's HTTP/2 behaviour), the bytes `curl` uploaded **and the relay's own count** — the body the relay itself printed, `read N bytes`, kept in `$CHK91/echo.out` and printed beside the status line, which is what proves the whole body arrived and not only the headers; and the exact text of the first refusal on each protocol. A row whose `curl` status is `401` is **not** a size limit: it is an expired identity token, and the row is re-run with a fresh one. `--data-binary @file` sends a `Content-Length`, which is what makes the two comparable; a chunked upload is a different question and this check does not ask it.

### Check 91 step 7: Restore

```bash
# Ownership first, and it is checked rather than assumed: the baseline writes
# `no` only when it found no repository of this name, and it STOPS the check
# otherwise, so anything else here means this record is not this check's and
# nothing below it may run. Read before the scratch directory that holds it.
REPO_MINE=$(cat "$CHK91/repo-existed")        # read it before the scratch dir goes
[ "$REPO_MINE" = no ] \
  || { echo "Check 91 stopped: $CHK91/repo-existed does not say the repository ${SVC} was absent when this check started (it says '$REPO_MINE'), so it is not this check's to delete. Nothing was deleted; remove the service and the image by hand only if you know they are yours, and leave the repository alone." >&2; exit 1; }
gcloud run services delete "${SVC:?}" --project="${P:?}" --region="${L:?}" --quiet
gcloud artifacts docker images delete "${IMG:?}" --project="${P:?}" --delete-tags --quiet
gcloud artifacts repositories delete "${SVC:?}" --project="${P:?}" --location="${L:?}" --quiet
# The build's staged source, removed from the prefix of this run's own (see
# step 3). Only that prefix: never a bucket-wide delete. A build is only ever
# started with that prefix — an unlistable staging bucket stops the run before
# the build — so an attempted build always has one to remove.
rm -f "$AUTH_CONF"                          # the minted identity token goes before the directory
gcloud storage rm -r "gs://${P}_cloudbuild/chk91-source/${CHK91##*/}/" 2>/dev/null || true
```

Then the assertion, **every count of which must read `0`** — and the scratch directory is removed **only when all three do**:

```bash
SVC_LEFT=$(gcloud run services list --project="${P:?}" --region="${L:?}" --filter="metadata.name=${SVC}" --format='value(metadata.name)' | wc -l)
IMG_LEFT=$(gcloud artifacts docker images list "${L}-docker.pkg.dev/${P}/${SVC}" --project="${P:?}" --include-tags --format='value(image)' 2>/dev/null | wc -l)
REPO_LEFT=$(gcloud artifacts repositories list --project="${P:?}" --location="${L:?}" --filter="name~/repositories/${SVC}\$" --format='value(name)' 2>"$CHK91/repo-count.err" | wc -l)
# The repository list SWALLOWS a server error: `WARNING: Failed to list repositories ...` on
# stderr, nothing on stdout, exit 0 — which `wc -l` would read as 0. That line means the
# count was not read: the block stops and keeps $CHK91, exactly as for a count that is not 0.
if grep -q 'Failed to list' "$CHK91/repo-count.err"; then
  cat "$CHK91/repo-count.err" >&2
  echo "Check 91 stopped: the repository list printed 'Failed to list repositories' and exited 0 — gcloud swallowed an API error, so the repository count was NOT read (an empty answer here is not a 0). $CHK91 is KEPT. Run the list again by hand until it answers without that line." >&2
  exit 1
fi
echo "services:   $SVC_LEFT"
echo "images:     $IMG_LEFT"
echo "repository: $REPO_LEFT"
# A count that is not 0 leaves the DIRECTORY IN PLACE: it holds service-mine,
# repo-existed and the record of which repository is the check's, which is what
# tells you by hand what is this check's and what is not. It does NOT let a
# fresh run of the check finish the job: a run makes a scratch directory of its
# own and stops in hard stop 4 or the baseline, because the service or the
# repository is still in the project. Finishing this is hand work, and the
# commands that survive are the three deletes above and the three lists below.
if [ "$SVC_LEFT" != 0 ] || [ "$IMG_LEFT" != 0 ] || [ "$REPO_LEFT" != 0 ]; then
  echo "Check 91 stopped: the counts above are not all 0, so something this check created is still there (services=$SVC_LEFT images=$IMG_LEFT repository=$REPO_LEFT). $CHK91 is KEPT on purpose — it holds the records that say what is this check's — so nothing was removed. Delete the survivors by hand (the service, its image, and the repository if $CHK91/repo-existed says no), then run the three list commands above until all three counts read 0, and remove $CHK91 yourself when you no longer need the records." >&2
  exit 1
fi
# The recursive delete names only the scratch directory hard stop 3 made, and
# both guards are re-checked HERE, immediately before it.
case "$CHK91" in
  ""|/|"$HOME"|"$HOME"/) echo "Check 91 stopped: refusing to remove a scratch directory of '$CHK91'" >&2; exit 1;;
esac
[ -f "$CHK91/.chk91-mine" ] \
  || { echo "Check 91 stopped: $CHK91 carries no marker from this check — nothing was removed" >&2; exit 1; }
rm -rf "$CHK91"
rm -f "$CHK91_RECORD"
```

**FACT:** the three counts, which must all read `0` before the scratch directory goes — and, beside them, that the prefix of this run's own in the staging bucket is empty (`gcloud storage ls` on it answers "matched no objects"; that message, and not the exit status, is what says it is gone, because the CLI exits non-zero for a prefix with nothing under it) — a non-zero one is the failure, and the directory is then **kept**, with the stop's message saying so, because it holds the ownership records that say which service, image and repository are this check's — the records the hand work below is done from, and the only copy of them. The first two count this check's own service and image; the third counts the repository, which the check created (the baseline stopped it otherwise), so a surviving repository is this check's too and the manual delete above names it. Beside them, `restore-token-file` reports the 0600 curl config file that held the last minted identity token: `PASS` only when it is not on disk, because that file lives in this same directory and a directory that is kept must not keep a live token with it. **A count that could not be READ is not a count of `0`, and the script treats the two differently:** `gcloud … | wc -l` prints `0` for a query that printed nothing, and a query that FAILED printed nothing too — an identity token that expired late in the run, a transient API error, and the deletes above (guarded the same way) can fail together — so reading a failure as a count would print `restore: PASS`, exit `0` and remove the records that say what is this check's. The script reads each query's own exit status instead, records `restore-counts: FAIL` naming which query failed and with what status, keeps the directory and ends the block at exit `1`. The one failure that is not a failed query is the image list: the repository it names was just deleted, so a `NOT_FOUND` there IS the answer, and the script asks the image question only while the repository list succeeded and says a repository is still there.

**Passes when:** the guard's hard stops held and every request minted its own identity token; the relay ran with an image this check built, from a service that refuses unauthenticated calls; the cold and warm time-to-first-byte figures are recorded with the `started=` values that show a **different** instance answered the cold request (a pair that matches is recorded as a warm figure and repeated, not written into U5 as a cold start); the SSE events arrived one a second through the front end rather than in a burst; the longest completed stream and the first refusal are recorded; the largest body accepted on **HTTP/1.1** and the front end's own behaviour on **HTTP/2 to it** are recorded, each row with the relay's own byte count; and the Restore's three counts each read `0` with the staged source gone from the prefix this run gave it, its scratch directory removed only after that. **Passing does not settle U5's end-to-end HTTP/2 half** — that row is not an end-to-end HTTP/2 request, and step 6 says what a check that settles it needs.

**Fails when:** any of those is not what you saw. A stream that arrives in a burst, or a body that is accepted on HTTP/1.1 above what the check believed, is a **finding about U5, not a failure of the check** — record it, because the service's forwarding is written against these numbers, and the reconciliation's F31 consequence changes with them (a per-request timeout binds a call and not a review; the size risk is one large request). A `401` on a probe is neither: it is an expired identity token, and that row is repeated with a fresh one before anything is concluded from it. A non-zero count in the Restore's assertion is a failure of the check itself and stops it — and it **keeps** the scratch directory, on purpose, so the records that say what is this check's are still there: delete the survivors by hand (the count messages name them), run the three list commands until all three counts read `0`, and remove the kept directory yourself once you no longer need the records. **Finishing this is hand work:** the check's own Restore has already run and ended with the run, and a fresh run of the check cannot finish it — it makes a scratch directory of its own and stops in its guards or at its baseline while the service or the repository is still in the project. **A repository named `fugaro-chk91` that is already in the project stops the check at its baseline**, before anything is created or pushed: this check never adopts, writes into or deletes a registry repository it did not create, so that stop is the check's answer and not something to work around in the session.

**Paste back:** `fugaro version` and the service's configuration (timeout, min/max instances, image digest); the cold and warm figures with the wait before the first and the two `started=` values; the streaming timestamps and the `X-Relay` header; the longest completed stream and the first refusal; the body-limit table with `curl`'s and the relay's own byte counts per row, labelled as the front end's HTTP/2 against HTTP/1.1 end to end; and the Restore's three counts with the answer for the staged-source prefix. **Never paste the identity token**, and never a URL's query string if one appears. Until this check runs, U5 is **UNVERIFIED** and its row above stays **NOT RUN**; after it runs, **U5's end-to-end HTTP/2 half is still UNVERIFIED**, and the plan's forwarding is written against that row knowing it is open.
