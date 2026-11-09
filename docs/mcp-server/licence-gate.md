# The hosted MCP server: the M5 licence gate

**Status: design answered by the owner 2026-11-04; M1, M2, M3, M4, M6 and M10 are built and merged
on `main`; the front door M4 creates is OFF on every installation until a release writes the flag
that turns it on, `fugaro update --mcp-server` (D934); M5 is built except the paired device M-a asks
for, which is M5b and is a hard prerequisite before any binary links the front door (the owner's
ruling of 2026-11-07); M7 is built except the reading of the installation's published state its
lists come from, which no task of this plan carries; M8 is built except the wiring that would serve
its four read tools, which is the task M5b gates; M9 is not started; this folder approves no task.**

**This is the gate file [reconciliation.md](reconciliation.md) §6 check 1 asks for, and §6 check 1 is
met.** What it records is the owner's clearance of D921's licence. The owner, **Dimi Paun**, cleared
the `zitadel/oidc` dependency set on **2026-11-07** by reading
[licence-table.md](licence-table.md) — every module this branch adds or bumps against `main`, with
its licence as the module's own file in the module cache reads it and with the column for anything
not clearly permissive empty on every row. **His answer, word for word: "Cleared: all
permissive."** **The table is the evidence this clearance stands on**, and reading it is the whole
method: the owner read the table and answered.

**Whose clearance this is.** It is the owner's own. **Counsel was not asked about the transitive set
and has cleared nothing**, so no line of this file is counsel's clearance, and the check's own
alternative — the owner or counsel — is met by the owner.

**What it does not clear.** It closes D921's licence check and nothing else in this folder: the
modules the table lists are the set it covers, and **M5 still merges dormant**, because the paired
device M-a asks for is M5b and **M5b is a hard prerequisite before any binary links the front door**.
