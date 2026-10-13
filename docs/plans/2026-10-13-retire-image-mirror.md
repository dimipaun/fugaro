# Implementation plan: retire the image mirror for a digest-pinned FROM

Source: [docs/design/retire-image-mirror.md](../design/retire-image-mirror.md). Depends on the base-image hard cut (`docs/plans/2026-10-08-base-image.md` Tasks 17–21, release 0.7.0) having already landed: `config.Bases == ["base"]`, one `base_images` key. **This plan is not executed in this change** (docs only); it is handed to the owner to rule on and, once approved, implemented task by task.

**A note on this revision (second review round).** Several findings forced changes that ripple across more than one task — most importantly, signing moved from a GitHub-Release asset to an OCI-digest signature attached directly to the pushed image in the registry (design §2, this plan's Task 1–2), which in turn simplified Task 4, and a second finding (bring-your-own's VPC-SC case, Task 5) forced reversing an earlier decision to avoid a Terraform `count`/`moved` pair. Where a task's text below says "an earlier draft," that is this document correcting itself, not a hypothetical.

## Global constraints

- **No live cloud or network in any automated test.** Every fake (`TagResolver`, `OCIVerifier`, Terraform plans, Cloud Build requests, check-job reads) is in-process; the real cosign signature, the real Cloud Build pull paths and the real Terraform plan behavior are the owner's live checks (Task 9), each marked NOT RUN until the owner runs it, and **Task 5's code does not ship until the owner has run Task 9's checks 38a and 38b successfully** (an irreversible-in-spirit step — once installations stop needing the grant, there is no cheap way to tell whether a subtle Cloud Build network/IAM nuance the test suite can't catch would have broken them).
- **Never touch `belong`** (EdgeWeb's and EdgeServer's installation) in any live step. Every live check in Task 9 is scoped to `fugaro`/`fugaro-dev`, sandbox repository only, exactly as the existing entries of `docs/gcp-live-checklist.md` already are.
- **Nothing here weakens an existing guard.** `tf.Guard` still refuses every delete without `--allow-delete`; `Cover.checkSpecs` still refuses everything it refuses today, plus the one narrow addition Task 4 makes explicit and tests both ways (accepts the exact recorded value, still refuses everything else).
- **Each task is independently reviewable**: a task's tests fail before its implementation and pass after, with no task depending on a later one's code to compile (fakes stand in for what a later task provides).
- **Security-sensitive tasks are flagged for opus review** inline, with the specific claim a mistake would break.
- **Docs match code.** A doc describing a flag, command or config key this plan hasn't implemented yet is written as "proposed" until the task that implements it lands; `go test ./internal/cli -run 'Docs|Lint'` and `gofmt -l .` are run after every task.

## Task 1: CI — sign the pushed base/history image by OCI digest

**Flag for opus review:** this task is the root of the new trust chain (design §2) — a mistake in *which* identity the signature is checked against, or signing on a trigger other than a tag push (so the certificate's workflow ref doesn't say what a verifier expects), silently reintroduces "whoever can write the ghcr.io tag" as the trust anchor while the design doc claims otherwise.

**Starting state, checked 2026-10-13 (see the design doc's note on current repository state):** `.github/workflows/images.yml` already carries the real, merged `fugaro-base`/`publish-base` jobs (commit `356f7b1`, already an ancestor of this branch); `docs/pending-workflows/` holds only `README.md`, and that file's own `## images.yml` section still describes the now-merged base-image diff rather than "nothing pending" — it is itself stale and gets replaced wholesale by this task, not amended. This task's first step is therefore not "modify the pending file" (there isn't one) but "stage a fresh one": create `docs/pending-workflows/images.yml` as the current real `.github/workflows/images.yml`'s content plus this task's own addition, exactly as `docs/pending-workflows/README.md`'s own instructions describe for any workflow change Fugaro needs to make (Fugaro cannot push a commit touching `.github/workflows/**`, so the new step can only ever reach the real file by a human copying this staged one in, the same `cp`/`git rm` sequence the README already documents).

**Redesigned after the second review round (was: a separate `sign-base-digests` job uploading a signed `image-digests.txt` to the GitHub Release).** That design does not survive two things this repository already does:

1. `docs/release.md`'s own "Before you tag" checklist tells the owner to turn on **immutable releases**, under which a release's assets cannot be added or changed once GoReleaser (`release.yml`, a wholly independent workflow on the same tag push) has published it. `gh release view` succeeding only proves the release *object* exists, not that GoReleaser's own `release --clean` step (which can run for most of its 45-minute job) has finished, so a bounded wait loop cannot reliably avoid racing an immutable-release refusal.
2. `images.yml` also runs on a nightly `schedule` and `workflow_dispatch` (`images.yml:7-9`). Its own "Pick the version" step treats both identically to `push` in one respect (it still sets `publish=true`) but differently in another: it resolves `tag` to **the latest existing release tag**, never the dispatched ref, and then re-pushes `fugaro-base:<v>` under a **new** digest. A GitHub-Release-asset design signing on every `publish=='true'` run would, on a nightly, sign under the wrong OIDC certificate identity (`images.yml@refs/heads/main`, since the run itself is not a tag push, not `images.yml@refs/tags/vX.Y.Z`) and `--clobber` a correctly tag-identity-signed release's assets with an unverifiable one.

**The fix: `cosign sign` (an OCI-attached image signature), not `cosign sign-blob`, run as one more step inside `publish-base`'s and `publish`'s **existing** jobs, right after each push — no new job, no GitHub Release asset, no wait loop, no `--clobber`.** This needs no cross-workflow coordination at all (a container registry has no release-immutability concept), and it resolves the round-1 "do not widen the job that holds `packages: write`" concern by making it moot rather than honoring it differently: `cosign sign` pushes the signature as a new OCI artifact into the **same** repository, which inherently needs the same registry-write credential the image push already needed — there is no way to decouple "can push the image" from "can sign the image just pushed" for this signing mechanism (unlike the earlier blob-signing design, where the split was meaningful). `publish-base` and `publish` each gain `id-token: write` alongside their existing `contents: read`/`packages: write`; `TestOnlyPublishJobsHoldPackagesWrite` (the real, already-existing test) needs **no change at all**, since it only asserts `packages: write`'s allowlist, which does not change.

**The nightly's signing is gated to `github.event_name == 'push'` only — and this alone is not enough, which a third review round found.** Since `workflow_dispatch` is treated identically to `schedule` by the version-picking script (always "the latest release tag," never the dispatched ref — confirmed by reading the script, not assumed), there is no legitimate "manual re-run against a specific tag" case this guard would wrongly skip; a transient signing failure on a real tag push is recovered by GitHub's own "re-run failed jobs," which replays the same `push` event unchanged. **But the signing guard by itself does nothing to stop the nightly from moving the plain `fugaro-base:<v>` tag to a new, unsigned digest every night** (`cron: "23 3 * * *"`), for whatever the *latest* release is. Resolving against that plain tag would mean every fresh resolution (a brand-new `fugaro init`/`upgrade`/`image refresh` of the then-latest release, not only an already-pinned install missing a patch) hard-refuses for the window between the first nightly after a release and the next release tag — a window whose length tracks this project's own release cadence, not a fixed, small number of hours, and could be weeks.

**The fix: a second, nightly-immune tag, `fugaro-base:<v>-pinned`/`fugaro-history:<v>-pinned`, created only on a tag push, in the same push call that already creates `:<v>`/`:<major>`, and resolved against instead of the plain tag.** Since the `-pinned` tag addition is itself conditioned on `github.event_name == 'push'`, the nightly's own run of the same `imagetools create`/push-loop call never creates or moves it — `basepin.Resolve` (Task 2) always finds the one digest the tag-push run itself signed, with no dependency on how soon the next release follows. The plain `fugaro-base:<v>`/`fugaro-history:<v>` tags' nightly refresh is otherwise unchanged (still useful to a bring-your-own/VPC-SC install that pulls by tag with no pin at all, Task 5's "give the refusal a real path"). Design doc §9 Decision 5 recommends this mechanism.

**Files:**
- Add: `docs/pending-workflows/images.yml` (a fresh staged copy: today's real `.github/workflows/images.yml`, plus the additions below — not a diff against an existing pending file, since none exists)
- Modify: `images/base_workflow_test.go` (new pinning tests below), `docs/pending-workflows/README.md` (its `## images.yml` section is rewritten wholesale to describe *this* diff against the real file, replacing the stale description of the already-merged base-image diff; the general "Copying a file in" instructions above it are unaffected)

**Interfaces:**
- No new Go package, no new shell script: the whole change is a tag addition plus two short inline steps per job (install cosign, sign).
- **The two digests that get signed are resolved differently, stated accurately (a second review round's "no re-resolution by tag after the fact" claim was wrong for the index case and is corrected here):**
  - `publish`'s `history` leg's digest **is** captured straight from the push, with no tag query at all: `docker image inspect --format '{{index .RepoDigests 0}}'` right after its own push loop, the same idiom `publish-base` already uses for its per-architecture digests.
  - `publish-base`'s **index** digest is *not* available this way: `docker buildx imagetools create` (which produces the multi-platform index `publish-base` signs) does not print the resulting digest on its own output, unlike a plain `docker push`, which is why the existing script never captured it before this task. The only way to learn it is `docker buildx imagetools inspect "$repo:$VERSION" --format '{{.Manifest.Digest}}'` **after** `imagetools create` has run — a tag-based registry query, in the plain sense of the words. This is safe for a reason distinct from (and much narrower than) "no tag query at all": it happens in the **same job**, **immediately** after the **same job's own** `imagetools create` call wrote that exact tag, under a concurrency group (`images-publish-base`, `cancel-in-progress: false`) that already serializes every run touching this image — nothing else can have moved `:$VERSION` in the seconds between the two calls. This is categorically different from, and does not reintroduce, the severe nightly-vs-fresh-resolution race above: that race is about a *separate, later* run (hours to weeks later) moving the tag between a release and a fresh install's own resolution; this is an immediate, same-transaction-in-spirit read within one job's own run.
- `cosign sign --yes "ghcr.io/dimipaun/fugaro-base@$digest"` and `cosign sign --yes "ghcr.io/dimipaun/fugaro-history@$digest"`, each given the already-resolved digest above (never a tag, so `cosign` itself never has to resolve anything).
- `sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2` — the **exact, real pin `release.yml:95` already uses** (not a placeholder: an earlier draft of this document left a `<PIN>` placeholder, which fails `TestActionPinsAreFullSHAs`, the existing test that reads the pending file; this repository's own convention of using the newest pin across workflows is already satisfied by copying the one `release.yml` uses today).
- **The nightly-immune `-pinned` tag (a third review round's fix for the severe window above).** `publish-base`'s existing `tags="--tag $repo:$VERSION"` (plus `$MAJOR` when it moves) gains `--tag $repo:$VERSION-pinned`, added only `if [ "$GITHUB_EVENT_NAME" = push ]`, fed into the **same** `docker buildx imagetools create $tags $digests` call that already creates the other tags — so it points at the exact same index, with no second create call and no second digest to track. `publish`'s `history` leg gets the equivalent one-line addition to its own push loop. Neither the signing step nor `basepin` ever reads or writes a `-pinned` suffix anywhere **except** as this registry-side tag name: the recorded, built-FROM value is always the clean `fugaro-<kind>:<version>@<digest>` (Task 2).

- [ ] **Step 1: Write the failing tests**

`images/base_workflow_test.go`:

```go
// TestPublishJobsSignTheirPushByOCIDigestOnPushOnly pins the redesigned
// signing shape (design retire-image-mirror.md §2): publish-base and
// publish gain id-token: write (alongside their existing packages: write;
// OCI signing needs the same registry-write credential the push already
// needed, so there is nothing to isolate into a separate job), each has a
// cosign-installer step at the exact pin release.yml already uses, and
// each has a `cosign sign` step gated to github.event_name == 'push' only
// (never the nightly/dispatch triggers, which would sign under the wrong
// certificate identity and have nothing of value to sign for a pinned
// install anyway).
func TestPublishJobsSignTheirPushByOCIDigestOnPushOnly(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string `yaml:"uses"`
				If   string `yaml:"if"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	const cosignPin = "sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6"
	for _, name := range []string{"publish-base", "publish"} {
		job, ok := wf.Jobs[name]
		if !ok {
			t.Fatalf("no %s job", name)
		}
		if job.Permissions["id-token"] != "write" {
			t.Errorf("%s: permissions[id-token] = %q, want write", name, job.Permissions["id-token"])
		}
		var installed, signed bool
		for _, s := range job.Steps {
			if strings.HasPrefix(s.Uses, cosignPin) {
				installed = true
			}
			if strings.Contains(s.Run, "cosign sign ") {
				signed = true
				if !strings.Contains(s.If, "github.event_name == 'push'") {
					t.Errorf("%s: cosign sign step's if = %q, want it to require github.event_name == 'push'", name, s.If)
				}
			}
		}
		if !installed || !signed {
			t.Errorf("%s: cosign-installer present = %v, cosign sign step present = %v", name, installed, signed)
		}
	}
}

// TestPublishJobsTagPinnedOnlyOnPush pins the fix for the severe nightly
// race a third review round found: signing alone does not stop the
// nightly from moving the plain fugaro-base:<v> tag to a new, unsigned
// digest, which would make every FRESH resolution (not only an
// already-pinned install) hard-refuse for the whole window between a
// nightly and the next release. The -pinned tag must be created only on
// a tag push, in the SAME run (never by the nightly's own run of the
// same push/create call), or this does not hold.
func TestPublishJobsTagPinnedOnlyOnPush(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Steps []struct{ Run string `yaml:"run"` } `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for name, wantTag := range map[string]string{"publish-base": "$VERSION-pinned", "publish": "$VERSION-pinned"} {
		job, ok := wf.Jobs[name]
		if !ok {
			t.Fatalf("no %s job", name)
		}
		var found bool
		for _, s := range job.Steps {
			if strings.Contains(s.Run, wantTag) {
				found = true
				if !strings.Contains(s.Run, `"$GITHUB_EVENT_NAME" = push`) {
					t.Errorf("%s: the step adding %s must condition it on $GITHUB_EVENT_NAME = push, so the nightly's own run never creates or moves it", name, wantTag)
				}
			}
		}
		if !found {
			t.Errorf("%s: no step creates the %s tag", name, wantTag)
		}
	}
}
```

(`TestOnlyPublishJobsHoldPackagesWrite`, the real, already-existing test in this file, needs no change and is not reproduced here — it already asserts exactly the invariant this task must not violate, that no job other than `publish`/`publish-base` holds `packages: write`, and this task adds no new job.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./images/ -run 'TestPublishJobsSignTheirPushByOCIDigestOnPushOnly|TestPublishJobsTagPinnedOnlyOnPush' -count=1`
Expected: FAIL (no `id-token: write`, no cosign steps, no `-pinned` tag yet).

- [ ] **Step 3: Implement**

Create `docs/pending-workflows/images.yml` by copying today's real `.github/workflows/images.yml` verbatim, then making these additions:

`publish-base`'s `permissions` gains `id-token: write`; its existing "Push per architecture and publish the index" step (which already builds `$tags` and calls `docker buildx imagetools create $tags $digests`) gains the `-pinned` tag addition **before** that call, and the signing steps go **after** it:

```yaml
          tags="--tag $repo:$VERSION"
          if [ "$MOVE_MAJOR" = true ]; then
            tags="$tags --tag $repo:$MAJOR"
          fi
          # Nightly-immune: created only on a real tag push, in the SAME
          # create call as :$VERSION/:$MAJOR above, so the nightly's own
          # run of this same script never creates or moves it (design
          # retire-image-mirror.md §2, the fix for the severe window a
          # third review round found: resolving the plain :$VERSION tag
          # would make every FRESH resolution, not only an already-pinned
          # install, hard-refuse between a nightly and the next release).
          if [ "$GITHUB_EVENT_NAME" = push ]; then
            tags="$tags --tag $repo:$VERSION-pinned"
          fi
          # shellcheck disable=SC2086
          docker buildx imagetools create $tags $digests
```

then, still in the same step (the existing size-summary block is unaffected), add the signing steps as two more steps in the job:

```yaml
      - uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2
        if: github.event_name == 'push'
      - name: Sign the base image
        if: github.event_name == 'push'
        env:
          VERSION: ${{ needs.version.outputs.version }}
        run: |
          set -eu
          # docker buildx imagetools create does not print the index's own
          # digest, unlike a plain docker push: the only way to learn it is
          # to ask the registry what the tag it just wrote now resolves to.
          # This is safe here (unlike resolving the plain tag days or
          # weeks later, the severe case above): it happens in THIS SAME
          # job, immediately after THIS SAME job's own imagetools create
          # wrote $repo:$VERSION-pinned, under a concurrency group
          # (images-publish-base, cancel-in-progress: false) that already
          # serializes every run touching this image, so nothing else can
          # have moved it in between.
          digest=$(docker buildx imagetools inspect "$repo:$VERSION-pinned" --format '{{.Manifest.Digest}}')
          cosign sign --yes "ghcr.io/dimipaun/fugaro-base@$digest"
```

(`$repo`/`$VERSION`/`$GITHUB_EVENT_NAME` are already in scope from the existing step's own `env:`/shell variables; `$GITHUB_EVENT_NAME` is one of the default environment variables every GitHub Actions step already has, used here the same unadorned way the version job's own "Pick the version" step already reads it.)

`publish`'s `permissions` gains `id-token: write` (shared by all four matrix legs — `web-node`/`go`/`java-services` simply never invoke cosign, a harmless, accepted widening rather than a separate job for one leg). Its existing "Load and publish" step's push loop gains the `-pinned` tag the same way, conditioned the same way:

```yaml
          tags=$VERSION
          if [ "$MOVE_MAJOR" = true ]; then
            tags="$tags $MAJOR"
          fi
          if [ "${{ matrix.base }}" = history ] && [ "$GITHUB_EVENT_NAME" = push ]; then
            tags="$tags $VERSION-pinned"
          fi
          for t in $tags; do
            docker tag "fugaro-${{ matrix.base }}:ci" "ghcr.io/dimipaun/fugaro-${{ matrix.base }}:$t"
            docker push "ghcr.io/dimipaun/fugaro-${{ matrix.base }}:$t"
          done
```

then, gated to the `history` leg only, add the signing steps (the digest **is** captured straight from the push here, no tag query, since a plain `docker push` of a single-platform image does expose `RepoDigests` the way `imagetools create`'s index does not):

```yaml
      - uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2
        if: matrix.base == 'history' && github.event_name == 'push'
      - name: Sign the history image
        if: matrix.base == 'history' && github.event_name == 'push'
        env:
          VERSION: ${{ needs.version.outputs.version }}
        run: |
          set -eu
          digest=$(docker image inspect --format '{{index .RepoDigests 0}}' "ghcr.io/dimipaun/fugaro-history:$VERSION" | sed 's/.*@//')
          cosign sign --yes "ghcr.io/dimipaun/fugaro-history@$digest"
```

`docs/pending-workflows/README.md`'s `## images.yml` section is replaced (not appended to): it now describes *this* diff — the `-pinned` tag addition and two short cosign steps in two existing jobs — against the real, already-merged `.github/workflows/images.yml`, instead of the base-image consolidation diff that commit `356f7b1` already copied in. The "Copying a file in" instructions and the `TestReadWorkflowFallsBackOnlyWhilePending`-style tests above that section are unaffected.

**A cheap, recommended addendum, not required by this task:** extending `images/verify-public.sh`'s existing checks to also confirm the new `fugaro-base:<v>-pinned`/`fugaro-history:<v>-pinned` tags are anonymously readable, the same proof `verify-public` already gives for every other published tag.

Run: `go test ./images/ -count=1`
Expected: PASS.

## Task 2: `internal/basepin` — resolve a tag and verify its OCI signature

**Flag for opus review:** this is new, security-critical code with no precedent in this repository (nothing here has ever verified a cosign/OCI signature in Go before); a bug that accepts an unverified or wrongly-identified signature as valid defeats the entire design. It is **also flagged** because whether `sigstore-go`'s library API cleanly supports OCI-registry-attached signature verification (as opposed to the detached blob-verification an earlier draft of this plan assumed) is **unverified at design time** — the implementer confirms this before committing to the library route (design §9 Decision 1).

**Files:**
- Add: `internal/basepin/basepin.go`, `internal/basepin/basepin_test.go`
- Modify: `internal/mirror/mirror.go` (gains one small new exported function, `ResolveDigest`, reusing the package's existing, already-reviewed anonymous-registry-HTTP internals rather than a second HTTP client)
- Depends on (new go.mod entry, owner's call per design §9 item 1): either `github.com/sigstore/sigstore-go`'s `verify` package (if it supports OCI-attached verification cleanly) or shelling out to the real `cosign verify` binary — the implementer's call, per the design doc's Decision 1.

**Interfaces:**

```go
// Package basepin resolves what a release's fugaro-base (or fugaro-history)
// -pinned tag currently names in the registry and verifies that specific
// digest's OCI-attached cosign signature, from images.yml's own tag-push
// identity (design retire-image-mirror.md §2). It makes no decision about
// where the result is recorded; that is internal/cli's.
package basepin

// TagResolver resolves what source+"/fugaro-"+name+":"+version+"-pinned"
// currently names in the registry — the same anonymous, digest-verified
// read internal/mirror already does today (reused via mirror.ResolveDigest,
// not reimplemented), against the nightly-immune "-pinned" tag (Task 1),
// never the plain version tag a nightly rebuild moves. version here is the
// clean "X.Y.Z" the caller passes; building the "-pinned" tag string from
// it is this interface's own job, not the caller's — Resolve's returned
// reference never carries the suffix (below).
type TagResolver interface {
	ResolveDigest(ctx context.Context, source, name, version string) (digest string, err error)
}

// CertIdentity is what a signing certificate must name: the workflow ref
// GitHub's OIDC issues for a TAG PUSH specifically (images.yml running on
// push, never schedule/workflow_dispatch — Task 1's own event guard is
// exactly why this identity is the one to expect).
type CertIdentity struct {
	WorkflowRef string // "https://github.com/<owner>/<repo>/.github/workflows/images.yml@refs/tags/vX.Y.Z"
	Issuer      string // "https://token.actions.githubusercontent.com"
}

// OCIVerifier checks a specific digest's OCI-attached cosign signature
// against an identity. The real implementation wraps either sigstore-go's
// verify package or the cosign binary (design §9 Decision 1); tests use a
// fake that never touches the network.
type OCIVerifier interface {
	Verify(ctx context.Context, ref string, id CertIdentity) error // ref: "<source>/fugaro-<name>@sha256:<hex>"
}

// Resolve resolves (via the "-pinned" tag, TagResolver), verifies and
// returns the pinned reference "<source>/fugaro-<name>:<version>@<digest>"
// — the CLEAN version, with no "-pinned" suffix anywhere in the returned
// string — for name ("fugaro-base" or "fugaro-history") at this release's
// version. The suffix exists purely as a registry-side resolution anchor
// (Task 1); surfacing it in what gets recorded or built FROM would break
// baseRefReleaseRE and every version-skew gate built on it (design §2),
// which parse a plain "X.Y.Z" out of the base ref and know nothing of a
// "-pinned" shape. owner/repoOnGitHub name the GitHub repository whose
// images.yml workflow is trusted (see "Forks and --image-source" below —
// not necessarily the same string as source's ghcr.io owner path segment,
// though they coincide by convention). Nothing is ever returned unverified.
func Resolve(ctx context.Context, tr TagResolver, v OCIVerifier, owner, repoOnGitHub, source, name, version string) (string, error)
```

**Forks and `--image-source`.** `--image-source ghcr.io/<owner>` has always meant "a fork names its own source prefix" (`mirror.ValidSourcePrefix`, unchanged). Verifying a signature needs a second thing this flag was never previously asked to carry: *which GitHub repository's `images.yml` workflow identity to trust*. This plan defines the relation explicitly rather than leaving it implicit: the expected GitHub repository is `<owner>/fugaro` — the ghcr.io owner path segment of `--image-source`, reused as the GitHub owner, with the repository name assumed to be `fugaro` (a fork's own convention, since nothing in `mirror`'s existing code lets a fork rename the upstream repository it is forking). A fork that renamed its own GitHub repository would need a documented, separate override (not built here, flagged as a known limit of this convention, consistent with `--image-source` already assuming a fork's layout mirrors upstream's in every other respect).

- [ ] **Step 1: Write the failing tests**

```go
package basepin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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

// Verify returns the INJECTED err whenever the call matches what the test
// expects, so TestResolveRefusesAFailedVerification tests the stated
// failure reason, not an accidental "wrong identity" mismatch an earlier
// draft of this test produced by leaving wantID at its zero value while
// Resolve always builds a real, non-zero CertIdentity.
func (f fakeVerifier) Verify(_ context.Context, ref string, id CertIdentity) error {
	if ref != f.wantRef || id != f.wantID {
		return errors.New("verify called with unexpected ref or identity")
	}
	return f.err
}

const testDigest = "sha256:" + sixtyFourHexC

var sixtyFourHexC = strings.Repeat("c", 64)

func wantIdentity(owner, repoOnGitHub, version string) CertIdentity {
	return CertIdentity{
		WorkflowRef: "https://github.com/" + owner + "/" + repoOnGitHub + "/.github/workflows/images.yml@refs/tags/v" + version,
		Issuer:      "https://token.actions.githubusercontent.com",
	}
}

func TestResolve(t *testing.T) {
	tr := fakeResolver{digest: testDigest}
	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	v := fakeVerifier{wantRef: ref, wantID: wantIdentity("dimipaun", "fugaro", "0.7.1")}
	got, err := Resolve(context.Background(), tr, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "fugaro-base", "0.7.1")
	want := "ghcr.io/dimipaun/fugaro-base:0.7.1@" + testDigest
	if err != nil || got != want {
		t.Errorf("got %q, %v; want %q", got, err, want)
	}
}

func TestResolveRefusesAFailedVerification(t *testing.T) {
	tr := fakeResolver{digest: testDigest}
	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	v := fakeVerifier{wantRef: ref, wantID: wantIdentity("dimipaun", "fugaro", "0.7.1"), err: errors.New("certificate identity mismatch")}
	if _, err := Resolve(context.Background(), tr, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "fugaro-base", "0.7.1"); err == nil || !strings.Contains(err.Error(), "certificate identity mismatch") {
		t.Errorf("got %v, want the injected verification error", err)
	}
}

func TestResolveRefusesAResolveError(t *testing.T) {
	tr := fakeResolver{err: errors.New("404")}
	if _, err := Resolve(context.Background(), tr, fakeVerifier{}, "dimipaun", "fugaro", "ghcr.io/dimipaun", "fugaro-base", "0.7.1"); err == nil {
		t.Error("want an error")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/basepin/... -count=1`
Expected: FAIL (package does not exist yet).

- [ ] **Step 3: Implement**

`Resolve` calls `tr.ResolveDigest(ctx, source, name, version)`, builds `ref := source+"/fugaro-"+name+"@"+digest` and `id := CertIdentity{WorkflowRef: fmt.Sprintf("https://github.com/%s/%s/.github/workflows/images.yml@refs/tags/v%s", owner, repoOnGitHub, version), Issuer: "https://token.actions.githubusercontent.com"}`, calls `v.Verify(ctx, ref, id)`, and on success returns `source+"/fugaro-"+name+":"+version+"@"+digest`. Any error from either call is returned as-is (wrapped with context, never swallowed); nothing is ever returned when verification fails.

`internal/mirror/mirror.go` gains:

```go
// ResolveDigest reads what ref's tag currently names, anonymously, the same
// way Plan's own first step does (reused, not reimplemented): the caller
// (internal/basepin) builds ref from a configured source and asks what it
// resolves to right now. It does not read blobs or compare against any
// expected value; basepin.Resolve's OCIVerifier does the trust work.
func (m *Mirror) ResolveDigest(ctx context.Context, ref Ref) (digest string, err error) {
	_, digest, _, err = m.getManifest(ctx, ref, ref.Tag)
	return digest, err
}
```

The real `TagResolver` (wired in Task 3) builds a `mirror.Ref{Host: ..., Repo: "dimipaun/fugaro-"+name, Tag: version+"-pinned"}` from `source`/`name`/`version` — the `-pinned` suffix is appended here, inside the real implementation, never by `Resolve` itself (which only ever sees and returns the clean `version`) — and calls `mirror.ResolveDigest`. The real `OCIVerifier` is either `sigstore-go`'s image-verification API or a shelled-out `cosign verify --certificate-identity ... --certificate-oidc-issuer ... ref` (design §9 Decision 1); either way it is compiled or invoked by `internal/basepin` alone, never imported into `internal/cli` directly.

Run: `go test ./internal/basepin/... ./internal/mirror/... -count=1`
Expected: PASS.

## Task 3: Wire `basepin` into `init`/`image refresh`'s base step — and pin it during a FRESH `init` too

**Flag for opus review:** this is where "copied" becomes "verified"; a code path that falls back to an unverified value (reusing `image.BaseRef`'s old bare-tag success case, or submitting a Cloud Build before `base_images.base` has ever been resolved) would silently reintroduce the weaker trust model the design is replacing it with. **A real gap the second review round found and this task now closes:** an earlier draft of this document deleted the images stage's base-kind *copy* logic (Task 3) without replacing it with a verify-and-*pin* step for a brand-new `fugaro init`, while separately claiming (Task 5, "no new deadlock") that a fresh `init` already has the pin by the time Terraform runs. That claim was false: `internal/image/local.go`'s `BaseRef` is read as a **bare-tag, no-digest** fallback at three real Cloud-Build-submission call sites (`internal/cli/image.go:206`, `init.go:2415`, `image_refresh_steps.go:91`), and none of them refuses for a release build — only for a development one. Without this task's addition, a fresh installation's very first build would submit Cloud Build with an unverified, undigested `FROM`.

**Files:**
- Modify: `internal/cli/init_images.go` (the images stage's base-kind branch is **not** deleted outright — it is **repointed** from "copy, then record the destination" to "resolve and verify via `basepin.Resolve`, then record the verified source reference," reusing the same `refreshBase` helper `image refresh`'s own base step calls, Task 3 below; `confirmReplace`'s base-kind path and `record()`'s "copied" wording are rewritten to "verified" wording, not deleted), `internal/cli/image_refresh_rules.go` (`refreshBase` resolves+verifies instead of building a `mirror.Dest`), `internal/cli/image_refresh_steps.go` (confirmation text), `internal/image/local.go` (`BaseRef`'s contract change, below)
- Modify tests: `internal/cli/init_images_test.go`, `internal/cli/image_refresh_rules_test.go`, `internal/cli/image_refresh_test.go`, `internal/image/local_test.go`

**Interfaces:**
- `managedVersion(ref, host, source, kind string) (version string, ok bool)` / `isManaged(ref, host, source, kind string) bool`: recognize **two** shapes, tried in order:
  1. The new, source-prefixed, **digest-mandatory** shape: `strings.CutPrefix(ref, source+"/fugaro-"+kind+":")`, then the remainder must match `^([0-9]+\.[0-9]+\.[0-9]+)@sha256:[0-9a-f]{64}$` — **the digest requirement is load-bearing**, fixing a real bug an earlier draft of this document had: it applied `releaseRE` (`^[0-9]+\.[0-9]+\.[0-9]+$`, a full-string anchor) directly to the remainder **including** the `@sha256:…` suffix, which can never match at all (so the branch, as originally sketched, could never actually recognize a real pinned ref as managed) — and, separately, a bare `ghcr.io/dimipaun/fugaro-base:9.9.9` with **no** digest must **never** be "managed," or a hand-edited (or simply mistyped) config entry naming a fabricated "newer" version would be accepted by `refreshBase`'s own `newer()`-keeps-the-existing-copy branch and **never re-verified**, defeating the whole design.
  2. The existing, host-prefixed shape (`strings.CutPrefix(ref, host+"/"+mirror.BaseRepository+"/fugaro-"+kind+":")`, then `releaseRE.MatchString(rest)`, unchanged), recognizing a pre-migration, Artifact-Registry-hosted copy.
- `imageSource(flag string) string` (**new**; nothing with this name exists in the codebase today): mirrors the existing `(*imagesStage).allow()` method's logic as a small free function — `if flag != "" { return flag }; return mirror.DefaultSources[0]`.
- `refreshBase(ctx context.Context, lc *localcfg.Config, host, source, kind, ver string, resolve func(ctx context.Context, name, version string) (string, error)) (string, error)` — unchanged from the prior draft; `resolve` is production-wired to `basepin.Resolve` (Task 2), tests supply a fake. **Called from two places now, not one:** `image refresh`'s own base step (unchanged), and the images stage's `Apply` (new, below), both through the same function — there is exactly one place that decides "copy vs. verify vs. keep," reused, not duplicated.
- **`internal/image/local.go`'s `BaseRef(base, version string) (string, error)` changes its contract**: it no longer has a success case for `base` at all. Today it returns a bare `ghcr.io/dimipaun/fugaro-<base>:<v>` for any release version; after this task, for `base` (the only kind post-hard-cut) it **always** refuses — for a development build with its existing message, and for a release build with a new one naming the actual fix (`base_images.base is not set; run fugaro image refresh (or, for a fresh installation, rerun fugaro init) first`). This closes the gap directly, as a backstop independent of the images-stage fix above: even if that fix had a bug, or a future caller reaches `BaseRef` through some path this task didn't anticipate, no caller can ever receive an unverified, undigested reference to submit a build with.

- [ ] **Step 1: Write the failing tests**

`internal/image/local_test.go`:

```go
func TestBaseRefAlwaysRefusesForBase(t *testing.T) {
	if _, err := BaseRef("base", "0.7.1"); err == nil {
		t.Error("want a refusal: base_images.base is not set, nothing safe to return")
	}
	if _, err := BaseRef("base", "dev"); err == nil {
		t.Error("want the existing development-build refusal")
	}
}
```

`internal/cli/image_refresh_rules_test.go` (as the prior draft, plus the digest-mandatory and stripped-digest fixes):

```go
const testHost = "us-east5-docker.pkg.dev/proj-1234"
const testSource = "ghcr.io/dimipaun"

func TestManagedVersionRequiresADigestForTheSourceShape(t *testing.T) {
	// A hand-edited, undigested entry under the configured source must
	// NEVER be "managed" — or refreshBase's newer()-keeps-it branch would
	// accept a fabricated "newer" version with nothing ever re-verified.
	bare := "ghcr.io/dimipaun/fugaro-base:9.9.9"
	if _, ok := managedVersion(bare, testHost, testSource, "base"); ok {
		t.Errorf("managedVersion(%q) = ok, want false: no digest, never managed", bare)
	}
	pinned := "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)
	if v, ok := managedVersion(pinned, testHost, testSource, "base"); !ok || v != "0.7.1" {
		t.Errorf("managedVersion(%q) = %q, %v; want 0.7.1, true", pinned, v, ok)
	}
	// The old, host-prefixed shape stays recognized, no digest required
	// (it predates this design and was never digest-pinned).
	old := testHost + "/fugaro-base/fugaro-base:0.7.0"
	if v, ok := managedVersion(old, testHost, testSource, "base"); !ok || v != "0.7.0" {
		t.Errorf("managedVersion(%q) = %q, %v; want 0.7.0, true", old, v, ok)
	}
}

func TestRefreshBaseResolvesAndVerifies(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5"}
	resolved := "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)
	resolve := func(ctx context.Context, name, version string) (string, error) {
		if name != "fugaro-base" || version != "0.7.1" {
			t.Fatalf("resolve(%q, %q)", name, version)
		}
		return resolved, nil
	}
	got, err := refreshBase(context.Background(), lc, testHost, testSource, "base", "0.7.1", resolve)
	if err != nil || got != resolved {
		t.Errorf("got %q, %v; want %q", got, err, resolved)
	}
}

func TestRefreshBaseRefusesToKeepAFabricatedNewerEntry(t *testing.T) {
	// A hand-edited base_images.base naming a "newer" version with no
	// digest must be re-resolved, not trusted as a kept, newer copy.
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5",
		BaseImages: map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:9.9.9"}}
	resolved := "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)
	called := false
	resolve := func(context.Context, string, string) (string, error) { called = true; return resolved, nil }
	got, err := refreshBase(context.Background(), lc, testHost, testSource, "base", "0.7.1", resolve)
	if err != nil || !called || got != resolved {
		t.Errorf("got %q, %v, resolve called = %v; want the fabricated entry re-resolved", got, err, called)
	}
}

func TestRefreshBaseKeepsANewerManagedCopy(t *testing.T) {
	newer := "ghcr.io/dimipaun/fugaro-base:0.8.0@sha256:" + strings.Repeat("d", 64)
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5", BaseImages: map[string]string{"base": newer}}
	resolve := func(context.Context, string, string) (string, error) { t.Fatal("must not resolve: the existing copy is newer"); return "", nil }
	got, err := refreshBase(context.Background(), lc, testHost, testSource, "base", "0.7.1", resolve)
	if err != nil || got != newer {
		t.Errorf("got %q, %v; want the kept newer copy %q", got, err, newer)
	}
}

func TestRefreshBaseRefusesAnUnverifiedResolution(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5"}
	resolve := func(context.Context, string, string) (string, error) { return "", errors.New("certificate identity mismatch") }
	if _, err := refreshBase(context.Background(), lc, testHost, testSource, "base", "0.7.1", resolve); err == nil {
		t.Error("want an error: resolve failed, nothing is recorded unverified")
	}
}
```

`internal/cli/init_images_test.go` gains a case proving a **fresh** `fugaro init` (no prior `base_images.base`) calls the same resolve-and-record path before `buildImages` runs (an integration-shaped test at whatever level this file's existing fixtures support — the implementer wires it to them; the property to prove is "no Cloud Build is submitted before `base_images.base` holds a resolved, digest-pinned value").

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestManagedVersion|TestRefreshBase' -count=1` and `go test ./internal/image/ -run TestBaseRefAlwaysRefusesForBase -count=1`
Expected: FAIL (old signatures/behavior).

- [ ] **Step 3: Implement**

`refreshBase`'s shape is unchanged from the prior draft (resolve-or-keep-newer, §3 of this task's "Interfaces"); `managedVersion`/`isManaged` gain the digest-mandatory source-shape check described above, fixing the stripped-`@sha256:` bug by matching the **whole** remainder against `^([0-9]+\.[0-9]+\.[0-9]+)@sha256:[0-9a-f]{64}$` rather than applying `releaseRE` (which can never match a string with a trailing `@sha256:…`) to it.

The images stage's `Apply` (`internal/cli/init_images.go`) calls `refreshBase` for the `base` kind exactly as `image refresh`'s own base step does, writing the result into `base_images.base` the same way `record()` already writes other recorded values — this runs as part of **every** `fugaro init` (not only `image refresh`), before `buildImages`/`image.go`'s own build-submission code ever reads `lc.BaseImage("base")`, closing the gap a fresh installation had.

`internal/image/local.go`'s `BaseRef` becomes:

```go
func BaseRef(base, version string) (string, error) {
	if base != config.BaseKind {
		return "", fmt.Errorf("%s has no published base image", base) // unchanged, unreachable post-hard-cut
	}
	v := strings.TrimPrefix(version, "v")
	if !releaseRE.MatchString(v) {
		return "", fmt.Errorf("fugaro %s is a development build with no published base image; pass --base (build one with images/build-base.sh %s)", version, base)
	}
	return "", fmt.Errorf("base_images.base is not set; run fugaro image refresh (or, for a fresh installation, rerun fugaro init) first")
}
```

Run: `go test ./internal/cli/... ./internal/image/... -count=1`
Expected: PASS.

## Task 4: `Cover.checkSpecs`'s second allowance — exact match, not a pattern

**Flag for opus review:** `cover.go` is the one piece of code that decides what "nothing is applied unseen" silently covers; a loose match here would let a hand-edited `base_images.base` naming an attacker's own image sail through the single confirmation instead of asking its own.

**Redesigned after the second review round (was: a regex matching any `X.Y.Z` and any 64-hex digest under the configured source — which an earlier draft of this plan also got the anchoring of wrong).** The simpler, strictly stronger fix: `cover()` (`internal/cli/init_review.go`) already has the **exact** value `lc.BaseImage("base")` holds — the same string the Terraform plan's `FUGARO_CHECK_SPEC` is built from — so there is no need to re-validate its shape at cover-time at all. A shape-only regex can be fooled by a prefix trick (an earlier draft's own `-evil/` bug); an exact string comparison cannot, and a digest is guaranteed present not because `cover.go` checks for one, but because the only thing that ever *writes* `base_images.base` (Task 3) never writes a value without one.

**Files:**
- Modify: `internal/infra/tf/cover.go` (`checkSpecs`'s `inRegistry` gains a second check, no new `config`/`regexp` import needed), `internal/infra/tf/cover_test.go`, `internal/cli/init_review.go` (wires the new field)

**Interfaces:**
- `Cover` gains a field, `ExpectedBaseImage string` — the local config's **current** `base_images.base` value, read once at `cover()` construction time (`internal/cli/init_review.go`'s `(*initEngine).cover()`, which already builds `Registry` the same way a few lines up): `c.ExpectedBaseImage = e.lc.BaseImage("base")`.
- `checkSpecs`'s `s.BaseImages` loop (only there — `s.Registry` and the build-service-account check are untouched) calls a new `coveredBaseImage(ref string, inRegistry func(string) bool) bool`: `inRegistry(ref) || (c.ExpectedBaseImage != "" && ref == c.ExpectedBaseImage)`.

- [ ] **Step 1: Write the failing tests**

`internal/infra/tf/cover_test.go`: add to `TestCoverCheckSpec`'s existing map literal (`jobWithSpec`/`spec(registry, bases...)` are that test's own existing fixtures; `reg` there is `"us-east5-docker.pkg.dev/proj-1/"`), and set `ExpectedBaseImage` on the shared `testCover` fixture (`cover_test.go:21`):

```go
"the recorded base image is covered": {
	jobWithSpec(spec(reg+"r", "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:"+strings.Repeat("c", 64)), false), true,
},
"a different digest of the same tag is NOT covered": {
	jobWithSpec(spec(reg+"r", "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:"+strings.Repeat("d", 64)), false), false,
},
"the same shape under another owner is NOT covered": {
	jobWithSpec(spec(reg+"r", "ghcr.io/someone-else/fugaro-base:0.7.1@sha256:"+strings.Repeat("c", 64)), false), false,
},
```

(`testCover.ExpectedBaseImage = "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)`.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/tf/ -run TestCoverCheckSpec -count=1`
Expected: FAIL (`Cover` has no `ExpectedBaseImage` field yet).

- [ ] **Step 3: Implement**

```go
func (c Cover) coveredBaseImage(ref string, inRegistry func(string) bool) bool {
	return inRegistry(ref) || (c.ExpectedBaseImage != "" && ref == c.ExpectedBaseImage)
}
```

`checkSpecs`'s `s.BaseImages` loop calls `c.coveredBaseImage(ref, inRegistry)` instead of `inRegistry(ref)` directly.

Run: `go test ./internal/infra/tf/... -count=1`
Expected: PASS.

## Task 5: Terraform — the build account's `fugaro-base` reader grant becomes conditional, not deleted

**Flag for opus review:** getting the condition wrong in either direction either strands a repository (removes a grant still needed) or never actually tightens anything (keeps a grant forever). This task's code **does not ship until the owner has run Task 9's live checks 38a and 38b successfully** (Global constraints).

**Reversed after the second review round.** The prior draft of this task deleted `google_artifact_registry_repository_iam_member.build_base` outright and relied on a **Go-side precondition** (refuse `fugaro init --repo` until `base_images.base` is pinned) to sequence the removal safely. That does not account for a real, **permanent** case: a VPC-SC or otherwise locked-down installation's bring-your-own custom base (`--base-image`, design §3 option C) is documented today to live inside the installation's own `fugaro-base` registry (`docs/gcp-setup.md`'s existing "Until you use a release build..." instructions: build from a checkout, push to `fugaro-base`). For such an installation, `base_images.base` **never** moves to the ghcr.io pin — that is the whole point of bringing your own base — so a Go-side refusal gated on "pin first" would refuse `fugaro init --repo` **forever**, and its own suggested fix, `fugaro image refresh`, **also** refuses forever for exactly this installation (`customBaseRefusal`: "fugaro image refresh never replaces" a custom base). An earlier draft of this document did not give this case a real path at all.

**The fix: the grant's existence tracks the real, current need, computed once per apply, kept exactly as long as it is actually load-bearing — for either reason, temporary (not yet migrated) or permanent (bring-your-own).** `deploy/terraform/gcp/modules/repo/build.tf`'s resource gains `count = var.repo.base_grant_needed ? 1 : 0`, where `base_grant_needed` is a new boolean Terraform input, computed in Go (`infra.Repo`'s spec-building code) from **two** signals, both required to be "no longer needed" before the count flips to 0:

1. **The local config**, already-designed: `baseStillInInstallationRegistry(lc.BaseImage("base"), host)` — true for any ref still hosted inside the installation's own `fugaro-base`, in *any* shape (a pre-migration managed copy, a hand-pushed `dev-<sha>` tag, a permanent bring-your-own image), false only once it points somewhere else entirely.
2. **The deployed daily check job's own spec** (a gap the second review round found: `fugaro image refresh`'s step 3, which moves the *deployed* Cloud Run job's `FUGARO_CHECK_SPEC` onto the new pin, is a separate, independently-confirmable/failable step from step 2, which records the pin locally and in the shared config — `Shared()`'s union, `shared.go:81`, means a different operator/machine's resolved config is NOT the risk here (it re-reads the same union each time), but a **partial step-3 failure on the very repository that was being refreshed** is: local/shared say "pinned," the daily check job's deployed spec still names the old Artifact-Registry path, and it submits Cloud Build as the same build account the grant protects). This read reuses `infra.PlanCheckJob` (`internal/infra/checkjob.go:145`, already called this way by `refreshCheckJob`) for the repository's own check job, inspecting its *old* (currently-deployed) spec's `base_images.base`, not applying anything.

Because the resource had no `count`/`for_each` before, adding one needs a `moved` block (`moved { from = google_artifact_registry_repository_iam_member.build_base to = google_artifact_registry_repository_iam_member.build_base[0] }`) so Terraform does not plan a spurious destroy-and-recreate of an unchanged grant purely from the indexing change — this repository has a precedent for a conditional-count resource (`registry.tf`'s `google_artifact_registry_repository.legacy`, `count = var.adopt_legacy_registry ? 1 : 0`), just not yet one for adding `count` to a resource that started without it.

**This eliminates the earlier draft's Go-side refusal mechanism entirely**: there is nothing left to refuse. Terraform's own plan naturally keeps the grant whenever it is still needed (for however long that is — days, for a migrating installation; forever, for a bring-your-own one) and shows the `delete` — still gated by `tf.Guard`'s existing, unmodified `--allow-delete` requirement — exactly once both signals above confirm it is genuinely safe. Bring-your-own (design §3 option C) now has the real path the second review round asked for: nothing refuses, the grant simply never goes away for that installation, and `init --repo` keeps working normally, indefinitely.

**Files:**
- Modify: `deploy/terraform/gcp/modules/repo/build.tf` (`count` + `moved`, above), `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl` (its `build_role_set_on_own_registry` run's two assertions on `google_artifact_registry_repository_iam_member.build_base` — the resource now at index `[0]` — are updated to the indexed address, not deleted: the fresh-create scenario this test exercises still needs the grant, so it still exists at `[0]` there)
- Modify: `deploy/terraform/terraform_test.go` (`TestMirrorRunsAsUserNotBuildAccount`'s doc comment, not its assertions: its premise — "the image every repository's credential step runs comes from" `fugaro-base` — is now **only true for an installation whose `base_grant_needed` is still true**; its actual assertions (every grant on `fugaro-base` is either a reader or the one named operator-writer grant) remain correct and need no change, since this task never adds a new *kind* of grant, only conditions an existing one)
- Modify: `internal/infra/tf/testdata/golden/repo.plan.json`, `repo-adopt.plan.json` (both regenerated via `scripts/gen-golden-plans.sh`; an earlier draft's golden-plan regression test checked only `"repo"`, missing that `repo-adopt.plan.json` independently contains a `build_base` entry too)
- Modify: `internal/infra` (wherever `infra.Repo`'s Terraform-variable-building code lives — computes `base_grant_needed` from the two signals above, reading the check job via `infra.PlanCheckJob`)
- Modify: `docs/base-image-migration.md` (a short note: the grant now tracks real state automatically; a bring-your-own installation keeps it, permanently, with nothing to do)

**Interfaces:**
- `baseStillInInstallationRegistry(ref, host string) bool` (unchanged from the prior draft — still correct, just now feeding a Terraform variable instead of a Go-side refusal): `strings.HasPrefix(ref, host+"/"+mirror.BaseRepository+"/")`.
- A new function alongside it, computing the full `base_grant_needed` boolean from both signals, shaped like `func baseGrantNeeded(ctx context.Context, c *infra.Clients, lc *localcfg.Config, host string, spec infra.RepoSpec) (bool, error)` (name illustrative) — `true` if `baseStillInInstallationRegistry` is true, **or** if `infra.PlanCheckJob`'s read of the currently-deployed spec still shows an AR-hosted `base_images.base` for this repository's kind.

- [ ] **Step 1: Write the failing tests**

```go
func TestBaseStillInInstallationRegistry(t *testing.T) {
	const host = "us-east5-docker.pkg.dev/proj-1234"
	for ref, want := range map[string]bool{
		"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.6.0":       true,  // old managed copy
		"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc":    true,  // permanent bring-your-own, still here
		"ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64): false, // new pin: outside fugaro-base
		"registry.example.com/acme/custom-base:1":                             false, // bring-your-own pointed entirely elsewhere
		"": false,
	} {
		if got := baseStillInInstallationRegistry(ref, host); got != want {
			t.Errorf("baseStillInInstallationRegistry(%q) = %v, want %v", ref, got, want)
		}
	}
}

// TestBaseGrantNeededReadsTheDeployedCheckJobToo: local config saying
// "pinned" is not enough on its own — the deployed check job's own spec
// (a separate, independently-failable update, image refresh step 3) must
// agree, or a partial step-3 failure strands the daily check.
func TestBaseGrantNeededReadsTheDeployedCheckJobToo(t *testing.T) {
	lc := &localcfg.Config{GCPProject: "proj-1234", Region: "us-east5",
		BaseImages: map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)}}
	// fakeClients' PlanCheckJob-reachable state reports the DEPLOYED spec
	// still naming the old Artifact-Registry path (the implementer wires
	// this to whatever fake infra.Clients/PlanCheckJob this package's
	// existing tests already use for infra.PlanCheckJob).
	needed, err := baseGrantNeeded(context.Background(), fakeClientsWithStaleCheckJob(t), lc, "us-east5-docker.pkg.dev/proj-1234", fakeRepoSpec(t))
	if err != nil || !needed {
		t.Errorf("needed = %v, err = %v; want true: the deployed check job still reads the old path", needed, err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestBaseStillInInstallationRegistry|TestBaseGrantNeeded' -count=1`
Expected: FAIL (neither function exists yet).

- [ ] **Step 3: Implement**

`baseStillInInstallationRegistry`, unchanged from the prior draft. `baseGrantNeeded` calls `infra.PlanCheckJob` for the repository's own check job (same call shape `refreshCheckJob` already makes) and ORs `baseStillInInstallationRegistry` on the local config's value with the same check on the *old* (deployed) spec's `base_images.base` that `PlanCheckJob`'s own diff already reads. Wire the resulting boolean into the Terraform variables `infra.Repo` builds, as `var.repo.base_grant_needed`; add the `count` + `moved` block to `build.tf`; update `repo.tftest.hcl`'s two assertions to the `[0]`-indexed address (the test's own fresh-create scenario has no prior `base_images.base`, so `base_grant_needed` is true there and the grant still exists to assert on); regenerate both golden plans; update `TestMirrorRunsAsUserNotBuildAccount`'s doc comment only.

`docs/base-image-migration.md` gains, under "Everywhere": a short paragraph stating the grant now tracks real state automatically (no operator action, no ordering to remember), and a bring-your-own installation (design §3 option C) keeps it permanently, with nothing to do and no refusal to work around.

Run: `go test ./internal/cli/... ./internal/infra/... -count=1`
Expected: PASS.

## Task 6: CLI surface cleanup — flags, help text, dead confirmations

**Files:**
- Modify: `internal/cli/init.go` (`--base` removed outright — the decision from the prior draft, confirmed: a flag that does nothing for every value it used to accept is worse than no flag; `--expect-digest`/`--replace-image` help text updated), `internal/cli/image_refresh.go` (`newImageRefreshCmd`'s `Long` text), `internal/cli/init_images.go` (`confirmReplace`, `sourceLine`, `reviewLine` lose their copy-specific wording)
- Modify tests: `internal/cli/init_test.go`, `internal/cli/image_refresh_test.go`

**Interfaces:**
- `--base`: removed. Passing it is a cobra "unknown flag" error, not a Fugaro-level refusal — an earlier draft of this plan's own test wrongly asserted `err == nil` for `--base base`, which cannot be true once the flag no longer exists.
- `--expect-digest KIND=sha256:<hex>`: **picking the plan's wording, aligning the design doc to match (not the reverse)** — an earlier draft's design-doc wording ("able to override... the signed digest list's own resolution") and this plan's wording ("an extra equality check, refusing a mismatch") described two different behaviors; this plan's is correct and simpler (`basepin.Resolve`'s own result must equal `--expect-digest`'s value, or the step refuses — never an override of what verification already produced, since there is nothing weaker to override it *to*).
- `--replace-image KIND`: `base`'s meaning removed; `history` unchanged.

- [ ] **Step 1: Write the failing tests**

```go
func TestBaseFlagIsRemoved(t *testing.T) {
	// --base no longer exists at all: cobra itself refuses it, not a
	// Fugaro-level message (an earlier draft of this test wrongly expected
	// err == nil, written before this task's own "remove it outright"
	// decision was finalized).
	_, _, err := executeStdin(t, "", "init", "--base", "base", "--plan-only")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("err = %v, want cobra's unknown flag error", err)
	}
}
```

`internal/cli/image_refresh_test.go` gains/updates a case asserting the base step's confirmation text names "verified" and a digest, never "copies" and a megabyte figure, for the `base` kind, and that `--expect-digest base=sha256:...` failing to equal `basepin.Resolve`'s own result refuses (not "overrides" it).

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestBaseFlagIsRemoved|TestImageRefresh' -count=1`
Expected: FAIL (the flag still exists; today's text still says "copies").

- [ ] **Step 3: Implement**

Delete `--base`'s flag registration and its base-kind-copying code path entirely. Rewrite every confirmation and review-line string in `init_images.go`/`image_refresh_steps.go` that currently says "copies N image(s), M MB … into your registry" to instead say "verifies this release's base image against its signed digest and records \<ref\>."

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

## Task 7: Docs

**Files:**
- Modify: `docs/base-image.md` ("Building it locally", "Bring your own": the base pull is now direct and digest-pinned, not a copy; `--base base` no longer exists; no mention of "`fugaro init --base`" survives — an earlier draft left stray references to it as a verification site even after describing its own removal, a contradiction this task fixes along with Task 9's live-check table)
- Modify: `docs/base-image-migration.md` (Task 5's conditional-grant note, the "what used to be a copy is now a verified pin" note)
- Modify: `docs/gcp-setup.md` §"The base image, in `fugaro-base`" (rewritten: no more megabytes-through-your-machine language; the confirmation now names a digest; `fugaro-base` the registry repository is called out as kept, conditionally read, not simply "dormant" — Task 5's conditional grant means some installations keep reading it forever)
- Modify: `docs/release.md` (the `images.yml` section documents the two new cosign-sign steps inside `publish-base`/`publish`, gated to `push`; the "How `fugaro init` uses the images (the mirror)" section is split: history keeps today's wording, base gets the new verify-and-pin wording; F1 marked done for the base image specifically)
- No change expected: `SECURITY.md` (confirm with a careful read rather than edit speculatively)

**Acceptance:** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` passes.

- [ ] Draft each doc change, citing the design doc's wording where it already states the user-facing behavior, so the docs and the design never drift. Grep the whole repository for `` `fugaro init --base` `` and `--base base` after this task to confirm no stray reference survives anywhere (docs, this plan's own Task 9 table — see below).
- [ ] Run the docs/lint suite and `gofmt -l .`; fix anything it flags.

## Task 8: Full sweep

- [ ] `go build ./...`, `go vet ./...`
- [ ] `go test ./... -count=1` (every package this plan touched, plus a full run to catch an unrelated regression)
- [ ] `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
- [ ] `gofmt -l .`
- [ ] Re-read `docs/design/retire-image-mirror.md` against the final code and update anything that drifted during implementation.

## Task 9 (owner-run; live checks, NOT RUN until the owner runs them)

**Task 5's code does not merge/release until 38a and 38b below have both passed** (Global constraints) — removing availability for anything that currently reads `fugaro-base` is not something a test suite can fully substitute for.

Add to `docs/gcp-live-checklist.md`, in the existing numbered-table format, scoped to `fugaro`/`fugaro-dev`, sandbox repository only, never `belong`:

| # | Check | Command | Expected |
|---|---|---|---|
| 38a | The repository's own build service account (not the default compute SA) pulling the pinned base, both as a `docker pull` inside a step and as a step's own `name:` image (design retire-image-mirror.md §7, item a) | A one-off Cloud Build submitted as the sandbox repository's build account, one step `docker pull <pinned ref>`, one step `name: <pinned ref>` running a trivial command | Both steps succeed; record the elapsed time and the resolved digest as a `FACT`. |
| 38b | A full derived-image build with the digest-pinned `FROM`, end to end | `fugaro image refresh` in the sandbox repository's checkout, after the implementation lands | `credential`, `render`, `build`, `smoke`, `gate`, `promote`, `record` all succeed; the build record's `base_ref` is the ghcr.io-pinned form. |
| 38c | OCI-signature verification of a real signed base image, on the operator's own machine | `fugaro upgrade` (or `fugaro image refresh`) against a real release that has run Task 1's signing steps | The CLI reports the verified digest; a deliberately mismatched expected identity (a test build, or a manually-constructed bad reference) is refused (tamper-test, done by hand once). |
| 38d | Egress/rate-limit behavior under load | — | **Out of scope for this design (§3); not planned.** |
| 38e | The nightly's own run genuinely never touches the `-pinned` tag (the fix for the severe window a third review round found, design §2) — a GitHub Actions/ghcr.io check, unrelated to any GCP project | Manually dispatch `images.yml` by `workflow_dispatch` once (never a real tag push), and compare `fugaro-base:<v>-pinned`'s digest before and after | Unchanged; the plain `fugaro-base:<v>` tag's digest changes (today's existing nightly behavior), the `-pinned` tag's does not. |

Number `38a`–`38e` follows the next free slot after the generic-tool plan's Check 38 (`docs/gcp-live-checklist.md`'s own out-of-order note already reserves numbers this way); the owner renumbers at integration time if another plan has since claimed 38.
