// Secret-shaped strings, assembled at run time and never written down
// (desktop-console plan Task 2.0).
//
// Every rule in `src/source/redact.ts` and in `src/shared/sanitize.ts` is a
// rule about a *shape*: the value of a key that names a credential, a GitHub
// token, an AWS access-key id, a Google API key, a Slack token, a JSON Web
// Token, a PEM private-key block. The tests that hold those rules need strings
// with the shape, and the honest way to write one is not to write it.
//
// **A literal one in this repository is a defect even though it is fake.** It
// is refused by GitHub's push protection, so the commit that carries it cannot
// be pushed at all; it is flagged by every secret scanner that reads the file;
// and a reader -- or an incident responder who arrives later with a scanner's
// output and no context -- cannot tell an invented key from a leaked one
// without reading the line around it and the diff that added it. The rule this
// module exists to keep is therefore: **no line of this repository contains a
// secret-shaped string, including in the tests.**
//
// So every value below is assembled from pieces at run time, and the pieces are
// chosen so that no *piece* is a shape either: `'gh' + 'p_'` is not a GitHub
// token, `'AI' + 'za'` is not a Google API key, and `-----BEGIN '` does not
// begin a PEM block. A scanner reading this file -- or any test importing it --
// finds nothing, because there is nothing to find.
//
// Nothing here is a credential and nothing here is secret: the values are
// repeated letters and digits, they exist only in a test process, and they are
// never sent anywhere.

/** piece joins the fragments of a fake secret. It exists to make the intent of
 * every call below obvious -- `piece('gh', 'p_')` reads as "two fragments
 * concatenated", not as "a typo in a token" -- and to keep the fragments on one
 * line each so no assembled shape appears in this file. */
function piece(...fragments: readonly string[]): string {
  return fragments.join('')
}

/** repeat is a run of one character, so no fragment here is a shape on its own. */
function repeat(character: string, count: number): string {
  return character.repeat(count)
}

/**
 * inert assembles a shape out of fragments for a test that needs a *piece* of a
 * credential rather than one of the whole values below: half of a token split
 * across two array elements, a body to wrap in base64, a header on its own. It
 * is `piece` under a name a reader of a foreign test file can read --
 * `inert('gh', 'p_')` says "assembled here, on purpose", where a bare prefix
 * says nothing and a scanner cannot tell the difference.
 */
export function inert(...fragments: readonly string[]): string {
  return piece(...fragments)
}

/** highEntropyRun is a 20-character run that mixes upper case, lower case and
 * digits -- the one arrangement `isHighEntropy` calls a credential. It is
 * deliberately **not** written literally in the tests either, for the same
 * reason: it is a value a scanner's entropy heuristic has an opinion about, and
 * the string this module exists to keep out of the repository is any string
 * that looks like a key. */
export function highEntropyRun(): string {
  return piece('aB3xK9m', 'Q2pL7vN', '4tR8wZ')
}

/**
 * HEX_DIGEST is a 40-character hexadecimal run with both letters and digits --
 * the other half of `isHighEntropy`'s split-token rule (a key id, a digest
 * outside a digest field). Lower-case hex is not secret-shaped on its own, but
 * it is assembled here beside the rest so a test that needs "the entropy rule
 * fires" does not reach for a literal run that a heuristic might flag.
 */
export function hexDigest(): string {
  return piece('8f14e45f', 'ceea167a', '5a36dedd', '4bea2543', 'f0b1c2d3')
}

/** githubToken is a GitHub personal access token: the forge's own prefix,
 * then 36 characters of its alphabet. */
export function githubToken(): string {
  return piece('gh', 'p_', repeat('a', 36))
}

/** githubPat is a GitHub fine-grained token. */
export function githubPat(): string {
  return piece('github', '_pat_', repeat('B', 22))
}

/** gitlabToken is a GitLab project token. */
export function gitlabToken(): string {
  return piece('gl', 'pat-', repeat('c', 20))
}

/** npmToken is an npm automation token. */
export function npmToken(): string {
  return piece('np', 'm_', repeat('d', 36))
}

/** providerApiKey is a provider API key of the family the recorder's table
 * names (the assistants' vendors). */
export function providerApiKey(): string {
  return piece('s', 'k-', repeat('E', 24))
}

/** slackToken is a Slack bot token. */
export function slackToken(): string {
  return piece('xo', 'xb-', '0123456789', '-', repeat('f', 12))
}

/** awsKeyId is an AWS access-key id. */
export function awsKeyId(): string {
  return piece('AK', 'IA', repeat('G', 16))
}

/** googleApiKey is a Google API key. */
export function googleApiKey(): string {
  return piece('AI', 'za', repeat('H', 35))
}

/** googleAccessToken is a Google OAuth access token. */
export function googleAccessToken(): string {
  return piece('ya', '29.', repeat('i', 40))
}

/** googleRefreshToken is a Google OAuth refresh token. */
export function googleRefreshToken(): string {
  return piece('1/', '/0', repeat('J', 20))
}

/** jwt is a three-part token, the shape a JSON Web Token has. */
export function jwt(): string {
  return piece('ey', 'J', repeat('K', 10), '.', repeat('L', 10), '.', repeat('M', 10))
}

/** bearerHeader is a `Bearer ...` credential as it appears in a log line. */
export function bearerHeader(body = googleAccessToken()): string {
  return piece('Bearer ', body)
}

/**
 * pemBlock is a PEM private-key block. The header is assembled from two
 * fragments so the file contains no `-----BEGIN ... PRIVATE KEY-----` line; the
 * body is a run of `A`, which is not a key in any encoding.
 */
export function pemBlock(body = repeat('A', 24)): string {
  const begin = piece('-----BEGIN ', 'PRIVATE KEY-----')
  const end = piece('-----END ', 'PRIVATE KEY-----')
  return piece(begin, '\n', body, '\n', end)
}

/** MIXED is the alphabet the realistic bodies below draw from. */
const MIXED = piece('ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz', '0123456789')
const UPPER_DIGITS = piece('ABCDEFGHIJKLMNOPQRSTUVWXYZ', '0123456789')

/** mixedBody is a deterministic, mixed-case, high-entropy run: what a real
 * token's body looks like, unlike the repeated letters above. A small linear
 * congruential generator keeps it reproducible; it is assembled at run time, so
 * no line of this file holds the result. */
function mixedBody(seed: number, length: number, alphabet = MIXED): string {
  let state = seed >>> 0
  let out = ''
  for (let index = 0; index < length; index += 1) {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0
    out += alphabet[state % alphabet.length] ?? 'x'
  }
  return out
}

/** realisticSecrets is one realistic, high-entropy value per token family the
 * rules know, named. The bodies mix case and digits the way issued tokens do,
 * which is what a test of a *fragment* needs: a run of one repeated letter is a
 * shape no entropy rule ever finds, so a fragment of it could leak unnoticed. */
export function realisticSecrets(): readonly (readonly [string, string])[] {
  return [
    ['github', piece('gh', 'p_', mixedBody(11, 36))],
    ['github fine-grained', piece('github', '_pat_', mixedBody(12, 40))],
    ['gitlab', piece('gl', 'pat-', mixedBody(13, 20))],
    ['npm', piece('np', 'm_', mixedBody(14, 36))],
    ['provider sk-', piece('s', 'k-', mixedBody(15, 40))],
    ['slack', piece('xo', 'xb-', mixedBody(16, 12, '0123456789'), '-', mixedBody(17, 24))],
    ['aws key id', piece('AK', 'IA', mixedBody(18, 16, UPPER_DIGITS))],
    ['google api key', piece('AI', 'za', mixedBody(19, 35))],
    ['google access token', piece('ya', '29.', mixedBody(20, 40))],
    ['jwt', piece('ey', 'J', mixedBody(21, 16), '.', 'ey', 'J', mixedBody(22, 16), '.', mixedBody(23, 20))],
    ['high-entropy run', mixedBody(24, 32)],
  ]
}
