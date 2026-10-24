// website/src/lib/zoom-levels.mjs: the four zoom diagrams of a run, in one place.
//
// `/how-it-works/` draws all four (`ZoomLevels.astro`), and since the redesign's Step 3 (the
// owner's rulings of 2026-10-23, levers 1 and 4) the homepage's one section about the run shows
// three of them beside the run's steps. The names and the alt texts are written once here, so
// the two pages describe the same picture in the same words.

/** @type {{ id: string, name: string, src: string, alt: string }[]} */
export const LEVELS = [
  {
    id: 'level-1',
    name: 'Inside one job',
    src: '/images/zoom/level1.png',
    alt: 'Level 1 of 4, inside one job. One run is one ephemeral cloud job. Checks run first when the recipe asks for them; the coder and the first-line reviewer go round; then a senior review judges the result and a draft pull request becomes ready only when it ships and the tests pass on the final commit.',
  },
  {
    id: 'level-2',
    name: 'One orchestrator, many jobs',
    src: '/images/zoom/level2.png',
    alt: 'Level 2 of 4, one orchestrator, many jobs. The orchestrator on the developer’s machine asks Fugaro for several runs; Fugaro checks the budget and launches the jobs in parallel; every job opens a draft pull request that flows back to the orchestrator.',
  },
  {
    id: 'level-3',
    name: 'The developer’s view',
    src: '/images/zoom/level3.png',
    alt: 'Level 3 of 4, the developer’s view. You work with an orchestrator agent on your laptop. It uses the Fugaro CLI, which launches runs in your cloud project; the console reads the same project through the CLI.',
  },
  {
    id: 'level-4',
    name: 'The whole project',
    src: '/images/zoom/level4.png',
    alt: 'Level 4 of 4, the whole project. Each developer works from a laptop or workstation with an orchestrator agent and the Fugaro CLI. Jobs, secrets, budgets, live counters and history, and who may launch all live in one cloud project; the coordinator and central orchestrators, the security service and the phone app are planned.',
  },
]
