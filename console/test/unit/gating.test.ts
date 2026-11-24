// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Role gating (console-improvements plan RB4; D2907, F22).
//
// `gating.ts` maps one document -- WH1's `user ls --me --json` -- onto the
// question a write control asks (`can(action)`), and this suite is the five
// states that document can be in: each role, `none`, an unknown scope word, and
// **no answer at all**. The last is the one with a rule of its own (D2907): no
// answer is not a refusal, so a control the console cannot place a role for is
// drawn exactly as it was before this module existed.
//
// Two things are held here rather than in a screen's suite:
//
//   - **the table is the whole of it.** `GATE_ACTIONS` mirrors
//     `internal/access`'s `actionRules`, and a case holds its rows to
//     `GATE_ACTION_NAMES`, so an action added without a rule (or a rule left
//     behind by an action) fails here rather than gating nothing in silence.
//   - **the read is planned, not guessed.** `readGate` asks nobody below its
//     floor, asks once above it, and reads every gap `newOpsFrom` can answer --
//     a refusal, a missing method, a document this build cannot decode -- as no
//     answer.

import { describe, expect, it } from 'vitest'

import {
  GATE_ACTION_NAMES,
  GATE_ACTIONS,
  GATE_ALLOWED,
  GATE_SCOPE_WORDS,
  gateFrom,
  readGate,
} from '../../src/renderer/gating'
import type { Gate, GateDecision } from '../../src/renderer/gating'
import { newOpsFrom } from '../../src/renderer/new-bridge'
import { NEW_OP_ANSWERS } from '../support/newOpAnswers'
import type { WhoAmIRole, WhoAmIScope, WhoAmIView } from '../../src/shared/new-ops'
import { WHOAMI_CLI_FLOOR, whoamiSupported } from '../../src/shared/version'

/** aRead is one `user ls --me --json` document, as the contract reads it. */
function aRead(role: WhoAmIRole, scope: WhoAmIScope | null = 'project'): WhoAmIView {
  return Object.freeze({ version: 1, role, scope, repos: Object.freeze([]) })
}

/** A_VERSION is a version line that would pass any floor this console carries,
 * so a case that is about the role read itself does not depend on which release
 * happens to carry `--me`. It is not the constant's value: the constant is what
 * `readGate`'s own case drives. */
const A_VERSION = '9.9.9'

/** A_FLOOR is the floor those cases hand `readGate` explicitly, for the same
 * reason: the read above it is what they are about. */
const A_FLOOR = '0.0.1'

/** refusing is one decision read as the reason a control would draw, with the
 * control's own label in front of it -- exactly what `writes.ts` composes. */
function refusing(gate: Gate, action: Parameters<Gate['can']>[0], label = 'Halt fleet'): string {
  const decision = gate.can(action)
  expect(decision.allowed, `the role was allowed ${action}`).toBe(false)
  return `${label} ${decision.reason}`
}

describe('the action table', () => {
  it('has one rule per action, and no rule without an action', () => {
    expect(Object.keys(GATE_ACTIONS).sort()).toEqual([...GATE_ACTION_NAMES].sort())
    expect(GATE_ACTION_NAMES.length).toBeGreaterThan(0)
  })

  it('takes the roles internal/access gives the same actions', () => {
    // `internal/access/resolve.go`'s `actionRules`: launch and cancel are an
    // operator's (D1587), managing a repository and managing the project are an
    // admin's, and only the project-level one is held at project scope alone.
    expect(GATE_ACTIONS.launch).toEqual({ min: 'operator', project: false })
    expect(GATE_ACTIONS.cancel).toEqual({ min: 'operator', project: false })
    expect(GATE_ACTIONS['manage-repo']).toEqual({ min: 'admin', project: false })
    expect(GATE_ACTIONS['manage-project']).toEqual({ min: 'admin', project: true })
  })
})

describe('can, by role', () => {
  it('lets a project admin use every action', () => {
    const gate = gateFrom(aRead('admin', 'project'))
    for (const action of GATE_ACTION_NAMES) {
      expect(gate.can(action), action).toEqual(GATE_ALLOWED)
    }
  })

  it('lets an operator use the two run actions and nothing else', () => {
    const gate = gateFrom(aRead('operator', 'project'))
    expect(gate.can('launch').allowed).toBe(true)
    expect(gate.can('cancel').allowed).toBe(true)
    expect(refusing(gate, 'manage-repo', 'Kill')).toBe(
      'Kill needs the admin role and your role is operator at project scope',
    )
    expect(refusing(gate, 'manage-project')).toBe(
      'Halt fleet needs the admin role at project scope and your role is operator at project scope',
    )
  })

  it('lets a viewer use no action, and names the role it needs and its own', () => {
    const gate = gateFrom(aRead('viewer', 'project'))
    for (const action of GATE_ACTION_NAMES) {
      expect(gate.can(action).allowed, action).toBe(false)
    }
    // The tooltip names both halves: the role the control needs, the scope it
    // is held at, and the caller's own role and where it reaches.
    expect(refusing(gate, 'manage-project')).toBe(
      'Halt fleet needs the admin role at project scope and your role is viewer at project scope',
    )
    expect(refusing(gate, 'launch', 'Retry run')).toBe(
      'Retry run needs the operator role and your role is viewer at project scope',
    )
  })

  it('lets none use no action, and says so as the CLI’s own word', () => {
    const gate = gateFrom(aRead('none', null))
    for (const action of GATE_ACTION_NAMES) {
      expect(gate.can(action).allowed, action).toBe(false)
    }
    // `none` ranks below every role (`internal/access.Rank` is -1 for it), so no
    // minimum is ever met by a caller who holds nothing.
    expect(refusing(gate, 'launch')).toBe(
      'Halt fleet needs the operator role and your role is none',
    )
  })
})

describe('can, by scope', () => {
  it('refuses a project-level action to an admin whose role is held elsewhere', () => {
    // `EffectiveAtProject`: an all-repos or repository grant holds no
    // project-level thing (F19), so Halt fleet is not theirs however high their
    // role reads. The tooltip is what explains it.
    const all = gateFrom(aRead('admin', 'all'))
    expect(all.can('launch').allowed).toBe(true)
    expect(all.can('manage-repo').allowed).toBe(true)
    expect(refusing(all, 'manage-project')).toBe(
      'Halt fleet needs the admin role at project scope and your role is admin at every repository',
    )
    const repos = gateFrom(aRead('admin', 'repos'))
    expect(refusing(repos, 'manage-project')).toBe(
      'Halt fleet needs the admin role at project scope and your role is admin at the repositories its grant names',
    )
  })

  it('never enables an action from a scope', () => {
    // Scope narrows and never enables: a viewer at project scope is refused
    // every write, which is the half D2907's read exists for.
    for (const scope of ['project', 'all', 'repos'] as const) {
      const gate = gateFrom(aRead('viewer', scope))
      for (const action of GATE_ACTION_NAMES) {
        expect(gate.can(action).allowed, `${scope} ${action}`).toBe(false)
      }
    }
  })

  it('refuses a project-level action to an unknown scope word, and says only the role', () => {
    // A CLI newer than this build may name a scope this build does not know.
    // The contract reads it as `null` (not as the project), the agent's own
    // rule is the narrower reading, and the tooltip names the role alone rather
    // than inventing where it reaches.
    const gate = gateFrom(aRead('admin', null))
    expect(gate.scope).toBeNull()
    expect(gate.can('launch').allowed).toBe(true)
    expect(refusing(gate, 'manage-project')).toBe(
      'Halt fleet needs the admin role at project scope and your role is admin',
    )
  })

  it('names every scope it knows, so no tooltip falls back to the wire word', () => {
    expect(Object.keys(GATE_SCOPE_WORDS).sort()).toEqual(['all', 'project', 'repos'])
  })
})

describe('no answer', () => {
  it('allows every action and carries no reason', () => {
    const gate = gateFrom(null)
    expect(gate.role).toBeNull()
    expect(gate.scope).toBeNull()
    for (const action of GATE_ACTION_NAMES) {
      expect(gate.can(action), action).toEqual(GATE_ALLOWED)
      // An allowed control says nothing about roles: `writes.ts` draws its own
      // "not available in this release" words, which are this build's fact and
      // not the caller's.
      const decision: GateDecision = gate.can(action)
      expect(decision.reason).toBe('')
    }
  })

  it('is not read as the role none', () => {
    // The whole distinction D2907 turns on: nobody read is not nobody allowed,
    // or every installation one release behind would lose every write control.
    expect(gateFrom(null).can('manage-project').allowed).toBe(true)
    expect(gateFrom(aRead('none', null)).can('manage-project').allowed).toBe(false)
  })
})

describe('readGate', () => {
  it('asks nobody while the floor is unset, which is today’s release', async () => {
    // `WHOAMI_CLI_FLOOR` is `null` until a release carries `user ls --me`: no
    // child is spawned, and the gate is the no-answer one.
    expect(WHOAMI_CLI_FLOOR).toBeNull()
    expect(whoamiSupported(A_VERSION)).toBe(false)
    let asked = 0
    const gate = await readGate(
      {
        whoAmI: () => {
          asked += 1
          return Promise.resolve({ ok: true as const, value: aRead('admin', 'project') })
        },
      },
      A_VERSION,
    )
    expect(asked).toBe(0)
    expect(gate.role).toBeNull()
    expect(gate.can('manage-project').allowed).toBe(true)
  })

  it('asks nobody without a version line, whatever the floor says', async () => {
    let asked = 0
    const gate = await readGate(
      {
        whoAmI: () => {
          asked += 1
          return Promise.resolve({ ok: true as const, value: aRead('admin', 'project') })
        },
      },
      null,
      A_FLOOR,
    )
    expect(asked).toBe(0)
    expect(gate.role).toBeNull()
  })

  it('asks a CLI at or above the floor once, and maps what it answers', async () => {
    const gate = await readGate(
      {
        whoAmI: (request: unknown) => {
          // The operation takes no argument at all (C-OPS's `NoArgumentRequest`).
          expect(request).toEqual({})
          return Promise.resolve({ ok: true as const, value: aRead('viewer', 'project') })
        },
      },
      A_VERSION,
      A_FLOOR,
    )
    expect(gate.role).toBe('viewer')
    expect(gate.scope).toBe('project')
    expect(gate.can('launch').allowed).toBe(false)
  })

  it('reads a refusal as no answer', async () => {
    const gate = await readGate(
      {
        whoAmI: () =>
          Promise.resolve({ ok: false as const, gap: { gap: 'refused' as const, sentence: 'the CLI refused it' } }),
      },
      A_VERSION,
      A_FLOOR,
    )
    expect(gate.role).toBeNull()
    expect(gate.can('manage-project').allowed).toBe(true)
  })

  it('reads a missing bridge method as no answer', async () => {
    // `newOpsFrom` answers `not-available` for a bridge with no such member --
    // the old preload, or a console whose `whoami` row is not registered (a CLI
    // below `WHOAMI_CLI_FLOOR`) -- and that is no answer, so every control stays
    // as it is.
    const gate = await readGate(newOpsFrom({}), A_VERSION, A_FLOOR)
    expect(gate.role).toBeNull()
    expect(gate.can('launch').allowed).toBe(true)
  })

  it('reads a document it cannot decode as no answer', async () => {
    const broken = JSON.parse(JSON.stringify(NEW_OP_ANSWERS['whoami'])) as {
      document: Record<string, unknown>
    }
    delete broken.document['role']
    const gate = await readGate(
      newOpsFrom({ whoAmI: () => Promise.resolve({ ok: true, data: broken }) }),
      A_VERSION,
      A_FLOOR,
    )
    expect(gate.role).toBeNull()
    expect(gate.can('manage-project').allowed).toBe(true)
  })

  it('reads the role an admin reaching one project answers', async () => {
    // The document is **this case's own** rather than a fixture: what it is
    // about is the gate's mapping for the one role that enables every action,
    // and the shared fixtures are the CLI's recordings now (I-OPS) -- WH1's is
    // an operator at `repos` scope, which is a different case and is the one
    // `ops-contract.test.ts` holds to its golden. A case that read a screen's
    // fixture to assert a role would be asserting the fixture.
    const admin = {
      document: { version: 1, role: 'admin', scope: 'project', repos: ['acme/app'] },
    }
    const gate = await readGate(
      newOpsFrom({ whoAmI: () => Promise.resolve({ ok: true, data: admin }) }),
      A_VERSION,
      A_FLOOR,
    )
    expect(gate.role).toBe('admin')
    expect(gate.scope).toBe('project')
    for (const action of GATE_ACTION_NAMES) {
      expect(gate.can(action).allowed, action).toBe(true)
    }
  })
})
