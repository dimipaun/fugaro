// The redactor's tests (mobile plan, Task P1.3) -- the CLI's own vectors,
// ported.
//
// The plan asks that "the redactor removes token-shaped and key-shaped
// strings, and its tests include the shapes this tree actually uses". The
// redactor this one is a port of is `internal/agent/redact.go` -- the thing
// `fugaro logs` and the agent relay run agent output through -- and its own
// test vectors are `internal/agent/redactfunc_test.go` and
// `TestRelayRedactsEncodedSecrets` in `internal/agent/relay_test.go`. Those
// vectors are ported here rather than invented: a redactor whose tests are
// written from its implementation proves only that it is self-consistent.
//
// **Every secret-shaped value below is built at run time by concatenation.**
// The tree's rule for a fixture is that it must not be mistakable for a real
// leak -- `internal/agent/relay_test.go` says the same of its own long secret
// ("the prefix is the length of a real key's but deliberately not its
// format, so secret scanning never takes this fixture for a leaked key") --
// and a literal that *looks* like a token is a literal a scanner has to be
// taught to ignore.
//
// The expected values are computed here with Node's own `Buffer` rather than
// with the module's encoder, so a defect in the module's hand-rolled base64
// fails these tests instead of agreeing with itself.

import assert from 'node:assert/strict';
import test from 'node:test';

import { DigestError, ERROR } from './errors.js';
import { REDACTED, redact, redactFunc, redactForms } from './redact.js';

// --- the shapes this tree actually uses, built at run time ---------------

/** A GitHub token's shape (`ghs_`, as `internal/agent/relay_test.go` uses). */
const GH_SECRET = ['gh', 's_T0ken', '+With/Sl=sh?&x~!'].join('');

/** An Anthropic key's shape, from `internal/agent/relay_test.go`. */
const LONG_BASE = ['fake', '-sk', '-ant-', 'x',
  'Zq8+Rv/2mXkP0wLs9TbN4yHc7GdJ1eUa6FoIr3VhQ5nWt-Bx_KzYpMg8jSl2CuEo0iAd4fR7HbT9vNq1XwLkPz6mYs3'].join('');

/** A PEM private key: the multi-line shape the port's line rule exists for. */
const PEM_SECRET = ['-----BEGIN ', 'PRIVATE KEY-----', '\n',
  'MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQ', '\n',
  '-----END ', 'PRIVATE KEY-----'].join('');

/** A credential with a `"` and a newline in it: the JSON-escaped shape. */
const QUOTED_SECRET = ['sk', '-test-', 'a"b\\c', '\n', 'd'].join('');

const BEARER = 'Authorization: Bearer ';

// --- Node's own encoders, as the test's reference ------------------------

function std(s, pad = true) {
  const out = Buffer.from(s, 'utf8').toString('base64');
  return pad ? out : out.replace(/=+$/, '');
}

function url(s, pad = true) {
  const out = Buffer.from(s, 'utf8').toString('base64url');
  return pad ? out.padEnd(Math.ceil(out.length / 4) * 4, '=') : out;
}

// Go's url.QueryEscape and url.PathEscape, derived here from
// `encodeURIComponent` plus Go's own documented differences from it: Go
// escapes `! ' ( ) *` (which `encodeURIComponent` leaves alone), writes a
// space in a query as `+`, and in a path segment leaves `$ & + : = @`
// unescaped while escaping `/ ; , ?`.
function queryEscape(s) {
  return encodeURIComponent(s)
    .replace(/%20/g, '+')
    .replace(/[!'()*]/g, (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`);
}

function pathEscape(s) {
  return encodeURIComponent(s)
    .replace(/[!'()*]/g, (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`)
    .replace(/%24/g, '$')
    .replace(/%26/g, '&')
    .replace(/%2B/g, '+')
    .replace(/%3A/g, ':')
    .replace(/%3D/g, '=')
    .replace(/%40/g, '@');
}

function wrapLines(s, w) {
  const lines = [];
  for (let i = 0; i < s.length; i += w) {
    lines.push(s.slice(i, i + w));
  }
  return lines.join('\n');
}

/**
 * `internal/agent/relay_test.go`'s `secretOnlyRuns`: the base64 characters
 * that encode only secret bytes, at each byte offset the secret can start at
 * inside a longer encoded input. A redactor that leaves one of these runs
 * standing has left a run of the secret in the clear.
 */
function secretOnlyRuns(encode, input, lo, hi) {
  const out = encode(input);
  const bytes = Buffer.byteLength(input);
  const runs = [];
  for (let g = 0; 3 * g + 6 <= bytes; g++) {
    if (3 * g >= lo && 3 * g + 6 <= hi && 4 * g + 8 <= out.length) {
      runs.push(out.slice(4 * g, 4 * g + 8));
    }
  }
  return runs;
}

// --- the ported vectors ---------------------------------------------------

test('a token-shaped secret is removed in every spelling the CLI registers', () => {
  for (const form of [
    GH_SECRET,
    std(GH_SECRET),
    std(GH_SECRET, false),
    url(GH_SECRET),
    url(GH_SECRET, false),
    queryEscape(GH_SECRET),
    pathEscape(GH_SECRET),
  ]) {
    const text = `the log said ${form} and nothing else`;
    const got = redact(text, [GH_SECRET]);
    assert.ok(!got.includes(form), `left ${form} standing`);
    assert.ok(got.includes(REDACTED));
  }
});

test('the shapes this tree actually uses are caught, raw and base64', () => {
  for (const secret of [GH_SECRET, LONG_BASE, PEM_SECRET, QUOTED_SECRET, `/etc/key: ${GH_SECRET}`]) {
    for (const spelling of [secret, std(secret), url(secret, false)]) {
      const got = redact(`a tool result carrying ${spelling}`, [secret]);
      assert.ok(!got.includes(spelling), `left a ${spelling.length}-byte spelling standing`);
      assert.ok(got.includes(REDACTED));
    }
  }
});

test('base64 of the secret inside a longer input is removed at every byte offset', () => {
  // `echo $TOKEN | base64` (a trailing newline shifts the final group), the
  // secret embedded at each offset inside a longer encoded string, and both
  // alphabets -- `TestRelayRedactsEncodedSecrets`'s own cases.
  const encodings = [std, url];
  for (const prefix of ['', 'A', 'AB', BEARER]) {
    const input = `${prefix}${GH_SECRET}\n`;
    const lo = Buffer.byteLength(prefix);
    const hi = lo + Buffer.byteLength(GH_SECRET);
    for (const encode of encodings) {
      const text = `exit 1: ${encode(input)}\n`;
      const got = redact(text, [GH_SECRET]);
      assert.ok(got.includes(REDACTED));
      for (const run of secretOnlyRuns(encode, input, lo, hi)) {
        assert.ok(!got.includes(run), `left base64 run ${run} of the secret standing`);
      }
    }
  }
  for (const encode of [std, url]) {
    for (const pad of [true, false]) {
      assert.ok(!redact(`decoded: ${encode(GH_SECRET, pad)}`, [GH_SECRET]).includes(encode(GH_SECRET, pad)));
    }
  }
});

test('percent-encoded forms are removed, in a query and in a path', () => {
  for (const text of [
    `curl: (22) 401 for https://api.invalid/?token=${queryEscape(GH_SECRET)}`,
    `curl: (22) 401 for https://api.invalid/t/${pathEscape(GH_SECRET)}/x`,
  ]) {
    assert.ok(redact(text, [GH_SECRET]).includes(REDACTED));
    assert.ok(!redact(text, [GH_SECRET]).includes(GH_SECRET));
  }
});

test('wrapped base64 output is removed at both wrap widths', () => {
  // GNU coreutils `base64` wraps at 76 columns and `openssl base64` at 64;
  // the redactor works line by line, so every wrapped line of the secret is
  // a form. The four lengths are one per base64 group the wrapping can end
  // on -- `longSecrets()`'s own reason.
  for (const secret of [
    LONG_BASE.slice(0, 100),
    LONG_BASE.slice(0, 101),
    LONG_BASE.slice(0, 102),
    LONG_BASE,
  ]) {
    for (const input of [secret, `${secret}\n`]) {
      const wrapped = wrapLines(std(input), 76);
      const wrapped64 = wrapLines(std(input), 64);
      const got = redact(`$ echo $TOKEN | base64\n${wrapped}\n${wrapped64}`, [secret]);
      assert.ok(got.includes(REDACTED));
      for (const line of [...wrapped.split('\n'), ...wrapped64.split('\n')]) {
        assert.ok(!got.includes(line), `left wrapped line ${line} standing`);
      }
    }
  }
});

test('a multi-line secret is removed whole and line by line', () => {
  // A PEM key arrives both raw (as one value) and split across writes, one
  // line at a time -- `internal/agent/redact.go` registers each of its lines
  // for exactly that.
  assert.ok(!redact(`pem ${PEM_SECRET} end`, [PEM_SECRET]).includes('PRIVATE KEY'));
  assert.ok(!redact('only line-two-of-it', ['line-one-of-it\nline-two-of-it']).includes('line-two-of-it'));
  assert.ok(!redact('only MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQ here', [PEM_SECRET]).includes('MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQ'));
});

test('a JSON-escaped secret is removed', () => {
  // Stream-json transcripts carry a secret JSON-encoded, so `a"b\c` arrives
  // as `a\"b\\c` and a byte-level redactor that knows only the raw form
  // misses it (`internal/agent/relay.go`'s header says the same).
  const escaped = JSON.stringify(QUOTED_SECRET).slice(1, -1);
  assert.notEqual(escaped, QUOTED_SECRET);
  const got = redact(`{"type":"text","text":"${escaped}"}`, [QUOTED_SECRET]);
  assert.ok(!got.includes(escaped));
  assert.ok(!got.includes(QUOTED_SECRET));
  assert.ok(got.includes(REDACTED));
});

test('a secret of three bytes or fewer is ignored, and clean text is untouched', () => {
  // The port's inherited rule: redacting a value that short would mangle
  // ordinary text. It is a documented rule of the CLI's redactor, not a
  // silent one.
  assert.equal(redact('the abc of it', ['abc']), 'the abc of it');
  assert.equal(redact('nothing here', [GH_SECRET]), 'nothing here');
  assert.equal(redact('', []), '');
  assert.equal(redact('a secret: abcd', ['abcd']), `a secret: ${REDACTED}`);
});

test('a secret under eight bytes registers no encoded forms', () => {
  // `encodedMin` in `internal/agent/redact.go`: below it, the encodings are
  // short enough to match ordinary text. The raw form is still registered --
  // that is the whole point of the boundary.
  const short = 'abcdefg';
  const got = redact(`raw ${short} and encoded ${std(short)}`, [short]);
  assert.ok(!got.includes(`raw ${short}`));
  assert.ok(got.includes(std(short)), 'seven bytes must not register a base64 form');
});

test('the raw secret is removed even where it is a prefix of the text it hides in', () => {
  // The forms are sorted longest first, so a secret containing another is
  // replaced whole rather than in pieces (`redactForms`'s own reason).
  const inner = ['gh', 's_inner0000token'].join('');
  const outer = ['gh', 's_outer0000', inner].join('');
  const got = redact(`seen ${outer}`, [inner, outer]);
  assert.ok(!got.includes(inner));
  assert.ok(!got.includes(outer));
});

test('one set of forms redacts many strings, and changes none of them', () => {
  // `TestRedactFunc`'s shape: build once, redact many, and leave clean text
  // exactly as it was.
  const secrets = [['sk', '-test-', '0123456789abcdef'].join(''), 'line-one-of-it\nline-two-of-it'];
  const red = redactFunc(secrets);
  for (const text of [
    `key ${secrets[0]} and ${std(secrets[0])}`,
    `pem ${secrets[1]}`,
    'only line-two-of-it',
  ]) {
    const got = red(text);
    assert.ok(got.includes(REDACTED));
    assert.ok(!got.includes('0123456789abcdef'));
    assert.ok(!got.includes('line-'));
  }
  assert.equal(red('nothing here'), 'nothing here');
});

test('redactForms is deduped and ordered longest first', () => {
  const forms = redactForms([GH_SECRET, GH_SECRET, 'aaa']);
  assert.equal(new Set(forms).size, forms.length);
  for (let i = 1; i < forms.length; i++) {
    assert.ok(Buffer.byteLength(forms[i - 1]) >= Buffer.byteLength(forms[i]));
  }
  assert.ok(forms.includes(GH_SECRET));
  assert.ok(!forms.includes('aaa'), 'a three-byte secret is not a form');
});

// --- fail closed ----------------------------------------------------------

test('a secret list that is not a list of strings is refused', () => {
  for (const bad of ['not a list', null, 42, [GH_SECRET, 7], [GH_SECRET, null], [GH_SECRET, ['x']]]) {
    assert.throws(() => redact('text', bad), (err) => {
      assert.ok(err instanceof DigestError);
      assert.equal(err.code, ERROR.SECRETS_NOT_A_LIST);
      return true;
    });
  }
  assert.throws(() => redactForms(undefined), (err) => err.code === ERROR.SECRETS_NOT_A_LIST);
});

test('text that is not text is refused', () => {
  for (const bad of [null, 42, {}, [], undefined]) {
    assert.throws(() => redact(bad, [GH_SECRET]), (err) => {
      assert.equal(err.code, ERROR.TYPE);
      return true;
    });
  }
});

test('the redactor holds no state: equal calls, equal output, inputs untouched', () => {
  const secrets = [GH_SECRET, PEM_SECRET];
  const before = [...secrets];
  assert.equal(redact(`x ${GH_SECRET} y`, secrets), redact(`x ${GH_SECRET} y`, secrets));
  assert.deepEqual(secrets, before);
});

// --- mutation-killing cases (fix round) -----------------------------------

test('a space in a query-escaped secret is the plus sign', () => {
  const secret = ['pass', 'phrase'].join('') + ' one two';
  const text = `GET /?k=${queryEscape(secret)} HTTP/1.1`;
  assert.ok(text.includes('passphrase+one+two'));
  const got = redact(text, [secret]);
  assert.ok(!got.includes('passphrase+one+two'));
  assert.ok(got.includes(REDACTED));
});

test('U+2028 and U+2029 in a secret are removed in their JSON-escaped spelling', () => {
  for (const sep of [' ', ' ']) {
    const secret = ['sk', '-line', sep, 'sep-tail'].join('');
    const escaped = `sk-line\\u${sep.charCodeAt(0).toString(16)}sep-tail`;
    const got = redact(`stream-json said "${escaped}" here`, [secret]);
    assert.ok(!got.includes(escaped), 'left the JSON-escaped form standing');
    assert.ok(got.includes(REDACTED));
  }
});
