# 0.14.0 operator steps for the shared image registry (draft)

The step an installation must run after deploying 0.14.0, the release whose CLI
carries the shared image registry's Terraform (design
docs/forge-authorship-images/shared-images.md §10, §11): the repository and its
grants exist as soon as `fugaro update` runs a 0.14.0 binary, whether or not the
repository uses shared images yet. It is drafted here, beside the design, and
copied into the release notes' `### For operators` section by the 0.14.0 release
PR (`docs/releases/v0.14.0.md`), which is what an operator reads.

### For operators

1. Run `fugaro update` once per installation, then `fugaro update --repo` in each repository's checkout, so the shared image registry and its grants exist before the first shared refresh.
