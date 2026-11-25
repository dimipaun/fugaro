<!-- SPDX-License-Identifier: LicenseRef-Fugaro-Subscription -->
<!-- See mobile/LICENSE.md for the terms. -->

# `mobile/spike/` — the Check 733 spike

A throwaway Expo app that answers four questions the identity and pairing work
is blocked on. It is **task M0** of
[`docs/mobile-identity-pairing/implementation-plan.md`](../../docs/mobile-identity-pairing/implementation-plan.md),
it is **not the product app**, and it is deleted once the owner's readings are
written into `docs/mobile-identity-pairing/app-spike-DRAFT.md` (that document is
the owner's; this repository does not write it).

**One screen per question**, each with a **Copy result** button that puts a
short plain-text block on the clipboard:

| Tab | Question | What the block carries |
| --- | --- | --- |
| U4 | Do the three providers' tokens survive Firebase's `accounts:signInWithIdp`? | HTTP status per provider, Firebase's own error text on a refusal, whether two identities are held at once and how |
| U5 | Can this stack hold a hardware-backed, non-exportable signing key? | Algorithm, hardware-backed yes/no, exportable yes/no, a signature over a fixed message and its verification |
| U7 | Does a shared text reach the app, and whole? | The text, its length, the sending package or bundle when the route exposes it, and whether a 20,000-character paste arrives in one piece |
| U10 | Does the platform's synced keychain work with the option the app uses? | The options asked for, the read-back, and the field the owner records the second phone's reading in |

The steps that need a console — the provider client ids, the iOS share
extension's App Group and bundle identifier, the EAS environment variables and
the build itself — are in
[`docs/mobile-identity-pairing/app-spike-howto.md`](../../docs/mobile-identity-pairing/app-spike-howto.md).
This page is what the app is and how to run it.

## What it never does

It does not call a Fugaro installation, `credsvc`, a pairing route or any real
credential store; it holds no Fugaro credential; and it shows no provider token,
no private key byte and no keychain value on any screen or in any block. The
Firebase Web API key is typed into the Settings tab and lives in that screen's
memory — it is never written to a file, the keychain or storage. The provider
client ids come from the environment at build time (`.env.example` names every
variable), and the two identities and the App Group come from the EAS
environment, so **no value of the owner's is committed**.

Where the stock Expo stack cannot do something, the screen says so rather than
reporting a success it did not get. Two of those are known before a build:

* **No hardware-backed key.** No stock Expo package generates an asymmetric key
  in Android's Keystore/StrongBox or iOS's Secure Enclave, so U5's answer is the
  software fallback (`@noble/curves`, Ed25519) and the screen says so. A
  hardware key needs a native module or a config plugin that brings one.
* **No stock share route.** No Expo API reads Android's `Intent.EXTRA_TEXT` and
  nothing in Expo adds an iOS share extension, so U7's route is
  `expo-share-intent`'s config plugin, which adds both halves at prebuild. The
  iOS half needs console work the plugin cannot do for you (the how-to).

## Run it

```sh
cd mobile/spike
npm ci
npm test                                              # the pure parts, no device needed
npx eas build --profile development --platform ios    # or --platform android
```

`npm test` is the same runner `mobile/` uses (`node --test`), and it covers the
result-block formatters, the `signInWithIdp` request builder and refusal parser
(no token in any output), the software-key fallback's sign-and-verify, the share
payload parser and the secure-store option table. It runs on Node 22 and needs
no device, no account and no network.

The build is **owner-run**: it needs an Expo account, the EAS environment
variables above, and the provider consoles. Then install the development build
on a phone, open it, and work through the tabs — Settings first, so the API key
and the phone's name are in place for the blocks.

## What is not verified here

Nothing in this directory has been built, installed or run against a device from
this repository: no `eas build` has been run, no phone has been touched, and the
four answers do not exist until the owner produces them. The tests cover the
pure logic; the screens, the plugin's native halves and every question about a
real handset are the owner's run. `mobile/spike/` is not wired into CI (neither
is `mobile/`; see `docs/pending-workflows/shared-tests.yml`).

## Three notes for whoever reads this next

**The scans walk this spike, and two of its files are named on them.** The
repository's boundary scan (`mobile/src/boundary.test.js`) walks every `.js`
under `mobile/` — this spike included — so the spike is held to the app's rules
rather than excused from them, and it says so in the two places the rules name
files:

* `src/secure/sdk.js` is the spike's one door to the platform store, and it is
  on rule 1's allow-list beside `src/accounts/expo-secure-store.js`. It calls
  `expo-secure-store` under the SDK's own method names, holds the one
  accessibility decision, and no screen ever sees the SDK.
* `src/firebase/idp.js` is the spike's one call site, and it is on rule 6's
  allow-list beside `src/voice-switch/hop.js`. It calls the platform's `fetch`
  directly — the `accounts:signInWithIdp` `POST` is U4's whole question.

The first is the only file here that names `expo-secure-store`, and the second
the only one that opens the network: each rule's claim — one door, in a file a
reviewer reads whole — holds over this directory too, and no screen can reach
either one.

**One name the app's own write-path scan holds.** `src/mailbox/write-path.test.js`
forbids `.write(` outside the mailbox client (D113 is about the database, not a
keychain), so the spike's store port method is `secure.put(`. That fence's
claim — one write path, in the product app's mailbox — is untouched.

**The product app is untouched, and its scan is not.** The spike has its own
`package.json`, `app.json`, `app.config.js` and `eas.json`; the product app's
manifest, config, dependencies and screens are exactly as they were, and its own
tests (`npm test` in `mobile/`) pass unchanged. The one file this spike changed
outside its own directory is `mobile/src/boundary.test.js` — the two allow-list
entries above, which is where a second module that reaches the platform store
and a second file that opens the network are declared rather than spelled
around.
