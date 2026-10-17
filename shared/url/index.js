// The URL validator (mobile plan, Task P0.3 -- SECURITY-SENSITIVE).
//
// **One validator, written once, for every link either artifact may open.**
// The console's rule lives in [desktop-console.md] section 5.1 rule 2 as prose
// and, as finding **F27** records, it exists nowhere in code: "no
// `validateExternalUrl` anywhere". It has to exist before either artifact opens
// a link (D124), and D119 puts it here so the console and the phone cannot
// disagree about which strings are safe to hand to `openExternal`.
//
// The rule, verbatim from the console design, is four checks:
//
//   * the scheme is **`https:`** and nothing else;
//   * the host is on an **exact allow-list** -- never a suffix, never a
//     wildcard;
//   * there is **no userinfo**; and
//   * there is **no port**.
//
// Nine more are added here, and they are the reason this module is not a
// two-line regex. Each is a way the four checks above can be walked around by a
// string that *looks* fine. `README.md` carries the same nine rows in the same
// order, and the two lists are kept in step deliberately:
//
//   * **The authority is read off the raw string.** `new URL()` drops an
//     explicit `:443` -- `parsed.port` is `''` -- so a check on the parsed port
//     would accept `github.com:443`.
//   * **The string must spell `https://`.** `https:github.com/a` and
//     `https:/github.com/a` both parse as https, with host `github.com`.
//   * **No backslash**: the parser rewrites it as a slash, so
//     `https://github.com\.evil.example/a` has host `github.com`.
//   * **No `%` in the authority**: `%67ithub.com` parses to host `github.com`.
//   * **No IP literal**: `2130706433`, `0x7f.1` and `0177.0.0.1` are all
//     `127.0.0.1` to the parser, and `[::1]` is a literal in brackets.
//   * **No punycode label, no non-ASCII host**: a Cyrillic `аpple.com` parses
//     to `xn--pple-43d.com`, which no ASCII allow-list can name.
//   * **A length bound** (`MAX_URL_LENGTH`), so a hostile string cannot reach a
//     shell, a log line or a native bridge unbounded.
//   * **Whitespace and control characters are refused, never trimmed.**
//     `new URL('  https://x  ')` succeeds; trimming would validate a different
//     string from the one a caller opens.
//   * **Nothing is coerced.** A non-string, an empty string and a malformed
//     allow-list are each a refusal with its own code.
//
// Two things are notes rather than checks. `github.com.` is `github.com` to
// DNS, so the trailing dot is a miss rather than a normalised hit. And the
// refusal messages are static, so none of them echoes the string it refuses.
//
// **The allow-list is not this module's to choose.** It is the owner's answer
// to **D124** (reconciliation U3: "the rule is design prose and names no list;
// the providers are named, the hosts are not"), and it has not been given. So
// the default is **empty**, and an empty list refuses everything -- see
// `DEFAULT_HOST_ALLOW_LIST`. The list is a **parameter** rather than a constant
// because it is per account: an account is one installation (D114), and its
// links are its own repository's and its own provider's.
//
// **This module builds no link.** No shape the CLI emits carries a provider
// (gap 7, F27), so a construction is P3.3's and the console's job. This module
// answers one question -- "may this string be opened?" -- and never answers
// "which string should be opened".

/**
 * The longest string this validator will look at, in UTF-16 code units.
 *
 * 2048 is the bound because it is the smallest round number above every URL
 * this repository can produce: a pull-request URL is under 120 characters, the
 * longest Cloud console link under 300. It is a bound rather than a preference:
 * a caller that hands a megabyte of text to a native bridge or a log line has a
 * problem this module can refuse to be part of.
 */
export const MAX_URL_LENGTH = 2048;

/**
 * The shipped allow-list: **empty**, because D124's list is the owner's and has
 * not been given.
 *
 * This is the module's most important export and the least convenient one. A
 * guessed host -- `github.com`, because that is where this repository's own
 * pull requests are -- would be wrong for every Bitbucket installation and
 * dangerous rather than merely useless: it would manufacture a link that passes
 * the check while naming a repository the operator does not own, which is the
 * defect F27's gap 7 describes in full. An empty list refuses everything, which
 * is the correct behaviour for a gate whose list nobody has written yet.
 *
 * It is frozen, and `validateExternalUrl` never mutates a list it is given.
 */
export const DEFAULT_HOST_ALLOW_LIST = Object.freeze([]);

/**
 * Every way this validator refuses, as a value a consumer can switch on.
 *
 * A refusal is never a boolean and never an exception: it is an object with one
 * of these codes, so a caller that renders "this link is not opened" can say
 * *why* without parsing a message. The codes are stable strings rather than
 * numbers because they appear in test names, in logs and in a UI.
 */
export const URL_REFUSAL = Object.freeze({
  /** The candidate is not a string. Never coerced: no `String(x)`, no `x.href`. */
  NOT_A_STRING: 'not-a-string',
  /** The candidate is the empty string. */
  EMPTY: 'empty',
  /** The candidate is longer than `MAX_URL_LENGTH`. */
  TOO_LONG: 'too-long',
  /** The candidate contains a control character or a space. Never trimmed. */
  CONTROL_CHARACTER: 'control-character',
  /** The candidate contains a character outside printable ASCII. */
  NON_ASCII: 'non-ascii',
  /** The candidate contains a backslash, which the parser rewrites as a slash. */
  BACKSLASH: 'backslash',
  /**
   * The candidate is not a URL this module will treat as one: `new URL` refused
   * it, or it parsed with an **empty authority** -- `https:///evil.example/a`
   * parses, with host `evil.example` and nothing between `https://` and the
   * path, so there is no host to check and nothing to open.
   */
  NOT_A_URL: 'not-a-url',
  /** The URL parses, and its scheme is not `https:`. */
  SCHEME_NOT_HTTPS: 'scheme-not-https',
  /** The URL is `https:` but the string does not spell `https://`. */
  NOT_CANONICAL: 'not-canonical',
  /** The authority carries userinfo. */
  USERINFO: 'userinfo',
  /** The authority carries a port, the default `:443` included. */
  PORT: 'port',
  /** The host is an IP literal, in any form the parser accepts. */
  IP_LITERAL: 'ip-literal',
  /** A host label is punycode (`xn--`), which no ASCII allow-list can name. */
  PUNYCODE_HOST: 'punycode-host',
  /** The authority percent-encodes its host, so the string is not the URL it looks like. */
  ENCODED_HOST: 'encoded-host',
  /** The host is not on the allow-list. */
  HOST_NOT_ALLOWED: 'host-not-allowed',
  /** The allow-list is empty, so nothing can be on it. */
  ALLOW_LIST_EMPTY: 'allow-list-empty',
  /** The allow-list is not a list of hosts. */
  INVALID_ALLOW_LIST: 'invalid-allow-list',
});

// The messages are static, one per code, and **none of them interpolates the
// candidate**. A refusal is shown to a person and often logged, and the string
// being refused is by definition hostile -- it arrived from a database node a
// coding agent wrote. Echoing it into a message is how a refusal becomes the
// injection it was refusing.
const MESSAGES = Object.freeze({
  [URL_REFUSAL.NOT_A_STRING]: 'the link is not a string, so it is not opened',
  [URL_REFUSAL.EMPTY]: 'the link is empty, so it is not opened',
  [URL_REFUSAL.TOO_LONG]: `the link is longer than ${MAX_URL_LENGTH} characters, so it is not opened`,
  [URL_REFUSAL.CONTROL_CHARACTER]: 'the link contains a control character or a space, so it is not opened',
  [URL_REFUSAL.NON_ASCII]: 'the link contains a character outside printable ASCII, so it is not opened',
  [URL_REFUSAL.BACKSLASH]: 'the link contains a backslash, so it is not opened',
  [URL_REFUSAL.NOT_A_URL]: 'the link is not a URL, so it is not opened',
  [URL_REFUSAL.SCHEME_NOT_HTTPS]: 'the link is not https, so it is not opened',
  [URL_REFUSAL.NOT_CANONICAL]: 'the link does not begin with https://, so it is not opened',
  [URL_REFUSAL.USERINFO]: 'the link carries a user name, so it is not opened',
  [URL_REFUSAL.PORT]: 'the link names a port, so it is not opened',
  [URL_REFUSAL.IP_LITERAL]: 'the link is an IP address rather than a host name, so it is not opened',
  [URL_REFUSAL.PUNYCODE_HOST]: 'the link names an internationalised host, so it is not opened',
  [URL_REFUSAL.ENCODED_HOST]: 'the link encodes its host, so it is not opened',
  [URL_REFUSAL.HOST_NOT_ALLOWED]: 'the link is not on this account’s list of hosts, so it is not opened',
  [URL_REFUSAL.ALLOW_LIST_EMPTY]: 'no host is allowed yet, so the link is not opened',
  [URL_REFUSAL.INVALID_ALLOW_LIST]: 'the host list is not a list of hosts, so the link is not opened',
});

/** A refusal, with its static message. The candidate is never carried on it. */
function refuse(code) {
  return { ok: false, code, message: MESSAGES[code] };
}

// A control character (0x00-0x1F), a space, or DEL. The WHATWG parser trims the
// first and strips the rest, which is exactly why the raw string is checked.
const CONTROL_OR_SPACE = /[\u0000- \u007f]/;

// Anything outside printable ASCII. This catches an internationalised host
// before `new URL` turns it into punycode, and an internationalised path that
// the parser would silently percent-encode.
const NON_PRINTABLE_ASCII = /[^!-~]/;

// The dotted-quad form the parser normalises every IPv4 spelling into:
// `2130706433`, `0x7f.1` and `0177.0.0.1` all arrive here as `127.0.0.1`.
const IPV4_LITERAL = /^\d{1,3}(\.\d{1,3}){3}$/;

// An allow-list entry: a host name of letters, digits, dots and inner hyphens,
// at least one character long. It admits no `*`, no `:`, no `/`, no `@`, no
// `%`, no whitespace, no leading or trailing dot and no empty label -- so a
// wildcard entry, which this module does not implement, is refused rather than
// quietly matching nothing.
const HOST_ENTRY = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/;

/**
 * Normalise an allow-list to lower-cased hosts, or `null` if it is not one.
 *
 * `null` is the "this is not a list of hosts" answer, and the caller turns it
 * into `invalid-allow-list`. It is deliberately not an exception and not an
 * empty list: a list that silently drops an entry it cannot read is a list the
 * owner did not write, and an empty result means something different (nobody
 * has written the list yet). The list it is given is never mutated.
 */
function normalizeAllowList(hostAllowList) {
  if (!Array.isArray(hostAllowList)) return null;
  const hosts = [];
  for (const entry of hostAllowList) {
    if (typeof entry !== 'string') return null;
    const host = entry.toLowerCase();
    if (!HOST_ENTRY.test(host)) return null;
    hosts.push(host);
  }
  return hosts;
}

/**
 * The authority of a string that begins with `https://`: everything up to the
 * first `/`, `?` or `#`.
 *
 * It is read off the **raw string** rather than off `parsed.host`, because the
 * parser removes from `host`/`port`/`username` precisely what this function
 * exists to see. The caller has already refused a control character, so there
 * is no whitespace left to trim.
 */
function rawAuthority(candidate) {
  const rest = candidate.slice('https://'.length);
  const end = rest.search(/[/?#]/);
  return end === -1 ? rest : rest.slice(0, end);
}

/**
 * Decide whether `candidate` may be handed to a browser.
 *
 * @param {unknown} candidate The string to check -- typically a `pr_url` off a
 *   document a coding agent wrote, which is to say: hostile.
 * @param {readonly string[]} [hostAllowList] The hosts this **account** may
 *   open, exactly. Defaults to `DEFAULT_HOST_ALLOW_LIST`, which is empty.
 * @returns {{ok: true, url: string, host: string}
 *   | {ok: false, code: string, message: string}}
 *   On success, `url` is the **candidate verbatim** -- never a re-serialisation
 *   of the parsed URL, so the string that was checked is the string a caller
 *   opens, with no gap between the two. `host` is the lower-cased host that
 *   matched. On refusal, `code` is one of `URL_REFUSAL` and `message` is a
 *   static sentence that never echoes the candidate.
 */
export function validateExternalUrl(candidate, hostAllowList = DEFAULT_HOST_ALLOW_LIST) {
  // --- The string itself ---------------------------------------------------
  // Nothing is coerced, in either direction: a `URL` object, a number and an
  // array are all refused rather than stringified, because `String(x)` is how a
  // value nobody checked becomes a link.
  if (typeof candidate !== 'string') return refuse(URL_REFUSAL.NOT_A_STRING);
  if (candidate.length === 0) return refuse(URL_REFUSAL.EMPTY);
  if (candidate.length > MAX_URL_LENGTH) return refuse(URL_REFUSAL.TOO_LONG);
  if (CONTROL_OR_SPACE.test(candidate)) return refuse(URL_REFUSAL.CONTROL_CHARACTER);
  if (NON_PRINTABLE_ASCII.test(candidate)) return refuse(URL_REFUSAL.NON_ASCII);
  // A backslash is a slash to the WHATWG parser for a special scheme, so
  // `https://github.com\.evil.example/a` has host `github.com` -- a string that
  // is not the URL it appears to be. No https URL needs one.
  if (candidate.includes('\\')) return refuse(URL_REFUSAL.BACKSLASH);

  // --- The parse -----------------------------------------------------------
  let parsed;
  try {
    parsed = new URL(candidate);
  } catch {
    return refuse(URL_REFUSAL.NOT_A_URL);
  }
  if (parsed.protocol !== 'https:') return refuse(URL_REFUSAL.SCHEME_NOT_HTTPS);
  // `new URL('https:github.com/a')` and `new URL('https:/github.com/a')` both
  // succeed with host `github.com`, so the scheme check above cannot catch
  // them: only the raw string can.
  if (candidate.slice(0, 'https://'.length).toLowerCase() !== 'https://') {
    return refuse(URL_REFUSAL.NOT_CANONICAL);
  }

  // --- The authority, read off the raw string ------------------------------
  const authority = rawAuthority(candidate);
  // `https:///evil.example/a` parses, with host `evil.example` and an empty
  // authority. There is nothing to check, so there is nothing to open.
  if (authority.length === 0) return refuse(URL_REFUSAL.NOT_A_URL);
  if (authority.includes('@')) return refuse(URL_REFUSAL.USERINFO);
  if (authority.startsWith('[')) return refuse(URL_REFUSAL.IP_LITERAL);
  // The parser drops an explicit default port -- `github.com:443` has
  // `port === ''` -- so this is a check on the string, not on `parsed.port`.
  if (authority.includes(':')) return refuse(URL_REFUSAL.PORT);
  if (authority.includes('%')) return refuse(URL_REFUSAL.ENCODED_HOST);

  const host = authority.toLowerCase();

  // --- The host ------------------------------------------------------------
  // An IP literal is refused here, before anything reads the host as a name,
  // because the spellings the parser normalises are exactly the ones that would
  // otherwise be answered as something else: `2130706433`, `0x7f.1` and
  // `0177.0.0.1` are all `127.0.0.1` to `new URL`, so their raw authority is
  // not a dotted quad at all, and "an IP address in a disguise" is the more
  // useful thing to say about them than "this host is not on the list".
  //
  // The check reads the **parsed** host, which is the address a browser would
  // dial, and it is the only IP clause here for a reason: a raw dotted quad is
  // normalised to itself, and a bracketed IPv6 literal is refused above at the
  // authority, so neither a raw-host clause nor a `parsedHost.startsWith('[')`
  // clause is ever the sole guard for any input. An earlier version carried
  // both; a sweep over 276 IP spellings found no candidate where either was the
  // only thing standing in the way, so they were removed rather than kept as
  // assertions nobody can exercise -- the same reason the raw-host invariant
  // that used to sit below was removed.
  if (IPV4_LITERAL.test(parsed.hostname)) return refuse(URL_REFUSAL.IP_LITERAL);

  // `host` -- the raw string's own authority, lower-cased -- is what the
  // allow-list is matched against, and it is the host the parser resolved for
  // every input that reached this line: an earlier version asserted that
  // equality here and returned NOT_A_URL when it failed, and mutation testing
  // showed no input could reach it, because each of the checks above reads the
  // raw string and each of the parser's host transformations (`%`-decoding,
  // IDNA, IP normalisation, lower-casing, a trailing dot) is either refused
  // there or leaves both sides equal. The branch was removed rather than kept
  // as an assertion nobody can exercise; what it expressed is that this line is
  // the raw host, not the parsed one.

  for (const label of host.split('.')) {
    if (label.startsWith('xn--')) return refuse(URL_REFUSAL.PUNYCODE_HOST);
  }

  // --- The allow-list ------------------------------------------------------
  const allowed = normalizeAllowList(hostAllowList);
  if (allowed === null) return refuse(URL_REFUSAL.INVALID_ALLOW_LIST);
  if (allowed.length === 0) return refuse(URL_REFUSAL.ALLOW_LIST_EMPTY);
  // An exact match. Not `endsWith`, not a suffix on a dot boundary, not a
  // wildcard -- which is why `github.com.evil.example` and `sub.github.com`
  // are both misses, and why the trailing dot in `github.com.` is a miss even
  // though DNS would resolve it to the same host.
  if (!allowed.includes(host)) return refuse(URL_REFUSAL.HOST_NOT_ALLOWED);

  return { ok: true, url: candidate, host };
}
