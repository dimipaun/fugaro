# The locations record

The project config on this machine carries one optional block, `locations`: where the installation's
located things are. `fugaro setup` and `fugaro update` write it from what the cloud holds, as they
converge the installation, after the last apply. This document says what it is; it is a reference for
a person reading the file or a config dump, not a design.

```yaml
locations:
  firestore: us-east5
  rtdb: us-central1
  secrets: automatic
  log_bucket: global
```

## What each member holds

| Member | The thing | Written as |
| --- | --- | --- |
| `firestore` | the project's Firestore database — the run history's | the region or multi-region Google put it in: `us-east5`, `nam5`, `eur3` |
| `rtdb` | the project's default Realtime Database instance — the budget's | a region: `us-central1`. A Realtime Database has no multi-region and is never `global` |
| `secrets` | what the installation's own Secret Manager containers do | `automatic` (Google's default replication, which today means the United States), `mixed` (some automatic, some regional), or the regions they are replicated in, as a list: `[europe-west1]` |
| `log_bucket` | the installation's run log bucket | the region or multi-region Cloud Logging offers it in (`europe-west1`, `us`, `eu`), or `global` on an installation made before the run-log work had a location |

An absent key means **not known yet**, and the same sentence covers both cases it stands for: no
converge has read the cloud on this machine yet, and the thing does not exist at all — there is no
database to be anywhere. An installation set up with `--no-log-isolation` has no bucket of its own,
so `log_bucket` stays absent there. A member the cloud could not be read for stays absent too: the
record says what was read, and never a guess.

`mixed` is the ordinary answer on an installation made before this work. Its containers that already
existed keep the automatic replication they have — a container's replication is ignored by Terraform
rather than changed — and every container created since is replicated in the installation's region
(D3082, D3092, R5).

## One machine's own, and never an input

**It is not published in the shared config.** The published copy that goes into the runs bucket is
read by every teammate's Fugaro, and it decodes strictly: a `locations` key in it would stop every
teammate whose Fugaro is older than the release that wrote it, with the whole file refused. Nothing
in the record is for another machine to read anyway — each machine writes its own record from the
cloud on its next `fugaro update`, and where two machines disagree, each is right about what it read.
The publish drops the block, the merge never adopts one back, and a shared config carrying the key is
refused by name (`internal/localcfg/shared.go`, `internal/cli/sharedcfg.go`; F17).

**Nothing reads it as an input.** Every plan asks the cloud itself, for the thing it is about: a
database that exists is adopted or kept wherever it is, a container keeps its replication, and the
mapping that places new things answers only for a thing that does not exist (R5, D3087). So a
record that is wrong, stale or hand-edited changes no plan, no import and no apply — and the converge
that reads the cloud corrects it on its next run. Its one reader is `fugaro doctor` (LP12), which
prints where the data is: information, and never a failure when a thing is outside the installation's
place (D3091).

The loader holds the block to the shape above as the file is read: a `firestore` in capitals, a
multi-region Realtime Database, a `secrets` that is neither of the two words nor a list of regions, a
region listed twice — each is refused by name, where it was written, rather than printed later as a
doctor row about where the data is.

## Where the rest is written

The record is the answer to D3087 of [reconciliation.md](reconciliation.md), amended after review
round 1; what exists is read from the cloud by `infra.ObserveLocations` (LP7), and the record is
written by the two converge doors (LP14). The places a new installation's things are created in are
the mapping's, in [implementation-plan.md](implementation-plan.md); the record only says where they
ended up.
