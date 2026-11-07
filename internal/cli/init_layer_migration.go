package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/yamledit"
	"gopkg.in/yaml.v3"
)

// The repository-image removal (D1195) and a project layer a release before
// this one published: the object every repository of the project resolves
// against still holds `image.apt`, `rebuild.check` and the rest, and every
// reader refuses it by name (config.ParseProjectLayer, the scope walk) — so
// every run of every repository of that project fails, and `fugaro update`,
// the one command the upgrade is, has to be what puts the layer right.
//
// The migration is the removal applied to the layer: the keys go, everything
// else stays byte for byte (yamledit edits the text, so the owner's comments
// and order survive), the object is rewritten in place against the generation
// this run read (never "delete it and publish again", which would leave the
// project with no layer at all for as long as it takes), and the same strip is
// what this machine's local layer file gets, because a file the release that
// published it wrote holds the same keys.

// layerRemovedKey is one key the repository-image removal takes out of a
// project layer: the profile it was written in and its own path under it
// (image.apt, rebuild.check).
type layerRemovedKey struct {
	Profile string
	Key     string
}

// Path is where the key sat in the layer, spelled the way the layer's own
// problems spell it: a reader who meets the migration and one who meets
// `fugaro validate`'s refusal are looking at the same path.
func (k layerRemovedKey) Path() string { return "profiles." + k.Profile + "." + k.Key }

// Sentence is what the key is refused with when it is written today: the same
// words at both points, so the migration names the command that replaces what
// it took out (`fugaro image add`) as the refusal does.
func (k layerRemovedKey) Sentence() string {
	return config.RemovedImageKeyMessage("workflows.*." + k.Key)
}

// layerRemoval is the removal applied to one side of the project layer — the
// published object, or this machine's local layer file — as this run may write
// it.
type layerRemoval struct {
	// Data is the document without the removed keys, and Layer is that
	// document parsed: what the side is written from.
	Data  []byte
	Layer *config.ProjectLayer
	// Keys are the keys that went, in the order a reader meets them.
	Keys []layerRemovedKey
	// Profiles are the profiles the removal emptied out of the layer: every key
	// they held was one the removal took, and a mapping with no keys left has
	// no spelling yamledit writes (`{}` is a value this editor does not
	// compose), so the profile goes with them.
	Profiles []string
	// Problems is why this removal is not one this Fugaro may write: what is
	// left of the document is not a project layer it reads, so the caller
	// publishes nothing from it. Empty is the removal being writable.
	Problems []config.Problem
	// Refused is why the editor made no removal at all — a key written in a
	// flow mapping whose value is a collection has no edit that takes it out,
	// and yamledit splices nothing else — and "" when every edit landed. It is
	// not Problems: Problems is what the document a removal DID make says, and
	// this is the removal there is none of. Refused and Problems must both stay
	// apart from a nil result, which is a document with nothing to take out: a
	// caller that read either as nil would report a layer converged while the
	// document still holds keys every reader refuses.
	Refused string
}

// unwritable is why this run does not write this removal — the editor's own
// refusal, or the problems of the document the removal left — and false when it
// is one it may write.
func (r *layerRemoval) unwritable() bool { return r.Refused != "" || len(r.Problems) > 0 }

// text names what the removal takes out of one side, for a sentence.
func (r *layerRemoval) text() string {
	noun := "keys"
	if len(r.Keys) == 1 {
		noun = "key"
	}
	names := make([]string, 0, len(r.Keys))
	for _, k := range r.Keys {
		names = append(names, k.Path())
	}
	return fmt.Sprintf("%d %s the repository-image removal refuses (%s)", len(r.Keys), noun, strings.Join(names, ", "))
}

// layerProfiles is a project layer document read as what each profile of it
// says it writes, and as nothing else: config.ProjectLayer refuses the removed
// keys, so the migration cannot ask it which profiles are there to migrate.
// The profiles stay `any` because this read is made on documents a reader
// refuses for other reasons too — the migration judges what is left of them,
// and needs to answer for the keys it took rather than for a shape the parse
// already refuses.
type layerProfiles struct {
	Profiles map[string]any `yaml:"profiles"`
}

// layerRemovalEdit is one edit the removal makes: the path yamledit is asked
// to take out of the document, and the removed keys that go with it — the key
// itself, or every removed key of a block the edit takes whole.
type layerRemovalEdit struct {
	path []string
	keys []layerRemovedKey
}

// layerRemovalEdits is the edits the removal makes to one profile: for every
// removed key it writes, that key — or, when the removal takes every key the
// block around it holds, the block whole.
//
// The block whole for a reason yamledit has: it cuts a key out of a block
// mapping, and out of a flow mapping only while the member's value is a plain
// value, so a member written as a collection (`image: { apt: [libvips-dev] }`)
// has no edit that takes the key alone. A block the removal empties has
// nothing to keep that would stop it going with its keys — and going whole is
// the same document either way.
func layerRemovalEdits(profile string, held map[string]any) []layerRemovalEdit {
	var out []layerRemovalEdit
	taken := map[string]bool{}
	for _, key := range config.RemovedImageKeys() {
		bare, ok := strings.CutPrefix(key, "workflows.*.")
		if !ok {
			continue
		}
		block, leaf, nested := strings.Cut(bare, ".")
		if !nested {
			if _, ok := held[bare]; ok {
				out = append(out, layerRemovalEdit{path: []string{bare},
					keys: []layerRemovedKey{{Profile: profile, Key: bare}}})
			}
			continue
		}
		sub, ok := held[block].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := sub[leaf]; !ok {
			continue
		}
		if taken[block] {
			continue
		}
		if removalTakesTheBlock(block, sub) {
			taken[block] = true
			e := layerRemovalEdit{path: []string{block}}
			for _, gone := range config.RemovedImageKeys() {
				g, ok := strings.CutPrefix(gone, "workflows.*."+block+".")
				if !ok {
					continue
				}
				if _, ok := sub[g]; ok {
					e.keys = append(e.keys, layerRemovedKey{Profile: profile, Key: block + "." + g})
				}
			}
			out = append(out, e)
			continue
		}
		out = append(out, layerRemovalEdit{path: []string{block, leaf},
			keys: []layerRemovedKey{{Profile: profile, Key: block + "." + leaf}}})
	}
	return out
}

// removalTakesTheBlock reports whether every key sub writes is one the removal
// refuses: the block around them then has nothing left to hold.
func removalTakesTheBlock(block string, sub map[string]any) bool {
	for k := range sub {
		if config.RemovedImageKeyMessage("workflows.*."+block+"."+k) == "" {
			return false
		}
	}
	return true
}

// removedKeysOut takes the keys the repository-image removal refuses out of a
// project layer document, leaving every other byte as it is. It returns nil
// when there is nothing to do: a document holding no removed key, one whose
// profiles the doc does not decode, and one whose every edit leaves the bytes
// as they were.
//
// The result is not always one a caller may write, and the two ways it is not
// are kept apart — Refused for the key yamledit will not cut out of the
// document at all, Problems for the document the removal does make and this
// Fugaro does not read (a profile is emptied and `default_profile` named it,
// say) — because a caller that read either as "nothing to do" would report a
// layer converged while the document still holds keys every reader refuses. Data
// and Layer are what the side is written from, and mean nothing when unwritable.
func removedKeysOut(data []byte, lc *localcfg.Config) *layerRemoval {
	var doc layerProfiles
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Profiles) == 0 {
		return nil
	}
	r := &layerRemoval{}
	out := data
	for _, name := range slices.Sorted(maps.Keys(doc.Profiles)) {
		held, _ := doc.Profiles[name].(map[string]any)
		for _, e := range layerRemovalEdits(name, held) {
			next, err := yamledit.RemoveKeyWithOptions(out,
				append([]string{"profiles", name}, e.path...), yamledit.RemoveOptions{DropEmptyParents: true})
			if err != nil {
				// The keys of this edit go with the ones already taken out of
				// the report, because this run writes neither: what the sentence
				// names is what the removal would take out of the side.
				r.Keys = append(r.Keys, e.keys...)
				r.Refused = spliceRefusal(err)
				return r
			}
			if yamledit.Changed(out, next) {
				r.Keys = append(r.Keys, e.keys...)
			}
			out = next
		}
	}
	if len(r.Keys) == 0 {
		return nil
	}
	r.Data = out
	var left layerProfiles
	if err := yaml.Unmarshal(out, &left); err != nil {
		r.Problems = []config.Problem{{Message: "the removal leaves a document that is not YAML this Fugaro reads"}}
		return r
	}
	for _, name := range slices.Sorted(maps.Keys(doc.Profiles)) {
		if _, ok := left.Profiles[name]; !ok {
			r.Profiles = append(r.Profiles, name)
		}
	}
	l, ps := projectLayerProblems(out, lc)
	r.Layer, r.Problems = l, ps
	return r
}

// printLayerRemoval says what the removal took out of one place and what a
// repository that needed those keys does now, before the write it belongs to:
// the keys are gone from the layer rather than refused, so the reader is told
// what the layer lost, and each line is the sentence the key is refused with.
func printLayerRemoval(w io.Writer, place string, r *layerRemoval) {
	noun := "keys"
	if len(r.Keys) == 1 {
		noun = "key"
	}
	fmt.Fprintf(w, "%s holds %d %s the repository-image removal refuses, so this run rewrites it without them:\n", place, len(r.Keys), noun)
	for _, k := range r.Keys {
		fmt.Fprintf(w, "  %s: %s\n", k.Path(), k.Sentence())
	}
	for _, name := range r.Profiles {
		fmt.Fprintf(w, "  the profile %s held nothing else, so it goes with them: a repository whose profile: names it has to select another one\n", name)
	}
}

// spliceRefusal is yamledit's own refusal in the owner's words, the way a
// models write renders it: the class, never the document's bytes. The removal
// meets it on a layer whose profile writes a removed key inside a flow mapping
// whose value is a collection — `image: { apt: [...] }` written as one member
// of a `{ ... }` line — which has no edit that takes the key out.
func spliceRefusal(err error) string {
	reason := yamledit.ReasonOf(err)
	if reason == "" {
		return "this editor refused to cut the key out of the document"
	}
	return fmt.Sprintf("this editor will not cut the key out of the document as it is written — %s", reason)
}

// why is what stopped the removal, for a sentence: the editor's own refusal, or
// the problems of the document the removal did make. The two are failures on
// different sides of the write — one is a removal there is none of, the other a
// removal a reader refuses what it left — and both are told apart from a nil
// result, which is a document with nothing to take out.
func (r *layerRemoval) why() string {
	if r.Refused != "" {
		return r.Refused
	}
	return "what is left is not a project layer this Fugaro reads: " + pluginwire.Printable(layerProblemsText(r.Problems))
}

// removedKeysFolded is the removal's sentence for the failures that stop it
// before any write: the keys it would take out of one side, and what stopped it.
// What the layer itself supplies (the profile names of the keys, the problems'
// text) is wrapped printable here rather than the sentence being wrapped whole
// at the call site: MaxPrintable runes is not enough for the reason and the
// instruction that ends the sentence, and a cut refusal loses the half an
// operator acts on.
func removedKeysFolded(r *layerRemoval) string {
	if r.Refused != "" {
		return fmt.Sprintf("the repository-image removal takes %s out of it, and this run does not write it: %s; take the key out by hand and run %s again",
			pluginwire.Printable(r.text()), r.why(), selfCommand()+" update")
	}
	return fmt.Sprintf("the repository-image removal takes %s out of it, and %s", pluginwire.Printable(r.text()), r.why())
}

// objectRefusalFolded is the same failure on the published object, and it is a
// sentence of its own: removedKeysFolded hands the work to the owner because the
// side it is about is a file of theirs that they can edit, and this side is an
// object in the project's bucket, where nothing is theirs to edit by hand. What
// repairs it is a layer published over it, and the door that does that is named
// with the flag it needs: the published object is a document this Fugaro does
// not read, so the executable-key gate counts every executable key of the layer
// that replaces it as changed (L7) and refuses without --executable-changes —
// which `fugaro update`, the command that brought the reader here, has not got.
func objectRefusalFolded(r *layerRemoval) string {
	return fmt.Sprintf("the published project layer holds %s and this run does not take them out of it: %s; it is not a file to edit by hand — publish a layer without them over it with %s, and run %s again",
		pluginwire.Printable(r.text()), r.why(), selfCommand()+" config publish --executable-changes", selfCommand()+" update")
}

// layerMigrateWhat is the fourth thing this stage asks before it writes: the
// published object is rewritten in place, not replaced from a file.
const layerMigrateWhat = "this rewrites the published project layer in place: the keys the repository-image removal refuses are taken out of it, and nothing else about it changes. Every repository of the project resolves against it, and so does every run of them"

// objectAt is config.LayerKey as this run read it, for the write that replaces
// it: the bytes the conditional write is made against are the object's own
// (which the removal may since have taken keys out of), and the report and the
// executable-key gate are made of the document the removal leaves, which is
// the only one of the two this Fugaro may parse, diff or echo.
func (p *layerPlan) objectAt() *layerAt {
	at := &layerAt{old: p.published, gen: p.gen, exists: p.pubExists}
	if p.objectRemoval != nil {
		at.old, at.shown = p.publishedRaw, p.published
	}
	return at
}

// migrateObject rewrites the published project layer in place without the keys
// the repository-image removal refuses, against the generation this run read (a
// publisher that landed in between is refused, never overwritten), and fans the
// object out to the project's repositories the way a publish does. The layer is
// left published throughout: no door of the project ever sees it gone.
//
// The generation it lands at is what this run's other halves record, so the
// write is made before any of them reads it (p.gen is left at the new one).
func (s *layerStage) migrateObject(ctx context.Context, env initflow.Env, p *layerPlan) error {
	cmd, w := s.e.r.cmd, s.e.r.w
	printLayerRemoval(w, "the published project layer", p.objectRemoval)
	if err := s.e.r.asking(func() error { return confirmRemoval(cmd, p.lc, env.Yes, layerMigrateWhat) }); err != nil {
		return err
	}
	b, err := openRecordBucket(ctx, p.url)
	if err != nil {
		return remote(fmt.Errorf("opening %s to rewrite the project layer: %w", p.url, err))
	}
	defer b.Close()
	old := p.gen
	// The record is written by whichever half of this run writes the local
	// layer file (or by none of them, on a machine with no file), never here:
	// the generation this run adopts is the one the file ends up written from.
	gen, err := publishLayer(ctx, w, b, p.objectLayer, false, p.objectAt(), layerRecord{})
	if err != nil {
		return err
	}
	p.gen = gen
	fmt.Fprintf(w, "rewrote the published project layer without them (generation %d -> %d, sha256 %s)\n", old, gen, p.objectLayer.SHA256)
	failed := fanOutLayer(ctx, w, &cloudEnv{lc: p.lc, bucket: b}, p.objectLayer, gen)
	if failed > 0 {
		return remote(fmt.Errorf("the project layer is rewritten, but %d repository copy(ies) failed (listed above); run %s again", failed, selfCommand()+" update"))
	}
	return nil
}

// rewriteLocal is the migration's other half: the local layer file is written
// from next, backed up first, and says so. It leaves the generation record to
// its caller, which is the only one that knows which write the file now comes
// from.
func rewriteLocal(w io.Writer, p *layerPlan, next []byte) error {
	backup, err := backupFile(p.path, p.localRaw, time.Now())
	if err != nil {
		return userErr("backing up %s before taking the removed keys out of it: %v", p.path, err)
	}
	if err := writeFileAtomic(p.path, next); err != nil {
		return userErr("rewriting %s without the removed keys: %v", p.path, err)
	}
	fmt.Fprintf(w, "rewrote %s without them (backup at %s)\n", p.path, backup)
	return nil
}

// fileRewriteWhat is a state's own sentence plus the local layer file's own
// migration, for a run that takes the removed keys out of that file too: every
// write this run makes is one the operator was shown first.
func (p *layerPlan) fileRewriteWhat(base string) string {
	if p.localRemoval == nil {
		return base
	}
	return base + "; it also rewrites your local layer file " + p.path + " without the keys the repository-image removal refuses, keeping a copy of it"
}

// migrateLocal is the migration's half on this machine: the local layer file is
// written from the object's own bytes (after a migrateObject the two sides are
// the same layer), and the generation the object is at is recorded beside it.
//
// The object's half is what this stage confirmed (migrateObject), and it is
// only asked for when there is one: a file that is the only side still holding
// the removed keys — the object was published by a run that already took them
// out, or by another machine's — writes nothing but the file, so it is asked
// for on its own. The file is printed and a copy of it kept either way, the way
// the re-seed does.
func (s *layerStage) migrateLocal(env initflow.Env, p *layerPlan) (initflow.Outcome, error) {
	cmd, out := s.e.r.cmd, s.e.r.w
	if p.objectRemoval == nil {
		if err := s.e.r.asking(func() error { return confirmRemoval(cmd, p.lc, env.Yes, layerFileMigrateWhat) }); err != nil {
			return initflow.Outcome{}, err
		}
	}
	if p.localRemoval != nil {
		printLayerRemoval(out, "your local layer file "+p.path, p.localRemoval)
		if err := rewriteLocal(out, p, p.published); err != nil {
			return initflow.Outcome{}, err
		}
	}
	if err := layerGenerationRecord(out, p.configPath, p.lc, p.gen); err != nil {
		return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("rewrote %s (generation %d)", p.rewroteWhat(), p.gen)}, err
	}
	return initflow.Outcome{Changed: true,
		Detail: fmt.Sprintf("rewrote %s without the removed keys (generation %d)", p.rewroteWhat(), p.gen)}, nil
}

// rewroteWhat names the side the migrate state rewrote, for the report of it:
// the published object in place, or this machine's file alone when the object
// already holds none of the removed keys.
func (p *layerPlan) rewroteWhat() string {
	if p.objectRemoval == nil {
		return "your local layer file"
	}
	return "the published project layer"
}
