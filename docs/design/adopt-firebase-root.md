# Adopting an existing Firebase root (design)

*Status: 2026-10-06, design proposal; the three open questions are decided (see Decisions); awaiting the user's approval to implement. Nothing here is built. Background: [m11-setup-and-skills.md](m11-setup-and-skills.md) (one confirmation per run, `tf.Cover`), [../gcp-setup.md](../gcp-setup.md) ("Retrying the migration later": what the installation root imports again), [v1.md](v1.md). Code references are to `main` at `bfee1e8`.*

## 1. Problem

On 2026-10-06 the belong installation moved to a state bucket that did not hold the Firebase root's state, while its Firebase project, `fugaro-belong`, already held everything an earlier apply of that root had created (recorded in the old installation's state bucket). `fugaro init --firebase fugaro-belong` passed its pre-checks (the project, billing, the database's mark), planned the Firebase root against an empty `fugaro/firebase` state, and the apply failed on four creates:

| Address (in the root, under `module.firebase.`) | Error |
|---|---|
| `google_firebase_database_instance.this` | `Error creating Instance: Only one default database is allowed` |
| `google_apikeys_key.web` | `Error creating Key: Resource already exists` |
| `google_service_account.signer` | `Service account fugaro-token-signer already exists` |
| `google_project_iam_custom_role.token_minter` | `Custom project role projects/fugaro-belong/roles/fugaroTokenMinter already exists and must be imported` |

The other creates of that apply succeeded (the project services, `google_firebase_project.this`, the project IAM members): creating an existing one of those is accepted by the provider. The user merged five resources (the four above and `google_service_account_iam_member.minter`) from the old state file into the new one with a throwaway script; the next `init` showed no changes.

The installation root never needs that: `infra.DiscoverInstallation` (`internal/infra/discover.go`) reads what exists before the plan, and `infra.WriteImports` turns what is ours into import blocks (`imports.tf.json`), so the plan says `Plan: N to import, ...` (`tf.Summary`). The Firebase stage (`initRun.initFirebase`, `internal/cli/init_firebase.go`) writes tfvars and a backend and calls `applyRoot`, with no discovery and no imports.

**Why it matters.** The same failure follows any loss of the Firebase root's state while its resources survive: re-homing an installation (a new GCP project or state bucket, as here), a deleted or recreated state bucket, an apply from a second machine whose workdir is fine but whose bucket is not the one the resources were recorded in, and a partial first apply whose state write failed. The workaround (state surgery across two buckets) needs Terraform knowledge the user should not need and is easy to get wrong. Delete-and-recreate is not available either (§6c).

## 2. Goals and non-goals

**Goals.**
- `fugaro init --firebase <fp>` adopts the Firebase root's singletons that already exist and are ours, by importing them, and plans creates only for what is missing.
- The plan and the confirmation name every import; nothing else about the Firebase stage changes.
- A rerun after adoption shows `No changes` (or no imports), and a partial failure is resumed by rerunning `init`.

**Non-goals.**
- Adopting a foreign Firebase project's resources, or anything under our names that does not pass the checks of §3. Such a resource is refused with a message, never imported, never deleted.
- Importing IAM members (§3.5), the project services or `google_firebase_project.this` (their creates succeed when they exist, observed live).
- Moving state between buckets, or a `--forget` for the Firebase root.
- Identity Platform and the Firestore database: they are not Terraform and already adopt what exists (`identityPlatform`, `firestoreStep`).

## 3. Deciding a resource is ours

The installation root's marks are labels where the resource has them (`fugaro=managed` on the runs bucket and registries, checked by `hasMarks`) and otherwise a fixed text the module sets: the custom roles' titles (`launcherRoleTitle` etc.), the scheduler account's display name (`schedulerDisplayName`), the log bucket's description. A resource under our name whose mark differs is a `ForeignError`; a missing one (`discovery.absent`: 404, or the API disabled) is a create. The same pattern applies to the Firebase root, with two additions: every check is against the *exact* name and project the module would create, and where the mark is only a copyable string, the properties that matter for security are compared too (§5). The names are `infra.SignerAccountID`, `infra.MinterRoleID` and `infra.APIKeyID` (`internal/infra/firebase.go`).

### 3.1 The Realtime Database instance

Address `module.firebase.google_firebase_database_instance.this`; import ID `projects/<fp>/locations/us-central1/instances/<fp>-default-rtdb`.

It has no label, but its *data* carries the mark: `/fugaro/mark`, checked by `DB.Check` (`internal/infra/firebasedb.go`) on every database `infra.DatabaseURLs` lists, before the first apply. That check already refuses a database with data and no mark, or another project's or GCP project's mark. Discovery adds the shape: the project's `DEFAULT_DATABASE` instance (from the same list call, across `locations/-`) must be in `us-central1`, named `<fp>-default-rtdb`, and `ACTIVE`. Then it is imported.

- In another location or under another ID: refused (a default instance cannot move, and only one is allowed). The message says the Firebase project cannot host the backend as the module defines it.
- `DISABLED` or `DELETED`: refused with the console step to re-enable or the wait.
- Empty and unmarked (Firebase or the console created it): adopted (decision 1, §9). `DB.Check` passes it today, the import is shown in the plan like any other and needs the stage's confirmation (covered by the run's typed confirmation when the plan is covered, else its own typed name), and the database step writes our mark right after the apply. An unmarked instance that holds data is still refused by `DB.Check`.

The module comment on the instance ("an adopted project keeps it") is wrong today, since the create fails when the instance exists; this change makes it true.

### 3.2 The web API key

Address `module.firebase.google_apikeys_key.web`; import ID `projects/<fp>/locations/global/keys/fugaro-web`.

Mark: display name `Fugaro run sign-in`. Shape: the restrictions are exactly the module's (API targets `identitytoolkit.googleapis.com` and `securetoken.googleapis.com`, no methods, no browser, server, Android or iOS restriction), the same rule `tf.Cover` applies to a planned key (`apiTargets` in `internal/infra/tf/cover.go`), plus no other kind. A key with the right name and display name but wider restrictions is refused, not imported and narrowed: it may be in use by something else.

Discovery calls `keys.get` only. It never calls `keys.getKeyString`: the key string reaches the state when Terraform reads the imported key, as it does for a created one, and Fugaro reads it from the root's output as today.

A deleted key keeps its ID for 30 days; the read returns it with a delete time. Refused with the `gcloud services api-keys undelete` command.

### 3.3 The token signer account

Address `module.firebase.google_service_account.signer`; import ID `projects/<fp>/serviceAccounts/fugaro-token-signer@<fp>.iam.gserviceaccount.com`.

Mark: display name `Fugaro token signer` and description `Signs the custom tokens of the Fugaro project <name>'s runs. Holds no roles.`, which names this Fugaro project, so a signer of another project is refused. Shape, because this account's signature is what admits a run to the database:

- no user-managed key (`serviceAccounts.keys.list` with `keyTypes=USER_MANAGED` is empty): the module never creates one, and one someone holds signs tokens without IAM;
- not disabled;
- its own IAM policy grants nothing but the `fugaroTokenMinter` role of this project, and only to this run's launchers and operators (decision 2, §9). Any other member of that role (for example a person of the old installation) is refused, since each could mint run tokens. The message names every such member and the command that removes it, one line each:

  ```
  token signer fugaro-token-signer@<fp>.iam.gserviceaccount.com grants fugaroTokenMinter to members who are not this installation's launchers or operators:
    user:old@example.com
  Each could mint run tokens. Remove them, then rerun fugaro init --firebase <fp>:
    gcloud iam service-accounts remove-iam-policy-binding fugaro-token-signer@<fp>.iam.gserviceaccount.com --project <fp> --member=user:old@example.com --role=projects/<fp>/roles/fugaroTokenMinter
  Or add them as launchers or operators if they should keep it.
  ```

  A grant of any other role on the signer is refused the same way, with the same removal command for that role.

### 3.4 The token minter role

Address `module.firebase.google_project_iam_custom_role.token_minter`; import ID `projects/<fp>/roles/fugaroTokenMinter`.

Mark: title `Fugaro token minter` (as `installationSingletons` matches the installation's roles by title). Shape: the permissions equal `tf.RolePermissions["fugaroTokenMinter"]` (`iam.serviceAccounts.signJwt`) exactly. More permissions are refused: the role is granted to people on the signer, and a wider role would make each grant reach further. A deleted role is not imported; as for the installation's roles, a note says the plan's create restores it.

### 3.5 IAM members

`google_service_account_iam_member.minter` and every `google_project_iam_member` are not imported. They are non-authoritative, and creating a member that exists is accepted by the provider (it merges an identical role and member into the policy), which is why the installation root never imports them either (`BindingCount` in `internal/infra/imports.go`). The user's hand merge of the minter binding was not needed. The plan shows them as creates that change nothing live, and `tf.Cover` already checks each one's role and member.

## 4. Mechanism

1. **Discovery.** A new `infra.DiscoverFirebase(ctx, c, spec)` returns `infra.Imports`, built like `DiscoverInstallation`: one `discovery` (project = the FP), read-only calls, `absent` means create, a mismatch is a refusal, and every refusal is reported at once (`discovery.result`). It runs in `initFirebase` right after `infra.Firebase` builds the root's spec, before `fwd.WriteVars`, so `--plan-only` shows it too. `Clients` gains an API Keys client (`google.golang.org/api/apikeys/v2`) and an endpoint for the fake; the RTDB, IAM and Resource Manager clients exist. Reads use the FP as the quota project, as the root's provider does (`user_project_override`).
2. **Import blocks.** Four new rows in `importTable`, and `infra.WriteImports(fwd.Root, im)` writes `imports.tf.json` next to the root's tfvars, as `prepare` does for the installation. Import blocks, not `terraform import`: they are part of the saved plan, so what is shown and confirmed is what is applied, and they need no extra apply. An empty discovery writes an empty file, which replaces an earlier run's imports.
3. **Already managed.** Terraform skips an import block whose address is already in the state (the installation root relies on this: it writes the runs bucket's import on every run). Discovery still drops addresses that `TF.ShowState` lists, so the summary and the notes are exact and a state entry with another ID is never shadowed. A rerun after adoption therefore writes an empty `imports.tf.json` and plans `No changes`.
4. **Plan, summary, confirmation.** `applyRoot` is unchanged: `tf.Summary` already prints `Plan: N to import, ...` and an `import <address> (<id>)` line per import, and `CountPlan` counts them. The confirmation text (`what` in `initFirebase`) gains the imported resources by name, for example `applies 4 imports, 12 creates, 0 updates to the Firebase project fugaro-belong (adopting the existing Realtime Database fugaro-belong-default-rtdb, web API key fugaro-web, token signer and fugaroTokenMinter role; ...)`. Notes from discovery print before the plan, as `installRoot` prints `im.Notes`.
5. **What the classifier allows.** `tf.Guard` is unchanged: no delete and no replace without `--allow-delete`; an import never needs one. `tf.Cover` today accepts any import whose ID is in one of the run's projects (`Cover.importOK`). This is tightened for every Terraform root, not only the Firebase root (decision 3, §9): an import is covered only when its address and ID are exactly one that root's discovery wrote. The `Cover` the review builds once per run (`initEngine` in `internal/cli/init_review.go`) holds no per-root data, so each confirmation passes its own discovery's list along with the plan: `installRoot` (`DiscoverInstallation`'s), the repository stage (`DiscoverRepo`'s) and `applyRoot` (`DiscoverFirebase`'s), through `notCovered` and `notCoveredFirebase`. `importOK`'s project check stays as a second condition. An import not on the list (a stale `imports.tf.json`, a hand edit in the workdir) makes the plan not covered, so the stage asks its own typed name. An `import and update` is covered under the existing attribute and custom-role checks (`Cover.customRole`, `Cover.attributes`), which already run on imports; in practice discovery's shape checks leave only cosmetic updates. When the Firebase project is not verified as Fugaro's (`firebaseVerified`), the plan is not covered, as today, and the stage asks its own typed name, after the summary that lists the imports.
6. **Partial failure and resume.** An import block either lands in the state with the apply or not at all; a failed apply leaves a state with some resources, and the rerun's discovery finds the rest (step 3 skips what is managed). A create that fails because the resource appeared between discovery and apply (or because an API was disabled at discovery, which reads as absent) fails as today, and the rerun imports it. No step deletes or rewrites anything outside the state.

## 5. Security

- **Squatting.** Names, titles and descriptions are public (they are in this repository), so they only stop accidental adoption; someone with IAM rights in the FP can copy them. The checks that protect the backend are the ones on shape: the signer holds no user-managed key and no grant but ours (§3.3), the minter role's permissions are exactly the pinned ones (§3.4), the key's restrictions are exactly the module's (§3.2), and the database holds no data that is not marked as this project's (§3.1, `DB.Check`). A resource that fails any of them is refused with what was found and what was expected, never imported, never changed.
- **Verified, not trusted.** An adopted key's restrictions and an adopted role's permissions are compared before the import, and the plan's attribute checks apply after it. Nothing in the old state is read or trusted: the state bucket the resources were first recorded in may be gone or someone else's.
- **No secrets read.** Discovery never calls `getKeyString` and never reads key material of the signer (it lists key metadata only). The API key string enters the state as it does for a created key; `CheckFirebaseOutputsFor` still checks the outputs.
- **Foreign minters.** A signer that lets anyone but this run's launchers and operators mint tokens is refused, with the members named (§3.3); adopting it would hand them run tokens without any review screen showing them.
- **Empty databases.** An empty, unmarked default instance is adopted (§3.1): it holds nothing to take over, and the import is listed and confirmed. One with data is not.
- **Imports are allowlisted** in every root's covered plan (§4.5), so a covered confirmation can never adopt something discovery did not check.
- **IAM members** are not adopted (§3.5); only the four singleton resource types of §3 are imported, at the four exact addresses.
- **Who may adopt.** The stage needs the same credentials as today; adoption adds read calls only.

## 6. Alternatives considered

- **(a) A documented `terraform import` recipe.** Cheap, but it puts state commands, the root's workdir and four import IDs in front of the user, after a failed apply, and leaves the module comment's promise unkept. Kept only as the fallback in `gcp-setup.md` if this is not built.
- **(b) Copy the state between buckets** (what the user did). It needs the old bucket, couples two installations' states, and copies whatever the old state held, including resources the new spec no longer plans. A wrong merge corrupts the state silently. Rejected.
- **(c) Delete and recreate.** The default RTDB instance holds the budget counters and the run registry (`prevent_destroy`), a deleted custom role's ID stays reserved for up to 37 days, a deleted API key's ID for 30, and a recreated signer is a new identity every launcher's binding must follow. Rejected.

## 7. Tests

With the repository's fakes: `gcpfake` (`IAM.AddRole`, `IAM.SetRolePermissions`, `IAM.AddServiceAccount`, `FirebaseDB.AddInstance`; new: an API Keys fake, the IAM fake's key list and an instance's type, location and state), `faketerraform` for the stage, and the golden plans under `internal/infra/tf/testdata/golden/`.

| Case | Expected |
|---|---|
| Clean FP | No imports, empty `imports.tf.json`, plan as today (`TestDiscoverInstallation`'s pattern) |
| FP with the four resources, unmanaged | Four imports with the exact addresses and IDs; the confirmation text names them |
| Empty, unmarked default instance | Imported; the database step then writes the mark |
| Each squat: role with an extra permission; signer with another description, a user-managed key, or another role granted on it; key with a third API target or a browser restriction; default instance in another region | Refused (exit 1) with found and expected; nothing written, no terraform plan run |
| Signer with `fugaroTokenMinter` granted to a member who is not a launcher or operator (and with one who is) | Refused (exit 1); the message names exactly the foreign members, each with its `remove-iam-policy-binding` command; a signer whose minters are all launchers or operators is imported |
| State already manages some or all | Those addresses are not imported; all managed gives an empty imports file |
| Rerun after an adopting apply | No imports, `No changes` |
| `tf.Cover`, every root | An import at an address or with an ID that root's discovery did not write is not covered; the existing goldens (`installation.plan.json`, `repo.plan.json`, `firebase.plan.json`) gain import variants (from real terraform with import blocks: the runs bucket and a role for the installation, a secret and a job account for a repository, the four singletons as `firebase-adopt.plan.json`), each covered with its discovery's list and not covered with the list empty or one ID changed |
| Call sites | The installation, repository and Firebase confirmations each pass their own discovery's list (a stage test per root with a stale `imports.tf.json` asks its own typed name) |
| API disabled at discovery | Treated as absent, as `absent` does |

**Live check (sandbox only).** In the sandbox's Firebase project: `terraform state rm` the four addresses from the Firebase root's state (simulating the lost state), run `fugaro init --firebase <fp> --plan-only` (expect `4 to import`, no create of them), then apply, then rerun (expect `No changes`). Then a squat: give the minter role an extra permission by hand, remove it from the state, rerun (expect the refusal), restore.

## 8. Work breakdown

1. `infra`: `DiscoverFirebase`, the four `importTable` rows, the API Keys client and endpoint, the signer's policy check with the foreign-minter refusal and its message, unit tests with new `gcpfake` handlers. *1.25 days.*
2. `cli`: call it in `initFirebase`, write `imports.tf.json` in the Firebase workdir, drop managed addresses via `ShowState`, print notes, name the imports in the confirmation; `faketerraform` stage tests. *0.5 day.*
3. `tf.Cover`, all roots: the import allowlist (address and ID) in `cover.go`, passed from each root's discovery at the installation, repository and Firebase confirmation call sites; import variants of the installation, repository and Firebase golden plans, generated with real terraform; stage tests per root. *1 day.*
4. Docs: `gcp-setup.md` (what `init --firebase` adopts and refuses, the foreign-minter fix), the module comment on the instance, the covered-plan rule in `m11-setup-and-skills.md`, a live-checklist entry. *0.25 day.*
5. Live check on the sandbox (§7), including a foreign minter on the signer. *0.25 day.*

About 3.25 days, one PR (task 3 can ship first on its own if wanted).

## 9. Decisions (2026-10-06, by the user)

1. **An empty default Realtime Database instance with no Fugaro mark is adopted:** shown as an import in the plan and covered by the typed confirmation. It holds nothing to take over, and the database step marks it right away (§3.1).
2. **A signer whose `fugaroTokenMinter` members include anyone who is not this run's launchers or operators is refused,** naming those members and how to remove them: each could mint run tokens, and no review screen would show them (§3.3).
3. **The `tf.Cover` rule "accept only imports whose address and ID discovery wrote" applies to all Terraform roots** (installation, repository and Firebase): every root already has a discovery, and a covered confirmation should never adopt what discovery did not check (§4.5).
