# The Check 733 spike: how to build it and take the readings

This is the owner's runbook for the throwaway app in
[`mobile/spike/`](../../mobile/spike/) — **task M0** of
[implementation-plan.md](implementation-plan.md), which produces the readings
[Check 733](../gcp-live-checklist.md) asks for. It is the console and account
work the app cannot do for itself; what the app *is* is in
[`mobile/spike/README.md`](../../mobile/spike/README.md).

**What this folder does not contain.** The readings themselves go into
`app-spike-DRAFT.md`, which the owner writes from the Copy buttons. This page is
the steps before that: an Expo account, four environment variables, three
provider consoles, and one build per platform.

## 0. What you need before you start

* An Expo account with EAS enabled, and `npx eas-cli login` done once.
* Node 22 at the repository root, and the repository at the commit you built
  from (`eas.json` sets `requireCommit: true`, so a build comes from a commit).
* On the iOS side: an Apple Developer account, and a device registered with it
  (an internal-distribution build installs only on registered devices).
* A Firebase project for the identity work, with **Authentication** enabled and
  the Google, Microsoft and Apple providers turned on (step 3).
* The three provider consoles: Google Cloud (OAuth client ids), Microsoft Entra
  (an app registration) and Apple (the Sign in with Apple capability). Apple's
  provider only exists on iOS; the Android phone is asked about Google and
  Microsoft.

## 1. The four environment variables

A build needs the two store identities and the two iOS share-extension names.
`mobile/spike/app.config.js` reads them and refuses a build without them; the
four are named in `mobile/spike/.env.example` with what each one is. Set them in
the EAS project, once per environment the profile uses (`development` here):

```sh
cd mobile/spike
npx eas env:set --name SPIKE_IOS_BUNDLE_ID --value <a reverse-DNS name you control> --environment development
npx eas env:set --name SPIKE_ANDROID_PACKAGE --value <another reverse-DNS name> --environment development
npx eas env:set --name SPIKE_IOS_APP_GROUP --value group.<the iOS bundle identifier> --environment development
npx eas env:set --name SPIKE_IOS_SHARE_EXTENSION_BUNDLE_ID --value <the iOS bundle identifier>.share --environment development
```

The four provider client ids (step 3) go in the same way, or into a local
`.env` copied from `mobile/spike/.env.example` — `.env` is gitignored and stays
that way. The Firebase **Web API key is not one of these**: it is typed into the
app's Settings tab at run time and never stored, so nothing of your project's
goes into the repository or the EAS environment as a key.

## 2. The two platforms in the consoles

**Android.** No console step beyond the package name above, and nothing to add
by hand: `expo-share-intent`'s config plugin writes the `ACTION_SEND` intent
filter for `text/plain` into the merged manifest at prebuild. Google sign-in
needs the Android OAuth client (step 3) registered against the package name
**and** the signing certificate's SHA-1 — EAS holds the keystore, and
`npx eas credentials --platform android` prints the fingerprint to paste into
the Google console.

**iOS.** Two steps the plugin cannot do for you.

1. **The App Group.** The share extension reads the shared text out of a shared
   container, so the app and the extension must both be entitled to the App
   Group `SPIKE_IOS_APP_GROUP` names. `expo-share-intent` writes the entitlement
   into both targets at prebuild; the group itself is registered in the Apple
   Developer portal, under **Identifiers → App Groups**, and then assigned to
   both App IDs (the app's and the extension's).
2. **The extension's own App ID and profile.** Register an App ID for
   `SPIKE_IOS_SHARE_EXTENSION_BUNDLE_ID`, and make sure the provisioning profile
   EAS uses covers **both** identifiers and the App Group. A profile that covers
   only the app is the common failure: the build succeeds, the extension
   installs, and it never launches.

Apple sign-in needs no service id: the app id carries the **Sign in with Apple**
capability (tick it in the portal, or let EAS manage the profile and tick it
there), and `expo-apple-authentication`'s plugin declares the entitlement.

## 3. The provider consoles, and what each one is for

| Provider | Where | What to create | What the app needs |
| --- | --- | --- | --- |
| Google | Google Cloud console → APIs & Services → Credentials | An **iOS** OAuth client for `SPIKE_IOS_BUNDLE_ID`, an **Android** client for `SPIKE_ANDROID_PACKAGE` + the EAS SHA-1, and a **Web** client | The web client id is the one Firebase's `signInWithIdp` exchange wants (`EXPO_PUBLIC_GOOGLE_CLIENT_ID`); the two platform ids are what the native sheet is opened with (`EXPO_PUBLIC_GOOGLE_IOS_CLIENT_ID`, `EXPO_PUBLIC_GOOGLE_ANDROID_CLIENT_ID`) |
| Microsoft | Microsoft Entra admin center → App registrations | One registration, **supported account types: any org directory and personal accounts** (the `common` tenant the app signs in through) | Its **Application (client) id** (`EXPO_PUBLIC_MICROSOFT_CLIENT_ID`), and a **mobile and desktop application** platform with the redirect URI the app's scheme makes: `fugarospike733://auth` |
| Apple | Apple Developer portal | Nothing beyond the app id's **Sign in with Apple** capability | Sign in with Apple has no client id here: the native sheet's identity token is what the exchange is given |

Then, in the **Firebase project** (Authentication → Sign-in method), enable
**Google**, **Microsoft** and **Apple**, and add the iOS bundle identifier and
the Android package name to the project's app registrations — Firebase checks
the token's audience, and a token minted for an unregistered client is refused
with `INVALID_IDP_RESPONSE`. The U4 screen shows exactly that refusal text when
it happens, which is the reading Check 733 wants; a refusal is a result, not a
failure of the run.

## 4. Build and install

```sh
cd mobile/spike
npm ci
npx eas build --profile development --platform ios       # then --platform android
```

The `development` profile builds a development client (an `.ipa` for a
registered device, an `.apk` for Android). EAS prints an install link when the
build finishes; open it on the phone, install, and open the app. On Android,
also install the development client's launcher if EAS does not offer the app
directly.

**Run the same build on both phones.** Check 733 wants each of the four readings
on an iPhone and on an Android phone, and the result blocks are labelled with
whatever you type in the **Settings** tab — type the handset's name there first,
once per phone.

## 5. Taking the four readings

Open the Settings tab, type the Firebase Web API key, the project id and the
phone's name. Then, per tab:

* **U4 — provider tokens.** Sign in with Google, then Microsoft (and Apple on
  iOS). Each press shows whether `accounts:signInWithIdp` accepted the token,
  the HTTP status, and Firebase's error text when it did not. Sign in with a
  **second** provider afterwards: the screen lists every identity it is holding
  at once, which is the second half of the question. Nothing shows a token.
* **U5 — the device key.** Press once: the screen mints an Ed25519 key, signs a
  fixed message, verifies it, and writes the private half into the app's secure
  store. The block records algorithm, hardware-backed, exportable, the public
  key and the signature. **No private key byte is shown on any screen.**
* **U7 — the share route.** Press **Copy the 20000-character probe**, paste it
  into any app that can share text (Notes, Messages, a browser), and share it
  back to this app. The screen shows what arrived, its length, the sending
  package or bundle *when the route exposes it* — this route does not, and the
  screen says so — and whether the paste arrived whole (both end marks present).
  Do it once more with a short selection, to show that a short share arrives
  too.
* **U10 — the synced keychain.** Press **Write the probe and read it back**, and
  keep the build installed. Then install the **same** build on a second iPhone
  signed into the **same Apple ID**, open U10 and press **Read it back without
  writing**. Type what the second phone showed into the observation field before
  copying the block. On Android there is no synced keychain; the screen says
  what Auto Backup does and does not do, and that half of the answer is a
  reading of the Android phone's reinstall behaviour.

Each tab's **Copy result** button puts a block on the clipboard starting with
`Check 733 — <question>` and the phone's name. Paste each one under that phone's
heading in `app-spike-DRAFT.md`, in the question's row.

## 6. After the readings

The spike has done its job when `app-spike-DRAFT.md` holds both phones' blocks
for all four questions: U4 and U7 decide what the pairing and share work is
built on, U5 decides whether the device binding is hardware or software, and U10
decides where the synced list is restored from. The app is throwaway — it holds
no Fugaro credential and calls no Fugaro installation — and `mobile/spike/` is
deleted once the answers are in the draft.
