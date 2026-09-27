# M0 — Cloud Run Feasibility Spike (runbook)

> **This is a spike.** Nothing built here ships. The output is a filled-in `docs/design/m0-results.md` and a go/no-go decision on Cloud Run jobs as the compute layer. Design: [docs/design/v1.md](../design/v1.md) §14.

> **Status (2026-09-27): split in two.** v1 ships web-first (design §14).
> - **Now:** run **Step 0 only**, against the **web** repo, before the `web-node` image is built (M3). A hit there means the web workflow also needs the Docker-capable backend.
> - **Later:** run the full runbook against the **server** repo before any server-workflow work starts.

**Questions to answer, most dangerous first:**

1. **Does the server test suite need Docker?** Cloud Run jobs have no Docker daemon and no privileged mode, so Testcontainers, docker-compose, and embedded-Docker test fixtures can't run there. If the suite depends on Docker, Cloud Run is out for the server workflow, whatever the memory ceiling.
2. **Does peak memory of build plus test fit in one task?** The believed ceiling is 8 vCPU / 32 GiB. Confirm it against the current [Cloud Run jobs limits](https://cloud.google.com/run/docs/configuring/jobs/memory-limits) before starting.
3. **How long does a warm run take** (image start, then build, then test) compared with the same commands on an unloaded laptop?

Answer 1 before spending anything on 2 and 3.

## Step 0 — Docker dependency check (local, 5 minutes)

In the server repo:

```bash
git grep -nIi -E 'testcontainers|docker-compose|DockerClient|dockerjava|@Container\b' -- '*.gradle*' '*.toml' '*.java' '*.kt' '*.yml' '*.yaml' | head -50
```

- **No hits:** go on to Step 1.
- **Hits:** write down which modules and tests use Docker, and how long those tests take. Then stop and bring the list back to the design discussion. The alternatives are **Cloud Batch** (Compute Engine VMs, where Docker works and larger machines are available), **GKE Autopilot jobs**, or splitting the Docker-dependent tests out of the offloaded loop. That decision changes §3 of the design.

## Step 1 — Variables

```bash
export PROJECT=<gcp-project-id>
export REGION=<region, e.g. europe-west1>
export AR=fugaro-spike
export IMAGE=$REGION-docker.pkg.dev/$PROJECT/$AR/spike:1
export JDK=<server repo's JDK major, e.g. 21>
export CMD='./gradlew --no-daemon build'   # the full build + test command you run locally
```

## Step 2 — Enable APIs and create a registry

```bash
gcloud config set project "$PROJECT"
gcloud services enable run.googleapis.com artifactregistry.googleapis.com logging.googleapis.com
gcloud artifacts repositories create "$AR" --repository-format=docker --location="$REGION"
gcloud auth configure-docker "$REGION-docker.pkg.dev"
```

## Step 3 — Spike image

In a scratch directory (not this repo), create a file `cloneurl.txt` containing an HTTPS clone URL with a read-only token embedded (for example `https://x-token-auth:<token>@bitbucket.org/<ws>/<repo>.git`). It is passed as a BuildKit secret, so it never lands in an image layer. Delete the file afterwards.

If the build needs private-registry credentials (Artifactory and so on), add them the same way as extra `--secret`s, and export them in the `RUN` line that warms the dependencies.

`Dockerfile`:

```dockerfile
ARG JDK=21
FROM eclipse-temurin:${JDK}-jdk
RUN apt-get update && apt-get install -y --no-install-recommends git procps && rm -rf /var/lib/apt/lists/*
RUN --mount=type=secret,id=cloneurl git clone --quiet "$(cat /run/secrets/cloneurl)" /work/repo \
 && git -C /work/repo remote remove origin
WORKDIR /work/repo
RUN ./gradlew --no-daemon dependencies > /dev/null
COPY spike.sh /spike.sh
ENTRYPOINT ["/bin/bash", "/spike.sh"]
```

`spike.sh`:

```bash
#!/bin/bash
set -uo pipefail
cd /work/repo
echo "spike: nproc=$(nproc) mem_total_mib=$(free -m | awk '/Mem:/{print $2}')"
peak=0
( while sleep 5; do free -m | awk '/Mem:/{print $3}'; done ) > /tmp/mem.log &
sampler=$!
start=$(date +%s)
bash -c "$CMD"; status=$?
end=$(date +%s)
kill "$sampler"
peak=$(sort -n /tmp/mem.log | tail -1)
echo "spike: status=$status wall_s=$((end - start)) peak_used_mib=$peak"
[ -r /sys/fs/cgroup/memory.peak ] && echo "spike: cgroup_memory_peak_bytes=$(cat /sys/fs/cgroup/memory.peak)"
exit $status
```

Build and push. Cloud Run runs linux/amd64 only:

```bash
docker buildx build --platform linux/amd64 --build-arg JDK="$JDK" \
  --secret id=cloneurl,src=cloneurl.txt -t "$IMAGE" --push .
rm cloneurl.txt
```

## Step 4 — Job and two executions

```bash
gcloud run jobs create fugaro-spike --image "$IMAGE" --region "$REGION" \
  --cpu 8 --memory 32Gi --task-timeout 2h --max-retries 0 \
  --execution-environment gen2 --set-env-vars "CMD=$CMD"

time gcloud run jobs execute fugaro-spike --region "$REGION" --wait   # cold: first image pull
time gcloud run jobs execute fugaro-spike --region "$REGION" --wait   # warm
```

Read the `spike:` lines:

```bash
gcloud logging read 'resource.type="cloud_run_job" AND resource.labels.job_name="fugaro-spike" AND textPayload:"spike:"' \
  --freshness 1d --format 'value(timestamp,textPayload)'
```

Also open the job's **Metrics** tab in the console and note the peak **Memory utilization**, which is the platform's view of the same number.

If an execution dies with an out-of-memory signal (exit 137, or "Memory limit exceeded" in the logs), record that. It is the answer to question 2.

## Step 5 — Local baseline

On the laptop, with nothing else heavy running, run the same `CMD` in a fresh clone and time it:

```bash
/usr/bin/time -l bash -c "$CMD"   # macOS; on Linux: /usr/bin/time -v
```

## Step 6 — Record results

Create `docs/design/m0-results.md`:

```markdown
# M0 results — <date>

| | Cloud Run (cold) | Cloud Run (warm) | Laptop |
|---|---|---|---|
| Build + test wall time | | | |
| Peak memory used | | | n/a |
| Exit status | | | |

- Docker-dependent tests: none | <list>
- Image size: <from Artifact Registry>
- Cloud Run limits confirmed on <date>: <vCPU> vCPU / <GiB> GiB per task, <h> h max task timeout
- Decision: GO | NO-GO (<reason>)
```

**Go criteria (all must hold):**

- There are no Docker-dependent tests, or they can reasonably be excluded from offloaded runs.
- Peak memory is ≤ 26 GiB, which leaves at least 20% headroom under 32 GiB.
- Warm wall time is ≤ 1.5× the laptop's unloaded time. Cloud Run's win is parallelism, not raw speed, but it can't be much slower.

## Step 7 — Clean up

```bash
gcloud run jobs delete fugaro-spike --region "$REGION" --quiet
gcloud artifacts repositories delete "$AR" --location "$REGION" --quiet
```

Optional: repeat Steps 3 and 4 for the web repo, using `node:<lts>` as the base, `pnpm install --frozen-lockfile` (or `npm ci`) in place of the Gradle warm-up, and 4 vCPU / 16 GiB. The web workflow is much less likely to hit either limit.
