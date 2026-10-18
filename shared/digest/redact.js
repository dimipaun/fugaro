// The redactor (mobile plan, Task P1.3; decision D118 and D119).
//
// D118 requires that nothing leaves the app's mailbox unredacted, and D119
// puts the redactor in this package rather than in either consumer, "so that
// the console and the phone cannot disagree about what is safe to say out
// loud". A second implementation of "what is safe" is a second answer to that
// question, so this file is a **port of the redactor this repository already
// has** -- `internal/agent/redact.go`, the one `fugaro logs` and the agent
// relay run agent-written text through -- rather than a new design.
//
// What is ported, and what the port keeps:
//
//   * every secret's **raw** form, its **JSON-escaped** form (a stream-json
//     transcript carries a secret JSON-encoded, so a secret containing `"`,
//     `\` or a newline would otherwise slip through), and, for a secret that
//     spans lines (a PEM key), **each of its lines**;
//   * the **encoded spellings** an agent is likely to print a secret in: the
//     four base64 alphabets of the secret alone, the alignment-independent
//     base64 cores that catch the secret embedded at any byte offset inside a
//     longer encoded input (`echo $TOKEN | base64`, an env dump, a header),
//     the wrapped line pieces of base64 tools (76 and 64 columns), and the
//     URL query and path escapes (percent-encoding);
//   * the two numeric rules: a secret shorter than **4 bytes** is ignored
//     (redacting it would mangle ordinary text), and a secret shorter than
//     **8 bytes** registers no encoded forms (they would be too short to
//     distinguish from ordinary text);
//   * longest form first, so a secret that contains another is replaced
//     whole.
//
// What is **not** ported, deliberately: the streaming `Redactor` writer, which
// emits line by line as it is written to. Both consumers of this package
// redact text they already hold -- a digest's fields, a message's text, an
// agent-written title and body -- and `internal/agent/redact.go` states the
// rule for exactly that case ("use it for text that is published whole rather
// than streamed"). A line-buffered writer is a stream's shape, and this
// package holds no stream.
//
// **Redaction is best-effort, not a guarantee**, and the Go header's list of
// what no redactor catches applies here word for word: a secret split across
// two fields, blocks or events; one the agent slices, truncates or reverses;
// one embedded mid-stream inside wrapped base64; and any encoding not listed
// above (hex, gzip, encryption). Porting the limitation faithfully is part of
// porting the redactor -- a caller that reads this file and believes otherwise
// is worse off than one that never redacted at all.
//
// It is pure and holds no state: `redactFunc` builds one set of forms for a
// caller that redacts many strings with the same secrets, and neither it nor
// `redact` mutates anything it is given.

import { DigestError, ERROR } from './errors.js';

/** What a secret's place is left as: the CLI's own marker, byte for byte. */
export const REDACTED = '[REDACTED]';

/** The shortest secret worth redacting, in UTF-8 bytes (`redactForms`). */
const MIN_SECRET_BYTES = 4;

/** The shortest secret whose encoded spellings are registered (`encodedMin`). */
const ENCODED_MIN_BYTES = 8;

const utf8 = new TextEncoder();

function byteLength(text) {
  return utf8.encode(text).length;
}

/**
 * `s` with every form of every secret in `secrets` replaced by `[REDACTED]`,
 * for text published whole. Throws `E_SECRETS_NOT_A_LIST` for a secret list
 * that is not a list of strings and `E_TYPE` for text that is not text: a
 * redactor that silently did nothing would be worse than one that refused.
 */
export function redact(text, secrets) {
  return replaceAll(assertText(text), redactForms(secrets));
}

/**
 * `redact` for a caller that redacts many strings with the same secrets, with
 * the forms built once. The returned function never mutates `secrets`.
 */
export function redactFunc(secrets) {
  const forms = redactForms(secrets);
  return (text) => replaceAll(assertText(text), forms);
}

/**
 * The strings to replace for `secrets`, longest first so that a secret which
 * contains another is replaced whole. Deduped, and a form shorter than four
 * bytes is dropped.
 */
export function redactForms(secrets) {
  if (!Array.isArray(secrets)) {
    throw new DigestError(ERROR.SECRETS_NOT_A_LIST, 'the secrets');
  }
  const seen = new Set();
  const forms = [];
  const add = (form) => {
    if (byteLength(form) >= MIN_SECRET_BYTES && !seen.has(form)) {
      seen.add(form);
      forms.push(form);
    }
  };
  for (const secret of secrets) {
    if (typeof secret !== 'string') {
      throw new DigestError(ERROR.SECRETS_NOT_A_LIST, 'the secrets');
    }
    add(secret);
    add(jsonEscape(secret));
    if (secret.includes('\n')) {
      for (const line of secret.split('\n')) {
        add(line);
      }
    }
    for (const form of encodedForms(secret)) {
      add(form);
    }
  }
  return forms.sort((a, b) => byteLength(b) - byteLength(a));
}

function assertText(text) {
  if (typeof text !== 'string') {
    throw new DigestError(ERROR.TYPE, 'the text to redact');
  }
  return text;
}

function replaceAll(text, forms) {
  let out = text;
  for (const form of forms) {
    out = out.split(form).join(REDACTED);
  }
  return out;
}

// --- the encodings a secret is printed in ----------------------------------

/**
 * The encoded spellings of a secret of at least `ENCODED_MIN_BYTES` bytes: the
 * four base64 alphabets, the alignment-independent cores, the percent-escapes
 * and the wrapped line pieces. A shorter secret gets none of them -- see the
 * header for why the boundary is where it is.
 */
function encodedForms(secret) {
  if (byteLength(secret) < ENCODED_MIN_BYTES) {
    return [];
  }
  const forms = [
    percentEscape(secret, 'query'),
    percentEscape(secret, 'path'),
    base64(secret, BASE64_STANDARD, true),
    base64(secret, BASE64_STANDARD, false),
    base64(secret, BASE64_URL, true),
    base64(secret, BASE64_URL, false),
  ];
  // The cores: for each unpadded alphabet, the base64 characters that encode
  // only secret bytes, whichever of the three byte offsets the secret starts
  // at inside a longer encoded input. At most two bytes of the secret at each
  // end can stay visible.
  for (const alphabet of [BASE64_STANDARD, BASE64_URL]) {
    for (let offset = 0; offset < 3; offset++) {
      const padded = '\u0000'.repeat(offset) + secret;
      const first = Math.floor((offset + 2) / 3);
      const last = Math.floor(byteLength(padded) / 3);
      if (last - first < 2) {
        continue; // under 8 characters: too weak a pattern
      }
      forms.push(base64(padded, alphabet, false).slice(first * 4, last * 4));
    }
  }
  return forms.concat(wrappedForms(secret));
}

/** The line widths base64 tools wrap at: coreutils at 76, `openssl` at 64. */
const WRAP_WIDTHS = [76, 64];

/**
 * For each wrap width, the lines the standard base64 of `s` and of `s + "\n"`
 * (as `echo "$TOKEN" | base64` prints it) is wrapped into, the secret starting
 * the encoded input. The last line is also registered trimmed to the
 * characters that encode only bytes of the secret, so it matches whatever
 * follows the secret.
 */
function wrappedForms(secret) {
  const forms = [];
  const core = Math.floor(byteLength(secret) / 3) * 4;
  for (const input of [secret, `${secret}\n`]) {
    const encoded = base64(input, BASE64_STANDARD, true);
    for (const width of WRAP_WIDTHS) {
      for (let start = 0; start < encoded.length; start += width) {
        const end = Math.min(start + width, encoded.length);
        forms.push(encoded.slice(start, end));
        if (end === encoded.length && core > start && core < end) {
          forms.push(encoded.slice(start, core));
        }
      }
    }
  }
  return forms.filter((form) => byteLength(form) >= ENCODED_MIN_BYTES);
}

const BASE64_STANDARD = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
const BASE64_URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';

/**
 * The base64 of `text`'s UTF-8 bytes in `alphabet`, padded or unpadded. The
 * four spellings Go's `base64` package registers are this function with the
 * two alphabets and the two padding settings -- which is why it is written
 * out here rather than imported: this package has no dependency, and the
 * tests check it against Node's own encoder rather than against itself.
 */
function base64(text, alphabet, pad) {
  const bytes = utf8.encode(text);
  let out = '';
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i];
    const b1 = i + 1 < bytes.length ? bytes[i + 1] : null;
    const b2 = i + 2 < bytes.length ? bytes[i + 2] : null;
    out += alphabet[b0 >> 2];
    out += alphabet[((b0 & 0x03) << 4) | ((b1 ?? 0) >> 4)];
    out += b1 === null ? '=' : alphabet[((b1 & 0x0f) << 2) | ((b2 ?? 0) >> 6)];
    out += b2 === null ? '=' : alphabet[b2 & 0x3f];
  }
  return pad ? out : out.replace(/=+$/, '');
}

const UNRESERVED = /[A-Za-z0-9\-_.~]/;
const PATH_UNRESERVED = '$&+:=';

/**
 * The percent-encoding of `text`, as Go's `url.QueryEscape` (mode `query`)
 * and `url.PathEscape` (mode `path`) write it: every byte outside the
 * unreserved set is escaped, a query writes a space as `+`, and a path
 * segment leaves `$ & + : = @` unescaped.
 */
function percentEscape(text, mode) {
  let out = '';
  for (const byte of utf8.encode(text)) {
    const ch = String.fromCharCode(byte);
    if (UNRESERVED.test(ch)) {
      out += ch;
      continue;
    }
    if (mode === 'query' && ch === ' ') {
      out += '+';
      continue;
    }
    if (mode === 'path' && (PATH_UNRESERVED.includes(ch) || ch === '@')) {
      out += ch;
      continue;
    }
    out += `%${byte.toString(16).toUpperCase().padStart(2, '0')}`;
  }
  return out;
}

/**
 * `text` as it would appear inside a JSON string literal, without the quotes:
 * `a"b` becomes `a\"b`, and a two-line string's embedded newline becomes the
 * two characters `\` and `n`. It never HTML-escapes `<`, `>` or `&`, matching
 * how the stream-json output itself is encoded.
 */
function jsonEscape(text) {
  let out = '';
  for (const ch of text) {
    const code = ch.codePointAt(0);
    if (ch === '"') {
      out += '\\"';
    } else if (ch === '\\') {
      out += '\\\\';
    } else if (ch === '\n') {
      out += '\\n';
    } else if (ch === '\r') {
      out += '\\r';
    } else if (ch === '\t') {
      out += '\\t';
    } else if (ch === '\b') {
      out += '\\b';
    } else if (ch === '\f') {
      out += '\\f';
    } else if (code >= 0xd800 && code <= 0xdfff) {
      // A lone surrogate is the JS analogue of invalid UTF-8, which Go's
      // encoder writes as a raw U+FFFD (go 1.27).
      out += '\ufffd';
    } else if (code < 0x20 || code === 0x2028 || code === 0x2029) {
      out += `\\u${code.toString(16).padStart(4, '0')}`;
    } else {
      out += ch;
    }
  }
  return out;
}
