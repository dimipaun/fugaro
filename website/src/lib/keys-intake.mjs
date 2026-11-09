// website/src/lib/keys-intake.mjs: the one host the site's key request forms post to.
//
// The key service (subscriptions plan slice S3, docs/design/key-signing-service.md) is
// owner-operated in its own project, and its internet-facing intake is the only service the
// site talks to: a reader asks for a key with a form, and the form posts to the intake
// directly rather than to a path on this site (the design's §7.2, decision D451 option (a)).
// Nothing else on the site reaches it — the CLI, the console and the app never call it, which
// is the design's R2 — and the privacy statement's own section for the sign-up data is S7.3's.
//
// The intake is served on a subdomain of the site's own domain, and the host is **derived from
// `site.yaml`'s canonical URL** rather than typed anywhere: the owner owns one domain, the
// intake's custom domain is mapped to the Cloud Run service at the deploy (the design's §11),
// and deriving it is what keeps pointing the site at a domain a one-line change — the page's
// three form actions, the CSP's `form-action` token the origin check holds the deployed policy
// to (website/test/origins.mjs) and the host `site.yaml` admits for the one form that posts
// off-site all move with it.
//
// Two readers, one literal (the shape `consent-key.mjs` states): the key request page builds
// its form actions from it, and the origin check derives the one host it admits a form action
// from it.
export function keysIntakeOrigin(canonicalURL) {
  return `https://keys.${new URL(canonicalURL).host}`
}
