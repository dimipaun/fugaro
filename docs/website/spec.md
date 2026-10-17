# Fugaro website spec

## Goal
A public site for Fugaro at fugaro.dev (redirect fugaro.ai if acquired). Explains the tool, gets people installed fast, and looks bold and polished.

## Stack
- Astro (static-first); React islands only where interactivity needs it
- Hosting: Firebase Hosting or Cloudflare Pages
- Privacy-friendly analytics (e.g. Plausible or similar)

## Visual direction
- Dark theme, one electric accent colour (orange or violet), soft gradient glows
- Scroll-triggered animations; references: linear.app, vercel.com
- Deliver 2–3 palette options for the owner to choose from

## Pages / sections
1. **Hero**
   - Tagline candidate: "Fleeting cloud workers for coding agents: one task, one container, one PR, then gone."
   - Animated terminal typing a `fugaro` command and showing jobs fanning out
   - Slot for a 30–45 s explainer video (add once the console has visuals)
2. **How it works**: animated architecture diagram: local dispatch → cloud job → cache → security boundary → PR or artifact collected back
3. **Getting started**: install per OS: macOS, Linux; Windows via WSL (no native promise yet)
4. **Console downloads**: per-OS download links (placeholder until the console ships)
5. **Caps & costs guide**
   - How to set the project cap and default per-repo caps
   - Realistic expectations: tasks with review often run ~2 h, not 30 min
   - Ballpark time/cost ranges per task type, computed by averaging real run history from existing projects (agent to collect); publish as ranges, not promises
6. **Docs**: quick start, configuration, recipes, CLI reference
7. **Security & trust model**: link/render the SECURITY doc
8. **FAQ / comparison**: how Fugaro differs from other agent runners
9. **Changelog**: generated from GitHub releases
10. **Community**: GitHub repo, contributing guide, issues

## Open items
- Register domain (fugaro.dev preferred; check availability at a registrar)
- Produce the explainer video after the console exists
