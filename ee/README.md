# `ee/` is the Fugaro Subscription Edition

This directory holds the **business-licensed features**: new ones, and those the owner moved out of
the core (D434, D444, the 2026-10-29 ruling). They ship under their own licence rather than under
the MIT licence the core carries. **Released MIT versions stay MIT**: moving code here leaves the
versions already published under MIT as they were.

**Everything here is under [`ee/LICENSE`](LICENSE)**, the Fugaro Subscription Edition licence. Its text is **counsel's, of 2026-10-27**, one wording in `ee/`, `console/` and `mobile/`; it names Subscription Terms (at https://fugaro.dev/legal/subscription-terms, **not published yet**: the page goes up once counsel has approved the text) and a contributor agreement, published at https://gist.githubusercontent.com/dimipaun/d5bb5db937f973b25be5778ce14ec8dd/raw/64918c7fc0f782327db82e7e790db53c3d47d09f/fugaro-cla.md (a raw Gist link pinned to a revision, so a later edit to the Gist does not change it). Its
first file is the assembly point, `ee/register`, which a build with the `ee` tag compiles in and
which sets the seams the moved features fill; the features themselves move here one at a time, and
nothing under this directory has ever been released.

Three rules hold for everything placed here:

- **Nothing outside `ee/` imports anything under `ee/`.** The core builds and tests with this
  directory deleted, and `cmd/fugaro/ee_on.go`, a build-tagged assembly file, is the only place any
  of it is reached.
- **Every file that lands here starts with the two-line notice**: `SPDX-License-Identifier:
  LicenseRef-Fugaro-Subscription`, and a pointer to `ee/LICENSE`. This file and `ee/LICENSE` are the
  only files exempt from it.
- **A release carries no `ee/` code until it sets the `ee` build tag**, and that tag obliges the
  release to label the binary as carrying code under the Subscription Edition licence.

Nothing else changes: the core stays under the MIT licence in [`LICENSE`](../LICENSE), and every
version already released under it stays MIT.
