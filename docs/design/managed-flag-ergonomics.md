# Managed flag ergonomics

**Spec owner: Dimi Paun. Date: 2026-11-13. Numbers reserved: D2210-D2219 (D2210-D2214 used).**
**Status: a design only; nothing is built and no task is approved.**

The owner asked, on 2026-11-13, why marking a project managed takes `--managed`, a project ID typed on the command line and a budget amount and currency with no defaults, when every project setup creates is managed and the checkout already names its project. This note records what the code does today, the owner's ruling where one was given, and the choices that are still the owner's.

## What the code does today

| Surface | Today | Source of the rule |
| --- | --- | --- |
| `fugaro setup` on a first run | Every project it creates is managed from its creation (the `fugaro_managed` label and the creation fact in the runs bucket). | `fugaro setup --help` |
| `fugaro setup --managed` | Does **not** mark the project. It only records the monthly budget amount the managed apply needs, and requires `--budget`, `--budget-currency` and `--billing-account` together. | `fugaro setup --help` |
| `fugaro managed flag PROJECT_ID` | Marks an existing project. The exact GCP project ID of the selected installation is an argument; no `--yes`, no prompt. The amount and currency are required flags. | `internal/cli/managed_flag.go`; D1541, D58 |
| The amount | The budget alert amount the installation's Terraform creates, and what the apply-time budget read opens the gate on: a marked project that records none is refused by every managed apply. | D64, `internal/cli/managed_apply.go` |

## Decisions

| # | Question | Recommendation and reason | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D2210** | The setup wizard asks for the monthly budget. What does it suggest? | **A prompt whose suggestion is 100 USD, which Enter takes**, shown with what the number is for (the alert threshold, not a cap). This is the setup wizard's own convention: each question carries a suggestion Enter takes. | No suggestion (D64 as ruled): an empty answer is refused. Costs one more thing the operator must invent at their first run. | **2026-11-13: as the owner stated it: the wizard prompts, default 100 USD.** This amends D64's "no suggestion taken for you"; D64's amount-is-required rule stands, so a project is never marked without an amount. |
| **D2211** | Is `--budget-currency` still a required flag on `fugaro setup` and `fugaro managed flag`? | **No: it defaults to USD.** It is a unit, not a spend decision, and the amount is what D64 protects. The recorded marker still carries both, so no reader changes. | Keep it required: every non-interactive call spells out `USD`. | _pending_ |
| **D2212** | Does a non-interactive `fugaro setup` or `fugaro managed flag` (no terminal, `--json`) default the amount to 100? | **No: the amount stays required there.** A default taken with nobody to see it is the "number the owner did not choose that reads as a promise" D64 refused. Only the prompt, where a person presses Enter, suggests one. | Default it everywhere: simpler, but the number is chosen by nobody. | _pending_ |
| **D2213** | Does `fugaro managed flag` read the project from the checkout's `fugaro.yaml` instead of taking `PROJECT_ID`? | **Read it from the selected installation, and keep a typed confirmation of the project ID at the owner's terminal.** D1541 chose consent by argument over a prompt because typing the exact ID is the declaration; reading the ID silently would remove the declaration and leave nothing between a stray command and a wrong project. A prompt on a person's own terminal that names the project and takes its ID keeps the declaration without the argument. | (a) Keep the argument as D1541 has it: no change. (b) Read it with no confirmation: the least typing, and flagging any project the checkout points at becomes one command. | _pending_ |
| **D2214** | `fugaro setup --managed` records the budget only and reads as if it marked the project. | **Drop the flag**: the budget is asked for (D2210) on every creating run, so the flag carries no information; keep `--budget`, `--budget-currency` and `--billing-account` for non-interactive use. Before 1.0 there is no compatibility to keep. | Keep and rename it (`--record-budget`): one flag for a thing the prompt now covers. Leave it and fix only the help text. | _pending_ |

D2213 and D2214 touch D1541 and the setup flag surface; each needs the owner's answer before a task is written. D2210 is answered.

## UNVERIFIED

- **U1.** Whether any script, skill or documentation spells `--managed` on the command line. A search of `plugin/`, `docs/` and `scripts/` for it, with the Docs tests (`go test ./internal/cli/ -run Docs`), decides how much a drop (D2214) touches.
- **U2.** Whether the console or mobile app reads the currency field of the marker as written; a default changes who writes it and never its shape.

## If the owner accepts the recommendations

One small task per row, none approved: the wizard prompt (D2210) and the USD default (D2211) in `internal/cli/init.go` and `internal/cli/managed_flag.go` with their help text and Docs tests; the project from the selected installation with a typed confirmation (D2213); the removal of `--managed` (D2214). The first two touch no wire format; none changes the marker's shape, so no format version moves. The `fugaro update` rule is unaffected: nothing here changes an installation, only how it is asked for.
