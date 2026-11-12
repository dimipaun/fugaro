// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/streams"
)

// This file is `fugaro coordinator roadmap publish` and `unpublish`: how a plan
// document gets into the project's roadmap record from a checkout (P9c,
// D2111).
//
// **The push model, and no repository.** Nothing here reads a repository the
// person is not standing in, and nothing grants the installation a token to
// read one: the person runs the command in whatever checkout holds the plan
// they mean. That is D2111's answer — the coordinator never learns where a
// repository's plans are — and it is why a repository that never runs this
// command is a repository the record says nothing about rather than one that is
// out of date.
//
// **What leaves the checkout is the artifact, trimmed.** The graph is read
// through the confined walk plan status uses (GV1.6's rule ii: no symlink
// anywhere on the path to the file, no non-regular file, the 1 MiB per-artifact
// bound), parsed by internal/plan's reader — an artifact this build refuses is
// refused whole, never half-published — and trimmed to P9a's shape: a task's
// id, title, kind, size and target version, the plan's own edges, and the
// provenance below. A node's footprint, its run, the decoupling record, the
// change requests and the contract stubs stay in the checkout, which is where
// the plan's reviewer reads them.
//
// **The provenance is recorded and never read back.** Every published document
// carries the origin repository this checkout names, the artifact's path in it,
// the commit HEAD points at and the graph schema the artifact was written in
// (D2111). Nothing in this build reads it back: it is what makes a published
// document traceable to the tree it came from, and the one reader that has it
// is the record's own D2123 refusal, which compares the repository a publish
// names with the one the stored document names. The working tree has to be
// clean at the artifact too — what is published is what is committed (D30's
// "clobber uncommitted work", turned around), so a person who edited the graph
// and has not committed it is told that rather than having the draft published
// under a commit it is not in.
//
// **The commands hold no database writer**, exactly as the rest of the group
// (D2118, D922/H3): every write is one `tools/call` to the front door's own
// tool, and the process that changes the record is the apply job. This file
// names no store, opens no client, and imports nothing of the record's.
//
// **The checkout is read before the front door is opened.** Every refusal here
// that a person can fix without the installation — no such artifact, a symlink
// out of the checkout, a refused graph, an uncommitted file, no origin remote —
// is decided first, so a typo costs no Terraform run and no token exchange, and
// a publish that could never have been built is never queued.

// coordinatorPublishDocVersion is the version of the document `publish` and
// `unpublish` print with --json (the console reads it, so a field never changes
// meaning under this number: a later shape is version 2).
const coordinatorPublishDocVersion = 1

// coordinatorPublishMaxArtifacts bounds one `--all` read, the way plan
// status's own read is bounded: a checkout carrying more graph artifacts than
// this is one whose docs/ is not a directory of plans, and reading it whole
// costs the record a document per file. Sixty-four is the cap GV1.6 fixes, and
// the two readers hold the same number because they read the same tree.
const coordinatorPublishMaxArtifacts = 64

// coordinatorPublishReleaseState is the state a stream's current release is
// imported in: the version the catalogue says a stream is at is the release it
// has shipped. Nothing here writes an open one — a release still open is the
// record's to change, and this command has no view of it.
const coordinatorPublishReleaseState = "finished"

// The statuses this command's document carries. The four the front door's
// outcome uses are mcpauth's; `skipped` is this file's own, and it means the
// record refused a release before queueing it — which for an import of a
// catalogue is the ordinary case (the record already holds that release) and
// not a request anybody is waiting on.
const coordinatorPublishSkipped = "skipped"

// coordinatorPublishResult is one artifact's or one release's line: what was
// sent and what the record did with it.
type coordinatorPublishResult struct {
	// Plan is the plan id the document was recorded under, which is the name
	// `unpublish` takes back.
	Plan string `json:"plan,omitempty"`
	// Artifact is the graph artifact as it sits in the checkout, for a publish.
	Artifact string `json:"artifact,omitempty"`
	// Release is the release an import wrote or skipped.
	Release string `json:"release,omitempty"`
	// Request is the front door's id for the queued request, when it queued
	// one: it is how a write that is still pending is read back later.
	Request string `json:"request,omitempty"`
	// Status is applied, refused, failed, pending, or skipped.
	Status string `json:"status"`
	// Reason is why, when the status is not applied.
	Reason string `json:"reason,omitempty"`
	// Current is the version the record holds now, beside a refusal that is a
	// stale `expect`.
	Current string `json:"current,omitempty"`
}

// coordinatorPublishDoc is what the two commands print with --json.
type coordinatorPublishDoc struct {
	Version int `json:"version"`
	// Repo and Commit are the provenance every plan document in this run
	// carries, printed once rather than beside each one.
	Repo     string                     `json:"repo,omitempty"`
	Commit   string                     `json:"commit,omitempty"`
	Plans    []coordinatorPublishResult `json:"plans"`
	Releases []coordinatorPublishResult `json:"releases,omitempty"`
}

// coordinatorPublishPlan is P9a's plan document as this command puts it on the
// wire.
//
// It is a copy of the record's own shape and not a reading of it, the way
// coordinatorOutcomeDoc is a copy of the front door's: this file may not import
// the record's package (TestRoadmapNeverWritesTheDatabaseItself), and a command
// that cannot reach a writer is the whole point of the route. The cost is a
// shape two packages spell, and it is paid where it is cheap — the tool's
// decoder is closed (`additionalProperties: false`, and the record's own reader
// refuses an unknown field), so a misspelt name is a refusal the first time
// anybody publishes, and TestPublishSendsTheTrimmedArtifactAndItsProvenance
// decodes what was sent into the record's own type to catch the same drift
// without a run.
//
// `schema` is deliberately absent: the shape version is the record's own to set
// (P9b sets it, and the door sets it before it queues), and a number written
// here would be a second place the record's version is decided.
type coordinatorPublishPlan struct {
	Plan   string                   `json:"plan"`
	Stream string                   `json:"stream"`
	Source coordinatorPublishOrigin `json:"source"`
	Tasks  []coordinatorPublishTask `json:"tasks"`
	Edges  []coordinatorPublishEdge `json:"edges"`
}

// coordinatorPublishOrigin is where a plan document was read from: the origin
// repository this checkout names, the artifact's path in it, the commit HEAD
// points at and the graph schema the artifact was written in (D2111).
type coordinatorPublishOrigin struct {
	Repo        string `json:"repo"`
	Path        string `json:"path"`
	Commit      string `json:"commit"`
	GraphSchema int    `json:"graph_schema"`
}

// coordinatorPublishTask is one task of a published plan. It is internal/plan's
// Node with the checkout's own fields dropped: the footprint and the run stay
// in the artifact, and a kind or a size a schema-3 graph carries crosses under
// the names it already has.
type coordinatorPublishTask struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Kind    string   `json:"kind,omitempty"`
	Size    *float64 `json:"size,omitempty"`
	Version *string  `json:"version"`
}

// coordinatorPublishEdge is one dependency inside one plan. The reason stays in
// the artifact, where the planner reviews it.
type coordinatorPublishEdge struct {
	Task  string `json:"task"`
	Needs string `json:"needs"`
}

type coordinatorPublishOptions struct {
	coordinatorRoadmapOptions
	all      bool
	expect   string
	releases string
}

func newCoordinatorRoadmapPublishCmd() *cobra.Command {
	var o coordinatorPublishOptions
	cmd := &cobra.Command{
		Use:   "publish [<plan>]",
		Short: "Put a checkout's plan graphs into the roadmap record",
		Long: "Publish the graph artifact beside a plan document, from any checkout\n" +
			"(roadmap_publish_plan, D2111).\n\n" +
			"Read the plan's graph where it sits — `docs/plans/x.md` is described by\n" +
			"`docs/plans/x.graph.json` — and send it, trimmed to what the record holds\n" +
			"and carrying where it came from: this repository, the artifact's path and\n" +
			"the commit HEAD names. A symlink anywhere on the way to the file, a file\n" +
			"that is not a regular file, and a graph this build refuses all end the run\n" +
			"before anything is sent; an artifact with uncommitted changes is refused\n" +
			"too, because what is published is what is committed.\n\n" +
			"`--expect` is the version of the plan document you last read, and the\n" +
			"record compares it with the version it holds: an empty `--expect` is the\n" +
			"claim that the record holds no such plan, so leave it out for a plan the\n" +
			"record does not hold and send what the record holds for one it does. A\n" +
			"publish that carries the version it read and sends the same artifact\n" +
			"writes nothing the second time — the record compares the document it holds\n" +
			"with the one it is sent — and one that carries a version the record has\n" +
			"moved past is refused, which is what keeps two people publishing one plan\n" +
			"from two checkouts from overwriting each other. This command holds no read\n" +
			"that would fetch the version for you: the refusal names it, and sending it\n" +
			"back is the next run.\n\n" +
			"`--all` publishes every graph artifact under the checkout's docs/, each its\n" +
			"own request. `--releases <file>` imports a release catalogue — this\n" +
			"repository's `docs/release-streams.yaml` is the one that exists — one\n" +
			"release per stream, named by the catalogue. It is a flag a person passes\n" +
			"and never a default: a repository with no such file publishes its plans\n" +
			"the same way.\n\n" +
			"Nothing is read from the checkout but the artifact, and nothing leaves it\n" +
			"but the trimmed document and its provenance.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCoordinatorPublish(cmd, &o, args)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.all, "all", false, "publish every graph artifact under the checkout's docs/")
	f.StringVar(&o.expect, "expect", "", "the version of the plan document you last read; leave it out for a plan the record does not hold")
	f.StringVar(&o.releases, "releases", "", "a release catalogue to import, such as docs/release-streams.yaml (never a default)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

type coordinatorUnpublishOptions struct {
	coordinatorRoadmapOptions
	expect string
}

func newCoordinatorRoadmapUnpublishCmd() *cobra.Command {
	var o coordinatorUnpublishOptions
	cmd := &cobra.Command{
		Use:   "unpublish <plan>",
		Short: "Remove a published plan from the record",
		Long: "Remove one plan document from the roadmap record (roadmap_unpublish_plan).\n\n" +
			"The plan is named by its id — the `plan` field of what `roadmap publish\n" +
			"--json` printed, which is the part of a task's qualified name before the\n" +
			"slash. The tasks it carried are left dangling in the assignments and edges\n" +
			"that still name them, and a reader shows them as dangling until a later\n" +
			"publish or an unneed removes them (D2115).\n\n" +
			"`--expect` is the version of the plan document you read before asking to\n" +
			"remove it. A plan the record does not hold is the state this asks for\n" +
			"already, so an unpublish of one that is gone is answered rather than\n" +
			"refused.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCoordinatorUnpublish(cmd, &o, args[0])
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.expect, "expect", "", "the version of the plan document you last read; leave it out for a plan the record does not hold")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

// runCoordinatorPublish is the whole of the command: what to send, read out of
// the checkout, and then one call per document.
func runCoordinatorPublish(cmd *cobra.Command, o *coordinatorPublishOptions, args []string) error {
	ctx := cmd.Context()
	doc := coordinatorPublishDoc{Version: coordinatorPublishDocVersion, Plans: []coordinatorPublishResult{}}
	switch {
	case o.all && len(args) > 0:
		return fcli.UserErr("--all publishes every artifact under %s/, so it takes no plan; name one or the other", plan.DocsDir)
	case o.all && o.expect != "":
		return fcli.UserErr("--expect is one document's version, and --all publishes many; publish them one at a time to say what you read")
	case o.expect != "" && o.releases != "":
		return fcli.UserErr("--expect is a plan document's version, and --releases writes releases; they are two different writes")
	case !o.all && len(args) == 0 && o.releases == "":
		return fcli.UserErr("nothing to publish: name a plan document, or --all for every artifact under %s/, or --releases <file> for a release catalogue", plan.DocsDir)
	}

	var artifacts []string
	switch {
	case o.all:
		found, err := coordinatorPublishArtifacts()
		if err != nil {
			return err
		}
		artifacts = found
	case len(args) == 1:
		rel, err := coordinatorArtifactRel(args[0])
		if err != nil {
			return err
		}
		artifacts = []string{rel}
	}
	if len(artifacts) == 0 && o.releases == "" {
		return fcli.UserErr("no graph artifact under %s/ in this checkout to publish", plan.DocsDir)
	}

	documents := make([]coordinatorPublishPlan, len(artifacts))
	plans := make([]coordinatorPublishResult, len(artifacts))
	ready := make([]bool, len(artifacts))
	anyReady := false
	// published is the plan id each artifact sent so far took, by path order.
	// A sweep has to hold it: two artifacts that derive one id are one document
	// in the record, so the second write would replace the first graph with
	// nothing anywhere saying so, and the record would hold one plan where the
	// checkout has two. plan status refuses to join them apart for the same
	// reason; here it is the record's own name that would be overwritten.
	published := make(map[string]string, len(artifacts))
	if len(artifacts) > 0 {
		repo, commit, err := coordinatorProvenance(ctx)
		if err != nil {
			return err
		}
		doc.Repo, doc.Commit = repo, commit
		for i, rel := range artifacts {
			// The placeholder is the refusal a read that fails leaves behind:
			// what is not sent is refused, and the sentence says by what.
			plans[i] = coordinatorPublishResult{Artifact: rel, Status: mcpauth.RoadmapRefused}
			trimmed, err := coordinatorPublishDocument(rel, repo, commit)
			if err == nil {
				err = coordinatorCheckCommitted(ctx, rel)
			}
			if err != nil {
				// A plan named by hand is refused whole, before anything is
				// sent — one plan is one document, and half of one is not a
				// plan. A sweep reports the artifact and carries on, because
				// one unreadable graph under docs/ is not a reason to publish
				// none of the others.
				if !o.all {
					return err
				}
				plans[i] = coordinatorResultOf(plans[i], err)
				continue
			}
			if other, taken := published[trimmed.Plan]; taken {
				// The first artifact in path order keeps the id and the second
				// is refused rather than sent: sending it would take the id from
				// the plan published a moment earlier, silently, and the person
				// would have one plan in the record and no sign that the other
				// graph went nowhere. Only an artifact that would be published
				// takes the id, so a first artifact refused above leaves it free.
				plans[i] = coordinatorResultOf(plans[i], fcli.UserErr(
					"%s and %s are both the plan %s; only the first is published",
					safetext.OneLine(other), safetext.OneLine(rel), safetext.OneLine(trimmed.Plan)))
				continue
			}
			published[trimmed.Plan] = rel
			documents[i], plans[i].Plan, ready[i] = trimmed, trimmed.Plan, true
			anyReady = true
		}
	}

	// The front door is opened once for the run, and only when there is
	// something to send: a sweep whose every artifact was refused is answered
	// without a token and without a Terraform run.
	var (
		door   *coordinatorDoor
		client *http.Client
	)
	if anyReady || o.releases != "" {
		var err error
		door, client, err = coordinatorOpenDoor(ctx, &o.cloud)
		if err != nil {
			return err
		}
	}
	for i := range artifacts {
		if !ready[i] {
			continue
		}
		plans[i], _ = coordinatorSendWrite(ctx, client, door, "roadmap_publish_plan", map[string]any{
			"plan": documents[i], "expect": o.expect,
		}, plans[i])
	}
	doc.Plans = append(doc.Plans, plans...)

	if o.releases != "" {
		releases, err := coordinatorPublishCatalogue(ctx, client, door, o.releases)
		if err != nil {
			return err
		}
		doc.Releases = releases
	}
	return coordinatorPublishReport(cmd, o.asJSON, doc)
}

// runCoordinatorUnpublish removes one plan document, by the id it was published
// under.
func runCoordinatorUnpublish(cmd *cobra.Command, o *coordinatorUnpublishOptions, id string) error {
	ctx := cmd.Context()
	if strings.TrimSpace(id) == "" {
		return fcli.UserErr("a plan id is needed: an unpublish removes the plan document this id names")
	}
	door, client, err := coordinatorOpenDoor(ctx, &o.cloud)
	if err != nil {
		return err
	}
	r, _ := coordinatorSendWrite(ctx, client, door, "roadmap_unpublish_plan", map[string]any{
		"plan": id, "expect": o.expect,
	}, coordinatorPublishResult{Plan: id, Status: mcpauth.RoadmapRefused})
	doc := coordinatorPublishDoc{Version: coordinatorPublishDocVersion, Plans: []coordinatorPublishResult{r}}
	return coordinatorPublishReport(cmd, o.asJSON, doc)
}

// coordinatorSendWrite queues one write and reads its outcome back, as the one
// result line the command reports for it.
//
// The statuses are the front door's own four, and the exit-code class of the
// failure is preserved rather than flattened: a refusal is the person's (exit
// 1, the record's sentence and the version it holds are beside it) and a fault
// of the installation is not (exit 2, nobody can act on it).
//
// The failure comes back beside the result because one caller reads a class of
// it: an import of a catalogue tells a refusal the tool answered — the record's
// own sentence about that release — from the door refusing the call, which is
// not about the release at all (coordinatorDoorRefusal).
func coordinatorSendWrite(ctx context.Context, client *http.Client, door *coordinatorDoor, tool string, args map[string]any, r coordinatorPublishResult) (coordinatorPublishResult, error) {
	answer, err := coordinatorCallTool(ctx, client, door, tool, args)
	if err != nil {
		return coordinatorResultOf(r, err), err
	}
	var queued struct {
		Version int    `json:"version"`
		Request string `json:"request"`
	}
	if err := json.Unmarshal(answer, &queued); err != nil {
		return coordinatorResultOf(r, fcli.RemoteErr(fmt.Errorf("the front door's answer to %s was not a queued request", tool))), nil
	}
	if err := coordinatorAnswerVersion("the answer to "+tool, queued.Version); err != nil {
		return coordinatorResultOf(r, err), nil
	}
	if queued.Request == "" {
		return coordinatorResultOf(r, fcli.RemoteErr(fmt.Errorf("the front door's answer to %s named no request id", tool))), nil
	}
	r.Request = queued.Request
	outcome, err := coordinatorAwaitOutcome(ctx, client, door, queued.Request)
	if err != nil {
		return coordinatorResultOf(r, err), nil
	}
	r.Status, r.Reason, r.Current = outcome.Status, outcome.Reason, outcome.Current
	return r, nil
}

// coordinatorResultOf is a failure as a result line, keeping whose it is: a
// refusal is exit 1 and a fault of the front door or of the record is exit 2.
func coordinatorResultOf(r coordinatorPublishResult, err error) coordinatorPublishResult {
	r.Reason = safetext.OneLine(err.Error())
	if fcli.ExitCode(err) == fcli.ExitRemoteError {
		r.Status = mcpauth.RoadmapFailed
		return r
	}
	r.Status = mcpauth.RoadmapRefused
	return r
}

// coordinatorPublishReport prints the run and answers the error the command
// exits with.
//
// The document is printed and the exit code is still the statuses': a console
// that asked for JSON asked for the data, not for a command that lies about
// what happened. A release the record refused is a skip rather than a failure —
// that is what an import of a catalogue does with a release that is already
// there — so only a refused or faulted plan, or a faulted release, decides the
// code, and the sentence names the first of them and how many others there
// were.
func coordinatorPublishReport(cmd *cobra.Command, asJSON bool, doc coordinatorPublishDoc) error {
	if asJSON {
		if err := writeCoordinatorJSON(cmd.OutOrStdout(), doc); err != nil {
			return err
		}
	} else {
		for _, r := range doc.Plans {
			fmt.Fprintln(cmd.OutOrStdout(), coordinatorPublishLine(r))
		}
		for _, r := range doc.Releases {
			fmt.Fprintln(cmd.OutOrStdout(), coordinatorPublishLine(r))
		}
	}
	total, refused, failed := 0, 0, 0
	var firstFailed, firstRefused coordinatorPublishResult
	for _, r := range append(append([]coordinatorPublishResult{}, doc.Plans...), doc.Releases...) {
		total++
		switch r.Status {
		case mcpauth.RoadmapFailed:
			failed++
			if firstFailed.Status == "" {
				firstFailed = r
			}
		case mcpauth.RoadmapRefused:
			refused++
			if firstRefused.Status == "" {
				firstRefused = r
			}
		}
	}
	switch {
	case failed > 0:
		return fcli.RemoteErr(errors.New(coordinatorPublishSentence(firstFailed, failed+refused, total)))
	case refused > 0:
		return fcli.UserErr("%s", coordinatorPublishSentence(firstRefused, refused, total))
	}
	return nil
}

// coordinatorPublishSentence is what the command exits with: the first thing
// that did not land, and how much of the run that was.
func coordinatorPublishSentence(r coordinatorPublishResult, failed, total int) string {
	s := coordinatorPublishLine(r)
	if failed > 1 {
		s = fmt.Sprintf("%s (and %d more)", s, failed-1)
	}
	return fmt.Sprintf("%s — %d of %d did not land", s, failed, total)
}

// coordinatorPublishLine is one result as one line a person reads. The path and
// the record's own words are rendered through safetext, as every field that was
// written by a file or a server is.
func coordinatorPublishLine(r coordinatorPublishResult) string {
	what := r.Release
	if what == "" {
		what = r.Artifact
	}
	if what == "" {
		what = r.Plan
	}
	s := safetext.OneLine(what) + ": " + r.Status
	if r.Request != "" {
		s += " (request " + safetext.Field(r.Request) + ")"
	}
	if r.Reason != "" {
		s += ": " + safetext.OneLine(r.Reason)
	}
	// The version the record holds is what a refusal that is a stale `expect`
	// is asking the person to read again, so it is printed the way the single
	// write's own sentence prints it.
	if r.Current != "" {
		s += fmt.Sprintf(" (the record now holds %q)", safetext.OneLine(r.Current))
	}
	return s
}

// coordinatorArtifactRel is the graph artifact a positional <plan> names: the
// document's graph beside it, or the artifact itself when that is what was
// passed. What comes back is the path the checkout will be read at, and a name
// outside the checkout's docs/ is refused here rather than turned into a read
// of somewhere else.
func coordinatorArtifactRel(name string) (string, error) {
	artifact := name
	if !strings.HasSuffix(artifact, plan.ArtifactSuffix) {
		artifact = plan.ArtifactPath(artifact)
	}
	if filepath.IsAbs(artifact) {
		return "", fcli.UserErr("%s is an absolute path; name the plan relative to the checkout, so that what is published says where in the repository it sits", safetext.OneLine(name))
	}
	rel := filepath.ToSlash(filepath.Clean(artifact))
	if !strings.HasPrefix(rel, plan.DocsDir+"/") {
		return "", fcli.UserErr("%s is not under %s/; a plan's graph is read where it sits in the checkout", safetext.OneLine(name), plan.DocsDir)
	}
	return rel, nil
}

// coordinatorConfinedArtifact refuses an artifact whose path leaves the
// checkout on the way to it.
//
// Every component from docs/ down is stat'ed without following anything, so a
// symlinked docs/, a symlinked directory on the way and a symlinked artifact are
// all refused rather than read through: a link is a way for the text that
// leaves the checkout to be text from somewhere else, and this command's whole
// job is to publish what the checkout holds. The file has to be a regular file
// within the read bound for the same reason opening anything else is refused —
// a fifo or a device is a read that may never end.
func coordinatorConfinedArtifact(rel string) error {
	parts := strings.Split(rel, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		info, err := os.Lstat(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fcli.UserErr("%s is not here; a plan's graph is written beside it as <plan>%s", safetext.OneLine(rel), plan.ArtifactSuffix)
			}
			return fcli.UserErr("%s could not be read: %v", safetext.OneLine(p), err)
		}
		last := i == len(parts)-1
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return fcli.UserErr("%s is a symlink; publish reads the graph artifacts where they sit and follows no link out of the checkout", safetext.OneLine(p))
		case !last && !info.IsDir():
			return fcli.UserErr("%s is not a directory, and %s is read through it", safetext.OneLine(p), safetext.OneLine(rel))
		case last && !info.Mode().IsRegular():
			return fcli.UserErr("%s is not a regular file; a plan's graph is a file in a checkout, and opening anything else is a read that may never end", safetext.OneLine(p))
		case last && info.Size() > plan.MaxArtifactBytes:
			return fcli.UserErr("%s is %d bytes, over the %d byte (1 MiB) bound this build reads", safetext.OneLine(p), info.Size(), plan.MaxArtifactBytes)
		}
	}
	return nil
}

// coordinatorPublishArtifacts lists the graph artifacts under the checkout's
// docs/, in path order, for `--all`.
//
// It is GV1.6's walk read again rather than shared: plan status's own is
// unexported in internal/cli, and an ee/ package cannot reach it. What the two
// have to agree about is the rule and not the sentences — no symlink anywhere
// under docs/, no non-regular file, and the cap above — and each names its own
// command in its refusals, which is why the copy is here and not a shared
// helper with a caller's name threaded through it.
func coordinatorPublishArtifacts() ([]string, error) {
	dir := plan.DocsDir
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fcli.UserErr("%s could not be read: %v", dir, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fcli.UserErr("%s is a symlink; publish reads the checkout's own docs directory and follows no link out of it", dir)
	case !info.IsDir():
		return nil, nil
	}

	var (
		out  []string
		fail error
	)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			fail = fcli.UserErr("%s is a symlink; publish reads the graph artifacts where they sit and follows no link", safetext.OneLine(filepath.ToSlash(p)))
			return filepath.SkipAll
		case d.IsDir(), !strings.HasSuffix(d.Name(), plan.ArtifactSuffix):
			return nil
		case !d.Type().IsRegular():
			fail = fcli.UserErr("%s is not a regular file; publish reads a checkout's graph artifacts, and opening anything else is a read that may never end", safetext.OneLine(filepath.ToSlash(p)))
			return filepath.SkipAll
		}
		out = append(out, filepath.ToSlash(p))
		if len(out) > coordinatorPublishMaxArtifacts {
			fail = fcli.UserErr("%s carries more than %d graph artifacts; publish reads a checkout's plans, and a directory of them this size is not one", dir, coordinatorPublishMaxArtifacts)
			return filepath.SkipAll
		}
		return nil
	})
	if fail != nil {
		return nil, fail
	}
	if err != nil {
		return nil, fcli.UserErr("%s could not be walked: %v", dir, err)
	}
	sort.Strings(out)
	return out, nil
}

// coordinatorPublishDocument reads one artifact and trims it to what the record
// holds, with the provenance it is published under.
func coordinatorPublishDocument(rel, repo, commit string) (coordinatorPublishPlan, error) {
	if err := coordinatorConfinedArtifact(rel); err != nil {
		return coordinatorPublishPlan{}, err
	}
	g, err := plan.ReadFile(rel)
	if err != nil {
		// internal/plan's refusal already names the artifact and what is wrong
		// with it; it is the person's, and it is one line.
		return coordinatorPublishPlan{}, fcli.UserErr("%s", safetext.OneLine(err.Error()))
	}
	return coordinatorTrimPlan(g, coordinatorPublishOrigin{
		Repo: repo, Path: rel, Commit: commit, GraphSchema: g.SchemaVersion,
	}), nil
}

// coordinatorTrimPlan is the whole of what a checkout contributes to the
// record: the plan's id, its stream, where it came from, its tasks and its own
// edges. Everything the artifact carries beyond that — a node's run and
// footprint, the decoupling record, the change requests, the stubs a contract
// leaves — stays where the plan's reviewer reads it.
func coordinatorTrimPlan(g plan.Document, source coordinatorPublishOrigin) coordinatorPublishPlan {
	tasks := make([]coordinatorPublishTask, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		tasks = append(tasks, coordinatorPublishTask{ID: n.ID, Title: n.Title, Kind: n.Kind, Size: n.Size, Version: n.Version})
	}
	edges := make([]coordinatorPublishEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		edges = append(edges, coordinatorPublishEdge{Task: e.Task, Needs: e.Needs})
	}
	stream := ""
	if g.Stream != nil {
		stream = *g.Stream
	}
	return coordinatorPublishPlan{
		Plan: plan.IDFromPath(source.Path), Stream: stream, Source: source, Tasks: tasks, Edges: edges,
	}
}

// coordinatorProvenance reads where the checkout says the artifact came from:
// the origin remote's repository and the commit HEAD names.
//
// A checkout whose origin is not owner/name has nothing publishable in it: the
// provenance is a required part of the document, and a value this build guessed
// would make D2123's one comparison wrong in exactly the case it exists for.
func coordinatorProvenance(ctx context.Context) (string, string, error) {
	origin, err := coordinatorGit(ctx, "remote", "get-url", "origin")
	if err != nil {
		return "", "", fcli.UserErr("this checkout has no origin remote to take the repository from: a published plan records where it came from, and %v", err)
	}
	repo, ok := coordinatorRepoFromOrigin(origin)
	if !ok {
		// The redaction is the core's own helper for this sentence
		// (internal/cli's `origin %s does not name owner/name`), and it is here
		// for the origins HTTPSOrigin hands back as they are: a remote of a
		// scheme it does not know is copied out with whatever userinfo a clone
		// left in it, and this line is read at a terminal.
		return "", "", fcli.UserErr("origin %s does not name owner/name, so a published plan could not say which repository it came from", safetext.OneLine(gcp.RedactURL(image.HTTPSOrigin(origin))))
	}
	commit, err := coordinatorGit(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fcli.UserErr("this checkout names no commit to publish under: %v", err)
	}
	return repo, commit, nil
}

// coordinatorRepoFromOrigin is internal/cli's own owner/name rule, read here
// because that one is unexported and this command may not import the core's
// command package for it: an ssh, scp-like or https origin becomes an https URL
// on the same host, and a nested path is not owner/name.
func coordinatorRepoFromOrigin(origin string) (string, bool) {
	u, err := url.Parse(image.HTTPSOrigin(origin))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	repo := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if _, _, ok := gitprov.SplitRepo(repo); !ok {
		return "", false
	}
	return repo, true
}

// coordinatorCheckCommitted refuses an artifact with changes that are not
// committed. What is published is what is committed, so the commit the document
// names is one that really holds the graph the document carries.
//
// `--ignored` is not decoration: a path a `.gitignore` matches is reported by
// nothing but that flag, and an ignored artifact is exactly the case this check
// exists for — it is in the tree, it is in no commit, and a publish that took
// the read as "committed" would send a document under a commit that does not
// hold it. A tracked artifact is reported the same way with the flag as
// without it, so nothing else changes.
func coordinatorCheckCommitted(ctx context.Context, rel string) error {
	out, err := coordinatorGit(ctx, "status", "--porcelain", "--ignored", "--", rel)
	if err != nil {
		return err
	}
	if out != "" {
		return fcli.UserErr("%s has changes that are not committed; publish what the commit holds, so commit it first", safetext.OneLine(rel))
	}
	return nil
}

// coordinatorPublishGitHardening is put before every git call here, as it is
// before every git this build spawns on a checkout that may be somebody else's
// (internal/cli's gitHardening and internal/srctree's list — both unexported,
// and an ee/ package cannot reach either, so the list is written again rather
// than shared).
//
// It is the whole of what stands between a read and a program the checkout
// chose. `.git/config` is repository content like any other file, and `git
// status` — the read before every publish — runs the command `core.fsmonitor`
// names, as the person, on a checkout they may have cloned from anywhere; the
// hook path and the external transports are the same shape of hazard. `-c`
// outranks the file, so none of them runs whatever the repository says.
var coordinatorPublishGitHardening = []string{
	"-c", "core.hooksPath=" + os.DevNull,
	"-c", "core.fsmonitor=false",
	"-c", "protocol.ext.allow=never",
}

// coordinatorGit runs one git read in the checkout the person is standing in.
//
// The prompt is off and the index is not locked, as it is for every git this
// build spawns: a credential prompt on a machine that has none is a hang rather
// than a refusal, and a read has no business taking the index's lock.
func coordinatorGit(ctx context.Context, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", append(append([]string{"-C", "."}, coordinatorPublishGitHardening...), args...)...)
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	c.WaitDelay = 5 * time.Second
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	if err := c.Run(); err != nil {
		said := safetext.OneLine(strings.TrimSpace(errOut.String()))
		if said == "" {
			said = err.Error()
		}
		return "", fcli.UserErr("git %s could not be read here: %s", strings.Join(args, " "), said)
	}
	return strings.TrimSpace(out.String()), nil
}

// coordinatorPublishCatalogue imports a release catalogue as one release per
// stream (D2120).
//
// It is a flag a person passes and never a default (D2113): a repository that
// carries no catalogue is a repository whose plans publish exactly the same
// way, and a command that looked for such a file would make the record depend
// on a convention no repository agreed to.
//
// Each stream is sent with the empty `expect`, which is the claim that the
// record holds nothing — and that is what makes the import idempotent: the
// front door refuses a release the record already holds, and a refusal of a
// write that queued is a skip rather than a failure, because "this release is
// already in the record" is what a second import of the same catalogue means.
// The refusal's own sentence is printed beside it, so a catalogue the record
// refuses for some other reason is still visible.
//
// Only a refusal the tool answered is a skip. A refusal the door answered
// instead — a credential it will not take (401, 403) or a call it will not read
// — says nothing about the release: no tool ran, and reading it as "already
// there" would print a whole catalogue as skipped and exit 0 under a token that
// published nothing at all. The group's exit codes are the same for a catalogue
// as for a plan, and a plan refused that way is exit 1.
func coordinatorPublishCatalogue(ctx context.Context, client *http.Client, door *coordinatorDoor, path string) ([]coordinatorPublishResult, error) {
	cat, err := streams.Read(path)
	if err != nil {
		return nil, fcli.UserErr("%s", safetext.OneLine(err.Error()))
	}
	out := make([]coordinatorPublishResult, 0, len(cat.Streams))
	for _, s := range cat.Streams {
		// A stream that has released nothing has no release to import; the
		// catalogue says so with 0.0.0, which is a claim about the stream and
		// not a version anybody shipped.
		if s.Current.IsZero() {
			continue
		}
		order := s.Current.Minor - streams.FirstVersion(s.Name).Minor
		if order < 0 || order >= len(s.Names) {
			// streams' own reader holds a stream's version to its name list, so
			// this cannot be reached through the file. It is here because the
			// index is the one arithmetic in this file, and a version the list
			// does not reach would be a panic rather than a refusal.
			continue
		}
		r, err := coordinatorSendWrite(ctx, client, door, "roadmap_release_set", map[string]any{
			"id": s.Current.String(), "stream": s.Name, "name": s.Names[order],
			"state": coordinatorPublishReleaseState, "order": order, "expect": "",
		}, coordinatorPublishResult{Release: s.Name + " " + s.Current.String(), Status: mcpauth.RoadmapRefused})
		// A refusal the door answered is not a refusal of this release: no tool
		// ran, and a token it will not take or a call it will not read would
		// otherwise print a whole catalogue as skipped and exit 0 under a
		// credential that published nothing.
		var byTheDoor coordinatorDoorRefusal
		if r.Status == mcpauth.RoadmapRefused && !errors.As(err, &byTheDoor) {
			r.Status = coordinatorPublishSkipped
		}
		out = append(out, r)
	}
	return out, nil
}
