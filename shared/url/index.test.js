// The URL validator's own test (mobile plan, Task P0.3 -- SECURITY-SENSITIVE).
//
// It is written BEFORE the module it imports, as P0.1's skeleton test was: the
// red run is the evidence that the glob reaches this directory at all.
//
// The rule under test is the console's own, taken from desktop-console.md
// section 5.1 rule 2 and finding F27: a link is opened only if it is `https:`,
// on an **exact** host allow-list, with no userinfo and no port. The console's
// rule is design prose with no code and no list, so this module is where it
// becomes executable -- once, for both artifacts (D119, D124).
//
// Three properties this file exists to pin, in the order they matter:
//
//   * **The shipped state is empty.** The allow-list is the owner's answer to
//     D124 (reconciliation U3: "the rule is design prose and names no list"),
//     and it has not been given. So the default list is empty and an empty list
//     refuses everything -- including a URL that is perfect in every other
//     respect. A validator that shipped with a guessed host would be worse than
//     no validator, because it would look like a gate.
//   * **The parser's normalisations are not a way through.** `new URL()` is
//     used to parse, and it hides exactly the things this rule refuses: it
//     drops an explicit `:443`, decodes a percent-encoded host, folds
//     `2130706433` and `0x7f.1` into `127.0.0.1`, and turns a Cyrillic host
//     into punycode. Every one of those has a case below, and every one is
//     asserted against the **raw** string as well as the parsed one.
//   * **Every refusal is a refusal.** A non-string, an empty string, an
//     oversized string, a string with whitespace in it, an unknown shape: all
//     refused, each with its own code, none coerced. The fail-closed sweep at
//     the bottom of this file is that property as a table.
//
// What is NOT here, deliberately: no link is **built**. The provider is on no
// shape the CLI emits (gap 7, F27), so a construction is P3.3's and the
// console's job; this module only answers "may this string be opened". The two
// pull-request shapes the repository's providers produce are inputs below, not
// outputs.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  DEFAULT_HOST_ALLOW_LIST,
  MAX_URL_LENGTH,
  URL_REFUSAL,
  validateExternalUrl,
} from './index.js';

// Hosts a test is allowed to put on an allow-list. They are the two the
// repository's own providers use (docs/git-providers.md): GitHub and Bitbucket
// Cloud. They are test values for a parameter, NOT this module's shipped list.
const GITHUB = Object.freeze(['github.com']);
const BITBUCKET = Object.freeze(['bitbucket.org']);
const BOTH = Object.freeze(['github.com', 'bitbucket.org']);

const REFUSAL_CODES = Object.freeze(Object.values(URL_REFUSAL));

/** A printable name for a value that may not be printable at all. */
function show(candidate) {
  return typeof candidate === 'string'
    ? JSON.stringify(candidate)
    : `<${typeof candidate}>`;
}

/** Assert a refusal, and that the refusal is shaped the way consumers rely on. */
function refuses(candidate, hostAllowList, code) {
  const result = validateExternalUrl(candidate, hostAllowList);
  assert.equal(
    result.ok,
    false,
    `expected a refusal for ${show(candidate)}, got ${JSON.stringify(result)}`,
  );
  assert.equal(
    result.code,
    code,
    `expected ${show(candidate)} to be refused as ${code}, got ${result.code}`,
  );
  assert.ok(
    REFUSAL_CODES.includes(result.code),
    `${result.code} is not one of the exported URL_REFUSAL codes`,
  );
  assert.equal(typeof result.message, 'string');
  assert.ok(result.message.length > 0, 'a refusal carries a message');
  assert.equal('url' in result, false, 'a refusal carries no url');
  assert.equal('host' in result, false, 'a refusal carries no host');
  return result;
}

/** Assert an acceptance, and the exact shape an acceptance has. */
function accepts(candidate, hostAllowList, host) {
  const result = validateExternalUrl(candidate, hostAllowList);
  assert.equal(
    result.ok,
    true,
    `expected ${JSON.stringify(candidate)} to be accepted, got ${JSON.stringify(result)}`,
  );
  assert.equal(
    result.url,
    candidate,
    'the accepted url is the input verbatim, never a re-serialisation',
  );
  assert.equal(result.host, host);
  assert.equal('code' in result, false, 'an acceptance carries no code');
  return result;
}

// ---------------------------------------------------------------------------
// The shipped state: an empty allow-list, which refuses everything
// ---------------------------------------------------------------------------

test('the default host allow-list is empty, because D124\'s list is the owner\'s', () => {
  assert.ok(Array.isArray(DEFAULT_HOST_ALLOW_LIST));
  assert.equal(DEFAULT_HOST_ALLOW_LIST.length, 0);
  assert.ok(Object.isFrozen(DEFAULT_HOST_ALLOW_LIST));
});

test('with the default (empty) allow-list, a perfect URL is still refused', () => {
  refuses(
    'https://github.com/dimipaun/fugaro/pull/518',
    undefined,
    URL_REFUSAL.ALLOW_LIST_EMPTY,
  );
  refuses(
    'https://bitbucket.org/edgeappinc/fugarosandbox/pull-requests/1',
    DEFAULT_HOST_ALLOW_LIST,
    URL_REFUSAL.ALLOW_LIST_EMPTY,
  );
});

test('an explicitly empty allow-list refuses everything too', () => {
  refuses('https://github.com/a', [], URL_REFUSAL.ALLOW_LIST_EMPTY);
});

test('a malformed allow-list is refused rather than repaired', () => {
  for (const bad of [
    'github.com',
    {},
    null,
    [''],
    ['*'],
    ['*.github.com'],
    ['.github.com'],
    ['github.com.'],
    ['github.com:443'],
    ['github.com/pull'],
    ['user@github.com'],
    ['%67ithub.com'],
    ['github..com'],
    ['github .com'],
    ['аpple.com'],
    [42],
    ['github.com', ''],
  ]) {
    const result = validateExternalUrl('https://github.com/a', bad);
    assert.equal(
      result.ok,
      false,
      `expected the allow-list ${JSON.stringify(bad)} to be refused, got ${JSON.stringify(result)}`,
    );
    assert.equal(result.code, URL_REFUSAL.INVALID_ALLOW_LIST);
  }
});

// ---------------------------------------------------------------------------
// The scheme: https, and nothing else
// ---------------------------------------------------------------------------

test('the scheme must be https', () => {
  refuses('http://github.com/a', GITHUB, URL_REFUSAL.SCHEME_NOT_HTTPS);
  refuses('javascript:alert(1)', GITHUB, URL_REFUSAL.SCHEME_NOT_HTTPS);
  refuses('JaVaScRiPt:alert(1)', GITHUB, URL_REFUSAL.SCHEME_NOT_HTTPS);
  refuses('data:text/html,<b>x</b>', GITHUB, URL_REFUSAL.SCHEME_NOT_HTTPS);
  refuses('vbscript:msgbox(1)', GITHUB, URL_REFUSAL.SCHEME_NOT_HTTPS);
  refuses('file:///etc/passwd', GITHUB, URL_REFUSAL.SCHEME_NOT_HTTPS);
  refuses('mailto:operator@example.com', GITHUB, URL_REFUSAL.SCHEME_NOT_HTTPS);
});

test('a scheme the parser accepts but the string does not spell is refused', () => {
  // `new URL` reads all three of these as https and gives the same host, so
  // only a check against the raw string catches them.
  refuses('https:github.com/a', GITHUB, URL_REFUSAL.NOT_CANONICAL);
  refuses('https:/github.com/a', GITHUB, URL_REFUSAL.NOT_CANONICAL);
  refuses('https:///github.com/a', GITHUB, URL_REFUSAL.NOT_A_URL);
});

test('the scheme is matched case-insensitively, as every browser matches it', () => {
  accepts('HTTPS://github.com/a', GITHUB, 'github.com');
});

// ---------------------------------------------------------------------------
// The host: an exact match, never a suffix and never a prefix
// ---------------------------------------------------------------------------

test('a host on the allow-list is accepted', () => {
  accepts('https://github.com/a/b/pull/1', GITHUB, 'github.com');
  accepts('https://github.com', GITHUB, 'github.com');
  accepts('https://github.com/', GITHUB, 'github.com');
});

test('the two pull-request shapes this repository\'s providers produce are accepted', () => {
  accepts(
    'https://github.com/dimipaun/fugaro/pull/518',
    BOTH,
    'github.com',
  );
  accepts(
    'https://bitbucket.org/edgeappinc/fugarosandbox/pull-requests/1',
    BOTH,
    'bitbucket.org',
  );
});

test('a host that merely ends with an allow-listed host is refused', () => {
  refuses('https://github.com.evil.example/a', GITHUB, URL_REFUSAL.HOST_NOT_ALLOWED);
  refuses('https://evilgithub.com/a', GITHUB, URL_REFUSAL.HOST_NOT_ALLOWED);
  refuses('https://notgithub.com/a', GITHUB, URL_REFUSAL.HOST_NOT_ALLOWED);
});

test('a subdomain of an allow-listed host is refused: the list is exact', () => {
  refuses('https://sub.github.com/a', GITHUB, URL_REFUSAL.HOST_NOT_ALLOWED);
  refuses('https://gist.github.com/a', GITHUB, URL_REFUSAL.HOST_NOT_ALLOWED);
});

test('a trailing dot is refused, though DNS resolves it to the same host', () => {
  // The reason this is its own case: `github.com.` IS `github.com` for DNS, so
  // a validator that stripped the dot would accept a string the allow-list
  // never named. It is refused because the match is on the string.
  refuses('https://github.com./a', GITHUB, URL_REFUSAL.TRAILING_DOT);
});

test('a trailing dot is refused as its own code, whatever the list holds', () => {
  // Refused in the authority, before the list is read: with an empty list, with
  // a list that names the dotted form's undotted twin, and with the dot on a
  // host that is not listed at all. The dot never decides the answer by luck.
  refuses('https://github.com./a', [], URL_REFUSAL.TRAILING_DOT);
  refuses('https://github.com.', GITHUB, URL_REFUSAL.TRAILING_DOT);
  refuses('https://github.com.?q=1', GITHUB, URL_REFUSAL.TRAILING_DOT);
  refuses('https://github.com.#f', GITHUB, URL_REFUSAL.TRAILING_DOT);
  refuses('https://evil.example./a', GITHUB, URL_REFUSAL.TRAILING_DOT);
  // A dot inside the path or query is just a URL.
  accepts('https://github.com/a.', GITHUB, 'github.com');
  accepts('https://github.com/a?x=y.', GITHUB, 'github.com');
});

test('a host on another list is refused, and the lists do not overlap', () => {
  refuses('https://bitbucket.org/a', GITHUB, URL_REFUSAL.HOST_NOT_ALLOWED);
  refuses('https://github.com/a', BITBUCKET, URL_REFUSAL.HOST_NOT_ALLOWED);
});

test('a host is matched case-insensitively, as DNS matches it', () => {
  accepts('https://GitHub.COM/a', GITHUB, 'github.com');
});

test('an allow-list entry is matched case-insensitively', () => {
  accepts('https://github.com/a', ['GitHub.com'], 'github.com');
});

test('the allow-list it is given is never mutated', () => {
  // `normalizeAllowList` lower-cases its entries, and the obvious way to write
  // that is in place. The list belongs to the caller -- an account's list, held
  // for the life of the app -- so a validator that rewrote it would be editing
  // another component's state, and would throw outright on a frozen one.
  const list = ['GitHub.com', 'Bitbucket.ORG'];
  const before = [...list];
  const frozen = Object.freeze([...list]);

  accepts('https://github.com/a', list, 'github.com');
  refuses('https://evil.example/a', list, URL_REFUSAL.HOST_NOT_ALLOWED);
  accepts('https://github.com/a', frozen, 'github.com');

  assert.deepEqual(list, before, 'the list was rewritten in place');
  assert.deepEqual(frozen, before);
  assert.equal(Object.isFrozen(list), false, 'the list was frozen by the call');
});

// ---------------------------------------------------------------------------
// No userinfo
// ---------------------------------------------------------------------------

test('a URL with userinfo is refused', () => {
  refuses('https://user@github.com/a', GITHUB, URL_REFUSAL.USERINFO);
  refuses('https://user:secret@github.com/a', GITHUB, URL_REFUSAL.USERINFO);
  refuses('https://:@github.com/a', GITHUB, URL_REFUSAL.USERINFO);
});

test('userinfo is refused even when it hides a host that is not on the list', () => {
  // The parsed host here is `evil.example` and the allow-listed-looking part is
  // the *username*. Either check refuses it; the case exists so that a future
  // refactor that drops one of them is still caught.
  refuses('https://github.com@evil.example/a', GITHUB, URL_REFUSAL.USERINFO);
});

// ---------------------------------------------------------------------------
// No port
// ---------------------------------------------------------------------------

test('a URL with a port is refused', () => {
  refuses('https://github.com:8443/a', GITHUB, URL_REFUSAL.PORT);
  refuses('https://github.com:80/a', GITHUB, URL_REFUSAL.PORT);
  refuses('https://github.com:1/a', GITHUB, URL_REFUSAL.PORT);
});

test('the default port is refused too, though the parser drops it', () => {
  // `new URL('https://github.com:443/a').port` is `''`, so a check on the
  // parsed port alone would accept this. The rule says no port.
  refuses('https://github.com:443/a', GITHUB, URL_REFUSAL.PORT);
  refuses('https://github.com:/a', GITHUB, URL_REFUSAL.PORT);
});

// ---------------------------------------------------------------------------
// No IP literals
// ---------------------------------------------------------------------------

test('an IPv4 literal is refused', () => {
  refuses('https://127.0.0.1/a', GITHUB, URL_REFUSAL.IP_LITERAL);
  refuses('https://192.168.0.1/a', GITHUB, URL_REFUSAL.IP_LITERAL);
  refuses('https://0.0.0.0/a', GITHUB, URL_REFUSAL.IP_LITERAL);
});

test('an IPv4 literal in a form the parser normalises is still refused', () => {
  // Each of these is `127.0.0.1` to `new URL`, and to a browser.
  refuses('https://2130706433/a', GITHUB, URL_REFUSAL.IP_LITERAL);
  refuses('https://0x7f.1/a', GITHUB, URL_REFUSAL.IP_LITERAL);
  refuses('https://0177.0.0.1/a', GITHUB, URL_REFUSAL.IP_LITERAL);
});

test('an IPv6 literal is refused', () => {
  refuses('https://[::1]/a', GITHUB, URL_REFUSAL.IP_LITERAL);
  refuses('https://[2001:db8::1]/a', GITHUB, URL_REFUSAL.IP_LITERAL);
});

// ---------------------------------------------------------------------------
// Confusables: punycode, non-ASCII, a decoded host, a backslash
// ---------------------------------------------------------------------------

test('a punycode host is refused rather than matched', () => {
  // The list is ASCII, so a punycode label can never be on it. It is refused
  // with its own code rather than as a miss, because "not on the list" and
  // "this host is not the host it looks like" are different facts.
  refuses('https://xn--80ak6aa92e.com/a', GITHUB, URL_REFUSAL.PUNYCODE_HOST);
  refuses('https://xn--80ak6aa92e.com/a', ['xn--80ak6aa92e.com'], URL_REFUSAL.PUNYCODE_HOST);
});

test('a non-ASCII host is refused, before the parser turns it into punycode', () => {
  // `https://аpple.com/` (Cyrillic а) parses to `xn--pple-43d.com`.
  refuses('https://аpple.com/a', GITHUB, URL_REFUSAL.NON_ASCII);
  refuses('https://github.com/ünïcøde', GITHUB, URL_REFUSAL.NON_ASCII);
});

test('a `%` in the path or query is accepted: only the authority is checked', () => {
  // The complement of `encoded-host`, and the case that keeps it honest. A
  // percent-encoded host is a string that is not the URL it looks like; a
  // percent-encoded path is just a URL. If the `%` check ever widens from the
  // authority to the whole string, this is the test that fails.
  accepts('https://github.com/a%20b/c', GITHUB, 'github.com');
  accepts('https://github.com/o/r/pull/1?diff=split&w=%31', GITHUB, 'github.com');
  accepts('https://github.com/o/r/pull/1#discussion_r%31', GITHUB, 'github.com');
  accepts('https://github.com/o/r/pull/1?q=%E2%9C%93#frag', GITHUB, 'github.com');
});

test('a percent-encoded host is refused, though the parser decodes it', () => {
  // `new URL('https://%67ithub.com/a').hostname` is `github.com`.
  refuses('https://%67ithub.com/a', GITHUB, URL_REFUSAL.ENCODED_HOST);
  refuses('https://github%2ecom/a', GITHUB, URL_REFUSAL.ENCODED_HOST);
});

test('a backslash is refused, because the parser turns it into a slash', () => {
  // `https://github.com\.evil.example/a` parses with host `github.com` and the
  // rest in the path -- a string that is not the URL it appears to be.
  refuses('https://github.com\\.evil.example/a', GITHUB, URL_REFUSAL.BACKSLASH);
  refuses('https://evil.example\\@github.com/a', GITHUB, URL_REFUSAL.BACKSLASH);
});

// ---------------------------------------------------------------------------
// Whitespace and control characters: refused, never trimmed
// ---------------------------------------------------------------------------

test('whitespace around or inside a URL is refused, never trimmed', () => {
  refuses('  https://github.com/a  ', GITHUB, URL_REFUSAL.CONTROL_CHARACTER);
  refuses('https://github.com/a ', GITHUB, URL_REFUSAL.CONTROL_CHARACTER);
  refuses('https://github.com/a b', GITHUB, URL_REFUSAL.CONTROL_CHARACTER);
  refuses('https://github.com\t/a', GITHUB, URL_REFUSAL.CONTROL_CHARACTER);
  refuses('https://github.com\n/a', GITHUB, URL_REFUSAL.CONTROL_CHARACTER);
  refuses('https://github.com\u0000/a', GITHUB, URL_REFUSAL.CONTROL_CHARACTER);
});

// ---------------------------------------------------------------------------
// The length bound
// ---------------------------------------------------------------------------

test('the length bound is 2048, pinned here so it cannot drift silently', () => {
  // Every other case in this file derives its string from MAX_URL_LENGTH, which
  // means the constant could be changed to anything and they would all still
  // pass. This is the one assertion on the number itself: the module's doc
  // comment and its README both state 2048, and a bound that moves without a
  // test moving with it is a bound nobody chose.
  assert.equal(MAX_URL_LENGTH, 2048);
});

test('the length bound is a refusal, and it is inclusive at the bound', () => {
  const prefix = 'https://github.com/';
  const atLimit = prefix + 'a'.repeat(MAX_URL_LENGTH - prefix.length);
  assert.equal(atLimit.length, MAX_URL_LENGTH);
  accepts(atLimit, GITHUB, 'github.com');
  refuses(`${atLimit}a`, GITHUB, URL_REFUSAL.TOO_LONG);
});

// ---------------------------------------------------------------------------
// Wrong types, and every other way in
// ---------------------------------------------------------------------------

test('anything that is not a string is refused, never coerced', () => {
  for (const candidate of [
    undefined,
    null,
    42,
    0,
    true,
    false,
    {},
    [],
    ['https://github.com/a'],
    new URL('https://github.com/a'),
    Symbol('https://github.com/a'),
    () => 'https://github.com/a',
  ]) {
    refuses(candidate, GITHUB, URL_REFUSAL.NOT_A_STRING);
  }
});

test('the empty string is refused, and so is a string of whitespace', () => {
  refuses('', GITHUB, URL_REFUSAL.EMPTY);
  refuses(' ', GITHUB, URL_REFUSAL.CONTROL_CHARACTER);
});

test('a string that is not a URL is refused', () => {
  refuses('fugarosandbox', GITHUB, URL_REFUSAL.NOT_A_URL);
  refuses('edgeappinc/fugarosandbox', GITHUB, URL_REFUSAL.NOT_A_URL);
  refuses('//github.com/a', GITHUB, URL_REFUSAL.NOT_A_URL);
  refuses('www.github.com/a', GITHUB, URL_REFUSAL.NOT_A_URL);
  refuses('https://', GITHUB, URL_REFUSAL.NOT_A_URL);
});

test('a string with no scheme at all is refused, whatever the parser calls it', () => {
  // `new URL('github.com:443/a')` succeeds: the parser reads `github.com` as a
  // scheme, because a scheme is letters, digits, `+`, `-` and `.`. So this is
  // refused as a scheme rather than as a parse failure, which is the same
  // answer either way -- it is not https.
  refuses('github.com:443/a', GITHUB, URL_REFUSAL.SCHEME_NOT_HTTPS);
});

test('a refusal never carries a host, so nothing downstream can trust one', () => {
  const result = validateExternalUrl('https://evil.example/a', GITHUB);
  assert.equal(result.ok, false);
  assert.equal(result.host, undefined);
  assert.equal(result.url, undefined);
});

test('a refusal never echoes the string it refused', () => {
  // A refusal is shown to a person and often logged, and the string being
  // refused is hostile by definition -- it arrived from a node a coding agent
  // wrote. Interpolating it is how a refusal becomes the injection it refused.
  const marker = 'zz-refused-marker-zz';
  const hostile = [
    `https://${marker}.example/a`,
    `https://evil.example/${marker}`,
    `javascript:alert('${marker}')`,
    `https://user:${marker}@evil.example/a`,
    `https://github.com:443/${marker}`,
    `https://${marker}`,
  ];
  for (const candidate of hostile) {
    const result = validateExternalUrl(candidate, GITHUB);
    assert.equal(result.ok, false, `${candidate} should be refused`);
    assert.equal(
      result.message.includes(marker),
      false,
      `the message for ${result.code} echoed the string it refused`,
    );
  }
});

test('the refusal codes are distinct non-empty strings', () => {
  const codes = Object.values(URL_REFUSAL);
  assert.equal(new Set(codes).size, codes.length, 'two codes share a name');
  for (const code of codes) {
    assert.equal(typeof code, 'string');
    assert.ok(code.length > 0);
  }
});

// ---------------------------------------------------------------------------
// The fail-closed sweep: everything hostile, in one table
// ---------------------------------------------------------------------------

test('every hostile string is refused, each with its own code', () => {
  const table = [
    // The scheme, or the absence of a usable one.
    ['http://github.com/a', URL_REFUSAL.SCHEME_NOT_HTTPS],
    ['ftp://github.com/a', URL_REFUSAL.SCHEME_NOT_HTTPS],
    ['javascript:alert(1)', URL_REFUSAL.SCHEME_NOT_HTTPS],
    ['JavaScript:alert(1)', URL_REFUSAL.SCHEME_NOT_HTTPS],
    ['data:text/html,<script>alert(1)</script>', URL_REFUSAL.SCHEME_NOT_HTTPS],
    ['blob:https://github.com/abc', URL_REFUSAL.SCHEME_NOT_HTTPS],
    ['file://github.com/a', URL_REFUSAL.SCHEME_NOT_HTTPS],
    ['https:github.com/a', URL_REFUSAL.NOT_CANONICAL],
    ['https:/github.com/a', URL_REFUSAL.NOT_CANONICAL],
    ['https:///github.com/a', URL_REFUSAL.NOT_A_URL],
    ['HTTPS:///github.com/a', URL_REFUSAL.NOT_A_URL],
    // The host.
    ['https://github.com.evil.example/a', URL_REFUSAL.HOST_NOT_ALLOWED],
    ['https://evil.example/github.com', URL_REFUSAL.HOST_NOT_ALLOWED],
    ['https://sub.github.com/a', URL_REFUSAL.HOST_NOT_ALLOWED],
    ['https://github.com./a', URL_REFUSAL.TRAILING_DOT],
    // `gіthub.com` with a Cyrillic і. The label has to be valid punycode:
    // Node 22's parser throws on an invalid one (the earlier fixture,
    // `xn--github-9d0c`), so the case answered `not-a-url` there and never
    // reached the punycode rule it exists to test.
    ['https://xn--gthub-n2e.com/a', URL_REFUSAL.PUNYCODE_HOST],
    ['https://%67ithub.com/a', URL_REFUSAL.ENCODED_HOST],
    ['https://127.0.0.1/a', URL_REFUSAL.IP_LITERAL],
    ['https://[::1]/a', URL_REFUSAL.IP_LITERAL],
    ['https://2130706433/a', URL_REFUSAL.IP_LITERAL],
    // The authority.
    ['https://user@github.com/a', URL_REFUSAL.USERINFO],
    ['https://github.com@evil.example/a', URL_REFUSAL.USERINFO],
    ['https://github.com:443/a', URL_REFUSAL.PORT],
    ['https://github.com:8443/a', URL_REFUSAL.PORT],
    ['https://github.com\\.evil.example/a', URL_REFUSAL.BACKSLASH],
    // The string itself.
    ['', URL_REFUSAL.EMPTY],
    ['https://github.com/a b', URL_REFUSAL.CONTROL_CHARACTER],
    ['  https://github.com/a', URL_REFUSAL.CONTROL_CHARACTER],
    ['https://github.com/ünïcøde', URL_REFUSAL.NON_ASCII],
    ['fugarosandbox', URL_REFUSAL.NOT_A_URL],
    ['edgeappinc/fugarosandbox', URL_REFUSAL.NOT_A_URL],
    ['fix the flaky test in internal/watch', URL_REFUSAL.CONTROL_CHARACTER],
    ['https://github.com/' + 'a'.repeat(MAX_URL_LENGTH), URL_REFUSAL.TOO_LONG],
  ];

  for (const [candidate, code] of table) {
    refuses(candidate, GITHUB, code);
  }
});

test('the sweep is a negative control: nothing in it is ever passed through', () => {
  // The plan's own sentence -- "a string the feed carries that is not a URL is
  // never passed through" -- as one assertion over the whole table, so that a
  // future edit that widens a rule has to widen this too.
  const feedStrings = [
    'fugarosandbox',
    'edgeappinc/fugarosandbox',
    'dimipaun/fugaro#518',
    'PR 518',
    'ready for review',
    '',
    'undefined',
    'null',
  ];
  for (const candidate of feedStrings) {
    const result = validateExternalUrl(candidate, BOTH);
    assert.equal(
      result.ok,
      false,
      `a feed string was passed through as a link: ${JSON.stringify(candidate)}`,
    );
  }
});

test('an empty label in the host is a miss, never a normalised hit', () => {
  refuses('https://github..com/a', GITHUB, URL_REFUSAL.HOST_NOT_ALLOWED);
  refuses('https://.github.com/a', GITHUB, URL_REFUSAL.HOST_NOT_ALLOWED);
});
