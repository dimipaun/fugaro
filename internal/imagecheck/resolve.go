package imagecheck

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// This file resolves a repository's dependency set (design
// docs/forge-authorship-images/shared-images.md §2.5, D622): the four inputs
// §2.1 names, reduced to the facts Key hashes. It runs **only on the
// operator's machine** — a job runs with installation credentials and a
// build's own clone is not the tree the operator's checkout is — so the
// refresh computes a key here and hands it to the build, which writes the
// value it was given and never recomputes one (D622).

// Resolve computes the resolved dependency set of a workflow: every key file
// its KeyFiles set holds, reduced per ecosystem; the toolchain its mise
// configuration and image.tools state; and the base the build runs FROM.
//
// tree must be the repository at its **base branch head** (§2.5), and not the
// working directory: the build clones the base branch, so a set read from
// uncommitted work would key an image built from another tree. In production
// that is imagecheck.Open at the base branch's ref.
//
// A key file that does not reduce — a lock of a format §2.3's table does not
// name, a package from a source that is not its ecosystem's registry, a file
// over the size or entry bound, a tool file whose toolchain this package
// cannot read — comes back as an *UnkeyedError, and the caller then treats the
// repository as §2.5's unkeyed one: it keeps its own per-workflow image and is
// on no shared one. Anything else is an error about the workflow or the tree.
func Resolve(cfg *config.Config, workflow string, tree Tree, baseRef, baseDigest string) (Resolved, error) {
	w, ok := cfg.Workflows[workflow]
	if !ok {
		return Resolved{}, fmt.Errorf("fugaro.yaml has no workflow %q", workflow)
	}
	files, err := KeyFiles(cfg, workflow, tree)
	if err != nil {
		return Resolved{}, err
	}
	var pkgs []string
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		data, err := readKeyFile(tree, rel)
		if err != nil {
			return Resolved{}, err
		}
		facts, err := Reduce(rel, data)
		if err != nil {
			return Resolved{}, err
		}
		pkgs = append(pkgs, facts...)
	}
	tools, err := toolChain(tree, w.Image.Tools)
	if err != nil {
		return Resolved{}, err
	}
	slices.Sort(pkgs)
	slices.Sort(tools)
	return Resolved{
		BaseRef: baseRef, BaseDigest: baseDigest,
		Packages: slices.Compact(pkgs), Tools: slices.Compact(tools),
	}, nil
}

// sizedTree is a Tree that can answer a file's size without reading it. A key
// file is bounded before it is read (§2.3's last paragraph): Reduce refuses a
// file over the bound, but by then the bytes are already in memory, and the
// read is the caller's to bound. Both trees this package ships implement it —
// git answers from the object header, a directory from the file's stat — so
// Resolve bounds the read wherever a tree can, and falls back to reading and
// letting Reduce refuse where one cannot.
type sizedTree interface {
	Size(path string) (int64, error)
}

// readKeyFile reads one key file of tree, refusing one the reduction would not
// take before it is loaded.
func readKeyFile(tree Tree, rel string) ([]byte, error) {
	if st, ok := tree.(sizedTree); ok {
		switch n, err := st.Size(rel); {
		case errors.Is(err, fs.ErrNotExist):
			return nil, &UnkeyedError{Path: rel, Reason: UnkeyedUnreadable, Detail: "it is not a file of the tree the key is computed from"}
		case err != nil:
			return nil, fmt.Errorf("the key file %s: %w", rel, err)
		case n > maxKeyFileBytes:
			return nil, &UnkeyedError{Path: rel, Reason: UnkeyedTooLarge, Detail: fmt.Sprintf("%d bytes", n)}
		}
	}
	data, err := tree.ReadFile(rel)
	if err != nil {
		return nil, fmt.Errorf("the key file %s: %w", rel, err)
	}
	return data, nil
}

// SplitFact takes a pkg: fact apart into the three fields §2.4 writes it as: a
// package's name, its version and the integrity the file stated for that
// version. ok is false for a fact that is not three quoted literals, which is
// not a fact this package wrote.
func SplitFact(fact string) (name, version, integrity string, ok bool) {
	fields := strings.Split(fact, " ")
	if len(fields) != 3 {
		return "", "", "", false
	}
	for i, f := range fields {
		v, err := strconv.Unquote(f)
		if err != nil {
			return "", "", "", false
		}
		switch i {
		case 0:
			name = v
		case 1:
			version = v
		default:
			integrity = v
		}
	}
	return name, version, integrity, true
}
