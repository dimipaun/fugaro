# What to paste into the Play Console's "any other information required for access"

Google Play asks for this on the app's *App content* page, next to the sign-in
details. A reviewer cannot create a Fugaro account and cannot pair a phone with
an installation, so the app ships a built-in demo instead: this text is what
tells them so, and it is short on purpose, because the field is short.

The owner (who submits the release) pastes the paragraph below, unchanged.

---

Open the app and tap **Try the demo** on the first screen. It runs on built-in
sample data and needs no account, sign-in or network.

---

**What that text is describing, so the form and the app stay in step.**

* The control is on the first screen, with nothing to set up first:
  `src/app/first-screen.js` renders it, `mobile/index.js` wires it, and the
  owner's words are `TRY_DEMO` in `src/demo/index.js`.
* The demo mode itself is `src/demo/`: the app's own client, verification,
  prompt queue and confirmation machine, on fixed sample data ("Demo
  installation", `demo-repo`). It touches no network, no secure storage and no
  credential, and `src/demo/safety.test.js` is the fence around all three.
* Every screen it draws carries the banner "Demo mode: sample data" and the
  always-visible "Leave demo mode" -- both drawn by the shell
  (`src/app/root.js`), so a screen added later cannot forget them.
* `mobile/README.md` ("Demo mode: how to enter it, and how to leave it") is the
  same instructions for whoever builds the release.

**If the first screen's control moves**, this file and that README are the two
places to change, and the paragraph above is the only text that goes into the
form.
