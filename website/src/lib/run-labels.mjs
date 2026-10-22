/**
 * website/src/lib/run-labels.mjs — the run, in the repository's own words (the owner's
 * approved draft of 2026-10-19, "How it works, draft 2 (diagram)"; the redesign's part P1).
 *
 * **Every label here is a line a document of this repository writes.** The box names, the
 * lines under them, the stage names and the cap facts are the repository's own words — the
 * README's `## How it works`, `## Why Fugaro`, `## Getting started` and `## Safety and cost
 * controls`, `docs/gcp-setup.md`'s budget and checkpoint sentences, the `parallelism` skill and
 * the changes spec's own example request. Nothing here paraphrases: each label goes through
 * `comparable()` and `holdLabels()` below stops the build, naming the file, when it is not in
 * the document it cites (Global Constraint 6).
 *
 * **The labels live here rather than in a component** because two components render them: the
 * how-it-works page's `RunDiagram.astro` draws the run, and the homepage's `HomeRunBand.astro`
 * lists its four steps. A second copy of a label is a copy that drifts from the README, and a
 * second copy of the guard is two guards that can disagree about what "the same line" means —
 * so the labels and the guard are written once and both components import them.
 * `website/test/how-it-works.mjs` and `website/test/home.mjs` read the built pages and hold the
 * lines to their documents from the outside as well.
 */
import { comparable, holdLabels, section, wholeFile } from './repo-quote.mjs'

export const SPEC = 'docs/website/redesign/changes-spec-2026-10-18.md'
export const PARALLELISM = 'plugin/skills/parallelism/SKILL.md'
export const README_HOW = 'README.md#How it works'
export const README_WHY = 'README.md#Why Fugaro'
export const README_SAFETY = 'README.md#Safety and cost controls'
export const README_START = 'README.md#Getting started'
export const GCP_BUDGET = 'docs/gcp-setup.md#Turning the model budget on'
export const GCP_RUN = 'docs/gcp-setup.md#Run, watch, diagnose'
export const DESIGN = 'docs/design/v1.md'

export const SOURCES = {
  [SPEC]: wholeFile(SPEC),
  [PARALLELISM]: wholeFile(PARALLELISM),
  [README_HOW]: section('README.md', 'How it works'),
  [README_WHY]: section('README.md', 'Why Fugaro'),
  [README_SAFETY]: section('README.md', 'Safety and cost controls'),
  [README_START]: section('README.md', 'Getting started'),
  [GCP_BUDGET]: section('docs/gcp-setup.md', 'Turning the model budget on'),
  [GCP_RUN]: section('docs/gcp-setup.md', 'Run, watch, diagnose'),
  [DESIGN]: wholeFile(DESIGN),
}

/** A line of the run: its text, and the document the text is a line of.
 *  @typedef {{ text: string, source: string }} Label */

/** @type {Record<string, Label>} */
export const LABELS = {
  // The reader's own agent, asking for the work: the changes spec's item 3 example.
  request: { text: 'Fan out these three bug fixes.', source: SPEC },
  // Fugaro's half of the run: one run per piece, the `parallelism` skill's own rule.
  perPiece: { text: 'One run per piece, each with a self-contained task', source: PARALLELISM },
  // The job: whose project it runs in, what it is, and how long it lives.
  ownProject: { text: 'in your own cloud project', source: README_WHY },
  jobKind: { text: 'Cloud Run job per repository workflow', source: README_HOW },
  oneExecution: { text: 'one execution per task, then gone', source: README_HOW },
  // The pull request: when it opens and in what state, README.md's own step 4.
  draft: { text: 'a draft opens at the first push (marked not verified)', source: README_HOW },
  // The band: the two caps, and that the run spends the reader's own credential.
  perRunCap: { text: "A run halts when its next call's worst case would pass the cap", source: GCP_BUDGET },
  dailyCap: { text: 'runs also share daily caps per repository and project', source: README_SAFETY },
  yourCredential: {
    text: 'There is no Fugaro service in between: it uses your Google Cloud project and your model credentials',
    source: README_WHY,
  },
  // The page's facts the diagram does not carry, in the page's own words under it.
  howItStarts: { text: 'the plugin’s working skill launches, watches and diagnoses runs', source: README_START },
  checkpoint: {
    text: 'The draft appears at the run’s first checkpoint push (within about a minute of its first commit), marked not verified',
    source: GCP_RUN,
  },
  caches: { text: 'Sync to the exact target commit and restore the dependency caches.', source: README_HOW },
  whatRemains: { text: 'The run record and the PR remain; the container does not.', source: README_HOW },
  boundary: { text: 'The trust boundary is the container, not the process.', source: DESIGN },
}

holdLabels(LABELS, SOURCES, 'website/src/lib/run-labels.mjs (the diagram’s labels)')

/**
 * The stages of the run, in order, and the loop the draft asks for: `review` sends a `fix`
 * back round, which is `docs/design/v1.md` §4's own arrow (`[fix ─► review(2)] …`). The
 * names are the run's stages rather than words written for the page, and
 * `website/test/how-it-works.mjs` holds them to the two documents that name them.
 */
export const STAGES = ['bootstrap', 'implement', 'verify', 'review', 'writeback']
export const LOOP = 'fix'

/**
 * The four boxes, and the band, each with the labels it carries. The list under the figure
 * is generated from this — same names, same labels, same order — so the words below the
 * picture cannot say something the picture does not.
 */
export const BOXES = [
  { name: 'Your agent', labels: [LABELS.request] },
  { name: 'Fugaro', labels: [LABELS.perPiece] },
  { name: 'Ephemeral cloud job', labels: [LABELS.ownProject, LABELS.jobKind, LABELS.oneExecution], stages: true },
  { name: 'Pull request', labels: [LABELS.draft] },
]

export const BAND_NAME = 'Spend caps watch the whole run'
export const BAND = [LABELS.perRunCap, LABELS.dailyCap, LABELS.yourCredential]

/**
 * The page's remaining facts, the ones the diagram does not carry: how a run starts, when
 * the draft appears, what a run syncs before it works, what outlives the container, and
 * where the boundary of a run is. Each is quoted like every label above.
 */
export const ALSO = [LABELS.howItStarts, LABELS.checkpoint, LABELS.caches, LABELS.whatRemains, LABELS.boundary]

holdLabels(
  Object.fromEntries(ALSO.map((label, index) => [`line ${index + 1} under the diagram`, label])),
  SOURCES,
  'website/src/lib/run-labels.mjs (the facts under the diagram)',
)
