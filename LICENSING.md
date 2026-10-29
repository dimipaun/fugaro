# Licensing

Fugaro's core is released under the MIT licence in [`LICENSE`](LICENSE), and that file is the whole
of the core's licence. The open-core work does not change it: the core stays MIT, and every release
before 0.11.0 is MIT.

Four parts of the repository are not covered by it. Three of them, `console/`, `mobile/` and
`ee/`, are excluded from the MIT licence and carry a business licence of their own, one text in
three files of identical wording, which is counsel's text of 2026-10-27 and replaces the draft the owner adopted on 2026-10-26. Every file under those three directories carries a
two-line SPDX notice naming that licence as `LicenseRef-Fugaro-Subscription` — as a comment, or,
where the file type has no comment syntax, in a sibling `<name>.license` carrying the same two
lines — and the only files without one are each directory's own licence file and its `README`,
which the licence and the page belong to instead. The text the
identifier resolves to is [`LICENSES/LicenseRef-Fugaro-Subscription.txt`](LICENSES/LicenseRef-Fugaro-Subscription.txt),
a byte-for-byte copy of [`ee/LICENSE`](ee/LICENSE). **It names two documents**: the Subscription Terms, at https://fugaro.dev/legal/subscription-terms (the page is **not published yet**; it goes up once counsel has approved the text), and the contributor licence agreement, published at https://gist.githubusercontent.com/dimipaun/d5bb5db937f973b25be5778ce14ec8dd/raw/64918c7fc0f782327db82e7e790db53c3d47d09f/fugaro-cla.md (a raw Gist link pinned to a revision, so a later edit to the Gist does not change it). The licence allows copying and modifying for development and testing,
and needs a subscription for the correct number of user seats (one seat per person who uses it)
for production use, under those Subscription Terms.
A first draft of the Subscription Terms, written for counsel, is at
[`docs/licensing/subscription-terms.md`](docs/licensing/subscription-terms.md): it is **not in force**
until counsel has reviewed it and the owner has published it.

## The desktop console, `console/`

From console 0.113.0 the desktop console is under [`console/LICENSE.md`](console/LICENSE.md), the
whole of it: the renderer bundle and everything the app's windows load are covered too, with no
MIT carve-out for code that runs on the client. Every console release up to and including 0.112.0
stays under the MIT licence it was released under, and so does the console's source as published
before the change. Third-party components the console ships keep their own licences
([`console/THIRD_PARTY_NOTICES.md`](console/THIRD_PARTY_NOTICES.md)).

## The mobile app, `mobile/`

The mobile app is under [`mobile/LICENSE.md`](mobile/LICENSE.md), from its first release: no
release of it has been published. The app's source as published before the change stays under the
MIT licence it was published under. Its third-party components keep their own licences.

## `ee/`, the Subscription Edition

[`ee/`](ee/README.md) is excluded from the MIT licence and reserved for net-new organisation
features, under [`ee/LICENSE`](ee/LICENSE). Nothing under it has ever been released, and nothing
outside `ee/` imports anything under it: its first file is the assembly point `ee/register`, which
only a build with the `ee` build tag compiles in, through [`cmd/fugaro/ee_on.go`](cmd/fugaro/ee_on.go).

## `.agents/` is third-party code

[`.agents/`](.agents/LICENSE) holds Go skills vendored from a third-party project. They are that
project's code under its own MIT licence, whose text and copyright line are in the file linked
above; they are not Fugaro's to relicense, and the licence in `LICENSE` does not cover them.

## `shared/` stays MIT

`shared/`, the pure-logic package the mobile app imports and the core's own tests read, is not
under the business licence: it stays under the MIT licence in `LICENSE`.

## Released versions

Every release before 0.11.0 is MIT, and so is every release of the core and every console release
up to and including 0.112.0. The split adds a licence to three directories from here on; it takes
nothing away from what has already been published.

## Trademarks

The licences grant no right to the Fugaro name or logo: see the [README's trademark
paragraph](README.md#trademarks).
