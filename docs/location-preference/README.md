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
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, the census of every location fixed in code, what each Google product offers (read 2026-11-20), findings F1–F19, the mapping, decisions D3080–D3091 with the owner's answers, resolutions R1–R9, open items O1–O5, UNVERIFIED U1–U6. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks LP0 to LP14 (LP5 is LP5a and LP5b: 16 runs); levels, critical path, shared files; Checks 820 and 821 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3080–D3099** (D3080–D3091 used), checks **820–829** (820 and
821 used). Read on 2026-11-20: the highest decision on `main` was D3039 (reserved by
always-a-budget, D3020–D3028 used there and D3029 in its open pull request #3305); D3040–D3059 and
checks 800–809 are held by the open pull request #3308 (ci-affected-tests); D3060–D3079 and checks
810–819 are set aside for a spec in progress, by the coordinator's word, and appear in no open pull
request. The highest check on `main` was 791, with 790–799 reserved. The other open pull requests
whose documents were read (#3303, #3296) take no number above D3039.

**Owner answers of 2026-11-20:** D3080 to D3091, **every one as recommended**; none is marked
"not the recommendation". In short: the region stays the one stored setting and the place is
derived from it (D3080); US and EU are named, other regions work with stated fallbacks (D3081); a
new installation's data at rest, secrets and run logs included, is in the place (D3082); model
calls are outside the promise (D3083); a workspace's members may differ (D3084); Firestore goes in
the installation's own region where offered (D3085); EU means `europe-west1` when no region is
given (D3086); a locations record in the project config (D3087); a changed region is refused
(D3088); an older installation's missing database goes in its derived place (D3089); the
location's own confirmation is removed everywhere (D3090), which reverses the ruling D12 of
2026-10-20 for the location; a database found elsewhere is adopted, recorded and reported (D3091).

**The contract-first pass** broke no edge: the one break tried (the record as a contract) read as
no gain and was undone. The plan says why.

**What a reader should not take from here:** no task is approved; each needs the owner's go by
name. Checks 820 and 821 are owner-run after the release. The EU row is tested against fakes
until Check 820 passes (U1, U2, U6).
