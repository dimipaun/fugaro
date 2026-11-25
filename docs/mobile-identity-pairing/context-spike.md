# Context spike (Check 732, task Z0)

Status: the owner's own answers, 2026-11-24, no transcript kept. Fields marked `UNVERIFIED` are open.

## U3: five-hour and weekly limits per token (machine reading)

- Route: `GET https://api.anthropic.com/api/oauth/usage` with the session token (token not recorded here)
- HTTP status: 403
- Result: **not readable by this route** (a 401, a 403 or a document without the windows is U3's finding)
- Windows: none
- Within Anthropic's terms to read this way? Yes, within terms (owner's answer); the route still refuses it.

## U1: context fullness, and an injected `/compact`

- What the screen / status line shows about how full the context is: nothing on screen; only `/context` reports it when run (owner).
- Does a hook or status line receive the context fullness? UNVERIFIED (owner unsure)
- `/compact` injected while the session was idle: honoured (owner).
- Another slash command injected while idle: queued (owner).
- Evidence (what you typed, what you saw): owner's answers in the agent session of 2026-11-24; no transcript kept.

## U2: the auto-compact threshold

- Is the built-in threshold configurable? No (owner).
- Where does that setting live (file, key, flag, env)? Not configurable.
- Evidence: owner's answer, 2026-11-24; no transcript kept.

## What waits on this

Z1 (the gauge's source), Z2 (the compaction's mechanism) and Z3 (the meter's route).

## Capability list for Z1-Z3

- Context fullness: reachable only by running `/context`; no screen or status-line figure, and whether a hook receives it is UNVERIFIED.
- Idle `/compact`: honoured at once. Another injected slash command: queued until the next turn.
- Auto-compact threshold: not configurable.
- Five-hour and weekly limits: not readable by machine (HTTP 403 on the usage route), though a machine reading is within the terms.
