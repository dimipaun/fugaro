# Budgets on and off

Two budgets exist here, and neither one is the other.

- **The monthly billing budget** on the GCP project is an alert: it emails the billing account's administrators and users at 50%, 90% and 100% of the amount, and it stops nothing.
- **The model budget** is the guard on what a run may spend on the model. A gateway inside the run holds the model credential, prices every call against the built-in table (or the `model_prices` you set), and refuses or halts a run that would pass its cap. It lives in a Firebase Realtime Database in the installation's own GCP project.

The project config is `$XDG_CONFIG_HOME/fugaro/projects/<name>.yaml`, else under `~/.config`.

## The monthly billing budget

**Per installation.** `fugaro setup --budget 100 --budget-currency USD --billing-account <account-id>` creates it. Creating it needs `billing.budgets.create` on the billing account — **Billing Account Administrator** (`roles/billing.admin`) or **Billing Account Costs Manager** (`roles/billing.costsManager`). **Billing Account User** (`roles/billing.user`) is what *linking* the account to the project needs ([gcp-setup.md](../gcp-setup.md)), and it is not enough to create the budget.

The amount, the currency and the account are recorded nowhere, so **pass the same three flags on every later `fugaro setup` and `fugaro update`**: a plan without them deletes the budget, and the guard refuses that plan and names the flags.

**Off again:** run `fugaro update` without the three flags — the plan then deletes the budget, and the guard refuses it — and name the budget's address, which the refusal prints, in `--allow-delete <address>`.

## The model budget

**Per installation, then per repository.** It is off until you turn it on.

1. `fugaro setup --firebase-project <gcp-project-id> --budget-mode observe`, or on an installation that already exists `fugaro update --firebase-project <gcp-project-id> --budget-mode observe`. `--firebase-project`'s value is the installation's own GCP project id, and any other id is refused. The run builds the backend — the database, its rules and its mark, the history account and its job — and records it in the project config with `budget.mode: observe`. A development build warns that the history image does not exist and prints the two commands that build and push it: run them and the command again.
2. **Per repository:** `fugaro update --repo` in each checkout. The jobs then carry the backend, and a run opens a budget session.
3. **Let it run for a week.** In `observe` a run counts its spend and refuses nothing; `fugaro budget show` prints today's counted, spent and notional dollars, the caps and the runs in flight. That week is what calibrates the cap.
4. **Set the caps** from what you saw: `fugaro budget set --global --daily 150 --per-run 20`, `fugaro budget set --defaults --daily 60`, and `fugaro budget set --repo owner/name --daily 150` for one repository.
5. **Enforce.** Set `budget.per_run_usd` (more than 0) in the project config — it is the per-run cap the jobs enforce, and `--budget-mode enforce` is refused without it — then run `fugaro update --firebase-project <gcp-project-id> --budget-mode enforce`, then `fugaro update --repo` in each repository. A run halts when its next call's worst case would pass the cap: it pushes what it has, opens a draft PR and ends `halted`. **A halted run exits 0**, so Cloud Run shows the execution as succeeded and the "execution failed" alert never fires for it: `fugaro runs ls` shows the status.

Day to day: `fugaro budget show`, `fugaro budget preview --recipe <name> --runs N`, `fugaro watch`, `fugaro budget kill --all` (the emergency stop) and `fugaro budget resume --all`.

**Off again:** `fugaro update --firebase-project <gcp-project-id> --budget-mode off`, then `fugaro update --repo` in each repository. The jobs stop opening a budget session; the backend's address and key stay in every job, so a run can still read a published project policy, and the data stays.

Detail, and what the guard does and does not protect against: [project-layer.md](../project-layer.md) §5, [design/m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md).
