# Licensing

Fugaro's core is released under the MIT licence in [`LICENSE`](LICENSE), and that file is the whole
of the core's licence. The open-core work does not change it: the core stays MIT, and every release
before 0.11.0 is MIT.

Two parts of the repository are not covered by it.

## `ee/` is excluded and reserved

[`ee/`](ee/README.md) is excluded from the MIT licence and reserved for net-new organisation
features, which will ship under their own licence. The directory holds no code and no licence file
today, so nothing placed there is licensed yet, and nothing outside `ee/` imports anything under it.

## `.agents/` is third-party code

[`.agents/`](.agents/LICENSE) holds Go skills vendored from a third-party project. They are that
project's code under its own MIT licence, whose text and copyright line are in the file linked
above; they are not Fugaro's to relicense, and the licence in `LICENSE` does not cover them.

## Released versions

Every release before 0.11.0 is MIT, and so is every release of the core. The split adds a directory
under a separate licence; it takes nothing away from what has already been published.

## Trademarks

The licences grant no right to the Fugaro name or logo: see the [README's trademark
paragraph](README.md#trademarks).
