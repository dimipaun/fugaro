// The shared package's skeleton module (mobile plan, Task P0.1).
//
// It carries no logic on purpose. It is here so that the package has one module
// with one test before the first module that does carry logic arrives -- P0.2's
// `shared/mailbox-schema/`, P0.3's `shared/url/`, then phase 1's five -- and so
// that the test script's glob has a nested directory to prove itself against.
// Every later module is a directory beside this one, with an `index.js` and an
// `index.test.js`; no later task edits `shared/package.json`, which is the whole
// point of the glob.
//
// What belongs in this package (D119): pure logic, and nothing that needs a
// runtime. Nothing here imports Electron, React Native, Node's own non-builtin
// surface, a database or a network client; the message schema, the mailbox
// semantics, the digest and its redactor, the presence rule and the URL
// validator are all functions over values.

/**
 * The package this module belongs to -- `package.json`'s name, which
 * `index.test.js` asserts agrees with this constant.
 */
export const SKELETON = 'fugaro-shared';
