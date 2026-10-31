---
paths:
  - "website/**"
---

# Website (Astro, fugaro.dev)

- Run `npm run check` (the build plus every `test/*.mjs`, about 7 minutes) before pushing.
- The tests pin copy, nav, CSP and wording. When a test fails because the wording changed, update the pinned fact; never make the test weaker.
- Go docs tests pin website content too (for example `internal/cli/docs_website_security_test.go`). Also run `go test ./internal/cli/ -run Docs`.
- Same-origin JavaScript is allowed since 2026-10-23. The CSP is `script-src 'self'` plus the analytics host and, since G2.3, the three hosts of the campaign pages' marketing consent (D401: Meta's pixel, LinkedIn's insight tag, Calendly) — nothing else, no wildcard except `https://*.licdn.com` in `img-src`, which the owner ruled in explicitly on 2026-10-31 (`script-src` stays wildcard-free), and every directive's exact token list is `test/origins.mjs`'s (`test/brand.mjs` holds the coarse one).
- Keep the nav labels and URLs, the logo, the palette and the privacy and legal copy unless the owner approves a change. The owner approved moving Why GCP under Project on 2026-10-23.
- Release summary lines in `website/data/release-summaries.yaml` carry no backticks. The changelog prints them as plain text.
- Every `docs/releases/vX.Y.Z.md` needs its line in `release-summaries.yaml`.
- `docs/releases/` may hold only `README.md`, the `vX.Y.Z.md` files and the `console/` and `mobile/` subfolders. `src/lib/changelog.mjs` throws on anything else.
- `test/privacy.mjs` scans the shipped code for hosts. Only the console files on its allow-list (`onboarding.ts`, `window.ts`) may name fugaro.dev.
- Deploying is `scripts/deploy-website.sh` or the website-preview/website-promote workflows, and only with the owner's authorisation. Never deploy from an agent run.
