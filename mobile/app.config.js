// The app's build-time config (mobile plan, Task P4.4; D120, D125; Check 89).
//
// `app.json` is the app's **static** config, and it is the file a reviewer
// reads: P3.1's `src/app-config.test.js` asserts that it carries no credential,
// no installation and **no release identity** -- no `ios.bundleIdentifier`, no
// `android.package`, no reverse-DNS string at all. That is D125's ruling: the
// identities are the owner's, they differ per installation and per store, and a
// copy of the app somebody else builds must not ship with them baked into a
// committed file.
//
// A build still needs them: a store submission without a bundle identifier and
// a package name cannot exist, and the Android package **can never change**
// once the app exists on Play. So they arrive from **EAS environment
// variables**, at build time, through this file -- Expo's dynamic config, the
// documented `({ config }) => ({ ...config, ... })` form, which Expo calls with
// the `expo` object it read from `app.json` and whose return value is the
// config the build actually uses. The two variables are named here, and only
// here:
//
//   FUGARO_IOS_BUNDLE_ID      the iOS bundle identifier   (a reverse-DNS name)
//   FUGARO_ANDROID_PACKAGE    the Android package name    (a reverse-DNS name)
//
// **Neither value is committed.** Not in `app.json`, not in `eas.json` (see
// `src/eas-config.test.js`), not in this file. The owner sets them with
// `eas env:create` in the EAS project's `production` and `preview`
// environments; `README.md` has the commands and says which account runs them.
//
// **What this file refuses, and what it leaves alone.** Under a real build --
// `EAS_BUILD` is set on EAS's build servers, or the owner asks for the same
// check locally with `FUGARO_REQUIRE_IDS=1` -- a missing or malformed variable
// is a thrown error naming the variable and the fix, so a build cannot quietly
// produce an artifact with no identity or with a typo'd one. With neither set
// (a developer's `expo start`, and `npm test`) the config is returned **without**
// the identities: nothing here needs a store account to run the tests, and a
// local start does not need the identity either.
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

/** The two variables, by platform. The names are this task's, and a rename is deliberate. */
export const ID_VARIABLES = Object.freeze({
  ios: 'FUGARO_IOS_BUNDLE_ID',
  android: 'FUGARO_ANDROID_PACKAGE',
});

/**
 * The reverse-DNS shapes the two identities must have.
 *
 * Shared body: at least two dot-separated segments, each of lower-case letters,
 * digits, hyphens and underscores. **The Android package is stricter than the
 * iOS one**: no segment may start with a digit (Play's rule; `dev.fugaro.1app`
 * is a package the Play Console refuses), while a bundle identifier may.
 */
const SEGMENT = '[a-z0-9][a-z0-9_-]*';
const ANDROID_SEGMENT = '[a-z][a-z0-9_-]*';

export const ID_PATTERNS = Object.freeze({
  ios: new RegExp(`^${SEGMENT}(?:\\.${SEGMENT})+$`),
  android: new RegExp(`^${ANDROID_SEGMENT}(?:\\.${ANDROID_SEGMENT})+$`),
});

/** The field of `expo` each identity is written to, and the words the messages use. */
const FIELDS = Object.freeze({
  ios: { field: 'bundleIdentifier', what: 'iOS bundle identifier' },
  android: { field: 'package', what: 'Android package name' },
});

/**
 * Whether this run must carry the identities: an EAS build (`EAS_BUILD` is set
 * by EAS's build servers), or an explicit local ask (`FUGARO_REQUIRE_IDS=1`,
 * the owner's own switch, so a developer can check the same thing without an
 * account). Anything else -- `FUGARO_REQUIRE_IDS=0`, an empty `EAS_BUILD`, no
 * variables at all -- is a dev/lint run and gets a config without them.
 */
export function requiresIdentifiers(env = process.env) {
  return Boolean(env.EAS_BUILD) || env.FUGARO_REQUIRE_IDS === '1';
}

/** Is `value` a usable identity for `platform`? */
export function isIdentifier(value, platform) {
  const pattern = ID_PATTERNS[platform];
  return typeof value === 'string' && pattern !== undefined && pattern.test(value);
}

/** The error a missing variable raises: the variable's name, and where to set it. */
function missing(variable, what) {
  return new Error(
    `${variable} is not set, and a build cannot be made without the ${what}. Set it in the ` +
      `EAS project's environment variables (` +
      `eas env:create --name ${variable} --value <value> --environment production --environment preview` +
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
function malformed(variable, what) {
  return new Error(
    `${variable} is not a reverse-DNS identifier, so it cannot be the ${what}. It must be ` +
      `lower-case letters, digits, hyphens and underscores in at least two dot-separated ` +
      `segments, and no segment of the Android package may start with a digit. Fix the value ` +
      `in the EAS project's environment variables (eas env:update --name ${variable} ...) and ` +
      `re-run the build. The value is not repeated here.`,
  );
}

/** Read, validate and return one identity; throws on absent or malformed. */
function read(platform, env) {
  const variable = ID_VARIABLES[platform];
  const { what } = FIELDS[platform];
  const value = env[variable];
  if (value === undefined || value === '') throw missing(variable, what);
  if (!isIdentifier(value, platform)) throw malformed(variable, what);
  return value;
}

/** `section` without `field`, as a plain object (the section may be absent). */
function without(section, field) {
  const rest = { ...(section ?? {}) };
  delete rest[field];
  return rest;
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
    // Local mode. The identities are removed rather than passed through, so
    // app.json cannot become a second site for them by accident: this file is
    // the one place they come from, and a value sitting in the static config
    // is a value this file drops.
    return {
      ...config,
      ios: without(config.ios, FIELDS.ios.field),
      android: without(config.android, FIELDS.android.field),
    };
  }

  return {
    ...config,
    ios: { ...(config.ios ?? {}), [FIELDS.ios.field]: read('ios', env) },
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
