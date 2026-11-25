// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The spike's build-time config (task M0; Check 733).
//
// **What it resolves, and from where.** Four things are not in this repository
// and never are: the two store identities, the iOS App Group the share
// extension and the app must both hold, and the provider client ids. They come
// from the environment -- EAS environment variables in a cloud build
// (`eas env:set`, `README.md` has the commands) or a local `.env` for
// `expo start` (`.env.example` names every variable) -- and this file copies
// them into the config the build actually uses. Nothing is defaulted: a build
// that resolves no identity is a build that cannot be uploaded, and a silent
// fallback here would produce a spike under somebody else's name.
//
// **The client ids are public; the identities are not a secret either.** A
// Google, Microsoft or Apple client id for a native app is public by design
// (that is why they may be set as `.env` values), and an App Group identifier
// is a name rather than a credential. The one thing that is *not* here at all
// is the Firebase Web API key: it is typed into the Settings screen at run time
// and lives in that screen's memory, because a key baked into a committed
// config is a key in a public repository (`screens/settings.js`).
//
// **The share extension.** `expo-share-intent`'s plugin is what adds the iOS
// share extension and the Android `ACTION_SEND` `text/plain` intent filter at
// prebuild; the app config is where it is registered and where its two iOS
// names are given. Its `iosAppGroupIdentifier` must be the App Group both
// targets are entitled to, and `iosShareExtensionBundleIdentifier` must be an
// identifier the owner's provisioning profile covers, or the extension installs
// and never launches -- which is the owner's console step, not a code one.

/** The environment variables this file reads, named once. */
export const VARIABLES = Object.freeze({
  iosBundle: 'SPIKE_IOS_BUNDLE_ID',
  androidPackage: 'SPIKE_ANDROID_PACKAGE',
  iosAppGroup: 'SPIKE_IOS_APP_GROUP',
  iosExtension: 'SPIKE_IOS_SHARE_EXTENSION_BUNDLE_ID',
  googleClient: 'EXPO_PUBLIC_GOOGLE_CLIENT_ID',
  googleIosClient: 'EXPO_PUBLIC_GOOGLE_IOS_CLIENT_ID',
  googleAndroidClient: 'EXPO_PUBLIC_GOOGLE_ANDROID_CLIENT_ID',
  microsoftClient: 'EXPO_PUBLIC_MICROSOFT_CLIENT_ID',
});

/** The variables a build cannot be made without, and what a reader does about each. */
const REQUIRED = Object.freeze([
  [VARIABLES.iosBundle, 'the iOS bundle identifier', 'eas env:set --name SPIKE_IOS_BUNDLE_ID --value <reverse-DNS name> --environment development'],
  [VARIABLES.androidPackage, 'the Android package name', 'eas env:set --name SPIKE_ANDROID_PACKAGE --value <reverse-DNS name> --environment development'],
  [VARIABLES.iosAppGroup, 'the iOS App Group shared by the app and the share extension', 'eas env:set --name SPIKE_IOS_APP_GROUP --value group.<the bundle identifier> --environment development'],
  [VARIABLES.iosExtension, 'the share extension bundle identifier', 'eas env:set --name SPIKE_IOS_SHARE_EXTENSION_BUNDLE_ID --value <bundle identifier>.share --environment development'],
]);

const value = (env, name) => {
  const read = env[name];
  return typeof read === 'string' && read !== '' ? read : undefined;
};

/** The error a missing variable raises: its name, what it is for, and the command that sets it. */
function missing(name, what, command) {
  return new Error(
    `${name} is not set, and a build needs ${what}. Set it in the EAS project's environment ` +
      `variables (${command}, and once per environment this profile uses), or put it in a local ` +
      `.env for \`expo start\` -- .env.example names every variable.`,
  );
}

/** The four identity variables, required in a build and optional everywhere else. */
function identities(config, env, required) {
  const resolved = { ...config };
  const iosAppGroup = value(env, VARIABLES.iosAppGroup);
  const iosExtension = value(env, VARIABLES.iosExtension);
  for (const [name, what, command] of REQUIRED) {
    const read = value(env, name);
    if (read === undefined && required) throw missing(name, what, command);
    if (read === undefined) continue;
    if (name === VARIABLES.iosBundle) resolved.ios = { ...(resolved.ios ?? {}), bundleIdentifier: read };
    if (name === VARIABLES.androidPackage) resolved.android = { ...(resolved.android ?? {}), package: read };
  }
  return {
    // The share extension is iOS-only, so the two names it needs are written
    // into the plugin's options rather than into a platform section.
    ...resolved,
    plugins: [
      ...(resolved.plugins ?? []),
      'expo-apple-authentication',
      [
        'expo-share-intent',
        {
          // Text is the whole of U7's question; the URL spellings are on because
          // a browser share is how a long selection is most easily produced.
          iosActivationRules: {
            NSExtensionActivationSupportsText: true,
            NSExtensionActivationSupportsWebURLWithMaxCount: 1,
            NSExtensionActivationSupportsWebPageWithMaxCount: 1,
          },
          iosShareExtensionName: 'Fugaro Spike Share',
          // The two iOS names are written only when the environment supplies
          // them: a `development` build has both (they are required above), and
          // `expo start` on a machine without them leaves the plugin's own
          // defaults in place rather than an empty identifier, which is a
          // prebuild refused for a reason nothing on the screen would explain.
          ...(iosAppGroup === undefined ? {} : { iosAppGroupIdentifier: iosAppGroup }),
          ...(iosExtension === undefined ? {} : { iosShareExtensionBundleIdentifier: iosExtension }),
          androidIntentFilters: ['text/*'],
        },
      ],
    ],
    extra: {
      ...(resolved.extra ?? {}),
      // Read by `src/app.js` through `expo-constants`: the ids are public, and
      // the screens show whether each one is set rather than assuming it is.
      googleClientId: value(env, VARIABLES.googleClient) ?? '',
      googleIosClientId: value(env, VARIABLES.googleIosClient) ?? '',
      googleAndroidClientId: value(env, VARIABLES.googleAndroidClient) ?? '',
      microsoftClientId: value(env, VARIABLES.microsoftClient) ?? '',
    },
  };
}

/**
 * The resolved config. `EAS_BUILD` is set by EAS's build servers and by nothing
 * else, so a missing identity is a stop in a build and is tolerated in
 * `expo start` and `npm test` -- which is where a spike is read and read again.
 */
export function resolveConfig(config, env = process.env) {
  if (config === null || typeof config !== 'object' || Array.isArray(config)) {
    throw new Error(
      'app.config.js was called without the static config: Expo passes the app.json "expo" ' +
        'object as `config`, and without it there is no name or slug to build from.',
    );
  }
  return identities(config, env, Boolean(env.EAS_BUILD));
}

export default function appConfig(context = {}) {
  return resolveConfig(context?.config, process.env);
}
