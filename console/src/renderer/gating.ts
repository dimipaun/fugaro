// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Role gating: what the caller's own role may use, and the sentence that says
// why a control it may not use is drawn disabled (console-improvements plan
// RB4; D2907, F22; the read is WH1's `user ls --me --json`).
//
// **The boundary this module lives inside.** D2907 settles where a role is
// decided: the CLI decides it (`internal/access`'s resolver, the same one every
// gate refuses with) and the console only *maps* what it read onto the controls
// it draws. So the role held here is exactly the word WH1 published -- the
// console mints no role from a token, a grant, an address or a scope name of its
// own -- and a role word this build does not know was already read as `none` by
// the contract's decoder (`src/shared/new-ops.ts`, C-OPS) before it arrives,
// `none` being the one answer that disables every write rather than enabling
// one.
//
// **What the console does own is the table below**: which thing it is about to
// do, and the least role that thing takes. It is a copy of `internal/access`'s
// `actionRules` (`internal/access/resolve.go`) -- the least role, and whether
// only a project-scope role holds the action (`access.go`'s `project` flag) --
// so a control is disabled for the same reason the command would refuse it. It
// is deliberately a copy and not an import: `internal/access` is Go, and
// `docs_console_boundary_test.go` is what keeps the two trees apart. The gate
// remains **advisory**: the CLI still refuses, which is why diverging from this
// table costs a refusal after a click and never authority.
//
// **No answer is not a refusal.** With no role read -- a CLI below
// `WHOAMI_CLI_FLOOR`, the handshake's own refusal, a bridge that cannot ask, a
// reply this build cannot decode -- `can` allows every action: the console draws
// the control the way it always did and the CLI's own refusal is what the
// operator reads after the click. Reading "no answer" as `none` would disable
// every write of every installation whose CLI is one release behind, which is
// the failure D2907's read exists to prevent in the other direction.
//
// **Scope narrows; it never enables.** The read names the role *and the scope it
// is held at* (WH1), and `internal/access` holds a project-level action only at
// project scope: an all-repos admin holds no project-level thing
// (`EffectiveAtProject`, F19). So a project-level action asks for `project`
// scope and a repository-level one does not, and no action is ever enabled *by*
// a scope -- a `viewer` at project scope is still refused the writes. The scope
// is named in the tooltip as well, because "you are admin at every repository"
// is what explains a control that an admin expects to be able to use.

import { WHOAMI_CLI_FLOOR, whoamiSupported } from '../shared/version'
import type { NewOps, WhoAmIRole, WhoAmIScope, WhoAmIView } from '../shared/new-ops'

/**
 * GateAction is one thing this console writes, named as `internal/access` names
 * the action it gates: F19's rule is that a caller asks about the thing it is
 * doing rather than about a role, so a control names its action here and the
 * role it takes is looked up, never spelled at the control.
 *
 * `handoff-agent` (`ActionHandoffAgent`) is not one of them: no screen of this
 * console draws a control for it, and a task that drew one adds its action, its
 * rule and its case rather than borrowing another action's role.
 */
export type GateAction = 'launch' | 'cancel' | 'manage-repo' | 'manage-project'

/** GateRole is the least role an action takes, in `internal/access`'s own
 * spellings and its own order (viewer < operator < admin). */
export type GateRole = 'viewer' | 'operator' | 'admin'

/**
 * GateRule is one action's requirement: the least role that may do it, and
 * whether only a project-scope role holds it. It is `access.go`'s `actionRule`,
 * minus the action name it is keyed by.
 */
interface GateRule {
  readonly min: GateRole
  /** true for the project-level actions -- the ones `Allowed` answers with
   * `EffectiveAtProject`, where an all-repos or repository grant holds
   * nothing. */
  readonly project: boolean
}

/**
 * GATE_ACTIONS is the whole table, and it is complete: `GATE_ACTION_NAMES` is
 * the closed set as a value, and a test holds the two to each other, so an
 * action with no rule is a compile error and a rule with no action is a test
 * failure rather than a control that silently gates nothing.
 *
 * The roles are `internal/access`'s: `ActionLaunch` and `ActionCancel` take an
 * operator (cancelling is not a viewer's, D1587), `ActionManageRepo` and
 * `ActionManageProject` take an admin.
 */
export const GATE_ACTIONS: Readonly<Record<GateAction, GateRule>> = Object.freeze({
  // ActionLaunch: starting a run (`run.launch`), and re-running one.
  launch: Object.freeze({ min: 'operator', project: false }),
  // ActionCancel: cancelling one run -- the Live screen's Reap on a run whose
  // heartbeat is lost.
  cancel: Object.freeze({ min: 'operator', project: false }),
  // ActionManageRepo: one repository's own settings and caps.
  'manage-repo': Object.freeze({ min: 'admin', project: false }),
  // ActionManageProject: the project-level writes -- Halt fleet, the project's
  // own Halt, the project layer and its guardrails, the image catalog, the
  // model accounts and the global budget.
  'manage-project': Object.freeze({ min: 'admin', project: true }),
})

/** GATE_ACTION_NAMES is `GateAction` as a value, in the table's own order. */
export const GATE_ACTION_NAMES: readonly GateAction[] = Object.freeze([
  'launch',
  'cancel',
  'manage-repo',
  'manage-project',
])

/**
 * GATE_ROLE_RANKS is each role's position, `internal/access`'s `Rank` exactly,
 * including `-1` for `none`: a caller holding nothing is not a viewer by
 * accident, so no minimum role is ever met by `none`.
 */
const GATE_ROLE_RANKS: Readonly<Record<WhoAmIRole, number>> = Object.freeze({
  viewer: 0,
  operator: 1,
  admin: 2,
  none: -1,
})

/** GATE_SCOPE_WORDS is how each scope reads in a tooltip: the CLI's own fact
 * (`WhoAmIScope`), in words an operator who never read the roles document can
 * place. */
export const GATE_SCOPE_WORDS: Readonly<Record<WhoAmIScope, string>> = Object.freeze({
  project: 'project scope',
  all: 'every repository',
  repos: 'the repositories its grant names',
})

/** needsWords is what one action needs, in `internal/access`'s words plus the
 * scope the action is held at: `the admin role`, or `the admin role at project
 * scope` for the project-level actions. */
function needsWords(rule: GateRule): string {
  return rule.project ? `the ${rule.min} role at project scope` : `the ${rule.min} role`
}

/** holdsWords is how the caller's own role reads: the CLI's word for it and,
 * when the read named one, where it reaches. `null` scope is a scope word this
 * build does not know -- a CLI newer than this console -- and it is stated as
 * the role alone rather than guessed at. */
function holdsWords(role: WhoAmIRole, scope: WhoAmIScope | null): string {
  const role_ = `your role is ${role}`
  if (scope === null) return role_
  return `${role_} at ${GATE_SCOPE_WORDS[scope]}`
}

/**
 * GateDecision is what one control is drawn from: whether the caller's role may
 * use it, and -- when it may not -- the clause naming the role it needs and the
 * caller's own. A decision that allows carries an empty `reason` and not the
 * control's own "not available in this release" words: those belong to the
 * drawing (`writes.ts`), which is the module that knows a control is not
 * performed by this build.
 */
export interface GateDecision {
  readonly allowed: boolean
  readonly reason: string
}

/** GATE_ALLOWED is the one decision every allowed control shares, frozen so a
 * caller cannot edit the answer another control reads. */
export const GATE_ALLOWED: GateDecision = Object.freeze({ allowed: true, reason: '' })

/**
 * gateReason is the refusal for one action: what it needs and what the caller
 * holds. It is a clause and not a sentence, because the control's own label is
 * what it hangs from -- `writes.ts` draws `<label> needs the admin role at
 * project scope and your role is viewer at project scope` -- so one sentence is
 * composed in one place and two controls with the same action read alike.
 */
export function gateReason(needs: string, holds: string): string {
  return `needs ${needs} and ${holds}`
}

/**
 * Gate is the role read, mapped: the CLI's own two facts about the caller and
 * the one question a control asks them.
 */
export interface Gate {
  /** role is the word WH1 published, or `null` when nothing answered. */
  readonly role: WhoAmIRole | null
  /** scope is where that role is held, `null` for an unknown scope word and for
   * no answer at all (the two are told apart by `role`). */
  readonly scope: WhoAmIScope | null
  /** can answers whether the caller's role may use one action, and why not. */
  can(action: GateAction): GateDecision
}

/**
 * gateFrom maps one role read onto the console's decisions. `null` is **no
 * answer** -- no CLI below the floor was asked, or the ask answered a gap -- and
 * it allows every action (`GATE_ALLOWED`), so a control the console cannot place
 * a role for is drawn exactly as it was before this module existed.
 *
 * `answer.role` is a word the contract already closed (`WhoAmIRole`) and
 * `answer.scope` may be `null`; nothing here re-reads the document, decides a
 * role from it or invents one.
 */
export function gateFrom(answer: WhoAmIView | null): Gate {
  const role: WhoAmIRole | null = answer === null ? null : answer.role
  const scope: WhoAmIScope | null = answer === null ? null : answer.scope
  return Object.freeze({
    role,
    scope,
    can: (action: GateAction): GateDecision => {
      if (role === null) return GATE_ALLOWED
      const rule = GATE_ACTIONS[action]
      const enough = GATE_ROLE_RANKS[role] >= GATE_ROLE_RANKS[rule.min]
      // A project-level action is held only at project scope (`EffectiveAtProject`),
      // and an unknown scope word is not the project: the safe reading of a CLI
      // newer than this build is the narrower one.
      const atScope = !rule.project || scope === 'project'
      if (enough && atScope) return GATE_ALLOWED
      return Object.freeze({
        allowed: false,
        reason: gateReason(needsWords(rule), holdsWords(role, scope)),
      })
    },
  })
}

/** GateOps is the one operation this module reaches: the caller's own role. The
 * type is narrowed to it so that a caller -- and a test -- hands `readGate` a
 * bridge it cannot ask anything else of. */
export type GateOps = Pick<NewOps, 'whoAmI'>

/**
 * readGate reads the caller's own role once, at startup, and maps it. It is the
 * whole of "when its floor allows":
 *
 *   - **no version line** (a handshake that could not read one) and **a version
 *     below the floor** both answer `gateFrom(null)` **without asking anybody**,
 *     so an older CLI is never handed `--me` (`WHOAMI_CLI_FLOOR`,
 *     `src/shared/version.ts`; the constant is `null` until a release carries
 *     it, which is today's state);
 *   - **a refusal, a missing method and a document this build cannot decode**
 *     are one answer too: `newOpsFrom` answers all three as a gap
 *     (`src/renderer/new-bridge.ts`), and a gap is no answer, never `none`.
 *
 * `version` is the line the startup handshake already read
 * (`gate.compatibility.cliVersion`), so this read makes no probe of its own, and
 * `ops` is the renderer's typed read of the operation (`newOpsFrom`), which
 * never rejects -- a synchronous `throw` here would be this module's own bug.
 */
export async function readGate(
  ops: GateOps,
  version: string | null,
  floor: string | null = WHOAMI_CLI_FLOOR,
): Promise<Gate> {
  if (version === null || !whoamiSupported(version, floor)) return gateFrom(null)
  const answer = await ops.whoAmI({})
  return gateFrom(answer.ok ? answer.value : null)
}
