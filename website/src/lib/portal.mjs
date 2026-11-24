// website/src/lib/portal.mjs: the one host this site sends a reader to for a licence key.
//
// A Fugaro licence key is issued by the signed-in portal (the portal plan's P9.3; the design
// `docs/key-signing-service/spec.md`), and the portal is Fugaro's own web app on the site's own
// domain with `app.` in front of it — the same pairing the licence service's
// own code derives when it sends an invite, where the intake host is `keys.` and the portal is
// `app.` (`ee/keyservice/invites.go`). The site no longer carries a form of its own: the key
// request page points at the portal, and the reader signs in and asks there, so this host is one
// the page LINKS to rather than one it posts to.
//
// The address is **derived from `site.yaml`'s canonical URL** rather than typed anywhere: the
// domain is a fact the site states once, so the page's link to the portal, the `allowed_hosts`
// entry the link check resolves it against, and the origin the tests pin all move with a
// one-line change to it. Two readers, one literal (the shape `landing.mjs` and the removed
// `keys-intake.mjs` state): the key request page builds its link from it, and the tests derive
// the host they expect from it.
export function portalOrigin(canonicalURL) {
  return `https://app.${new URL(canonicalURL).host}`
}
