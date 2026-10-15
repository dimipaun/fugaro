# Implementation plan: verify the base image at copy time

> **Status: PARTLY SUPERSEDED.** This plan's premise — that `fugaro init --base` / `image refresh` keeps *copying* the base image into the installation's registry — is replaced by [docs/design/registry-remote-repository.md](../design/registry-remote-repository.md) and its plan [docs/plans/2026-10-14-registry-remote-repository.md](2026-10-14-registry-remote-repository.md): the base is resolved, its signature verified, read back through a new Artifact Registry remote repository and **pinned** there, with nothing copied. Read every task below through that, and in particular the newer plan's own section "Changes to #318's plan", which is the authority for what survives:
>
> - **Task 1** (CI — sign the pushed base image by OCI digest, the `-pinned` tag, the `cosign-installer` pin): **kept, unchanged, and a precondition of the newer plan** — it is what the resolve step verifies. Its rollout precondition (the workflow file must be on `main` before the release's version-bump merge commit is created and tagged) applies verbatim.
> - **Task 2** (`internal/basepin` + `mirror.ResolveDigest`): **kept, amended** — `basepin.Resolve` returns the **index** digest to its caller, and the reference it feeds is the remote path (the newer plan's Tasks 2/3), not a ghcr.io one.
> - **Task 3** (the copy gains a verification pre-step): **dropped entirely** — its whole premise is that a copy still happens; the call site it edits (`internal/cli/init_images.go`'s `read()`, `mirror.Plan`) is replaced by the newer plan's Task 3.
> - **Task 4** (CLI confirmation text, "still copies"): **dropped entirely**, replaced by the newer plan's Task 5.
> - **Task 5** (docs): **kept in spirit, rewritten** — the newer plan's Task 6.
> - **Task 6** (full sweep): **kept** — the newer plan's Task 7.
> - **Task 7** (live checks 39a–39f): **amended — by the newer plan, not here.** 39a, 39c and 39e stand. 39a's Expected text **is amended** — by the newer plan's Task 6, in this branch — to *"and the pinned reference is the remote repository's path at that digest; nothing is copied"* (there is no copy: the same command verifies the same digest and pins the remote path instead), and the row below now reads that way; the checklist's numbering ruling and the newer plan's Task 8 record the amendment. **39b is dropped** (it asserts the copy lands from the `-pinned` tag — there is no copy) and **39f is superseded by Check 43f** of [docs/gcp-live-checklist.md](../gcp-live-checklist.md) (the same round trip against the new path); those two rows below are likewise left as they were written, and the checklist's numbering ruling names both and says to read them through it. 39d stays out of scope.
>
> The design it implements, [docs/design/retire-image-mirror.md](../design/retire-image-mirror.md), is likewise kept as the historical design of the copy-plus-verification; this change adds the one status line at its top pointing at the newer design. This document stays the record of the copy's design, not a description of what ships. **The banner above adds twelve lines at the top of this file and two at the top of that design**, so the absolute line citations other documents made into them are re-pointed in place with it: this file's Task 1 quote `:11` → `:23`; 39a's Expected text `:562` → `:574` (amended by the newer plan's Task 6 to *"…the pinned reference is the remote repository's path at that digest; nothing is copied"*); 39b `:563` → `:575`; 39e `:566` → `:578`; 39f `:567` → `:579`; the design's §4 quote `:60` → `:62`. The clauses that said the design carries no status line pointing at the newer design are updated with them.

Source: [docs/design/retire-image-mirror.md](../design/retire-image-mirror.md). Depends on the base-image hard cut (`docs/plans/2026-10-08-base-image.md` Tasks 17–21, release 0.7.0) having already landed: `config.Bases == ["base"]`, one `base_images` key. **This plan is not executed in this change** (docs only); it is handed to the owner to rule on and, once approved, implemented task by task.

**A note on this revision (fifth review round — a narrowing, scoped down from the fourth round's own course correction).** A fourth review found that the daily image check job is itself a Cloud Run Job running directly FROM the base image (`check.tf`, `internal/infra/spec.go`'s `check()`), and Cloud Run Jobs cannot deploy a bare `ghcr.io` reference (not verified live, assumed conservatively) — so `base_images.base` must keep its existing, Artifact-Registry-hosted shape, used exactly as today by the check job and `FUGARO_CHECK_SPEC`. That round's own fix, though, still had **Cloud Build** resolve a verified `ghcr.io` digest fresh at every build submission while the check job kept reading the Artifact Registry copy — leaving two independently-maintained "what base was this built from" references. A fifth review found this breaks rebuild detection: `submitRebuild` (`internal/cli/imagecheck.go:690-692`), `refreshVerdict` (`internal/cli/image_refresh_rules.go:51-66`), and `triggers()` (`internal/imagecheck/triggers.go:34`) all compare `base_ref`/`base_images.base`-derived strings by exact equality, with no concept that a ghcr.io-shaped value and an Artifact-Registry-shaped value could name the same digest. **This revision narrows the scope to "verify-then-copy": Cloud Build and the check job both keep reading `base_images.base`, unchanged, exactly as today.** The design doc's earlier Tasks 4 (`cover.go`) and 5 (the Terraform grant) stay dropped — their premise was already false before this round, for the same reason — and what was Task 3's "Part A" (the three Cloud Build callers resolving fresh) is removed entirely: no build-submission call site changes at all. Task 3 is now exactly what Part B was: a verification pre-step ahead of the *existing*, unchanged copy mechanism, with its source fixed to the nightly-immune `-pinned` tag instead of the plain tag the nightly moves (a bug this round's review caught in the previous draft of Task 3, below).

## Global constraints

- **No live cloud or network in any automated test.** Every fake (`TagResolver`, `OCIVerifier`) is in-process; the real cosign signature and the real copy-time resolution against ghcr.io are the owner's live checks (Task 7), each marked NOT RUN until the owner runs it.
- **Never touch `belong`** (EdgeWeb's and EdgeServer's installation) in any live step. Every live check in Task 7 is scoped to `fugaro`/`fugaro-dev`, sandbox repository only, exactly as the existing entries of `docs/gcp-live-checklist.md` already are.
- **Nothing here weakens an existing guard.** `tf.Guard`, `internal/infra/tf/cover.go`, `managedVersion`/`isManaged`, the Terraform `build_base` grant — **none of these are touched by this plan at all** (design §4, §6).
- **Each task is independently reviewable**: a task's tests fail before its implementation and pass after.
- **Security-sensitive tasks are flagged for opus review** inline.
- **A real, owner-applied rollout precondition (design §5, §8), corrected: it's about which commit gets tagged, not a prior tag push.** `docs/pending-workflows/images.yml` (Task 1) can only reach the real `.github/workflows/images.yml` by a human manually copying it in. Once Task 3's code lands, **every** `fugaro init --base`/`image refresh` copy attempt depends on finding a signed `-pinned` image for the installed CLI's **own** version — not a Cloud Build submission (none is touched by this plan), and not any other release's signed image (`basepin.Resolve` only ever resolves the running CLI's embedded version's tag). GitHub Actions runs a tag push's workflow exactly as that file exists in the tagged commit, and `scripts/release.sh` always tags whatever commit is currently on `main` — so the real requirement is just: **the workflow file copy must be on `main` before that release's version-bump PR's merge commit is created and tagged.** Once it is, that release's own tag push runs the updated `images.yml` and signs its own `-pinned` image as part of the same push — there is no earlier, separate tag push to wait for ("at least one real tag push must have already run" was a circular, earlier-draft mistake). This can be the same release as Task 1, as long as the copy lands on `main` first. The release process cannot enforce this automatically; the owner applies it by hand. **A real, if ordinarily brief, window exists regardless:** `release.yml` (the CLI) and `images.yml` (`publish-base`, the sign) are independent workflows on the same tag push with no guaranteed ordering, so copies with a newly-published CLI can refuse until `publish-base` finishes signing; if its sign step fails outright, a maintainer can re-run that failed job, which **reuses the build artifacts that push uploaded** (kept one day, `retention-days: 1`) and runs them through create → sign → verify → `-pinned` again — it does not rebuild from the tagged source, and GitHub only offers re-runs for about 30 days, after which that release's `-pinned` image stays unsigned and the copy-time resolve fails loudly (design §5; corrected by PR #377's review, which found "rebuilds" wrong).
- **Docs match code.** `go test ./internal/cli -run 'Docs|Lint'` and `gofmt -l .` are run after every task.

## Task 1: CI — sign the pushed base image by OCI digest

**Flag for opus review:** this task is the root of the new trust chain (design §2) — a mistake in *which* identity the signature is checked against, or signing on a trigger other than a tag push, silently reintroduces "whoever can write the ghcr.io tag" as the trust anchor while the design doc claims otherwise.

**Scope, narrowed from earlier drafts: base only, not history.** History's copy mechanism is entirely unchanged by this design (design §1, §6) — nothing ever verifies it, so signing it would serve no purpose. An earlier draft of this document signed and tagged `fugaro-history` too; dropped here.

**Starting state:** `.github/workflows/images.yml` already carries the real, merged `fugaro-base`/`publish-base` jobs (commit `356f7b1`); `docs/pending-workflows/` holds only `README.md`. This task's first step stages a *fresh* `docs/pending-workflows/images.yml` as the current real file plus this task's own addition, per `docs/pending-workflows/README.md`'s own instructions (Fugaro cannot push a commit touching `.github/workflows/**`).

**Files:**
- Add: `docs/pending-workflows/images.yml` (a fresh staged copy of the real file plus the additions below)
- Modify: `images/base_workflow_test.go` (new pinning tests below), `docs/pending-workflows/README.md` (its `## images.yml` section rewritten wholesale to describe this diff)

**Interfaces:**
- `publish-base`'s `permissions` gains `id-token: write` (alongside its existing `contents: read`/`packages: write`) — needed **only** for the OCI signing step's keyless (OIDC) Fulcio certificate request. The `-pinned` tag push itself is an ordinary registry write, the same as `:$VERSION`/`:$MAJOR`, and needs only the already-granted `packages: write`; an earlier draft of this task said `id-token: write` was needed for "both," which overstates it. It is granted at **job** scope (GitHub grants permissions per job, not per step, and `publish-base` is the only signing job) and on **no other job**; a workflow-level `id-token: write` would hand it to the build-only jobs too.
- **The digest that gets signed is read from the create call's own `--metadata-file`, never from a tag** (PR #377 review H1). `docker buildx imagetools create` takes `--metadata-file FILE`, and the file's `containerimage.descriptor.digest` is the index it just published — that key, not `containerimage.digest`, is what docker's docs document for *this* command (`imagetools create`; `containerimage.digest` is `buildx build`'s — checked against docs.docker.com 2026-10-15). A missing or malformed value fails the job rather than signing something unknown. Re-reading the just-created `-pinned` tag instead (an earlier draft's approach, and a severe mistake) makes the signed digest whatever that tag names **at that moment**: anyone who can write this repository's tags — a leaked `packages: write` token is enough — could move it between the create and the re-read, and this workflow's own OIDC identity would then sign *their* image, which is exactly the trust anchor this design exists to remove.
- The index's own children are asserted against the two child digests **this job** pushed (`imagetools inspect --raw "$repo@$digest"`'s `.manifests[].digest` vs the two `RepoDigests` from the per-arch pushes), so a digest that is not this job's build is never signed whatever the metadata file says.
- A second, nightly-immune tag, `fugaro-base:<v>-pinned`, created **only after** the signature exists and has been verified (PR #377 review M1), and pointing at that same signed digest — `docker buildx imagetools create --tag "$repo:$VERSION-pinned" "$repo@$DIGEST"`, one index source, which docker's docs say is copied as-is (carbon copy, same digest) — followed by a read-back that fails unless the tag resolves to that digest. The nightly/`workflow_dispatch` runs, whose version-picking step resolves the identical `$VERSION`, must never create or move it: every step of the chain below carries `if: github.event_name == 'push'` exactly.
- `cosign sign --yes "ghcr.io/dimipaun/fugaro-base@$DIGEST"` for that digest (never a tag), and then `cosign verify` against the same digest with `--certificate-identity https://github.com/dimipaun/fugaro/.github/workflows/images.yml@refs/tags/v$VERSION --certificate-oidc-issuer https://token.actions.githubusercontent.com --certificate-github-workflow-trigger push --certificate-github-workflow-repository dimipaun/fugaro` (PR #377 review M3; the last two added by its re-review, M1 — the identity alone is also what a `workflow_dispatch` run against the same tag ref presents) — an independent read-back that fails the job on a wrong identity, a non-push trigger, or another repository. No `--insecure-ignore-tlog` or "verify without an artifact" mode anywhere.
- `sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2` — the exact, real pin `release.yml:95` already uses, not a placeholder; it is gated `if: github.event_name == 'push'` like the rest of the chain, and it runs before the sign step so `cosign` is on `PATH` there.

- [ ] **Step 1: Write the failing tests**

Two tests, both reading the **parsed YAML** (the fields GitHub executes — `id`, `uses`, `if`, `env`, `run`, `continue-on-error`, the job and workflow `permissions`, and the order the steps run in), never substrings in comments: a guard that survives only in a comment is not a guard, and reviewing PR #377 found eight mutations that string-matching tests let through. `images/base_workflow_test.go` holds the real ones; in shape:

```go
// signingProblems reads the parsed publish-base chain and names every way it
// can be weakened. TestPublishBaseSignsByOCIDigestOnPushOnly requires it
// empty for the real workflow; TestSigningProblemsFindEveryMutation feeds it
// one mutation at a time and requires each to be reported.
func signingProblems(wf imagesWorkflow) []string { /* ... */ }

func TestPublishBaseSignsByOCIDigestOnPushOnly(t *testing.T) {
	for _, p := range signingProblems(parseImagesWorkflow(t, readImagesWorkflow(t))) {
		t.Error(p)
	}
}
```

What `signingProblems` must catch (each mutation is one test case in `TestSigningProblemsFindEveryMutation`, on a clone of the real parsed workflow):

- the sign step signs a **tag** (`"$repo:$VERSION"`) instead of the digest handed to it, or takes its `DIGEST` from anything but `steps.index.outputs.digest`;
- the index step no longer takes the digest from `--metadata-file` (in particular, learns it by `imagetools inspect`ing a tag), or stops asserting the published index's children are the two digests this job pushed;
- `cosign sign ... || true` — or the same thing spelled `|| :`, `|| /bin/true`, `|| exit 0` — `continue-on-error: true` on any chain step, or `continue-on-error` at the **job** level on `publish-base`: a failed signature must fail the job;
- an insecure mode anywhere (`--insecure-ignore-tlog`, …);
- the sign, verify or `-pinned` step's `if` is not exactly `github.event_name == 'push'` (a widened condition signs or tags on an event this release's identity does not cover), or `-pinned` is made unconditional with the guard left in a comment;
- a workflow-level `id-token: write`, or `id-token: write` on any job but `publish-base`;
- the verify step's `--certificate-identity`, `--certificate-oidc-issuer`, `--certificate-github-workflow-trigger` or `--certificate-github-workflow-repository` missing or wrong (the last two are what make the first two mean *a tag push*), or the verify step dropped;
- `publish-base`'s own `if:` widened past its publish/kinds gate (to `always()`, say), or dropped;
- a floating `cosign-installer` pin (anything but the full commit SHA);
- **the step order**: index → sign → verify → `-pinned`, with the installer before sign. The `-pinned` tag must never precede the signature.

(`TestOnlyPublishJobsHoldPackagesWrite`, the real, already-existing test in this file, needs no change — this task adds no new job and widens no job's `packages: write`.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./images/ -run TestPublishBaseSignsByOCIDigestOnPushOnly -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Create `docs/pending-workflows/images.yml` by copying today's real `.github/workflows/images.yml` verbatim, then making these additions. The existing "Push per architecture and publish the index" step gains an `id: index` and a `--metadata-file`, and hands its digest out through `$GITHUB_OUTPUT` (separate Actions steps do **not** share shell variables — an earlier draft of this document made exactly that mistake, and a later one had the sign step re-read a tag to get the digest, which is the race H1 above); all other additions are new steps in the same job:

```yaml
      - name: Push per architecture and publish the index
        id: index
        # ... unchanged env and per-arch push loop ...
        # (--metadata-file is the only place the index's own digest comes
        # from: nothing here asks a tag what it resolves to.)
        # shellcheck disable=SC2086
        docker buildx imagetools create $tags $digests --metadata-file index.json
        index=$(jq -r '."containerimage.descriptor".digest' index.json)
        # ... validate it is sha256:<64 hex>, assert the published index's
        # .manifests[]?.digest set equals the two child digests this job
        # pushed, then:
        echo "digest=$index" >> "$GITHUB_OUTPUT"
      - uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2
        if: github.event_name == 'push'
      - name: Sign the published index
        id: sign
        if: github.event_name == 'push'
        env:
          DIGEST: ${{ steps.index.outputs.digest }}
        run: |
          set -eu
          repo=ghcr.io/dimipaun/fugaro-base
          cosign sign --yes "$repo@$DIGEST"
      - name: Verify the signature just attached
        id: verify
        if: github.event_name == 'push'
        env:
          DIGEST: ${{ steps.index.outputs.digest }}
          VERSION: ${{ needs.version.outputs.version }}
        run: |
          set -eu
          repo=ghcr.io/dimipaun/fugaro-base
          cosign verify \
            --certificate-identity "https://github.com/dimipaun/fugaro/.github/workflows/images.yml@refs/tags/v$VERSION" \
            --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
            "$repo@$DIGEST"
      - name: Publish the -pinned tag at the signed digest
        id: pinned
        if: github.event_name == 'push'
        env:
          DIGEST: ${{ steps.index.outputs.digest }}
          VERSION: ${{ needs.version.outputs.version }}
        run: |
          set -eu
          repo=ghcr.io/dimipaun/fugaro-base
          docker buildx imagetools create --tag "$repo:$VERSION-pinned" "$repo@$DIGEST"
          got=$(docker buildx imagetools inspect "$repo:$VERSION-pinned" --format '{{.Manifest.Digest}}')
          if [ "$got" != "$DIGEST" ]; then
            echo "::error::$repo:$VERSION-pinned resolves to $got, not the signed digest $DIGEST" >&2
            exit 1
          fi
```

(`docs/pending-workflows/images.yml` in this repository is the authority for the exact text; it also keeps the two-step comment lines that explain each of the above in place.)

`docs/pending-workflows/README.md`'s `## images.yml` section is replaced wholesale to describe this diff (the `--metadata-file` digest source, the create → sign → verify → `-pinned` chain, and the push-only gates) against the real, already-merged file.

**A cheap, recommended addendum, not required by this task:** extend `images/verify-public.sh`'s existing checks to also confirm `fugaro-base:<v>-pinned` is anonymously readable.

Run: `go test ./images/ -count=1`
Expected: PASS.

## Task 2: `internal/basepin` — resolve a tag and verify its OCI signature

**Flag for opus review:** this is new, security-critical code with no precedent in this repository; a bug that accepts an unverified or wrongly-identified signature as valid defeats the entire design. **Also flagged** because whether `sigstore-go`'s library API cleanly supports OCI-registry-attached signature verification is **unverified at design time** — the implementer confirms this before committing to the library route (design §9 Decision 1).

**Files:**
- Add: `internal/basepin/basepin.go`, `internal/basepin/basepin_test.go`
- Modify: `internal/mirror/mirror.go` (gains one small new exported function, `ResolveDigest`, reusing the package's existing, already-reviewed anonymous-registry-HTTP internals)
- Depends on (new go.mod entry, owner's call per design §9 Decision 1): either `github.com/sigstore/sigstore-go`'s `verify` package or shelling out to the real `cosign verify` binary.

**Interfaces:**

```go
// Package basepin resolves what a release's fugaro-base -pinned tag
// currently names in the registry and verifies that specific digest's
// OCI-attached cosign signature, from images.yml's own tag-push identity
// (design retire-image-mirror.md §2). It is called at copy time only
// (fugaro init --base / image refresh, internal/cli/init_images.go, Task
// 3) — never at a Cloud Build submission — and nothing records its
// result anywhere beyond the copy it feeds.
package basepin

// TagResolver resolves what source+"/fugaro-"+kind+":"+version+"-pinned"
// currently names in the registry — the same anonymous, digest-verified
// read internal/mirror already does today, against the nightly-immune
// "-pinned" tag, never the plain version tag a nightly rebuild moves.
// kind is the bare base kind ("base" — history is out of scope, Task 1),
// NOT pre-prefixed with "fugaro-": the real implementation builds the
// full image name itself, the same way mirror.Dest's callers already do
// everywhere else in this codebase. (An earlier draft of this document
// had Resolve's caller pass an already-"fugaro-"-prefixed name AND had
// Resolve itself prepend "fugaro-" again, producing
// "ghcr.io/dimipaun/fugaro-fugaro-base" — fixed here by fixing the
// contract, not patching around it at the call site.)
type TagResolver interface {
	ResolveDigest(ctx context.Context, source, kind, version string) (digest string, err error)
}

// CertIdentity is what a signing certificate must name. WorkflowRef and
// Issuer are NOT enough on their own (PR #377's review, M1): a
// workflow_dispatch run started against a tag ref gets the very same
// ".../images.yml@refs/tags/vX.Y.Z" subject, so a ref that looks like a
// release proves nothing about what triggered the run. Trigger and
// Repository are the claims that distinguish them — the Fulcio extensions
// 1.3.6.1.4.1.57264.1.2 ("event_name") and 1.3.6.1.4.1.57264.1.5
// ("repository") — and all four MUST be required. With the cosign binary
// that is exactly:
//   cosign verify \
//     --certificate-identity "$WorkflowRef" \
//     --certificate-oidc-issuer "$Issuer" \
//     --certificate-github-workflow-trigger "$Trigger" \
//     --certificate-github-workflow-repository "$Repository"
// (flag names checked against cosign v3's own cosign_verify docs; the
// extension OIDs against fulcio's docs/oid-info.md).
type CertIdentity struct {
	WorkflowRef string // "https://github.com/<owner>/<repo>/.github/workflows/images.yml@refs/tags/vX.Y.Z"
	Issuer      string // "https://token.actions.githubusercontent.com"
	Trigger     string // "push" — never a dispatch run against the same tag ref
	Repository  string // "<owner>/fugaro" — the same owner Resolve was given, never one read back from the registry
}

// OCIVerifier checks a specific digest's OCI-attached cosign signature
// against an identity. ref is "<source>/fugaro-<kind>@sha256:<hex>" — the
// verifier MUST pin its own artifact-digest policy to exactly this digest
// (never a "without artifact" / unsafe mode that accepts a signature for
// some other digest, design §9 Decision 1's "correctness beats convenience"
// framing). The real implementation wraps either sigstore-go's verify
// package or the cosign binary; tests use a fake.
type OCIVerifier interface {
	Verify(ctx context.Context, ref string, id CertIdentity) error
}

// Resolve resolves (via the "-pinned" tag), verifies and returns the
// pinned reference "<source>/fugaro-<kind>:<version>@<digest>" — the
// CLEAN version, no "-pinned" suffix anywhere in the returned string, so
// baseRefReleaseRE and every version-skew gate built on it keep parsing a
// plain "X.Y.Z" unmodified. owner/repoOnGitHub name the GitHub repository
// whose images.yml workflow is trusted (see "Forks and --image-source"
// below). Nothing is ever returned unverified.
func Resolve(ctx context.Context, tr TagResolver, v OCIVerifier, owner, repoOnGitHub, source, kind, version string) (string, error)
```

**Forks and `--image-source`.** `--image-source ghcr.io/<owner>` has always meant "a fork names its own source prefix" (`mirror.ValidSourcePrefix`, unchanged). The expected GitHub repository is `<owner>/fugaro` — the ghcr.io owner path segment of `--image-source`, reused as the GitHub owner, repository name assumed `fugaro` by convention. **A known, pre-existing limitation, not introduced by this task:** `--image-source` is a flag only, never saved to local config, so a fork's `fugaro image refresh` (which reads the local config, not flags, for its base-kind handling) would see its own custom `base_images.base` entry as "not a release image this fugaro verified" and hit `customBaseRefusal` unless `--image-source` is passed every time. Stated here, not fixed — out of scope for this plan.

**A subtlety worth one sentence (design §9 Decision 1, M3):** cosign's OCI signature binds to the **digest**, not to which repository name it is referenced under — so a digest signed as `fugaro-base`'s could, in principle, verify just as well if (by mistake or by copying bytes) the same digest were ever referenced under `fugaro-history`'s name instead. Low impact here, since `Resolve`'s own `ref` is always built from the caller's own, correctly-known `kind`, never read from anywhere an attacker could substitute it — but worth knowing if this code is ever reused more generally.

**Finding the signature (sigstore-go route, if chosen — design §9 Decision 1).** `sigstore-go` itself fetches nothing from a registry; the implementer must locate the signature artifact first. Under cosign v3 (paired with `cosign-installer` v4.x, Task 1's pin), a signature is stored as an **OCI referrer** of the signed digest (the OCI 1.1 `subject` field, listed through the registry's referrers API), or, where that API is missing, through the OCI distribution spec's **referrers tag schema**: an image index at tag `<algorithm>-<hex>` — i.e. `sha256-<hex>`, with **no `.sig` suffix** — in the same repository (spec §"Unavailable Referrers API", whose table maps a `sha256:aaaa…` subject to the tag `sha256-aaaa…`). The `sha256-<hex>.sig` tag is cosign's *older* layout, used only with `--registry-referrers-mode=legacy`, not what the pinned v3 writes; a fetch that looks for `.sig` will find nothing and must not then conclude "unsigned". The implementer checks which of the two ghcr.io actually produces (not verified here) before writing the fetch. **The verifier must set an artifact-digest policy equal to the resolved digest and must never use a "without artifact" / unsafe verification mode** — accepting a signature with no artifact binding at all would defeat the entire point of digest-pinning.

**Info for the Task 2 verifier (PR #377's supply-chain review, after Task 1's signing chain was hardened).** Three things the implementer of this task must check, because Task 1's workflow now guarantees them and `Resolve` is where that guarantee is *enforced*:

- **The certificate must say the run was a tag *push*, of `dimipaun/fugaro`'s own workflow — four claims, not two.** Task 1 signs only under `if: github.event_name == 'push'`, so the certificate's identity is `https://github.com/dimipaun/fugaro/.github/workflows/images.yml@refs/tags/vX.Y.Z` and its issuer is `https://token.actions.githubusercontent.com` (`CertIdentity.WorkflowRef` and `.Issuer`, both checked exactly). **The ref alone does not prove a push** (PR #377's re-review, M1): a `workflow_dispatch` run started against a tag ref is issued the very same `...images.yml@refs/tags/vX.Y.Z` subject, so identity-plus-issuer would accept an image signed by a manual run on a release ref. The claims that distinguish them are the Fulcio extensions **1.3.6.1.4.1.57264.1.2** (`event_name`) and **1.3.6.1.4.1.57264.1.5** (`repository`), i.e. `CertIdentity.Trigger` = `"push"` and `CertIdentity.Repository` = `"<owner>/fugaro"`; with the cosign binary they are `--certificate-github-workflow-trigger push` and `--certificate-github-workflow-repository dimipaun/fugaro` (flag names checked against cosign v3's `cosign_verify` docs; the OIDs against fulcio's `docs/oid-info.md`). All four MUST be required, and the repository must be the one the caller asked for — a fork's `--image-source ghcr.io/<fork-owner>` expects `<fork-owner>/fugaro` (see "Forks and --image-source" above) — never a name read out of anything the registry or the image returned. Task 1's own `cosign verify` step carries all four for the same reason.
- **Never use a cosign mode that skips the transparency log or the digest binding.** Specifically not `--insecure-ignore-tlog`, not `--insecure-ignore-sct`, and not any "verify without an artifact" / `verify-blob`-style path that accepts a signature covering some other digest: the signature must be checked against exactly the `<source>/fugaro-<kind>@sha256:<hex>` digest `TagResolver` just resolved, or the whole of digest-pinning is decoration. (Design §9 Decision 1's "correctness beats convenience"; the same rule applies whether the route is `sigstore-go` or the `cosign` binary.)
- **Look for the signature in the right place, in `fugaro-base` itself.** Under cosign v3 (paired with Task 1's `cosign-installer` v4.x pin), the signature is attached in the **same repository** as the signed image — as an OCI *referrer* of the signed digest, or, where the registry has no referrers API, through the OCI distribution spec's referrers tag schema, an image index at `sha256-<hex>` (no `.sig`: that suffix is cosign's older, legacy-mode tag, and looking only for it finds nothing on a v3 signature). Which of the two ghcr.io actually produces is **not verified here** (no live registry access in this run): the implementer must determine it before writing the fetch, and a fetch that finds nothing must fail closed, never fall through to "no signature found, accept" or to the plain `<version>` tag.

**Network, beyond ghcr.io (design §2's "new operational cost").** Keyless verification needs the sigstore TUF trusted root and Rekor to be reachable from wherever `Resolve` runs (the operator's machine, or a CI runner doing `fugaro image refresh --yes`) — not only `ghcr.io` itself.

- [ ] **Step 1: Write the failing tests**

```go
package basepin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var sixtyFourHexC = strings.Repeat("c", 64)
var testDigest = "sha256:" + sixtyFourHexC // var, not const: a const cannot be built from strings.Repeat

type fakeResolver struct {
	digest string
	err    error
}

func (f fakeResolver) ResolveDigest(context.Context, string, string, string) (string, error) {
	return f.digest, f.err
}

type fakeVerifier struct {
	wantRef string
	wantID  CertIdentity
	err     error
}

func (f fakeVerifier) Verify(_ context.Context, ref string, id CertIdentity) error {
	if ref != f.wantRef || id != f.wantID {
		return errors.New("verify called with unexpected ref or identity")
	}
	return f.err
}

// All four claims, not just the ref: the ref alone is also what a
// workflow_dispatch run against a tag ref is issued (see CertIdentity).
func wantIdentity(owner, repoOnGitHub, version string) CertIdentity {
	return CertIdentity{
		WorkflowRef: "https://github.com/" + owner + "/" + repoOnGitHub + "/.github/workflows/images.yml@refs/tags/v" + version,
		Issuer:      "https://token.actions.githubusercontent.com",
		Trigger:     "push",
		Repository:  owner + "/" + repoOnGitHub,
	}
}

func TestResolve(t *testing.T) {
	tr := fakeResolver{digest: testDigest}
	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	v := fakeVerifier{wantRef: ref, wantID: wantIdentity("dimipaun", "fugaro", "0.7.5")}
	// "base", not "fugaro-base": Resolve builds the full image name itself.
	got, err := Resolve(context.Background(), tr, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.5")
	want := "ghcr.io/dimipaun/fugaro-base:0.7.5@" + testDigest
	if err != nil || got != want {
		t.Errorf("got %q, %v; want %q", got, err, want)
	}
}

func TestResolveRefusesAFailedVerification(t *testing.T) {
	tr := fakeResolver{digest: testDigest}
	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	v := fakeVerifier{wantRef: ref, wantID: wantIdentity("dimipaun", "fugaro", "0.7.5"), err: errors.New("certificate identity mismatch")}
	if _, err := Resolve(context.Background(), tr, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.5"); err == nil || !strings.Contains(err.Error(), "certificate identity mismatch") {
		t.Errorf("got %v, want the injected verification error", err)
	}
}

func TestResolveRefusesAResolveError(t *testing.T) {
	tr := fakeResolver{err: errors.New("404")}
	if _, err := Resolve(context.Background(), tr, fakeVerifier{}, "dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.5"); err == nil {
		t.Error("want an error")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/basepin/... -count=1`
Expected: FAIL (package does not exist yet).

- [ ] **Step 3: Implement**

`Resolve` calls `tr.ResolveDigest(ctx, source, kind, version)`, builds `ref := source+"/fugaro-"+kind+"@"+digest` and the identity, calls `v.Verify(ctx, ref, id)`, and on success returns `source+"/fugaro-"+kind+":"+version+"@"+digest`.

`internal/mirror/mirror.go` gains:

```go
// ResolveDigest reads what ref's tag currently names, anonymously, reusing
// Plan's own first step (not reimplemented).
func (m *Mirror) ResolveDigest(ctx context.Context, ref Ref) (digest string, err error) {
	_, digest, _, err = m.getManifest(ctx, ref, ref.Tag)
	return digest, err
}
```

The real `TagResolver` (wired in Task 3) parses `source` (e.g. `"ghcr.io/dimipaun"`) into host and owner, and builds `mirror.Ref{Host: host, Repo: owner+"/fugaro-"+kind, Tag: version+"-pinned"}` — the owner comes from `source`, **never hard-coded** (an earlier draft of this document hard-coded `"dimipaun"` here, silently ignoring a fork's own `--image-source`). The real `OCIVerifier` is either `sigstore-go`'s image-verification API or a shelled-out `cosign verify --certificate-identity ... --certificate-oidc-issuer ... ref` (design §9 Decision 1).

Run: `go test ./internal/basepin/... ./internal/mirror/... -count=1`
Expected: PASS.

## Task 3: the existing copy gains a verification pre-step, sourced from the `-pinned` tag

**Flag for opus review:** a code path that falls back to an unverified value, or to the plain tag, on any error (instead of refusing) would silently reintroduce the weaker trust model the design is replacing it with.

**Scope, narrowed by this round's ruling: no build-submission call site changes at all.** `internal/cli/image.go`, `init.go`'s `buildImages`, `internal/cli/image_refresh_steps.go`'s `refreshBuilds` keep reading `base_images.base` exactly as today, falling back to `internal/image/local.go`'s `BaseRef` only when `base_images.base`/`--base` is unset for a **release** build (`BaseRef` refuses outright for a development build, requiring `--base` explicitly — it is not a dev-build fallback) — an earlier draft of this task ("Part A") had these three resolve a verified `ghcr.io` digest fresh at every build submission; dropped entirely, because it created a second, ghcr.io-shaped representation of "what base was this built from" that `submitRebuild`, `refreshVerdict`, and `triggers()` could never match against the Artifact-Registry-shaped one they compare by exact string equality (design §1 note, §2). `fugaro image build --local` (`internal/cli/image.go:122`, the `fugaro:setup` skill's local-Docker-build loop) was never in scope for either draft and needs no change.

**Files:**
- Modify: `internal/cli/init_images.go` only (the images stage's existing base-kind branch — `items()`, `read()` — unchanged in structure, gains one new call before `mirror.Plan`, and one new parameter threaded through `mk`). **This is the single shared copy path, and there is only one.** An earlier draft of this document also listed `internal/cli/image_refresh_rules.go`'s `refreshBase(lc, kind, ver) (string, error)` here, confusing it with a same-named but unrelated function: that one (`image_refresh_rules.go:26`) never calls `mirror.Plan` at all — it only computes a *destination string* for planning purposes (`refreshVerdict`/`p.want`, read-only), so a verification call inserted there would be a dead-end addition that affects nothing actually copied. `image refresh`'s own second entry point into the real copy path is `image_refresh_steps.go`'s differently-shaped `(r *initRun) refreshBase(ctx, e, kinds) error` — a thin wrapper that only calls `newImagesStage(e).Check()`/`.Apply()`, so it already routes through `init_images.go`'s `imagesStage` and needs no separate change of its own.

**Interfaces:**

1. **A new, overridable package-level hook, the same pattern `newMirror` already uses in this file — not literally `var resolvePinnedBase = basepin.Resolve` (a mistake an earlier draft of this task made).** `basepin.Resolve`'s real signature takes 8 parameters (`ctx, tr, v, owner, repoOnGitHub, source, kind, version`) and returns the full, clean reference `source+"/fugaro-"+kind+":"+version+"@"+digest` (Task 2) — not the bare digest `mirror.Plan`'s `expect` parameter wants (`mirror.go:167-168` compares it against `topDigest`, a bare `sha256:...` value). Passing `Resolve`'s return value straight through as `expect` would make that comparison fail on every single copy, since a full `source/fugaro-kind:version@sha256:...` string never equals a bare digest. `resolvePinnedBase` is a thin adapter instead: `func(ctx context.Context, source, kind, version string) (digest string, err error)`, which bakes in the real `TagResolver`/`OCIVerifier` and the installation's own `owner`/`repoOnGitHub` (from `--image-source`, Task 2's "Forks and --image-source"), calls `basepin.Resolve`, and returns only the digest — the substring after the last `@` in its result. Called only for the base kind (never history, §1/Task 1 scope).
2. **`mk`'s signature gains a source-tag parameter distinct from the destination tag — set statically, with no resolution and no network call, inside `items()`.** `items()`'s own doc comment says "nothing is read from the network" (`init_images.go:174-176`), and `Check()` calls it directly (`init_images.go:229`) — so nothing that needs a real read can live in `mk`/`items()` at all. `mk(label, kind, name, srcTag, dstName, dstTag string) error` builds `src` from `allow[0]+"/"+name+":"+srcTag` instead of always reusing `ver`: a plain string substitution, same cost as today. History's call (`mk("history", "", "fugaro-history", ver, infra.HistoryImagePackage, "latest")`) passes `ver` as `srcTag`, unchanged behavior. The base-kind call becomes `mk("base "+k, k, "fugaro-"+k, ver+"-pinned", "fugaro-"+k, ver)` — also just a string, built unconditionally the moment `items()` runs, **before** any resolution happens (below); nothing here calls `resolvePinnedBase`.
3. **The actual resolution happens in `read()`, immediately before `mirror.Plan(ctx, it.src, it.dst, expect[it.key()])`, for each base-kind item in its existing loop over `items` (`init_images.go:283-301`) — not in `items()`/`mk`, which stay network-free.** For the base kind's item, `read()` calls `resolvePinnedBase(ctx, source, kind, ver)` first (`source` from `s.allow()`, already wired to `--image-source` — `fugaro init`, `image refresh`, and `upgrade` all carry that flag, so there is no caller here lacking it, unlike the deleted Part A's `fugaro image build` concern). On success, its resolved digest becomes that item's `expect[it.key()]` (overriding an absent `--expect-digest`, never a conflicting one — if `--expect-digest` was also given and disagrees, that is the existing mismatch refusal `mirror.Plan` already makes, unchanged) immediately before that item's own `m.Plan(...)` call. Since `mk` (rule 2, above) has already built `it.src` from `ver+"-pinned"` for the base kind, `src.Tag` and `expect` always agree by construction, both coming from the same `-pinned` tag — this is the fix for a bug an earlier draft of this task had: feeding the resolved digest as `expect` while leaving `src` built from the plain `ver` tag — after the first nightly rebuild moved that tag to a new, unsigned digest, `mirror.Plan` (`mirror.go:169`, `expect != "" && topDigest != expect`) would return `ErrDigestMismatch` on every subsequent copy attempt, defeating the entire purpose of the `-pinned` tag.
4. If `resolvePinnedBase` fails, the stage refuses before attempting any copy for that item, naming the real reason (no signed image yet, or a verification failure) rather than falling back to an unverified copy or the plain tag.

- [ ] **Step 1: Write the failing tests**

```go
// imagesRegistry.publish's cfg/layer content depends only on repo, not tag
// (init_images_test.go:53-64), so simply re-publishing the same repo+tag
// (an earlier draft of this test's mistake) produces identical bytes and
// never actually moves the plain tag's digest — the "nightly" would be a
// no-op, and the bug this test exists to catch (sourcing the copy from
// the plain tag) would pass undetected either way. This helper gives the
// nightly's rebuild genuinely different bytes under the same repo+tag.
func publishMarked(r *imagesRegistry, repo, tag, marker string) (indexDigest, childDigest string) {
	cfg := []byte(`{"os":"linux","architecture":"amd64","marker":"` + marker + `"}`)
	layer := []byte("layer of " + repo + " " + marker)
	r.blobs[cdg(cfg)], r.blobs[cdg(layer)] = cfg, layer
	im, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{"digest": cdg(cfg), "size": len(cfg)}, "layers": []any{map[string]any{"digest": cdg(layer), "size": len(layer)}}})
	r.mans[cdg(im)], r.types[cdg(im)] = im, "application/vnd.oci.image.manifest.v1+json"
	ix, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": []any{map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": cdg(im), "size": len(im), "platform": map[string]string{"os": "linux", "architecture": "amd64"}}}})
	r.mans[cdg(ix)], r.types[cdg(ix)] = ix, "application/vnd.oci.image.index.v1+json"
	r.tags[repo+":"+tag] = cdg(ix)
	return cdg(ix), cdg(im)
}

// A nightly rebuild between releases has already moved the plain "1.2.3"
// tag to a genuinely different, unsigned image by the time the copy
// runs. The verified copy must still succeed, reading the nightly-immune
// "-pinned" tag (the one basepin verified) as its source — not the plain
// tag mirror.Plan would otherwise resolve. This pins the fix for a bug an
// earlier draft of this task had: feeding the -pinned digest as
// mirror.Plan's expect while leaving the copy's source on the plain tag
// would return ErrDigestMismatch here, from the ver tag's now-different
// digest.
//
// What the destination tag actually ends up holding is the signed
// index's linux/amd64 CHILD manifest digest, not the index digest itself
// (mirror.go:166-181: Plan resolves src.Tag to topDigest — the index,
// what expect is compared against — then, for an index, dereferences to
// the amd64 entry and overwrites p.Manifest/p.Digest with THAT before
// Copy's putManifest pushes it, mirror.go:286). An earlier draft of this
// test asserted the destination held the index digest it fed to
// resolvePinnedBase, which is never what gets written and would fail
// even against correct code.
func TestImagesStageCopiesFromThePinnedTagNotThePlainOne(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	releasedIndex := r.src.tags["dimipaun/fugaro-base:1.2.3"] // the release's own, signed index digest
	var ix struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	json.Unmarshal(r.src.mans[releasedIndex], &ix)
	releasedChild := ix.Manifests[0].Digest // what Copy actually writes to the destination tag
	r.src.tags["dimipaun/fugaro-base:1.2.3-pinned"] = releasedIndex

	publishMarked(r.src, "dimipaun/fugaro-base", "1.2.3", "nightly") // the nightly: a genuinely different image under the plain tag

	resolveCalls := 0
	old := resolvePinnedBase
	resolvePinnedBase = func(ctx context.Context, source, kind, version string) (string, error) {
		resolveCalls++
		return releasedIndex, nil // basepin.Resolve verifies and returns the INDEX digest (mirror.ResolveDigest/getManifest never dereferences an index, mirror.go:688-712) — the same value mirror.Plan's expect compares topDigest against (mirror.go:167-168), which is also an index digest
	}
	t.Cleanup(func() { resolvePinnedBase = old })

	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if resolveCalls == 0 {
		t.Error("basepin resolution was never called")
	}
	if got := r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"]; got != releasedChild {
		t.Errorf("copied digest = %s, want the released image's amd64 manifest %s (not the nightly's)", got, releasedChild)
	}
}

// A failed resolution or verification refuses before any copy, and writes
// nothing — the same shape TestInitRefusesWhenFugaroBaseIsNotPublished
// already asserts for the "not published at all" case.
func TestImagesStageRefusesOnAFailedVerification(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	old := resolvePinnedBase
	resolvePinnedBase = func(ctx context.Context, source, kind, version string) (string, error) {
		return "", errors.New("no signature found for this digest")
	}
	t.Cleanup(func() { resolvePinnedBase = old })

	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base")
	if ExitCode(err) != ExitRemoteError || err == nil || !strings.Contains(err.Error(), "no signature found") {
		t.Fatalf("exit %d, err %v, want the verification failure named\n%s", ExitCode(err), err, out)
	}
	if r.dst.puts != 0 {
		t.Errorf("the registry was written to despite a failed verification: %d writes", r.dst.puts)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestImagesStageCopiesFromThePinnedTagNotThePlainOne|TestImagesStageRefusesOnAFailedVerification' -count=1`
Expected: FAIL (package `basepin` and `resolvePinnedBase` do not exist yet).

- [ ] **Step 3: Implement**

Add the `resolvePinnedBase`-then-`mirror.Plan` sequencing and the `mk` source-tag parameter described above.

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

## Task 4: CLI confirmation text — the base kind is now verified, not just "what the registry says"

**Scope, narrowed:** an earlier draft of this task (then numbered 6) removed `--base`, rewrote `--expect-digest`'s meaning, and replaced "copies" wording with "verifies" wording throughout. None of that applies anymore: the copy mechanism is unchanged (design §6), so `--base`, `--expect-digest`, `--replace-image` all stay exactly as they are today, and "copies N image(s), M MB … into your registry" stays accurate — it still copies. **A narrower draft of this task (round 6 of review) then proposed only adding one clause, which a seventh review found insufficient: two existing sentences become actively false for the base kind once Task 3 lands, and simply adding a clause alongside them leaves the false ones in place.**

- `internal/cli/init_images.go:410`'s confirmation says *"The digests above are what the release tags resolve to now, which is what the registry says, not a signature."* — false for the base kind once Task 3 lands: its digest **is** a signature-verified one, not just whatever the tag currently names.
- The same confirmation's trailing `unpinnedText(unpinned)` (and `sourceLine`'s parallel use, `init_images.go:577`) lists `base` as *"NOT pinned with --expect-digest"* whenever `--expect-digest base=...` wasn't passed explicitly — also false once Task 3 lands: the base kind is pinned, by the verified digest `basepin.Resolve` produces, independently of whether `--expect-digest` was ever given for it. Both places build their "unpinned" list from `parseExpectDigests(r.o.expectDigests)` alone (`init_images.go` `Apply()`'s own local `expect`/`unpinned`, and `sourceLine`'s identical pattern), with no way to know Task 3's verification succeeded — they need to stop treating `--expect-digest` as the only way a kind can be pinned.

**Files:**
- Modify: `internal/cli/init_images.go` (`Apply()`'s confirmation-building block and `unpinned` loop, `sourceLine()`'s parallel `unpinned` loop)

**Interfaces:**

1. **The base kind is excluded from both "unpinned" lists unconditionally, not only when `--expect-digest base=...` is given.** This needs no network read and no new state to plumb through `read()`: by construction (Task 3), any base-kind item that reaches either of these code paths (post-`Plan()` in `Apply()`'s confirmation, or the no-network `sourceLine()` preview) was necessarily resolved and verified by `resolvePinnedBase` — a copy that failed verification never gets this far (Task 3, item 4). So both loops gain a one-line exception: `if p.item.key() == "base" { continue }` (or the `sourceLine()` equivalent over `items`, not `pl`), ahead of the existing `expect[...] == ""` check.
2. **The confirmation text in `Apply()` (`init_images.go:410`) splits into two sentences, conditioned on whether a base-kind item is among `pl`:**
   - If present: *"The base image's digest was verified against its signed release."*
   - For every item that remains unpinned after rule 1 (in practice, only `history`, which this design never verifies, §1/§6): the existing sentence, unchanged — *"The other digests above are what the release tags resolve to now, which is what the registry says, not a signature. NOT pinned with --expect-digest: %s"* (dropping "other" when no base-kind item is in this run at all, unchanged from today's wording).
   - If nothing remains unpinned (no history in this run, or it too has `--expect-digest`): the sentence is omitted, same as today's existing behavior when `unpinned` is empty.
3. **`sourceLine()`'s equivalent text gets the same base-kind exclusion** (no new sentence needed there — it is a terse, one-line review-screen preview, not the typed confirmation).

- [ ] **Step 1: Write the failing test**

```go
// Two items in one run — a base-kind copy (always verified once Task 3
// lands) and a history copy (never verified, §1/§6) — so the
// confirmation text's two different provenance statements can both be
// asserted in the same run. The rig must publish fugaro-base:1.2.3-pinned
// itself (an earlier draft of this test only faked resolvePinnedBase,
// which bypasses verification but not the copy's own real network read
// of the -pinned tag Task 3 sources from — mirror.Plan would 404 against
// the rig's default fixture, which never publishes that tag, and the
// test would fail after Task 3 lands for a reason that has nothing to do
// with what it's testing). The assertions are specific to the NEW
// wording: "verified by digest" and generic "verif"/"copies" substrings
// are already in today's unrelated text (init_images.go:436, :240, :537)
// and would make this test pass before Task 3/4 exist at all, the same
// mistake an earlier draft of this test had.
func TestImagesStageConfirmationNamesVerificationForBaseOnly(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	releasedIndex := r.src.tags["dimipaun/fugaro-base:1.2.3"]
	r.src.tags["dimipaun/fugaro-base:1.2.3-pinned"] = releasedIndex
	old := resolvePinnedBase
	resolvePinnedBase = func(ctx context.Context, source, kind, version string) (string, error) {
		return releasedIndex, nil
	}
	t.Cleanup(func() { resolvePinnedBase = old })

	out, _, err := executeStdin(t, "us-east5\n", "init", "--yes", "--base", "base", "--firebase", fpID)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "base image's digest was verified against its signed release") {
		t.Errorf("no verified-base-image sentence:\n%s", out)
	}
	if !strings.Contains(out, "NOT pinned with --expect-digest: history") {
		t.Errorf("history should still be listed as unpinned (it is never verified), and base must not be in that list (it is verified unconditionally):\n%s", out)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/cli/ -run TestImagesStageConfirmationNamesVerificationForBaseOnly -count=1`
Expected: FAIL (the rig, `resolvePinnedBase`, and the new sentence do not exist yet).

- [ ] **Step 3: Implement**

Add the base-kind exclusion to both `unpinned` loops, and the conditional two-sentence confirmation text.

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

## Task 5: Docs

**Scope, narrowed:** most of `docs/gcp-setup.md`, `docs/base-image.md` and `docs/base-image-migration.md`'s existing description of the copy mechanism stays accurate (it is unchanged). What needs updating is narrower than earlier drafts assumed.

**Files:**
- Modify: `docs/gcp-setup.md` §"The base image, in `fugaro-base`" (add one paragraph: `fugaro init --base`/`image refresh`'s existing copy now verifies the release's signed digest before copying, sourced from the nightly-immune `-pinned` tag; Cloud Build's `FUGARO_BASE` substitution and the check job's container image both keep reading `base_images.base` exactly as today — nothing about either reader changes)
- Modify: `docs/release.md` (the `images.yml` section documents the new `publish-base` signing step and the `-pinned` tag; F1 marked done for the base image specifically)
- No change expected: `SECURITY.md`, `docs/base-image.md`'s "Bring your own" section (`--base-image` is untouched by this design)

**Acceptance:** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` passes.

- [ ] Draft each doc change, citing the design doc's wording.
- [ ] Run the docs/lint suite and `gofmt -l .`.

## Task 6: Full sweep

- [ ] `go build ./...`, `go vet ./...`
- [ ] `go test ./... -count=1`
- [ ] `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
- [ ] `gofmt -l .`
- [ ] Re-read `docs/design/retire-image-mirror.md` against the final code.

## Task 7 (owner-run; live checks, NOT RUN until the owner runs them)

Add to `docs/gcp-live-checklist.md`, in the existing numbered-table format, scoped to `fugaro`/`fugaro-dev`, sandbox repository only, never `belong`:

| # | Check | Command | Expected |
|---|---|---|---|
| 39a | `fugaro init --base`/`image refresh` resolving and verifying a real, signed `fugaro-base:<v>-pinned` image on the operator's own machine, against a real release that has run Task 1's signing steps (design retire-image-mirror.md §7, item a) | `fugaro init --base base` (or `image refresh`) in the sandbox repository's checkout, after the implementation lands and a real tag push has produced a signed image | The resolution succeeds and reports the verified digest, and the pinned reference is the remote repository's path at that digest; nothing is copied. |
| 39b | The copy is sourced from the `-pinned` tag, not the plain tag the nightly moves: a copy run after the nightly has moved the plain tag still succeeds and copies the release's originally-signed image (design §7, item b) | Dispatch `images.yml` nightly once (moving the plain tag), then run `fugaro init --base base`/`image refresh` in the sandbox | The copy succeeds (no `ErrDigestMismatch`); the registry's `fugaro-base:<v>` now holds the signed index's linux/amd64 manifest digest (what `mirror.Copy` actually writes, not the index digest itself), matching the release's originally-signed image, not the nightly-moved plain tag's. |
| 39c | OCI-signature verification of a real signed base image, on the operator's own machine; a deliberately mismatched expected identity is refused (tamper-test) | A copy attempt against a real release that has run Task 1's signing steps, then once by hand against a forged/mismatched identity | The real signature verifies; the forged/mismatched one is refused with a clear reason. |
| 39d | Egress/rate-limit behavior under load | — | **Out of scope for this design (§3); not planned.** |
| 39e | The nightly's own run genuinely never touches the `-pinned` tag — a GitHub Actions/ghcr.io check, unrelated to any GCP project | Manually dispatch `images.yml` by `workflow_dispatch` once (never a real tag push), and compare `fugaro-base:<v>-pinned`'s digest before and after | Unchanged; the plain tag's digest changes (today's existing nightly behavior), the `-pinned` tag's does not. A *re-run* of the tag-push workflow itself does move and re-sign `-pinned` again — harmless, not what this check tests. |
| 39f | A full `fugaro init --repo`/`image refresh` in the sandbox, end to end, with the verification pre-step in place: the check job's container image and a real Cloud Build submission both still deploy/run correctly from the now-verified copy | A full `fugaro init --repo`/`image refresh` in the sandbox | The check job's container image (the Artifact-Registry copy, now verified-before-copy) deploys and runs unchanged from today; a real build submission still succeeds, reading `base_images.base` exactly as today. |

**Numbering, corrected:** `docs/gcp-live-checklist.md`'s own "Numbering ruling (owner's assistant, 2026-10-13)" already reserves Check 38 for the generic-tool plan's Task 18 clone-workflow check — a decided fact in the committed checklist today, not a future possibility an earlier draft of this document wrongly hedged against ("the owner renumbers... if another plan has since claimed 38," as if 38 were still unclaimed). The true next free integer is **39**, used directly above, with letter suffixes `39a`–`39f` for this plan's own six checks.
