---
paths:
  - "mobile/**"
  - "shared/**"
---

# Mobile app (Expo) and shared modules

- **Two licences meet here** (`LICENSING.md`, D311): `mobile/` is under the business licence in `mobile/LICENSE.md` (counsel's text of 2026-10-27), `shared/` stays MIT. Moving code from `mobile/` into `shared/` (or the other way) changes its licence, so it is the owner's decision, not a refactor. Never edit the licence text itself.
- Run `npm test` in `mobile/` and in `shared/` (`node --test`). CI runs neither until `docs/pending-workflows/shared-tests.yml` is copied in, so your local run is the only check.
- Each module's `index.js` is its only entry point; import from it, never around it. Where an `index.test.js` asserts the exported surface as an exact set, a new export is a decision and goes into that test.
- The scan tests fence these rules; extend a scan's list on purpose and never weaken its pattern:
  - `src/boundary.test.js`: the Keychain SDK only in `expo-secure-store.js`, `reveal(` only in `secret.js` and `secure-store.js`, no `console.` anywhere in the app,
  - `src/mailbox/write-path.test.js`: the mailbox client is the app's only write path (D113).
- When you change a guard, run the module's `mutation-proof.sh` too.
- JS and Go share wire formats. `internal/mailbox` tests read `shared/mailbox-schema/vectors.json` and `shared/mailbox/index.js` directly, and `shared/lease/parity.test.js` generates `internal/mailbox/testdata/lease-parity.json`. When one side changes, update both sides and run `npm test` and `go test ./internal/mailbox/`.
- Never swallow an error silently. Every failure path does something deliberate:
  - show the user feedback when they started the action,
  - restore the UI state (spinners, disabled controls),
  - or, if you ignore the error on purpose, add a one-line comment saying why.
- Secure storage:
  - keep in-memory caches coherent with the store: a delete clears the cached value, and a value whose save failed is not cached,
  - never put a credential value in an error message or on screen,
  - the app has no logger at all.
- Cleanup (sign-out, account switch, unmount) flushes or rejects pending callbacks and resets in-flight flags. A flag left set blocks every later attempt. `resources.js` tears down the previous account's listeners and caches before opening the next.
- Keep state per object (keyed by id), not one global "busy" flag that drops a concurrent request for another object.
- Cancel the previous timer, subscription or request before you schedule a new one.
- A cache entry that fails to decode is cleared, not retried forever.
- The mobile app releases on its own tag, `mobile-vX.Y.Z`. Its notes go in `docs/releases/mobile/`.
