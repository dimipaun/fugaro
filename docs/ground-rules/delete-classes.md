# The delete classes

Every planned removal is judged on **what it deletes**, never on the fact that it is a delete
(D3430, [reconciliation.md](reconciliation.md) — it wins over [spec.md](spec.md)). Two classes:

| Class | What it is | A remove may apply it |
| --- | --- | --- |
| **Recreatable** | Fugaro made the object and the same apply makes it again: a Cloud Run job or service, a Scheduler job, an image and the registry repository holding it, a build artifact, and a service account or an IAM binding Fugaro wrote | yes, with no approval |
| **Irreplaceable** | an apply cannot put it back: a Secret Manager secret, a bucket and its objects, the RTDB, Firestore, a KMS key, Terraform state, the policy and the settings, the spend ledger and the run history — and a type nobody has decided about | no; `update` refuses it |

The implementation is one table, `removalClasses` in `internal/infra/tf/cover.go`, one row per
resource type of `allowedTypes`. Each row carries both answers, because `RefusesRemovalType` (D1551's
type question) and `RemovalClass` (D3430's per-change question) ask the same thing and may not be
told different classifications.

The class column is D3430's; the gate still reads D1551's column, and `RefusesRemovalType` is
byte for byte the predicate it was. A removal of a `google_artifact_registry_repository` or a
`google_service_account` therefore refuses today exactly as it did before DG0, and `RemovalClass` is
the answer DG1a moves that gate onto.

## Per change, not only per type

`tf.RemovalClass(rc ResourceChange, people []string) Class` reads two things besides the type.

- **An IAM member grant** is judged by the member it names. A grant to a **service account** is a
  binding Fugaro wrote — recreatable. A grant to a **person** is membership and irreplaceable, and
  `update` admits it only when `people` records that person's removal: the roster DG5 writes when a
  membership command revokes what it can reach (D3454). A grant they could not reach is then removed
  by the owning member's own `update`. A principal that is neither — a group, a domain, a public
  member — is refused.
- **The workspace service account** (`google_service_account` with
  `account_id = fugaro-workspace-iam`, Terraform's `workspace_iam`) is irreplaceable (D3456): `Link`
  is the member owner's act and no admin list exists for `update` to re-grant from, so a recreated
  account is not the installation's again.

An **unclassified type** — one added to `allowedTypes` and no row — is irreplaceable, and so are a
change that carries no member where the rule needs one, a member whose address is empty
(`serviceAccount:` or `user:` with nothing after it) and a record that names nobody. Blank inputs
are refused rather than compared with each other. A task that finds a type Fugaro makes missing
from the table adds its row in its own change.

Consumers: DG1a (the managed gate), DG2 (`tf.Guard` and `--allow-delete`), DG3 (`prevent_destroy`),
UO2 (retiring per-repository roots), AG2a (an agent's `update`) and DG4 (the end-to-end test).
