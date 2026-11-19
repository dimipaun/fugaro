// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Copy-command builder: the one way a Manage kind puts a command on the
// clipboard (the console-improvements plan's task MG0; spec section 5 as D2903
// amended it; the prose is `docs/console-improvements/manage-kinds.md`).
//
// **Why a builder rather than a string.** D2903 keeps every write that is secret
// or destructive out of the console and gives the operator the exact command to
// run instead. The words of that command are not all the console's: a member's
// address, an account's name and a repository's name come from a document the
// CLI answered, and a document's string inside a command line is a shell's
// syntax until something makes it not be one. So a command is **assembled** here
// rather than formatted at each call site: every word declares whether it is the
// console's own or a value from a document, a value is admitted only in the
// narrow alphabet below, and a word that does not fit **refuses the whole
// action** -- it is never quoted, never escaped and never drawn.
//
// **Refused, not quoted, and this is on purpose.** Quoting is a second
// implementation of the shell's own grammar, and one that has to be right about
// a language nobody here owns (`.claude/rules/console.md` says the same for the
// WSL route: `distroShellUnsafe` refuses a word with a metacharacter, and
// `shellQuoteWord` is the shell layer's job rather than the plan builder's). A
// copy button has a cheaper answer than getting it right: the values a Manage
// kind copies are an address, a role, a repository, an account and a key's name,
// and every one of those is a plain word -- so a value that is not one is a value
// this console will not copy, and the row is drawn without its Copy rather than
// with a command that could be a second command. The operator still has the
// command's own name to type: a refusal costs a copy, never a wrong line.
//
// **The end of the line is a word with no space in it.** The action carries the
// argv (`types.ts`'s `CopyCommandAction`), and the text put on the clipboard is
// that argv joined with single spaces -- which is unambiguous exactly because no
// admitted word can hold a space, a tab or a newline. `buildCopyCommand` is the
// only door to an action and this file is the only place the join is spelled, so
// no caller has to decide how to quote; the shell copies `argv.join(' ')`.

import type { CopyCommandAction } from './types'

/**
 * VALUE_PATTERN is the alphabet a value from a document may be drawn from:
 * one letter or digit, then letters, digits and `.`, `_`, `/`, `@`, `:`, `=`,
 * `+`, `-`. It admits every value a Manage kind copies -- `ana@example.invalid`,
 * `dimipaun/fugaro`, `2026-11-18T10:00:00Z`, a role's `roles/datastore.viewer`,
 * a key's `openrouter-main` -- and it admits nothing a shell reads as syntax:
 * no space, no tab, no newline, no `;`, no `|`, no `&`, no `$`, no backtick, no
 * quote, no `*`, no `?`, no `~`, no `<`, no `>`.
 *
 * **The first character is a letter or a digit**, which is the half of the rule
 * that keeps a value from being read as a flag: a value that opened with `-`
 * would be the command's own option the moment the operator pressed Enter, and
 * the console would have handed a document the power to choose one. `=` `@` and
 * `~` are refused as openers for the same reason they are not words -- `=` leads
 * a shell assignment, `~` a home directory and `@` nothing at all -- and they are
 * still ordinary characters *inside* a word, where `--project=x` and
 * `ana@example.invalid` need them.
 */
const VALUE_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._/@:=+-]*$/

/**
 * FLAG_PATTERN is `VALUE_PATTERN` with one allowance: a word the console wrote
 * itself may **open with one or two dashes**, because the console's own words
 * include the CLI's flags (`--project`, `--no-apply`, `-v`) and a flag is a word
 * no document supplied. It is the same alphabet otherwise, so a "fixed" word
 * built by string concatenation out of a document's string -- `--repo=` + name --
 * is refused exactly as a value would be, and the `..` rule below applies to it
 * too.
 */
const FLAG_PATTERN = /^-{0,2}[A-Za-z0-9][A-Za-z0-9._/@:=+-]*$/

/** OPENER_PATTERN is the first character both alphabets require -- a letter or a
 * digit -- read on its own, because it is the one failure the refusal tells
 * apart: a value that opens with `-`, `=`, `@` or `~` is a value a shell could
 * read as something other than a word. */
const OPENER_PATTERN = /^[A-Za-z0-9]/

/**
 * CommandPart is one word of a command being built: `word` is the console's own
 * text (a command name, a subcommand, a flag) and `value` is a string from a
 * document, held to the narrower alphabet above. The two are told apart at every
 * call site rather than inferred, because "is this string a document's?" is the
 * whole question a copy-command has to answer, and a builder that guessed would
 * be the place a document's string got in.
 */
export interface CommandPart {
  readonly kind: 'word' | 'value'
  readonly text: string
}

/** commandWord is a word the console wrote itself: `fugaro`, `user`, `grant`,
 * `--project`. */
export function commandWord(text: string): CommandPart {
  return { kind: 'word', text }
}

/** commandValue is a value a document supplied: a name, an address, a role, a
 * repository. */
export function commandValue(text: string): CommandPart {
  return { kind: 'value', text }
}

/** CopyCommandAnswer is the builder's answer: the action to copy, or the reason
 * the console draws no Copy at all. A refusal is an ordinary outcome -- the
 * value was a value the console will not copy -- and the caller draws the row
 * without its Copy rather than a command it had to repair. */
export type CopyCommandAnswer =
  | { readonly ok: true; readonly action: CopyCommandAction }
  | { readonly ok: false; readonly reason: string }

/**
 * buildCopyCommand admits the words of one copy-command, or refuses the whole
 * action. The first word that is not one of the two alphabets above refuses it:
 * nothing is repaired, nothing is quoted and nothing is dropped, because a
 * command with a word missing is a command that does something else.
 *
 * The reason never quotes the value. It is drawn beside the row, and the row's
 * value may be anything a document holds -- a value the console will not put in a
 * shell is a value it will not put in a sentence either.
 */
export function buildCopyCommand(parts: readonly CommandPart[]): CopyCommandAnswer {
  if (parts.length === 0) {
    return { ok: false, reason: 'this command has no words at all, so there is nothing to copy' }
  }
  const argv: string[] = []
  for (const part of parts) {
    const what =
      part.kind === 'value' ? 'the value in this command' : "the word this command's own kind wrote"
    if (hasParentSegment(part.text)) {
      return {
        ok: false,
        reason: `${what} names a parent directory (a ".." segment), which the console will not copy`,
      }
    }
    if (part.kind === 'value' && !VALUE_PATTERN.test(part.text)) {
      return { ok: false, reason: valueRefusal(part.text) }
    }
    if (part.kind === 'word' && !FLAG_PATTERN.test(part.text)) {
      return {
        ok: false,
        reason: `${what} is not a plain word: it carries a space, a newline or a character a shell would read as syntax, and the console refuses it rather than quoting it`,
      }
    }
    argv.push(part.text)
  }
  return { ok: true, action: { kind: 'copy-command', argv } }
}

/** valueRefusal is the sentence for a value that is not a plain word. It reads
 * the value only to tell the two cases apart -- an opener that is not a letter or
 * a digit from a character inside the word -- and never draws it. */
function valueRefusal(text: string): string {
  if (!OPENER_PATTERN.test(text)) {
    return 'the value in this command opens with a character that is not a letter or a digit, so a shell could read it as a flag or as syntax: the console refuses it rather than quoting it'
  }
  return 'the value in this command carries a space, a newline or a character a shell would read as syntax, and the console refuses it rather than quoting it'
}

/** hasParentSegment reports whether a word walks up out of its own directory.
 * The rule is a segment and not a substring: `..` between slashes is a parent
 * directory, while `a..b` and `1.2.3` are ordinary names -- and the segment is
 * checked on the word as it stands, so `owner/../etc` is refused where
 * `owner/repo` is not. */
function hasParentSegment(text: string): boolean {
  return text.split('/').includes('..')
}
