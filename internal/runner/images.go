package runner

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
)

// visionPinPath is where a problem with the vision slot points the owner: the
// fourth of internal/config's pinPaths, restated here for the reason
// coderPinPath restates the first. The slot is the one pin a run may leave
// empty (config's own pins), and checkImages below is the check that makes an
// empty one matter.
const visionPinPath = "agent.models.vision"

// imageScanLimit bounds the files the repository scan reports. The warning
// names the first few of them, and a repository that tracks thousands of
// pictures needs no longer list; the listing itself is bounded by the git call
// (imageScanArgs), which never reads the tree's other files at all.
const imageScanLimit = 10

// imageNamesInTask are the words of text that name an image file, in the order
// they appear and without repeats: a name whose last extension is one of the
// kinds agent.ImageReadExtensions holds (docs/shot.png), and the kind's own
// word (a task that says the golden is a PNG). Both are names, and a task that
// only mentions a picture is deliberately enough to refuse on (V5, D2005): the
// check cannot tell a task that reads the file from one that talks about it,
// and a task refused for the mention costs the owner one config line, while
// the run that reads a picture nobody named dies at implement — which is the
// failure the check exists to prevent.
func imageNamesInTask(text string) []string {
	exts := agent.ImageReadExtensions()
	var out []string
	seen := map[string]bool{}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !imageNameRune(r) }) {
		if !namesAnImage(word, exts) || seen[strings.ToLower(word)] {
			continue
		}
		seen[strings.ToLower(word)] = true
		out = append(out, word)
	}
	return out
}

// imageNameRune reports whether r can be part of a file name or a path: the
// split keeps those and cuts everything else — the prose's spaces, its
// punctuation, the quotes around a name — away from the name.
func imageNameRune(r rune) bool {
	return r == '.' || r == '-' || r == '_' || r == '/' || r == '+' || r == '~' ||
		unicode.IsLetter(r) || unicode.IsDigit(r)
}

// namesAnImage reports whether word is one of the kinds by itself ("png", the
// extension an owner writes when the task says what the file is) or names a
// file of one of them ("docs/shot.png", the extension after the last dot).
func namesAnImage(word string, exts []string) bool {
	w := strings.ToLower(strings.Trim(word, "._-+~/"))
	if slices.Contains(exts, w) {
		return true
	}
	i := strings.LastIndexByte(w, '.')
	return i >= 0 && slices.Contains(exts, w[i+1:])
}

// imageScanArgs is the one git command the repository scan runs: the files of
// HEAD's tree whose name ends in one of the image kinds. The pathspecs are what
// bounds it — git matches them against the tree's paths and reports the
// matches, so a checkout of many files is never listed whole — and they are why
// the scan is not the ls-tree the same question suggests: ls-tree takes no
// wildcard, so a pattern there is a path nothing has, and the question would
// mean reading the whole tree and filtering it here. A name of another case
// (LOGO.PNG) is not matched: git compares paths case-sensitively, and the
// warning this feeds is a log line about the repository, not a refusal.
func imageScanArgs() []string {
	args := []string{"ls-files", "-z", "--with-tree=HEAD", "--end-of-options", "--"}
	for _, ext := range agent.ImageReadExtensions() {
		args = append(args, "*."+ext)
	}
	return args
}

// imageFilesInTree lists the image files HEAD's tree carries, at most
// imageScanLimit of them. ok is false when the tree could not be listed, which
// is a scan that says nothing rather than one that refuses a run for a git
// failure the run's own stages report themselves.
//
// HEAD, and not the base branch by name: at bootstrap it is the commit the run
// was cut from — the base branch's tip for a first run, the branch a follow-up
// continues — and both are the tree the run's stages read a picture out of.
func imageFilesInTree(ctx context.Context, repo *gitops.Repo) ([]string, bool) {
	if repo == nil {
		return nil, false
	}
	out, err := sessionGit(ctx, repo, imageScanArgs()...)
	if err != nil {
		return nil, false
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || slices.Contains(files, p) {
			continue
		}
		if files = append(files, p); len(files) == imageScanLimit {
			break
		}
	}
	return files, true
}

// checkImages is bootstrap's image check (V5, D2005, D2006): a coder whose
// price row carries no image rate, with no agent.models.vision to run in its
// place, is a run that dies at the first stage to read a picture — Claude Code
// Reads it, puts it in the next request as an image block, and the gateway
// refuses the whole call (forward.go's own refusal) — so the stage's work is
// paid for and thrown away, which is the issue's own complaint.
//
// A task whose text names an image file is therefore refused here, before any
// stage spends, naming the slot and the price member that fix it. The problem
// points at the coder pin, which is the pin that names the model nothing can
// read, and which for a challenger arm is the arm's own model — so bootstrap
// reads this refusal as that arm's (modelRefusal, D556) rather than as the
// champion's, exactly as it reads a pin the arm's model fails.
//
// A repository that tracks an image file is the same state and cannot refuse:
// most repositories track a logo or a favicon, and a check that refused on one
// would refuse most runs (D2005). It warns in the job log, where the
// bootstrap's model warnings already go, so an owner who meets the gateway's
// refusal at implement can see that the run knew.
//
// It is asked only where the gateway carries the run's calls, and only for a
// coder the run pins: with no gateway there is no image refusal to avoid, and
// a stage pinned to no model runs Claude Code's own default, an Anthropic
// model with an image rate in every configuration this program ships
// (denyBinaryReads' own two rules).
func (r *run) checkImages(ctx context.Context) error {
	coder := r.cfg.Agent.ModelFor(config.RoleCoder)
	if coder == "" || r.cfg.Agent.VisionModel() != "" || r.modelTakesImages(coder) {
		return nil
	}
	if names := imageNamesInTask(r.spec.Task); len(names) > 0 {
		return refusal("the coder can't read an image the task names", []config.Problem{{
			Path: coderPinPath,
			Message: fmt.Sprintf("%s is priced without an image rate, and the task names %s: set %s to a model that can read images, and price that model's images with model_prices' image_tokens (0 refuses one)",
				coder, strings.Join(names, ", "), visionPinPath),
		}})
	}
	files, ok := imageFilesInTree(ctx, r.repo)
	if !ok || len(files) == 0 {
		return nil
	}
	r.d.Log.Warn("the repository tracks image files and the coder can't read them: set "+visionPinPath+" to a model that can read images and price that model's images with model_prices' image_tokens",
		"code", "image_files", "files", strings.Join(files, ", "))
	return nil
}
