# One location preference for an installation

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-20. Checked against `origin/main` at `252329076`.

An installation's region decides where everything it stores goes. Setup asks a place (US or EU)
and takes its default region, or a region typed in full; a mapping the product owns places both
databases, the secrets and the run logs from it; and `fugaro update` records, for an installation
made before this work, where its data already is, moving nothing. It carries out D3029 of
[always a budget](../always-a-budget/README.md), whose task AB7 waits on this plan's **LP2**.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's ruling; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, the census of every location fixed in code, what each Google product offers (read 2026-11-20), findings F1–F21, the mapping, decisions D3080–D3097 with the owner's answers, resolutions R1–R10, open items O1–O13, UNVERIFIED U1–U9. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks LP0 to LP15 (LP5 is LP5a, LP5b and LP5c: 18 runs); levels, critical path, shared files; Checks 820 and 821 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |
| [record.md](record.md) | The `locations` block of the project config: its four members and their spellings, that it is one machine's own, and that nothing reads it as an input (LP0) |

**Numbers reserved:** decisions **D3080–D3099** (D3080–D3097 used), checks **820–829** (820 and
821 used). Read on 2026-11-20: the highest decision on `main` was D3039 (reserved by
always-a-budget, D3020–D3029 used there); D3040–D3059 and checks 800–809 are held by the open
pull request #3308 (ci-affected-tests); D3060–D3079 and checks 810–819 by the open pull request
#3315 (credentials-belong-to-models), and again by #3323, which is theirs to settle. The highest check on `main` was 791, with 790–799 reserved.
No open pull request takes a number in D3080–D3099 or 820–829.

**Owner answers of 2026-11-20:** D3080 to D3091, **every one as recommended**; none is marked
"not the recommendation". Two are marked **amended after review**, D3087 (round 1) and D3088 (round 3), and
**the owner confirmed both amended texts on 2026-11-20**.

**Owner answers after the reviews, 2026-11-20:** D3092 to D3096 as recommended (new secrets are
regional on every installation; the four further exceptions to the promise; AB7 once LP2, LP4 and
LP8 have merged, and LP3, LP6, LP10 and LP14 once AB7 has; the key service's redeploy is a line
in `docs/release.md`; a region that cannot be checked is accepted with a line). **D3097 is not
the recommendation**: a fourth review of the documents, where an owner's plan-only run on the
dogfood installation before LP2 and LP5a merge was recommended. The real provider's behaviour
(U9) therefore stays unverified until Check 821. In short: the region stays the one stored setting and the place is
derived from it (D3080); US and EU are named, other regions work with stated fallbacks (D3081); a
new installation's data at rest, secrets and run logs included, is in the place (D3082); model
calls are outside the promise (D3083); a workspace's members may differ (D3084); Firestore goes in
the installation's own region where offered (D3085); EU means `europe-west1` when no region is
given (D3086); a locations record in the project config (D3087); a changed region is refused
(D3088); an older installation's missing database goes in its derived place (D3089); the
location's own confirmation is removed everywhere (D3090), which reverses the ruling D12 of
2026-10-20 for the location; a database found elsewhere is adopted, recorded and reported (D3091).

**Review round 1** changed no answer but one. **D3087 is amended after review**: the record is
each machine's own, is not published in the shared config (which decodes strictly and would stop
an older CLI), and is never an input to a plan; what exists is read from the cloud each time. It
added LP5c (the intake service's secrets) and LP15 (the vendor's key service reads a regional
database), ordered LP2 and LP3 after LP8, and listed the two cases in which run output is not in
the run log bucket. The amended D3087, the grown list of exceptions and R10 were then put to the
owner, who answered them the same day (below).

**Review round 2** changed no answer. It replaced the rule for secrets with a simpler one: a
secret that exists is left alone (Terraform ignores its replication), and every secret created
from then on is in the region, on an old installation too, so nothing has to read the cloud
before it plans a secret, `fugaro update --forget` included. It found that LP2 and LP3 share a
file and ordered them, completed LP10's and LP6's file lists, corrected three test patterns that
matched nothing, and added the key service's redeploy as the owner's step (O9). What it sharpened
in D3082 is listed for the owner (O8).

**Review round 3** was the last of the three the skill allows. **D3088 is amended after review**: `--region` is a tested
per-invocation override on read commands, so the refusal is in `setup` and `update`, the
commands that plan (O12). It also found that discovery never reads a Realtime Database already in
Terraform's state, so LP2 has Terraform ignore a managed instance's region, or a European
installation's existing database would have planned a replacement. It added files to LP6 and
LP10, moved LP7 after LP5b, and moved the key service's step to `docs/release.md`. Its fixes were read by round 4.

**Review round 4** ran because the owner asked for it (D3097). It found no path on which an
existing installation's `fugaro update` plans a replacement. It found that LP8's refusal would
turn one existing test of `update` red and missed `--repo`, that LP5a must add its variable to
`intakeServiceEnv` in `internal/infra/tf/cover.go`, that two fakes lack what LP8 and LP6 need,
and, by running it, that a "no replacement" assertion under the mock provider passes with or
without `ignore_changes`, so LP2 and LP5a assert the planned value. LP5b lost its dependency on
LP1 and LP7 moved to level 1. **Round 4's fixes were not reviewed.** What the real provider does
(U9) is still measured only by Check 821.

**The contract-first pass** broke no edge: the one break tried (the record as a contract) read as
no gain and was undone. The plan says why.

**What a reader should not take from here:** no task is approved; each needs the owner's go by
name. Checks 820 and 821 are owner-run after the release. The EU row is tested against fakes
until Check 820 passes (U1, U2, U6).
