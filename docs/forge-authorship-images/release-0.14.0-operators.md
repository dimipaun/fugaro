# 0.14.0: the operators' steps for the shared image registry

Draft of the release note section for the release that ships C8
(docs/forge-authorship-images/shared-images.md §10, §11). It is written here,
beside the design, and moves into `docs/releases/v0.14.0.md` under the
highlights: `docs/releases/README.md` asks for the post-deploy steps in the
same PR as the change, and this is that section. The file carries no title in
its published form.

### For operators

1. Run `fugaro update` once per installation, then `fugaro update --repo` in each repository's checkout, so the shared image registry and its grants exist before the first shared refresh.
