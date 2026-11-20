# One location preference for an installation

Owner: Dimi Paun. Date: 2026-11-20. Written by the coordinator from the
owner's ruling.

### The ruling

The owner, 2026-11-20: at project setup, ask for a general location
preference (for example US east, EU), and drive the databases' locations
from it automatically. He agreed it is a spec of its own and that the task
which creates the budget database on every installation waits on it (D3029
of docs/always-a-budget).

### Why

Two databases are created in places fixed in code, and neither can ever be
moved once created: the run history's Firestore database (`us-east5`) and
the budget's Realtime Database (`us-central1`). An installation in Europe
gets both in the United States, permanently. The always-a-budget plan is
about to create the budget database on every installation.

### What is asked

1. An installation has one location preference, chosen once at setup, in
   terms a person understands (a broad place such as US or EU, not a list of
   product regions).
2. Every regional resource the installation creates follows it, by a mapping
   the product owns. Where a product does not exist in the preferred place,
   the mapping says which place is used instead and setup says so before
   anything is created.
3. Prefer one setting over two: setup already takes a region for the
   installation's compute. The preference and that region must not be able
   to disagree.
4. A location that can never be changed is chosen by the person once, up
   front. No later step needs its own confirmation for it.
5. An existing installation keeps what it has. Nothing is moved, deleted or
   recreated, and a database that already exists somewhere else is accepted
   as it is, not refused.
6. `fugaro update` is the whole upgrade: an installation set up before this
   work gets its preference recorded from what it already has, with no
   question and no other command.
7. Where the data lives is stated in the documents a customer reads
   (SECURITY.md, the website's security page).

### What the brief knows it does not know

- The full list of regional resources (compute, buckets, secrets and their
  replication, the image registry, the credentials service, both databases,
  the scheduler, anything else) and which of them are fixed in code today.
- Which places each product offers today. The Realtime Database exists in
  only a few; Firestore has regions and multi-regions.
- Which preferences to offer at first, and what "EU" promises: every
  resource in the EU, or only the data at rest.
- Whether model calls and the model providers' own regions are in scope of
  the promise (they leave the installation either way).
- What a workspace (several projects) does when its projects prefer
  different places.

### Out of scope

- Moving an existing installation's data to another place.
- Removing budget mode off (docs/always-a-budget), except its task AB7,
  which waits on this work.
