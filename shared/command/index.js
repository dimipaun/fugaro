// The wake-word router and the app-command grammar (mobile plan, Task P6.1).
//
// One pure function over a transcript and the app's configured lists, answering
// one of three things:
//
//     app-command      the utterance's first token is a wake word and the rest
//                      of it is one of the closed set of commands below
//     agent-text       the utterance is meant for the active agent; it is
//                      forwarded VERBATIM, and this module does not read it
//     not-understood   the utterance began with the wake word and matched no
//                      command: answered with a fixed sentence and NEVER
//                      forwarded
//
// Why "never forwarded" is the load-bearing half (D164, answered 2026-10-17;
// finding G5 of the functional-spec reconciliation): "Fugaro" is not a
// dictionary word, so an on-device engine (D44, D46, D123 -- local engines
// only) will hear "Figaro", "Fugar oh", or drop it. The spec's claim that the
// wake word "never misroutes" is therefore not true of a transcript, and the
// owner chose **fail closed**: a misheard app command must not become agent
// input. So an utterance that begins with a wake word has exactly two possible
// answers here, and forwarding is not one of them. A missed wake word is the
// other direction and cannot be fixed here: that utterance is agent text, which
// is the spec's own rule and the measured rate is owner-run check 102.
//
// Two consequences of the closed grammar, stated rather than left to be found:
//
//   * **The grammar is closed and small.** It is exactly the five §3.4 commands
//     (switch to a project, talk to an agent, "what project am I on?", "list
//     projects"/"list agents", "what's pending?") plus D169's two: "confirm" and
//     "cancel that". There is no intent detection, no prefix match, no scoring
//     and no "did you mean": an utterance outside it is not understood.
//   * **"Confirm" is recognised only as an app command**, which means it carries
//     the wake word ("Fugaro, confirm"). A bare "confirm" is agent text (D164's
//     first-token rule), and so is every near miss -- "yes", "yes, merge it",
//     "sure", "conform", "confirmed it" -- which is what Voice D47's "a fixed,
//     short, low-confusability phrase rather than 'yes'" requires (G10). The
//     phrase must be the WHOLE command: "Fugaro, confirm it" is not understood.
//
// Nothing here reads a clock, a file, the network or a random source, and the
// module imports nothing at all: the inputs are the whole input, which is what
// D126 asks of everything that can be a pure function. `shared/command/README.md`
// is the module's own contract, and `../../docs/mobile/implementation-plan.md`'s
// P6.1 is this module's task.

/**
 * The three answers. Nothing else is a route: every other outcome is a refusal
 * (see `REFUSAL`), and a caller that renders an unlisted string is rendering
 * something this module never returns.
 */
export const ROUTE = Object.freeze({
  APP_COMMAND: 'app-command',
  AGENT_TEXT: 'agent-text',
  NOT_UNDERSTOOD: 'not-understood',
});

/**
 * The closed command set. Seven kinds: the five §3.4 commands and D169's two.
 * A command kind is data, not a branch -- the grammar table below is the only
 * place a spelling is matched, and `SPEC_COMMANDS` is the five the spec names.
 */
export const COMMAND = Object.freeze({
  SWITCH_PROJECT: 'switch-project',
  TALK_TO_AGENT: 'talk-to-agent',
  WHERE_AM_I: 'where-am-i',
  LIST: 'list',
  WHATS_PENDING: 'whats-pending',
  CONFIRM: 'confirm',
  CANCEL_THAT: 'cancel-that',
});

/** The five §3.4 commands, as data, so a test can pin that there are five. */
export const SPEC_COMMANDS = Object.freeze([
  COMMAND.SWITCH_PROJECT,
  COMMAND.TALK_TO_AGENT,
  COMMAND.WHERE_AM_I,
  COMMAND.LIST,
  COMMAND.WHATS_PENDING,
]);

/**
 * D169's two commands. They are commands rather than confirmations: "confirm"
 * answers a read-back, and "cancel that" abandons the pending action -- it is
 * not a job cancel, which is not in this grammar at all (see the README).
 */
export const CONFIRMATION_COMMANDS = Object.freeze([
  COMMAND.CONFIRM,
  COMMAND.CANCEL_THAT,
]);

/** What `list` can be asked for: a closed pair, never a word the user supplies. */
export const LIST_TARGET = Object.freeze({
  PROJECTS: 'projects',
  AGENTS: 'agents',
});

/**
 * Where D163's default agent came from. The owner's answer is "the last agent
 * used in that project on this phone; on first use, the consumer's first
 * advertised agent -- always announced", and the announcement is the app's
 * (P6.12); the source is here so the app can tell the two apart when it reports
 * what it did.
 */
export const SOURCE = Object.freeze({
  LAST_USED: 'last-used',
  FIRST_ADVERTISED: 'first-advertised',
});

/** Why a wake-word utterance was not understood. A reason is not a fourth route. */
export const REASON = Object.freeze({
  UNMATCHED: 'unmatched',
  NAME_NOT_CONFIGURED: 'name-not-configured',
});

/**
 * The wake word's alias set, as **data** (D164: "a closed alias set for
 * 'Fugaro' (measured on device, check 102)").
 *
 * Two entries, and no more than two, on purpose: `figaro` is the near miss G5
 * names by name, and a general model hears it often. The set is a list rather
 * than a branch so that check 102's measurement -- which is owner-run and has
 * not been run -- can widen it by editing data, and so that a test can pin its
 * contents instead of trusting a reader.
 *
 * An alias is matched against the utterance's FIRST token, folded the same way
 * the utterance is (lowercase, trailing sentence punctuation removed). An alias
 * is one token: "fugar oh" cannot be one, and a caller that passes one is
 * refused rather than silently never matching.
 */
export const WAKE_ALIASES = Object.freeze(['fugaro', 'figaro']);

/**
 * The confirmation phrase, as data, and closed. One phrase: Voice D47 asks for a
 * "fixed, short, low-confusability phrase", and D169's answer calls it an
 * explicit "confirm".
 */
export const CONFIRMATION_PHRASES = Object.freeze(['confirm']);

/**
 * The near misses, as data. This module has **no branch on any of them**: they
 * are here so the test that pins "none of these confirms" reads as the list G10
 * and P6.2 name -- "yes", "yes, merge it", "sure" from the spec's own examples,
 * "conform" and "confirmed it" from P6.2's misrecognitions -- plus the obvious
 * synonyms, rather than as a hand-written table nobody can audit.
 */
export const NOT_CONFIRMATIONS = Object.freeze([
  'yes',
  'yeah',
  'yep',
  'ok',
  'okay',
  'sure',
  'yes please',
  'yes, merge it',
  'merge it',
  'go ahead',
  'do it',
  'approve',
  'approved',
  'confirmed',
  'confirmed it',
  'conform',
]);

/**
 * What the app says when a wake-word utterance matches no command. D164's own
 * words: it "is answered 'I didn't catch that' and is never forwarded to the
 * agent". The sentence is a constant here rather than built by the app so that
 * the two halves of that rule -- the answer and the refusal to forward -- cannot
 * drift apart.
 */
export const NOT_UNDERSTOOD_REPLY = "I didn't catch that";

/**
 * The alias set's bound. Anti-pathology, not policy: a wake-word set is a
 * handful of near misses (the default has two), and a caller handing this module
 * hundreds of them is a bug to refuse, not a configuration to honour.
 */
export const MAX_ALIASES = 8;

/**
 * The configured lists' bound -- projects and agents alike. D161 calls the
 * agents list "a short list" in the heartbeat, and P6.3 fixes the schema's own
 * bound; this one only stops a pathological list from being walked.
 */
export const MAX_ENTRIES = 32;

/** A spoken name's bound, in code units. Long enough for a spelled-out name. */
export const MAX_NAME_LENGTH = 64;

/**
 * An entry id's bound and shape. An id reaches the `agent` field of a message
 * (D161) and the app's account store (D114, P3.1), so it is held to a database
 * key's rule -- a strict subset of what RTDB itself admits, so nothing this
 * module accepts can be refused later by the rules P6.3 and P6.4 write.
 */
export const MAX_ID_LENGTH = 64;

/**
 * The utterance's bound, in code units. It is P0.2's `TEXT_MAX` (4096), and the
 * reason is not arbitrary: an utterance this router calls agent text becomes a
 * mailbox message, and a transcript the schema would refuse is refused here
 * instead -- before it is routed, and never silently truncated.
 */
export const MAX_UTTERANCE_LENGTH = 4096;

/**
 * The refusal codes. One per distinct way an input can fail to be this router's
 * input, because "the app heard nothing" and "the app was handed something it
 * could not read" are different facts and a single code for both loses the
 * second one. A refusal is never coerced into a route: a caller that could not
 * be routed to is a caller that must not be routed.
 */
export const REFUSAL = Object.freeze({
  INPUT_NOT_AN_OBJECT: 'input-not-an-object',
  UNKNOWN_FIELD: 'unknown-field',

  UTTERANCE_NOT_A_STRING: 'utterance-not-a-string',
  UTTERANCE_BLANK: 'utterance-blank',
  UTTERANCE_TOO_LONG: 'utterance-too-long',
  UTTERANCE_CONTROL_CHARACTER: 'utterance-control-character',
  UTTERANCE_BAD_CHARACTER: 'utterance-bad-character',
  UTTERANCE_FORMAT_CHARACTER: 'utterance-format-character',

  INVALID_ALIASES: 'invalid-aliases',
  INVALID_PROJECTS: 'invalid-projects',
  INVALID_PROJECT: 'invalid-project',
  INVALID_AGENTS: 'invalid-agents',
  INVALID_AGENT: 'invalid-agent',
  AMBIGUOUS_SPOKEN_NAME: 'ambiguous-spoken-name',

  /**
   * The one refusal that is about the *shape of the app's own configuration*
   * rather than about a value: a wake word that is also a confirmation word or
   * a yes-word. It is its own code because it is its own hazard.
   *
   * A wake word of "yes" would make "yes confirm" a confirmation, and a wake
   * word of "confirm" would make "confirm confirm" one. Voice D47's "a fixed,
   * short, low-confusability phrase rather than 'yes'" is exactly the
   * requirement that the two vocabularies not overlap, so the overlap is
   * refused by construction. The honest limit: this keeps the DEFAULT set and
   * any checked set disjoint from the vocabularies this module knows; a caller
   * that passes a wake word this module has never heard of ("yep sure") can
   * still widen what may precede the phrase, and no pure function of one
   * utterance can catch that.
   */
  ALIAS_COLLIDES_WITH_CONFIRMATION: 'alias-collides-with-confirmation',

  INVALID_PROJECT_ID: 'invalid-project-id',
  INVALID_LAST_USED: 'invalid-last-used',
  NO_AGENTS_ADVERTISED: 'no-agents-advertised',
});

/** The name slot in a grammar pattern: the rest of the utterance, as a name. */
const NAME_SLOT = '<name>';

/**
 * The grammar, as a table. Every accepted spelling of every command is a row,
 * and a row is a list of normalised tokens with at most one name slot, which is
 * always last. Nothing matches a row by prefix, score or similarity: a row
 * either equals the utterance's tokens (after the wake word) or it does not.
 *
 * "what's pending" has three rows because an on-device engine renders the
 * apostrophe as ' or as a typographic ’, or drops it; that is the one
 * spelling variance the grammar admits, and it is data like everything else.
 */
const GRAMMAR = Object.freeze([
  Object.freeze({
    command: COMMAND.SWITCH_PROJECT,
    pattern: Object.freeze(['switch', 'to', NAME_SLOT]),
    slot: 'project',
  }),
  Object.freeze({
    command: COMMAND.TALK_TO_AGENT,
    pattern: Object.freeze(['talk', 'to', NAME_SLOT]),
    slot: 'agent',
  }),
  Object.freeze({
    command: COMMAND.WHERE_AM_I,
    pattern: Object.freeze(['what', 'project', 'am', 'i', 'on']),
  }),
  Object.freeze({
    command: COMMAND.LIST,
    pattern: Object.freeze(['list', 'projects']),
    list: LIST_TARGET.PROJECTS,
  }),
  Object.freeze({
    command: COMMAND.LIST,
    pattern: Object.freeze(['list', 'agents']),
    list: LIST_TARGET.AGENTS,
  }),
  Object.freeze({
    command: COMMAND.WHATS_PENDING,
    pattern: Object.freeze(["what's", 'pending']),
  }),
  Object.freeze({
    command: COMMAND.WHATS_PENDING,
    pattern: Object.freeze(['whats', 'pending']),
  }),
  Object.freeze({
    command: COMMAND.WHATS_PENDING,
    pattern: Object.freeze(['what’s', 'pending']),
  }),
  Object.freeze({
    command: COMMAND.CONFIRM,
    pattern: Object.freeze(['confirm']),
  }),
  Object.freeze({
    command: COMMAND.CANCEL_THAT,
    pattern: Object.freeze(['cancel', 'that']),
  }),
]);

/** The fields each function reads. An unknown one is refused: this is not a bag. */
const ROUTE_FIELDS = Object.freeze(['utterance', 'aliases', 'projects', 'agents']);
const DEFAULT_AGENT_FIELDS = Object.freeze(['project', 'lastUsedByProject', 'agents']);

/** An entry's fields, exactly: D161's id and spoken name, and nothing else. */
const ENTRY_FIELDS = Object.freeze(['id', 'spokenName']);

/**
 * What separates words: whitespace, and the clause marks an engine (or a typed
 * transcript) puts between the wake word and the command with or without a space
 * -- comma, semicolon, colon, the one-character ellipsis, en and em dashes.
 * "Fugaro,confirm" and "Fugaro—confirm" are two words, as they were spoken.
 */
const WORD_SEPARATORS = /[\s,;:…–—]+/u;

/**
 * Punctuation (Unicode category P), stripped from BOTH ENDS of each word. An
 * engine punctuates what it hears ("Fugaro: list projects?", a quoted
 * transcript), and a mark at a word's edge is not part of the word. Review
 * finding on PR #585: stripping only a trailing `,.!?` left "fugaro:" and
 * "\"fugaro" as first tokens, so a heard app command was forwarded as agent
 * text. Internal punctuation is left alone, so "v1.2" and "v12", and "con-firm"
 * and "confirm", stay different words; letters are never folded beyond case.
 */
const EDGE_PUNCTUATION = /^\p{P}+|\p{P}+$/gu;

/**
 * An invisible format character (Unicode category Cf): zero-width space and
 * joiners, the BOM, the soft hyphen, the bidi embeddings, overrides and
 * isolates. None is spoken; each lets the text a person SEES differ from the
 * text this module ROUTES ("Fugaro\u200b, confirm" looks like an app command and
 * is not one), so it is refused rather than stripped or routed.
 */
const FORMAT_CHARACTER = /\p{Cf}/u;

/** A database key, as an entry id must be. RTDB admits no `.`, `$`, `#`, `[`, `]`, `/`. */
const ID_PATTERN = /^[A-Za-z0-9_-]+$/;

/** A refusal: no route, a code the caller can branch on, and the field at fault. */
function refuse(code, field, detail) {
  return Object.freeze({ ok: false, code, field, detail });
}

/**
 * A plain data object -- an object literal, or one with no prototype -- and not
 * an instance of anything. An instance can answer a field through a getter or
 * carry state this module has no business reading, and a `Map` is not a store
 * this module agreed to read.
 */
function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

/**
 * The first character in `value` that must not be routed, or `null`.
 *
 * Two shapes, and they are different facts: a **control character** (C0, DEL)
 * is a rendering hazard -- it reaches a screen and a speaker -- and a **lone
 * surrogate** is not encodable text at all. Line breaks are admitted only where
 * the caller says so: an utterance may carry one (the text screen of P6.9 types
 * into the same pipeline, and P0.2's schema admits line breaks in text), but a
 * name or an alias may not, because a name with a newline in it is a name
 * nothing can match.
 */
function badCharacter(value, lineBreaks) {
  for (let i = 0; i < value.length; i += 1) {
    const code = value.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = value.charCodeAt(i + 1);
      if (next >= 0xdc00 && next <= 0xdfff) {
        i += 1;
        continue;
      }
      return 'surrogate';
    }
    if (code >= 0xdc00 && code <= 0xdfff) {
      return 'surrogate';
    }
    if (code === 0x7f) {
      return 'control';
    }
    if (code < 0x20 && !(lineBreaks && (code === 0x09 || code === 0x0a || code === 0x0d))) {
      return 'control';
    }
  }
  if (FORMAT_CHARACTER.test(value)) {
    return 'format';
  }
  return null;
}

/**
 * The utterance's words, folded: lowercased, split on whitespace, trailing
 * sentence punctuation removed, empty words dropped. One function, used on the
 * utterance AND on every configured name and alias, so both sides of every
 * comparison are folded the same way and nothing matches by accident.
 *
 * Lowercasing is JavaScript's own and is locale-independent: there is no
 * Turkish-i trap here, and the configured name is folded once per call rather
 * than compared to a differently-folded copy of itself.
 */
function wordsOf(text) {
  return text
    .toLowerCase()
    .split(WORD_SEPARATORS)
    .map((word) => word.replace(EDGE_PUNCTUATION, ''))
    .filter((word) => word !== '');
}

/** The folded form of a whole phrase, for comparing names and phrases as units. */
function fold(text) {
  return wordsOf(text).join(' ');
}

/** The words an alias set must never contain, folded: the two other vocabularies. */
const FORBIDDEN_ALIASES = new Set(
  [...CONFIRMATION_PHRASES, ...NOT_CONFIRMATIONS].map((phrase) => fold(phrase)),
);

/**
 * The alias set, checked and folded. An alias is one folded token; a set that
 * cannot match anything -- empty, duplicated, multi-word, or overlapping the
 * confirmation vocabulary -- is refused rather than honoured.
 */
function checkAliases(list) {
  const raw = list === undefined ? WAKE_ALIASES : list;
  if (!Array.isArray(raw)) {
    return refuse(REFUSAL.INVALID_ALIASES, 'aliases', 'the alias set must be an array of tokens');
  }
  if (raw.length === 0) {
    // The one refusal whose reason is worth spelling out: an empty set would
    // make every utterance agent text, which is the opposite of fail closed.
    return refuse(
      REFUSAL.INVALID_ALIASES,
      'aliases',
      'an empty alias set would send every app command to the agent; a wake word is required',
    );
  }
  if (raw.length > MAX_ALIASES) {
    return refuse(REFUSAL.INVALID_ALIASES, 'aliases', `at most ${MAX_ALIASES} aliases`);
  }

  const values = [];
  const seen = new Set();
  for (let i = 0; i < raw.length; i += 1) {
    const alias = raw[i];
    if (typeof alias !== 'string' || alias.length === 0 || alias.length > MAX_NAME_LENGTH) {
      return refuse(REFUSAL.INVALID_ALIASES, `aliases[${i}]`, 'an alias is a non-empty string of at most 64 characters');
    }
    const bad = badCharacter(alias, false);
    if (bad === 'control') {
      return refuse(REFUSAL.INVALID_ALIASES, `aliases[${i}]`, 'an alias may not carry a control character');
    }
    if (bad === 'surrogate') {
      return refuse(REFUSAL.INVALID_ALIASES, `aliases[${i}]`, 'an alias must be encodable text');
    }
    if (bad === 'format') {
      return refuse(REFUSAL.INVALID_ALIASES, `aliases[${i}]`, 'an alias may not carry an invisible format character');
    }
    const key = fold(alias);
    if (key === '') {
      return refuse(REFUSAL.INVALID_ALIASES, `aliases[${i}]`, 'an alias with no words in it can never match');
    }
    if (key.includes(' ')) {
      return refuse(
        REFUSAL.INVALID_ALIASES,
        `aliases[${i}]`,
        'an alias is a single token: D164 matches the utterance’s first token',
      );
    }
    if (seen.has(key)) {
      return refuse(REFUSAL.INVALID_ALIASES, `aliases[${i}]`, 'two aliases fold together');
    }
    if (FORBIDDEN_ALIASES.has(key)) {
      return refuse(
        REFUSAL.ALIAS_COLLIDES_WITH_CONFIRMATION,
        `aliases[${i}]`,
        'a wake word that is a confirmation word or a yes-word makes the two vocabularies overlap',
      );
    }
    seen.add(key);
    values.push(key);
  }
  return { ok: true, values: Object.freeze(values) };
}

/**
 * One configured list -- projects (D114's accounts) or agents (D161's advertised
 * endpoints) -- checked and indexed by folded spoken name.
 *
 * Every entry carries an id and a spoken name and nothing else. Two entries
 * whose names fold together are **refused**, not resolved to one: a name that
 * addresses two endpoints is an ambiguity, and this module will not pick one and
 * call the other a bug later. That is the same rule `shared/presence` applies to
 * two bands that do not nest.
 */
function checkEntries(list, field, listCode, entryCode) {
  const raw = list === undefined ? [] : list;
  if (!Array.isArray(raw)) {
    return refuse(listCode, field, `the configured ${field} must be an array of entries`);
  }
  if (raw.length > MAX_ENTRIES) {
    return refuse(listCode, field, `at most ${MAX_ENTRIES} entries`);
  }

  const values = [];
  const byName = new Map();
  for (let i = 0; i < raw.length; i += 1) {
    const entry = raw[i];
    const where = `${field}[${i}]`;
    if (!isPlainObject(entry)) {
      return refuse(entryCode, where, 'each entry is a plain object carrying an id and a spoken name');
    }
    for (const key of Object.keys(entry)) {
      if (!ENTRY_FIELDS.includes(key)) {
        return refuse(entryCode, `${where}.${key}`, `unknown entry field: ${key}`);
      }
    }
    if (!('id' in entry) || !('spokenName' in entry)) {
      return refuse(entryCode, where, 'an entry carries both an id and a spoken name (D161)');
    }

    const id = entry.id;
    if (
      typeof id !== 'string' ||
      id.length === 0 ||
      id.length > MAX_ID_LENGTH ||
      !ID_PATTERN.test(id)
    ) {
      return refuse(
        entryCode,
        `${where}.id`,
        `an id is 1..${MAX_ID_LENGTH} characters of [A-Za-z0-9_-], because it is a database key`,
      );
    }

    const spokenName = entry.spokenName;
    if (typeof spokenName !== 'string' || spokenName.length > MAX_NAME_LENGTH) {
      return refuse(entryCode, `${where}.spokenName`, `a spoken name is a string of at most ${MAX_NAME_LENGTH} characters`);
    }
    const bad = badCharacter(spokenName, false);
    if (bad === 'control') {
      return refuse(entryCode, `${where}.spokenName`, 'a spoken name may not carry a control character');
    }
    if (bad === 'surrogate') {
      return refuse(entryCode, `${where}.spokenName`, 'a spoken name must be encodable text');
    }
    if (bad === 'format') {
      return refuse(entryCode, `${where}.spokenName`, 'a spoken name may not carry an invisible format character');
    }

    const key = fold(spokenName);
    if (key === '') {
      return refuse(entryCode, `${where}.spokenName`, 'a spoken name with no words in it can neither be spoken nor matched');
    }
    if (byName.has(key)) {
      return refuse(
        REFUSAL.AMBIGUOUS_SPOKEN_NAME,
        field,
        `two entries share the spoken name "${key}", so neither can be addressed`,
      );
    }
    // The entry's values, in a frozen object of this module's own making: the
    // caller's values verbatim, never the caller's object. Everything that
    // leaves this module -- a resolved project, a default agent -- hands back
    // one of these, which is why nothing downstream copies again.
    const copy = Object.freeze({ id, spokenName });
    byName.set(key, copy);
    values.push(copy);
  }
  return { ok: true, values: Object.freeze(values), byName };
}

/** A frozen "not understood": D164's sentence, and no route to the agent. */
function notUnderstood(command, reason, name) {
  const result = {
    ok: true,
    route: ROUTE.NOT_UNDERSTOOD,
    forwarded: false,
    command,
    reason,
    reply: NOT_UNDERSTOOD_REPLY,
  };
  if (name !== undefined) {
    result.name = name;
  }
  return Object.freeze(result);
}

/**
 * The first grammar row the words match, or `null`. A row matches when its
 * tokens equal the utterance's tokens one for one, or -- for the rows ending in
 * the name slot -- when its head equals the utterance's first tokens and at
 * least one token is left over to be the name. There is no partial credit.
 */
function matchGrammar(words) {
  for (const rule of GRAMMAR) {
    const pattern = rule.pattern;
    const head = pattern[pattern.length - 1] === NAME_SLOT ? pattern.slice(0, -1) : pattern;
    const hasSlot = head.length !== pattern.length;
    if (hasSlot ? words.length <= head.length : words.length !== pattern.length) {
      continue;
    }
    let matched = true;
    for (let i = 0; i < head.length; i += 1) {
      if (head[i] !== words[i]) {
        matched = false;
        break;
      }
    }
    if (matched) {
      return { rule, name: hasSlot ? words.slice(head.length).join(' ') : null };
    }
  }
  return null;
}

/**
 * The router. A pure function of its input object:
 *
 *     routeUtterance({ utterance, aliases?, projects?, agents? })
 *
 * `utterance` is the transcript, verbatim. `aliases` is the wake-word set
 * (`WAKE_ALIASES` when omitted); `projects` and `agents` are the configured
 * lists, each entry `{ id, spokenName }`, empty when omitted.
 *
 * It returns `{ ok: true, route, forwarded, ... }` or
 * `{ ok: false, code, field, detail }`. It never throws, never coerces a wrong
 * type into a right one, and never guesses a route from an input it could not
 * read -- the fail-closed rule this package is built on.
 *
 * The whole configuration is checked before anything is routed, including the
 * configuration a given utterance will not need: a router that could not read
 * its own project list must not route on the agent list it did read, and a
 * refusal is louder than a silent half-configuration.
 */
export function routeUtterance(input = {}) {
  if (!isPlainObject(input)) {
    return refuse(REFUSAL.INPUT_NOT_AN_OBJECT, null, 'the input must be a plain object');
  }
  for (const key of Object.keys(input)) {
    if (!ROUTE_FIELDS.includes(key)) {
      return refuse(REFUSAL.UNKNOWN_FIELD, key, `unknown input field: ${key}`);
    }
  }

  // Each field is read exactly once, so a getter cannot answer differently
  // between the check and the use.
  const utterance = input.utterance;
  if (typeof utterance !== 'string') {
    return refuse(REFUSAL.UTTERANCE_NOT_A_STRING, 'utterance', 'the utterance must be a string');
  }
  if (utterance.length > MAX_UTTERANCE_LENGTH) {
    return refuse(
      REFUSAL.UTTERANCE_TOO_LONG,
      'utterance',
      `an utterance longer than ${MAX_UTTERANCE_LENGTH} code units is refused: P0.2's TEXT_MAX is what a forwarded message can carry`,
    );
  }
  const bad = badCharacter(utterance, true);
  if (bad === 'control') {
    return refuse(REFUSAL.UTTERANCE_CONTROL_CHARACTER, 'utterance', 'an utterance may not carry a control character');
  }
  if (bad === 'surrogate') {
    return refuse(REFUSAL.UTTERANCE_BAD_CHARACTER, 'utterance', 'an utterance must be encodable text');
  }
  if (bad === 'format') {
    return refuse(
      REFUSAL.UTTERANCE_FORMAT_CHARACTER,
      'utterance',
      'an utterance may not carry an invisible format character: what is shown must be what is routed',
    );
  }

  const words = wordsOf(utterance);
  if (words.length === 0) {
    return refuse(REFUSAL.UTTERANCE_BLANK, 'utterance', 'an utterance with no words in it cannot be routed');
  }

  const aliases = checkAliases(input.aliases);
  if (!aliases.ok) {
    return aliases;
  }
  const projects = checkEntries(input.projects, 'projects', REFUSAL.INVALID_PROJECTS, REFUSAL.INVALID_PROJECT);
  if (!projects.ok) {
    return projects;
  }
  const agents = checkEntries(input.agents, 'agents', REFUSAL.INVALID_AGENTS, REFUSAL.INVALID_AGENT);
  if (!agents.ok) {
    return agents;
  }

  const first = words[0];
  if (!aliases.values.includes(first)) {
    // The spec's other half: everything that does not begin with the wake word
    // is speech for the active agent. The payload is the transcript verbatim --
    // not the folded copy this module matched on -- because the agent is owed
    // what the user said.
    return Object.freeze({
      ok: true,
      route: ROUTE.AGENT_TEXT,
      forwarded: true,
      command: null,
      text: utterance,
    });
  }

  const match = matchGrammar(words.slice(1));
  if (match === null) {
    // The wake word was heard and nothing after it was recognised. This is the
    // branch D164 is about: the answer is a sentence, and the utterance does not
    // leave the app.
    return notUnderstood(null, REASON.UNMATCHED);
  }

  if (match.name !== null) {
    const list = match.rule.slot === 'project' ? projects : agents;
    const entry = list.byName.get(match.name);
    if (entry === undefined) {
      // A command shape with a name that is not configured. It is not
      // understood -- the name is echoed in its folded form, never as the raw
      // transcript -- and it is still not forwarded.
      return notUnderstood(match.rule.command, REASON.NAME_NOT_CONFIGURED, match.name);
    }
    const result = {
      ok: true,
      route: ROUTE.APP_COMMAND,
      forwarded: false,
      command: match.rule.command,
    };
    result[match.rule.slot] = entry;
    return Object.freeze(result);
  }

  const result = {
    ok: true,
    route: ROUTE.APP_COMMAND,
    forwarded: false,
    command: match.rule.command,
  };
  if (match.rule.list !== undefined) {
    result.list = match.rule.list;
  }
  return Object.freeze(result);
}

/**
 * Whether a route this module produced is a confirmation (D169).
 *
 * It takes the RESULT, not a string, and that is the point: there is
 * deliberately no string-level predicate here, because a bare "confirm" without
 * the wake word is agent text (D164) and a helper that answered "yes, that is a
 * confirmation" for the bare word would be a way to confirm without the fixed
 * phrase being routed at all. The only way to ask the question is to route the
 * utterance first.
 *
 * It reads a result this module produced. It is not a verifier of foreign
 * objects, and the consumer's own re-validation (P6.7, in Go) is what decides
 * whether an action record may run.
 */
export function isConfirmation(result) {
  return (
    isPlainObject(result) &&
    result.ok === true &&
    result.route === ROUTE.APP_COMMAND &&
    result.command === COMMAND.CONFIRM &&
    result.forwarded === false
  );
}

/**
 * D163's default agent, as a pure function:
 *
 *     defaultAgent({ project, lastUsedByProject?, agents? })
 *
 * `project` is the id of the project being switched to, `lastUsedByProject` is
 * the app's local store (project id -> agent id; D163 keeps it on the phone and
 * never in the database, D127's reasoning), and `agents` is the list the
 * consumer advertises in its heartbeat (D161).
 *
 * The rule, as the owner answered it: the last agent used in that project on
 * this phone; on first use, the consumer's first advertised agent. It returns
 * `{ ok: true, agent, source }` or a refusal.
 *
 * One case D163's answer does not name, decided here and stated rather than
 * hidden: a remembered agent that the consumer **no longer advertises**. It
 * cannot be addressed -- an agent is an endpoint the consumer advertises, and
 * nothing else -- so the rule falls back to the first advertised one and says so
 * through `source`. The alternative, refusing, would leave the phone unable to
 * switch to a project whose remembered agent has been renamed, which is against
 * D163's own "switching must be fast".
 *
 * The app announces the result (D163: "always announced"); this function does
 * not speak, and the announcement text is P6.12's.
 */
export function defaultAgent(input = {}) {
  if (!isPlainObject(input)) {
    return refuse(REFUSAL.INPUT_NOT_AN_OBJECT, null, 'the input must be a plain object');
  }
  for (const key of Object.keys(input)) {
    if (!DEFAULT_AGENT_FIELDS.includes(key)) {
      return refuse(REFUSAL.UNKNOWN_FIELD, key, `unknown input field: ${key}`);
    }
  }

  const project = input.project;
  if (
    typeof project !== 'string' ||
    project.length === 0 ||
    project.length > MAX_ID_LENGTH ||
    !ID_PATTERN.test(project)
  ) {
    return refuse(
      REFUSAL.INVALID_PROJECT_ID,
      'project',
      `a project id is 1..${MAX_ID_LENGTH} characters of [A-Za-z0-9_-], because it is a database key`,
    );
  }

  const agents = checkEntries(input.agents, 'agents', REFUSAL.INVALID_AGENTS, REFUSAL.INVALID_AGENT);
  if (!agents.ok) {
    return agents;
  }

  const rawLastUsed = input.lastUsedByProject;
  const lastUsedByProject = rawLastUsed === undefined ? {} : rawLastUsed;
  if (!isPlainObject(lastUsedByProject)) {
    return refuse(REFUSAL.INVALID_LAST_USED, 'lastUsedByProject', 'the local store must be a plain object mapping project ids to agent ids');
  }
  const remembered = Object.hasOwn(lastUsedByProject, project) ? lastUsedByProject[project] : undefined;
  if (remembered !== undefined) {
    // A corrupted store is refused rather than ignored: a store the app cannot
    // read is a bug, and quietly falling back to the first advertised agent
    // would hide it behind a plausible answer.
    if (
      typeof remembered !== 'string' ||
      remembered.length === 0 ||
      remembered.length > MAX_ID_LENGTH ||
      !ID_PATTERN.test(remembered)
    ) {
      return refuse(
        REFUSAL.INVALID_LAST_USED,
        `lastUsedByProject.${project}`,
        `a remembered agent id is 1..${MAX_ID_LENGTH} characters of [A-Za-z0-9_-]`,
      );
    }
  }
  for (const [key, value] of Object.entries(lastUsedByProject)) {
    if (key === project) {
      continue;
    }
    if (
      typeof value !== 'string' ||
      value.length === 0 ||
      value.length > MAX_ID_LENGTH ||
      !ID_PATTERN.test(value)
    ) {
      return refuse(
        REFUSAL.INVALID_LAST_USED,
        `lastUsedByProject.${key}`,
        `a remembered agent id is 1..${MAX_ID_LENGTH} characters of [A-Za-z0-9_-]`,
      );
    }
  }

  if (agents.values.length === 0) {
    return refuse(
      REFUSAL.NO_AGENTS_ADVERTISED,
      'agents',
      'the consumer advertises no agent, so there is no default to name',
    );
  }

  if (remembered !== undefined) {
    for (const agent of agents.values) {
      if (agent.id === remembered) {
        return Object.freeze({ ok: true, agent, source: SOURCE.LAST_USED });
      }
    }
  }
  return Object.freeze({
    ok: true,
    agent: agents.values[0],
    source: SOURCE.FIRST_ADVERTISED,
  });
}
