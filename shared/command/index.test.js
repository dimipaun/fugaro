// The wake-word router and the app-command grammar's tests (mobile plan, Task P6.1).
//
// Written BEFORE the module it imports, on purpose: that is this task's red
// step. `npm test` from `shared/` discovers this file through P0.1's glob and
// fails with ERR_MODULE_NOT_FOUND until `shared/command/index.js` exists, so the
// run is red for the right reason and not because no test was found -- the one
// way this package's runner can lie (see `shared/README.md`).
//
// The plan's red step is narrower than "the module is missing", and it is about
// the failure that matters: **the router forwarding what it must not**. Run
// against a **stub router that forwards** -- an `index.js` whose `routeUtterance`
// answers `agent-text` for everything -- the test named "an unmatched Fugaro
// utterance is never forwarded" fails, and every other fail-closed test with it.
// `shared/command/mutation-proof.sh` re-runs that same stub as its first guard,
// so the red step stays reproducible after this commit instead of living only in
// a pull-request transcript.
//
// What the tests pin, from the plan's P6.1 and from D164 (answered 2026-10-17):
//
//   * an utterance whose FIRST TOKEN is in the closed alias set is an app
//     command; any other utterance is agent text, forwarded verbatim;
//   * each of the five §3.4 commands parses, with a spoken project or agent name
//     matched ONLY against the configured lists;
//   * a "Fugaro ..." utterance that matches no command yields "not understood"
//     and is NEVER forwarded -- fail closed, which is D164's answer;
//   * "confirm" and "cancel that" are app commands (D169), and nothing else is:
//     "yes", "yes, merge it", "sure", "conform", "confirmed it" and a bare
//     "confirm" without the wake word never confirm;
//   * the alias set is DATA -- a frozen exported list, checked by test -- and
//     "yes" is not in it, nor in the confirmation phrase set;
//   * a name is matched exactly (case-folded), never as a prefix and never
//     fuzzily, and two configured names that fold together are refused rather
//     than resolved to one;
//   * D163's default agent: the last agent used in that project, else the
//     consumer's first advertised one;
//   * every way an input can fail to be this router's input has its own refusal
//     code, and a refusal never carries a route.
//
// No test value here is a secret, and no literal is secret-shaped.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  COMMAND,
  CONFIRMATION_COMMANDS,
  CONFIRMATION_PHRASES,
  LIST_TARGET,
  MAX_ALIASES,
  MAX_ENTRIES,
  MAX_ID_LENGTH,
  MAX_NAME_LENGTH,
  MAX_UTTERANCE_LENGTH,
  NOT_CONFIRMATIONS,
  NOT_UNDERSTOOD_REPLY,
  REASON,
  REFUSAL,
  ROUTE,
  SOURCE,
  SPEC_COMMANDS,
  WAKE_ALIASES,
  defaultAgent,
  isConfirmation,
  routeUtterance,
} from './index.js';

// ---------------------------------------------------------------------------
// The configured lists
// ---------------------------------------------------------------------------

// A phone with three installations (D114's account list, P3.1's store) and a
// consumer advertising two agents (D161's heartbeat list). The names are chosen
// so the tests can tell a name slot from the wake word ("Fugaro dev" is a
// project whose name begins with the wake word), and so one of them is
// multi-word.
const PROJECTS = [
  { id: 'belong', spokenName: 'Belong' },
  { id: 'fugaro-dev', spokenName: 'Fugaro dev' },
  { id: 'docs', spokenName: 'docs refactor' },
];

const AGENTS = [
  { id: 'orchestrator', spokenName: 'orchestrator' },
  { id: 'front', spokenName: 'front agent' },
];

/** Route one utterance against the standard lists. */
function route(utterance, extra = {}) {
  return routeUtterance({ utterance, projects: PROJECTS, agents: AGENTS, ...extra });
}

/** A lone surrogate, built at run time so no literal in this file is malformed. */
const LONE_SURROGATE = String.fromCharCode(0xd800);

// ---------------------------------------------------------------------------
// The route: an app command, agent text, or "not understood"
// ---------------------------------------------------------------------------

test('an utterance whose first token is in the alias set is an app command', () => {
  for (const utterance of [
    'Fugaro, what project am I on?',
    'fugaro list projects',
    'FUGARO, WHATS PENDING',
    'Figaro, list agents',
    'Fugaro,   list    projects',
  ]) {
    const result = route(utterance);

    assert.equal(result.ok, true, utterance);
    assert.equal(result.route, ROUTE.APP_COMMAND, utterance);
    assert.equal(result.forwarded, false, utterance);
  }
});

test('any other utterance is agent text, forwarded verbatim', () => {
  for (const utterance of [
    'fix the flaky test in internal/watch',
    'what project am I on?',
    'list projects',
    'confirm',
    'please Fugaro, list projects',
    "Fugaro's list projects",
    'fugaroish',
  ]) {
    const result = route(utterance);

    assert.equal(result.ok, true, utterance);
    assert.equal(result.route, ROUTE.AGENT_TEXT, utterance);
    assert.equal(result.forwarded, true, utterance);
    assert.equal(Object.isFrozen(result), true, utterance);
    // The payload is what the user said, not a normalised copy of it: the agent
    // gets the transcript, and P1.3's redactor -- not this module -- is what
    // stands between it and the mailbox.
    assert.equal(result.text, utterance, utterance);
  }
});

test('the wake word must be the FIRST token: a mention later in the sentence is agent text', () => {
  // D164's rule is a first-token test, and this is the half of it that sends
  // speech to the agent. A "contains" test would intercept an agent sentence
  // that merely mentions the product name.
  const result = route('ask Fugaro to list the projects');

  assert.equal(result.route, ROUTE.AGENT_TEXT);
  assert.equal(result.text, 'ask Fugaro to list the projects');
});

test('a wake word followed by any punctuation is still the wake word, so a heard command is never forwarded', () => {
  // Review finding (PR #585): only `,.!?` were stripped, and only from the end of
  // a word, so "Fugaro: confirm", "Fugaro… cancel that" (an ellipsis an engine
  // emits as one character), "Fugaro—confirm" or a quoted transcript made the
  // first token "fugaro:" -- not an alias -- and the app command reached the
  // agent as text. That is the misroute D164's fail-closed answer forbids.
  const cases = [
    ['Fugaro: confirm', COMMAND.CONFIRM],
    ['Fugaro; list projects', COMMAND.LIST],
    ['Fugaro… cancel that', COMMAND.CANCEL_THAT],
    ['Fugaro—confirm', COMMAND.CONFIRM],
    ['Fugaro – confirm', COMMAND.CONFIRM],
    ['Fugaro - confirm', COMMAND.CONFIRM],
    ['"Fugaro, confirm."', COMMAND.CONFIRM],
    ['“Fugaro, list projects”', COMMAND.LIST],
    ['Fugaro,confirm', COMMAND.CONFIRM],
    ["(Fugaro) what's pending?", COMMAND.WHATS_PENDING],
    ['Fugaro: switch to Belong.', COMMAND.SWITCH_PROJECT],
  ];
  for (const [utterance, command] of cases) {
    const result = route(utterance);

    assert.equal(result.route, ROUTE.APP_COMMAND, utterance);
    assert.equal(result.command, command, utterance);
    assert.equal(result.forwarded, false, utterance);
  }
  for (const utterance of ['Fugaro: banana', 'Fugaro… confirm it', '"Fugaro" yes']) {
    const result = route(utterance);

    assert.equal(result.route, ROUTE.NOT_UNDERSTOOD, utterance);
    assert.equal(result.forwarded, false, utterance);
  }
});

test('punctuation is stripped only at word edges: an internal mark still makes a different word', () => {
  for (const utterance of ["Fugaro, confirm's", 'Fugaro, con-firm', 'Fugaro, con.firm', 'Fugaro, switch to Bel-ong']) {
    const result = route(utterance);

    assert.equal(result.route, ROUTE.NOT_UNDERSTOOD, utterance);
    assert.equal(isConfirmation(result), false, utterance);
  }
});

test('invisible format characters are refused, so what is shown is what is routed', () => {
  // Review finding (PR #585): a zero-width space, a BOM, a soft hyphen or a bidi
  // control (Unicode category Cf) passed the character check. "Fugaro\u200b,
  // confirm" renders exactly like an app command on the text screen (P6.9) and
  // was forwarded to the agent; a bidi override makes the text shown differ from
  // the text routed. Neither is routed now, in either direction.
  for (const utterance of [
    'Fugaro\u200b, confirm',
    '\u200bFugaro confirm',
    '\ufeffFugaro, confirm',
    'Fugaro \u202econfirm',
    'Fugaro\u2066 confirm\u2069',
    'Fugaro, con\u00adfirm',
    'Fugaro, confirm\u200d',
    'fix the\u200b tests',
  ]) {
    const result = routeUtterance({ utterance });

    assert.equal(result.ok, false, JSON.stringify(utterance));
    assert.equal(result.code, REFUSAL.UTTERANCE_FORMAT_CHARACTER, JSON.stringify(utterance));
    assert.equal(Object.hasOwn(result, 'route'), false);
    assert.equal(isConfirmation(result), false);
  }
  // The configuration is held to the same rule: a name or an alias with an
  // invisible character in it is a name nothing spoken can match.
  assert.equal(route('Fugaro, list projects', { aliases: ['fu\u200bgaro'] }).code, REFUSAL.INVALID_ALIASES);
  assert.equal(
    route('Fugaro, list projects', { projects: [{ id: 'a', spokenName: 'Be\u200blong' }] }).code,
    REFUSAL.INVALID_PROJECT,
  );
});

test('folding is case only: accents, look-alikes and compatibility forms never match a word', () => {
  // Folding must not be looser than lowercasing. A fold that stripped accents or
  // applied compatibility normalisation would make these confirm.
  for (const utterance of [
    'Fugaro, cónfirm',
    'Fugaro, \uff43\uff4f\uff4e\uff46\uff49\uff52\uff4d',
    'Fugaro, c\u043enfirm', // Cyrillic o
    'Fugaro, confirm\u0301',
    'Fugaro, CONF\u0130RM', // Turkish dotted capital I
  ]) {
    const result = route(utterance);

    assert.equal(result.route, ROUTE.NOT_UNDERSTOOD, utterance);
    assert.equal(isConfirmation(result), false, utterance);
  }
  // A look-alike wake word is not the wake word: it is a missed wake word (agent
  // text, D164's own other direction), and it is never a confirmation.
  for (const utterance of ['Fug\u0430ro, confirm', '\uff26\uff35\uff27\uff21\uff32\uff2f confirm', '\ufb01garo, confirm']) {
    const result = route(utterance);

    assert.equal(result.route, ROUTE.AGENT_TEXT, utterance);
    assert.equal(isConfirmation(result), false, utterance);
  }
  // A look-alike name is not the configured name.
  assert.equal(route('Fugaro, switch to B\u0435long').reason, REASON.NAME_NOT_CONFIGURED);
});

test('a leading filler word makes the utterance agent text: the first-token rule, pinned', () => {
  // D164's rule is a first-token test, so "Um, Fugaro, confirm" is a missed wake
  // word -- agent text, never a confirmation. Stripping fillers would widen what
  // may precede the phrase; the residual is check 102's to measure.
  for (const utterance of ['Um, Fugaro, confirm', 'okay Fugaro confirm', 'hey Fugaro, list projects']) {
    const result = route(utterance);

    assert.equal(result.route, ROUTE.AGENT_TEXT, utterance);
    assert.equal(isConfirmation(result), false, utterance);
  }
});

test('a wake word on a later line is agent text, and a newline does not hide one', () => {
  assert.equal(route('fix the tests\nFugaro, confirm').route, ROUTE.AGENT_TEXT);
  assert.equal(route('Fugaro,\nconfirm').command, COMMAND.CONFIRM);
  assert.equal(route('Fugaro\r\nswitch to\nBelong').command, COMMAND.SWITCH_PROJECT);
});

// ---------------------------------------------------------------------------
// The five §3.4 commands
// ---------------------------------------------------------------------------

test('the closed grammar is exactly the five §3.4 commands plus D169’s two', () => {
  assert.equal(SPEC_COMMANDS.length, 5);
  assert.deepEqual([...SPEC_COMMANDS], [
    COMMAND.SWITCH_PROJECT,
    COMMAND.TALK_TO_AGENT,
    COMMAND.WHERE_AM_I,
    COMMAND.LIST,
    COMMAND.WHATS_PENDING,
  ]);
  assert.deepEqual([...CONFIRMATION_COMMANDS], [COMMAND.CONFIRM, COMMAND.CANCEL_THAT]);
  // Every command the module can return is one of those seven, and nothing else
  // is a command at all.
  assert.deepEqual(
    new Set(Object.values(COMMAND)),
    new Set([...SPEC_COMMANDS, ...CONFIRMATION_COMMANDS]),
  );
});

test('"switch to <project>" resolves the spoken name against the configured list', () => {
  const result = route('Fugaro, switch to Belong');

  assert.equal(result.route, ROUTE.APP_COMMAND);
  assert.equal(result.command, COMMAND.SWITCH_PROJECT);
  assert.deepEqual(result.project, { id: 'belong', spokenName: 'Belong' });
});

test('"switch to <project>" matches case-insensitively and across word boundaries', () => {
  assert.deepEqual(route('fugaro switch to FUGARO DEV').project, {
    id: 'fugaro-dev',
    spokenName: 'Fugaro dev',
  });
  assert.deepEqual(route('Fugaro, switch to docs refactor.').project, {
    id: 'docs',
    spokenName: 'docs refactor',
  });
});

test('"talk to <agent>" resolves the spoken name against the configured list', () => {
  const result = route('Fugaro, talk to front agent');

  assert.equal(result.route, ROUTE.APP_COMMAND);
  assert.equal(result.command, COMMAND.TALK_TO_AGENT);
  assert.deepEqual(result.agent, { id: 'front', spokenName: 'front agent' });
});

test('"what project am I on?" parses', () => {
  const result = route('Fugaro, what project am I on?');

  assert.equal(result.route, ROUTE.APP_COMMAND);
  assert.equal(result.command, COMMAND.WHERE_AM_I);
});

test('"list projects" and "list agents" parse, and are one command with a target', () => {
  const projects = route('Fugaro, list projects');
  const agents = route('Fugaro, list agents');

  assert.equal(projects.command, COMMAND.LIST);
  assert.equal(projects.list, LIST_TARGET.PROJECTS);
  assert.equal(agents.command, COMMAND.LIST);
  assert.equal(agents.list, LIST_TARGET.AGENTS);
  // "list" with nothing after it is not a command, and "list frogs" is not one
  // either: the target is a closed pair, not a word.
  assert.equal(route('Fugaro, list').route, ROUTE.NOT_UNDERSTOOD);
  assert.equal(route('Fugaro, list frogs').route, ROUTE.NOT_UNDERSTOOD);
});

test('"what’s pending?" parses, with and without the apostrophe an STT engine may drop', () => {
  for (const utterance of ["Fugaro, what's pending?", 'Fugaro, what’s pending?', 'Fugaro, whats pending', 'Fugaro whats pending']) {
    const result = route(utterance);

    assert.equal(result.route, ROUTE.APP_COMMAND, utterance);
    assert.equal(result.command, COMMAND.WHATS_PENDING, utterance);
  }
});

// ---------------------------------------------------------------------------
// Fail closed: an unmatched wake-word utterance is never forwarded
// ---------------------------------------------------------------------------

test('an unmatched Fugaro utterance is never forwarded', () => {
  const result = route('Fugaro, banana');

  assert.equal(result.ok, true);
  assert.equal(result.route, ROUTE.NOT_UNDERSTOOD);
  assert.equal(result.forwarded, false);
  assert.equal(result.reply, NOT_UNDERSTOOD_REPLY);
  assert.equal(result.command, null);
  assert.equal(result.reason, REASON.UNMATCHED);
  // The transcript is not carried anywhere in the result: what is echoed is the
  // app's own fixed sentence, and nothing the recogniser produced.
  assert.equal(Object.hasOwn(result, 'text'), false);
});

test('no wake-word utterance is ever agent text, over a table of near misses', () => {
  const utterances = [
    'Fugaro',
    'Fugaro,',
    'Fugaro.',
    'Fugaro banana',
    'Fugaro, switch',
    'Fugaro, switch to',
    'Fugaro, switch to Belong please',
    'Fugaro, switch to the Belong project',
    'Fugaro, please switch to Belong',
    'Fugaro, talk to',
    'Fugaro, talk to orchestrator now',
    'Fugaro, which project am I on',
    'Fugaro, what project am I on now',
    'Fugaro, list project',
    'Fugaro, list of projects',
    'Fugaro, what is pending',
    'Fugaro, pending',
    'Fugaro, cancel the docs refactor',
    'Fugaro, cancel',
    'Fugaro, confirm it',
    'Fugaro, confirmed',
    'Fugaro, conform',
    'Fugaro, yes',
    'Fugaro, yes, merge it',
    'Fugaro, sure',
    'Fugaro, merge the pull request',
    'Fugaro, kill the run',
    'Fugaro, set the budget to 100',
    'Fugaro, switch to Belong and cancel the run',
  ];

  for (const utterance of utterances) {
    const result = route(utterance);

    assert.equal(result.ok, true, utterance);
    assert.notEqual(result.route, ROUTE.AGENT_TEXT, utterance);
    assert.equal(result.forwarded, false, utterance);
    assert.equal(result.route, ROUTE.NOT_UNDERSTOOD, utterance);
  }
});

test('a destructive verb is not in D164’s grammar, and with the wake word it is not forwarded', () => {
  // The plan's grammar is closed: the five §3.4 commands plus "confirm" and
  // "cancel that". "Cancel the docs refactor" is a destructive command the
  // D128 amendment allows with a read-back and a "confirm" -- and it is not a
  // word this router understands, so with the wake word it fails closed rather
  // than reaching the agent as text. Without the wake word it is agent text,
  // which is §3.1's stated residual risk (F38), refused on the wire by the
  // consumer (P6.7) and not here.
  assert.equal(route('Fugaro, cancel the docs refactor').route, ROUTE.NOT_UNDERSTOOD);
  assert.equal(route('Fugaro, cancel the docs refactor').forwarded, false);
  assert.equal(route('cancel the docs refactor').route, ROUTE.AGENT_TEXT);
});

test('a name that is not configured is not understood, and names the field at fault', () => {
  const result = route('Fugaro, switch to Nowhere');

  assert.equal(result.route, ROUTE.NOT_UNDERSTOOD);
  assert.equal(result.forwarded, false);
  assert.equal(result.reason, REASON.NAME_NOT_CONFIGURED);
  assert.equal(result.command, COMMAND.SWITCH_PROJECT);
  // The echoed name is the normalised one -- lowercased, punctuation-stripped,
  // whitespace-collapsed -- never the raw transcript.
  assert.equal(result.name, 'nowhere');
  assert.equal(result.reply, NOT_UNDERSTOOD_REPLY);
});

test('with no projects configured, a switch is not understood rather than forwarded', () => {
  const result = routeUtterance({ utterance: 'Fugaro, switch to Belong', projects: [] });

  assert.equal(result.route, ROUTE.NOT_UNDERSTOOD);
  assert.equal(result.forwarded, false);
  assert.equal(result.reason, REASON.NAME_NOT_CONFIGURED);
});

// ---------------------------------------------------------------------------
// The confirmation (D169): what counts as "confirm"
// ---------------------------------------------------------------------------

test('"confirm" and "cancel that" are app commands, recognised only with the wake word', () => {
  const confirm = route('Fugaro, confirm');
  const cancelThat = route('Fugaro, cancel that');

  assert.equal(confirm.route, ROUTE.APP_COMMAND);
  assert.equal(confirm.command, COMMAND.CONFIRM);
  assert.equal(isConfirmation(confirm), true);

  assert.equal(cancelThat.route, ROUTE.APP_COMMAND);
  assert.equal(cancelThat.command, COMMAND.CANCEL_THAT);
  assert.equal(isConfirmation(cancelThat), false);
});

test('a bare "confirm" without the wake word is agent text, never a confirmation', () => {
  const result = route('confirm');

  assert.equal(result.route, ROUTE.AGENT_TEXT);
  assert.equal(result.forwarded, true);
  assert.equal(isConfirmation(result), false);
});

test('no near-miss phrase is ever a confirmation, with or without the wake word', () => {
  assert.ok(NOT_CONFIRMATIONS.length > 0);
  for (const phrase of NOT_CONFIRMATIONS) {
    assert.equal(
      isConfirmation(routeUtterance({ utterance: phrase })),
      false,
      `"${phrase}" must not confirm without the wake word`,
    );
    assert.equal(
      isConfirmation(route(`Fugaro, ${phrase}`)),
      false,
      `"Fugaro, ${phrase}" must not confirm`,
    );
  }
});

test('"yes" is not an alias of "confirm", and the two vocabularies are disjoint', () => {
  // The plan names this test: the alias set is data, and a test pins that "yes"
  // is not an alias of "confirm".
  assert.equal(WAKE_ALIASES.includes('yes'), false);
  assert.equal(CONFIRMATION_PHRASES.includes('yes'), false);
  assert.deepEqual([...CONFIRMATION_PHRASES], ['confirm']);

  for (const phrase of NOT_CONFIRMATIONS) {
    assert.equal(WAKE_ALIASES.includes(phrase), false, `${phrase} must not be a wake word`);
    assert.equal(CONFIRMATION_PHRASES.includes(phrase), false, `${phrase} must not be a confirmation`);
  }
  for (const phrase of CONFIRMATION_PHRASES) {
    assert.equal(WAKE_ALIASES.includes(phrase), false, `${phrase} must not be a wake word`);
  }
});

test('a confirmation phrase must be the whole command, not a prefix of one', () => {
  for (const utterance of [
    'Fugaro, confirm that',
    'Fugaro, confirm it',
    'Fugaro, confirm the merge',
    'Fugaro, confirming',
    'Fugaro, confirm confirm',
  ]) {
    assert.equal(route(utterance).route, ROUTE.NOT_UNDERSTOOD, utterance);
    assert.equal(isConfirmation(route(utterance)), false, utterance);
  }
});

test('a command and "confirm" in one utterance is not a confirmation', () => {
  // P6.2's rule, pinned where the recognition happens: the confirmation is one
  // utterance carrying the fixed phrase and nothing else.
  for (const utterance of [
    'Fugaro, confirm and switch to Belong',
    'Fugaro, switch to Belong confirm',
    'Fugaro, cancel that confirm',
  ]) {
    assert.equal(isConfirmation(route(utterance)), false, utterance);
    assert.equal(route(utterance).forwarded, false, utterance);
  }
});

test('isConfirmation answers false for anything that is not a result of this router', () => {
  // There is deliberately no string-level predicate: a bare "confirm" is agent
  // text (D164), so the only way to ask "is this a confirmation" is to route the
  // utterance first.
  for (const value of ['confirm', null, undefined, 42, [], {}, { ok: true }, { ok: true, route: ROUTE.AGENT_TEXT }]) {
    assert.equal(isConfirmation(value), false, JSON.stringify(value));
  }
  // A hand-built object claiming to be a confirmation is not one: the result has
  // to carry the router's own `forwarded: false` as well, so an object that
  // merely spells the words cannot stand in for a routed utterance.
  assert.equal(
    isConfirmation({ ok: true, route: ROUTE.APP_COMMAND, command: COMMAND.CONFIRM }),
    false,
  );
  assert.equal(
    isConfirmation({ ok: true, route: ROUTE.APP_COMMAND, command: COMMAND.CONFIRM, forwarded: true }),
    false,
  );
});

// ---------------------------------------------------------------------------
// The alias set is data
// ---------------------------------------------------------------------------

test('the alias set is a frozen exported list of tokens', () => {
  assert.ok(Array.isArray(WAKE_ALIASES));
  assert.equal(Object.isFrozen(WAKE_ALIASES), true);
  assert.deepEqual([...WAKE_ALIASES], ['fugaro', 'figaro']);
  for (const alias of WAKE_ALIASES) {
    assert.equal(typeof alias, 'string');
    assert.ok(alias.length > 0);
    assert.equal(alias, alias.toLowerCase(), 'an alias is stored folded, as it is matched');
  }
});

test('the alias set can be replaced wholesale, and the replacement is what routes', () => {
  const result = route('Fugarow, list projects', { aliases: ['Fugarow'] });

  assert.equal(result.route, ROUTE.APP_COMMAND);
  assert.equal(result.command, COMMAND.LIST);
  // The override replaces the set: the default aliases no longer route.
  assert.equal(route('Fugaro, list projects', { aliases: ['Fugarow'] }).route, ROUTE.AGENT_TEXT);
});

test('an empty alias set is refused, never read as "no wake word, so forward everything"', () => {
  for (const aliases of [[], ['', '   '], ['  ']]) {
    const result = route('Fugaro, list projects', { aliases });

    assert.equal(result.ok, false, JSON.stringify(aliases));
    assert.equal(result.code, REFUSAL.INVALID_ALIASES, JSON.stringify(aliases));
    assert.equal(Object.hasOwn(result, 'route'), false);
  }
});

test('an alias that is a confirmation word or a yes-word is refused', () => {
  // A wake word of "yes" would make "yes confirm" a confirmation, which is what
  // Voice D47's "a fixed phrase rather than 'yes'" forbids. The two vocabularies
  // are kept disjoint by construction, and this is the construction.
  for (const aliases of [['yes'], ['Confirm'], ['sure'], ['okay']]) {
    const result = route('Fugaro, confirm', { aliases });

    assert.equal(result.ok, false, JSON.stringify(aliases));
    assert.equal(result.code, REFUSAL.ALIAS_COLLIDES_WITH_CONFIRMATION, JSON.stringify(aliases));
  }
});

test('a malformed alias set is refused', () => {
  for (const aliases of [
    'fugaro',
    [1],
    ['fugaro', 'fugaro'],
    ['Fugaro', 'fugaro'],
    ['fugaro fugaro'],
    ['fugaro', ...Array.from({ length: MAX_ALIASES }, (_, i) => `a${i}`)],
    ['fugaro\n'],
    [`${'a'.repeat(MAX_NAME_LENGTH + 1)}`],
  ]) {
    const result = route('Fugaro, list projects', { aliases });

    assert.equal(result.ok, false, JSON.stringify(aliases));
    assert.equal(result.code, REFUSAL.INVALID_ALIASES, JSON.stringify(aliases));
  }
});

// ---------------------------------------------------------------------------
// Names: exact, case-folded, never fuzzy
// ---------------------------------------------------------------------------

test('a name is matched exactly, never as a prefix or a fuzzy guess', () => {
  for (const utterance of [
    'Fugaro, switch to belo',
    'Fugaro, switch to belong project',
    'Fugaro, switch to doc',
    'Fugaro, talk to orchestr',
    'Fugaro, talk to front',
  ]) {
    const result = route(utterance);

    assert.equal(result.route, ROUTE.NOT_UNDERSTOOD, utterance);
    assert.equal(result.reason, REASON.NAME_NOT_CONFIGURED, utterance);
    assert.equal(result.forwarded, false, utterance);
  }
});

test('two configured names that fold together are refused, not resolved to one', () => {
  const projects = [
    { id: 'a', spokenName: 'Belong' },
    { id: 'b', spokenName: 'belong' },
  ];

  const result = route('Fugaro, switch to Belong', { projects });

  assert.equal(result.ok, false);
  assert.equal(result.code, REFUSAL.AMBIGUOUS_SPOKEN_NAME);
  assert.equal(Object.hasOwn(result, 'route'), false);
});

test('a malformed configured list is refused before anything is routed', () => {
  for (const projects of [
    [null],
    [{ id: 'a' }],
    [{ spokenName: 'A' }],
    [{ id: '', spokenName: 'A' }],
    [{ id: 'a.b', spokenName: 'A' }],
    [{ id: 'a', spokenName: '' }],
    [{ id: 'a', spokenName: '  , ' }],
    [{ id: 'a', spokenName: 'A\u0007' }],
    [{ id: 'a', spokenName: 'A', extra: 1 }],
    [{ id: 'a'.repeat(MAX_ID_LENGTH + 1), spokenName: 'A' }],
    [{ id: 'a', spokenName: 'A'.repeat(MAX_NAME_LENGTH + 1) }],
  ]) {
    const result = route('Fugaro, switch to A', { projects });

    assert.equal(result.ok, false, JSON.stringify(projects).slice(0, 60));
    assert.equal(result.code, REFUSAL.INVALID_PROJECT, JSON.stringify(projects).slice(0, 60));
  }
  // A list that is not a list at all is its own code, and so is a list that is
  // too long to walk: both are failures of the list, not of an entry in it.
  assert.equal(route('Fugaro, list projects', { projects: 7 }).code, REFUSAL.INVALID_PROJECTS);
  assert.equal(route('Fugaro, list projects', { agents: {} }).code, REFUSAL.INVALID_AGENTS);
  assert.equal(
    route('Fugaro, switch to A', {
      projects: Array.from({ length: MAX_ENTRIES + 1 }, (_, i) => ({ id: `a${i}`, spokenName: `A${i}` })),
    }).code,
    REFUSAL.INVALID_PROJECTS,
  );
  assert.equal(
    route('Fugaro, list projects', { aliases: ['fugaro', ...Array.from({ length: MAX_ALIASES }, (_, i) => `a${i}`)] }).code,
    REFUSAL.INVALID_ALIASES,
  );
});

test('the whole configuration is checked before anything is routed', () => {
  // A broken agent list refuses a project command too: a router that could not
  // read its own configuration must not route on the part it did read.
  const result = route('Fugaro, switch to Belong', { agents: [{ id: 'a' }] });

  assert.equal(result.ok, false);
  assert.equal(result.code, REFUSAL.INVALID_AGENT);
});

// ---------------------------------------------------------------------------
// D163: the default agent
// ---------------------------------------------------------------------------

test('the default agent is the last one used in that project', () => {
  const result = defaultAgent({
    project: 'fugaro-dev',
    lastUsedByProject: { 'fugaro-dev': 'front' },
    agents: AGENTS,
  });

  assert.equal(result.ok, true);
  assert.deepEqual(result.agent, { id: 'front', spokenName: 'front agent' });
  assert.equal(result.source, SOURCE.LAST_USED);
});

test('on first use the default agent is the consumer’s first advertised one', () => {
  const result = defaultAgent({ project: 'belong', lastUsedByProject: {}, agents: AGENTS });

  assert.equal(result.ok, true);
  assert.deepEqual(result.agent, { id: 'orchestrator', spokenName: 'orchestrator' });
  assert.equal(result.source, SOURCE.FIRST_ADVERTISED);
});

test('the remembered agent is per project, and never leaks across projects', () => {
  const lastUsedByProject = { 'fugaro-dev': 'front' };

  assert.equal(defaultAgent({ project: 'fugaro-dev', lastUsedByProject, agents: AGENTS }).agent.id, 'front');
  assert.equal(defaultAgent({ project: 'belong', lastUsedByProject, agents: AGENTS }).agent.id, 'orchestrator');
});

test('a remembered agent that is no longer advertised falls back to the first advertised one', () => {
  // D163 names two sources and not this third case. An agent that the consumer
  // no longer advertises cannot be addressed, so the rule falls back rather than
  // naming an endpoint that is not there -- and says which source it used.
  const result = defaultAgent({
    project: 'belong',
    lastUsedByProject: { belong: 'retired' },
    agents: AGENTS,
  });

  assert.equal(result.ok, true);
  assert.equal(result.agent.id, 'orchestrator');
  assert.equal(result.source, SOURCE.FIRST_ADVERTISED);
});

test('the default agent is always one of the advertised agents', () => {
  const agents = [{ id: 'only', spokenName: 'only' }];
  const cases = [
    { project: 'p' },
    { project: 'p', lastUsedByProject: { p: 'only' } },
    { project: 'p', lastUsedByProject: { p: 'ghost' } },
    { project: 'p', lastUsedByProject: { other: 'ghost' } },
  ];

  for (const input of cases) {
    const result = defaultAgent({ ...input, agents });
    assert.equal(result.ok, true, JSON.stringify(input));
    assert.deepEqual(result.agent, { id: 'only', spokenName: 'only' }, JSON.stringify(input));
  }
});

test('with nothing advertised there is no default agent, and it is a refusal', () => {
  const result = defaultAgent({ project: 'belong', agents: [] });

  assert.equal(result.ok, false);
  assert.equal(result.code, REFUSAL.NO_AGENTS_ADVERTISED);
  assert.equal(Object.hasOwn(result, 'agent'), false);
});

test('a malformed local store is refused rather than guessed at', () => {
  for (const lastUsedByProject of [
    'front',
    { belong: 7 },
    { belong: '' },
    { belong: 'a b' },
    { belong: null },
    new Map([['belong', 'front']]),
  ]) {
    const result = defaultAgent({ project: 'belong', lastUsedByProject, agents: AGENTS });

    assert.equal(result.ok, false, JSON.stringify(lastUsedByProject));
    assert.equal(result.code, REFUSAL.INVALID_LAST_USED, JSON.stringify(lastUsedByProject));
  }
  // The project key itself is an id, and a broken one is refused before the
  // store is read.
  for (const project of ['', '   ', 'a/b', 'a.b', 7, null, 'a'.repeat(MAX_ID_LENGTH + 1)]) {
    const result = defaultAgent({ project, agents: AGENTS });

    assert.equal(result.ok, false, JSON.stringify(project));
    assert.equal(result.code, REFUSAL.INVALID_PROJECT_ID, JSON.stringify(project));
  }
});

// ---------------------------------------------------------------------------
// Refusals: fail closed, never coerce
// ---------------------------------------------------------------------------

test('an utterance that is not a string is refused, never read as empty text', () => {
  for (const utterance of [undefined, null, 42, [], {}, true, ['Fugaro, list projects']]) {
    const result = routeUtterance({ utterance });

    assert.equal(result.ok, false, JSON.stringify(utterance));
    assert.equal(result.code, REFUSAL.UTTERANCE_NOT_A_STRING, JSON.stringify(utterance));
    assert.equal(Object.hasOwn(result, 'route'), false);
    assert.equal(Object.hasOwn(result, 'forwarded'), false);
  }
});

test('an utterance with no words in it is refused', () => {
  for (const utterance of ['', '   ', '\n\t', ' , . ', '...']) {
    const result = routeUtterance({ utterance });

    assert.equal(result.ok, false, JSON.stringify(utterance));
    assert.equal(result.code, REFUSAL.UTTERANCE_BLANK, JSON.stringify(utterance));
  }
});

test('an over-long utterance is refused, at the schema’s own bound', () => {
  assert.equal(routeUtterance({ utterance: 'a'.repeat(MAX_UTTERANCE_LENGTH) }).ok, true);
  const result = routeUtterance({ utterance: 'a'.repeat(MAX_UTTERANCE_LENGTH + 1) });

  assert.equal(result.ok, false);
  assert.equal(result.code, REFUSAL.UTTERANCE_TOO_LONG);
});

test('control characters and lone surrogates are refused, never routed', () => {
  assert.equal(routeUtterance({ utterance: 'Fugaro\u0007 list projects' }).code, REFUSAL.UTTERANCE_CONTROL_CHARACTER);
  assert.equal(routeUtterance({ utterance: 'Fugaro\u0000' }).code, REFUSAL.UTTERANCE_CONTROL_CHARACTER);
  assert.equal(routeUtterance({ utterance: 'Fugaro\u007f' }).code, REFUSAL.UTTERANCE_CONTROL_CHARACTER);
  assert.equal(routeUtterance({ utterance: `Fugaro ${LONE_SURROGATE}` }).code, REFUSAL.UTTERANCE_BAD_CHARACTER);
  // A line break is not a control character here: the text screen (P6.9) types
  // into the same pipeline, and P0.2's schema admits line breaks in text.
  assert.equal(routeUtterance({ utterance: 'Fugaro\nlist projects' }).route, ROUTE.APP_COMMAND);
});

test('the input is not a bag: an unknown field is refused', () => {
  const result = routeUtterance({ utterance: 'Fugaro, list projects', now: 1 });

  assert.equal(result.ok, false);
  assert.equal(result.code, REFUSAL.UNKNOWN_FIELD);
  assert.equal(result.field, 'now');
  assert.equal(defaultAgent({ project: 'p', agents: AGENTS, extra: 1 }).code, REFUSAL.UNKNOWN_FIELD);
});

test('the input must be a plain object', () => {
  for (const input of ['Fugaro, list projects', 42, null, [], new Date(0), () => {}]) {
    const result = routeUtterance(input);

    assert.equal(result.ok, false, String(input));
    assert.equal(result.code, REFUSAL.INPUT_NOT_AN_OBJECT, String(input));
    assert.equal(defaultAgent(input).code, REFUSAL.INPUT_NOT_AN_OBJECT, String(input));
  }
});

test('a refusal is frozen, carries a code, a field and a detail, and never a route', () => {
  const result = routeUtterance({ utterance: 42 });

  assert.equal(Object.isFrozen(result), true);
  assert.equal(typeof result.code, 'string');
  assert.equal(typeof result.detail, 'string');
  assert.ok(result.detail.length > 0);
  assert.equal(Object.hasOwn(result, 'route'), false);
  assert.equal(Object.hasOwn(result, 'forwarded'), false);
  assert.equal(result.ok, false);
});

test('the refusal codes are distinct non-empty strings', () => {
  const codes = Object.values(REFUSAL);

  assert.equal(new Set(codes).size, codes.length, 'two codes share a name');
  for (const code of codes) {
    assert.equal(typeof code, 'string');
    assert.ok(code.length > 0);
  }
});

test('every refusal code is reachable, and every one is pinned by this suite', () => {
  // A code no test can reach is a code no test defends, and a code that is
  // reachable but unpinned is one an edit could silently stop returning. The
  // sweep names the input for each, and the last assertion fails if REFUSAL
  // grows a code this table does not cover.
  const sweep = [
    [REFUSAL.INPUT_NOT_AN_OBJECT, () => routeUtterance('Fugaro, list projects')],
    [REFUSAL.UNKNOWN_FIELD, () => routeUtterance({ utterance: 'Fugaro, list projects', x: 1 })],
    [REFUSAL.UTTERANCE_NOT_A_STRING, () => routeUtterance({ utterance: null })],
    [REFUSAL.UTTERANCE_BLANK, () => routeUtterance({ utterance: ' , ' })],
    [REFUSAL.UTTERANCE_TOO_LONG, () => routeUtterance({ utterance: 'a'.repeat(MAX_UTTERANCE_LENGTH + 1) })],
    [REFUSAL.UTTERANCE_CONTROL_CHARACTER, () => routeUtterance({ utterance: 'Fugaro\u0007' })],
    [REFUSAL.UTTERANCE_BAD_CHARACTER, () => routeUtterance({ utterance: LONE_SURROGATE })],
    [REFUSAL.UTTERANCE_FORMAT_CHARACTER, () => routeUtterance({ utterance: 'Fugaro\u200b confirm' })],
    [REFUSAL.INVALID_ALIASES, () => route('Fugaro, list projects', { aliases: [] })],
    [REFUSAL.ALIAS_COLLIDES_WITH_CONFIRMATION, () => route('Fugaro, list projects', { aliases: ['yes'] })],
    [REFUSAL.INVALID_PROJECTS, () => route('Fugaro, list projects', { projects: 'Belong' })],
    [REFUSAL.INVALID_PROJECT, () => route('Fugaro, list projects', { projects: [{ id: 'a' }] })],
    [REFUSAL.INVALID_AGENTS, () => route('Fugaro, list projects', { agents: 1 })],
    [REFUSAL.INVALID_AGENT, () => route('Fugaro, list projects', { agents: [{ id: 'a' }] })],
    [REFUSAL.AMBIGUOUS_SPOKEN_NAME, () => route('Fugaro, switch to A', {
      projects: [{ id: 'a', spokenName: 'A' }, { id: 'b', spokenName: 'a' }],
    })],
    [REFUSAL.INVALID_PROJECT_ID, () => defaultAgent({ project: '', agents: AGENTS })],
    [REFUSAL.INVALID_LAST_USED, () => defaultAgent({ project: 'p', lastUsedByProject: { p: 1 }, agents: AGENTS })],
    [REFUSAL.NO_AGENTS_ADVERTISED, () => defaultAgent({ project: 'p', agents: [] })],
  ];

  for (const [code, run] of sweep) {
    const result = run();

    assert.equal(result.ok, false, code);
    assert.equal(result.code, code);
  }
  assert.deepEqual(
    new Set(sweep.map(([code]) => code)),
    new Set(Object.values(REFUSAL)),
    'a refusal code exists that this sweep does not exercise',
  );
});

// ---------------------------------------------------------------------------
// Purity: no clock, no state, no runtime
// ---------------------------------------------------------------------------

test('the same input twice returns equal results, and nothing is remembered', () => {
  const input = { utterance: 'Fugaro, switch to Belong', projects: PROJECTS, agents: AGENTS };
  const first = routeUtterance(input);
  // A different call in between must not change the answer: there is no state.
  routeUtterance({ utterance: 'Fugaro, talk to front agent', projects: PROJECTS, agents: AGENTS });
  const second = routeUtterance(input);

  assert.deepEqual(second, first);
  assert.notEqual(second, first, 'each call returns its own frozen result');
  assert.equal(Object.isFrozen(first), true);
  assert.equal(Object.isFrozen(second), true);
});

test('the caller’s objects are never mutated, and never handed back', () => {
  const entry = { id: 'belong', spokenName: 'Belong' };
  const projects = [entry];
  const result = routeUtterance({ utterance: 'Fugaro, switch to Belong', projects });

  assert.equal(Object.isFrozen(entry), false, 'this module does not freeze what it was handed');
  assert.notEqual(result.project, entry, 'the result carries a copy');
  assert.deepEqual(result.project, { id: 'belong', spokenName: 'Belong' });
  assert.equal(Object.isFrozen(result.project), true);

  // A frozen input works too: nothing here writes to it.
  const frozen = Object.freeze({ utterance: 'Fugaro, list projects', projects: Object.freeze([Object.freeze({ ...entry })]) });
  assert.equal(routeUtterance(frozen).route, ROUTE.APP_COMMAND);
});

test('the module is a pure function: no import, no clock, no randomness, no runtime', () => {
  // D126 asks for a pure function wherever one is possible, and this is that
  // claim as a check rather than a comment. The module imports nothing (the
  // package's contract: no runtime, no database, no network client) and reads no
  // clock and no random source, so its result is a function of its input alone.
  const source = readFileSync(new URL('./index.js', import.meta.url), 'utf8');
  const forbidden = [
    [/^\s*import[\s{]/m, 'an import statement'],
    [/\brequire\s*\(/, 'require()'],
    [/new\s+Date\b/, 'a clock of its own'],
    [/Date\s*\.\s*now/, 'a clock of its own'],
    [/Math\s*\.\s*random/, 'a random source'],
    [/\bperformance\s*\./, 'a clock of its own'],
    [/\bfetch\s*\(/, 'a network client'],
    [/\bsetTimeout\s*\(/, 'a timer'],
    [/\bsetInterval\s*\(/, 'a timer'],
    [/\bprocess\s*\./, 'a process handle'],
    [/\bglobalThis\b/, 'a global handle'],
  ];

  for (const [pattern, what] of forbidden) {
    assert.equal(pattern.test(source), false, `shared/command/index.js must not reach for ${what}`);
  }
});
