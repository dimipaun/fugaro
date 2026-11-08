<!-- SPDX-License-Identifier: LicenseRef-Fugaro-Subscription -->
<!-- See ee/LICENSE for the terms. -->

# ee/coordinator/testdata/mcpauth

Fixtures for the front door's own package (`../mcpauth`).

`jwks.json` is the SHAPE of the key set the authorize endpoint reads: the
document Google publishes for the project that signs an ID token (M6, D923).
Its modulus is a placeholder and the test that reads it puts one of its own
in, so the file pins the shape and holds no key material — a fixture shaped
like a real key is key material in a repository whatever it is called
(`.claude/rules/ee.md`, subscriptions R5 as amended by D440).

**Nothing here is a token, a client secret, or a value any record is written
from.** The store's own records (M2, D935) live in a store of their own — the
`mcpauth/` prefix of a bucket whose only member is the front door's own
account (`mcp_server`, M4's) — and its tests reach that store through `internal/gcpfake` rather
than through a fixture: the generation-matched write those tests are about is
a property of a bucket, and a file in this directory cannot stand in for it.

The limits, the scopes and the rigor window (M1) are ordinary values and are
written in the tests themselves rather than in a fixture, so that a change to
the vocabulary is one file.
