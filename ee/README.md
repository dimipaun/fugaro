# `ee/` is the Fugaro Enterprise Edition

This directory is reserved for **net-new organisation features**, which ship under their own
licence rather than under the MIT licence the core carries. It is not a place for anything that
lives in the core today: what is already published under MIT stays there.

**Everything here is under [`ee/LICENSE`](LICENSE)**, the Fugaro Enterprise Edition licence. Its text is **counsel's, of 2026-10-27**, one wording in `ee/`, `console/` and `mobile/`; it names Subscription Terms (at https://fugaro.dev/legal/subscription-terms, **not published yet**: the page goes up once counsel has approved the text) and a contributor agreement, published at https://gist.githubusercontent.com/dimipaun/d5bb5db937f973b25be5778ce14ec8dd/raw/64918c7fc0f782327db82e7e790db53c3d47d09f/fugaro-cla.md (a raw Gist link pinned to a revision, so a later edit to the Gist does not change it). The
folder holds no code yet, and nothing under it has ever been released.

Three rules hold for everything placed here:

- **Nothing outside `ee/` imports anything under `ee/`.** The core builds and tests with this
  directory deleted, and a build-tagged assembly file is the only place a future feature may reach
  into it.
- **Every file that lands here starts with the two-line notice**: `SPDX-License-Identifier:
  LicenseRef-Fugaro-Enterprise`, and a pointer to `ee/LICENSE`. This file and `ee/LICENSE` are the
  only files exempt from it.
- **A release carries no `ee/` code until it sets the `ee` build tag**, and that tag obliges the
  release to label the binary as carrying code under the enterprise licence.

Nothing else changes: the core stays under the MIT licence in [`LICENSE`](../LICENSE), and this
folder takes nothing away from it.
