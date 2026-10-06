# Shared installation config (design)

*Status: 2026-10-06, built on branch `design-shared-config`; the task numbers are those of [../plans/2026-10-06-shared-config.md](../plans/2026-10-06-shared-config.md). Rulings made during review are folded into the sections below, and §13 lists the accepted limits. Background: [m11-setup-and-skills.md](m11-setup-and-skills.md) (adopt, `init`), [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md) (project identity, the marker), [../gcp-setup.md](../gcp-setup.md). The text below says what the code does.*

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

Trust level: launchers and operators already hold `roles/storage.objectAdmin` on the runs bucket (`modules/installation/iam.tf`), so they can read the file with no new grant, and any of them can already overwrite the marker. The shared file is exactly as trustworthy as the marker, which is read the way `checkCloudName` reads it. The validation in §5 is defence in depth, not a new boundary.

## 4. The published file

**Writer.** `fugaro init` writes `fugaro/config.yaml` to the runs bucket at the end of any run that writes the local config (after `writeConfig`), including `init --repo` and `init --base`, through `Marshal` of a published subset. `fugaro init --publish-config` does only that step. No Terraform change: Terraform does not own this object. An installation that predates this feature gets the file the next time `init` runs against it.

**Publishing is a merge.** The publisher first reads the object that is there (through `ParseShared`, against the local config's own name, GCP project and bucket) and merges the local view into it, so a machine that lacks what another onboarded does not erase it: maps (`repos`, `base_images`, `model_prices`, `compute_prices`) are unioned with the local entry winning on a key clash; scalars and pointers take the local value when non-zero, else the existing one; `version`, `name`, `gcp_project` and `runs_bucket` always come from the local config. An existing object that is unparseable, over 64 KiB or refused by `ParseShared` is replaced, with a warning; that includes one of another installation, which fails the identity check (`MergeShared` also ignores a foreign object, a second line of defence the CLI path does not reach). If the merged output is over 64 KiB (a writer bloated a price table below the cap) the publisher falls back to the local view alone, with a warning. The publisher then runs `ParseShared` on the exact bytes it is about to write, and never writes a file teammates would refuse (it warns instead).

**Only for the default runs bucket.** Teammates find the bucket from `gcp_project:` alone (`fugaro-runs-<gcp_project>`), so the publisher, and the `gcp_project:` line that `init --repo` writes (§8), apply only to installations whose runs bucket has that name. A custom `--runs-bucket` installation does not publish and gets a warning.

**Included:** everything in the local project config (`localcfg.Config.Shared()`) except what is listed under Excluded: so the whole `budget` block (mode, per-run cap, token ceiling, allowed models, database URL, Firebase project, API key, token signer, grace), `backend`, prices, `repos` (including each repository's `vertex` flag) and the rest. The four budget connection values are documented as not secret.

**Excluded:** the whole `terraform:` section (state bucket, launchers, operators, budget admins, alert email, cleanup flags), `user:`, and `endpoints:` (test and fake overrides), which are owner-only or per-person; `bucket_url` and the legacy `registry`, which are derived; and three things that are not published and are **refused when read**, each for a reason:

- `providers:`: a hostile `base_url` would send model traffic and provider data to another host, and no host can be pinned. Runs take providers from the repository's reviewed `fugaro.yaml`; a teammate who needs overrides sets them locally.
- each repository's `base_branch`: it would override the reviewed checkout's `git.base_branch` in the image build, `init` and `run`, so a writer could get an operator's image build to clone an unreviewed branch. Consumers use the checkout's value, else `main`.
- `build.service_account`: deprecated.

`build.region` and `build.machine_type` are validated when any local config is loaded (a region pattern; `^[A-Za-z0-9_-]{1,40}$`), because they reach a Cloud Build resource path.

**Format and size.** YAML through the same strict `localcfg.Parse` (unknown keys refused), after a shape check: one document holding a mapping, with no merge keys (`<<`), anchors, aliases, explicit tags or repeated keys at any depth. Without that check a merge key could carry a forbidden section past the key checks. The read cap is 64 KiB, larger than the marker's 4 KiB because `repos` grows.

## 5. Reading and validating

**When.** Only when selection finds no local config for the project: `--config` and a local `projects/<name>.yaml` always win. The project name is the checkout's `project:`. The GCP project is the checkout's `gcp_project:`, or the existing `--gcp-project` flag. The bucket is `fugaro-runs-<gcp_project>`. If `gcp_project:` is missing the error says exactly which line to add and that whoever onboarded the repo can run `fugaro init --repo` to add it. **There is no guessing and no hint of a guess:** the committed, reviewed value is the trust anchor. A guessed project ID would make the claim self-fulfilling (a stranger's project of that name with a matching marker would pass every consistency check), and the convention does not even hold for our own installations (`fugaro` is in `fugaro-dev`, belong was in `edge-devel-dimi`).

**Sequence.**
1. Read the marker with the same plain bucket read `checkCloudName` uses (`blobx`, at most 4 KiB, `Version == 1`), and require its name and GCP project to equal the checkout's. `infra.ReadProjectMarker` is not used: it needs the project number from Cloud Resource Manager (`projects.get`), which a plain launcher's custom role does not have, and the launcher is the primary user. The trust level is the status quo: every cloud command already trusts this marker through `checkCloudName`, which runs again after selection.
2. Read `fugaro/config.yaml`, at most 64 KiB, through `blobx`, and `Parse` it strictly.
3. Cross-check and refuse (never repair) on any failure, naming the field and telling the user to ask an operator to re-run `fugaro init`:
   - `name`, `gcp_project` and `runs_bucket` equal the marker and the bucket the file was read from;
   - `registry_host` is `<region>-docker.pkg.dev/<gcp_project>`, and every `base_images` entry is `<registry_host>/fugaro-base/<path>` with an optional tag and `@sha256:` digest, in the Docker reference grammar (no `..` or `.` path component);
   - `log_view` is under `projects/<gcp_project>/`;
   - `budget.firebase_project` equals `gcp_project`. An installation with a separate Firebase project is refused with "run fugaro init (adopt)": the checkout anchors only one project, and an internally consistent triple (database, signer, project) of a different project would otherwise pass. The same-project layout is the supported default;
   - the budget block passes the existing Firebase output validation (`CheckFirebaseOutputsFor`): the database host derives from `firebase_project`, and `token_signer` is `fugaro-token-signer@<firebase_project>.iam.gserviceaccount.com`;
   - no `endpoints:`, `user:`, `terraform:`, `bucket_url:`, `registry:` or `providers:` section, no `build.service_account`, and no repository `base_branch`.

Error messages quote hostile text (`%q`) so a writer cannot inject terminal escapes or newlines. A 403 from the bucket is an error ("no access"), never "absent": the shared-config readers use a strict read (`blobx.ReadStrict` and `ReadMaxStrict`) that returns it as itself. Ordinary reads keep the driver's 403-as-absent mapping, because the job and build accounts hold prefix-conditioned grants (no list permission, so GCS answers a missing object with 403).

## 6. Cache

One file per project in the user's cache directory (not the config directory), holding the validated config, the object's generation, the bucket and the last check time. Within a day it is used as is. After a day: the object is re-read (at most 64 KiB) and its generation compared with the cached one (unchanged only refreshes the stamp); `blobx` has no conditional-read primitive, and the extra read is small. If the refresh fails for lack of network, the cache is used for up to 7 days with a warning, then the command fails. The cache is dropped only when the entry itself fails validation, or when the marker or object has vanished or been refused; a cancel, a 5xx, a 403 or an unreachable bucket past the allowance fails the command and leaves the entry in place. The offline fallback applies only when the bucket cannot be reached at all (no route, DNS failure, timeout): any answer from the server, a refusal above all, means no fallback, so a revoked or tampered installation does not keep working from the cache. The existing name-check cache (`localcfg/checkcache.go`) is the pattern to follow, including its location (see §12).

## 7. Selection and commands

The fallback lives at the one choke point, `localcfg.Select` (`select.go`), before it returns `ErrMissing` (`selector.run`, `named`); every cloud command reaches it through `selectNamed`, `selectFrom`, `selectProject`. `validate` and `config example` (`selectedProjectConfig`) stay offline and never fetch, and so does `init`, which only writes a local config (a command that would write the selected config refuses when it is the shared one). `--gcp-project` must agree with the checkout's `gcp_project:` where the shared config is looked up. `fugaro doctor` gains one information line: the config came from the shared file (generation, age), or a local file overrides it and whether it differs from the shared one. `init`'s adopt flow is unchanged; operators still adopt through Terraform state.

## 8. The `gcp_project:` line

`config.Config` (`internal/config/config.go`) gains an optional `gcp_project:` (validated as a GCP project ID) beside `project:`, because its parser refuses unknown keys. `config.ProjectOf` (the lenient scan) gets a sibling that reads it, and `localcfg.Checkout` carries it. The runner ignores the value. `fugaro init --repo` writes the line into the checkout's `fugaro.yaml` only for a default-named runs bucket (§4), after the plugin stage's pattern: the diff is shown first; `--yes` writes; a terminal is asked `[y/N]`; non-interactive without `--yes` writes nothing and prints the line for the user to add by hand; `--plan-only` writes nothing. It inserts only after a simple single-line top-level `project:` scalar and refuses other shapes (quoted or block-scalar `gcp_project:`, multi-line values, a different existing `gcp_project:`), printing the line instead; it re-parses its own edit and refuses it unless the project and `gcp_project` are as intended; CRLF files keep their line endings. The owner commits it with the repo's other changes, and on EdgeWeb the user merges. `init --repo` also warns, naming the `fugaro image build` command, while the repository's current image predates the field (§9), and also when the line is already present.

## 9. Compatibility hazard and rollout

The `fugaro.yaml` parser is strict, and job images carry a baked-in `fugaro` binary that reads the repository's `fugaro.yaml` from the base branch (and the daily image check reads it too). A repo whose `fugaro.yaml` gains `gcp_project:` while its image still holds an older binary would have runs and the daily check refuse the file until the image is rebuilt. The rollout order, written into `docs/release.md` and the release notes:

1. Merge and release the version that adds the field.
2. Upgrade the CLI; run `fugaro init` once per installation. This only publishes the shared file. No repository changes.
3. Rebuild each repository's image (`fugaro image build`), baking in the new binary.
4. Every teammate's CLI and every CI job or pin that runs `fugaro` against the repository (validate, doctor --plugin --strict, run) must be on the new release BEFORE the gcp_project line is merged: older versions refuse the key as unknown (the parser is strict).
5. Run `fugaro init --repo` in each checkout to add the line; commit and merge it. `init --repo` warns when the repository's current image predates the field and says to rebuild first, and says the same of teammates' CLIs and CI pins when it offers the line.

Until step 5 nothing changes for teammates; a checkout without the line gets the clear "add this line" error and can use `init` as today.

## 10. Tests

- **Validation** (security-critical, table-driven, one tampered fixture per rule): wrong registry host, signer outside the Firebase project, mismatched name, GCP project or bucket, `endpoints:`, `user:` or `terraform:` present, unknown key, oversize file, marker with the wrong version or a different name or GCP project. Each is refused with the field named.
- **Cache:** use within the day, refresh by re-reading and comparing generations, the 7-day offline allowance, dropping on a missing or invalid object.
- **Selection:** a local file and `--config` win; the missing-`gcp_project:` message; `validate` and `config example` never touch the network.
- **Publish:** a golden file proving the published subset, and that `terraform:`, `user:` and `endpoints:` never appear.
- **End to end** against the existing fakes: fresh config and cache directories and a checkout with `gcp_project:`; `fugaro ls` and a launch resolve the shared config with no local file.
- **Live Check 28** in `docs/gcp-live-checklist.md`: from a clean state, `fugaro doctor` and `fugaro ls` in the sandbox checkout show the shared-config line. The check sets both the config and the cache directories explicitly (a stale `XDG_CONFIG_HOME` in a terminal sent live config writes to a temporary directory during D19).

## 11. Work breakdown and review

About six tasks: (1) the `gcp_project:` field and its parser; (2) the publisher in `init` and `--publish-config`; (3) the fetch, validate and cache reader; (4) the selector fallback and the `doctor` line; (5) `init --repo` writing the line and the image warning; (6) docs and the live check. Task 3 (validation) gets a second, independent reviewer. Estimated half a day to a day with review.

## 12. Open items

None blocking. To decide in the plan: the 64 KiB cap and the 7-day offline allowance are starting values, and the cache location follows `localcfg/checkcache.go` (`getenv`, `XDG_CACHE_HOME`, else `$HOME/.cache`), not `os.UserCacheDir`.

## 13. Known limits and accepted residual risks

Found in review and not fixed, on purpose:

- **Zero values cannot clear a published scalar.** The merge takes the local value only when it is non-zero, so removing a scalar locally leaves the published one. Edit or delete the object by hand.
- **Offboarding is not published.** A repository removed from the local config stays in the published `repos` until the object is edited, or republished by hand.
- **Version skew.** A legitimate object written by a newer or older fugaro (a key this binary does not know, or one it now refuses) fails `ParseShared`, so the publisher replaces it, dropping other machines' repositories and prices, and teammates on that binary are refused with "ask an operator to run fugaro init again".
- **A writer can relax policy for people who use the shared file.** Anyone holding `objectAdmin` on the runs bucket can set `budget.mode: off`, `per_run_usd` or `allowed_models`. A launcher can already launch with `--no-budget-check`, so this adds no capability, but teammates without a local config inherit it.
- **A foreign `firebase_api_key`** can only cause denial of service.
- **Repository keys such as `../..`** are accepted; no sink that builds a path from one is known.
- **A fixed object can outlive its fix in a cache** for up to 24 hours, by design (§6).
- **Custom runs-bucket names are unsupported for auto-fetch.** The anchor is written only for default names because a stranger could create `fugaro-runs-<project>` for a custom-bucket installation, and a teammate who passed that project with `--gcp-project` would read the stranger's object (it must still pass every check of §5, but a squatter controls the marker too).
- **The publish convention check and the write differ in source.** The check compares `RunsBucketName()`; the write goes to `lc.BucketURL()`. A config whose `bucket_url` does not match its runs bucket publishes where teammates do not look.
