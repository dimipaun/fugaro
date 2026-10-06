# Adopt an Existing Firebase Root Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `fugaro init --firebase <fp>` adopts the Firebase root's four singletons that already exist and are ours (database instance, web API key, token signer, token minter role) by import blocks, refuses look-alikes, and `tf.Cover` accepts an import in every root only when that root's discovery wrote it.

**Architecture:** A new read-only `infra.DiscoverFirebase` (the pattern of `DiscoverInstallation`) returns `infra.Imports`; `initFirebase` writes them as `imports.tf.json` into the Firebase workdir before the plan, drops addresses already in the state, names the imports in the confirmation, and passes the discovery's list to `tf.Cover`, whose new allowlist rule applies to the installation, repository and Firebase roots alike.

**Tech Stack:** Go; Terraform import blocks (`imports.tf.json`); `google.golang.org/api` clients (`apikeys/v2` is new; `iam/v1`, `firebasedatabase/v1beta` exist); the repo's fakes (`gcpfake`, `faketerraform`) and golden plans.

**Spec:** `docs/design/adopt-firebase-root.md` (decisions in §9 settled by the user on 2026-10-06; this plan implements it). Code facts below come from a read of `main` at `4232adc`; each implementer confirms a signature by reading before editing, since line numbers drift.

## Global Constraints

- Discovery makes **read calls only** and never calls `keys.getKeyString` or reads signer key material (list key metadata only).
- Every refusal from discovery is collected and reported at once (`discovery.result`), and happens **before** any file is written to the Firebase workdir or any terraform command runs.
- Exact import addresses and IDs (the four, `fp` = the Firebase project ID):
  - `module.firebase.google_firebase_database_instance.this` ← `projects/<fp>/locations/us-central1/instances/<fp>-default-rtdb`
  - `module.firebase.google_apikeys_key.web` ← `projects/<fp>/locations/global/keys/fugaro-web`
  - `module.firebase.google_service_account.signer` ← `projects/<fp>/serviceAccounts/fugaro-token-signer@<fp>.iam.gserviceaccount.com`
  - `module.firebase.google_project_iam_custom_role.token_minter` ← `projects/<fp>/roles/fugaroTokenMinter`
- IAM members (`google_service_account_iam_member.minter`, every `google_project_iam_member`), project services and `google_firebase_project.this` are **never** imported.
- Marks: role title `Fugaro token minter` with permissions exactly `["iam.serviceAccounts.signJwt"]` (`tf.RolePermissions["fugaroTokenMinter"]`); signer display name `Fugaro token signer`, description `Signs the custom tokens of the Fugaro project <name>'s runs. Holds no roles.`, not disabled, no USER_MANAGED key, and its IAM policy grants only `projects/<fp>/roles/fugaroTokenMinter` to this run's launchers and operators; API key display name `Fugaro run sign-in` with restrictions exactly the API targets `identitytoolkit.googleapis.com` and `securetoken.googleapis.com` (no other restriction kind); default RTDB instance `DEFAULT_DATABASE`, location `us-central1`, name `<fp>-default-rtdb`, state `ACTIVE`; an empty unmarked instance IS adopted (decision 1); a deleted role or key is refused with the undelete/restore hint (role: note that the plan's create restores it, as for the installation's roles).
- A foreign minter member (decision 2) is **refused** with exactly the message of design §3.3, one `remove-iam-policy-binding` line per member (and per role for a grant of any other role).
- `tf.Cover` rule (decision 3, all roots): an import is covered only when its address AND ID equal an entry of the list that root's discovery wrote; the existing project-prefix check stays as a second condition. An import not on the list makes the plan not covered, so the stage asks its own typed name.
- No live cloud actions in any task; the live check in the spec is the user's, not an agent's.
- Commits end with the two attribution lines the session was given; tests first; one commit per task minimum.

## Review Focus

1. Any discovery refusal must leave the Firebase workdir untouched and run no terraform command (the stale-`imports.tf.json` danger: an earlier run's file for another project must be overwritten, including under `--plan-only`).
2. A disabled API (Firebase Database, API Keys, IAM) at discovery reads as "absent", not as an error; a 403 is an error that names access, never "absent".
3. Foreign-minter detection must compare members exactly as the run's launchers/operators are written (`user:`, `group:`, `serviceAccount:` prefixes, case) and must refuse a grant of any other role on the signer, including `roles/owner` style grants and `allUsers`/`allAuthenticatedUsers`.
4. `tf.Cover`: an import whose ID is in the project but whose address is not on the list is not covered; an empty list with an import in the plan is not covered; an `import and update` is still checked by the attribute and custom-role rules; the installation and repository roots keep passing their golden plans with their own lists.
5. Addresses already in the state must not be imported and must not hide a state entry that has a different ID (refuse or note, never shadow).
6. The module comment ("an adopted project keeps it") becomes true; no module `.tf` behavior change is allowed (adoption is Go-side).

## File Structure

| File | Responsibility |
|---|---|
| `internal/infra/discover.go` / new `internal/infra/discover_firebase.go` | `DiscoverFirebase`, the four checks, the refusal message |
| `internal/infra/imports.go` | four new `importKind`s and `importTable` rows |
| `internal/infra/discover.go` (`Clients`, `Endpoints`, `NewClients`), `internal/localcfg/localcfg.go` (`Endpoints`), `internal/cli/init.go` (`newInitClients`) | API Keys client and endpoint plumbing |
| `internal/gcpfake/apikeys.go` (new), `firebasedb.go`, `iam.go` | fakes: API Keys; instance type/location/state/get; account description and key list |
| `internal/infra/tf/tf.go` (State), `internal/infra/tf/cover.go` | `State.Addresses`, `ImportKey`, the allowlist rule |
| `internal/cli/init_firebase.go`, `init.go` (`installRoot`, `planRepo`), `init_review.go` (`notCovered`, `notCoveredFirebase`) | wiring, confirmation text, passing each root's list |
| `internal/infra/tf/testdata/golden/*`, `scripts/gen-golden-plans.sh` | import variants of the goldens |
| `deploy/terraform/gcp/modules/firebase/firebase.tf` (comment only), `docs/gcp-setup.md`, `docs/design/m11-setup-and-skills.md`, `docs/gcp-live-checklist.md` | docs |

Run tasks **sequentially** in `/Users/dimi/git.lattica/Fugaro/.worktrees/adopt-fb` (branch `adopt-firebase-root`).

---

### Task 1: API Keys client, endpoint plumbing and fakes

**Files:** Modify `internal/infra/discover.go` (`Clients`, `Endpoints`, `NewClients`), `internal/localcfg/localcfg.go` (`Endpoints`, its validation list), `internal/cli/init.go` (`newInitClients`), `internal/gcpfake/firebasedb.go`, `internal/gcpfake/iam.go`; create `internal/gcpfake/apikeys.go`; tests in `internal/infra/discover_test.go`, `internal/gcpfake/*_test.go`.

**Interfaces:**
- Produces: `Clients.APIKeys *apikeys.Service` (`google.golang.org/api/apikeys/v2`), built `if !o.Endpoints.NoAuth || e.APIKeys != ""` (the pattern of `FirebaseDB`); `infra.Endpoints.APIKeys string`; `localcfg.Endpoints.APIKeys string yaml:"api_keys,omitempty"` (declared for fakes only, like its siblings).
- Fakes: `gcpfake.APIKeys` with `AddKey(project, id, displayName string, targets []string, otherRestriction bool, deleted bool)` serving `GET /v2/projects/{p}/locations/global/keys/{id}` (and 404 when absent); `FirebaseDB.AddInstanceFull(project, location, id, instType, state, url string)` plus `GET .../instances/{id}` and the list reading these attributes (keep `AddInstance(project, url)` working with its current defaults); `IAM.AddServiceAccountFull(project, email, displayName, description string, disabled bool)`, `IAM.AddUserKey(project, email)` and a `GET /v1/projects/{p}/serviceAccounts/{email}/keys` handler honoring `keyTypes=USER_MANAGED`.

- [ ] **Step 1: Write failing tests** (a fake-level test per new handler, and `TestNewClientsNoAuthNeedsEveryEndpoint`-style: with `NoAuth` and no `APIKeys` endpoint `Clients.APIKeys` is nil; with the endpoint it is non-nil). Example for the fake:

```go
func TestAPIKeysFakeServesAKeyAndNotFound(t *testing.T) {
	f := NewAPIKeys(t)
	f.AddKey("fp-1234", "fugaro-web", "Fugaro run sign-in", []string{"identitytoolkit.googleapis.com", "securetoken.googleapis.com"}, false, false)
	svc, _ := apikeys.NewService(context.Background(), option.WithEndpoint(f.URL), option.WithoutAuthentication())
	k, err := svc.Projects.Locations.Keys.Get("projects/fp-1234/locations/global/keys/fugaro-web").Do()
	if err != nil || k.DisplayName != "Fugaro run sign-in" || len(k.Restrictions.ApiTargets) != 2 {
		t.Fatalf("%v %+v", err, k)
	}
	if _, err := svc.Projects.Locations.Keys.Get("projects/fp-1234/locations/global/keys/other").Do(); !isNotFound(err) {
		t.Fatalf("want 404, got %v", err)
	}
}
```

- [ ] **Step 2: Run to confirm failure** (`go test ./internal/gcpfake ./internal/infra -run 'APIKeys|NewClients' -count=1`; undefined symbols).
- [ ] **Step 3: Implement** the client, the endpoint in `infra.Endpoints`, `localcfg.Endpoints` (add to the validation list that requires fakes' endpoints only when the existing pattern does for `FirebaseDatabase`; read `localcfg.go` ~339 and ~718 and mirror), `newInitClients` mapping, and the three fakes (follow `firebasedb.go` and `iam.go` handler style; keep every existing fake call and test unchanged). Add the module to `go.mod`/vendor only if the repo vendors (the `apikeys/v2` package ships inside the already-required `google.golang.org/api`; run `go mod tidy` and check `git diff go.mod go.sum` stays empty or minimal).
- [ ] **Step 4: Run** `go test ./internal/gcpfake ./internal/infra ./internal/localcfg -count=1` and `go vet ./internal/...`; expect PASS.
- [ ] **Step 5: Commit** `git commit -m "infra: API Keys client and endpoint; fakes for keys, instance attributes and signer keys"`

---

### Task 2: `DiscoverFirebase` and the four import kinds

**Files:** Create `internal/infra/discover_firebase.go`, `internal/infra/discover_firebase_test.go`; modify `internal/infra/imports.go` (kinds, table rows).

**Interfaces:**
- Consumes: `discovery`/`absent`/`refuse`/`foreign`/`add`/`result` (discover.go), `FirebaseSpec{Project, FugaroProject, Names, Launchers, Operators, ...}`, consts `SignerAccountID`, `MinterRoleID`, `APIKeyID`, `tf.RolePermissions`.
- Produces: `func DiscoverFirebase(ctx context.Context, c *Clients, spec FirebaseSpec) (Imports, error)`; `importFirebaseDB`, `importAPIKey`, `importSignerSA`, `importMinterRole` with these `importTable` rows (placeholders `{project}`, `{region}`, `{name}`):

```go
importFirebaseDB:  {"module.firebase.google_firebase_database_instance.this", "projects/{project}/locations/{region}/instances/{name}"},
importAPIKey:      {"module.firebase.google_apikeys_key.web", "projects/{project}/locations/global/keys/{name}"},
importSignerSA:    {"module.firebase.google_service_account.signer", "projects/{project}/serviceAccounts/{name}"},
importMinterRole:  {"module.firebase.google_project_iam_custom_role.token_minter", "projects/{project}/roles/{name}"},
```

(`newImport(k, project, region, key, name)`: instance: region `us-central1`, name `<fp>-default-rtdb`; key: name `fugaro-web`; signer: name = the account email; role: name `fugaroTokenMinter`.)

- [ ] **Step 1: Write the failing table test** (the spec's §7 cases; use the Task 1 fakes through the `newCloud`/`cloud` helper of `discover_test.go`, extended with `fbdb`, `apikeys` and an IAM that already exists there):

```go
func TestDiscoverFirebase(t *testing.T) {
	t.Run("clean project imports nothing", func(t *testing.T) { /* no instance, key, account, role: im.List empty, no refusal */ })
	t.Run("the four exist and are ours", func(t *testing.T) { /* want exactly the four address -> ID pairs above, no more */ })
	t.Run("empty unmarked default instance is adopted", func(t *testing.T) { /* DB.Check passes; instance imported */ })
	t.Run("api disabled reads as absent", func(t *testing.T) { /* each service returning the disabled error => no import, no refusal */ })
}
func TestDiscoverFirebaseRefuses(t *testing.T) {
	cases := []struct{ name string; arrange func(*cloud); want []string }{
		{"role with an extra permission", /* AddRole fugaroTokenMinter + SetRolePermissions signJwt,getAccessToken */ nil, []string{"fugaroTokenMinter", "iam.serviceAccounts.getAccessToken"}},
		{"role with another title", nil, []string{"title"}},
		{"deleted role", nil, []string{"deleted"}},
		{"signer with another description", nil, []string{"description"}},
		{"signer with a user-managed key", nil, []string{"user-managed key"}},
		{"signer disabled", nil, []string{"disabled"}},
		{"signer grants another role", nil, []string{"roles/owner", "remove-iam-policy-binding"}},
		{"signer grants the minter role to a stranger", nil, []string{"user:old@example.com", "grants fugaroTokenMinter to members who are not this installation's launchers or operators"}},
		{"key with a third api target", nil, []string{"restrictions"}},
		{"key with a browser restriction", nil, []string{"restrictions"}},
		{"key deleted", nil, []string{"undelete"}},
		{"key with another display name", nil, []string{"display name"}},
		{"default instance in another region", nil, []string{"us-central1"}},
		{"default instance under another id", nil, []string{"default-rtdb"}},
		{"default instance not ACTIVE", nil, []string{"ACTIVE"}},
	}
	// each case: DiscoverFirebase returns a *UserError whose text contains every want substring;
	// and one case with TWO bad resources returns both refusals in one error.
}
func TestDiscoverFirebaseMinterMembersAreCompared(t *testing.T) {
	// launchers {"user:a@x.com"}, operators {"group:ops@x.com"}: a signer granting minter to exactly those passes;
	// granting also "serviceAccount:old@p.iam.gserviceaccount.com" or "allUsers" is refused naming that member only.
}
```

Write each `arrange` against the fakes for real (the `nil`s above are the spec of the cases, not stubs: implement them).
- [ ] **Step 2: Run** `go test ./internal/infra -run DiscoverFirebase -count=1` (FAIL: undefined).
- [ ] **Step 3: Implement** `DiscoverFirebase`: one `discovery{project: spec.Project}`; instance via the same `Instances.List("projects/"+fp+"/locations/-")` call `DatabaseURLs` uses (pick the `DEFAULT_DATABASE`), key via `APIKeys.Projects.Locations.Keys.Get`, signer via `IAM.Projects.ServiceAccounts.Get`, then `.Keys.List(name).KeyTypes("USER_MANAGED")` and `.GetIamPolicy`, role via `IAM.Projects.Roles.Get`; mismatches go through `d.refuse(&ForeignError{...})` or a formatted error; the foreign-minter refusal text is exactly design §3.3 (build it with a small function `foreignMinterMessage(sa, fp string, members []string) string`, unit-tested for the exact bytes); members are compared as exact strings against `spec.Launchers ∪ spec.Operators`; a deleted role adds the note "custom role ... is deleted; the plan's create restores it" and is not imported (as the installation's roles); return `d.result()`.
- [ ] **Step 4: Run** `go test ./internal/infra -count=1`; expect PASS.
- [ ] **Step 5: Commit** `git commit -m "infra: DiscoverFirebase adopts the four Firebase singletons and refuses look-alikes"`

---

### Task 3: Wire discovery into `initFirebase`

**Files:** Modify `internal/cli/init_firebase.go` (`initFirebase`, `applyRoot` call, the `what` text), `internal/infra/tf/tf.go` (`State.Addresses`); test `internal/cli/init_firebase_adopt_test.go`; module comment in `deploy/terraform/gcp/modules/firebase/firebase.tf` (comment only).

**Interfaces:**
- Consumes: `DiscoverFirebase` (Task 2), `infra.WriteImports(dir string, im Imports) error`, `(*tf.TF).ShowState`, `applyRoot(ctx, t, wd, root, what string) (applied, stop bool, err error)`.
- Produces: `func (s *State) Addresses() []string` (walks `values.root_module` and `child_modules` of `terraform show -json` state output; see `tf.go` ~217 for the State type); `applyRoot` gains the imports (a parameter `imports []tf.ImportKey` or a field on `initRun`) for Task 4; `infra.Imports.Keys() []tf.ImportKey` helper (`infra` imports `tf`).

- [ ] **Step 1: Write failing stage tests** with `fbRig` (init_firebase_test.go:30): (a) a rig whose fakes hold the four resources and whose scripted `show@firebase` returns a plan with four `importing` entries: the run prints `import module.firebase.google_apikeys_key.web (id projects/...)` lines, the confirmation says `applies 4 imports` and names the four resources, and `imports.tf.json` in `r.fbRoot()` holds exactly the four rows; (b) a clean project writes `{}`; (c) a refusal (role with an extra permission) exits 1 with the found/expected text, `imports.tf.json` and tfvars are NOT written to the Firebase workdir and `r.applies(t)` is empty; (d) a stale `imports.tf.json` from an earlier run is replaced (empty) on a clean project, also with `--plan-only`; (e) addresses the scripted state already lists are not imported (script `show` of the state via `show@firebase` with `values.root_module.resources`): all managed gives `{}`; (f) the confirmation count text.
- [ ] **Step 2: Run** `go test ./internal/cli -run 'FirebaseAdopt' -count=1` (FAIL).
- [ ] **Step 3: Implement.** In `initFirebase`, after `infra.Firebase` builds `fspec` and before `fwd.WriteVars`: `im, err := infra.DiscoverFirebase(ctx, c, fspec)` (refusal returns the user error, nothing written); print `im.Notes` with `r.warn`; compute `managed := state addresses` only if the Firebase root's state exists (`ft.Init` must run first for `ShowState`; so order is: `ft.Init`, `ShowState`, drop managed addresses from `im.List`, then `infra.WriteImports(fwd.Root, im)`; do the write before the plan, after init; the refusal check precedes `ft.Init`); build the confirmation `what` from the imports (design §4.4 example text) and keep it unchanged when there are none; thread `im.Keys()` toward `applyRoot` for Task 4 (add the parameter now, unused until Task 4). Change the comment above the instance in `modules/firebase/firebase.tf` to say adoption is done by `fugaro init` discovery (comment only, `terraform fmt` clean, `deploy/terraform` tests unchanged).
- [ ] **Step 4: Run** `go test ./internal/cli ./internal/infra/... -count=1 -run 'Firebase|Init'` (full `./internal/cli` once at the end); expect PASS.
- [ ] **Step 5: Commit** `git commit -m "init --firebase: discover, import and name the existing Firebase singletons"`

---

### Task 4: The import allowlist in `tf.Cover`, all roots (security-critical; second independent reviewer)

**Files:** Modify `internal/infra/tf/cover.go` (`NotCovered`, new `ImportKey`), `internal/cli/init_review.go` (`notCovered`, `notCoveredFirebase`), `internal/cli/init.go` (`installRoot` ~1180, `planRepo` ~2247); tests `internal/infra/tf/cover_test.go`, stage tests in `internal/cli`.

**Interfaces:**
- Produces: `type tf.ImportKey struct{ Address, ID string }`; `func (c Cover) NotCovered(p *Plan, imports []ImportKey) []string`; `(*initRun).notCovered(p *tf.Plan, imports []tf.ImportKey)`; `notCoveredFirebase(p, imports)`; `infra.Imports.Keys()` (Task 3).

- [ ] **Step 1: Write the failing cover tests** (extend the table at cover_test.go:127):

```go
func TestImportsAreCoveredOnlyWhenDiscoveryWroteThem(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"} /* + the fields the neighbouring tests set */}
	imp := func(addr, id string) *Plan { /* a one-resource plan: action no-op, Importing{ID: id} at addr, an allowed type */ }
	key := ImportKey{"module.installation.google_project_iam_custom_role.launcher", "projects/proj-1234/roles/fugaroLauncher"}
	cases := []struct{ name string; plan *Plan; list []ImportKey; covered bool }{
		{"on the list", imp(key.Address, key.ID), []ImportKey{key}, true},
		{"empty list", imp(key.Address, key.ID), nil, false},
		{"same id other address", imp("module.installation.google_project_iam_custom_role.tag_mover", key.ID), []ImportKey{key}, false},
		{"same address other id in project", imp(key.Address, "projects/proj-1234/roles/other"), []ImportKey{key}, false},
		{"other project id", imp(key.Address, "projects/evil/roles/fugaroLauncher"), []ImportKey{{key.Address, "projects/evil/roles/fugaroLauncher"}}, false},
		{"no imports in the plan, list non-empty", /* plain create plan */ nil, []ImportKey{key}, true},
	}
	// assert (len(c.NotCovered(plan, list)) == 0) == covered, and for the false cases that the reason names the import.
}
```

and a test that an `import and update` (actions `["update"]` with `Importing`) is still run through `customRole`/`attributes` (an extra permission on the role makes it not covered even when on the list).
- [ ] **Step 2: Run** `go test ./internal/infra/tf -run Imports -count=1` (FAIL).
- [ ] **Step 3: Implement** in `cover.go` (the switch at ~226): replace `case rc.Change.Importing != nil && !c.importOK(...)` with a check that the pair `(rc.Address, rc.Change.Importing.ID)` is in the list AND `c.importOK(id)`; reasons: "imports <id>, which discovery did not find" vs the existing "not in this run's projects". Change the signature and update every caller (`go build ./...` finds them): `installRoot` passes `im.Keys()`, `planRepo` passes its `im.Keys()`, `applyRoot` passes the Firebase `im.Keys()`; the review's `cover()` stays as is. Existing tests that built imports by hand now pass the matching list.
- [ ] **Step 4: Stage tests, one per root** (`installation`, `repository`, `firebase`): with a scripted plan holding an import and a stale `imports.tf.json` that discovery did not write (write the file by hand into the workdir after discovery ran: simulate via a fake `show` that returns an import discovery never listed), the confirmation falls back to its own typed name (`⚠ CONFIRM ... not covered`); with a plan whose imports are exactly discovery's, one run confirmation covers it.
- [ ] **Step 5: Run** `go test ./internal/infra/tf ./internal/cli -count=1`; expect PASS (the full cli suite takes about 5 minutes).
- [ ] **Step 6: Commit** `git commit -m "tf.Cover: accept an import only when that root's discovery wrote it"`

---

### Task 5: Golden import variants

**Files:** Modify `scripts/gen-golden-plans.sh`, `internal/infra/tf/cover_golden_test.go`; create `internal/infra/tf/testdata/golden/*-adopt.plan.json`.

**Why synthesized:** a real plan with import blocks makes the provider read the live resource, so `gen-golden-plans.sh` (offline by design: dead proxy, fake token) cannot produce one. The variants are derived by `jq` from the existing real goldens: chosen `create` changes become `no-op` with `importing: {"id": "<id>"}`, `before` set equal to `after`, the same JSON shape `real_terraform_test.go` (~132-157) obtains from real terraform with an import block of a `terraform_data` resource. A test asserts the synthesized shape against that real one so it cannot drift.

- [ ] **Step 1: Write failing tests**: for each of installation (the runs bucket and one custom role), repository (one secret, one job account) and Firebase (the four singletons, `firebase-adopt.plan.json`): the variant is covered with its discovery list, not covered with an empty list, and not covered with one ID changed; the golden assertion `slices.Equal(rc.Change.Actions, []string{"create"})` and the `>= 10` count in `TestGoldenPlansAreCovered` are updated to allow `no-op` rows that carry `Importing`.
- [ ] **Step 2: Implement** the `jq` step in the script (`jq --arg ... 'map(select(...))'` transformations committed as a function `adopt_variant`), regenerate the three `*-adopt.plan.json`, and a test that the importing shape equals the real-terraform fixture's field set.
- [ ] **Step 3: Run** `go test ./internal/infra/tf -count=1` and `bash scripts/gen-golden-plans.sh` (needs terraform and jq; skip the script run in CI if the existing script is skipped there, matching its current guard) with a clean `git diff` for the unchanged goldens.
- [ ] **Step 4: Commit** `git commit -m "tf: golden import variants for the installation, repository and Firebase roots"`

---

### Task 6: Docs and the live-check entry

**Files:** Modify `docs/gcp-setup.md`, `docs/design/m11-setup-and-skills.md`, `docs/design/adopt-firebase-root.md` (status line: built), `docs/gcp-live-checklist.md`.

- [ ] **Step 1:** `docs/gcp-setup.md`: a section "`init --firebase` on a project that already has the Firebase resources": what is adopted (the four), what is refused and why (the squat checks), the foreign-minter refusal with its message and fix, that IAM members are not adopted, the manual `terraform import` recipe kept as the fallback note.
- [ ] **Step 2:** `docs/design/m11-setup-and-skills.md`: the covered-plan rule now reads "imports covered only when that root's discovery wrote them".
- [ ] **Step 3:** `docs/gcp-live-checklist.md`: a new Check (next free number) marked USER-RUN, NOT RUN, from the spec's §7 live check: sandbox only, `terraform state rm` of the four addresses, `init --firebase <fp> --plan-only` expecting `4 to import`, apply, rerun expecting `No changes`; then the squat (extra permission on the minter role) and the foreign-minter signer cases; explicit that the state surgery is on the sandbox Firebase root only.
- [ ] **Step 4:** the design status line says built, and §3.5 records the discovered fact that the IAM member is not imported.
- [ ] **Step 5: Verify:** `go build ./... && go vet ./...` and the doc tests; commit `git commit -m "docs: adopting an existing Firebase root"`.

---

## Self-review

- **Spec coverage:** §3.1-§3.4 and decisions 1-2 (Task 2, wiring Task 3), §3.5 (Global Constraints, Task 2 tests assert only four imports), §4.1-4.4 (Tasks 1-3), §4.5 and decision 3 (Task 4), §4.6 (Task 3 test e), §5 security (Tasks 2-4), §7 tests (Tasks 2-5), live check (Task 6, user-run), module comment (Task 3).
- **Spec differences found in the code, ruled here:** (1) the printed import line is `import <addr> (id <id>)`; (2) the golden import variants are synthesized because real import plans need the network (Task 5); (3) `ShowState` has no address accessor, added in Task 3; (4) the design's "five resources" is four imports (the IAM member is deliberately not imported).
- **Placeholders:** the `arrange` cases and the `imp`/plan helpers in Tasks 2, 4 and 5 are specifications to be written as real code against the fakes; every named interface is defined by an earlier task.
- **Type consistency:** `ImportKey{Address, ID}`, `Imports.Keys()`, `DiscoverFirebase`, `State.Addresses`, `notCovered(p, imports)` are used with the same names in Tasks 2-5.
