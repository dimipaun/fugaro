// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The launch's value rules and its reply decoder (docs/design/desktop-console.md
// §5.1 rule 11): what may become a word of the child's argv, what may be drawn,
// and what the CLI's reply may say.
//
// Nothing here is reachable from the renderer in this change (V2.5.1a part 1):
// the module is imported by this test alone, and the operation that will call it
// (`run.launch`, the frozen spawn, the confirmation window) is part 2's. What
// the tests pin is the rules themselves, so that part 2 has nothing left to
// decide about them.

import { describe, expect, it } from 'vitest'

import {
  MAX_LAUNCH_PLAN_TASK_BYTES,
  MAX_LAUNCH_RESULT_STRING,
  MAX_LAUNCH_TASK_BYTES,
  MAX_LAUNCH_TITLE_RUNES,
  checkLaunchPlanTask,
  checkLaunchTask,
  checkLaunchTitle,
  decodeLaunchResult,
  displayTaskText,
  launchFingerprint,
  launchRepoArgument,
  recordedBaseBranch,
} from '../../src/main/launchrules'
import type { LaunchRecordFacts } from '../../src/main/launchrules'

/** value is a rule's admitted value, or a failure naming the refusal -- so a
 * test that expected a value and got a refusal says so rather than reporting an
 * `undefined`. */
function value<T>(answer: { ok: true; value: T } | { ok: false; reason: string }): T {
  if (!answer.ok) throw new Error(`refused: ${answer.reason}`)
  return answer.value
}

/** reason is a rule's refusal sentence, or a failure when it admitted the
 * value. */
function reason(answer: { ok: true; value: unknown } | { ok: false; reason: string }): string {
  if (answer.ok) throw new Error('the rule admitted a value it should have refused')
  return answer.reason
}

describe('the record a launch is cut from', () => {
  /** RECORD is one installation's facts, in the shape the entry point reads
   * them off its own file and the rail's row. Every field carries a value a
   * change to it could be told apart from, so a field the fingerprint had
   * dropped or renamed cannot hide behind two equal values. */
  const RECORD: LaunchRecordFacts = {
    id: 'record-1',
    project: 'sandbox',
    binaryPath: '/usr/local/bin/fugaro',
    distro: 'Ubuntu',
    configPath: '/home/person-1/.config/fugaro/config.yaml',
    checkout: '/home/person-1/src/app',
    saved: true,
  }

  it('changes for every field that changes what a run for that name would do', () => {
    // Each fact is changed on its own, and the fingerprint must differ: a field
    // dropped from the string, or renamed in the object it reads, would make
    // the answer-time comparison stop noticing that thing moving -- silently,
    // because the frozen string is compared against a fresh computation of
    // itself, so no other test and no source scan can see it.
    const whole = launchFingerprint(RECORD)
    const moved: readonly [string, Partial<LaunchRecordFacts>][] = [
      ['the record id', { id: 'record-2' }],
      ['the project', { project: 'staging' }],
      ['the binary path', { binaryPath: '/opt/homebrew/bin/fugaro' }],
      ['the distribution', { distro: 'Debian' }],
      ['the config path', { configPath: '/home/person-1/other.yaml' }],
      ['the checkout', { checkout: '/home/person-1/src/other' }],
      ['the rail’s saved flag', { saved: false }],
    ]
    for (const [what, change] of moved) {
      expect(launchFingerprint({ ...RECORD, ...change }), what).not.toBe(whole)
    }
  })

  it('is the same string for the same record, and its own string for an absent one', () => {
    // The comparison's other direction: two reads of one unchanged record are
    // one identity, or every launch would decline. The empty facts are what the
    // entry point reads for a name its own file no longer carries (`saved:
    // false`), and they must not be this record's identity.
    expect(launchFingerprint({ ...RECORD })).toBe(launchFingerprint(RECORD))
    const gone: LaunchRecordFacts = {
      id: '',
      project: '',
      binaryPath: '',
      distro: '',
      configPath: '',
      checkout: '',
      saved: false,
    }
    expect(launchFingerprint(gone)).not.toBe(launchFingerprint(RECORD))
  })
})

describe('the task text', () => {
  it('trims the way the CLI trims, and sends the trimmed bytes', () => {
    // `internal/cli/run.go`'s `taskText`: `strings.TrimSpace`, then a refusal
    // when the result is empty. The bytes the window counts are then the bytes
    // the run keeps.
    const answer = value(checkLaunchTask('\n\t  do the thing\u00a0\n '))
    expect(answer.text).toBe('do the thing')
    expect([...answer.bytes]).toEqual([...new TextEncoder().encode('do the thing')])
    // U+0085 is whitespace to Go and not to JavaScript's `trim()`, and U+FEFF
    // is the other way round: both are pinned here, because a trim that used
    // `String.prototype.trim` would cut one and keep the other.
    expect(value(checkLaunchTask('\u0085do the thing\u0085')).text).toBe('do the thing')
    expect(value(checkLaunchTask('\ufeffdo the thing\ufeff')).text).toBe('\ufeffdo the thing\ufeff')
  })

  it('refuses a task that is empty once trimmed, including one that is only whitespace', () => {
    for (const text of ['', ' ', '\n\t\r', '\u00a0\u2000\u3000\u0085']) {
      expect(reason(checkLaunchTask(text)), JSON.stringify(text)).toContain('empty')
    }
  })

  it('counts the cap in bytes of UTF-8, after the trim', () => {
    // 16384 bytes pass and 16385 are refused, and the trim is what makes the
    // difference: the padded text is over the cap and the trimmed one is not.
    const at = 'a'.repeat(MAX_LAUNCH_TASK_BYTES)
    expect(value(checkLaunchTask(at)).bytes.length).toBe(MAX_LAUNCH_TASK_BYTES)
    expect(reason(checkLaunchTask(`${at}a`))).toContain(String(MAX_LAUNCH_TASK_BYTES))
    expect(value(checkLaunchTask(`\n${at}\n`)).bytes.length).toBe(MAX_LAUNCH_TASK_BYTES)
    // A multi-byte character counts as its bytes and not as one: 8192 of them
    // are exactly the cap, and one more is over it.
    const wide = 'é'.repeat(MAX_LAUNCH_TASK_BYTES / 2)
    expect(value(checkLaunchTask(wide)).bytes.length).toBe(MAX_LAUNCH_TASK_BYTES)
    expect(reason(checkLaunchTask(`${wide}é`))).toContain(String(MAX_LAUNCH_TASK_BYTES))
  })

  it('refuses a NUL byte and text that is not valid UTF-8', () => {
    // A lone surrogate is not valid UTF-8: it is refused rather than encoded
    // as U+FFFD, which would send the run a text the operator never wrote.
    expect(reason(checkLaunchTask('do \u0000 the thing'))).toContain('NUL')
    expect(reason(checkLaunchTask('do \ud800 the thing'))).toContain('UTF-8')
    expect(reason(checkLaunchTask('do \udfff the thing'))).toContain('UTF-8')
    // A *pair* is a character, and both halves are ordinary text.
    expect(value(checkLaunchTask('do 😀 the thing')).text).toBe('do 😀 the thing')
  })
})

describe('the title and the plan task', () => {
  it('allows either to be absent, which is not an empty value', () => {
    for (const absent of [undefined, null]) {
      expect(value(checkLaunchTitle(absent))).toBeNull()
      expect(value(checkLaunchPlanTask(absent))).toBeNull()
    }
    // Present and empty is a mistake the CLI refuses too (its `--title` with
    // nothing in it), and it is not the same thing as absent.
    expect(reason(checkLaunchTitle(''))).toContain('empty')
    expect(reason(checkLaunchPlanTask(''))).toContain('empty')
  })

  it('allows the space and the leading dash that the value alphabet refuses', () => {
    // The whole reason the *title* has a kind of its own:
    // `isSafeArgumentValue`'s `ARGUMENT_RE` and its leading-`-` check refuse
    // both, and a title may hold both. `spawn.ts` is untouched by this change.
    for (const title of ['a title with spaces', '-a title that starts like a flag', 'release 2.0: the fix']) {
      expect(value(checkLaunchTitle(title)), title).toBe(title)
    }
    // The plan task does **not** get to hold either: it is a shape and not
    // prose (`task.PlanTaskProblem`, the list the launcher and the runner both
    // apply), and its elements are `PlanTaskElemRE`'s alphabet.
    expect(reason(checkLaunchPlanTask('a title with spaces'))).toContain('as <plan>/<task>')
    expect(reason(checkLaunchPlanTask('a/title with spaces'))).toContain('where letters, digits')
    expect(reason(checkLaunchPlanTask('-a title that starts like a flag'))).toContain('as <plan>/<task>')
  })

  it('refuses a title that is nothing but whitespace, the way the CLI does', () => {
    // `titleFlag` trims the flag and `task.TitleProblem` refuses what is left
    // ("is empty (or nothing but whitespace)"), so a whitespace-only title is a
    // refusal *after* the window, which is what this catches first.
    for (const blank of [' ', '\t', '\u00a0', '\u3000', '\u0085']) {
      expect(reason(checkLaunchTitle(blank)), JSON.stringify(blank)).toContain('whitespace')
    }
    // A title with whitespace around it is not empty: it is the trimmed value
    // that the CLI stores, and that is what comes back.
    expect(value(checkLaunchTitle('  the fix \n'))).toBe('the fix')
    expect(value(checkLaunchPlanTask('  plan/task '))).toBe('plan/task')
  })

  it('refuses a title that is nothing but invisible characters, with the CLI’s own sentence', () => {
    // The CLI checks emptiness **twice**: `task.TitleProblem` on the trimmed
    // flag ("is empty (or nothing but whitespace)"), and again after
    // `safetext.Field` has dropped what it drops -- "is empty (or nothing but
    // whitespace and invisible characters)". These values pass the first check
    // and are emptied by the second, so a rule that mirrored only the first
    // would admit a title the CLI refuses *after* the operator confirmed the
    // window, which is the failure this module exists to prevent.
    //
    // The characters below are the half of `safetext`'s spoofing set that is
    // **not** `\p{Cf}`, so the format-character rule does not see them: the
    // Hangul fillers U+3164 and U+FFA0, U+115F and U+1160, the grapheme joiner
    // U+034F, the Khmer and variation selectors.
    for (const invisible of ['\u3164', '\uffa0', '\u115f', '\u1160', '\u034f', '\ufe0f', '\u{e0100}']) {
      expect(reason(checkLaunchTitle(invisible)), JSON.stringify(invisible)).toContain('invisible characters')
    }
    // Whitespace around them does not save the value: the trim happens first
    // and the rewrite then empties what is left.
    expect(reason(checkLaunchTitle(' \u3164\ufe0f '))).toContain('invisible characters')
  })

  it('returns the title the CLI would store, not the one the operator typed', () => {
    // `titleFlag` stores `safetext.Field(title)`, so the run keeps neither the
    // run of spaces nor the characters that rewrite drops. What this rule
    // returns is what the window is drawn from, so it is the rewritten value:
    // a window that showed "a  b" for a run that keeps "a b" would be showing
    // a value the argv does not carry.
    expect(value(checkLaunchTitle('a  b'))).toBe('a b')
    expect(value(checkLaunchTitle('the \u2003 fix'))).toBe('the fix')
    expect(value(checkLaunchTitle('a\u00a0b'))).toBe('a b')
    expect(value(checkLaunchTitle('a\u3000\u2009b'))).toBe('a b')
    // Inside prose these characters are dropped, not refused -- that is
    // `Field`'s own treatment, and the value the run stores has them gone. The
    // format characters are the ones the console refuses instead (the headline
    // rule above), because those are the ones a reader cannot see at all.
    expect(value(checkLaunchTitle('a\u3164b'))).toBe('ab')
    expect(value(checkLaunchTitle('a\ufe0fb'))).toBe('ab')
    // The rewrite happens **after** the cap, in the CLI's own order
    // (`task.TitleProblem` then `safetext.Field`): 100 characters plus a
    // variation selector is 101 to the CLI and is refused, not silently cut to
    // the cap by the rewrite.
    const titleAt = 'a'.repeat(MAX_LAUNCH_TITLE_RUNES)
    expect(reason(checkLaunchTitle(`${titleAt}\ufe0f`))).toContain(String(MAX_LAUNCH_TITLE_RUNES))
    expect(value(checkLaunchTitle(`${titleAt.slice(0, MAX_LAUNCH_TITLE_RUNES - 1)}\ufe0f`))).toHaveLength(
      MAX_LAUNCH_TITLE_RUNES - 1,
    )
  })

  it('holds a plan task to the CLI’s own shape, not to the title’s prose rules', () => {
    // task.PlanTaskProblem: `<plan>/<task>`, at least two elements, none empty,
    // "." or "..", each in PlanTaskElemRE. A value it refuses is a launch the
    // CLI would fail after the operator confirmed the window.
    for (const good of ['plan/task', 'plan-a/task.b', 'p1/t2/t3', '2026-10-28/fix_1']) {
      expect(value(checkLaunchPlanTask(good)), good).toBe(good)
    }
    const bads: readonly [string, string][] = [
      ['plan', 'as <plan>/<task>'],
      ['a title with spaces', 'as <plan>/<task>'],
      ['a/title with spaces', 'where letters, digits'],
      ['/plan/task', 'absolute path'],
      ['plan/', 'empty path element'],
      ['plan//task', 'empty path element'],
      ['plan/./task', 'path element'],
      ['plan/../task', 'path element'],
      ['plan/task a', 'where letters, digits'],
      ['plan/-task', 'where letters, digits'],
      ['plan/.hidden', 'where letters, digits'],
      ['plan/task#1', 'where letters, digits'],
      ['plan/tas\u202ek', 'format character'],
    ]
    for (const [bad, sentence] of bads) {
      expect(reason(checkLaunchPlanTask(bad)), JSON.stringify(bad)).toContain(sentence)
    }
  })

  it('refuses a title or a plan task that is not valid UTF-8, like the task text', () => {
    // A lone surrogate is not a character: `TextEncoder` would send U+FFFD in
    // its place, so the value in the window would not be the value in the argv
    // (task.TitleProblem refuses it too, as "is not valid UTF-8").
    for (const lone of ['fix \ud800 it', 'fix \udfff it']) {
      expect(reason(checkLaunchTitle(lone)), JSON.stringify(lone)).toContain('UTF-8')
      expect(reason(checkLaunchPlanTask(lone)), JSON.stringify(lone)).toContain('UTF-8')
    }
    expect(value(checkLaunchTitle('fix 😀 it'))).toBe('fix 😀 it')
  })

  it('refuses a title or plan task carrying a bidi override or a zero-width joiner, not stripping it', () => {
    // **The headline rule, and the one mutation proof of this change.** The CLI
    // would drop these characters (`internal/safetext`'s `unicode.Is(unicode.Cf,
    // r)`), so a value that passed here would be a value the window showed and
    // the run never kept. The console refuses on its side instead.
    //
    // Mutation (run, restore): delete the `FORMAT_RE` line from
    // `textCharacterProblem` and this test fails on every case below -- the
    // titles come back through as fine, and the plan tasks come back with the
    // shape's sentence ("where letters, digits ...") instead. It is the one
    // mutation of this change, and it fails exactly two tests: this one and the
    // shape test's own format-character row.
    for (const hostile of [
      'fix \u202egnp.exe', // a right-to-left override: reverses the line a reader sees
      'fix \u202dgnp.exe', // a left-to-right override, the same family
      'fix\u200bbroken', // a zero-width space
      'fix\u200cbroken', // a zero-width non-joiner
      'fix\u200dbroken', // a zero-width joiner
      'fix\u2066broken\u2069', // an isolate, which hides a run of text from the surrounding text's direction
      '\ufefffix', // a byte-order mark in the middle of a value
    ]) {
      expect(reason(checkLaunchTitle(hostile)), JSON.stringify(hostile)).toContain('format character')
      expect(reason(checkLaunchPlanTask(hostile)), JSON.stringify(hostile)).toContain('format character')
    }
    // And the refusal is a refusal, not a strip: what comes back is a sentence
    // and never the text with the character removed.
    expect(reason(checkLaunchTitle('fix \u202egnp.exe'))).not.toContain('gnp.exe')
  })

  it('checks a megabyte of whitespace inside the text in the time a window has', () => {
    // **The trim must not be quadratic.** Every rule here runs on whatever a
    // renderer hands it, in the Electron main process, and the three checks that
    // trim first (`checkLaunchTask`, and the title and the plan task before
    // their caps) all read the value once. The trailing run of whitespace *inside*
    // a long text is what a regular expression anchored at the end costs the
    // most: the engine tries every offset of the text, and one such check
    // measured 0.77 s at 32 KiB and 13.5 s at 128 KiB, so a megabyte is minutes
    // -- a window that does not answer, before any of this was about the argv.
    // The index scan is linear.
    //
    // Mutation (run, restore): put the anchored regular expression back into
    // `trimTaskText` and the same check does not return within minutes -- and a
    // synchronous call cannot be timed out either, so the test would hang rather
    // than fail.
    const megabyte = `do the thing${' '.repeat(1024 * 1024 - 24)}and now`
    const started = performance.now()
    expect(reason(checkLaunchTask(megabyte))).toContain(String(MAX_LAUNCH_TASK_BYTES))
    expect(reason(checkLaunchTitle(megabyte))).toContain(String(MAX_LAUNCH_TITLE_RUNES))
    expect(reason(checkLaunchPlanTask(megabyte))).toContain(String(MAX_LAUNCH_PLAN_TASK_BYTES))
    // A generous bound on purpose: this is a wall-clock assertion about a
    // linear scan on a loaded machine, so it is set where a *quadratic* check
    // (minutes, per the measurement above) cannot pass and a slow machine still
    // does -- a tighter number here would fail for reasons that have nothing to
    // do with the rule.
    expect(performance.now() - started).toBeLessThan(1000)
  })

  it('refuses a second line, a control character and a value past its cap', () => {
    // U+0085 is a line break to a title (task.TitleProblem names it beside the
    // newline and the carriage return) and a control character to a plan task;
    // both refuse it.
    for (const multi of ['one\ntwo', 'one\rtwo', 'one\u0085two', 'one\u2028two', 'one\u2029two']) {
      expect(reason(checkLaunchTitle(multi)), JSON.stringify(multi)).toContain('more than one line')
    }
    expect(reason(checkLaunchPlanTask('plan/one\u0085two'))).toContain('more than one line')
    for (const control of ['one\ttwo', 'one\u0000two', 'one\u009ftwo']) {
      expect(reason(checkLaunchTitle(control)), JSON.stringify(control)).toContain('control character')
    }
    const titleAt = 'a'.repeat(MAX_LAUNCH_TITLE_RUNES)
    expect(value(checkLaunchTitle(titleAt))).toBe(titleAt)
    expect(reason(checkLaunchTitle(`${titleAt}a`))).toContain(String(MAX_LAUNCH_TITLE_RUNES))
    // The title's cap is in characters and the plan task's in bytes, and the
    // difference is the point: they are two different kinds of word. The plan
    // task's cap is measured on a value that is a shape ('p/' is two bytes of
    // it), because the shape is checked after the cap.
    expect(value(checkLaunchTitle('é'.repeat(MAX_LAUNCH_TITLE_RUNES)))).toHaveLength(MAX_LAUNCH_TITLE_RUNES)
    const planAt = `p/${'a'.repeat(MAX_LAUNCH_PLAN_TASK_BYTES - 2)}`
    expect(value(checkLaunchPlanTask(planAt))).toBe(planAt)
    expect(reason(checkLaunchPlanTask(`${planAt}a`))).toContain(String(MAX_LAUNCH_PLAN_TASK_BYTES))
    // The plan task's cap is in bytes and its elements are ASCII
    // (`PlanTaskElemRE`), so for an id the shape would admit the two counts are
    // the same number -- and the cap is checked before the shape, exactly as
    // task.PlanTaskProblem orders them: a value over it in bytes is refused
    // whether or not its shape could have been read.
    expect(reason(checkLaunchPlanTask('é'.repeat(65)))).toContain(String(MAX_LAUNCH_PLAN_TASK_BYTES))
  })
})

describe('the task the window draws', () => {
  it('strips the format characters from the displayed copy and nothing else', () => {
    // The task itself is not refused for carrying them -- it travels on stdin
    // and the CLI sanitises what it stores -- but the paragraph a person reads
    // before spending must not be reorderable.
    const task = 'fix the \u202edirection\u202c of \u200bthe arrow'
    expect(displayTaskText(task)).toBe('fix the direction of the arrow')
    // A control character is shown as it is: this is not the title's rule, and
    // the window's own text classes are what draw an unnamed control character.
    expect(displayTaskText('one\ttwo')).toBe('one\ttwo')
    expect(displayTaskText('😀')).toBe('😀')
  })
})

describe('the repository argument', () => {
  it('drops the provider segment and keeps the owner/name', () => {
    expect(value(launchRepoArgument('github/owner/repo-a'))).toBe('owner/repo-a')
    expect(value(launchRepoArgument('bitbucket/owner/repo-a'))).toBe('owner/repo-a')
  })

  it('refuses anything that is not a provider and a canonical owner/name', () => {
    for (const recorded of [
      'gitlab/owner/repo-a', // a provider this tree does not serve
      'GitHub/owner/repo-a', // the same provider, spelled as the record never is
      'github/owner', // no name
      'github/owner/repo-a/extra', // three segments: a path, not a repository
      'github/../repo-a', // `..` is a path the CLI would resolve elsewhere
      'github/owner/..',
      'github/owner/.',
      'github//repo-a',
      'github/owner/repo a', // outside the CLI's alphabet
      'owner/repo-a',
      '',
    ]) {
      expect(reason(launchRepoArgument(recorded)), JSON.stringify(recorded)).not.toBe('')
    }
  })

  it('refuses a name ending in .git and a segment that begins like a flag', () => {
    // **A `.git` name is a different repository from the one the window showed.**
    // `task.CanonicalRepo` lowers the identity and trims a trailing `.git`, so
    // `--repo owner/repo-a.git` starts a run on `owner/repo-a` -- the console
    // refuses rather than pass an identity whose canonical form is another
    // string (rule 11: what the window shows is what is spawned).
    for (const dotGit of ['github/owner/repo-a.git', 'github/owner/repo-a.GIT', 'github/owner/.git']) {
      expect(reason(launchRepoArgument(dotGit)), dotGit).not.toBe('')
    }
    // Only the trailing segment is stripped by that canonical form, so an owner
    // that merely ends in `.git` is a name the CLI would keep, and the two
    // segments are held to their own rules.
    expect(value(launchRepoArgument('github/owner.git/repo-a'))).toBe('owner.git/repo-a')
    // A segment beginning with `-` is refused even though `task`'s own class
    // admits it: the value this returns is a word of a command line, and a word
    // that begins with a hyphen is a flag to whatever reads it next.
    for (const flagLike of ['github/-owner/repo-a', 'github/owner/-repo-a', 'github/owner/-']) {
      expect(reason(launchRepoArgument(flagLike)), flagLike).not.toBe('')
    }
    // And the refusal quotes the recorded spelling rather than a stripped one:
    // the sentence says which identity was refused, and never `owner/repo-a`.
    expect(reason(launchRepoArgument('github/owner/repo-a.git'))).toContain('owner/repo-a.git')
  })
})

describe('the recorded base branch', () => {
  const installation = {
    baseBranch: 'trunk',
    repositories: [
      { repository: 'github/owner/repo-a', baseBranch: 'dev' },
      { repository: 'github/owner/repo-b', baseBranch: '' },
    ],
  }

  it('reads the repository’s own value, and the installation’s below it', () => {
    expect(value(recordedBaseBranch(installation, 'github/owner/repo-a'))).toBe('dev')
    // An empty row falls through to the installation's own branch rather than
    // answering with the empty string.
    expect(value(recordedBaseBranch(installation, 'github/owner/repo-b'))).toBe('trunk')
  })

  it('refuses with the repository and the two settings, never git.base_branch', () => {
    const sentence = reason(recordedBaseBranch({ baseBranch: '', repositories: [] }, 'github/owner/repo-a'))
    expect(sentence).toContain('github/owner/repo-a')
    expect(sentence).toContain('repos.owner/repo-a.base_branch')
    expect(sentence).toContain("the project's own base_branch")
    // The one setting this must never send an operator to: it is a checkout's
    // file, and moving it does not move the launch.
    expect(sentence).not.toContain('git.base_branch')
    expect(reason(recordedBaseBranch({ baseBranch: '', repositories: [] }, 'gitlab/owner/repo-a'))).not.toBe('')
  })

  it('has its own sentence for a CLI whose document reports no base branches', () => {
    // **`null` is not "records none".** `base_branch` arrives in
    // `config list --json` with its own release, so an older CLI's document has
    // no such key; answering "the installation records no base branch ... set
    // `repos.<r>.base_branch`" to that operator sends them to a setting they
    // have already set, about a config that is not the problem. §6.8 keeps the
    // two apart and so does this.
    const older = { baseBranch: null, repositories: [{ repository: 'github/owner/repo-a', baseBranch: null }] }
    const sentence = reason(recordedBaseBranch(older, 'github/owner/repo-a'))
    expect(sentence).toContain('does not report base branches')
    expect(sentence).toContain('github/owner/repo-a')
    expect(sentence).not.toContain('repos.owner/repo-a.base_branch')
    // Either key alone being absent is that same state: a document that does not
    // report branches cannot say which branch this repository would be cut from.
    expect(reason(recordedBaseBranch({ ...older, baseBranch: 'trunk' }, 'github/owner/repo-a'))).toContain(
      'does not report base branches',
    )
    expect(
      reason(recordedBaseBranch({ baseBranch: null, repositories: [] }, 'github/owner/repo-a')),
    ).toContain('does not report base branches')
    // A row that records none is still the ordinary "unrecorded" refusal, and an
    // installation that records a branch still answers with it.
    const mixed = { baseBranch: 'trunk', repositories: [{ repository: 'github/owner/repo-a', baseBranch: '' }] }
    expect(value(recordedBaseBranch(mixed, 'github/owner/repo-a'))).toBe('trunk')
  })
})

describe('the launch reply', () => {
  const REPLY = {
    project: 'sandbox',
    run: 'run-1',
    repo: 'owner/repo-a',
    run_id: '20261028-120350-5b9b',
    branch: 'fugaro/20261028-120350-5b9b',
    ref: 'main',
    execution: 'fugaro-run-abc',
    log_url: 'https://example.invalid/logs/1',
    status: 'launched',
  }

  it('reads the values the CLI printed, and the launched status as a fact', () => {
    const answer = value(decodeLaunchResult(JSON.stringify(REPLY)))
    expect(answer.run).toBe('run-1')
    expect(answer.runId).toBe('20261028-120350-5b9b')
    expect(answer.branch).toBe('fugaro/20261028-120350-5b9b')
    expect(answer.ref).toBe('main')
    expect(answer.status).toBe('launched')
    // The optional keys of `launchResult` are read as absence, not as a refusal.
    expect(answer.pr).toBeNull()
    expect(answer.recipe).toBeNull()
    expect(answer.projectLayer).toBeNull()
    expect(answer.previousRun).toBe('')
    expect(value(decodeLaunchResult(JSON.stringify({ ...REPLY, status: 'already-launched' }))).status).toBe(
      'already-launched',
    )
    // The two shapes only a follow-up or a planned run carries.
    const full = value(
      decodeLaunchResult(
        JSON.stringify({
          ...REPLY,
          pr: 12,
          previous_run: 'run-0',
          recipe: { name: 'go', source: 'repo' },
          project_layer: { sha256: 'a'.repeat(64), generation: 3 },
        }),
      ),
    )
    expect(full.pr).toBe(12)
    expect(full.previousRun).toBe('run-0')
    expect(full.recipe).toEqual({ name: 'go', source: 'repo' })
    expect(full.projectLayer).toEqual({ sha256: 'a'.repeat(64), generation: 3 })
  })

  it('refuses a reply carrying a key it does not know', () => {
    // Rule 11: a field the decoder does not know refuses the whole reply rather
    // than being ignored -- the values here are drawn as the CLI's own facts.
    const answer = decodeLaunchResult(JSON.stringify({ ...REPLY, invented: 'x' }))
    expect(answer.ok).toBe(false)
    expect(reason(answer)).toContain('invented')
    // Nested as well: the two objects the CLI emits are closed too.
    expect(decodeLaunchResult(JSON.stringify({ ...REPLY, recipe: { name: 'go', invented: 'x' } })).ok).toBe(false)
    expect(
      decodeLaunchResult(JSON.stringify({ ...REPLY, project_layer: { sha256: 'x', invented: 'x' } })).ok,
    ).toBe(false)
  })

  it('refuses a reply that is not the reply, rather than drawing part of it', () => {
    const bads: readonly [string, string][] = [
      ['not JSON at all', 'fugaro: this CLI has no run command\n'],
      ['not an object', '[1,2,3]'],
      ['an array', '[]'],
      ['no status', JSON.stringify({ ...REPLY, status: undefined })],
      ['another status word', JSON.stringify({ ...REPLY, status: 'ok' })],
      ['no branch', JSON.stringify({ ...REPLY, branch: undefined })],
      ['a branch that is a number', JSON.stringify({ ...REPLY, branch: 7 })],
      ['a run that is an object', JSON.stringify({ ...REPLY, run: {} })],
      ['a pr that is a string', JSON.stringify({ ...REPLY, pr: '12' })],
      ['a pr that is negative', JSON.stringify({ ...REPLY, pr: -1 })],
      ['a recipe that is a string', JSON.stringify({ ...REPLY, recipe: 'go' })],
      ['a recipe with no source', JSON.stringify({ ...REPLY, recipe: { name: 'go' } })],
      ['a project layer with no sha', JSON.stringify({ ...REPLY, project_layer: { generation: 1 } })],
      ['a string past the bound', JSON.stringify({ ...REPLY, branch: 'b'.repeat(MAX_LAUNCH_RESULT_STRING + 1) })],
    ]
    for (const [what, text] of bads) {
      const answer = decodeLaunchResult(text)
      expect(answer.ok, what).toBe(false)
      expect(reason(answer), what).not.toBe('')
    }
  })
})
