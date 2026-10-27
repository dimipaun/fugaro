// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The entitlements the console asks for, and the only ones it may ask for
// (desktop-console plan Task 4.1; design section 7.2, and section 12's item 4,
// which leaves "is disable-library-validation required?" unverified until a
// signed build is run).
//
// The task's word is "minimum", and a minimum is only a fact if something
// refuses an addition. So this file is three things:
//
//   1. **a parser**, strict enough that an entitlement cannot hide from it --
//      it reads key/value pairs and *throws* on anything else, so a value it
//      does not understand is a failure rather than something skipped;
//   2. **the minimum set, asserted exactly.** One entitlement is granted
//      (`com.apple.security.cs.allow-jit`, justified below), and the set is
//      compared for equality, so a *widening* fails whether or not it is one of
//      the well-known names -- and each of those well-known names gets its own
//      named test anyway, because the failure sentence for a grant of
//      `disable-library-validation` should say that name, not "the sets
//      differ";
//   3. **the signing configuration that makes the plist load at all.** A plist
//      no build points at is a decoration: electron-builder signs with the
//      hardened runtime and with *this* file, or the minimum set is a wish.
//
// **The mutation that proves it**: adding `com.apple.security.cs.disable-library-validation`
// to the plist must turn the named test red, and it does -- the fixture below
// is that mutation, held as a planted plist the parser is run against. A
// checker that could not fail would pass the real file's assertion too.
//
// **What is deliberately not here.** No signature verification, no notarisation
// and no bundle inspection: those need a Mac, a certificate and a signed
// artifact, and they are owner-run checks (Tasks 4.3 and 4.5), stated as
// unverified in console/README.md rather than asserted here.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const PLIST = fileURLToPath(new URL('../../entitlements.mac.plist', import.meta.url))
const PACKAGE_JSON = fileURLToPath(new URL('../../package.json', import.meta.url))

/** plistText is the entitlements file, read from this file's own location
 * rather than the process's working directory, so `vitest` can start anywhere. */
const plistText = readFileSync(PLIST, 'utf8')

/**
 * commentsOf returns the plist's XML comments, in order. They are separated
 * from the parsed entitlements on purpose: the justification for an entitlement
 * is prose, the entitlement itself is a key, and the two must not be
 * confusable. `parseEntitlements` strips comments before it reads anything, so
 * a name that appears only in a comment is *not* a grant -- which is asserted
 * below, because the tempting way to "withhold" an entitlement is to mention it.
 */
export function commentsOf(source: string): readonly string[] {
  return source.match(/<!--[\s\S]*?-->/g) ?? []
}

/**
 * xmlCommentProblems reports every way `source`'s XML comments break XML 1.0's
 * comment production: a comment's body may not contain `--`, and may not end
 * with `-`.
 *
 * It exists because everything above reads this file with a regular expression,
 * and a regular expression cannot see this. `<!-- a -- b -->` is matched happily
 * by `commentsOf` (the only `-->` in it is the real terminator) and stripped
 * happily by `parseEntitlements`, so the suite is green on a file that *every
 * real XML parser refuses*: `plistlib` on expat, which is what Apple's own
 * tooling is built on, answers `not well-formed (invalid token)` and reads no
 * entitlement at all. This plist was written that way -- `--` as a dash, in the
 * prose that justifies the set -- and it was this suite's regexes, and nothing
 * else, that accepted it.
 *
 * The scan is deliberately the same shape as the readers above it: find a
 * comment, take its body, move past it. It is stricter than they are in the one
 * place that matters, and it says which body it objected to.
 */
export function xmlCommentProblems(source: string): readonly string[] {
  const problems: string[] = []
  let rest = source
  for (;;) {
    const open = rest.indexOf('<!--')
    const close = rest.indexOf('-->')
    if (close !== -1 && (open === -1 || close < open)) {
      problems.push('a `-->` with no `<!--` before it')
      rest = rest.slice(close + '-->'.length)
      continue
    }
    if (open === -1) return problems
    const end = rest.indexOf('-->', open + '<!--'.length)
    if (end === -1) {
      problems.push('a comment that is never closed')
      return problems
    }
    const body = rest.slice(open + '<!--'.length, end)
    const double = body.indexOf('--')
    if (double !== -1) {
      problems.push(
        `\`--\` inside a comment: ...${JSON.stringify(body.slice(Math.max(0, double - 30), double + 30))}...`,
      )
    }
    if (body.endsWith('-')) problems.push('a comment whose body ends with `-`')
    rest = rest.slice(end + '-->'.length)
  }
}

const KEY_VALUE_RE = /<key>([^<]+)<\/key>\s*(<true\/>|<false\/>|<string>[^<]*<\/string>)/g

/**
 * parseEntitlements reads `source` as a plist whose root is a dict of
 * entitlement keys, and returns it as a map. It is deliberately strict:
 *
 *   - the root must be `<plist ...>` containing exactly one `<dict>` pair;
 *   - inside that dict, *every* non-whitespace byte must belong to a
 *     `<key>NAME</key>` followed by `<true/>`, `<false/>` or
 *     `<string>VALUE</string>`;
 *   - anything else -- an array, a nested dict, an `<integer>`, a stray tag --
 *     throws.
 *
 * The strictness is the point. electron-builder merges a plist into the
 * signature as it finds it, so a value this parser skipped would be an
 * entitlement the suite reported as absent while the build granted it. An
 * unparseable file must be a red test, never a quiet zero.
 */
export function parseEntitlements(source: string): Map<string, boolean | string> {
  const bare = source.replace(/<!--[\s\S]*?-->/g, '')
  const plist = /<plist[^>]*>([\s\S]*)<\/plist>/.exec(bare)
  if (plist === null) throw new Error('not a plist: no <plist> element')
  const dict = /^\s*<dict>([\s\S]*)<\/dict>\s*$/.exec(plist[1] ?? '')
  if (dict === null) {
    throw new Error('not an entitlements plist: the plist root is not a single <dict>')
  }
  const body = dict[1] ?? ''
  const entries = new Map<string, boolean | string>()
  let rest = body
  for (const match of body.matchAll(KEY_VALUE_RE)) {
    const key = match[1] ?? ''
    if (entries.has(key)) throw new Error(`duplicate entitlement: ${key}`)
    const value = match[2] ?? ''
    if (value === '<true/>') entries.set(key, true)
    else if (value === '<false/>') entries.set(key, false)
    else entries.set(key, value.replace(/^<string>|<\/string>$/g, ''))
    rest = rest.replace(match[0], '')
  }
  if (rest.trim() !== '') {
    throw new Error(
      `not an entitlement pair: ${rest.trim().slice(0, 80)} -- every entry must be ` +
        '<key>NAME</key> and true, false or a string',
    )
  }
  return entries
}

/** GRANTED is the whole of the minimum set, and the reason for the one entry is
 * in the plist's own comment: the hardened runtime refuses executable memory, and
 * Electron's V8 needs it for its JIT. It is the only entitlement the console has
 * been shown to need. */
const GRANTED: readonly string[] = ['com.apple.security.cs.allow-jit']

/** PLANTED is the widening this file's red step names: the entitlement every
 * Electron guide adds "just in case", and the one Task 4.1 says may not be
 * granted unless the pinned Electron genuinely requires it. */
const PLANTED = 'com.apple.security.cs.disable-library-validation'

/**
 * WITHHELD is every entitlement a reader might expect to find, with the reason
 * it is not there. The list is longer than the mutation: a named test per entry
 * is what makes "the minimum set" a reviewable claim rather than a short plist
 * a reviewer has to take on trust.
 */
const WITHHELD: readonly { readonly key: string; readonly why: string }[] = [
  {
    key: 'com.apple.security.cs.disable-library-validation',
    why: 'the console loads no plugin and no native module of its own, so there is no library to validate differently',
  },
  {
    key: 'com.apple.security.cs.allow-unsigned-executable-memory',
    why: 'it is granted with disable-library-validation for plug-ins that ship their own JIT; ours has none',
  },
  {
    key: 'com.apple.security.cs.allow-dyld-environment-variables',
    why: 'it lets DYLD_* reach the process, which is the environment-variable hole the fuses close on the Node side',
  },
  {
    key: 'com.apple.security.cs.debugger',
    why: 'it lets another process attach a debugger to the hardened runtime, which is the same hole as --inspect',
  },
  {
    key: 'com.apple.security.get-task-allow',
    why: 'it is a development entitlement: a shipped build that carries it can be debugged by anything on the machine',
  },
  {
    key: 'com.apple.security.app-sandbox',
    why: 'the console is not a Mac App Store build (design D4), and the key alone buys nothing without a container',
  },
  {
    key: 'com.apple.security.network.client',
    why: 'the renderer reaches no network at all (design section 5.1 rule 9); the CLI the main process spawns is the network client',
  },
  {
    key: 'com.apple.security.network.server',
    why: 'nothing in the console listens on a socket',
  },
  {
    key: 'com.apple.security.device.audio-input',
    why: 'every device permission is denied rather than entitled (design section 5.1 rule 9)',
  },
  {
    key: 'com.apple.security.device.camera',
    why: 'every device permission is denied rather than entitled (design section 5.1 rule 9)',
  },
  {
    key: 'com.apple.security.files.user-selected.read-write',
    why: 'the console never opens a user-chosen file: it names a checkout, and the CLI reads it',
  },
]

/** notJustified returns the entitlements in `granted` that the minimum set does
 * not contain. The exact-set assertion and the planted fixture below are both
 * this function, so "the set is exactly this" and "this addition is refused" are
 * one check rather than two that could drift apart. */
export function notJustified(granted: Iterable<string>): readonly string[] {
  const allowed = new Set(GRANTED)
  return [...granted].filter((key) => !allowed.has(key))
}

const parsed = parseEntitlements(plistText)
const comments = commentsOf(plistText).join('\n')

describe('the entitlements parser refuses more than it reads', () => {
  it('reads a plist of the shipped shape', () => {
    const fixture = [
      '<?xml version="1.0" encoding="UTF-8"?>',
      '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">',
      '<plist version="1.0">',
      '<dict>',
      `\t<key>${PLANTED}</key>`,
      '\t<true/>',
      '\t<key>com.apple.security.get-task-allow</key>',
      '\t<string>yes</string>',
      '</dict>',
      '</plist>',
    ].join('\n')
    expect([...parseEntitlements(fixture)]).toEqual([
      [PLANTED, true],
      ['com.apple.security.get-task-allow', 'yes'],
    ])
  })

  it('fails on a plist whose root is an array rather than a dict', () => {
    const fixture = '<plist version="1.0"><array><string>x</string></array></plist>'
    expect(() => parseEntitlements(fixture)).toThrow(/not a single <dict>/)
  })

  it('fails on an entry it does not understand rather than skipping it', () => {
    // A nested dict is the shape an entitlement with options would take. The
    // parser must not report the *outer* key and drop the inner ones.
    const fixture = [
      '<plist version="1.0"><dict>',
      `<key>${PLANTED}</key>`,
      '<dict><key>com.apple.security.cs.debugger</key><true/></dict>',
      '</dict></plist>',
    ].join('\n')
    expect(() => parseEntitlements(fixture)).toThrow(/every entry must be/)
  })

  it('fails on a value that is not true, false or a string', () => {
    const fixture = `<plist version="1.0"><dict><key>${PLANTED}</key><integer>1</integer></dict></plist>`
    expect(() => parseEntitlements(fixture)).toThrow(/every entry must be/)
  })

  it('does not read an entitlement out of a comment', () => {
    // The names of withheld entitlements appear in this repository's comments on
    // purpose. A comment is not a grant, and this is the assertion that keeps
    // the two apart.
    const fixture = [
      '<plist version="1.0"><dict>',
      `<!-- deliberately not ${PLANTED}, see the note below -->`,
      '<key>com.apple.security.cs.allow-jit</key><true/>',
      '</dict></plist>',
    ].join('\n')
    expect([...parseEntitlements(fixture).keys()]).toEqual(['com.apple.security.cs.allow-jit'])
  })

  it('still reads an entitlement that a comment precedes', () => {
    const fixture = [
      '<plist version="1.0"><dict>',
      `<!-- why ${PLANTED} is not here -->`,
      `<key>${PLANTED}</key><true/>`,
      '</dict></plist>',
    ].join('\n')
    expect([...parseEntitlements(fixture).keys()]).toEqual([PLANTED])
  })

  it('refuses the planted widening, so the assertions below can fail', () => {
    // The plan's red step, held as a fixture: the minimum set plus one entry.
    expect(notJustified([...GRANTED, PLANTED])).toEqual([PLANTED])
    expect(notJustified(GRANTED)).toEqual([])
  })
})

describe('the plist is well-formed XML, not merely readable by this suite', () => {
  it('has no comment a real XML parser would refuse', () => {
    // The file is read by electron-builder, by `codesign` and by the owner's
    // `plutil`/`plistlib`; a comment carrying `--` makes all of them reject the
    // document whole, and the entitlements with it -- while the two regular
    // expressions above see nothing wrong.
    expect(xmlCommentProblems(plistText)).toEqual([])
  })

  it('reports the breakages it exists for, so the assertion above can fail', () => {
    // Held as fixtures, because a checker that cannot fail passes the real file
    // too. The first is the shape the shipped file had (review), the rest are
    // the two other ways a comment stops being a comment.
    expect(xmlCommentProblems('<!-- com.apple.security.cs.allow-jit -- granted -->')).toHaveLength(1)
    expect(xmlCommentProblems('<!-- fine - and fine -->')).toEqual([])
    expect(xmlCommentProblems('<!-- trailing --->')).toEqual(['a comment whose body ends with `-`'])
    expect(xmlCommentProblems('<!-- never closed')).toEqual(['a comment that is never closed'])
    expect(xmlCommentProblems('a stray --> in the document')).toEqual([
      'a `-->` with no `<!--` before it',
    ])
  })
})

describe('the console is entitled to the minimum and nothing else', () => {
  it('grants com.apple.security.cs.allow-jit and nothing else', () => {
    expect([...parsed.keys()].sort()).toEqual([...GRANTED].sort())
  })

  it('grants nothing that the set above does not name', () => {
    // The same assertion read from the other side, and the one a widening has
    // to edit: `notJustified` is a function, so a new entitlement fails here
    // whether or not the list below knows its name.
    expect(notJustified(parsed.keys())).toEqual([])
  })

  it('grants no entitlement with a false value', () => {
    // A `<false/>` is a decision the plist does not have to make: the
    // entitlement is absent because it is not granted, and writing it out as
    // false reads as a withheld grant that a later edit can flip.
    const falsy = [...parsed].filter(([, value]) => value === false).map(([key]) => key)
    expect(falsy).toEqual([])
  })

  for (const { key, why } of WITHHELD) {
    it(`withholds ${key} (${why})`, () => {
      expect(parsed.has(key)).toBe(false)
    })
  }

  for (const key of GRANTED) {
    it(`justifies ${key} in a comment beside it`, () => {
      expect(comments).toContain(key)
    })
  }

  it('names the plugin pair it withholds, so the exclusion is a decision rather than an omission', () => {
    // The design (section 12 item 4) leaves "does the hardened runtime need
    // disable-library-validation?" unverified, and Task 4.1 answers it with "not
    // here, and here is why". An exclusion nobody wrote down is one the next
    // person re-adds.
    expect(comments).toContain('com.apple.security.cs.disable-library-validation')
    expect(comments).toContain('com.apple.security.cs.allow-unsigned-executable-memory')
  })

  it('says in the plist that the claim is unverified without a signed build', () => {
    // The honest half of the justification: this repository has never run a
    // signed build (that is Check 59a, owner-run). A plist that reads as proven
    // would be the design's section 12 item 4 closed by assertion.
    expect(comments.toLowerCase()).toContain('unverified')
  })
})

describe('the macOS signing configuration the plist belongs to', () => {
  const build = JSON.parse(readFileSync(PACKAGE_JSON, 'utf8')).build as {
    readonly mac: Record<string, unknown>
  }

  it('signs with the hardened runtime', () => {
    expect(build.mac.hardenedRuntime).toBe(true)
  })

  it('hands this plist to the app and to everything it spawns', () => {
    // `entitlementsInherit` is what the helpers (renderer, GPU) are signed with.
    // Left unset it is electron-builder's own file, which grants the plugin
    // trio -- so setting it here is what keeps the minimum set a property of the
    // whole bundle rather than of its outermost binary.
    expect(build.mac.entitlements).toBe('entitlements.mac.plist')
    expect(build.mac.entitlementsInherit).toBe('entitlements.mac.plist')
  })

  it('names no signing identity: the identity comes from the environment', () => {
    // The task's rule: the identity is read from the environment and is never
    // written into a file in this repository. electron-builder takes it from
    // CSC_LINK or CSC_NAME; naming one here would put the owner's team in the
    // repository and decide the certificate for them.
    expect(build.mac).not.toHaveProperty('identity')
    expect(build.mac).not.toHaveProperty('certificateSubjectName')
  })

  it('does not turn the notariser off', () => {
    // In electron-builder 26 `notarize` is a *disable* switch: the step
    // activates from one of the credential triples in the environment. `false`
    // here would ship an unnotarised app under a notarised release's name, so
    // the assertion is about the one value that would break it.
    expect(build.mac.notarize).not.toBe(false)
  })

  it('builds the dmg and the zip, and neither a mas nor a pkg target', () => {
    expect(build.mac.target).toEqual(['dmg', 'zip'])
  })
})
