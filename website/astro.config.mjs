// website/astro.config.mjs — the site's Astro configuration.
//
// The site is static-first (the spec's stack sentence read as a constraint, F56):
// `output: 'static'` and no adapter, so `astro build` writes plain HTML and every byte a
// reader's browser receives was produced by this build. Nothing here fetches: there is no
// integration, no image service, no remote font and no adapter that talks to a host, so
// the build behaves the same with the network off as with it on (W1.1 step 2, D106).
//
// What is deliberately NOT here, and where it belongs instead:
//   - the canonical URL, the sitemap and the OpenGraph tags: the base layout (W1.3) reads
//     website/data/site.yaml, which is the one place a site-wide URL is written down;
//   - the styles and the palette tokens: website/src/styles/ (W1.2);
//   - the analytics loader: the base layout (W1.3) written so that W1.12's consent gate
//     decides whether anything is fetched, and nothing is loaded without consent.
//
// A build that needs no network is the point of the empty integration list: an
// integration is code that runs during the build and may fetch, and one added later has
// to be argued for rather than inherited.

import { defineConfig } from 'astro/config'

export default defineConfig({
  // No page compiles markdown since the docs hub links the CLI reference instead of rendering
  // it; if one does again, `--out` must not be rewritten to an en dash.
  markdown: { smartypants: false },
  // Static output: no server, no adapter, nothing rendered per request.
  output: 'static',
  // Every stylesheet is inlined into the page that uses it. The site's CSS is a few
  // kilobytes, so a page that carries its own styles costs one request instead of two, and a
  // built page is self-contained: the tests over `dist/*.html` (the reduced-motion base, the
  // base stylesheet) read what a browser receives without following a `<link>`. Without this
  // Astro inlines only below 4 kB, and the base stylesheet tipped the bundle over it.
  build: { inlineStylesheets: 'always' },
  // Stated rather than omitted, because "the shell ships no integration" is a rule this
  // site is held to and not a consequence of an absent line. An island that needs one
  // adds it in the task that needs it, with the byte budget that task carries.
  integrations: [],
})
