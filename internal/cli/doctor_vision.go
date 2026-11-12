package cli

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The vision check in force, as doctor says it (V6, D2011): the coder model's
// image rate, whether this repository's own file sets agent.models.vision, and
// — where it sets neither and the repository tracks a picture — the finding
// that the next stage to read one dies at the gateway.
//
// The three facts are one check and not three because they are one question: a
// coder whose price row carries an image rate reads a picture itself and the
// slot is beside the point (D2016), a coder that cannot read one is covered by
// the slot, and a coder that has neither is a run that pays for a stage's work
// and throws it away (the issue's own complaint, forward.go's refusal). Only
// the last state is a warning, and only where the repository tracks an image
// file: most repositories track a logo or a favicon, and a check that warned on
// one would warn on nearly every screen (D2005's own rule for the run's half).
//
// What a model can read is its price row's image rate and nothing else
// (D2000), which is why this check reads the price table rather than the
// config alone, and why a model priced by the owner's model_prices — the shape
// with no image rate at all — is a coder this check has something to say about.
const doctorVisionID = "vision-coder"

// doctorVisionFilesLimit bounds the image files the warning names: the finding
// is a warning to act on and not a listing, and a repository that tracks
// thousands of pictures needs no longer one.
const doctorVisionFilesLimit = 5

// doctorVisionChecks is the check doctor runs in a checkout: the coder's image
// rate, the vision slot, and the base branch's image files.
//
// It reads the base branch's tree and never the working copy, for the reason
// the security sweep does (D1518): the file a run resolves is the one at the
// base, and a working copy is what a change under review can edit. The tree is
// listed only in the one state where the answer decides the check's severity —
// a coder that can see, or a slot that is set, needs no listing — so the
// common configurations cost one price-table read and no git call at all.
//
// A tree that cannot be listed says nothing rather than warning: no default
// branch, or a git read that failed, is a fact nobody can check, and a finding
// nobody can check is worse than none (doctorSecurityListChecks' own rule). So
// does a price table that will not build: the catalog that will not load is
// `provider-price-catalog`'s line and the model_prices entry that will not
// compile is `fugaro validate`'s, and a second finding for one fault is the
// duplication the classes exist to keep off the screen.
func doctorVisionChecks(ctx context.Context, root string, lc *localcfg.Config, rf *resolvedFile, repo string) []doctorCheck {
	if rf == nil || rf.Cfg == nil {
		return nil
	}
	// No model pinned is Claude Code's own default, an Anthropic model with an
	// image rate in every configuration this program ships — the same answer
	// the run's own check gives that state (checkImages).
	coder := rf.Cfg.Agent.ModelFor(config.RoleCoder)
	if coder == "" {
		return nil
	}
	table, _, err := priceTable(lc, budgetNow())
	if err != nil {
		return nil
	}
	vision := rf.Cfg.Agent.VisionModel()
	if m, ok := table.Lookup(coder); ok && m.ImageTokens > 0 {
		return []doctorCheck{doctorVisionReads(coder, vision)}
	}
	if vision != "" {
		return []doctorCheck{doctorVisionSlotCovers(coder, vision)}
	}
	ref, files, ok := doctorVisionImageFiles(ctx, root)
	if !ok {
		return nil
	}
	if len(files) == 0 {
		return []doctorCheck{doctorVisionNoImages(coder)}
	}
	return []doctorCheck{doctorVisionFinding(coder, ref, files, repo)}
}

// doctorVisionSlot is the slot as a sentence names it: the key, and the model
// it holds or that it holds none. The empty slot is said in words because it is
// a state and not a missing member — the slot never falls back to agent.model
// (D2003), so a reader who took an empty cell for an inherited model would take
// the one pin with no default for a pin that has one.
func doctorVisionSlot(vision string) string {
	if vision == "" {
		return "agent.models.vision is unset"
	}
	return "agent.models.vision is " + pluginwire.Printable(vision)
}

// doctorVisionReads is the state nothing is wrong in: the coder's own price row
// carries an image rate, so a stage reads a picture itself and keeps its own
// model (D2016).
func doctorVisionReads(coder, vision string) doctorCheck {
	return doctorCheck{
		ID:       doctorVisionID,
		OK:       true,
		Severity: "info",
		Problem: fmt.Sprintf("the coder model %s is priced with an image rate, so a stage of this repository reads a picture itself; %s",
			pluginwire.Printable(coder), doctorVisionSlot(vision)),
		Fix: "nothing to do: a coder that can read an image keeps its own model, and the vision slot is for a stage whose own model cannot read one",
	}
}

// doctorVisionSlotCovers is the covered state: a coder priced without an image
// rate, with a model named for the stages that read one.
func doctorVisionSlotCovers(coder, vision string) doctorCheck {
	return doctorCheck{
		ID:       doctorVisionID,
		OK:       true,
		Severity: "info",
		Problem: fmt.Sprintf("the coder model %s is priced without an image rate, and %s, which a stage that reads a picture runs instead",
			pluginwire.Printable(coder), doctorVisionSlot(vision)),
		Fix: "nothing to do: the vision slot is set, and a model it names is priced through the same table as every other pin",
	}
}

// doctorVisionNoImages is the state that is safe today and only today: a coder
// that cannot read an image and no slot, in a repository whose base branch
// tracks none. It is a measurement rather than a warning — nothing is at risk
// — and it is said at all because the state one image file away from the
// finding is worth a reader knowing they are in.
func doctorVisionNoImages(coder string) doctorCheck {
	return doctorCheck{
		ID:       doctorVisionID,
		OK:       true,
		Severity: "info",
		Problem: fmt.Sprintf("the coder model %s is priced without an image rate and agent.models.vision is unset, and this repository tracks no image file at its base branch",
			pluginwire.Printable(coder)),
		Fix: "nothing to do yet: set agent.models.vision before the base branch tracks, or a task names, an image file",
	}
}

// doctorVisionFinding is the state D2011's warning is for: a coder that cannot
// read an image, no slot, and a repository that tracks one. The stage pays for
// its work and the gateway refuses the request that carries the picture, which
// is the failure the whole work exists to prevent. The slot is said through
// doctorVisionSlot's empty answer rather than by this function's own words, so
// the state reads the same wherever doctor names it.
func doctorVisionFinding(coder, ref string, files []string, repo string) doctorCheck {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = pluginwire.Printable(f)
	}
	return doctorCheck{
		ID:       doctorVisionID,
		Severity: "warning",
		Problem: fmt.Sprintf("the coder model %s is priced without an image rate, %s, and %s tracks %s: a stage that reads one is refused by the gateway mid-run, after its work is paid for",
			pluginwire.Printable(coder), doctorVisionSlot(""), pluginwire.Printable(ref), strings.Join(names, ", ")),
		Fix: fmt.Sprintf("set agent.models.vision to a model that can read images (%s models use %s vision <id>), and price that model's images with model_prices' image_tokens (0 refuses an image)",
			selfCommand(), pluginwire.Printable(repo)),
	}
}

// doctorVisionImageFiles is the base branch's image files, at most
// doctorVisionFilesLimit of them: the kinds agent.ImageBlockExtensions holds,
// which is the one list of them this program has and the list the gateway's
// refusal is about (a PDF is served whatever the model's image rate, so it is
// not one of them).
//
// The kind is matched without regard to case: git reports the tree's own
// spelling, and a picture of any case is one a stage that reads it cannot send
// — the run's own scan is case-sensitive because git's pathspecs are, and a
// warning that missed LOGO.PNG would be the silent pass this check exists to
// avoid.
func doctorVisionImageFiles(ctx context.Context, root string) (ref string, files []string, ok bool) {
	ref = defaultBranchRef(ctx, root)
	if ref == "" {
		return "", nil, false
	}
	paths, ok := doctorSecurityBaseTree(ctx, root, ref)
	if !ok {
		return "", nil, false
	}
	exts := agent.ImageBlockExtensions()
	for _, p := range paths {
		if !slices.Contains(exts, strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))) {
			continue
		}
		if files = append(files, p); len(files) == doctorVisionFilesLimit {
			break
		}
	}
	return ref, files, true
}
