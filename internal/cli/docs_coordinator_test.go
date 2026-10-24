package cli

// The coordinator agent's docs package, pinned.
//
// docs/coordinator/ holds the owner's spec for a per-project coordinator agent
// (spec.md, verbatim), the reconciliation of that spec against the code
// (reconciliation.md), the index (README.md), and -- since 2026-10-21 -- the
// plan that turns the owner's answers into ordered parts
// (implementation-plan.md). Two parts have left this folder for the tree, and
// they are the only two: P1 landed on the long-lived `coordinator` branch as
// fugaro coordinator status (internal/cli/coordinator.go over
// internal/coordinator), and P3 as the state that command reads from -- the
// `coordinator/` subtree, the account that writes it and the read grant for the
// signed-in person (internal/coordinator/state.go,
// deploy/terraform/gcp/modules/firebase/iam.tf and
// internal/budget/rules/rules.json.tmpl). So what follows from these documents
// is that command, that subtree, one Terraform grant and one rules change, and
// no TypeScript and no live action.
//
// The reconciliation's whole value is that every claim in it is checkable, and
// the things a later edit can silently break are: the spec stops being verbatim,
// a citation drifts to a path that no longer exists, a citation into a document
// other works add to drifts off the sentence it meant, the work starts
// allocating decision numbers the owner never gave it, or the plan's parts stop
// being an order and become a list. Each of the thirteen tests below is one of
// those, plus the structural facts (the questions are numbered and optioned; the
// index states the status, the owner and the commit checked against; the five
// decisions planning found are numbered, contiguous and written recommendation
// first, and answered on 2026-10-21, the one answered against the recommendation
// included) and the two that read this file rather than the documents (the
// mutation record's count against the list it heads, and the map below against
// the tests it declares).
//
//  * TestDocsCoordinatorIsWritten -- the three files exist and carry the
//    load-bearing sentences: the status line, the owner, the date, the commit
//    the reconciliation was run against, and the facts that arrived with the
//    owner's answers of 2026-10-19 -- all sixteen questions are answered, and
//    none of the answers is an approval -- and with the answers of 2026-10-21:
//    the five decisions the planning found are answered, and the plan's owner
//    gate is passed for the parts that need only those decisions (P0-P6), with
//    the parts it did NOT open named in the same sentence -- and the sentence
//    that scopes the content anchors to the reconciliation's citations, so that
//    the index cannot widen it back over its own links into those trees, which
//    name no phrase and are held by no anchor.
//  * TestDocsCoordinatorSpecIsVerbatim -- spec.md carries the spec's own title,
//    status and eleven sections, and carries none of the reconciliation's
//    apparatus: the two documents have not been mixed. Those are sentences, and
//    the index's promise is about bytes, so the file is pinned by its SHA-256 as
//    well -- the one check that fails when the file is edited anywhere else.
//  * TestDocsCoordinatorNewDecisionsAreNumbered -- the five decisions planning
//    found are D193-D197, written in the reconciliation's §6 as `### D<n> — ...`
//    headings in order and with no gap, each stating its recommendation before
//    its alternatives, and the index says of them that they are answered
//    2026-10-21. The old test here asserted the opposite -- that this work
//    allocated no decision number at all -- and it is replaced rather than
//    deleted: an answer picks an option and allocates nothing, which is still
//    true and is still pinned, but the planning that followed the answers found
//    five things the answers do not settle, and those are decisions.
//  * TestDocsCoordinatorDecisionsAreAnswered -- the five decisions are ANSWERED,
//    not merely written: each carries the owner's answer of 2026-10-21 under it,
//    **D196 against the recommendation** and the other four as recommended, the
//    section no longer says none of them is answered, Q10's recorded answer of
//    2026-10-19 is amended under rather than rewritten, the plan's gate table
//    records the same answer in each decision's own row, and the plan's gate
//    says it passed for P0-P6 while naming the parts it did not open.
//  * TestDocsCoordinatorPlanIsWritten -- the plan exists, is at the status the
//    index gives it (approved to build on the branch `coordinator`, target
//    0.11.0, partly implemented: P0 and P1 on the branch), carries P1's own
//    "Status: built" in P1's section, carries its parts P0..P8 contiguously
//    and in order, carries the one section heading the brief asked for by name,
//    names every decision D193-D197 with the part it blocks, marks the paths it
//    cites that do not exist yet as new, and carries no sentence saying the
//    coordinator is a role another service plays -- the reading D196 replaced.
//  * TestDocsCoordinatorPlanCitationsExist -- every repository path the plan
//    cites resolves -- the files, and the directories, each against an
//    independent census of the shapes the document writes, the same way the
//    reconciliation's citation check is measured -- with the plan's own
//    exception lists: for the paths it declares new, which must NOT exist yet,
//    so the day one of them lands the entry fails instead of silently excusing
//    a citation that would otherwise be checked; and, by name, for the
//    directory-shaped tokens that name a Realtime Database node rather than a
//    directory in this tree, which D195's `coordinator/` subtree is.
//  * TestDocsCoordinatorCitationsExist -- every repository path reconciliation.md
//    cites resolves on disk, in either the full form or the
//    deploy/terraform/gcp/-relative form the Terraform modules are cited in, in
//    every citation shape the document uses -- including a shortened reference
//    that names its file by base name alone and carries a line number, which is
//    resolved against the tree and refused when the name fits no file or more
//    than one -- and the check is measured against two independent counts: of
//    the document's path-looking tokens, and of the tokens that attach a line
//    number to a name, the second of which fails on any token no matcher
//    claims.
//  * TestDocsCoordinatorRelativeCitationsResolve -- every relative second
//    reference, in both of its shapes -- `:89`, `:14-24`, `:127 notCovered`,
//    and the symbol-first `sanitizeText:2208` -- resolves against the nearest
//    full-path citation before it in its own paragraph, to a line that exists,
//    and -- where a symbol is named -- to a symbol within a few lines of it.
//  * TestDocsCoordinatorNumbersTableIsReadFromItsSources -- the README's numbers
//    table is a reading of six other documents' allocations: every citation in
//    the section is one the test pins, every block either row states is named
//    with its state and read from the one line of the document it cites that
//    carries the block's own label, that line still carries it, and no range the
//    checks row calls unwritten covers a number the register has written.
//  * TestDocsCoordinatorFrequentCitationsAreAnchoredByContent -- the other half
//    of the same rule, on the reconciliation: none of its citations into
//    `docs/design/` or `docs/mobile/` carries a line number, every one of them
//    that names a file here is held to the single line of its document that
//    carries the phrase the citation turns on, and the anchors are counted
//    against the citations document by document, so a claim added against a
//    document that already has an anchor fails until it has one of its own --
//    the case a document-level check passes.
//  * TestDocsCoordinatorQuestionsAreNumberedAndOptioned -- Q1..Q16, contiguous,
//    each with at least two options, a recommendation and the owner's recorded
//    answer of 2026-10-19.
//  * TestDocsCoordinatorMutationRecordIsCounted -- the mutation record's own
//    count, checked against the list under it. The record is prose and cannot
//    be run, so the one thing about it that can be checked is that it does not
//    claim more mutations than it lists: each mutation is one bullet carrying
//    one arrow, and the header states the total in digits for this test to read
//    and compare. The count had drifted by two -- "twenty-six" over a list of
//    twenty-four -- which is the same silent-overclaim the citation census
//    exists to catch in the document.
//  * TestDocsCoordinatorSelfMapIsCounted -- the other test that reads this file
//    rather than the documents: the number of coordinator tests it declares, the
//    count its own map states over them, the one bullet per test the map carries
//    and the index's sentence about it are held to each other. They went stale
//    together once -- "eleven" over twelve tests -- because a count is the claim
//    that moves while nothing about what it counts looks wrong.
//
// Mutation (verified, run and restored) -- 97, one per way these documents can
// rot, each planted and reverted. The count is a digit and not a word, and
// there is only one of it, because TestDocsCoordinatorMutationRecordIsCounted
// reads it and counts the list under it: one arrow per mutation, so a number
// the list does not bear out fails the package like any other count that claims
// more than it sees. Written as a word it drifted -- "twenty-six" over a list
// of twenty-four -- and a word beside a digit would have let the two disagree
// without either looking wrong.
//
//	TestDocsCoordinatorIsWritten
//	  delete the README's status sentence -> fails, naming it;
//	  put the README's status sentence back to what it replaced ("plan written; no
//	  code is implemented; approval gate open") -> fails: the line is pinned
//	  whole, and the state it names is the one this round ended, so a rewrap that
//	  keeps the branch and drops the release is exactly the edit it catches;
//	  delete the README's gate-passed sentence -> fails: it is the permission a
//	  builder acts on, and the parts it does NOT open are named in the same
//	  sentence, so the pin has to hold the whole of it;
//	  delete the reconciliation's "The owner answered all sixteen" sentence ->
//	  fails;
//	  delete the README's "All sixteen questions are answered" sentence -> fails;
//	  drop "and none is approved" from the README's answered sentence -> fails:
//	  what is left says the questions were answered, not that answering one
//	  approves nothing;
//	  delete the README's "An answer is not an approval" sentence -> fails;
//	  revert the README's numbers row to "its open items are Q1–Q16" -> fails;
//	  widen the README's sentence about the anchors back to "holds every one of
//	  them" -> fails: the sentence is pinned, and the citations it would cover
//	  include the designs that live only in open pull requests, which have no line
//	  for an anchor to be held to;
//	  widen the README's other sentence back over the index's own citations into
//	  those trees (dropping the words that except its numbers section) -> fails:
//	  the numbers table's citations into the same trees are held, block by block,
//	  by this same file, so the sentence would deny coverage this package has.
//	TestDocsCoordinatorSpecIsVerbatim
//	  paste a `### Q1 — ` heading into spec.md -> fails;
//	  delete `## 9. Long-running behaviour` -> fails;
//	  change one byte of the spec's prose ("and suggests sequencing" written
//	  "and suggest sequencing": one character, no heading touched) -> fails,
//	  naming the digest: the two mutations above are mutations of the lines this
//	  test names, which is all it named before this round, and the index's
//	  promise is that the file is byte for byte the owner's. This is the P0 red
//	  step, run against the guard as it stood: it passed.
//	TestDocsCoordinatorNewDecisionsAreNumbered
//	  renumber the last of the five, D197 to D198 -> fails: the
//	  headings are read in order and held to the block the index records;
//	  delete one of the five -> fails, naming the missing number;
//	  swap a decision's two paragraphs, so its alternative comes first -> fails:
//	  recommendation-first is the whole of what "written to the recommendation"
//	  means, and the file's own order is the only thing that makes it checkable;
//	  write a decision's recommendation as option (B), leaving the alternative
//	  labelled (B) too -> fails: the letter is checked, not only the word, so a
//	  decision whose recommendation moved is not quietly still "recommended";
//	  put the README's "written, not answered" state back for D193–D197 -> fails:
//	  the owner answered all five on 2026-10-21, and the index has to say so.
//	TestDocsCoordinatorDecisionsAreAnswered
//	  record D196 as option (A) -> fails: it is the one decision answered AGAINST
//	  the recommendation, and it is the one that allocates work between two
//	  plans, so a document that files it under (A) describes the plan's own
//	  recommendation as the owner's ruling;
//	  delete D194's answer paragraph -> fails, naming D194: four answers and one
//	  decision still reading as open is a plan whose parts are half-gated, and
//	  the per-decision loop is what makes the missing one a failure rather than a
//	  shorter document;
//	  put the section's "**None is answered.**" sentence back -> fails: it is the
//	  claim that kept every part behind the gate, and the reconciliation may not
//	  deny an approval the plan records;
//	  delete the "**Amended 2026-10-21**" note under Q10 -> fails: Q10(A) is the
//	  owner's answer of 2026-10-19 and is left as it was given, so the amendment
//	  is the only thing saying D196 changed what the plan does;
//	  drop D196's answer from its gate-table row -> fails: the table is where a
//	  reader learns what each part waits on, and an answer recorded in the
//	  reconciliation and missing from the row leaves P7 reading as blocked;
//	  revert the plan's gate to the sentence it replaced ("No part of this plan
//	  starts before the owner passes this gate") -> fails: the permission and the
//	  state are the pair, and the old sentence is a plan nobody may start;
//	  delete "**P7 and P8 still wait**" from every place the plan writes it (it
//	  carries the sentence four times, and the pin reads the document, so
//	  removing one of the four is deliberately not a mutation) -> fails: the
//	  failure mode of a gate sentence is that it widens, and a reader who takes
//	  "passed" without the second half starts P7 on a front door nobody has
//	  reviewed.
//	TestDocsCoordinatorPlanIsWritten
//	  move P4 after P5 -> fails: the parts are an order, not a list;
//	  delete the "## What the owner must answer before any part starts" heading ->
//	  fails, naming it;
//	  drop a decision's row from the gate table (D193's, or D196's) -> fails,
//	  naming the decision: the table is read from its own section, so naming the
//	  decision somewhere else in the plan does not stand in for its row;
//	  restore the old status line ("reconciliation only; ... no plan is
//	  approved") -> fails;
//	  put the reading D196 replaced back into P7 ("the coordinator as a role it
//	  plays", one of the six phrases the check matches) -> fails: the plan
//	  carried that reading in four places, so it is matched as a phrase rather
//	  than pinned as a line, and a rewrite that fixes three of them fails here;
//	  delete "**its own resident service**" -> fails: whose service the front
//	  door is, is the whole of what D196 decided;
//	  drop "**whichever part lands first admits it and the other cites it**" ->
//	  fails: the two plans share one allow-list admission, and the part that
//	  lands second must cite it rather than add it again.
//	TestDocsCoordinatorPlanCitationsExist
//	  rename a cited path in the plan to `internal/coordinatorX/` -> fails,
//	  naming it;
//	  create the file the plan declares new (`internal/cli/coordinator.go`) ->
//	  fails: an exception that has outlived the thing it excused is a citation
//	  nobody checks;
//	  rename a cited DIRECTORY in the plan (`internal/coordinator/` written
//	  `internal/coordinatorX/`) -> fails, naming it: the path check above is
//	  written to find a file extension, and this is the citation it cannot see;
//	  create the directory the plan declares new (`internal/coordinator/`) ->
//	  fails, naming P1's own Files line as what excused it: the same expiry the
//	  file entry has, and the one that fires when P1 lands;
//	  drop one of the directory-shaped tokens that names a Realtime Database
//	  node (`config/`) from the plan -> fails, naming the now-unused entry: an
//	  exception that outlives its sentence is a token nobody is checking, the
//	  same rule the citation exception maps follow;
//	  add a slash-ending token in a shape the directory regexp does not know (a
//	  path starting at the root, `/srv/fugaro/`) -> fails, against the directory
//	  census, which is written differently from the check on purpose;
//	  rename the symbol a possessive pairing names (`GetETag` in
//	  internal/rtdb/client.go, which the plan cites as `internal/rtdb/client.go`'s
//	  `GetETag`) -> fails, naming the symbol and the file: the path still
//	  resolves, which is the half a path check cannot see;
//	  make the pairing regexp stop at the newline where it now allows any
//	  whitespace (`'s\s+` -> `'s `) -> fails, against the possessive census: the
//	  plan wraps one pairing across two lines, so the narrower regexp finds one
//	  pairing fewer and the census is what says so. Wrapping the plan's pairing
//	  itself is not a mutation -- the regexp reads across the wrap, which is why
//	  it is written to;
//	  reword the plan's one audited prose possessive so the phrase
//	  coordinatorPlanProsePossessives excuses no longer appears (the plan's
//	  `...`'s list` becomes "the list `...` carries") -> fails, naming the entry:
//	  an exception that has
//	  outlived its sentence is a pairing nobody is checking, the same rule the
//	  citation exception maps follow;
//	  delete the index's drift section -> fails: three other folders' readings of
//	  this register are recorded nowhere else.
//	TestDocsCoordinatorCitationsExist
//	  rename a cited path to `internal/budget/modelX.go` -> fails, naming it;
//	  put a line past the end of a cited file (`internal/budget/model.go:99999`,
//	  and the same on a second citation of a file cited twice above) -> fails,
//	  naming the line and the file's length, where before this check the path
//	  resolved and the line was never looked at;
//	  cite a nonexistent path in the `path:line Symbol` shape the old regexp
//	  missed (`internal/task/taskNOPE.go:328 NewRunID`) -> fails, naming it.
//	  Run against the old regexp with the census check removed, the same
//	  citation is not reported at all -- that is the hole this shape closed,
//	  and the reason the census is checked first;
//	  restore the old shape-blind regexp -> the census assertion fails,
//	  reporting "217 path-looking tokens and the citation check matched 182";
//	  make the regexp line-bounded again -> fails in the relative check below,
//	  because that loses the anchor `:127 notCovered` resolves against;
//	  put a line past the end of the file a shortened reference's name resolves
//	  to (`apptoken.go:9999`) -> fails, naming that file and its length, where
//	  before this round the token was skipped in silence and no check read it;
//	  shorten a reference to a name no file carries (`nosuchfile.go:89
//	  grantRules`) -> fails, saying no file in the tree is named it;
//	  shorten one to a name six files carry (`main.go:89 grantRules`) -> fails,
//	  listing them: a shortened name is a claim about one file, and the check
//	  will not choose one for it;
//	  name a symbol the resolved file does not have beside the line
//	  (`cover.go:1569 grantRules`) -> fails, naming the window it searched.
//	  The three shortened references the document carries were checked by no
//	  test before this round, and the path census could not see it: they are
//	  path-looking tokens like any other. The distinct-path count could -- it
//	  read 94 then and reads 96 now, which is why both counts are logged.
//	TestDocsCoordinatorRelativeCitationsResolve
//	  put the literal `:...` back where the F36 line reference is -> fails: a
//	  placeholder is not a line number;
//	  cite `:14024` against `internal/gateway/gateway.go`, a 573-line file ->
//	  fails, past the end of the file;
//	  cite `:1190 Start` for the same -> fails the same way, and does not slice
//	  out of range: the symbol window is only reached for a line that is there;
//	  name `:209 denyWindowGone` -> fails: no such symbol near the line;
//	  write the same reference as a bare `:83 tokenPermissions` after an anchor
//	  that is another file (secretmounts_test.go) -> fails on the symbol. This
//	  is the document defect this check was written for, reproduced: the
//	  paragraph's last full path before the reference was not the file the
//	  reference meant, and nothing could see it;
//	  delete the full-path citation that anchors the D143/D145 bullets ->
//	  fails three times, naming the references with no anchor;
//	  put the symbol-first reference's line past the end of the file its
//	  paragraph names (`sanitizeText:99999`) -> fails, past the end of the file;
//	  rename the symbol it names (`safeUrlGone:2477`) -> fails: no such symbol
//	  near the line, which is the check the shape had none of before this round;
//	  delete the citation of the console's sanitizer module, which anchors all
//	  three symbol-first references -> fails three times, each one resolving
//	  against the paragraph's previous citation (the console renderer's app
//	  module, 344 lines) and reporting its line past the end of that file: the
//	  anchor is the paragraph's, and a reference that loses its own file
//	  silently inherits another one.
//	TestDocsCoordinatorCitationsExist (the line census)
//	  write a line reference in a shape no matcher knows
//	  (`sanitize-text:2208`, hyphenated) -> fails: the token attaches a line to
//	  a name, the census sees it and no check claims it. This is the assertion
//	  that makes the symbol-first shape's arrival a failure rather than a
//	  silence, which is what it was: three citations, no matcher, and a census
//	  looking for file extensions that could not see them.
//	TestDocsCoordinatorNumbersTableIsReadFromItsSources
//	  put the checks row's old claim back -- 70–79 "unused and deliberately not
//	  taken" -> fails: 70 is inside that range and the register writes `## Check
//	  70a`, so the range rule rejects it. This is the defect this round fixed;
//	  write 71–79 as 70–79 in the row -> fails: the row no longer says of 71–79
//	  that it is inside the website work's held range;
//	  change a block's number in the row (D1–D20 written as D1–D21) -> fails:
//	  the row no longer names D1–D20, and the number the row states is the one
//	  the table's own readers look for. This is the round's own mutation: the
//	  row is where the numbers are stated, so a row stating a different one has
//	  to fail whether or not the document behind it moved;
//	  read D1–D20 from another document -> fails: the citation after the block
//	  is the redesign's README, not the mobile one;
//	  delete the mobile table's `| **D1–D20** |` row -> fails: the document
//	  carries no line with the anchor, and the anchor is the block's own label;
//	  paste a second copy of that row in -> fails, naming both lines: an anchor
//	  that names two lines is a citation two readers would follow to different
//	  places, which is what choosing one has to make impossible;
//	  put a line number back on a citation in the numbers section
//	  (`docs/mobile/README.md:25` for D1–D20) -> fails: the section's citation
//	  matcher reads the path and the line spec together, and the block's own
//	  citation is the one that carries the number;
//	  drop 70a–70c from the row -> fails: the row no longer names a block the
//	  register allocates, and the block's own document is named by no pin;
//	  drop the credentials-service citation after 90–99 -> fails: the citation
//	  the row leaves behind is the checklist's, so the row reads 90–99 from the
//	  register that explicitly does not reach it;
//	  move 71–79's citation to another document -> fails: that document carries
//	  no line holding the website work's held range;
//	  strip the register's `## Check` headings -> fails: the range rule has no
//	  written number left to judge a claim against, and says so rather than
//	  passing;
//	  change 105–109 to 100–109 in the state cell -> fails: the row names no
//	  105–109, so the pin has no block to read it from;
//	  delete the numbering-ruling citation from the note -> fails: the claim is
//	  stated with no document behind it;
//	  widen the D-block's gap to D78–D89 -> fails: the row no longer names
//	  D79–D89, and the anchor's line records the gap as D79–D89;
//	  call D180–D192 taken -> fails: the row no longer says of it that it is
//	  proposed;
//	  merge the console's two blocks back into D1–D38 -> fails: the row no
//	  longer names D21–D38, and the table it is read from splits them;
//	  call a held block unused (`**D55–D78** managed projects, unused`) ->
//	  fails: the range rule rejects it, because the allocation's own documents
//	  record D55–D78 as held;
//	  put the checks row's old state back for 100–104 (`**100–104** are
//	  **proposed, not approved**`, read from `docs/mobile/README.md`) -> fails
//	  twice: the row no longer carries the plan's own label, and the document the
//	  pin reads the block from is the mobile README, not the plan that approved
//	  those five checks;
//	  put a bare relative line reference back in the numbers section (a `:25`
//	  beside a block, with the document taken from the sentence around it) ->
//	  fails: the section's citation matcher reads that shape and refuses it, and
//	  before this round the matcher required a full `path.md` before the line
//	  spec, so the token was invisible -- the one citation shape in the section
//	  that names no document at all was the one nothing could see.
//	TestDocsCoordinatorFrequentCitationsAreAnchoredByContent
//	  put a line number back on a citation into one of those trees
//	  (`docs/design/v1.md:26`, the shape every one of these citations had) ->
//	  fails, naming the path and the number: the check the number used to get
//	  read the line's existence and never its sentence, so putting one back is
//	  putting the silent failure back;
//	  the same on a mobile document (`docs/mobile/spec.md:9`) -> fails;
//	  change an anchor to a phrase its document does not carry
//	  (`agent.models.coder|reviewer|referee` for the model keys) -> fails,
//	  naming the document and the phrase: a citation by content is a claim that
//	  the document carries the sentence, and that claim is the check;
//	  paste a second copy of an anchored line into `docs/design/desktop-console.md`
//	  -> fails, naming both lines: an anchor that names two lines is a citation
//	  two readers would follow to different places;
//	  cite a `docs/design/` document with no anchor behind it
//	  (`docs/design/project-centric.md`) -> fails: the citation names a document
//	  and no longer says where in it, which is the state the line numbers left
//	  every one of these in;
//	  delete the two `docs/mobile/spec.md` anchors -> fails on every citation of
//	  that document, which is what makes the table complete rather than a
//	  reading of the entries somebody remembered to write;
//	  add a second citation of a document that already has an anchor (a new
//	  sentence citing `docs/design/v1.md` beside the four there) -> fails,
//	  naming the citation count and the anchor count: one anchor holds one
//	  citation, and a claim added against an already-anchored document is
//	  exactly what a check comparing documents rather than citations passes;
//	TestDocsCoordinatorQuestionsAreNumberedAndOptioned
//	  delete Q9 -> fails on every question after it;
//	  leave Q12 with one option -> fails;
//	  rename Q14's "Recommended" -> fails;
//	  restore the section heading "## 3. Open for the owner (Q1–Q16)" -> fails:
//	  the questions are answered now, and the heading says which;
//	  strip the "Answered 2026-10-19: option (A)" line out of Q7 -> fails,
//	  naming Q7: a question with no answer reads as one that is still open.
//	TestDocsCoordinatorSelfMapIsCounted
//	  put this file's own count back to "twelve" over the thirteen tests it
//	  declares -> fails: the map's count and the tests the file declares are read
//	  against each other, which is the drift this round found;
//	  delete the map's bullet for one of the tests -> fails, naming it: a test the
//	  map does not list is a check no reader of the map knows is there;
//	  put the index's sentence back to "Twelve tests pin everything above" ->
//	  fails: the index states the same count, and the two readings are one claim;
//	  add a fourteenth `TestDocsCoordinator...` test with no bullet -> fails on
//	  every reading at once -- the declared tests, the map's count and the index's
//	  sentence -- which is what makes the map a list rather than prose about one.
//
// One change is deliberately NOT in that list, because it is not a way a
// document can rot and the record counts only those: insert a row above every
// block in `docs/mobile/README.md`'s numbers table and
// TestDocsCoordinatorNumbersTableIsReadFromItsSources still passes. That is the
// whole point of the content anchors and it is checked by running it. The line
// pins the anchors replaced failed on exactly that change, all at once, which
// is why they were replaced: a row inserted into somebody else's table is a
// change this work cannot see and must not have to chase.
//
// The same change in the reconciliation's citations is not counted either, and
// for the same reason: insert a paragraph above every anchor in
// `docs/design/v1.md` and TestDocsCoordinatorFrequentCitationsAreAnchoredByContent
// still passes. That is what the 21 citations into those two trees that carried a line number --
// 20 full paths holding 22 numbers between them, and the relative `:33` beside them -- could not
// do: the reconciliation already says so of one of them, in its own words at the Claude price
// table, where it cites a function by symbol because "a line range here was already wrong once".
//
// The numbers above are a reading of the document at the commit that last
// edited this comment, not a constant, and they have gone stale every time the
// document has grown: the census figures were recorded as 196/163 when the
// document was smaller, then as 214/180 three commits later, and the same
// mutation now reports 217/182. The live readings are the assertion's own
// message and the two log lines -- "N citation occurrences over P distinct
// paths, against a census of M path-looking tokens" and "N relative citations
// resolved against their paragraph's anchor". Anyone re-running a mutation
// should read the numbers there rather than here, and update this comment to
// whatever they say: a number quoted here is a witness that the mutation fires,
// not a fact about the test.
//
// The first of those two lines prints both citation counts because the census
// is compared with one of them and the line used to print the other: the
// assertion measures occurrences against the census, and the line reported
// distinct paths beside it, so the pair a reader took away was never the pair
// that was compared.
//
// The exception list is the one thing here a mutation cannot prove by itself,
// so it is checked the other way: the test fails when an entry goes unused.
//
// THREE HOLES, FOUND BY REVIEW IN THREE ROUNDS, each closed by the round that
// found it or recorded here as still open. They are worth keeping together
// because they are the same failure -- a check that silently sees less than it
// claims to -- arriving three times.
//
//  1. The symbol named BESIDE a citation was not checked. A sentence reading
//     "`DailyMicros` at `internal/budget/evaluate.go:31`" passed the whole
//     package while DailyMicros is declared in internal/budget/model.go:
//     evaluate.go:31 is ReasonRepoDailyCap. STILL OPEN, and the reason is
//     scope: the symbol sits outside the backticks, so pairing it with a path
//     means reading the sentence. Two checks do test the symbol where the
//     document names one -- the relative references below, and a shortened
//     reference that carries its line (`cover.go:89 grantRules`) in the
//     citation check above -- and that is the half of this that is mechanical:
//     the symbol there is inside the reference's own token.
//  2. The extractor's regexp required the closing backtick to follow the path
//     or its `:line` suffix, so the document's dominant `path:line Symbol`
//     shape never matched and nine cited paths were never checked. CLOSED, and
//     the census count is what keeps it closed: a check that silently sees
//     less is the failure mode, and a count is what turns it into a failure.
//  3. That same regexp stopped at a newline, so the three files the document
//     cites in a shape whose symbol wraps were invisible -- and two of them
//     were the anchor a relative reference resolved against. CLOSED; the
//     relative check is what makes it matter, since a lost anchor silently
//     re-points the reference at another file.
//
// A general checker for (1) was prototyped and deliberately not shipped:
// pairing identifiers with citations needs a notion of scope the prose does not
// carry (one parenthetical may cite a single file for a list drawn from three,
// and a paragraph names symbols belonging to other citations in it), and the
// coarse version reported hundreds of false positives -- every symbol in a
// paragraph paired with every path in it. Doing it properly means reading the
// sentences, which is what a reviewer does. The narrow version that IS
// mechanical -- a symbol inside the same backticked token as the line it is
// attached to, in any shape the document uses -- was also tried against the
// whole document and rejected as too loose to trust: three citations name a Go
// selector (`Claims.RB`), a terraform resource type or a slash-joined list of
// constants, none of which appears literally at the line named, so the check
// would have had to accept one of several candidate substrings and would have
// passed almost everything. The relative references below do not have that
// problem: each names exactly one symbol. So this file checks symbols where the
// document's own grammar makes the pairing unambiguous, and says so, rather
// than presenting a fuzzy check as a complete one.

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	coordinatorReadme     = "docs/coordinator/README.md"
	coordinatorSpec       = "docs/coordinator/spec.md"
	coordinatorReconciled = "docs/coordinator/reconciliation.md"
	coordinatorPlan       = "docs/coordinator/implementation-plan.md"
)

// coordinatorBase is the commit the plan was written against: the commit that
// brought docs/central-orchestrator/ in, which is what the plan's §1 reads to
// say what changed since the reconciliation's c6009b5f. Like
// coordinatorCheckedAgainst it is a separate constant because the test asserts
// the document NAMES it, not that HEAD is it.
const coordinatorBase = "1e0953b7"

// coordinatorReadmeStatus is the index's status line after P3 landed on the
// `coordinator` branch. It replaces the "approved to build ... no code is
// implemented yet" status this package pinned while the plan was all there was,
// and the P1 one before this, and it is pinned whole: what a reader has to take
// from it is that the work is APPROVED TO BUILD, on which branch, for which
// release, and HOW FAR IT HAS GOTTEN -- which parts are in the tree and which
// are not -- and a rewrap that keeps the branch and drops the release, or drops
// the parts, is exactly the edit this catches.
//
// The move is the repository's convention, not a preference: a status line moves
// in the commit that lands a part (docs/mobile/README.md, "partly implemented --
// P0.1, P0.2, ... are merged"; docs/setup-wizard/README.md, "approved to build;
// implementation in progress"), because the alternative is a folder whose
// documents say no code exists while the code is on the branch, with a test
// pinning the false sentence.
//
// It has moved four times: with the owner's answers, with P1, with P2 and -- on
// the same day -- with P3. P3 is the part that made the index's "no Terraform, no
// rules change" sentence false, and the two are edited together for that reason:
// a grant and a read are what it lands.
const coordinatorReadmeStatus = "**Status (2026-10-24): approved to build on the branch `coordinator`, target 0.11.0; partly implemented — P0, P1, P2 and P3 are on `coordinator`, and P4–P8 are not started.**"

// coordinatorReadmeDecisionsAnswered is the index's own sentence about the
// decisions the planning found, after the owner answered them on 2026-10-21.
// It replaces the "written, not answered" state this package pinned while the
// gate was open, and it is pinned for the reason that one was: the answered
// state is the claim a later edit softens first, and a README that went back to
// calling D193-D197 unanswered would say the gate never passed.
const coordinatorReadmeDecisionsAnswered = "are **answered 2026-10-21**"

// coordinatorReadmeDriftHeading is the index's section recording what writing
// into the D193 block made stale elsewhere: three other works read this register
// at their own base commits and concluded D193 onward was free, and their
// documents were not edited from here.
const coordinatorReadmeDriftHeading = "## What still reads this register the old way"

// coordinatorPlanGateHeadingRE is the one section heading the brief for this
// plan asked for by name, because that gate is what a reader has to find and a
// heading reworded is a gate nobody looks for. It is matched as a whole `## `
// heading rather than as a substring, and its section number is left free so
// that inserting a section above it is not a failure -- what is pinned is the
// gate's own words and that it is a section of its own.
var coordinatorPlanGateHeadingRE = regexp.MustCompile(`(?m)^## [0-9]+\. What the owner must answer before any part starts$`)

// coordinatorPlanGateColumns is the gate table's own header row: the brief
// asked for a table of the decision, what it blocks and the recommendation the
// plan is written to, and those three columns are what makes it answerable. A
// table that lost its third column would leave the owner choosing between
// options with no statement of which one the plan assumes.
const coordinatorPlanGateColumns = "| Decision | What it blocks | The recommendation the plan is written to |"

// coordinatorPlanStatus is the plan's own status line, the same statement the
// index makes, held separately because the plan is the document a builder reads
// first and it must not be able to say something else. It has moved four times:
// with the index when the owner answered D193-D197, again with the index when P1
// landed, again when P2 did, and again when P3 did -- a plan whose parts are
// being built while its own status still says none is the mismatch this pair of
// pins exists to catch, and so is one that says a part is built while the
// part's own section says otherwise (see coordinatorPlanBuiltStatus).
const coordinatorPlanStatus = "**Status (2026-10-24): approved to build on the branch `coordinator`, target 0.11.0; partly implemented — P0, P1, P2 and P3 are on `coordinator`, and P4–P8 are not started.**"

// coordinatorPlanBuiltStatus is a part's own status line in its section, in the
// shape P0's carries ("**Status: written.**"): the status line above says how far
// the plan has got, and each part says whether THAT part is in the tree, so that
// the count in the status line and the parts it counts cannot drift apart. P1, P2
// and P3 are the parts this pair of pins moved for -- P0's own line reads
// "**Status: written.**", and the constant holds the built shape the others
// carry -- and it is pinned whole for the same reason the two above are.
const coordinatorPlanBuiltStatus = "**Status: built.**"

// coordinatorPlanParts are the plan's parts in order, as their headings are
// written. Contiguity and order are the assertion: the plan's whole claim is
// that its parts are an order with dependencies, and a P-number that moved or
// disappeared while its heading stayed would leave the prose describing a
// sequence the headings no longer carry.
var coordinatorPlanParts = []string{
	"### P0 — ", "### P1 — ", "### P2 — ", "### P3 — ", "### P4 — ",
	"### P5 — ", "### P6 — ", "### P7 — ", "### P8 — ",
}

// coordinatorPlanDecisions are the five decisions the planning found, in the
// order the plan's gate table lists them. They are numbered in the owner's
// global block, not a folder-local one, and each is pinned by its heading in the
// reconciliation and by its name in the plan's table.
var coordinatorPlanDecisions = []string{"D193", "D194", "D195", "D196", "D197"}

// coordinatorCheckedAgainst is the commit the reconciliation was run against:
// the release commit of v0.8.6, which is `origin/main` when this package was
// written. It is a separate constant because the test asserts the documents
// name it, not that HEAD is it -- the reconciliation is a dated reading of one
// commit, and a later commit does not make it wrong.
const coordinatorCheckedAgainst = "c6009b5f"

// coordinatorAnsweredMarker is how every one of the sixteen questions records
// the owner's answer of 2026-10-19: the option taken, in place, under the
// question. All sixteen took option (A), the recommended one, so there is a
// single marker to pin -- and pinning it per question is what makes a question
// that quietly loses its answer fail, which is the state the document was in
// until this round.
const coordinatorAnsweredMarker = "**Answered 2026-10-19: option (A)**"

// coordinatorReadmeAnswered and coordinatorReconciledAnswered are the two
// documents' own sentences stating that the owner answered all sixteen
// questions and that answering one is not approving a plan. Each is pinned
// whole, and each is written on a single line in its document so that the pin
// is a sentence rather than a fragment that a rewrap can satisfy.
//
// The README states the second fact twice -- once inside the answered sentence
// ("none is approved") and once as its own sentence -- and both halves are
// pinned, because each is a place a later edit can soften the claim without
// touching the other.
const (
	coordinatorReadmeAnswered = "**All sixteen questions are answered, and none is approved.**"
	// Rewritten when the plan landed, and again when the owner answered D193-D197
	// and passed the gate for P0-P6. What the sentence said before was that
	// nothing in this directory had been accepted or approved; what it has to say
	// now is the narrower thing that is still true and that no other test can see
	// -- an answer picks an option and approves no work -- beside the gate that
	// DID pass, for which parts, and the two parts it did not open.
	coordinatorReadmeNotApproval  = "**An answer is not an approval**: an answer picks an option and approves no work."
	coordinatorReconciledAnswered = "**The owner answered all sixteen on 2026-10-19, each by taking option (A) — the recommendation — and an answer is not an approval.**"
)

// coordinatorReadmeGatePassed is the index's sentence recording that the plan's
// owner gate is passed, and for which parts: P0-P6, the ones that need only
// Q1-Q16 and D193-D197. It is pinned whole because it is the claim a reader acts
// on -- the sentence that says which parts may start -- and because the two
// parts it does NOT open are named in the same breath, so a rewrite that widens
// the gate over P7 and P8 fails here rather than reading as a permission.
const coordinatorReadmeGatePassed = "the plan's owner gate is **passed for the parts that need only those decisions (P0–P6)**, on the long-lived branch **`coordinator`**, target **0.11.0**"

// coordinatorDecisionAnsweredA and coordinatorDecisionAnsweredB are how the
// reconciliation's §6 records the owner's answers of 2026-10-21, in the same
// shape §3 uses for the sixteen questions: the option taken, in place, under the
// decision it answers. Four took the recommendation and therefore carry the
// first marker; **D196 was answered against it** and carries the second, which
// says so -- a document that recorded D196 as option (A) would be describing the
// plan's own recommendation as the owner's ruling, and the two differ about
// which document builds the coordinator's front door.
const (
	coordinatorDecisionAnsweredA = "**Answered 2026-10-21: option (A)**"
	coordinatorDecisionAnsweredB = "**Answered 2026-10-21: option (B), against the recommendation**"
)

// coordinatorReconciledDecisionsAnswered is §6's own sentence stating that the
// owner answered all five decisions, and when. It replaces the section's
// "None is answered." line, which was true while the gate was open and is a
// claim the section can no longer make.
const coordinatorReconciledDecisionsAnswered = "**The owner answered all five on 2026-10-21**"

// coordinatorQ10Amended is the note the reconciliation carries under Q10's
// recorded answer of 2026-10-19. The owner's answer is not edited -- it is the
// owner's, given on that date, and rewriting it would erase what D196 later
// changed -- so the amendment sits under it and says which decision changed it.
const coordinatorQ10Amended = "**Amended 2026-10-21**"

// coordinatorGateAnsweredA and coordinatorGateAnsweredB are the plan's gate
// table recording the same answers, one row per decision. The table is where a
// reader learns which part waits on which decision, so an answer recorded in the
// reconciliation and missing from the table is a part that still reads as
// blocked.
const (
	coordinatorGateAnsweredA = "answered 2026-10-21, option (A)"
	coordinatorGateAnsweredB = "answered 2026-10-21, option (B), against the recommendation"
)

// coordinatorPlanGatePassed is the plan's own sentence opening the gate it used
// to hold shut, and naming the parts that may now start. It is pinned whole for
// the reason the index's sentence is: it is the permission a builder acts on.
const coordinatorPlanGatePassed = "**Passed 2026-10-21 for P0–P6.**"

// coordinatorPlanGateStillWaits is the plan's sentence naming what the gate did
// NOT open, and what those two parts wait on. It is pinned beside the permission
// because the failure mode of a gate sentence is that it widens: a reader who
// takes "passed" without the second half starts P7 on a front door nobody has
// reviewed.
const coordinatorPlanGateStillWaits = "**P7 and P8 still wait**"

// coordinatorSections are the spec's own section headings, in order: the pin
// that spec.md is the document the owner wrote and not a summary of it.
var coordinatorSections = []string{
	"## 1. Problem",
	"## 2. Goal",
	"## 3. Principles",
	"## 4. Deployment",
	"## 5. State",
	"## 6. Inputs",
	"## 7. Interface: MCP front door, Firestore behind",
	"## 8. Models",
	"## 9. Long-running behaviour",
	"## 10. Console integration",
	"## 11. Open questions",
}

// TestDocsCoordinatorIsWritten: the package exists, and the index states the
// four facts a reader has to have before they trust anything in it -- that
// nothing is implemented, who owns it, when it was written, and which commit it
// was checked against.
func TestDocsCoordinatorIsWritten(t *testing.T) {
	for _, path := range []string{coordinatorReadme, coordinatorSpec, coordinatorReconciled, coordinatorPlan} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s does not exist: %v", path, err)
		}
	}
	readme := readDoc(t, coordinatorReadme)
	for _, want := range []string{
		coordinatorReadmeStatus,
		coordinatorReadmeDecisionsAnswered,
		coordinatorReadmeGatePassed,
		"**Owner: Dimi Paun. Date: 2026-10-19.**",
		coordinatorCheckedAgainst,
		coordinatorBase,
		"**None.** The spec is copied byte for byte",
		"its items were **Q1–Q16**",
		// What the content anchors cover and what they do not, each pinned whole on the one
		// line it is written on, so that widening either back is a failure rather than
		// something a reader has to check against the tests by hand. Both were wider than
		// they should have been: the held half said "every one of them" over citations that
		// name designs living only in open pull requests, and the unheld half claimed the
		// index's own citations into those trees were all unanchored when the numbers
		// section's are held by the pins this file carries.
		coordinatorReadmeHeldCitations,
		coordinatorReadmeAnchorScope,
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", coordinatorReadme, want)
		}
	}
	// The reconciliation states the commit and the date itself, not only the
	// index: it is the document a reader cites, and it has to stand alone.
	reconciled := readDoc(t, coordinatorReconciled)
	if !strings.Contains(reconciled, coordinatorCheckedAgainst) {
		t.Errorf("%s never names the commit it was checked against (%s)", coordinatorReconciled, coordinatorCheckedAgainst)
	}
	if !strings.Contains(reconciled, "2026-10-19") {
		t.Errorf("%s never states the date it was written", coordinatorReconciled)
	}
	// Both documents say, in their own words, two things that only appear as
	// prose and that a later edit can quietly invert: that the owner answered
	// all sixteen questions on 2026-10-19, and that an answer is not an
	// approval. The second is the one that matters -- a reconciliation whose
	// answers start reading as an approved plan is the failure this work's
	// no-D-number rule exists to prevent, and nothing else in this package
	// would see it. A reader who finds the answers described here as a plan
	// should treat that as an error in the other document.
	for path, wants := range map[string][]string{
		coordinatorReadme:     {coordinatorReadmeAnswered, coordinatorReadmeNotApproval},
		coordinatorReconciled: {coordinatorReconciledAnswered},
	} {
		for _, want := range wants {
			if !strings.Contains(readDoc(t, path), want) {
				t.Errorf("%s no longer says both that the owner answered Q1–Q16 and that an answer is not an approval: it lacks %q", path, want)
			}
		}
	}
}

// TestDocsCoordinatorSpecIsVerbatim: spec.md is the owner's document and
// nothing else. It carries the spec's own title, its status line and all eleven
// of its sections, in order -- and it carries none of the reconciliation's
// apparatus. A reconciliation paragraph pasted into the spec, or a section
// dropped from it, would leave the next reader unable to tell which of the two
// documents is the owner's. Those are sentence checks, and sentences are not
// bytes: the index's promise is that the spec "is copied byte for byte from the
// text the owner supplied", which no list of sentences can keep -- every check
// above passes on a file edited anywhere outside the lines it names -- so the
// file is pinned by its SHA-256 as well.
func TestDocsCoordinatorSpecIsVerbatim(t *testing.T) {
	spec := readDoc(t, coordinatorSpec)
	for _, want := range []string{
		"# Fugaro Coordinator Agent — Design Spec",
		"*Status: draft, Oct 2026*",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", coordinatorSpec, want)
		}
	}
	// In order, not merely present: a section moved is a section edited.
	at := 0
	for _, heading := range coordinatorSections {
		i := strings.Index(spec[at:], heading)
		if i < 0 {
			t.Errorf("%s lacks the section heading %q, or carries it out of order", coordinatorSpec, heading)
			continue
		}
		at += i + len(heading)
	}
	// The reconciliation's own vocabulary, which must never appear in the
	// owner's text: a finding number, a question heading, a classification.
	for _, notWant := range []string{
		"### Q1 —", "**Wrong / Exists", "UNVERIFIED", "Finding #", "reconciliation.md",
	} {
		if strings.Contains(spec, notWant) {
			t.Errorf("%s carries %q, which belongs to the reconciliation: the spec is the owner's text verbatim", coordinatorSpec, notWant)
		}
	}
	// "Byte for byte" is not a figure of speech, and the checks above cannot
	// keep it: they name a title, a status line and eleven headings, so an edit
	// to any other line of this 91-line file passes every one of them. That is
	// the mutation the plan's P0 records as a red step -- change one byte of
	// spec.md and the guard must fail -- and before this pin it did not: the
	// byte was changed, and this package stayed green. So the whole file is
	// pinned by its SHA-256, in the shape docs/voice-module pins its own spec,
	// which carries the same promise. The checks above stay because they say
	// *what* the spec has to say when they fail; this one says the file was
	// touched at all. Editing spec.md means the owner's text changed, and the
	// index's rule for a difference between the two is that the owner's copy is
	// right and this file is the one to fix -- so an edit here comes with the
	// owner's new text and this constant updated to it.
	const coordinatorSpecSHA256 = "48a6b6f7545c44ae6fd07aed27c135f7ab87a938367c37962c17c44a0dab42f5"
	sum := sha256.Sum256([]byte(spec))
	if got := hex.EncodeToString(sum[:]); got != coordinatorSpecSHA256 {
		t.Errorf("%s is not the text the owner supplied (sha256 %s, want %s): the index promises it is copied byte for byte, so any edit here -- one byte included -- is a difference from the owner's copy and needs the owner", coordinatorSpec, got, coordinatorSpecSHA256)
	}
}

// coordinatorAllocationRowRE matches a decision-allocation table row: a row
// whose FIRST cell is exactly one decision number, `| **D193** | ... |`. It is
// deliberately anchored on the closing `|` of the first cell, so the index's
// range rows (`| **D1–D192** |`, `| **D193 onward** |`) are not matches: those
// report the allocation, they do not take a number from it.
var coordinatorAllocationRowRE = regexp.MustCompile(`(?m)^\|\s*\*{0,2}D[0-9]+[a-z]?\*{0,2}\s*\|`)

// coordinatorDecisionHeadingRE matches one of §6's decision headings,
// `### D193 — How far does one coordinator's GitHub credential reach?`, in the
// numbered-then-em-dash shape the section uses for all five.
var coordinatorDecisionHeadingRE = regexp.MustCompile(`(?m)^### (D[0-9]+) — `)

// TestDocsCoordinatorNewDecisionsAreNumbered: the five things planning found are
// decisions and they are numbered, from the block the register records as free.
//
// This test replaces TestDocsCoordinatorTakesNoDecisionNumber, which asserted
// the opposite and was right to while the folder held a reconciliation and no
// plan. What it protected has not gone away and is still asserted: an ANSWER
// picks an option and allocates nothing -- the sixteen questions are still
// Q-numbers with no D-number each, and the reconciliation still says so. What
// changed is that the plan that followed the answers found five things the
// answers do not settle, and a decision is what those are. Leaving them
// unnumbered would have made the plan's gate table name a decision a reader
// cannot cite, and D-numbers are how this repository cites one.
//
// Two failure modes are checked, and the second is the one that matters:
// contiguity, so that a decision cannot be dropped and renumbered quietly; and
// the recommendation coming first in each, because "the plan is written to the
// recommendation" is only true if the recommendation is the one a reader meets
// first -- the file's own order is the only thing that makes that claim
// checkable.
func TestDocsCoordinatorNewDecisionsAreNumbered(t *testing.T) {
	reconciled := readDoc(t, coordinatorReconciled)
	var got []string
	for _, m := range coordinatorDecisionHeadingRE.FindAllStringSubmatch(reconciled, -1) {
		got = append(got, m[1])
	}
	if len(got) != len(coordinatorPlanDecisions) {
		t.Fatalf("%s carries %d decision headings (%v) and the plan names %d (%v): the five decisions planning found are the section's subject",
			coordinatorReconciled, len(got), got, len(coordinatorPlanDecisions), coordinatorPlanDecisions)
	}
	for i, want := range coordinatorPlanDecisions {
		if got[i] != want {
			t.Errorf("%s writes the decision at position %d as %s, not %s: the numbers are contiguous from D193 and in the plan's order",
				coordinatorReconciled, i+1, got[i], want)
		}
	}
	// Each decision states its recommendation before any alternative, and both.
	// The section is one paragraph per decision, so the check is positional
	// within each decision's own text.
	at := 0
	for _, n := range coordinatorPlanDecisions {
		head := "### " + n + " — "
		i := strings.Index(reconciled[at:], head)
		if i < 0 {
			t.Fatalf("%s lost the %s heading after the loop found it", coordinatorReconciled, head)
		}
		start := at + i
		at = start + len(head)
		next := strings.Index(reconciled[at:], "\n### ")
		end := len(reconciled)
		if next >= 0 {
			end = at + next
		}
		body := reconciled[start:end]
		// The recommendation is option (A) in all five -- that is what "the plan
		// is written to the recommendation" means concretely, and the letter is
		// checked rather than only the word: a decision whose recommendation
		// quietly became (B) while the alternative stayed labelled (B) is a
		// decision the plan's own parts no longer match, and ordering alone
		// cannot see it.
		rec := strings.Index(body, "**Recommendation — (A):")
		alt := strings.Index(body, "**Alternative — (B):")
		if rec < 0 {
			t.Errorf("%s's %s states no recommendation of option (A): the plan is written to the recommendation, and every one of these five recommends (A)", coordinatorReconciled, n)
			continue
		}
		if alt < 0 {
			t.Errorf("%s's %s states no alternative of option (B): a decision with one option is not a decision", coordinatorReconciled, n)
			continue
		}
		if rec > alt {
			t.Errorf("%s's %s writes its alternative before its recommendation: recommendation-first is what makes the plan's \"written to the recommendation\" checkable", coordinatorReconciled, n)
		}
		if !strings.Contains(body, "**Where it lands.**") {
			t.Errorf("%s's %s does not name where it lands: a decision the plan's parts cannot point at is a decision the gate table cannot block on", coordinatorReconciled, n)
		}
	}
	// The index has to say they are ANSWERED, in its own words, and dated: the
	// state this replaced was "written, not answered", which is what the folder
	// said while the gate was open.
	if !strings.Contains(readDoc(t, coordinatorReadme), coordinatorReadmeDecisionsAnswered) {
		t.Errorf("%s does not state that the planning's decisions are answered: it lacks %q", coordinatorReadme, coordinatorReadmeDecisionsAnswered)
	}
	// And the claim the old test protected, kept: an answer allocates nothing.
	if !strings.Contains(reconciled, "**an answer picks an option; it allocates nothing.**") {
		t.Errorf("%s no longer says that an answer picks an option and allocates nothing: the distinction between the owner's answers and a decision number is the reason D193 starts where it does", coordinatorReconciled)
	}
	// The README's numbers table still reports the block this work wrote into,
	// and no longer reports D193 onward as free -- that sentence was true of the
	// folder before the plan, and leaving it would say the block is unallocated.
	if strings.Contains(readDoc(t, coordinatorReadme), "**D193 onward** | — | **free.**") {
		t.Errorf("%s still reports **D193 onward** as free: this work wrote D193-D197 into it", coordinatorReadme)
	}
	// Writing into the block made three other folders' readings of this register
	// stale, and those documents were deliberately not edited from here: the
	// index records the drift site by site instead. That record is pinned,
	// because it is the only place a reader of those documents is told what
	// changed -- deleting it would leave three works offering "move this work to
	// D193 onward" as a live alternative to a block this one has taken.
	readmeDoc := readDoc(t, coordinatorReadme)
	for _, want := range []string{
		coordinatorReadmeDriftHeading,
		"docs/central-orchestrator/README.md",
		"docs/windows-cli/README.md",
		"docs/mobile/voice-session/README.md",
	} {
		if !strings.Contains(readmeDoc, want) {
			t.Errorf("%s no longer carries %q: the drift this block created in three other folders' readings has to be recorded somewhere, and this is the only place it is", coordinatorReadme, want)
		}
	}
	// And the index still reports the block the way it reports every block: as a
	// range row, not as one row per number. A `| **D193** | ... |` row in the
	// index would be the folder-local allocation convention this work did not
	// take, and it would say a number had been answered as well as written.
	if m := coordinatorAllocationRowRE.FindString(readDoc(t, coordinatorReadme)); m != "" {
		t.Errorf("%s allocates a decision number row by row: %q. This folder reports the block as a range; the five decisions are §6 headings", coordinatorReadme, strings.TrimSpace(m))
	}
}

// TestDocsCoordinatorDecisionsAreAnswered: the five decisions planning found are
// ANSWERED, and the one answered against the recommendation is pinned as such.
//
// This is the test the folder needs now that the gate has moved, and it is the
// mirror of the one above. That test holds the decisions to being numbered and
// written recommendation-first, which is what "the plan is written to the
// recommendation" means; this one holds them to being ANSWERED, which is what
// "the owner passed the gate" means. Both are needed, because the two states
// look identical in a table: a decision with a recommendation and no answer is a
// plan nobody may start, and a plan whose parts may start while its decisions
// still read as open is the mismatch.
//
// D196 is the one that matters. It was answered **against** the recommendation,
// so a document that recorded it as option (A) would describe the plan's own
// recommendation as the owner's ruling -- and the two differ about which
// document builds the coordinator's front door, which is the one decision that
// allocates work between two plans.
func TestDocsCoordinatorDecisionsAreAnswered(t *testing.T) {
	reconciled := readDoc(t, coordinatorReconciled)
	at := 0
	for _, n := range coordinatorPlanDecisions {
		head := "### " + n + " — "
		i := strings.Index(reconciled[at:], head)
		if i < 0 {
			t.Fatalf("%s lost the %s heading after the loop above found it", coordinatorReconciled, head)
		}
		start := at + i
		at = start + len(head)
		next := strings.Index(reconciled[at:], "\n### ")
		end := len(reconciled)
		if next >= 0 {
			end = at + next
		}
		body := reconciled[start:end]
		want := coordinatorDecisionAnsweredA
		if n == "D196" {
			want = coordinatorDecisionAnsweredB
		}
		if !strings.Contains(body, want) {
			t.Errorf("%s's %s does not record the owner's answer of 2026-10-21 (%q): a decision the owner has ruled on and the document still reads as open is the state this round ended",
				coordinatorReconciled, n, want)
		}
	}
	// The section says so in its own words, and the sentence it used to carry --
	// that none of the five is answered -- is gone. That sentence was the claim
	// that kept every part behind the gate, so leaving it standing would have the
	// reconciliation deny an approval the plan records.
	if !strings.Contains(reconciled, coordinatorReconciledDecisionsAnswered) {
		t.Errorf("%s does not state that the owner answered all five decisions: it lacks %q", coordinatorReconciled, coordinatorReconciledDecisionsAnswered)
	}
	if strings.Contains(reconciled, "**None is answered.**") {
		t.Errorf("%s still says none of the five decisions is answered: the owner answered them on 2026-10-21, and the plan's parts P0–P6 are written to those answers", coordinatorReconciled)
	}
	// Q10's answer is the owner's, given on 2026-10-19, and it is left as it was
	// given: D196 changed what the plan does, not what the owner answered. The
	// amendment is what says so, and it is the only thing that does.
	if !strings.Contains(reconciled, coordinatorQ10Amended) {
		t.Errorf("%s carries no %q note under Q10: D196 was answered against Q10(A)'s one-resident-service reading, and the owner's answer of 2026-10-19 is amended under it rather than rewritten", coordinatorReconciled, coordinatorQ10Amended)
	}

	plan := readDoc(t, coordinatorPlan)
	// The gate table records the same answers, one row per decision, read from
	// the gate section rather than the document: the table is where a reader
	// learns which part waits on what, and an answer recorded in the
	// reconciliation and missing from the table leaves the part reading as
	// blocked.
	gate := ""
	if loc := coordinatorPlanGateHeadingRE.FindStringIndex(plan); loc != nil {
		rest := plan[loc[1]:]
		if next := strings.Index(rest, "\n## "); next >= 0 {
			gate = rest[:next]
		} else {
			gate = rest
		}
	}
	if gate == "" {
		t.Fatalf("%s' gate section is empty or unfindable, so its rows cannot be read", coordinatorPlan)
	}
	for _, n := range coordinatorPlanDecisions {
		want := coordinatorGateAnsweredA
		if n == "D196" {
			want = coordinatorGateAnsweredB
		}
		row := -1
		for _, line := range strings.Split(gate, "\n") {
			if strings.Contains(line, "**"+n+"**") {
				row = strings.Index(line, want)
				break
			}
		}
		if row < 0 {
			t.Errorf("%s' gate table's %s row does not record the owner's answer (%q): the table is the list of what each part waits on, and a row whose decision is answered but whose answer is not written there leaves the part looking blocked",
				coordinatorPlan, n, want)
		}
	}
	// And the gate itself, in the plan's own words: passed, for which parts, and
	// what it did not open. Both halves are pinned, because the failure mode of a
	// gate sentence is that it widens -- P7 and P8 wait on a front-door design
	// review and on two owner checks, and neither is done.
	for _, want := range []string{coordinatorPlanGatePassed, coordinatorPlanGateStillWaits} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not carry %q: the gate's state, the parts it opened and the parts it did not are the permission a builder acts on", coordinatorPlan, want)
		}
	}
}

// coordinatorCitationRE finds a backtick-quoted repository path, in EVERY shape
// the reconciliation cites one in:
//
//	`internal/budget/model.go:1-2`         path, line range
//	`SECURITY.md:40`                      path and one line
//	`internal/task/task.go:328 NewRunID`  path, line, and the symbol there
//	`internal/cli/init.go:1494 installRoot`
//	the console's `main/spawn.ts:326+`        a line and an open end
//	`internal/budget/model.go:151,164`    a list of lines
//	`internal/pricing/table.go Embedded()` a path and a symbol, no line
//	`internal/budget/rules/rules.json.tmpl` a path alone
//
// The shapes after the path are the document's dominant ones and the reason
// this regexp is written the way it is. An earlier version required the closing
// backtick to follow the path (or its `:line` suffix) immediately, which is
// true of two of those shapes and false of the rest: `path:line Symbol` never
// matched, so nine cited paths were never checked at all, and no test could
// see it because the check simply found fewer citations and said nothing. The
// shape after the path is therefore written as "whatever is not a backtick",
// and coordinatorPathCensusRE below is the independent count that makes a
// regexp which matches nothing a failure rather than a silence.
//
// "Whatever is not a backtick" rather than "whatever is not a backtick or a
// newline" on purpose, and that is a correction of its own: the document cites
// three files in a shape whose symbol wraps onto the next line, as
// `modules/installation/history.tf:70 google_cloud_run_v2_job` does. A citation
// regexp that stopped at the newline did not see those three, which is the same
// silent-shortfall failure the paragraph above describes, one line further in.
// They matter beyond themselves: two of them are the anchor that the relative
// second references (`:223`, `:127 notCovered`) resolve against below, so a
// reader -- or a resolver -- that cannot see the anchor cannot check the
// reference. coordinatorPathCensusRE stays line-bounded, deliberately: it is a
// floor, not an exact count, and a census that stretched across newlines would
// manufacture giant "tokens" wherever the prose has an unpaired backtick.
//
// The extension set is the one this repository's cited files use; a backticked
// token without one (a node path such as `config/policy/shared`, a package
// name, a symbol on its own) is not a citation of a file and is left alone.
// The path character class carries `*` for the two globs the document names
// (`internal/cli/docs_*_test.go`, `docs/design/credentials-service*.md`); both
// are audited exceptions below, not files.
//
// The line spec is captured as a second group, because the path resolving is
// only half of what a `path:line` citation claims: the check below reads the
// file and holds the number to it, for every occurrence and not only the first
// one per path.
var coordinatorCitationRE = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./*-]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf)\\b)(:[0-9]+(?:[-,+][0-9]*)*)?[^`]*`")

// coordinatorPathCensusRE is the independent count of how many path-looking
// tokens the document holds, and it is deliberately dumber than the citation
// regexp: any backticked token that carries a known extension anywhere in it,
// whether the path starts the token or not, and whatever follows it. It is the
// floor the citation check is measured against, so that a regexp which has
// stopped matching the document's shapes fails the test instead of passing it
// vacuously -- the failure mode that hid the `path:line Symbol` gap.
//
// Two regexps rather than one on purpose: a census computed with the same
// regexp as the check would shrink with it and could never catch it.
var coordinatorPathCensusRE = regexp.MustCompile("`[^`\\n]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf)\\b[^`\\n]*`")

// coordinatorCitationPrefixes are the directories a citation may be relative to
// besides the repository root. The Terraform modules are cited the way
// SECURITY.md cites them -- `modules/project/`, not the whole
// `deploy/terraform/gcp/` path -- so the test accepts both forms rather than
// forcing one spelling on the prose.
var coordinatorCitationPrefixes = []string{"", "deploy/terraform/gcp/"}

// coordinatorRootFiles are the repository's own top-level documents, which a
// citation may name with no directory at all. Everything else has to look like
// a path (contain a `/`), or carry a line number that makes it a shortened
// reference to one file in the tree, which coordinatorBareNames resolves and
// refuses to guess at. What is left -- a bare filename with no line, a run
// artifact such as `launch.json` -- is not a claim about a file in this tree.
var coordinatorRootFiles = map[string]bool{"SECURITY.md": true, "README.md": true, "CONTRIBUTING.md": true}

// coordinatorCitationExceptions are citations that deliberately name something
// that is not a file in this repository at this commit, each with the reason.
// Every one is an audited case, not a blanket skip, and the test fails if an
// entry here goes unused: an exception that outlives the sentence it was
// written for is a hole in the check.
var coordinatorCitationExceptions = map[string]string{
	"CLAUDE.md":                             "§0 records that there is NO root CLAUDE.md; naming the absent file is the point of the sentence",
	"docs/design/always-on-orchestrator.md": "the always-on orchestrator design, in open PR #632 and not on main; §2 #29 says so",
	"docs/design/console-styling.md":        "the console styling design, in open PR #634 and not on main; U7 says so",
	"docs/design/credentials-service*.md":   "§0.3 quotes the brief's own glob, which names no file: the documents are at docs/credentials-service/, and the point of the sentence is that docs/design/ has no such subdirectory",
	"docs/releases/vX.Y.Z.md":               "the naming pattern of the release notes, not one file",
	"internal/cli/docs_*_test.go":           "§0.4 names the pattern of the docs packages' pinning tests -- `*.go` is not a filename -- as the shape this file's tests were written from",
}

// TestDocsCoordinatorCitationsExist: every repository path reconciliation.md
// cites resolves. This is the test that keeps a reconciliation honest: its
// claims are only worth as much as the files behind them, and a path that moved
// turns a checked finding into an unchecked one without any other symptom.
func TestDocsCoordinatorCitationsExist(t *testing.T) {
	doc := readDoc(t, coordinatorReconciled)
	// The census first, and independently: how many path-looking tokens the
	// document holds at all. The citation check below has to see at least that
	// many, so a regexp that stopped matching a shape the prose uses fails
	// rather than quietly checking less.
	census := len(coordinatorPathCensusRE.FindAllString(doc, -1))
	matches := coordinatorCitationRE.FindAllStringSubmatch(doc, -1)
	if len(matches) < census {
		t.Fatalf("%s holds %d path-looking tokens and the citation check matched %d of them: the document cites a shape coordinatorCitationRE no longer knows, so those citations are unchecked",
			coordinatorReconciled, census, len(matches))
	}
	// The floor for the other half of a citation -- the line, rather than the
	// path. Every token in the document that attaches a line number to a name is
	// one of the two named shapes the checks know: `path:line`, which this test
	// checks below, or `symbol:line`, whose file is the paragraph's anchor and
	// which TestDocsCoordinatorRelativeCitationsResolve checks. A token that is
	// neither is a citation no check reads, which is what the document's
	// `sanitizeText:2208` was: a shape that arrived with the prose and was
	// checked by nothing. The path census above cannot see that shape at all --
	// it looks for a file extension -- so this one exists to see it.
	for _, token := range coordinatorLineCensusRE.FindAllString(doc, -1) {
		if coordinatorCitationRE.MatchString(token) || coordinatorNamedRefRE.MatchString(token) {
			continue
		}
		t.Errorf("%s holds %q, which attaches a line number to a name and is matched by no citation check: the document has grown a shape the checks do not know, so that line is checked by nothing",
			coordinatorReconciled, token)
	}
	// The tree is walked once, so that a shortened reference can be resolved
	// against it rather than skipped.
	bareNames := coordinatorBareNames(t)
	seen := map[string]bool{}
	used := map[string]bool{}
	cites := 0
	for _, m := range matches {
		path := m[1]
		// The line numbers are checked for EVERY occurrence, before the
		// per-path dedup below: one file is cited at several lines, and
		// checking only the first occurrence would leave the rest unread --
		// which is how a citation can name a line past the end of a file and
		// still be reported as checked.
		first := !seen[path]
		seen[path] = true
		// The exceptions are checked first, so that one naming a bare filename
		// (`CLAUDE.md`) is not swallowed by the path test below.
		if reason, ok := coordinatorCitationExceptions[path]; ok {
			if first {
				used[path] = true
				t.Logf("%s cites %q, which is not a file here: %s", coordinatorReconciled, path, reason)
			}
			continue
		}
		// A citation is a path, one of the repository's own root documents, or a
		// shortened second reference that carries the line it means -- `cover.go:89
		// grantRules`, `apptoken.go:174`. The last of those used to be skipped
		// here, and silently: the token names no directory, so the path test
		// below never reached it, and the line number it carries was read by no
		// test at all -- while the document's contract is that every citation
		// shape it uses is checked. A shortened reference is a claim about one
		// file in this tree, so it is resolved against the tree and the check
		// refuses to pick a file for it when the name does not pick one.
		//
		// A bare name with NO line is still left alone: `launch.json` and
		// `fugaro.yaml` name a run artifact and a configuration file, not a file
		// in this tree, and the token carries nothing that could hold them to
		// one.
		if !strings.Contains(path, "/") && !coordinatorRootFiles[path] {
			if m[2] == "" {
				continue
			}
			file, ok := coordinatorResolveBareName(bareNames, path)
			if !ok {
				if first {
					cites++
					t.Errorf("%s cites %q, a shortened reference carrying a line number, and %s: a shortened name is a claim about one file in this tree, and this check will not choose one for it",
						coordinatorReconciled, path, coordinatorBareNameTrouble(bareNames, path))
				}
				continue
			}
			if first {
				cites++
			}
			if !coordinatorCheckCitedLines(t, path, file, m[2]) {
				continue
			}
			if symbol := coordinatorCitedSymbol(m[0], path, m[2]); symbol != "" {
				coordinatorCheckCitedSymbol(t, path, file, m[2], symbol)
			}
			continue
		}
		file, ok := coordinatorResolveCitation(path)
		if !ok {
			if first {
				cites++
				t.Errorf("%s cites %q, which does not exist (tried %s): a reconciliation whose citations have moved is a reconciliation nobody can check",
					coordinatorReconciled, path, strings.Join(coordinatorTriedCitations(path), ", "))
			}
			continue
		}
		if first {
			cites++
		}
		if spec := m[2]; spec != "" {
			coordinatorCheckCitedLines(t, path, file, spec)
		}
	}
	// An exception nobody uses is a hole in this check that no one is watching.
	for path := range coordinatorCitationExceptions {
		if !used[path] {
			t.Errorf("%s no longer cites %q, but coordinatorCitationExceptions still excuses it: delete the entry, or the next citation of that name is unchecked", coordinatorReconciled, path)
		}
	}
	// The numbers this test checked, said out loud, so that the count is
	// visible in the run rather than a fact nobody reads: a later edit that
	// halves it is a line in the log, and the census assertion above is what
	// makes it a failure when it should be.
	//
	// BOTH counts, because the census is measured against one of them and this
	// line used to report the other: the assertion above compares every
	// OCCURRENCE the matcher found with the census, while the number printed
	// here was the DISTINCT PATHS it resolved. A reader taking the live numbers
	// from this line, as the mutation record tells them to, read a pair that
	// was never compared.
	t.Logf("%s: %d citation occurrences over %d distinct paths, against a census of %d path-looking tokens in the document",
		coordinatorReconciled, len(matches), cites, census)
}

// coordinatorResolveCitation resolves a cited path against the two forms the
// document cites one in: from the repository root, and from
// deploy/terraform/gcp/ (the way SECURITY.md cites the Terraform modules). It
// returns the repository-relative path that exists, and whether one did.
func coordinatorResolveCitation(path string) (string, bool) {
	for _, prefix := range coordinatorCitationPrefixes {
		full := prefix + path
		if _, err := os.Stat("../../" + filepath.FromSlash(full)); err == nil {
			return full, true
		}
	}
	return "", false
}

// coordinatorBareNames indexes every file in the tree by its base name, so that
// a shortened second reference -- `cover.go:89 grantRules`, `apptoken.go:174` --
// can be resolved to the one file the document means.
//
// The document shortens a name only when the name is unambiguous in the tree,
// and this index is what makes that a checked claim rather than a convention:
// coordinatorResolveBareName below resolves a name only when exactly one file
// carries it. `.git` and `node_modules` are skipped -- neither is this
// repository's own source, and a name matching a vendored copy would be an
// ambiguity that is not one.
func coordinatorBareNames(t *testing.T) map[string][]string {
	t.Helper()
	index := map[string][]string{}
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel("../..", p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		index[filepath.Base(rel)] = append(index[filepath.Base(rel)], rel)
		return nil
	})
	if err != nil {
		t.Fatalf("the repository tree could not be walked, so a shortened reference into it cannot be resolved: %v", err)
	}
	for name := range index {
		sort.Strings(index[name])
	}
	return index
}

// coordinatorResolveBareName resolves a shortened reference's base name against
// the tree index. Exactly one file has to carry the name: a reference is a claim
// about one file, so a name matching two files (or none) is reported by
// coordinatorBareNameTrouble below rather than resolved to a guess.
func coordinatorResolveBareName(index map[string][]string, name string) (string, bool) {
	matches := index[name]
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
}

// coordinatorBareNameTrouble says why a shortened reference's name did not
// resolve to one file, in the same sentence as the failure: which files carry
// the name, or that none does.
func coordinatorBareNameTrouble(index map[string][]string, name string) string {
	switch matches := index[name]; len(matches) {
	case 0:
		return "no file in this tree is named " + name
	case 1:
		return "the one file named " + name + " is " + matches[0]
	default:
		return strconv.Itoa(len(matches)) + " files are named " + name + " (" + strings.Join(matches, ", ") + ")"
	}
}

// coordinatorCitedSymbol reads the symbol a citation names beside its line: the
// first word after `path:line` inside the citation's own backticks, as
// `cover.go:89 grantRules` names grantRules. A citation with nothing after the
// line names no symbol, and there is nothing to check.
//
// This is the same convention the document's relative references use (`:127
// notCovered`), and it is the only shape in which a path, a line and a symbol
// are paired by grammar rather than by reading the sentence -- which is why the
// symbol is checked here and not beside full-path citations ("THREE HOLES",
// hole 1, above).
func coordinatorCitedSymbol(token, path, spec string) string {
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(token, "`"+path+spec), "`"))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// coordinatorFileLines reads a repository file and returns its lines. The
// trailing newline every Go file ends with is not a line of it: splitting
// without trimming counts one more than the file has, which makes a citation
// to that phantom last line resolve to nothing and still pass -- the same
// "sees less than it claims" shape this file is written against.
func coordinatorFileLines(t *testing.T, file string) []string {
	t.Helper()
	body, err := os.ReadFile("../../" + filepath.FromSlash(file))
	if err != nil {
		t.Errorf("%s could not be read, so the citations into it are unchecked: %v", file, err)
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
}

// coordinatorCheckCitedLines checks the line numbers inside a full
// `path:line` citation -- the shape the document uses most, and the one whose
// path was checked while its line never was. The path existing is not the
// claim such a citation makes: `internal/budget/model.go:151,164` says those
// two lines are the ones meant, and a line past the end of the file is a
// citation no reader can follow. It is the same failure the bare-reference
// check below catches, in the other half of the document's citations, and
// finding #26 records a cited line range that was already wrong once.
//
// An open-ended line (`:326+`) is checked for the line it names and no more,
// which is all the `+` claims.
//
// It reports whether every line it was given is a line of the file, because the
// symbol check below indexes into those lines: a caller that goes on to look for
// a symbol beside a line past the end of the file would slice out of range.
func coordinatorCheckCitedLines(t *testing.T, path, file, spec string) bool {
	t.Helper()
	lines := coordinatorFileLines(t, file)
	if lines == nil {
		return false
	}
	ok := true
	for _, n := range coordinatorLineNumbers(strings.TrimPrefix(spec, ":")) {
		if n < 1 || n > len(lines) {
			ok = false
			t.Errorf("%s cites %q at %q, but %s holds %d lines: line %d is past the end of the file, so the citation names a line that is not there",
				coordinatorReconciled, path, spec, file, len(lines), n)
		}
	}
	return ok
}

// coordinatorCheckCitedSymbol checks the symbol a shortened reference names
// beside its line: `cover.go:89 grantRules` claims grantRules is at line 89, and
// a check that read the number and not the name would pass a citation whose name
// had moved to another file. The window is the same one the relative references
// use, and for the same reason.
func coordinatorCheckCitedSymbol(t *testing.T, path, file, spec, symbol string) {
	t.Helper()
	lines := coordinatorFileLines(t, file)
	if lines == nil {
		return
	}
	nums := coordinatorLineNumbers(strings.TrimPrefix(spec, ":"))
	lo, hi := nums[0], nums[0]
	for _, n := range nums {
		if n < lo {
			lo = n
		}
		if n > hi {
			hi = n
		}
	}
	from, to, found := coordinatorSymbolBeside(lines, lo, hi, symbol)
	if !found {
		t.Errorf("%s cites %q at %q naming %q, but %q is nowhere in lines %d-%d of %s: the shortened reference names a symbol the file does not have beside the line it is attached to",
			coordinatorReconciled, path, spec, symbol, symbol, from, to, file)
	}
}

// coordinatorTriedCitations names every spelling coordinatorResolveCitation
// tried, so a failure says what was looked for rather than only what was not.
func coordinatorTriedCitations(path string) []string {
	tried := make([]string, 0, len(coordinatorCitationPrefixes))
	for _, prefix := range coordinatorCitationPrefixes {
		tried = append(tried, prefix+path)
	}
	return tried
}

// coordinatorBareRefRE finds a relative second reference: a backticked token
// that begins with `:` and carries no path of its own.
//
//	`:89`                  one line
//	`:14-24`               a range
//	`:124,138,159,174`     a list
//	`:127 notCovered`      a line, and the symbol to find beside it
//
// It is line-bounded, where coordinatorCitationRE deliberately is not: this
// shape is short by nature, and a version that crossed newlines would read the
// colon that merely ENDS a citation -- `.../reconciliation.md`:` then a newline
// then `- **D143**` -- as a reference with a whole sentence for a line number.
//
// The colon has to be followed by a non-space, which keeps out the token that
// falls between two adjacent citations in the prose -- the colon and the space
// in "`job`: `path`" -- because that one is punctuation, not a reference.
var coordinatorBareRefRE = regexp.MustCompile("`(:[^ `\n][^`\n]*)`")

// coordinatorNamedRefRE finds the document's other second-reference shape, the
// one that names the symbol first and the line second and gives no path at all:
//
//	`sanitizeText:2208`     the symbol, and the line it is at
//	`safeUrl:2477`
//
// It is the mirror of the `:line symbol` shape above, and it resolves the same
// way: the file is the last full-path citation before it in its own paragraph,
// which for these three is the console's sanitizer module. It is line-bounded
// for the same reason -- the token is short by nature -- and the symbol has to
// start with a letter or an underscore, which is what keeps a host and a port
// (`127.0.0.1:0`, cited twice in this document) out of it.
//
// The shape arrived with the document and was matched by nothing: it carries no
// file extension, so the citation matcher and the census both passed over it,
// and it does not begin with a colon, so the `:line` matcher did too. Three
// citations were read by no test at all.
var coordinatorNamedRefRE = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*):([0-9]+(?:[-,+][0-9]*)*)`")

// coordinatorLineCensusRE is the independent count of how many backticked tokens
// in the document attach a line number to a NAME -- a path or a symbol. It is
// the floor for the two named shapes, and it is deliberately dumber than either
// matcher: any token where a name is followed by `:` and a digit, whether what
// follows is a well-formed line spec or not, and whether the name is a path, a
// symbol or neither.
//
// coordinatorPathCensusRE above cannot serve here: it looks for a file
// extension, so `sanitizeText:2208` is invisible to it, and a census that cannot
// see a shape cannot catch the shape going unchecked. This one sees it, and the
// citation test fails on any token it finds that no matcher claims -- which is
// what makes the third shape's arrival a failure rather than a silence.
//
// A name has to start with a letter or an underscore, so a host and a port is
// not a name: `127.0.0.1:0` is cited twice in this document and is not a
// citation of anything.
var coordinatorLineCensusRE = regexp.MustCompile("`[^`\n]*[A-Za-z_][A-Za-z0-9_]*:[0-9][^`\n]*`")

// coordinatorLineSpecRE is what may follow a relative reference's colon: a line
// number, or numbers joined by `-` or `,`, with an empty tail allowed for the
// open-ended `:326+` form. Anything else -- `:...`, `:N`, `:later` -- is a
// placeholder that resolves to nothing, and that is the failure this whole
// check exists for: the document carried a literal `:...` in a parenthetical no
// test could see, because a reference with no path in it was skipped by the
// citation check rather than failed by it.
var coordinatorLineSpecRE = regexp.MustCompile(`^[0-9]+(?:[-,+][0-9]*)*$`)

// coordinatorDigitsRE pulls the numbers out of a spec coordinatorLineSpecRE has
// already accepted.
var coordinatorDigitsRE = regexp.MustCompile(`[0-9]+`)

// coordinatorSymbolWindow is how far from a cited line a symbol named beside it
// may sit, in either direction. A doc comment sits a few lines above the
// declaration it belongs to and a call site a line or two either side, so the
// window has to be a few lines wide; it is far too narrow to accept a symbol
// that lives in another part of the file.
const coordinatorSymbolWindow = 5

// coordinatorSymbolBeside looks for a symbol in the window of lines a citation
// may mean, and returns the window it looked in and whether it found it. The
// window is why this is not an equality: the symbol named beside a line is
// usually on the line, but a doc comment or a call site puts it a few lines
// away, and both are the citation's own subject.
//
// The caller has established that lo and hi are lines of the file: the window is
// an index into lines, and a past-the-end line would slice out of range.
func coordinatorSymbolBeside(lines []string, lo, hi int, symbol string) (int, int, bool) {
	from, to := lo-coordinatorSymbolWindow, hi+coordinatorSymbolWindow
	if from < 1 {
		from = 1
	}
	if to > len(lines) {
		to = len(lines)
	}
	want := strings.TrimSuffix(symbol, "()")
	for _, l := range lines[from-1 : to] {
		if strings.Contains(l, want) {
			return from, to, true
		}
	}
	return from, to, false
}

// coordinatorParagraphSeparatorRE finds the blank line that ends a paragraph.
var coordinatorParagraphSeparatorRE = regexp.MustCompile(`\n[ \t]*\n`)

// coordinatorParagraph returns the blank-line-delimited block of text holding
// offset i. A relative reference is resolved inside one block and no further: a
// bare `:89` belongs to the sentence it sits in, and a search that ran back
// through the document would happily resolve it against a path from three
// findings earlier. A markdown table counts as one block, which is right -- a
// row's own citations are the row's anchor.
func coordinatorParagraph(doc string, i int) (int, int) {
	start, end := 0, len(doc)
	for _, sep := range coordinatorParagraphSeparatorRE.FindAllStringIndex(doc, -1) {
		if sep[1] <= i {
			start = sep[1]
			continue
		}
		if sep[0] >= i {
			end = sep[0]
			break
		}
	}
	return start, end
}

// TestDocsCoordinatorRelativeCitationsResolve: the document's second references
// -- a bare `:89`, `:145`, `:183`, `:209`, and a symbol-first `sanitizeText:2208`
// or `safeUrl:2477` -- are citations too, and are checked like ones. Each
// resolves against the nearest full-path citation before it in its own
// paragraph; the line it names has to exist in that file; and where the
// reference also names a symbol, the symbol has to be within a few lines of the
// line it is attached to.
//
// This is the check the document needed and did not have. A reference of either
// shape carries no path, so TestDocsCoordinatorCitationsExist skips it entirely,
// and a placeholder line number, a line past the end of the file and a symbol
// the file does not have all read exactly like a correct one. The document
// carried a literal `:...` for exactly that reason.
//
// The symbol-first shape arrived later and was read by nothing at all: no
// matcher knew it, and the census could not see it either, because the census
// looks for a file extension. Three citations (`sanitizeText:2208`,
// `sanitizeRefusal:2445`, `safeUrl:2477`) sat in that hole. Both shapes are
// handled here now, and the census below fails on a token that is neither.
func TestDocsCoordinatorRelativeCitationsResolve(t *testing.T) {
	doc := readDoc(t, coordinatorReconciled)
	refs := coordinatorBareRefRE.FindAllStringSubmatchIndex(doc, -1)
	named := coordinatorNamedRefRE.FindAllStringSubmatchIndex(doc, -1)
	if len(refs) == 0 {
		t.Fatal("the document carries no relative reference of the `:N` shape: this test would otherwise check nothing of that shape")
	}
	if len(named) == 0 {
		t.Fatal("the document carries no reference of the `symbol:N` shape: this test would otherwise check nothing of that shape, and coordinatorNamedRefRE would be dead code no test would miss")
	}
	checked := 0
	for _, m := range refs {
		spec, symbol, _ := strings.Cut(strings.TrimPrefix(doc[m[2]:m[3]], ":"), " ")
		if coordinatorCheckRelativeRef(t, doc, m[0], doc[m[2]:m[3]], spec, symbol) {
			checked++
		}
	}
	// The symbol-first shape: `sanitizeText:2208` names the symbol where the
	// other shape names the line, and its file is the paragraph's anchor either
	// way. The token as written is rebuilt from the two groups so that the
	// messages read the same for both shapes.
	for _, m := range named {
		ref := doc[m[2]:m[3]] + ":" + doc[m[4]:m[5]]
		if coordinatorCheckRelativeRef(t, doc, m[0], ref, doc[m[4]:m[5]], doc[m[2]:m[3]]) {
			checked++
		}
	}
	t.Logf("%s: %d relative citations resolved against their paragraph's anchor, of %d found", coordinatorReconciled, checked, len(refs)+len(named))
}

// coordinatorCheckRelativeRef checks one second reference -- either shape -- and
// reports whether it got far enough to count as checked. The two shapes differ
// only in which half of the reference is the symbol: both resolve against the
// last full-path citation before them in their own paragraph, and both are held
// to a line that exists and a symbol that sits beside it.
func coordinatorCheckRelativeRef(t *testing.T, doc string, at int, ref, spec, symbol string) bool {
	t.Helper()
	line := 1 + strings.Count(doc[:at], "\n") // where the reference is, for the messages
	start, _ := coordinatorParagraph(doc, at)
	// The anchor: the last full-path citation before this reference in the same
	// paragraph. It is allowed to wrap onto the next line, which is why
	// coordinatorCitationRE does not stop at a newline.
	anchor := ""
	for _, c := range coordinatorCitationRE.FindAllStringSubmatchIndex(doc[start:at], -1) {
		anchor = doc[start+c[2] : start+c[3]]
	}
	if anchor == "" {
		t.Errorf("%s:%d cites %q with no full-path citation before it in the same paragraph: a relative reference is resolvable only against an anchor, so a reader has no file to open. Name the path at the reference, or put the reference beside a citation",
			coordinatorReconciled, line, ref)
		return false
	}
	file, ok := coordinatorResolveCitation(anchor)
	if !ok {
		t.Errorf("%s:%d cites %q against %q, which does not exist (tried %s): the anchor has to resolve here too, or the reference resolves to nothing",
			coordinatorReconciled, line, ref, anchor, strings.Join(coordinatorTriedCitations(anchor), ", "))
		return false
	}
	if !coordinatorLineSpecRE.MatchString(spec) {
		t.Errorf("%s:%d cites %q: %q is not a line number, a range or a list of them. A placeholder that resolves to nothing is a citation no reader can follow, and this document's contract is that every one of them can be",
			coordinatorReconciled, line, ref, spec)
		return false
	}
	lines := coordinatorFileLines(t, file)
	if lines == nil {
		return false
	}
	nums := coordinatorLineNumbers(spec)
	lo, hi, past := nums[0], nums[0], false
	for _, n := range nums {
		if n < lo {
			lo = n
		}
		if n > hi {
			hi = n
		}
		if n < 1 || n > len(lines) {
			past = true
			t.Errorf("%s:%d cites %q against %q, which holds %d lines: line %d is past the end of the file",
				coordinatorReconciled, line, ref, file, len(lines), n)
		}
	}
	// Nothing more to check, and nothing safe to check either: the window below
	// is an index into lines, and a past-the-end line would make it an
	// out-of-range slice.
	if symbol == "" || past {
		return true
	}
	from, to, found := coordinatorSymbolBeside(lines, lo, hi, symbol)
	if !found {
		t.Errorf("%s:%d cites %q against %q, but %q is nowhere in lines %d-%d: the reference names a symbol the file does not have beside the line it is attached to",
			coordinatorReconciled, line, ref, file, symbol, from, to)
	}
	return true
}

// coordinatorChecklist, coordinatorMobileReadme, coordinatorMobilePlan and
// coordinatorCredentialsReadme are the documents the README's numbers table reads other works'
// numbers from: the register itself, and the documents the register does not reach.
//
// coordinatorMobilePlan is read for one thing only, and it is the one that had to be corrected
// rather than re-pinned: the state of **100–104**. The mobile README writes those five as
// proposed and the plan it belongs to approved all of Phase 6 -- those five checks with it -- on
// 2026-10-18, so the plan is the document that decides and the row above follows it.
const (
	coordinatorChecklist         = "docs/gcp-live-checklist.md"
	coordinatorMobileReadme      = "docs/mobile/README.md"
	coordinatorMobilePlan        = "docs/mobile/implementation-plan.md"
	coordinatorCredentialsReadme = "docs/credentials-service/README.md"
)

// coordinatorChecksRowHead and coordinatorDecisionsRowHead are the first cells of the two rows
// of the README's numbers table: the check numbers spoken for, and the decision numbers.
const (
	coordinatorChecksRowHead    = "| **Checks 1–109** |"
	coordinatorDecisionsRowHead = "| **D1–D192** |"
)

// coordinatorNumbersSectionHeading is the heading the checks row sits under, and the section
// the row's citations are read in.
const coordinatorNumbersSectionHeading = "## The numbers this work holds"

// coordinatorNumberBlock is one block of the README's numbers table: the block as the row
// writes it, the document the row reads it from, the anchor that finds the line of that document
// which allocates it, and the phrase the row must carry beside it.
//
// The line is not pinned, and that is the point of the anchor. This table used to cite the line
// it read each block from -- `docs/mobile/README.md:25` and the relative `:25` beside the block
// -- and every one of those numbers went stale at once when another work inserted a row into
// that table, which is a change this table can neither see nor be expected to chase. The anchor
// is what the cited line carries instead: the block's own label as the document that allocates
// it writes it, plus enough of what that document says of it to name one line. A block whose
// allocation is quoted, restated or reordered still resolves; a block the document stops
// allocating, or allocates twice, fails.
//
// An entry with no block is a citation the section makes in prose rather than for a block -- the
// register's numbering ruling, and the mobile README's record that 70-79 are not taken -- and is
// held to its document and its anchor the same way.
type coordinatorNumberBlock struct {
	block  string // the block, as the row writes it between ** **; "" for a citation the section makes in prose
	doc    string // the document the row cites for it
	anchor string // the text the line that allocates it carries, which is how the block finds that line
	row    string // the phrase the row must carry for that block
}

// coordinatorDecisionBlocks is the README's D-block row, and coordinatorCheckBlocks the checks
// row under it. Both are readings of other works' allocations, and both were wrong in the same
// way: the D-row called D1–D192 "taken" while the allocation's own documents record D79–D89 as
// a deliberately unused gap and the redesign's D180–D192 as proposed, and the checks row listed
// 70a–70c as taken in the same cell that called 70–79 "unused and deliberately not taken", when
// the register writes `## Check 70a` and `## Check 70c`.
//
// So every block either row states is pinned to the document that allocates it, to the line of
// that document which carries the block's own label, and to the row's own sentence about it: a
// block that drifts, a block that loses its citation, a block whose state changes, and a label
// the document stops carrying all fail here rather than reading as checked. The D-block's one
// gap is pinned by the line that records it as unused, which is why the rule below -- about a
// range called unwritten -- is needed on the checks row only: there, "unwritten" is a claim the
// register can contradict by writing a section, and the row that carried it did not name the gap
// at all.
var coordinatorDecisionBlocks = []coordinatorNumberBlock{
	{"D1–D20", coordinatorMobileReadme, "| **D1–D20** | the desktop console", "**D1–D20** the console"},
	{"D21–D38", coordinatorMobileReadme, "| **D21–D38** | the console v2 pair", "**D21–D38** console v2"},
	{"D39–D54b", coordinatorMobileReadme, "| **D39–D54, plus D54b** | the voice work", "**D39–D54b** voice"},
	{"D55–D78", coordinatorMobileReadme, "| **D55–D78** | managed projects", "**D55–D78** managed projects"},
	{"D90–D109", coordinatorMobileReadme, "| **D90–D109** | the website work", "**D90–D109** the website"},
	{"D110–D129", coordinatorMobileReadme, "| **D110–D129** | **this work**", "**D110–D129** mobile"},
	{"D130–D159", coordinatorMobileReadme, "| **D130–D159**, **checks 90–99** | the credentials service", "**D130–D159** the credentials service"},
	{"D160–D179", coordinatorMobileReadme, "| **D160–D179** | **the functional spec v0.1**", "**D160–D179** the mobile functional spec"},
	{"D180–D192", "docs/website/redesign/README.md", "**`D180`–`D192`**, the first", "**D180–D192** the website redesign, proposed"},
	{"D79–D89", "docs/website/redesign/implementation-plan.md", "`D79`–`D89` is a deliberately unused gap", "**D79–D89** is **deliberately unused**"},
}

// coordinatorCheckBlocks is the checks row: the register's own blocks, and the two documents
// whose allocations the register does not reach.
var coordinatorCheckBlocks = []coordinatorNumberBlock{
	{"48–56", coordinatorChecklist, "**48–56 are reserved by the project-centric plan**", "**48–56** reserved by the project-centric plan"},
	{"59a/59b", coordinatorChecklist, "does not carry it yet), **59 is the desktop-console plan's**", "**59a/59b** the desktop console's"},
	{"60", coordinatorChecklist, "register does not carry it either) and **60 is the budget-token channel's**", "**60** the budget-token channel's"},
	{"61", coordinatorChecklist, "**project-centric plan also holds 61** — the folder", "**61** project-centric phase 6's"},
	{"62–63", coordinatorChecklist, "59 (as 59a/59b), 60, 61, 62–63, 64–66 and 67–68 are reserved by the console,", "**62–63** console v2"},
	{"64–66", coordinatorChecklist, "59 (as 59a/59b), 60, 61, 62–63, 64–66 and 67–68 are reserved by the console,", "**64–66** voice"},
	{"67–68", coordinatorChecklist, "59 (as 59a/59b), 60, 61, 62–63, 64–66 and 67–68 are reserved by the console,", "**67–68** managed projects, each reserved"},
	{"69a–69c", coordinatorChecklist, "**Check 69 is the website work's, written as 69a–69c**", "**69a–69c** the website's"},
	{"70a–70c", coordinatorChecklist, "**Check 70 is the website redesign's, proposed as 70a–70c**", "**70a–70c** the website redesign's, proposed"},
	{"71–79", coordinatorChecklist, "**The website work holds 69 and up, and the mobile", "**71–79** inside the website work's held range, with no check written in them"},
	{"80–89", coordinatorChecklist, "work holds 80–89 carved out of that range**", "**80–89** mobile's, carved out of that range"},
	{"90–99", coordinatorCredentialsReadme, "| **Checks 90–99** | reserved for this work's owner-run checks", "**90–99** the credentials service, written **Checks 90–99**"},
	{"100–109", coordinatorMobileReadme, "| **Checks 100–109** | **the functional spec v0.1**", "**100–109** the mobile functional spec, written **Checks 100–109**"},
	{"100–104", coordinatorMobilePlan, "**6 — APPROVED 2026-10-18 (functional spec v0.1)**", "**100–104** are **approved 2026-10-18**"},
	{"105–109", coordinatorMobileReadme, "**105–109 reserved and unused**", "**105–109** are **reserved and unused**"},
	{"", coordinatorChecklist, "**Numbering ruling (owner's assistant, 2026-10-13).**", "the numbering ruling of 2026-10-13"},
	{"", coordinatorMobileReadme, "Checks 70–79 are unused and deliberately not taken", "records them as unused and not taken"},
}

// coordinatorSectionCitationRE finds the citations the numbers section makes: a backticked
// repository document, with the line spec it may carry as a second group -- OR a line spec on its
// own, with no document in the same token. The section used to carry line numbers here, and they
// are what this test's anchors replaced, so the line spec is read and refused rather than left
// unmatched, in both of its shapes.
//
// It was left unmatched twice. First by a regexp that required the closing backtick straight
// after `.md`: a `path:line` token in this section matched nothing, so the block's citation was
// silently attributed to the next block's, and a line number could be put back with every test
// green. Then by this regexp, which required a full `path.md` before the line spec and so could
// not see the other shape the section used: the bare relative reference beside the block
// (`:25`), which resolves against whatever document happens to precede it and is therefore the
// more brittle of the two, not the less. Both shapes are now matched, and the assertion below
// refuses either one.
var coordinatorSectionCitationRE = regexp.MustCompile("`(?:([A-Za-z0-9_./-]+\\.md)(:[0-9]+(?:[-,+][0-9]*)*)?|(:[0-9]+(?:[-,+][0-9]*)*))`")

// coordinatorSectionProseCitations are the section's citations that are not block citations: the
// document the Check 110 row names in prose. They are listed rather than ignored,
// and an entry that goes unused fails like an unused exception does, so that a citation moving
// out of a block and into the prose is a failure and not a silence.
var coordinatorSectionProseCitations = map[string]string{
	"docs/design/always-on-orchestrator.md": "the Check 110 row names the design claiming that number; the design is in open PR #632 and not on main",
}

// coordinatorUnwrittenRE finds the words that call a range's numbers unspoken for. They are the
// trigger for the rule below, not a spelling the row has to use.
var coordinatorUnwrittenRE = regexp.MustCompile(`unused|not taken|free`)

// coordinatorCheckRangeRE finds a check-number range as the row writes it. A number with a
// letter against it is not one: `70a–70c` is a block the table above pins by name, and the
// range rule is about the numbers a claim of "unwritten" covers.
var coordinatorCheckRangeRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9])([0-9]+)–([0-9]+)`)

// coordinatorWrittenCheckRE finds a written check section in the register: the sections that
// make a number taken whatever any table says about it.
var coordinatorWrittenCheckRE = regexp.MustCompile(`(?m)^## Check ([0-9]+)`)

// coordinatorHighestWrittenRE reads the checks row's own statement of how far the register has
// got. It is a claim about the register, so the register is what decides it: the row said the
// highest section written is 90 while another sentence of the same row said the register does
// not reach Checks 90-99, and nothing read either against the document.
var coordinatorHighestWrittenRE = regexp.MustCompile(`The highest check \*section written\* is \*\*([0-9]+)\*\*`)

// coordinatorDRangeRE finds a decision-number range as the table writes it, with or without the
// second D: `D79–D89`, `D1–D192`, `D39–D54b`.
var coordinatorDRangeRE = regexp.MustCompile(`D([0-9]+)[a-z]?–D?([0-9]+)`)

// coordinatorDNumberRE reads the numbers a decision block is written with, so that a block's
// own extent can be compared with a range a claim of "unwritten" covers.
var coordinatorDNumberRE = regexp.MustCompile(`D([0-9]+)`)

// coordinatorBlockExtent reads the first and last number of a decision block: `D39–D54b` is 39
// to 54, and `D1–D20` is 1 to 20.
func coordinatorBlockExtent(block string) (int, int, bool) {
	nums := coordinatorDNumberRE.FindAllStringSubmatch(block, -1)
	if len(nums) == 0 {
		return 0, 0, false
	}
	lo, err := strconv.Atoi(nums[0][1])
	if err != nil {
		return 0, 0, false
	}
	hi, err := strconv.Atoi(nums[len(nums)-1][1])
	if err != nil {
		return 0, 0, false
	}
	return lo, hi, true
}

// coordinatorUnwrittenWindow is how far after a range the table's own words are read when
// deciding whether it has been called unwritten. The D-block row names an unused gap and a
// proposed block in one cell, so the claim is the phrase beside the range, not the cell.
const coordinatorUnwrittenWindow = 40

// coordinatorNumbersSection returns the README's numbers section, from its heading to the next
// one. The checks row and the note under it are read as one block, because they are one claim.
func coordinatorNumbersSection(readme string) string {
	start := strings.Index(readme, coordinatorNumbersSectionHeading)
	if start < 0 {
		return ""
	}
	section, _, _ := strings.Cut(readme[start+len(coordinatorNumbersSectionHeading):], "\n## ")
	return section
}

// coordinatorAnchorLine returns the one line of a document that carries an anchor, and its line
// number so a failure can name it. It reports false, with the failure reported, when the
// document carries no such line and when it carries more than one: a label that is gone is a
// block the row reads from nowhere, and a label on two lines is a citation two readers would
// open differently -- which is the failure an anchor is chosen to make impossible, and this is
// the check that keeps the choice honest.
//
// what names the block or the claim the anchor is held for, so that a failure says which one
// lost its line rather than only which document was searched.
func coordinatorAnchorLine(t *testing.T, doc, anchor, what string) (string, int, bool) {
	t.Helper()
	lines := coordinatorFileLines(t, doc)
	if lines == nil {
		return "", 0, false
	}
	at, found := -1, ""
	for i, line := range lines {
		if !strings.Contains(line, anchor) {
			continue
		}
		if at >= 0 {
			t.Errorf("%s carries %q on line %d and again on line %d, and it is the anchor %s is read from: an anchor that names two lines is a citation two readers would follow to different places, so this block needs one that names the line it means",
				doc, anchor, at, i+1, what)
			return "", 0, false
		}
		at, found = i+1, line
	}
	if at < 0 {
		t.Errorf("%s carries no line with %q, and that is the anchor %s is read from: a citation by content is a claim that the document carries that phrase, and a phrase the document does not carry is a citation no reader can follow",
			doc, anchor, what)
		return "", 0, false
	}
	return found, at, true
}

// TestDocsCoordinatorNumbersTableIsReadFromItsSources: the README's numbers table is a reading
// of other documents' allocations, and it is checked against them block by block.
//
// This is the check the numbers table needed and did not have. Both of its rows were prose no
// test read, and both were wrong in the same way: the D-row called D1–D192 taken while the
// allocation's own documents record D79–D89 as deliberately unused, and the checks row listed
// 70a–70c as taken and, in the same cell, called 70–79 "unused and deliberately not taken". The
// register writes 70a and 70c and records the website work as holding 69 and up, so the row
// contradicted itself and the document it cited. Three assertions now hold the table:
//
//   - every document the section cites is one of the blocks pinned above, or one of the two the
//     prose names and the pins list by name, so a block added without a pin fails rather than
//     going unread;
//   - every block is named, carries its own state in the row's words, is read from the document
//     it cites, and that document still carries exactly one line with the block's own label --
//     which is the citation, now that the lines are gone;
//   - no range the checks row calls unwritten covers a number the register has written, which
//     is the rule the 70–79 claim broke.
func TestDocsCoordinatorNumbersTableIsReadFromItsSources(t *testing.T) {
	readme := readDoc(t, coordinatorReadme)
	section := coordinatorNumbersSection(readme)
	if section == "" {
		t.Fatalf("%s has no %q section: this test would otherwise check nothing", coordinatorReadme, coordinatorNumbersSectionHeading)
	}
	row := ""
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, coordinatorChecksRowHead) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("%s carries no %q row: the numbers table has lost the row this test is about, and with it the statement of which check numbers are spoken for", coordinatorReadme, coordinatorChecksRowHead)
	}
	if !strings.Contains(section, coordinatorDecisionsRowHead) {
		t.Fatalf("%s carries no %q row: the numbers table has lost the statement of which decision numbers are spoken for", coordinatorReadme, coordinatorDecisionsRowHead)
	}
	decisions := ""
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, coordinatorDecisionsRowHead) {
			decisions = line
			break
		}
	}
	blocks := append(append([]coordinatorNumberBlock{}, coordinatorDecisionBlocks...), coordinatorCheckBlocks...)

	// The census, and it comes first for the reason the citation census does: a check that
	// silently sees less than it claims is the failure this file is about. Every document the
	// section cites has to be one a block below is read from, or one of the two the prose names
	// and the list above carries -- a citation that drifts out of a block's own sentence into
	// the prose, and a document cited with no pin behind it, both fail here rather than going
	// unread.
	claimed := map[string]bool{}
	for _, e := range blocks {
		claimed[e.doc] = true
	}
	cites := coordinatorSectionCitationRE.FindAllStringSubmatchIndex(section, -1)
	if len(cites) == 0 {
		t.Fatalf("%s's numbers section cites no document at all: the table is a reading of other documents' allocations, and a number in it with no document behind it is a number no reader can check", coordinatorReadme)
	}
	prose := map[string]bool{}
	for _, m := range cites {
		doc := ""
		if m[2] >= 0 {
			doc = section[m[2]:m[3]]
		}
		// The line spec, in either of the two shapes this section used to carry: attached to a
		// document (`docs/mobile/README.md:25`) or bare (`:25`, with the document taken from the
		// sentence around it). Every citation in the section is by content now, and a number
		// here is the check this round removed. It is reported before the path is looked at,
		// because the same token would otherwise be read as a clean citation of the document and
		// the number would go unmentioned -- which is what happened while the matcher required
		// the backtick straight after `.md`: the token matched nothing, the block's citation
		// fell through to the next block's, and the line number was checked by nothing at all.
		//
		// The bare shape is the worse of the two and was the longer-lived hole: it names no
		// document in its own token, so what it points at is whatever document happens to
		// precede it, and no reader can follow it without reading the sentence. That is the
		// citation an anchor replaces, and the matcher above could not see it at all while it
		// required a full `path.md` before the line spec.
		spec := ""
		switch {
		case m[4] >= 0:
			spec = section[m[4]:m[5]]
		case m[6] >= 0:
			spec = section[m[6]:m[7]]
		}
		if spec != "" {
			if doc == "" {
				t.Errorf("%s's numbers section carries the bare line reference %s: a line number with no document in its own token is read against whatever document precedes it, which is the citation this round replaced with an anchor -- the anchor in the tables above is what holds the claim, and it needs no number",
					coordinatorReadme, section[m[0]:m[1]])
			} else {
				t.Errorf("%s's numbers section cites %s at %s: every citation in it is by content now, and a line number is the brittle citation this round removed -- the anchor in the tables above is what holds the claim, and it needs no number",
					coordinatorReadme, doc, spec)
			}
			continue
		}
		if claimed[doc] {
			continue
		}
		reason, ok := coordinatorSectionProseCitations[doc]
		if !ok {
			t.Errorf("%s's numbers section cites %s, which no block in the tables above is read from: the section states a number against a document, and this test does not check that it says so there",
				coordinatorReadme, doc)
			continue
		}
		prose[doc] = true
		t.Logf("%s's numbers section cites %s in prose: %s", coordinatorReadme, doc, reason)
	}
	for doc := range coordinatorSectionProseCitations {
		if !prose[doc] {
			t.Errorf("%s's numbers section no longer cites %s, but coordinatorSectionProseCitations still lists it: delete the entry, or the next block citation that lands on that document is unchecked",
				coordinatorReadme, doc)
		}
	}

	for _, e := range blocks {
		if e.block == "" {
			// A citation the section makes in prose rather than for a block.
			if !strings.Contains(section, e.row) {
				t.Errorf("%s's numbers section no longer carries %q: the claim this pin is about is gone, and with it the citation it justifies", coordinatorReadme, e.row)
			}
			if !strings.Contains(section, "`"+e.doc+"`") {
				t.Errorf("%s's numbers section no longer cites %s for %q: the claim is stated with no document behind it",
					coordinatorReadme, e.doc, e.row)
			}
			coordinatorAnchorLine(t, e.doc, e.anchor, "the claim "+strconv.Quote(e.row))
			continue
		}
		off := strings.Index(section, "**"+e.block+"**")
		if off < 0 {
			t.Errorf("%s's numbers table no longer names %s: the table is a reading of other documents' allocations, and a block that drops out of it is a number a reader will look for where it is not",
				coordinatorReadme, e.block)
			continue
		}
		if !strings.Contains(section, e.row) {
			t.Errorf("%s's numbers table no longer says %q of %s: the block is named and its state is not, which is the shape the 70–79 claim had -- a range in the table that the document it cites does not bear out",
				coordinatorReadme, e.row, e.block)
		}
		// The first citation after the block is the document the row reads it from: a citation
		// that moved to another document is the block read from the wrong place, and would
		// otherwise be checked against nothing.
		next, doc := -1, ""
		for _, m := range cites {
			if m[0] > off {
				next = m[0]
				// A bare line reference names no document: the pin below reads the block
				// from a document, so it stays "" and the failure names the disagreement.
				if m[2] >= 0 {
					doc = section[m[2]:m[3]]
				}
				break
			}
		}
		if next < 0 {
			t.Errorf("%s's numbers table names %s with no document after it: every block in it is read from a document, and a block with no document behind it is a number no reader can check",
				coordinatorReadme, e.block)
			continue
		}
		if doc != e.doc {
			t.Errorf("%s's numbers table reads %s from %s, and the document this pin reads it from is %s: the table and the pin disagree about where the number comes from",
				coordinatorReadme, e.block, doc, e.doc)
			continue
		}
		coordinatorAnchorLine(t, e.doc, e.anchor, e.block)
	}

	// And the rule the checks row broke: a range it calls unwritten must not cover a number the
	// register has written. The row called 70–79 "unused and deliberately not taken" while
	// listing 70a–70c as taken in the same cell, and the register writes `## Check 70a` and
	// `## Check 70c`, so 70 sits inside that range. The words are the trigger and the register
	// is the judge, so the next range written that way fails too. It is applied to the row and
	// not to the note below it, which exists to record that two documents read 70–79
	// differently and so has to be able to say so.
	written := map[int]bool{}
	for _, m := range coordinatorWrittenCheckRE.FindAllStringSubmatch(readDoc(t, coordinatorChecklist), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			written[n] = true
		}
	}
	if len(written) == 0 {
		t.Fatal("the register carries no written check section: this assertion would otherwise pass whatever the row claims about a range")
	}
	for _, cell := range strings.Split(row, "|") {
		if !coordinatorUnwrittenRE.MatchString(cell) {
			continue
		}
		for _, m := range coordinatorCheckRangeRE.FindAllStringSubmatch(cell, -1) {
			lo, _ := strconv.Atoi(m[1])
			hi, _ := strconv.Atoi(m[2])
			for n := lo; n <= hi; n++ {
				if written[n] {
					t.Errorf("%s's checks row calls %s unwritten, and the register writes a `## Check %d` section inside it: the row and the document it cites disagree, and the register is the one that allocates",
						coordinatorReadme, m[0], n)
				}
			}
		}
	}

	// The row's own count of what the register has written, held against the register: the state
	// column says the highest check SECTION written is a number, and the register's own `## Check`
	// sections are what decide it. This is the half the rule above cannot see -- it fires on a
	// range called unused or free, and the row's statement about what IS written had nothing
	// reading it, which is how the same row came to say both that the highest section written is
	// 90 and that the register does not reach Checks 90-99.
	if m := coordinatorHighestWrittenRE.FindStringSubmatch(row); m != nil {
		claimed, _ := strconv.Atoi(m[1])
		highest := 0
		for n := range written {
			if n > highest {
				highest = n
			}
		}
		if claimed != highest {
			t.Errorf("%s's checks row says the highest check section written is %d, and the register's highest `## Check` section is %d: the row is a reading of the register, and the two move together",
				coordinatorReadme, claimed, highest)
		}
	} else {
		t.Fatalf("%s's checks row no longer states the highest check section written: this test's marker has moved, and that statement is what a reader takes the register's progress from", coordinatorReadme)
	}

	// And the same rule on the D-block row, with the allocation's own lines as the judge: a
	// range it calls unwritten must not cover a block those lines record as held. The row names
	// D79–D89 as deliberately unused, and that gap is the one entry whose line says so -- so the
	// row's claim is checked against the entries themselves, and a row that called a held block
	// unused, or widened the gap over one, fails here. The words are looked for beside the range
	// rather than anywhere in the cell, because this row names an unused gap and a proposed
	// block in one cell.
	var held [][2]int
	for _, e := range coordinatorDecisionBlocks {
		if coordinatorUnwrittenRE.MatchString(e.anchor) {
			continue // the one block its own line records as unused
		}
		if lo, hi, ok := coordinatorBlockExtent(e.block); ok {
			held = append(held, [2]int{lo, hi})
		}
	}
	if len(held) == 0 {
		t.Fatal("no D-block in the tables above is recorded as held: this assertion would otherwise pass whatever the row claims about a range")
	}
	for _, cell := range strings.Split(decisions, "|") {
		for _, m := range coordinatorDRangeRE.FindAllStringSubmatchIndex(cell, -1) {
			after := cell[m[1]:]
			if len(after) > coordinatorUnwrittenWindow {
				after = after[:coordinatorUnwrittenWindow]
			}
			if !coordinatorUnwrittenRE.MatchString(after) {
				continue
			}
			lo, _ := strconv.Atoi(cell[m[2]:m[3]])
			hi, _ := strconv.Atoi(cell[m[4]:m[5]])
			for _, h := range held {
				if lo <= h[1] && h[0] <= hi {
					t.Errorf("%s's D-block row calls D%d–D%d unwritten, and the allocation's own documents record D%d–D%d as held: the row and the documents it cites disagree, and the allocation is what decides",
						coordinatorReadme, lo, hi, h[0], h[1])
				}
			}
		}
	}
}

// coordinatorContentAnchoredDirs are the trees the reconciliation cites by content and never by
// line number: `docs/design/` and `docs/mobile/`, which other works add sections and rows to
// while this one sits in review. A line number into one of them goes stale silently -- nothing
// about a number says which sentence it meant -- and it went stale here: the sentence that cites
// `docs/design/desktop-console.md` for D2 pointed at `:404`, a table's separator rule, when the
// D2 row it is about is at 406.
var coordinatorContentAnchoredDirs = []string{"docs/design/", "docs/mobile/"}

// coordinatorContentAnchored reports whether a cited path is in one of those trees.
func coordinatorContentAnchored(path string) bool {
	for _, dir := range coordinatorContentAnchoredDirs {
		if strings.HasPrefix(path, dir) {
			return true
		}
	}
	return false
}

// coordinatorContentAnchor holds one citation into those trees to the content it names: anchor
// is the text of the one line of doc that carries the claim, written exactly as doc writes it.
// The line number is not written down anywhere, here or in the prose -- it is found, which is
// what makes inserting a paragraph above it somebody else's change to make.
type coordinatorContentAnchor struct {
	doc    string // the document the reconciliation cites
	claim  string // the claim the citation makes about it
	anchor string // the text of the one line in doc that carries the claim
}

// coordinatorContentAnchors are those citations: ONE ENTRY PER CITATION, in the order the
// citations appear in reconciliation.md, and a document cited three times therefore appears three
// times. That is deliberate, and it is the difference between this table and a list of documents:
// the claim is that every citation into either tree is held, and a document cited four times with
// one anchor behind it is one citation held and three that are not. The test below compares the
// two counts per document, so a claim added against a document that already has an anchor fails
// until it has one of its own -- which is the case a document-level check would let through.
var coordinatorContentAnchors = []coordinatorContentAnchor{
	{"docs/design/v1.md", "a Fugaro project, the spec's §2/§4 unit", "**Fugaro project** (the word users see)"},
	{"docs/design/v1.md", "the installation, the same object named from the cloud side", "**Installation.** The project's cloud-side setup"},
	{"docs/design/m10-multi-model.md", "the model config is per slot, not per tier", "`agent.models.coder|reviewer|background`, `agent.model`, `budget.allowed_models`"},
	{"docs/design/desktop-console-v2.md", "D22, the one write: launch", "| **D22** | Does v2's first slice contain any write at all?"},
	{"docs/design/v1.md", "a Fugaro project, the definition finding #1 reads", "**Fugaro project** (the word users see)"},
	{"docs/design/desktop-console-v2.md", "v2's status: nothing in it is approved or built", "**Nothing in v2 is approved or built**"},
	{"docs/design/m10-multi-model.md", "the model config is per slot, not per tier, in finding #23", "`agent.models.coder|reviewer|background`, `agent.model`, `budget.allowed_models`"},
	{"docs/design/provider-price-catalog.md", "the catalog's own design admits `cheapest` can undercharge", "**`cheapest` can undercharge, and this design says so plainly rather than hiding it.**"},
	{"docs/design/desktop-console.md", "D2: v1 is strictly read-only", "| **D2** | Is v1 strictly read-only, with kill/resume/cancel deferred?"},
	{"docs/design/desktop-console-v2.md", "D22 again, for the console's one write", "| **D22** | Does v2's first slice contain any write at all?"},
	{"docs/design/desktop-console.md", "§5.1 rule 7: job-written text is text, never markup", "7. **Job-written text is text.**"},
	{"docs/mobile/reconciliation.md", "mobile D115, phase 2 hosting", "| **D115** | **Phase 2 hosting: who holds the model credential?**"},
	{"docs/mobile/implementation-plan.md", "P5.1, the always-on orchestrator, and its state", "### Task P5.1: Phase 2 hosting"},
	{"docs/mobile/reconciliation.md", "mobile D111, what the app may hold", "| **D111** | **What may the app hold?**"},
	{"docs/mobile/reconciliation.md", "mobile D115 again, for Q13's refusal", "| **D115** | **Phase 2 hosting: who holds the model credential?**"},
	{"docs/design/v1.md", "a Fugaro project, the glossary's unit", "**Fugaro project** (the word users see)"},
	{"docs/design/v1.md", "the installation, the glossary's cloud-side name for it", "**Installation.** The project's cloud-side setup"},
	{"docs/design/desktop-console-spec-v2.md", "the orchestrator, one per thread of work", "- Multiple orchestrators: typically one per thread of work"},
	{"docs/mobile/spec.md", "phase 1: the orchestrator on the owner's laptop", "- Phase 1: orchestrator runs on the owner's laptop"},
	{"docs/mobile/spec.md", "phase 2: the orchestrator moves to an always-on service", "- Phase 2: orchestrator moves to an always-on cloud service"},
	{"docs/mobile/implementation-plan.md", "P5.4, the conversational front agent", "### Task P5.4: The conversational front agent"},
	{"docs/mobile/spec.md", "the summarising agent beside the orchestrator", "- **Summarising agent** sits beside the orchestrator"},
}

// TestDocsCoordinatorFrequentCitationsAreAnchoredByContent: the reconciliation carries no line
// number into the documents other works edit, and every citation it makes into one of them is
// held to the content it names.
//
// This is the rule the README's numbers table was fixed under, applied to the reconciliation
// itself, and for the same reason: a citation into a document that grows rows and sections every
// week is a citation that rots while nobody is looking. The line numbers these replaced were
// checked only for being inside the file -- coordinatorCheckCitedLines reads the number and
// never the sentence -- so a paragraph inserted above one moved the claim and left the citation
// green. Two assertions hold the rule:
//
//   - no citation under `docs/design/` or `docs/mobile/` carries a line number, so a later edit
//     cannot put one back and get the old, silently-rotting check;
//   - every citation under either tree that names a file in this repository is named by one
//     entry in the table above, counted per document, and each anchor is carried by exactly one
//     line of its document -- so the claim and the line behind it are the same fact, a document
//     that stops making the claim fails, and a claim added against a document that already has
//     an anchor fails until it has one of its own. The citations that name something else -- the
//     designs that live only in open pull requests, and the globs quoted from the brief -- are
//     excused by name in coordinatorCitationExceptions, and only there: the exemption is an
//     audit, and it is listed with its reason rather than being a path this check skips quietly.
//
// The line each anchor lands on is logged rather than written down, which is the whole
// difference between this and the numbers it replaced: a citation that names the sentence it
// means survives the paragraph somebody else inserts above it.
func TestDocsCoordinatorFrequentCitationsAreAnchoredByContent(t *testing.T) {
	doc := readDoc(t, coordinatorReconciled)
	matches := coordinatorCitationRE.FindAllStringSubmatch(doc, -1)
	if len(matches) == 0 {
		t.Fatalf("%s cites no path at all: this test would otherwise check nothing", coordinatorReconciled)
	}

	// The rule first, and over every occurrence: a line number is what a later edit will
	// reintroduce, and the anchors below would happily co-exist with one.
	for _, m := range matches {
		path, spec := m[1], m[2]
		if spec == "" || !coordinatorContentAnchored(path) {
			continue
		}
		t.Errorf("%s cites %s at %s: a line number into a document other works add to is stale the moment one of them inserts a paragraph above it, and nothing here would notice -- cite the content it means and hold it in coordinatorContentAnchors instead",
			coordinatorReconciled, path, spec)
	}

	// Then the anchors, and then the two censuses that keep the table honest: a citation with no
	// entry behind it is unchecked, and an entry with no citation left is a pin that has outlived
	// the claim it was written for. Both are counted rather than listed, because the claim is
	// that every CITATION is held: a document cited three times with one entry behind it would
	// pass a check that compared documents, and two of its three citations would be read by
	// nothing.
	citations := map[string]int{}
	for _, m := range matches {
		if !coordinatorContentAnchored(m[1]) {
			continue
		}
		if _, excused := coordinatorCitationExceptions[m[1]]; excused {
			continue
		}
		citations[m[1]]++
	}
	if len(citations) == 0 {
		t.Fatalf("%s cites no document under %s: this test would otherwise check nothing",
			coordinatorReconciled, strings.Join(coordinatorContentAnchoredDirs, " or "))
	}
	anchors := map[string]int{}
	for _, a := range coordinatorContentAnchors {
		anchors[a.doc]++
		if _, at, ok := coordinatorAnchorLine(t, a.doc, a.anchor, a.claim); ok {
			t.Logf("%s: %s is at line %d", a.doc, a.claim, at)
		}
	}
	for path, cited := range citations {
		switch held := anchors[path]; {
		case held == 0:
			t.Errorf("%s cites %s (%s) by content, and no anchor in coordinatorContentAnchors is read from it: the citation names a document and no longer says where in it, so nothing holds it to the sentence it means",
				coordinatorReconciled, path, coordinatorCount(cited, "citation", "citations"))
		case held < cited:
			t.Errorf("%s cites %s (%s) by content and coordinatorContentAnchors holds %s for it: one anchor holds one citation, and a citation with none behind it is read by nothing -- which is what a check comparing documents rather than citations would pass",
				coordinatorReconciled, path, coordinatorCount(cited, "citation", "citations"), coordinatorCount(held, "anchor", "anchors"))
		case held > cited:
			t.Errorf("coordinatorContentAnchors holds %s in %s, and %s cites it (%s): delete the entries no citation is left for, or the next citation that lands on that document is checked against a claim that document has stopped making",
				coordinatorCount(held, "anchor", "anchors"), path, coordinatorReconciled, coordinatorCount(cited, "citation", "citations"))
		}
	}
	total := 0
	for _, held := range anchors {
		total += held
	}
	t.Logf("%s: %d citations under %s held by %d content anchors, none of them by a line number",
		coordinatorReconciled, total, strings.Join(coordinatorContentAnchoredDirs, " and "), len(coordinatorContentAnchors))
}

// coordinatorReadmeHeldCitations and coordinatorReadmeAnchorScope are the two halves of what
// the README says the content-anchor rule covers, and both are pinned, because the scope of a
// check is exactly the thing that gets widened by accident.
//
// The first is the half that IS held: the reconciliation's citations into those two trees, minus
// the ones that name something other than a file here. The second is the half that is NOT: the
// index's own citations outside its numbers section. Both were narrower than they claimed
// before this round, in the two ways this file exists to catch -- the first said "every one of
// them" while five of them name designs that exist only in open pull requests and one names a
// glob, none of which has a line to hold an anchor to; the second said the index's own citations
// were all unanchored while the numbers section's are held, block by block, by this same file.
const (
	coordinatorReadmeHeldCitations = "`TestDocsCoordinatorFrequentCitationsAreAnchoredByContent` holds every one of those citations that names a file in this repository to the single line of that document which carries the phrase"
	coordinatorReadmeAnchorScope   = "**This index's own citations into those two trees outside its numbers section name no phrase and are held by no anchor**"
)

// coordinatorCount writes a count with its noun, so that a failure reads "three citations" and
// "one anchor" rather than a bare number a reader has to work out the subject of.
func coordinatorCount(n int, one, many string) string {
	if n == 1 {
		return "one " + one
	}
	return strconv.Itoa(n) + " " + many
}

// coordinatorLineNumbers reads the line numbers out of a spec that
// coordinatorLineSpecRE has already accepted; there is always at least one.
func coordinatorLineNumbers(spec string) []int {
	var out []int
	for _, f := range coordinatorDigitsRE.FindAllString(spec, -1) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// coordinatorQuestionRE finds a question heading in the reconciliation's
// "Open for the owner" section: `### Q7 — Event intake: ...`.
var coordinatorQuestionRE = regexp.MustCompile(`(?m)^### Q([0-9]+) — `)

// TestDocsCoordinatorQuestionsAreNumberedAndOptioned: the reconciliation's
// questions are Q1..Q16, contiguous and in order, and each one gives the owner a
// real choice -- at least two options, and a recommendation among them. A
// question with one option is a decision wearing a question's clothes, which is
// the thing this work's "no D-number" rule exists to prevent.
func TestDocsCoordinatorQuestionsAreNumberedAndOptioned(t *testing.T) {
	doc := readDoc(t, coordinatorReconciled)
	const heading = "## 3. The owner's answers (Q1–Q16)"
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("%s has no %q section", coordinatorReconciled, heading)
	}
	section, _, _ := strings.Cut(doc[start+len(heading):], "\n## ")
	bounds := coordinatorQuestionRE.FindAllStringSubmatchIndex(section, -1)
	if len(bounds) == 0 {
		t.Fatal("the questions section carries no `### Q<n> — ` heading: this test would otherwise check nothing")
	}
	for i, b := range bounds {
		n, err := strconv.Atoi(section[b[2]:b[3]])
		if err != nil {
			t.Fatalf("question heading %q does not carry a number: %v", section[b[0]:b[1]], err)
		}
		if n != i+1 {
			t.Errorf("the questions run %d, %d, ...: question %d is out of order or missing", i, n, i+1)
		}
		end := len(section)
		if i+1 < len(bounds) {
			end = bounds[i+1][0]
		}
		body := section[b[0]:end]
		if options := strings.Count(body, "- **("); options < 2 {
			t.Errorf("Q%d offers %d option(s): every question here gives the owner at least two, or it is a decision and not a question", n, options)
		}
		if !strings.Contains(body, "Recommended") {
			t.Errorf("Q%d names no recommendation: the owner is owed the recommended option and why", n)
		}
		// Every question records the owner's answer of 2026-10-19 in place. A
		// question that has lost its answer reads as though it were still open,
		// which is exactly the state this document was in before this round:
		// the answers are the owner's and they are recorded, not implied by the
		// recommendation being the first option.
		if !strings.Contains(body, coordinatorAnsweredMarker) {
			t.Errorf("Q%d records no answer: the owner answered all sixteen on 2026-10-19 and every one of them took option (A), so each question carries %q", n, coordinatorAnsweredMarker)
		}
	}
	if len(bounds) != 16 {
		t.Errorf("the questions section carries %d questions, and this work's index says Q1–Q16", len(bounds))
	}
	// The index's count and the section's count are the same fact written
	// twice; a later edit that adds a question has to update both.
	readme := readDoc(t, coordinatorReadme)
	if !strings.Contains(readme, "Q1–Q16") {
		t.Errorf("%s no longer says the questions are Q1–Q16, while the section carries %d of them", coordinatorReadme, len(bounds))
	}
	// And the section heading itself names the range, so the two cannot drift
	// apart without one of them looking wrong.
	wantHeading := "## 3. The owner's answers (Q1–Q" + strconv.Itoa(len(bounds)) + ")"
	if !strings.Contains(doc, wantHeading) {
		t.Errorf("%s's questions section is headed %q while it carries %d questions: the heading and the count disagree", coordinatorReconciled, heading, len(bounds))
	}
}

// TestDocsCoordinatorMutationRecordIsCounted reads this file rather than the
// documents, and holds the mutation record to the list under it.
//
// The record is prose about work already done -- it cannot be run, and nothing
// in it proves a mutation ever fired. The one thing about it that IS
// mechanical is its own census: the header states how many mutations were
// planted and reverted, and every one of them appears below as a bullet
// carrying exactly one arrow. Counting the arrows is the same move the
// citation check makes against the document, and it exists for the same
// reason -- a check that claims more than it sees is the failure this whole
// file is about. It caught one: the header read "twenty-six" over a list of
// twenty-four, because two earlier bullets had been replaced by unrelated new
// pins instead of kept.
//
// The count is a single digit ("-- <N>,") so this test can read it without a
// word-to-number table, and so there is nothing for a second copy of it to
// disagree with: a word beside a digit would let the two drift apart with
// neither looking wrong, which is the shape of the mistake here. Nothing in
// this test quotes the number either -- the form below is built from the same
// constant the parse uses -- because a number written twice in this file is a
// number that can drift, and this file's whole subject is checks that quietly
// stop being true.
func TestDocsCoordinatorMutationRecordIsCounted(t *testing.T) {
	src, err := os.ReadFile("docs_coordinator_test.go")
	if err != nil {
		t.Fatalf("reading this test file to check its own mutation record: %v", err)
	}
	text := string(src)

	const open = "// Mutation (verified, run and restored) -- "
	start := strings.Index(text, open)
	if start < 0 {
		t.Fatalf("the mutation record's opening line (%q...) is gone: this test has nothing to check", open)
	}
	// The record runs from that line to the paragraph that disclaims its own
	// numbers; the arrows outside it (the source's own operators, other
	// comments) are not mutations and must not be counted.
	const close = "// The numbers above are a reading"
	end := strings.Index(text[start:], close)
	if end < 0 {
		t.Fatalf("the mutation record's closing paragraph (%q...) is gone, so the list it heads has no end this test can find", close)
	}
	record := text[start : start+end]

	declaredRE := regexp.MustCompile(`^` + regexp.QuoteMeta(open) + `([0-9]+),`)
	m := declaredRE.FindStringSubmatch(record)
	if m == nil {
		t.Fatalf("the mutation record's opening line must state its count as a digit, in the form %q, so this test can read it", open+"<N>,")
	}
	declared, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("the mutation record's stated count %q is not a number: %v", m[1], err)
	}

	listed := strings.Count(record, "->")
	if listed != declared {
		t.Errorf("the mutation record claims %d mutations and lists %d: every mutation below is one bullet carrying one arrow, so the stated count and the list are the same census, and a stated count the list does not bear out is a check claiming more than it sees -- which is exactly how this number drifted from the list once already", declared, listed)
	}
	if declared == 0 {
		t.Error("the mutation record claims no mutations at all: a record of zero planted mutations proves nothing about the checks above it")
	}
}

// coordinatorTestFuncRE finds every test this file declares for this folder, by
// the name they all share.
var coordinatorTestFuncRE = regexp.MustCompile(`(?m)^func (TestDocsCoordinator[A-Za-z]+)\(t \*testing\.T\) \{`)

// coordinatorCountWords spells a small count the way this file's own prose and
// the index's sentence write it, so the two can be compared with the number of
// tests the file actually declares.
var coordinatorCountWords = []string{
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight",
	"nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
	"sixteen", "seventeen", "eighteen", "nineteen", "twenty",
}

// TestDocsCoordinatorSelfMapIsCounted: this file's map of itself and the index's
// sentence about it both state HOW MANY tests there are, and both are claims
// about this file rather than about the documents -- which is why neither was
// checked, and why both went stale the moment a test was added: the map said
// "eleven" over twelve tests and the index repeated it.
//
// It is the same defect TestDocsCoordinatorMutationRecordIsCounted exists to
// catch, one level out: a count is the easiest claim to leave behind, because
// nothing about the thing it counts looks wrong when it moves. So the three
// readings are held to each other -- the tests the file declares, the map's own
// count and its one bullet per test, and the index's sentence -- and the map is
// read as the list it is, so a test added without an entry fails here rather
// than joining a list nobody re-counts.
func TestDocsCoordinatorSelfMapIsCounted(t *testing.T) {
	src, err := os.ReadFile("docs_coordinator_test.go")
	if err != nil {
		t.Fatalf("reading this test file to check its own map of itself: %v", err)
	}
	text := string(src)

	names := coordinatorTestFuncRE.FindAllStringSubmatch(text, -1)
	if len(names) == 0 {
		t.Fatal("no TestDocsCoordinator... test was found in this file, so its own map cannot be counted against anything")
	}
	if len(names) >= len(coordinatorCountWords) {
		t.Fatalf("this file declares %d coordinator tests, more than coordinatorCountWords spells; extend that table rather than dropping the check", len(names))
	}
	word := coordinatorCountWords[len(names)]

	if want := "Each of the " + word + " tests below"; !strings.Contains(text, want) {
		t.Errorf("this file declares %d coordinator tests and its own map does not say so: it lacks %q, and a count in prose is the claim that moves while nothing about what it counts looks wrong", len(names), want)
	}
	for _, m := range names {
		if want := "//  * " + m[1] + " --"; !strings.Contains(text, want) {
			t.Errorf("this file declares %s and its own map carries no %q bullet: a test the map does not list is a check no reader of the map knows is there", m[1], want)
		}
	}
	index := readDoc(t, coordinatorReadme)
	want := strings.ToUpper(word[:1]) + word[1:] + " tests pin everything above"
	if !strings.Contains(index, want) {
		t.Errorf("%s does not carry %q: the index states how many tests hold it, and that count is a claim about this file like any other", coordinatorReadme, want)
	}
}

// coordinatorPlanNewPaths are the paths the plan cites that are new -- marked
// **new** in the document because they do not exist yet -- each with the part
// that creates it. The check is the reverse of the citation exceptions above:
// an entry here must NOT resolve on disk, so that the day one of them lands the
// test fails and the citation starts being checked as the ordinary path it has
// become. An exception map that only excuses is how a check rots; this one is
// written to expire by itself.
// It is empty because P1 has landed: internal/cli/coordinator.go was the one
// path it ever excused, and the entry went in the commit that created the file
// (part P1, `fugaro coordinator status`). The map stays, empty, because
// the check that reads it is the one that must fail the day a part's plan
// declares a path that does not exist yet.
var coordinatorPlanNewPaths = map[string]string{}

// coordinatorPlanNewDirs are the same thing one level down: the directories the
// plan names in backticks that are new -- written "New `<path>`" in the Files
// line of the part that creates them -- each with that part. They are held to
// the same rule as the files above, and the reason they need an entry is the
// reason coordinatorPlanDirectoryRE exists: a directory is a citation the file
// checks cannot see, because a citation is matched by its file extension and a
// directory has none. Without this the plan could cite a directory that is
// neither there nor declared new and nothing would say so -- which is the state
// `internal/coordinator/` was in: cited seven times across the six parts after
// P0 whose Files lines name it, not there, declared new by P1, and read by no
// check at all.
// It holds one entry today. `internal/coordinator/` and
// `internal/coordinator/testdata/` left it in the commit that created them
// (P1, `fugaro coordinator status`), the same commit that emptied
// coordinatorPlanNewPaths above, and the plan's citations of both are now
// checked as the ordinary directories they have become.
var coordinatorPlanNewDirs = map[string]string{
	"deploy/terraform/gcp/modules/coordinator/": "P7's new Terraform module: the service, its account and its scale-to-zero configuration",
}

// coordinatorPlanNonPaths are the plan's backticked directory-shaped tokens that
// name something other than a directory in this tree, each with what it names.
// They are excused by name for the same reason the citation exceptions are: the
// check reads every token of that shape, and a token it cannot resolve has to be
// accounted for rather than skipped, or the next such token is unread.
//
// All four name nodes in the **Realtime Database**, not the repository: D195
// lays the coordinator's state out under a top-level `coordinator/` subtree with
// `project/` and `repos/<slug>/` beneath it, and `config/` is the existing node
// P4's writer account can reach. They are written in backticks exactly as a
// directory in this tree would be, which is why the distinction is recorded here
// rather than left to a reader of the plan.
var coordinatorPlanNonPaths = map[string]string{
	"coordinator/":              "the Realtime Database subtree D195 lays out, not a directory in this tree",
	"coordinator/project/":      "the project-wide facts under D195's subtree, in the database",
	"coordinator/repos/<slug>/": "the per-repository facts under D195's subtree, in the database",
	"config/":                   "the database node P4's writer account can reach, not a directory in this tree",
}

// coordinatorPlanDirectoryRE finds a backticked token that names a directory:
// one segment or more, each closed by `/`, and `<slug>` included so that a
// directory-shaped token the check cannot resolve is reported rather than
// skipped. It is the half of "the plan cites by path" that coordinatorCitationRE
// and the census beside it are both blind to, because both look for a file
// extension and a directory carries none.
var coordinatorPlanDirectoryRE = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./<>-]*/)`")

// coordinatorPlanDirCensusRE is the independent count of the directory-shaped
// tokens the document holds, and it is deliberately dumber than the regexp
// above: any backticked token that ends in a slash, whatever it starts with and
// whatever is inside it. It is the floor the directory check is measured
// against, for the reason the path census is the floor of the citation check --
// a regexp that has stopped matching the shapes the prose uses finds fewer
// citations and says nothing.
var coordinatorPlanDirCensusRE = regexp.MustCompile("`([^`\n]*/)`")

// coordinatorPlanDirCensusExceptions are the plan's slash-ending tokens that are
// not directory citations: a command line whose last word happens to be a path.
// Each is an audited case, and the check fails if one goes unused, so an entry
// that outlives its sentence is deleted rather than left excusing nothing --
// the same rule the citation exception maps follow.
var coordinatorPlanDirCensusExceptions = []string{
	"terraform fmt -check -recursive deploy/terraform/",
}

// coordinatorPlanRolePhraseRE is the reading D196 replaced, and it is matched as
// a phrase rather than pinned as a line because the plan carried it in four
// places -- its §1, its §3, its gate table's Q1–Q16 row and its P7 -- and a
// rewrite that fixes three of them leaves the fourth saying the coordinator is a
// role the central orchestrator's service plays. What it matches is the
// ASSERTION, not the words: the plan still describes the sibling plan's own
// per-installation service, which is a fact about that plan and stays true, and
// its §1 records the reading D196 replaced as history, which is what makes the
// change readable. What may not survive is a sentence of this plan's own saying
// the front door belongs to somebody else's service, or that this work builds
// none.
var coordinatorPlanRolePhraseRE = regexp.MustCompile(`as a role it plays|as a role \(Q10\)|builds no second service|builds no second one|must not build a second|the central orchestrator plan builds is the one`)

// coordinatorPlanOwnService is the plan's sentence stating whose service the
// coordinator's front door is, after D196 was answered (B).
const coordinatorPlanOwnService = "**its own resident service**"

// coordinatorPlanSharedAdmission is the plan's sentence recording that the
// allow-list admission of the Cloud Run service type is shared with the central
// orchestrator plan's P1, and that whichever part lands first admits it while
// the other cites it. The two plans cannot both add the type, and the second one
// to land must not re-add it.
const coordinatorPlanSharedAdmission = "**whichever part lands first admits it and the other cites it**"

// TestDocsCoordinatorPlanIsWritten: the plan exists, says what state the work is
// in, and is an ORDER rather than a list.
//
// The status line is the same claim the index makes and it is pinned in both
// documents, because they are read separately: a builder reads the plan and a
// visitor reads the index, and the plan is the one that could quietly stop
// saying that nothing is implemented. The parts are pinned in order for the
// plan's own reason: its §2 spends five paragraphs on the dependencies that
// force the order, and a P-number that moved while its heading stayed would
// leave that prose describing a sequence the headings no longer carry.
//
// The gate heading is pinned by name because the brief for this plan asked for
// that section by name, and a gate a reader cannot find is not a gate. Every one
// of D193-D197 is pinned in the plan's body, not only in the reconciliation:
// the gate table is where a reader learns which part waits on which decision,
// and a decision named in one document and missing from the other is the
// mismatch this pair of tests exists to catch.
func TestDocsCoordinatorPlanIsWritten(t *testing.T) {
	plan := readDoc(t, coordinatorPlan)
	for _, want := range []string{
		coordinatorPlanStatus,
		coordinatorPlanGateColumns,
		coordinatorBase,
		coordinatorCheckedAgainst,
		// The part the status line counts as built says so itself, in its own
		// section: a status line claiming a part is in the tree while the part's
		// section still reads as unbuilt is the same class of mismatch as a
		// status line claiming nothing is built while the code is on the branch.
		coordinatorPlanBuiltStatus,
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q", coordinatorPlan, want)
		}
	}
	if !coordinatorPlanGateHeadingRE.MatchString(plan) {
		t.Errorf("%s carries no section headed %q: the brief for this plan asked for that section by name", coordinatorPlan, "## <n>. What the owner must answer before any part starts")
	}
	// In order, not merely present: a part moved is a part reordered.
	at := 0
	for _, heading := range coordinatorPlanParts {
		i := strings.Index(plan[at:], heading)
		if i < 0 {
			t.Errorf("%s lacks the part heading %q, or carries it out of order", coordinatorPlan, heading)
			continue
		}
		at += i + len(heading)
	}
	// Every decision the planning found is a row of the gate table, which is
	// where a reader learns which part waits on it. Read from the section rather
	// than from the document: a decision named somewhere in a six-hundred-line
	// plan is not a decision the owner will find when they open the gate, and
	// naming it in the prose while dropping its row is exactly the edit that
	// leaves the table claiming to be the whole list.
	gate := ""
	if loc := coordinatorPlanGateHeadingRE.FindStringIndex(plan); loc != nil {
		rest := plan[loc[1]:]
		if next := strings.Index(rest, "\n## "); next >= 0 {
			gate = rest[:next]
		} else {
			gate = rest
		}
	}
	if gate == "" {
		t.Fatalf("%s' gate section is empty or unfindable, so its rows cannot be read", coordinatorPlan)
	}
	for _, n := range coordinatorPlanDecisions {
		if !strings.Contains(gate, "**"+n+"**") {
			t.Errorf("%s' gate table has no row naming %s: the table is the list of what the owner must answer before any part starts, and a decision missing from it is one nobody is told about", coordinatorPlan, n)
		}
	}
	// The paths the plan declares new are named in it, and it says they are new.
	// The citation test below is what holds them to not existing; this half is
	// that a reader can tell which of the plan's citations are promises.
	for path := range coordinatorPlanNewPaths {
		if !strings.Contains(plan, path) {
			t.Errorf("%s no longer names %q, which coordinatorPlanNewPaths still excuses: delete the entry, or a real citation of that path goes unchecked", coordinatorPlan, path)
		}
	}
	if !strings.Contains(plan, "**new**") {
		t.Errorf("%s never marks a path as **new**: the paths that do not exist yet have to be distinguishable from the ones that do", coordinatorPlan)
	}
	// D196 was answered (B), so the plan builds the coordinator's OWN resident
	// service, and no sentence of it may still say the coordinator is a role the
	// central orchestrator's service plays. That reading is what the plan was
	// written to before 2026-10-21, it is the reading every part after P6 was
	// written against, and a plan carrying both is a plan whose P7 builds nothing
	// while its prose says somebody else does.
	if m := coordinatorPlanRolePhraseRE.FindAllString(plan, -1); len(m) > 0 {
		t.Errorf("%s still writes %v: D196 was answered (B) on 2026-10-21, so the coordinator gets its own resident service and no part of this plan may describe it as a role another service plays", coordinatorPlan, m)
	}
	for _, want := range []string{coordinatorPlanOwnService, coordinatorPlanSharedAdmission} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not carry %q: the plan has to say whose service the front door is, and that the allow-list admission is shared with the central orchestrator plan's own part", coordinatorPlan, want)
		}
	}
	// The plan cites by path and symbol, never by line number -- the brief it
	// was written under, and the reason its citations do not rot the way the
	// reconciliation's would if it used them. The reconciliation pins the
	// opposite for itself (its citations are anchored by content or checked by
	// line); here there is nothing to check, so the absence IS the assertion.
	if m := coordinatorLineCensusRE.FindAllString(plan, -1); len(m) > 0 {
		t.Errorf("%s attaches a line number to a name (%v): this plan cites by path and symbol, never by line", coordinatorPlan, m)
	}
}

// TestDocsCoordinatorPlanCitationsExist: every repository path the plan cites --
// file or directory -- resolves on disk, or is one the plan declares new and
// which therefore must not resolve yet.
//
// It is the reconciliation's citation check, on the plan, with the same
// independent census in front of it -- the census is what stops a regexp that
// has stopped matching the document's shapes from passing vacuously, which is
// the failure that hid the `path:line Symbol` gap next door. Three things are
// checked, each with its own census and its own exceptions: the files
// (coordinatorPlanNewPaths, and the one entry is the file the plan itself says
// does not exist), the symbols named beside a file by possessives
// (coordinatorCheckPlanSymbols), and the directories
// (coordinatorCheckPlanDirectories). The directory half exists because the
// other two are both written to find a file extension, and the plan's most
// repeated citation of all -- the package every part after P0 writes into -- is
// a directory: before it, `internal/coordinator/` was cited seven times, was
// not there, was declared new by P1's own Files line, and was read by no check
// at all.
func TestDocsCoordinatorPlanCitationsExist(t *testing.T) {
	plan := readDoc(t, coordinatorPlan)
	census := len(coordinatorPathCensusRE.FindAllString(plan, -1))
	matches := coordinatorCitationRE.FindAllStringSubmatch(plan, -1)
	if len(matches) < census {
		t.Fatalf("%s holds %d path-looking tokens and the citation check matched %d of them: the plan cites a shape coordinatorCitationRE no longer knows, so those citations are unchecked",
			coordinatorPlan, census, len(matches))
	}
	seen := map[string]bool{}
	used := map[string]bool{}
	for _, m := range matches {
		path := m[1]
		first := !seen[path]
		seen[path] = true
		if reason, ok := coordinatorPlanNewPaths[path]; ok {
			if first {
				used[path] = true
				if _, err := os.Stat("../../" + path); err == nil {
					t.Errorf("%s cites %q as new, but it exists now: the part that creates it has landed, so the entry in coordinatorPlanNewPaths must go and the path must start being checked like any other. It was excused for: %s",
						coordinatorPlan, path, reason)
				}
			}
			continue
		}
		if _, err := os.Stat("../../" + path); err != nil {
			if first {
				t.Errorf("%s cites %q, which does not exist: a plan whose citations have moved is a plan nobody can check (%v)", coordinatorPlan, path, err)
			}
		}
	}
	for path := range coordinatorPlanNewPaths {
		if !used[path] {
			t.Errorf("%s no longer cites %q, but coordinatorPlanNewPaths still excuses it: delete the entry, or the next citation of that name is unchecked", coordinatorPlan, path)
		}
	}
	symbols := coordinatorCheckPlanSymbols(t, plan)
	dirs := coordinatorCheckPlanDirectories(t, plan)
	t.Logf("%s: %d citation occurrences over %d distinct paths, against a census of %d path-looking tokens in the document; %d `path`'s `symbol` pairings checked against a census of %d possessives after a path token; %d directory citation occurrences over %d distinct directories, against a census of %d directory-shaped tokens",
		coordinatorPlan, len(matches), len(seen), census, symbols, len(coordinatorPlanPossessiveCensusRE.FindAllString(plan, -1)),
		len(coordinatorPlanDirectoryRE.FindAllString(plan, -1)), dirs, len(coordinatorPlanDirCensusRE.FindAllString(plan, -1)))
}

// coordinatorCheckPlanDirectories holds every directory-shaped token the plan
// writes to one of three things, and returns how many distinct ones it read: a
// directory in this tree; a directory the plan declares new, which must NOT be
// there yet; or one of the audited non-paths, each of which names something
// else. A token that is none of the three is a citation of a path that does not
// exist, which is what the whole check is for.
//
// It is deliberately a census rather than a skip: the token count is logged
// beside it, and both excuse maps fail when an entry goes unused, so an entry
// that has outlived the sentence it was written for is deleted by the next
// person to run the package rather than left excusing nothing.
func coordinatorCheckPlanDirectories(t *testing.T, plan string) int {
	t.Helper()
	// The census first, and independently: every token in the document that ends
	// in a slash is either one coordinatorPlanDirectoryRE matches, or one of the
	// audited non-citations. A token that is neither is a directory citation in
	// a shape no check reads, which is the failure the two regexps being written
	// differently is what makes visible.
	unread := map[string]bool{}
	for _, token := range coordinatorPlanDirCensusRE.FindAllStringSubmatch(plan, -1) {
		if coordinatorPlanDirectoryRE.MatchString(token[0]) {
			continue
		}
		if slices.Contains(coordinatorPlanDirCensusExceptions, token[1]) {
			continue
		}
		unread[token[0]] = true
	}
	shapes := make([]string, 0, len(unread))
	for token := range unread {
		shapes = append(shapes, token)
	}
	sort.Strings(shapes)
	for _, token := range shapes {
		t.Errorf("%s holds %s, a token that ends in a slash and is matched by no directory check: the document has grown a shape the checks do not know, so that citation is checked by nothing", coordinatorPlan, token)
	}
	for _, want := range coordinatorPlanDirCensusExceptions {
		if !strings.Contains(plan, "`"+want+"`") {
			t.Errorf("%s no longer writes `%s`, which coordinatorPlanDirCensusExceptions excuses from the census: delete the entry, or a directory-shaped token goes unread", coordinatorPlan, want)
		}
	}
	seen := map[string]bool{}
	usedNew := map[string]bool{}
	usedNon := map[string]bool{}
	for _, m := range coordinatorPlanDirectoryRE.FindAllStringSubmatch(plan, -1) {
		dir := m[1]
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if _, ok := coordinatorPlanNonPaths[dir]; ok {
			usedNon[dir] = true
			continue
		}
		if reason, ok := coordinatorPlanNewDirs[dir]; ok {
			usedNew[dir] = true
			if _, err := os.Stat("../../" + dir); err == nil {
				t.Errorf("%s cites the directory %q as new, but it exists now: the part that creates it has landed, so the entry in coordinatorPlanNewDirs must go and the directory must start being checked like any other. It was excused for: %s",
					coordinatorPlan, dir, reason)
			}
			continue
		}
		fi, err := os.Stat("../../" + dir)
		if err != nil {
			t.Errorf("%s cites the directory %q, which does not exist: a plan whose citations have moved is a plan nobody can check (%v)", coordinatorPlan, dir, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("%s cites %q as a directory, and it is a file", coordinatorPlan, dir)
		}
	}
	for dir := range coordinatorPlanNewDirs {
		if !usedNew[dir] {
			t.Errorf("%s no longer cites the new directory %q, but coordinatorPlanNewDirs still excuses it: delete the entry, or the next citation of that name is unchecked", coordinatorPlan, dir)
		}
	}
	for dir := range coordinatorPlanNonPaths {
		if !usedNon[dir] {
			t.Errorf("%s no longer writes %q, which coordinatorPlanNonPaths excuses from the directory check: delete the entry, or a directory-shaped token goes unread", coordinatorPlan, dir)
		}
	}
	return len(seen)
}

// coordinatorPlanPossessiveCensusRE counts, independently of the pairing check
// below, how many times the plan attaches a possessive to a backticked path --
// `internal/rtdb/client.go`'s ... -- whatever follows it. It is deliberately
// dumber than the pairing regexp, and it is the floor that check is measured
// against, for the reason the path census exists: a regexp that has stopped
// matching a shape the prose uses finds fewer citations and says nothing, and
// the two shapes the plan uses for a symbol are one line apart -- `path`'s
// `symbol` on one line, and the same pair wrapped between them.
var coordinatorPlanPossessiveCensusRE = regexp.MustCompile("`[^`\n]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf)\\b[^`\n]*`'s")

// coordinatorPlanSymbolRE finds the pairing the plan writes for almost every
// symbol it names: a backticked path, a possessive, and the symbol in backticks.
// The symbol is captured as "anything but a backtick", newlines included,
// because one of the plan's pairings wraps inside the token
// (`google_cloud_run_v2_job_iam_member` and `"launcher"`); the check collapses
// the whitespace before looking for it.
var coordinatorPlanSymbolRE = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./*-]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf))`'s\\s+`([^`]+)`")

// coordinatorPlanProsePossessives are the plan's possessive phrases that name no
// symbol: a path followed by `'s` and ordinary prose. Each is an audited case
// and the check fails if one goes unused, because an entry here that outlives
// its sentence is a pairing nobody is checking -- the same rule the citation
// exception maps follow.
var coordinatorPlanProsePossessives = []string{
	"`internal/initflow/stage.go`'s list",
}

// coordinatorCheckPlanSymbols holds every `path`'s `symbol` pairing in the plan
// to the file it names, and returns how many it checked.
//
// This is the half of "cite by path and symbol" that a path check cannot see: a
// path that still resolves while the symbol behind it has been renamed is a
// citation that reads as checked and is not. It is deliberately narrow. Pairing
// an identifier with a citation in general needs a notion of scope the prose
// does not carry -- a sentence may name three files and one symbol, or a symbol
// belonging to a different citation in the same paragraph -- which is why the
// reconciliation's citation check records that hole rather than closing it with
// something fuzzy. The possessive form is the one shape where the plan's own
// grammar makes the pairing unambiguous, and it is the shape the plan uses for
// nearly every symbol it names, so it is the shape that is checked.
func coordinatorCheckPlanSymbols(t *testing.T, plan string) int {
	t.Helper()
	census := len(coordinatorPlanPossessiveCensusRE.FindAllString(plan, -1))
	matches := coordinatorPlanSymbolRE.FindAllStringSubmatch(plan, -1)
	prose := 0
	for _, want := range coordinatorPlanProsePossessives {
		if strings.Contains(plan, want) {
			prose++
			continue
		}
		t.Errorf("%s no longer writes %q, which coordinatorPlanProsePossessives excuses from the symbol check: delete the entry, or a pairing goes unchecked", coordinatorPlan, want)
	}
	if census != len(matches)+prose {
		t.Errorf("%s attaches a possessive to a path %d times, and this check pairs %d of them with a symbol (%d are the audited prose ones): a possessive the pairing regexp no longer matches is a symbol nothing checks, which is the failure the two shapes being one line apart makes easy",
			coordinatorPlan, census, len(matches), prose)
	}
	for _, m := range matches {
		path, symbol := m[1], strings.Join(strings.Fields(m[2]), " ")
		if _, ok := coordinatorPlanNewPaths[path]; ok {
			continue
		}
		src, err := os.ReadFile("../../" + path)
		if err != nil {
			t.Errorf("%s pairs %q with %q and the file cannot be read: %v", coordinatorPlan, path, symbol, err)
			continue
		}
		text := string(src)
		if strings.Contains(text, symbol) {
			continue
		}
		// A Terraform resource is written `resource "type" "name"`, and the plan
		// spells the same resource two ways -- `type.name` and `type "name"` --
		// neither of which is a literal the file carries. So a two-part pairing
		// is checked as its two halves, and only when the literal form is absent.
		if head, tail, ok := strings.Cut(symbol, "."); ok && !strings.ContainsAny(tail, " ") &&
			strings.Contains(text, head) && strings.Contains(text, `"`+tail+`"`) {
			continue
		}
		if head, tail, ok := strings.Cut(symbol, " "); ok &&
			strings.Contains(text, head) && strings.Contains(text, tail) {
			continue
		}
		t.Errorf("%s pairs %q with %q, and %s carries no such symbol: a path that still resolves while the symbol behind it has moved is a citation that reads as checked and is not",
			coordinatorPlan, path, symbol, path)
	}
	return len(matches)
}
