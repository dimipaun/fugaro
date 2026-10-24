# `ee/` is reserved

This directory is reserved for **net-new organisation features**, which will ship under their own
licence rather than under the MIT licence the rest of the repository carries. It is not a place for
anything that lives in the core today: what is already published under MIT stays there.

**Nothing here is licensed yet.** The folder holds no code and no licence file, and `ee/LICENSE`
lands only once counsel's text for it is approved. Until then there is no grant to use anything
placed here, which is why nothing is placed here.

Three rules hold while the folder is reserved:

- **Nothing outside `ee/` imports anything under `ee/`.** The core builds and tests with this
  directory deleted, and a build-tagged assembly file is the only place a future feature may reach
  into it.
- **Every file that lands here starts with the two-line notice**: `SPDX-License-Identifier:
  LicenseRef-Fugaro-Enterprise`, and a pointer to `ee/LICENSE`. This file and `ee/LICENSE` are the
  only files exempt from it.
- **A release carries no `ee/` code until it sets the `ee` build tag**, and that tag obliges the
  release to label the binary as carrying code under the enterprise licence.

Nothing else changes with the reservation: the rest of the repository stays under the MIT licence in
[`LICENSE`](../LICENSE), and this folder takes nothing away from it.
