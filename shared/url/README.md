# `shared/url` — the URL validator

Mobile plan, Task **P0.3** (**SECURITY-SENSITIVE**), decision **D124**. One
validator, written once, for every link either artifact may open.

**Status: implemented, with an empty allow-list.** `index.js` and
`index.test.js`; `npm test` from `shared/` runs 42 tests and they pass. The
module is complete and the list it checks against is **not** — see below.

## The rule

The console's rule is [desktop-console.md](../../docs/design/desktop-console.md)
§5.1 rule 2, in prose, and finding **F27** records that it exists nowhere in
code: "no `validateExternalUrl` anywhere". D124 answers that it must be written
once, in this package, before either artifact opens a link. It is four checks:

| Check | Why |
|---|---|
| the scheme is `https:` | anything else is a handler, a local file or a script |
| the host is on an **exact** allow-list | a suffix match turns `github.com.evil.example` into `github.com` |
| no userinfo | `https://github.com@evil.example/` has host `evil.example` |
| no port | the console's own wording, and a port is a different service |

Nine more are here because each is a way those four can be walked around by a
string that *looks* fine. Every one was checked against Node's own WHATWG parser
before it was written, and every one has a test. `index.js`'s header carries the
same nine in the same order, and the two lists are kept in step deliberately:

| Check | The string that shows why |
|---|---|
| the authority is read off the **raw string** | `new URL('https://github.com:443/a').port` is `''` |
| the string must spell `https://` | `https:github.com/a` and `https:/github.com/a` both parse as https |
| no backslash | the parser turns `\` into `/`: `https://github.com\.evil.example/a` has host `github.com` |
| no `%` in the authority | `%67ithub.com` parses to host `github.com` |
| no IP literal | `2130706433`, `0x7f.1` and `0177.0.0.1` are all `127.0.0.1` to the parser |
| no punycode label, no non-ASCII | `https://аpple.com/` (Cyrillic а) parses to `xn--pple-43d.com` |
| a length bound (`MAX_URL_LENGTH`, 2048) | a bound, not a preference: a hostile string does not reach a native bridge unbounded |
| whitespace and control characters are refused, never trimmed | `new URL('  https://x  ')` succeeds, so trimming validates a different string from the one a caller opens |
| nothing is coerced | `String(x)` is how a value nobody checked becomes a link |

Two things are notes rather than checks. `github.com.` is `github.com` to DNS,
so a validator that stripped the trailing dot would accept a string the
allow-list never named; the match is on the string, so it is a miss. And the
refusal messages are static — the string being refused arrived from a node a
coding agent wrote, so none of them echoes it.

Only the **authority** is checked for `%`, and that is the point rather than an
oversight: a percent-encoded host is a string that is not the URL it looks like,
while a percent-encoded path or query is just a URL. Both halves have tests.

## The allow-list is not this module's to choose

`DEFAULT_HOST_ALLOW_LIST` is **empty**, and an empty list refuses everything —
including a URL that is perfect in every other respect. That is deliberate:

- The list is the owner's answer to **D124**, and it has not been given.
  Reconciliation U3: "the rule is design prose and names no list; the providers
  are named, the hosts are not."
- A guessed host — `github.com`, because that is where this repository's own
  pull requests are — is not merely useless but **wrong**, and wrong in a
  specific way F27's gap 7 describes: Fugaro opens pull requests on GitHub **and
  Bitbucket Cloud**, `edgeappinc/fugarosandbox` is on Bitbucket, and a
  GitHub-shaped guess would pass the allow-list while naming a repository the
  operator does not own.
- An empty list is a gate whose list nobody has written yet. A guessed list is a
  gate that looks shut and is not.

The list is a **parameter**, not a constant, because it is per account: an
account is one installation (D114), and its links are its own repository's and
its own provider's.

```js
// from the console's voice module, or from the app: one call, one list per account
const result = validateExternalUrl(document.pr_url, account.hostAllowList);
if (result.ok) {
  openExternal(result.url); // the input verbatim, never a re-serialisation
} else {
  render(result.message);   // static, and it does not quote the input
}
```

## What this module does **not** do

**It builds no link.** No shape the CLI emits carries a provider (F27, gap 7),
so a construction — `github` → `https://github.com/<repo>/pull/<n>`, `bitbucket`
→ `https://bitbucket.org/<workspace>/<name>/pull-requests/<n>` — is P3.3's and
the console's job. This module answers "may this string be opened", never "which
string should be opened". Both shapes appear in the tests as **inputs**.

**It has no consumer yet.** The console's voice module does not exist (Voice
D50, D53) and the app does not either, so "written once" is a claim about the
future until one of them imports it.

## Refusal codes

`URL_REFUSAL` names them all, and a refusal carries one plus a static message.
The codes are stable strings because they appear in test names, in logs and in a
UI. A refusal never carries `url` or `host`, so nothing downstream can read a
value out of one.

`not-a-string` · `empty` · `too-long` · `control-character` · `non-ascii` ·
`backslash` · `not-a-url` · `scheme-not-https` · `not-canonical` · `userinfo` ·
`port` · `ip-literal` · `punycode-host` · `encoded-host` · `host-not-allowed` ·
`allow-list-empty` · `invalid-allow-list`

`allow-list-empty` and `invalid-allow-list` are refusals of the **list**, not of
the URL, and both mean "nothing is opened". A malformed list is refused rather
than repaired, because a list that silently drops an entry it cannot read is a
list the owner did not write.

## Mutation evidence

Every guard was mutation-tested: broken one at a time, with the test suite run
after each break. **21 of 21 mutants were killed**, each by the test named for
it, and **no clause in the module is without one**. Three findings came out of
the runs rather than confirming what was there:

- An earlier version asserted that the host read off the raw string equals the
  host the parser resolved, returning `not-a-url` when it did not. Deleting that
  branch left all tests green — it was unreachable, because every one of the
  parser's host transformations is refused before it or affects both sides
  equally. A fuzz over 96,389 authorities that survive the earlier checks found
  no divergence, so the branch was removed rather than kept as an assertion
  nobody can exercise.
- The same method caught **two more clauses in the IPv4 check**: a raw-host
  `IPV4_LITERAL.test(host)` and a `parsedHost.startsWith('[')` beside the parsed
  check. Neither was ever the sole guard for any input — a raw dotted quad is
  normalised to itself, and a bracketed IPv6 literal is refused earlier, at the
  authority — which a sweep over 276 IP spellings confirmed. Both were removed.
  A clause no test can kill is a clause a reviewer cannot tell is load-bearing.
- The IPv4-literal check runs **before** the host checks, because the spellings
  the parser normalises are exactly the ones that would otherwise be reported as
  a parse failure instead of as an address in a disguise.

The bound's **value** is mutation-tested too. Every length case derives its
string from `MAX_URL_LENGTH`, so changing the constant to 4096 would have left
the whole file green; `MAX_URL_LENGTH` is now pinned to 2048 by its own test,
and the 2048 → 4096 mutant is killed by it.

Where the reasoning lives: [implementation-plan.md](../../docs/mobile/implementation-plan.md)
(P0.3), [reconciliation.md](../../docs/mobile/reconciliation.md) (F27, D124, U3),
[desktop-console.md](../../docs/design/desktop-console.md) (§5.1 rule 2, §4.3
gaps 6 and 7).
