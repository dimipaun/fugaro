# Fugaro Licence Key Spec

Status: draft v0.1 · November 2026
Scope: issuing, validating, renewing and revoking Fugaro licence keys. Pricing, affiliate payouts and reseller billing are covered elsewhere.

Items marked **(proposed)** are implementation suggestions that weren't explicitly decided on the call.

---

## 1. Goals

- Paid tiers (Team, Enterprise) are unlocked by a **signed licence key** that Fugaro verifies **offline**.
- **All licence policy lives in the key** (renewal, grace, warnings, check interval), not hard-coded in Fugaro, so each key carries exactly the policy its deal needs.
- **No hidden phone-home.** Any contact with the licence server is done by a visible scheduled job in the customer's own GCP project, and only when the key says to.
- Losing a licence **never breaks work**: the project drops to Free, nothing is deleted, and a new key restores everything immediately.

## 2. Tiers

| Tier | How the key is obtained |
|---|---|
| Free | No key needed |
| Team | Self-serve purchase via Paddle, or a 14-day trial |
| Enterprise | Paddle purchase, reseller deal (manual issue), or founder-comped |

## 3. Key format

### 3.1 Encoding (proposed)

```
fgk_<base64url(payload JSON)>.<base64url(signature)>
```

- Signature: Ed25519 over the raw payload bytes.
- The `fgk_` prefix makes keys easy to recognise in logs, secret scanners and support tickets.

### 3.2 Payload (policy v1)

```json
{
  "v": 1,
  "kid": "sig-2026-01",
  "key_id": "lk_01J9Z…",
  "workspace_id": "ws_…",
  "tier": "enterprise",
  "seats": 50,
  "issued_at": "2026-11-12T00:00:00Z",
  "paid_through": "2027-11-12T00:00:00Z",
  "grace_days": 14,
  "renewal_mode": "auto",
  "renew_before_days": 7,
  "check_interval": "P1D",
  "warnings": [
    { "roles": ["admin"], "from": "grace_start" },
    { "roles": ["admin", "operator", "viewer"], "from": "grace_start+7d" }
  ],
  "kind": "paid",
  "note": "Rocket Core design partner"
}
```

| Field | Meaning |
|---|---|
| `v` | Policy version. See section 4. |
| `kid` | Which signing key produced the signature (for rotation). |
| `key_id` | Unique key ID; used for revocation and support. |
| `workspace_id` | Workspace/project the key is bound to. |
| `tier` | `team` or `enterprise`. |
| `seats` | Number of operator/admin seats. |
| `issued_at` | Issue time. |
| `paid_through` | End of the paid period. Key validity = `paid_through` + `grace_days`. |
| `grace_days` | Grace period length (default 14). |
| `renewal_mode` | `auto` (scheduled job fetches the next key), `manual` (user runs refresh), `none` (offline; runs to expiry). |
| `renew_before_days` | How early the job starts trying to fetch the next key. |
| `check_interval` | ISO 8601 duration for revocation checks (e.g. `P1D`), or `null` for none. Independent of expiry. |
| `warnings` | Who gets warned and from when. |
| `kind` | `paid`, `trial` or `comped`. |
| `note` | Free text, shown to admins only (optional). |

### 3.3 Key lifetime

- A key lasts for **the paid period plus grace**: monthly plans get monthly keys, annual plans get yearly keys.
- Seat **increases** take effect at the next renewal or check (a fresh key is issued right away). Seat **decreases** take effect at the next billing period.

## 4. Policy versioning

- Every key carries `v`. Fugaro accepts any version it knows.
- A key with a **higher** version is **refused** with: *"This licence key requires a newer version of Fugaro. Upgrade Fugaro to use it."* It never silently falls back to Free.
- Within a version, new **optional** fields may be added with safe defaults; unknown optional fields are ignored. Anything that changes meaning bumps `v`.
- Planned v2 (not now): explicit feature entitlements in the key instead of a tier.

## 5. Signing keys

- Private signing keys live in **Cloud KMS** on the licence server; they never touch disk or a laptop.
- Fugaro ships with **two public keys**, current and next, selected by `kid`.
- Rotation: start signing with the "next" key; a later Fugaro release ships a new "next". Old installs keep working throughout.

## 6. Validation in Fugaro

On project load and before any tier-gated action:

1. Parse the key; reject if malformed.
2. Look up `kid` among the built-in public keys; verify the signature.
3. If `v` > supported version → refuse with the upgrade message.
4. Check `workspace_id` matches the project.
5. Compute state from `now`:
   - `now ≤ paid_through` → **active**
   - `paid_through < now ≤ paid_through + grace_days` → **grace** (fully functional, warnings per `warnings`)
   - beyond that → **expired** → behave as Free
6. If the last revocation check (section 8) returned "revoked" → behave as Free.

## 7. Renewal

- Runs as a **Cloud Scheduler job in the customer's GCP project**, alongside the image rebuild jobs. It's visible, documented and under the customer's control.
- With `renewal_mode: auto`, the job starts trying `renew_before_days` before `paid_through` and fetches the next key from the licence server, sending only `key_id` and `workspace_id`.
- With `renewal_mode: manual`, the user runs `fugaro license refresh`.
- With `renewal_mode: none`, nothing contacts the server; the key runs to expiry and a new key is pasted in by hand (air-gapped Enterprise).
- Server outages are absorbed by the grace period.

### 7.1 Warnings

Default (configurable per key via `warnings`):

| Window | Who is warned |
|---|---|
| Active | Nobody |
| Grace, first week | Admins only |
| Grace, last week | Everyone, so developers can nudge an admin |

## 8. Revocation

- `check_interval` is separate from expiry: e.g. a monthly key that checks daily.
- The scheduled job calls the licence server's status endpoint at that interval. A `revoked` response drops the project to Free (same behaviour as expiry).
- The server keeps a **revocation list** for refunds, chargebacks and leaked keys. A leaked key is reissued with a new `key_id`; the old one is revoked and never renewed.
- Keys with `check_interval: null` (offline) are never checked and simply run to expiry.
- If a check fails because the server can't be reached, the key stays valid (proposed); only an explicit `revoked` response revokes.

## 9. Expiry and downgrade behaviour

When a key expires or is revoked:

- Enterprise/Team-only features switch off (e.g. multi-project workspaces, audit logs).
- **Jobs already running finish normally.**
- **Nothing is deleted.** Settings and data stay in place.
- Installing a new valid key restores everything immediately.

## 10. Seats

- A **seat = an operator or admin**, i.e. anyone who can launch jobs. **Viewers are free.**
- Admins assign seats explicitly.
- An operator **without a seat acts as a viewer** until a seat frees up or more are bought.
- **Admins always keep a seat**, so someone can always fix licensing.

## 11. Getting a key into a project

### 11.1 Setup wizard

- The key step is **optional**. Skipping it runs the project on Free.
- Offers: browser sign-in (default) or paste a key.
- On success, stores the key at project level, creates the renewal Scheduler job if the key needs one, and records the admins for warnings.

### 11.2 CLI (any time later)

```
fugaro license set <key>      # paste a key
fugaro license login          # browser sign-in, fetches a key
fugaro license refresh        # fetch the latest key now
fugaro license status         # tier, seats, expiry, last check
```

- The key is stored **at project level**, so every member is upgraded at once.
- `set` and `login` create the renewal job if it doesn't exist.
- Later: the same flows as buttons in the Electron console.

### 11.3 Browser sign-in flow

1. CLI starts a local HTTP server on `127.0.0.1:<random port>` and generates a random `state` (plus a PKCE verifier, proposed).
2. CLI opens the browser at the licence site: `/cli/authorize?port=…&state=…`.
3. User signs in and picks the workspace.
4. Site redirects to `http://127.0.0.1:<port>/callback?code=…&state=…`.
5. CLI checks `state`, exchanges the one-time `code` for the signed key, saves it, shuts down the local server.

### 11.4 No-browser fallback (device code)

1. CLI requests a device code and shows a short user code plus the URL.
2. User enters the code on the website from any device and picks the workspace.
3. CLI polls until the key is issued (or the code expires).

## 12. Licence server and website

Minimum scope:

- **Public site:** sign-in, Paddle checkout, account page (manage seats, download key, see invoices via Paddle).
- **Paddle webhook handler:** on `transaction.completed` (including renewals), issue or extend the key; on refund/chargeback, add to the revocation list.
- **Signing service:** builds payloads from the per-tier default policy and signs them via Cloud KMS.
- **Endpoints:** CLI authorize/token, device code, key renewal, key status (revocation).
- **Email:** new keys are also emailed to the purchaser.

### 12.1 Default policy per tier

The server holds a default policy per tier, so self-serve keys come out consistent. Overrides are set by hand only for special deals.

| | Team (monthly) | Team (annual) | Enterprise | Enterprise air-gapped | Trial |
|---|---|---|---|---|---|
| Lifetime | 1 month | 1 year | 1 year | 1 year | 14 days |
| `grace_days` | 14 | 14 | 14 | 14 (proposed) | 0 (proposed) |
| `renewal_mode` | auto | auto | auto | none | none |
| `check_interval` | P1D | P1D | P1D (proposed) | null | P1D |

## 13. Admin page (founder only)

A private page on the same site for keys issued without Paddle:

- **Reseller deals:** issue an Enterprise key for a workspace, seats and period after the invoice is sent. Billing happens outside Paddle (direct invoicing).
- **Comped keys:** for design partners and early adopters (e.g. Rocket Core). Form: workspace, tier, seats, expiry, note.
  - Comped keys **have an expiry** (e.g. one year) and are renewed by hand, so they can move to paid later.
  - `kind: comped`, excluded from revenue reporting and never earning affiliate commission.
- **Key management:** look up keys by workspace or `key_id`, revoke, reissue, override policy.

## 14. Trials

- A normal Team key issued from the website **without payment**: `kind: trial`, 14 days, checks daily.
- **One trial per workspace or account.**
- At expiry the project drops to Free unless a plan has been bought.

## 15. Open questions

- Exact grace period for trials and air-gapped keys.
- Whether seat assignment lives in the key (named seats) or only in the project (count only). Current assumption: count in the key, assignment in the project.
- Behaviour when a revocation check can't reach the server for a long time.
- Wording and placement of warnings in the CLI vs the console.
