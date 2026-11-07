# The ask: managed-project screens in the console

**The owner, 2026-11-07**, in the release planning for **console 0.121.0 (Raspberry)**. He is the
spec's owner: the paragraph below is his, verbatim, and it is the only text in this folder he wrote.
An answer he gives to a decision is recorded in
[reconciliation.md](reconciliation.md) §7, in that decision's own row; this file stays his words.

The ask, verbatim:

Owner 2026-11-07 put "managed-project screens" in console 0.121.0 (Raspberry): READ-ONLY screens showing what the CLI does on a managed project: the project's managed state (the marks: managed / revoked, who admitted what), the review account keys (default_claude_account, claude_account_stages: names only, never a token), the policy notes (what the last update changed, runs in flight at the time), the retirements the last plain `fugaro update` made (derived resources only), and the credentials service state once DON7 lands."

Nothing else came with the ask. It is a scope, not a design: it names five things to show and one
rule to obey — **READ-ONLY** — and it settles none of the questions a screen raises: where each fact
comes from, which of them the CLI can already print, what a screen may do when a fact is not recorded
anywhere, and what an operator may press. Those are [reconciliation.md](reconciliation.md)'s, and they
are why that file carries twenty decisions and this one carries none.
