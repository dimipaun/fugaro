# Website design reference (owner-approved, 2026-10-20)

These five boards are the owner-approved "bold" look for the home page and the product pages, drawn in Claude Design (artboards 1440 wide, inline styles). They are a REFERENCE for implementation: they are not built, not shipped and not served. Implement them in `website/src` with the site's own components, scoped CSS and its CSP rules; text and facts come from the site's data and content, never from these boards (the boards carry a few marketing headings that no source holds: use them, but every claim stays sourced).

| Board | Page |
| --- | --- |
| `HomeV3.dc.html` | home |
| `HowItWorksV3.dc.html` | how it works |
| `GettingStartedV4.dc.html` | getting started |
| `ConsoleV2.dc.html` | console |
| `CapsAndCostsV2.dc.html` | caps and costs (the per-recipe ranges section is removed until there is data) |
| `zoom-levels/level1.dc.html` .. `level4.dc.html` | the four zoom diagrams of How it works (owner-approved 2026-10-22, 1280x720 boards: inside one job, one orchestrator with many jobs, the developer's view, the whole project); drawn in Claude Design, the site redraws each as SVG |
