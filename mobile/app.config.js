// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's build-time config (mobile plan, Task P4.4; D120, D125; Check 89).
//
// `app.json` is the app's **static** config, and it is the file a reviewer
// reads: P3.1's `src/app-config.test.js` asserts that it carries no credential,
// no installation and **no release identity** -- no `ios.bundleIdentifier`, no
// `android.package`, no reverse-DNS string at all. That fence is P3.1's own
// boundary test, and behind it stands D125: the app is its own artifact with
// its own release path and cadence, whose store credentials and store identity
// are the owner's, so a copy of the app somebody else builds must not ship with
// an identity baked into a committed file. (D125 says the app is its own
// artifact; the "no identity in `app.json`" assertion is `app-config.test.js`'s
// to make, and it does.)
//
// A build still needs them: a store submission without a bundle identifier and
// a package name cannot exist, and the Android package **can never change**
// once the app exists on Play. So they arrive from **EAS environment
// variables**, at build time, through this file -- Expo's dynamic config, the
// documented `({ config }) => ({ ...config, ... })` form, which Expo calls with
// the `expo` object it read from `app.json` and whose return value is the
// config the build actually uses. The two variables are named here -- and in
// `README.md`, where the owner reads what sets them:
//
//   FUGARO_IOS_BUNDLE_ID      the iOS bundle identifier   (a reverse-DNS name)
//   FUGARO_ANDROID_PACKAGE    the Android package name    (a reverse-DNS name)
//
// **Neither value is committed.** Not in `app.json`, not in `eas.json` (see
// `src/eas-config.test.js`), not in this file. The owner sets them with
// `eas env:set` in the EAS project's `development`, `preview` and `production`
// environments -- one per build profile, see `eas.json` -- and exports them in
// the shell for the local `eas build`/`eas submit` runs; `README.md` has the
// commands and says which account runs them.
//
// **Three states, not two.**
//
//   * **A build.** `EAS_BUILD` is set on EAS's build servers, or the owner asks
//     for the same check locally with `FUGARO_REQUIRE_IDS=1`: a missing or
//     malformed variable is a thrown error naming the variable and the fix, so
//     a build cannot quietly produce an artifact with no identity or with a
//     typo'd one.
//   * **A local dev/lint run** (`expo start`, `npm test`) with the variables
//     unset: the config is returned **without** the identities, and nothing is
//     required -- nothing here needs a store account to run the tests.
//   * **A local `eas build`/`eas submit`.** `EAS_BUILD` is set on the build
//     *worker*, not by `eas-cli`, and `eas-cli` resolves this file locally
//     before it uploads anything -- it needs `ios.bundleIdentifier` and
//     `android.package` to configure credentials and to submit, so a config
//     that dropped them whenever `EAS_BUILD` was unset would make the
//     documented `eas build --profile internal` fail before a build ever
//     started. So in the dev/lint state a variable that **is** set is validated
//     and used (a value that is set must be one -- a typo is refused here too),
//     and the owner exports the two variables in the shell for those commands.
//
// The **requirement** is what the switches add; the identity itself is read
// wherever it is present.
//
// **No secret is read, and no value is printed.** The only environment this
// file looks at is the two variables above plus the two switches named below;
// nothing else in the environment reaches the config. The error messages repeat
// **no value**: an identifier is public, but a variable that was mistyped into
// holding something else must not have it printed into a build log -- so the
// messages describe the shape the value must have and never echo it.
//
// **ESM, and why.** `mobile/package.json` is `"type": "module"`, so this file
// is a module and the tests import it as one; Expo's own loader reads the
// `export default ({ config }) => ...` form -- `@expo/config`'s `evalConfig`
// resolves `result.default` and then calls it with the request built in
// `Config.ts` (`config` is the `app.json` `expo` object with Expo's defaults
// filled in) -- which is why the same file serves both. That shape was read
// off the loader's source; **it has not been run against a real `expo start`
// here** (README, "What is not verified here").
//
// **The export-compliance answer (the owner's declaration of 2026-10-20).**
// `eas build` printed that it cannot write `ITSAppUsesNonExemptEncryption` into
// a **dynamic** config -- this file is a function, so there is no object in it
// for the CLI to edit -- and asked for
// `{"ios": {"infoPlist": {"ITSAppUsesNonExemptEncryption": false}}}` in
// `app.config.js` or `app.json` by hand. The owner ruled on 2026-10-20: "write
// it, it's OK" -- the app uses only standard/exempt encryption, so the answer is
// declared here, in the config the build reads, and the submission no longer
// stops on the question. It is the same answer for every copy of the app and
// nobody's secret, so it is a constant below rather than a third EAS
// environment variable, and it is applied in **every** state -- a
// build and a dev/lint run alike -- because it is a fact about the app rather
// than about the environment this file resolves in (`eas-cli` resolves the file
// locally, see `resolveConfig`). **The owner answers Apple's other export
// questions in App Store Connect**; nothing here answers anything else, and
// `README.md` ("Building for internal testing") says whose ruling this is and
// dated when. The merge into `ios.infoPlist` is a merge and not an assignment:
// see `declaring`.

/** The two variables, by platform. The names are this task's, and a rename is deliberate. */
export const ID_VARIABLES = Object.freeze({
  ios: 'FUGARO_IOS_BUNDLE_ID',
  android: 'FUGARO_ANDROID_PACKAGE',
});

/**
 * The reverse-DNS shapes the two identities must have -- **each store's own
 * rule, not one rule with an Android exception**. Both are at least two
 * dot-separated segments, and **neither store requires lower case**: an
 * identifier in either case is a valid one, and refusing it here would be this
 * file inventing a rule its stores do not have.
 *
 *   * **iOS**: Apple allows letters (`A-Z` and `a-z`), digits, hyphens and
 *     periods -- and **no underscore**, in any segment. A segment may start
 *     with a digit.
 *   * **Android**: the package is an application id, whose segments are Java
 *     identifiers: letters, digits and underscores -- **no hyphen** (it is not
 *     a Java identifier character) -- and every segment must start with a
 *     **letter** (Play refuses `com.example.1app`, and a leading underscore).
 *
 * These two fences are the whole point of the file for the Android package:
 * it **can never change** once the app exists on Play, so a value the store
 * would reject has to be refused here, where the fix is still free.
 */
const IOS_SEGMENT = '[A-Za-z0-9][A-Za-z0-9-]*';
const ANDROID_SEGMENT = '[A-Za-z][A-Za-z0-9_]*';

export const ID_PATTERNS = Object.freeze({
  ios: new RegExp(`^${IOS_SEGMENT}(?:\\.${IOS_SEGMENT})+$`),
  android: new RegExp(`^${ANDROID_SEGMENT}(?:\\.${ANDROID_SEGMENT})+$`),
});

/** The field of `expo` each identity is written to, and the words the messages use. */
const FIELDS = Object.freeze({
  ios: {
    field: 'bundleIdentifier',
    what: 'iOS bundle identifier',
    shape:
      'letters (either case), digits and hyphens in at least two dot-separated segments ' +
      '(Apple allows no underscore)',
  },
  android: {
    field: 'package',
    what: 'Android package name',
    shape:
      'letters (either case), digits and underscores in at least two dot-separated segments, ' +
      'each starting with a letter (Play allows no hyphen, and no segment may start with a digit)',
  },
});

/**
 * The export-compliance answer: the plist key Apple asks about and the answer
 * the owner ruled on (2026-10-20), as the pair the merge below writes. The
 * value is the **boolean** `false` -- not the string `'false'`, which is a
 * different plist value and a different answer -- and the key is spelled as
 * Apple spells it, because a plist key is read by name and a near miss is a
 * question that stays open.
 */
export const EXPORT_COMPLIANCE = Object.freeze({
  field: 'ITSAppUsesNonExemptEncryption',
  value: false,
});

/**
 * Whether this run must carry the identities: an EAS build (`EAS_BUILD` is set
 * by EAS's build servers), or an explicit local ask (`FUGARO_REQUIRE_IDS=1`,
 * the owner's own switch, so a developer can check the same thing without an
 * account). Anything else -- `FUGARO_REQUIRE_IDS=0`, an empty `EAS_BUILD`, no
 * variables at all -- is a dev/lint run: the identities are **optional** there,
 * not forbidden, because `eas-cli` resolves this file locally that way (see
 * `optionalSection`).
 */
export function requiresIdentifiers(env = process.env) {
  return Boolean(env.EAS_BUILD) || env.FUGARO_REQUIRE_IDS === '1';
}

/**
 * Is `value` a usable identity for `platform`?
 *
 * `Object.hasOwn` rather than a lookup that only checks for `undefined`: a
 * platform name inherited from `Object.prototype` -- `constructor`, `toString`,
 * `__proto__` -- is not a key of `ID_PATTERNS`, and the old test read the
 * inherited function off the prototype and called `.test` on it, which throws a
 * `TypeError` out of a function whose whole contract is "is this a name?".
 * Anything that is not one of the two platforms is simply not an identity.
 */
export function isIdentifier(value, platform) {
  if (typeof value !== 'string' || !Object.hasOwn(ID_PATTERNS, platform)) return false;
  return ID_PATTERNS[platform].test(value);
}

/** The error a missing variable raises: the variable's name, and where to set it. */
function missing(variable, what) {
  return new Error(
    `${variable} is not set, and a build cannot be made without the ${what}. Set it in the ` +
      `EAS project's environment variables (` +
      `eas env:set --name ${variable} --value <value> --environment development --environment preview --environment production` +
      `), or export it in the shell for a local run, and re-run the build. FUGARO_REQUIRE_IDS=1 ` +
      `asks for this check outside an EAS build.`,
  );
}

/**
 * The error a malformed variable raises: the variable's name, the shape the
 * value must have, and the fix. **The value itself is not repeated** -- this is
 * a build log, and the one thing a mistyped variable might hold is something
 * that must not be printed.
 */
function malformed(platform, variable) {
  return new Error(
    `${variable} is not a reverse-DNS identifier, so it cannot be the ${FIELDS[platform].what}. ` +
      `It must be ${FIELDS[platform].shape}. Fix the value in the EAS project's environment ` +
      `variables (eas env:set --name ${variable} --value <value> --environment <the one the ` +
      `profile reads>) and re-run the build. The value is not repeated here.`,
  );
}

/** Read and validate one identity; throws on absent or malformed. */
function read(platform, env) {
  const variable = ID_VARIABLES[platform];
  const value = env[variable];
  if (value === undefined || value === '') throw missing(variable, FIELDS[platform].what);
  if (!isIdentifier(value, platform)) throw malformed(platform, variable);
  return value;
}

/** `section` without `field`, as a plain object (the section may be absent). */
function without(section, field) {
  const rest = { ...(section ?? {}) };
  delete rest[field];
  return rest;
}

/**
 * One section of the config in the dev/lint state: the static identity, if a
 * caller put one there, is dropped (this file is the single site for them), and
 * the variable is used **if it is set** -- validated like any other value, so a
 * typo is refused here as well. An unset variable adds nothing and requires
 * nothing, which is what keeps `expo start` and `npm test` runnable without a
 * store account.
 */
function optionalSection(platform, section, env) {
  const { field } = FIELDS[platform];
  const rest = without(section, field);
  const value = env[ID_VARIABLES[platform]];
  if (value === undefined || value === '') return rest;
  if (!isIdentifier(value, platform)) throw malformed(platform, ID_VARIABLES[platform]);
  return { ...rest, [field]: value };
}

/**
 * `section` with the export-compliance declaration merged into its `infoPlist`.
 *
 * **A merge, not an assignment**, and that is the whole of the function:
 * `infoPlist` is a shared object -- the base config may set keys in it, and an
 * Expo config plugin writes its own into it at prebuild time -- so replacing
 * the object on the way to adding one key would drop somebody else's, and the
 * drop would show up as a missing usage description or a missing capability in
 * a built app rather than as anything a reader could see here. The section may
 * be absent (a config that names no `ios` at all), which is why the spread
 * reads `section?.infoPlist ?? {}`.
 *
 * It runs in **both** states (`resolveConfig` calls it on both arms): the
 * declaration is a fact about the app, not about the environment the config is
 * resolved in, and `eas-cli` resolves this file locally -- in the dev/lint
 * state -- before it uploads a build.
 */
function declaring(section) {
  return {
    ...section,
    infoPlist: {
      ...(section?.infoPlist ?? {}),
      [EXPORT_COMPLIANCE.field]: EXPORT_COMPLIANCE.value,
    },
  };
}

/**
 * The resolved config: Expo's dynamic-config function, as a function of its
 * `env` so that the tests can call it with a fake one and a named mutation can
 * make each refusal red.
 *
 * `config` is the `expo` object Expo read from `app.json`; anything else is a
 * caller that is not Expo, and it is refused rather than silently resolved
 * against an empty base (a build whose config came out nameless would look
 * like a config bug rather than like a caller bug).
 */
export function resolveConfig(config, env = process.env) {
  if (config === null || typeof config !== 'object' || Array.isArray(config)) {
    throw new Error(
      'app.config.js was called without the static config: Expo passes the app.json "expo" ' +
        'object as `config`, and without it there is no name, slug or version to build from.',
    );
  }

  if (!requiresIdentifiers(env)) {
    // Dev/lint mode -- which is also the mode `eas-cli` resolves this file in,
    // locally, before it uploads a build: EAS sets `EAS_BUILD` on the build
    // worker, not in the CLI's own process, and the CLI needs
    // `ios.bundleIdentifier`/`android.package` to configure credentials and to
    // submit. So the identities are **not** required here, but a variable that
    // is set is read and validated rather than dropped; a static identity in
    // `app.json` is still removed, so this file stays the one place they come
    // from.
    return {
      ...config,
      ios: declaring(optionalSection('ios', config.ios, env)),
      android: optionalSection('android', config.android, env),
    };
  }

  return {
    ...config,
    ios: declaring({ ...(config.ios ?? {}), [FIELDS.ios.field]: read('ios', env) }),
    android: { ...(config.android ?? {}), [FIELDS.android.field]: read('android', env) },
  };
}

/**
 * Expo's entry point: the static `expo` object in, the build's config out.
 *
 * `context?.config` rather than destructuring, so a loader that calls this
 * with `null` gets the file's own message about the static config instead of
 * a `TypeError` a build log would show without saying what is wrong.
 */
export default function appConfig(context = {}) {
  return resolveConfig(context?.config, process.env);
}
