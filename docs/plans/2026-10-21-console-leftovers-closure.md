# The console leftovers the ledger held: all five are closed (verified 2026-10-21)

The coordinator's ledger of 2026-10-20 carried five console entries as deferred work — the
leftovers recorded when each of PRs **#694**, **#701**, **#709**, **#713** and **#717**
merged under the owner's merge-then-follow-up rule. Each was written down at its merge, and
each was handed to a follow-up run launched at that moment. **This document is the check of
that ledger against the tree, entry by entry, and its answer is that all five are already
closed: nothing in the ledger is outstanding, so this run changes no code.**

The check was made at `main`'s `1e0953b7`, before any change, and every row below names the
file and the symbol that carries the fix plus the guard that holds it. **Where the ledger
said a follow-up was launched, the follow-up is an ancestor of that commit** — #701, #708,
#713, #720 and #723, in the order the five entries were recorded — which is why the answer
is "already fixed" rather than "still open".

| Ledger entry | What the ledger recorded as left | Where it is closed | What holds it |
|---|---|---|---|
| **#694** — console styling Task 6.2 (the stylesheet step) | The independent review's 5 minors and 3 nits: CSS-escape scan gaps, a symlinked theme file followed, the cascade order unpinned, and wording | `console/tools/bundle-renderer/index.ts`: `BACKSLASH_RE`, `CSS_URL_RE`/`REMOTE_SCHEME_RE`/`quotedRemoteIn`, the `lstatSync(…).isFile()` test in `themeStylesheetSources`, and `refuseSymlinkedOutput`/`refuseSymlinkedDirectory`; the `SERVED_FILES` docstring in `console/src/main/protocol.ts`, which is the nit saying it is deliberately **not** a second filter | `console/test/unit/bundle.test.ts`: "holds every theme stylesheet the list names, and no `.css` the list does not" (183), "names the cascade in the design's order" (875), "refuses a listed theme file that is a symlink" (916), "refuses a backslash, which CSS reads as an escape" (1045) and "refuses a quoted string that names a URL…" (1089) |
| **#701** — the 6.2 follow-up's own review | Two findings left open: the symlinked-output refusal ran after the first write, and the remote-URL scan could be bypassed by the bytes the URL parser strips | `buildConsole` in `console/tools/bundle-renderer/index.ts` asks every refusal it can make **before the first write**; `urlParserTarget`/`isRemoteTarget` read a target as the parser would | `bundle.test.ts`: "refuses a symlinked output before it writes anything" (1391) and "refuses a module the walk will not bundle before it writes anything" (1432); the `url()` and quoted-string cases plus the probe |
| **#709** — console styling Task 6.3 (the layout and the rail) | Two stale docstrings: `THEME_STYLESHEETS`' still said "two files", and `bootDashboard`'s did not name the rail Task 6.3 appends | `THEME_STYLESHEETS`' docstring now says the list is the theme directory's **whole contents** and states no count; `bootDashboard`'s docstring describes the tree and the append order as it is | The docstrings are prose, so the guard is the code they describe: `bundle.test.ts`'s "holds every theme stylesheet the list names, and no `.css` the list does not" (183) holds the list and the directory equal in both directions |
| **#713** — the 6.3 docstring follow-up | Two more stale counts: the bundler's stylesheet-step header said "four source files", and the design's §5.2 misattributed who introduces the list | The header says the theme directory's whole contents, named by `THEME_STYLESHEETS` and by nothing else; `docs/design/console-styling.md` §5.2 names the files Task 6.1 wrote rather than counting them | `TestDocsConsoleStylingPlanTasksAreApproved` (`internal/cli/docs_console_styling_test.go`) reads the names off the design's §3.1 table and compares them, in order and in both directions, with the plan's File Structure tree lines — a count cannot be checked against the document it counts, a list can |
| **#717** — console styling Task 6.5 (the ground and the keyboard) | `console/README.md`'s six stale statements, `theme-tokens.test.ts`'s stale header, `shortcutRow` accepting the digits 1–9 where the design grants 1–3, and `event.key` where the physical key is meant | `console/README.md`'s Task 6.5 section is written and its six statements are past tense; `console/test/unit/theme-tokens.test.ts`'s header says both of §6.3's deferred cases are here; `shortcutRow` bounds its answer by `SHORTCUT_ROWS` (`console/src/renderer/entry.ts`, 609) and reads `event.code` | `console/test/unit/entry-wiring.test.ts`: "reads the physical key, so the operator's layout does not spell the shortcut away" (1222), "addresses the three rows the design grants and no fourth" (1244), and "selects the row a shortcut key names by position, and reads the platform's modifier" (1101) — the three the follow-up's mutation table records |

## What this run did not do, and why

**No code, no stylesheet, no test and no document is changed by this check.** Each of the
five entries is a claim about the tree, and each claim was read against the tree; every one
of them is already true, so there is nothing left to fix and nothing to widen.

**The console's own suites were not run here, and that is said rather than implied.** The
container this check ran in has no Node, so `npm run test:unit` and `npm run test:e2e` could
not be executed: the guards named above are read in the tree and pinned by the cases that
name them, not observed passing in this run. What was run is the repository's own Go suite,
which is what the console's documents are held by from this side — including
`TestDocsConsoleStylingPlanTasksAreApproved`, the case that reads the design's §3.1 table
against the plan's tree.

**The one byte #701's follow-up left open is a decision, not an open finding.** A raw LF
*inside* a quoted string is not refused by the quoted-string scan, because that scan's body
is bounded by the newline. The follow-up tried the widening, observed it admit a
`<bad-string-token>` while losing two spellings that do resolve, and reverted it;
`console/README.md` records the measurement under "The one byte left open, and the
measurement that settles it", and `bundle.test.ts`'s probe asserts both halves.
