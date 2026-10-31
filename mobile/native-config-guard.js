// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.

// The build-time guard for the two native config files (D260, plan Task P7.1b).
//
// The guard's subject is **the file**, not a variable name. The names live in
// one place -- `NATIVE_CONFIG_VARIABLES` in `mobile/app.config.js` -- and they
// are the plan's defaults, chosen by this task and confirmed in no EAS project:
// an owner whose EAS environment delivers the path under another name is still
// a build with a file. So the question asked here is only whether the config
// the file resolved carries a readable path, and the answer for a platform the
// owner is not building is not this build's business.

import { existsSync } from 'node:fs';

/** The environment variable EAS sets to the platform it is building. */
export const BUILD_PLATFORM_VARIABLE = 'EAS_BUILD_PLATFORM';

/** The README section a refusal to build points a reader at. */
export const NATIVE_CONFIG_README_SECTION = 'Building for internal testing';

/**
 * The field of `expo` a native config file path is written to. It is exported
 * rather than repeated because the two sites have to agree: the writer in
 * `mobile/app.config.js` puts the path there and the reader below looks for it
 * there, and a rename in one place alone would make the guard read a field
 * nothing writes.
 */
export const GOOGLE_SERVICES_FIELD = 'googleServicesFile';

/**
 * The platforms this resolution has to check: the one EAS named, or both when
 * it named none -- which is the state a local `eas-cli` resolution runs in, and
 * the state where checking only one platform would let the other's file be
 * absent through a store build. A value EAS does not use is treated as "none
 * named" rather than as a platform, so an unrecognised environment checks more
 * and never less.
 */
export function builtPlatforms(env = process.env) {
  const named = env[BUILD_PLATFORM_VARIABLE];
  if (named === 'android' || named === 'ios') return [named];
  return ['android', 'ios'];
}

/**
 * The platforms whose resolved config carries no readable native config file:
 * the field is unset, empty, or names a path that is not a file on this
 * machine. `exists` is injected so the file-system half of the rule can be
 * proved on its own; in a build it is `fs.existsSync`.
 */
export function nativeConfigMissing(resolvedConfig, platforms, exists = existsSync) {
  return platforms.filter((platform) => {
    const value = resolvedConfig?.[platform]?.[GOOGLE_SERVICES_FIELD];
    if (typeof value !== 'string' || value === '') return true;
    // `exists` resolves a relative path against the cwd, while the RNFirebase
    // plugin resolves `googleServicesFile` against `projectRoot`: the two agree
    // only because an EAS file variable's value is absolute. A hand-set
    // relative path is the one case where this check could pass and prebuild
    // could still not find the file, and prebuild fails there anyway.
    return !exists(value);
  });
}

/**
 * The refusal. It names the platform, the variable `mobile/app.config.js` reads
 * for it, and where to change that name -- and it **repeats no environment
 * value**: an error a public build log prints is the wrong place for a path,
 * and the variable it names is a name the owner may have chosen. The variable
 * name is an argument rather than a lookup so that the one constant is the only
 * site a rename touches.
 */
export function nativeConfigError(platform, variableName) {
  return new Error(
    `The ${platform} build has no Firebase native config file: \`${variableName}\` is unset, ` +
      `empty, or its value names no file. The file is delivered to the build as an EAS file ` +
      `variable and is never committed. Prebuild refuses a missing file as well, with the ` +
      `plugin's own error and after the upload; this check is the earlier and plainer stop ` +
      `(D260). Set \`${variableName}\` on the build's own environment; if the path arrived ` +
      `under a different name, change the name \`NATIVE_CONFIG_VARIABLES\` holds in ` +
      `mobile/app.config.js, and see "${NATIVE_CONFIG_README_SECTION}" in mobile/README.md.`,
  );
}
