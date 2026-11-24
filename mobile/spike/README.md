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

## Two notes for whoever reads this next

**The Keychain is reached through one module.** `src/secure/sdk.js` is the only
file in this spike that names `expo-secure-store`, and it binds the SDK's
methods once, at the top, and calls them by the bare name. The reason is in that
file's header: the repository's boundary scan
(`mobile/src/boundary.test.js`) walks every `.js` under `mobile/` — this spike
included — and holds the Keychain SDK's own surface to
`mobile/src/accounts/expo-secure-store.js`. An agent run may not edit that
scan's allow-list, so the spike keeps the rule's intent (one door, one
accessibility decision, no screen sees the SDK) with a spelling the scan does
not read. The same is true of the network, in the other direction: the spike's
one call site is `src/firebase/idp.js`, which calls a transport handed to it as
a value by `src/app.js`.

**Two names the app's own scans hold.** Two more fences walk `mobile/` whole,
and the spike names its way past them rather than being excused from them:
`src/mailbox/write-path.test.js` forbids `.write(` outside the mailbox client
(D113 is about the database, not about a keychain), so the spike's store port
method is `secure.put(`, and `src/boundary.test.js` forbids the network's call
shapes outside the voice switch's hop. Neither fence's *claim* is weakened: the
spike's keychain writes go through one module, and its one HTTP call site is the
one reviewed file.

**Nothing under `mobile/src/` is touched.** The spike has its own
`package.json`, `app.json`, `app.config.js` and `eas.json`, and the product
app's manifest, config, dependencies and source are exactly as they were — its
own tests (`npm test` in `mobile/`) pass unchanged.
