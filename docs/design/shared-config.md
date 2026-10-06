# Shared installation config (design)

*Status: 2026-10-06, design approved in conversation with the user (parts 1 to 3), written spec awaiting review. Background: [m11-setup-and-skills.md](m11-setup-and-skills.md) (adopt, `init`), [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md) (project identity, the marker), [../gcp-setup.md](../gcp-setup.md). Code references below are from a read of the tree at v0.3.1 and are to be re-checked by the plan's first task.*

## 1. Problem and goal

Every person who uses an installation keeps a local project config, `~/.config/fugaro/projects/<name>.yaml`. A teammate builds theirs with `fugaro init` (adopt), which reads the Terraform state bucket (operators only) and needs `--gcp-project` and `--region`. Almost everything in the file is installation-wide and not secret. A plain launcher cannot join at all, because the state bucket is operator-only.

**Goal.** A teammate with a launcher or operator role, in a checkout of an onboarded repository, on a fresh machine, runs `fugaro run`, `ls`, `diagnose` and the rest with no setup command. A tampered shared file cannot redirect their tools to another project's database or token signer, and no secret ever appears in it.

**Non-goals.** Replacing the local config (it stays, and always wins). Changing how operators adopt. Sharing anything per-person or owner-only. Discovering installations that use a custom `--runs-bucket` name.

## 2. Decisions (from the user)

1. The project ID comes from an optional `gcp_project:` in the checkout's `fugaro.yaml`, next to `project:`. It is never guessed from the project name (§5).
2. A fetched config is cached in the user's cache directory and refreshed daily (§6).
3. `init` publishes the shared file; a repository's `init --repo` writes the `gcp_project:` line (§4, §8).

## 3. Approach

A separate object, `fugaro/config.yaml`, in the runs bucket, written by `fugaro init` (Go), read by project selection with a cache.

Rejected: extending the marker `fugaro/project.json` (Terraform-written, three fields, 4 KiB read cap, and the budget values come from a different Terraform root, so one file would couple two roots); keeping it in the Firebase database (not every launcher can read it, and its mark logic is delicate).

Trust level: launchers and operators already hold `roles/storage.objectAdmin` on the runs bucket (`modules/installation/iam.tf`), so they can read the file with no new grant, and any of them can already overwrite the marker. The shared file is exactly as trustworthy as the marker. The validation in §5 is defence in depth, not a new boundary.

## 4. The published file

**Writer.** `fugaro init` writes `fugaro/config.yaml` to the runs bucket at the end of any run that writes the local config (after `writeConfig`), from the final merged config, through `Marshal` of a published subset. `fugaro init --publish-config` does only that step. No Terraform change: Terraform does not own this object. An installation that predates this feature gets the file the next time `init` runs against it.

**Included** (installation-wide, not secret): `version`, `name`, `gcp_project`, `region`, `runs_bucket`, `registry_host`, `base_images`, `build`, `log_view`, `scheduler_region`, the `budget` block (`mode`, `rtdb_url`, `firebase_project`, `firebase_api_key`, `token_signer`, per-run default), `model_prices`, `compute_prices`, `providers`, `watch`, `max_parallel`, and `repos` (provider, base branch, workflows, GitHub App ID). The four budget connection values are documented as not secret.

**Excluded:** the whole `terraform:` section (state bucket, launchers, operators, budget admins, alert email, cleanup flags), `user:`, and `endpoints:` (test and fake overrides). These are owner-only or per-person.

**Format and size.** YAML through the same strict `localcfg.Parse` (unknown keys refused). The read cap is 64 KiB, larger than the marker's 4 KiB because `repos` grows.

## 5. Reading and validating

**When.** Only when selection finds no local config for the project: `--config` and a local `projects/<name>.yaml` always win. The project name is the checkout's `project:`. The GCP project is the checkout's `gcp_project:`, or the existing `--gcp-project` flag. The bucket is `fugaro-runs-<gcp_project>`. If `gcp_project:` is missing the error says exactly which line to add and that whoever onboarded the repo can run `fugaro init --repo` to add it. **There is no guessing and no hint of a guess:** the committed, reviewed value is the trust anchor. A guessed project ID would make the claim self-fulfilling (a stranger's project of that name with a matching marker would pass every consistency check), and the convention does not even hold for our own installations (`fugaro` is in `fugaro-dev`, belong was in `edge-devel-dimi`).

**Sequence.**
1. Read the marker through the existing `ReadProjectMarker`, which requires the managed label on the bucket and a matching project number. Its name and GCP project must equal what the checkout said.
2. Read `fugaro/config.yaml`, at most 64 KiB, through `blobx`, and `Parse` it strictly.
3. Cross-check and refuse (never repair) on any failure, naming the field and telling the user to ask an operator to re-run `fugaro init`:
   - `name`, `gcp_project` and `runs_bucket` equal the marker and the bucket the file was read from;
   - `registry_host` is `<region>-docker.pkg.dev/<gcp_project>`, and every `base_images` entry is under it;
   - `log_view` is under `projects/<gcp_project>/`;
   - the budget block passes the existing Firebase output validation (`CheckFirebaseOutputsFor`): the database host derives from `firebase_project`, and `token_signer` is `fugaro-token-signer@<firebase_project>.iam.gserviceaccount.com`;
   - no `endpoints:`, `user:` or `terraform:` section is present.

## 6. Cache

One file per project in the user's cache directory (not the config directory), holding the validated config, the object's generation, the bucket and the last check time. Within a day it is used as is. After a day: a conditional read by generation (unchanged only refreshes the stamp). If the refresh fails for lack of network, the cache is used for up to 7 days with a warning, then the command fails. If the marker or object is gone, or the new content fails validation, the cache is dropped and the command fails. The existing name-check cache (`localcfg/checkcache.go`) is the pattern to follow.

## 7. Selection and commands

The fallback lives at the one choke point, `localcfg.Select` (`select.go`), before it returns `ErrMissing` (`selector.run`, `named`); every cloud command reaches it through `selectNamed`, `selectFrom`, `selectProject`. `validate` and `config example` (`selectedProjectConfig`) stay offline and never fetch. `fugaro doctor` gains one information line: the config came from the shared file (generation, age), or a local file overrides it and whether it differs from the shared one. `init`'s adopt flow is unchanged; operators still adopt through Terraform state.

## 8. The `gcp_project:` line

`config.Config` (`internal/config/config.go`) gains an optional `gcp_project:` (validated as a GCP project ID) beside `project:`, because its parser refuses unknown keys. `config.ProjectOf` (the lenient scan) gets a sibling that reads it, and `localcfg.Checkout` carries it. The runner ignores the value. `fugaro init --repo` writes the line into the checkout's `fugaro.yaml` (the diff shown first, as for the plugin settings); the owner commits it with the repo's other changes, and on EdgeWeb the user merges.

## 9. Compatibility hazard and rollout

The `fugaro.yaml` parser is strict, and job images carry a baked-in `fugaro` binary that reads the repository's `fugaro.yaml` from the base branch (and the daily image check reads it too). A repo whose `fugaro.yaml` gains `gcp_project:` while its image still holds an older binary would have runs and the daily check refuse the file until the image is rebuilt. The rollout order, written into `docs/release.md` and the release notes:

1. Merge and release the version that adds the field.
2. Upgrade the CLI; run `fugaro init` once per installation. This only publishes the shared file. No repository changes.
3. Rebuild each repository's image (`fugaro image build`), baking in the new binary.
4. Run `fugaro init --repo` in each checkout to add the line; commit and merge it. `init --repo` warns when the repository's current image predates the field and says to rebuild first.

Until step 4 nothing changes for teammates; a checkout without the line gets the clear "add this line" error and can use `init` as today.

## 10. Tests

- **Validation** (security-critical, table-driven, one tampered fixture per rule): wrong registry host, signer outside the Firebase project, mismatched name, GCP project or bucket, `endpoints:`, `user:` or `terraform:` present, unknown key, oversize file, marker without the managed label. Each is refused with the field named.
- **Cache:** use within the day, conditional refresh by generation, the 7-day offline allowance, dropping on a missing or invalid object.
- **Selection:** a local file and `--config` win; the missing-`gcp_project:` message; `validate` and `config example` never touch the network.
- **Publish:** a golden file proving the published subset, and that `terraform:`, `user:` and `endpoints:` never appear.
- **End to end** against the existing fakes: fresh config and cache directories and a checkout with `gcp_project:`; `fugaro ls` and a launch resolve the shared config with no local file.
- **Live Check 28** in `docs/gcp-live-checklist.md`: from a clean state, `fugaro doctor` and `fugaro ls` in the sandbox checkout show the shared-config line. The check sets both the config and the cache directories explicitly (a stale `XDG_CONFIG_HOME` in a terminal sent live config writes to a temporary directory during D19).

## 11. Work breakdown and review

About six tasks: (1) the `gcp_project:` field and its parser; (2) the publisher in `init` and `--publish-config`; (3) the fetch, validate and cache reader; (4) the selector fallback and the `doctor` line; (5) `init --repo` writing the line and the image warning; (6) docs and the live check. Task 3 (validation) gets a second, independent reviewer. Estimated half a day to a day with review.

## 12. Open items

None blocking. To decide in the plan: the 64 KiB cap and the 7-day offline allowance are starting values, and the cache location follows the platform's user cache directory (`os.UserCacheDir`).
