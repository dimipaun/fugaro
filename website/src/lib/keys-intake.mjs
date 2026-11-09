// website/src/lib/keys-intake.mjs: the one host the site's key request forms post to.
//
// The key service (subscriptions plan slice S3, docs/design/key-signing-service.md) is
// owner-operated in its own project, and its internet-facing intake is the only service the
// site talks to: a reader asks for a key with a form, and the form posts to the intake
// directly rather than to a path on this site (the design's §7.2, decision D451 option (a)).
// Nothing else on the site reaches it — the CLI, the console and the app never call it, which
// is the design's R2 — and the privacy statement's own section for the sign-up data is S7.3's.
//
// The address is the intake's own subdomain of the site's domain — `keys.` plus the canonical
// host, which the design's §3.1 writes out in full and which S3.5's Terraform attaches to the
// intake's Cloud Run service as a custom domain, applied by S3.7 — and the host is **derived from
// `site.yaml`'s canonical URL** rather than typed anywhere: the domain is a fact the site states
// once, so the page's three form actions, the `form-action` token `website/test/origins.mjs`
// holds the deployed policy to, and the `allowed_hosts` entry the link check resolves the actions
// against all move with a one-line change to it. The host is a contract between the site and the
// service rather than a hostname the site chose alone.
//
// Two readers, one literal (the shape `consent-key.mjs` states): the key request page builds
// its form actions from it, and the origin check derives the one host it admits a form action
// from it.
export function keysIntakeOrigin(canonicalURL) {
  return `https://keys.${new URL(canonicalURL).host}`
}
