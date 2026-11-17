package diagnoseagent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// bundleManifest is one manifest as the launcher writes it: the strict shape
// ReadBundle parses, marshalled here so a field renamed in Go and a field
// renamed in the JSON cannot drift apart unnoticed. T-TC5's WriteBundle is the
// other half of this pair; until it lands, the manifest a test reads back is
// one this file wrote.
func bundleManifest(parts ...BundlePart) Bundle {
	return Bundle{
		Version:       BundleVersion,
		RunID:         "update-20261107183012-48213",
		Command:       "update",
		FugaroVersion: "0.9.5",
		OS:            "linux",
		Parts:         parts,
		SourceVersion: "v0.9.5",
		SourceCommit:  "0123456789abcdef0123456789abcdef01234567",
		SourceTree:    "tree",
		Redaction:     "both passes, as the local tier sends it",
	}
}

// part is one part of those manifests, with a digest of the right shape.
func part(name, sum string) BundlePart {
	return BundlePart{Name: name, Bytes: 15, SHA256: sum}
}

const partSHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// TestBundleRejectsTraversalName: a part's name is what a filesystem path and a
// bucket key are built from (the runner's dest, and BundlePrefix+name here), so
// a name that leaves the run's prefix, is absolute, is empty or carries a NUL
// or a newline is refused when the manifest is read — before the runner builds
// a key or opens a file at a path the manifest chose (D2404). The rule is the
// task's own input-name rule, applied to the whole object name.
//
// Mutation (drop the task.InputNameProblem check): every case here is accepted
// and the escape reaches the runner's filepath.Join unchecked.
func TestBundleRejectsTraversalName(t *testing.T) {
	for _, name := range []string{
		"../escape",
		"a/../../escape",
		"/etc/x",
		"",
		"x\ny",
		"a\x00b",
		"evidence/./log.txt",
		"evidence//log.txt",
	} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			data, err := json.Marshal(bundleManifest(part("evidence/log.txt", partSHA), part(name, partSHA)))
			if err != nil {
				t.Fatal(err)
			}
			b, err := ReadBundle(data)
			if err == nil {
				t.Fatalf("the bundle named the part %q and was read: %+v", name, b)
			}
			if !strings.Contains(err.Error(), "names a part") {
				t.Errorf("the refusal does not name the part: %v", err)
			}
		})
	}
	// The same rule the other way round: a name the rule admits is read, and the
	// object name it is stored under is one spelling.
	data, err := json.Marshal(bundleManifest(part("evidence/log.txt", partSHA)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadBundle(data)
	if err != nil {
		t.Fatalf("a bundle of ordinary names was refused: %v", err)
	}
	if got := PartObjectName(b.Parts[0].Name); got != "diagnose/evidence/log.txt" {
		t.Errorf("PartObjectName = %q, want the run's own leaf %q", got, "diagnose/evidence/log.txt")
	}
}

// TestBundleRejectsUnknownField: the strict codec every wire shape in this
// program uses. A manifest from a release that added a field is refused rather
// than read with the field dropped, because the field it drops is one this
// build would have to act on.
func TestBundleRejectsUnknownField(t *testing.T) {
	const yaml = `{"version":1,"run_id":"r","command":"update","fugaro_version":"0.9.5","os":"linux",
		"parts":[{"name":"evidence/log.txt","bytes":15,"sha256":"` + partSHA + `"}],
		"source_version":"v0.9.5","source_commit":"c","source_tree":"tree","redaction":"both",
		"something_new":true}`
	if _, err := ReadBundle([]byte(yaml)); err == nil || !strings.Contains(err.Error(), "something_new") {
		t.Errorf("a manifest with an unknown field was read: %v", err)
	}
}

// TestBundleRejectsAManifestItCannotUse: everything else the manifest can get
// wrong is refused at the read, before a part is downloaded and before the
// model is called — a version this build does not read, no parts at all, the
// same part twice, a part with no bytes, and a digest that is not a SHA-256. A
// manifest a launcher of another release wrote, or one assembled by hand, is
// data this build will not act on a guess about.
func TestBundleRejectsAManifestItCannotUse(t *testing.T) {
	duplicate := bundleManifest(part("evidence/log.txt", partSHA), part("evidence/log.txt", partSHA))
	noParts := bundleManifest()
	zeroBytes := bundleManifest(BundlePart{Name: "evidence/log.txt", Bytes: 0, SHA256: partSHA})
	noDigest := bundleManifest(BundlePart{Name: "evidence/log.txt", Bytes: 15})
	shortDigest := bundleManifest(part("evidence/log.txt", "abc123"))
	upperDigest := bundleManifest(part("evidence/log.txt", strings.ToUpper(partSHA)))

	cases := []struct {
		name string
		man  Bundle
		want string
	}{
		{"no parts", noParts, "names no parts"},
		{"the same part twice", duplicate, "twice"},
		{"a part with no bytes", zeroBytes, "claims 0 bytes"},
		{"a part with no digest", noDigest, "no SHA-256"},
		{"a digest that is not one", shortDigest, "no SHA-256"},
		{"an uppercase digest", upperDigest, "no SHA-256"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.man)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadBundle(data); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("a manifest with %s was read: %v, want a refusal saying %q", tc.name, err, tc.want)
			}
		})
	}
	// A version this build does not read, which is the one refusal the runner's
	// own stage also pins (internal/runner): the fields are a wire shape both
	// sides hold, so a guessed-at version is a bundle read as something else.
	for _, version := range []int{0, BundleVersion + 1, 99} {
		man := bundleManifest(part("evidence/log.txt", partSHA))
		man.Version = version
		data, err := json.Marshal(man)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadBundle(data); err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("a manifest of version %d was read: %v", version, err)
		}
	}
	// Two documents in one object is not a manifest either: the second one is
	// read as no part of this bundle.
	data, err := json.Marshal(bundleManifest(part("evidence/log.txt", partSHA)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBundle(append(data, []byte("\n{}")...)); err == nil {
		t.Error("a manifest holding two JSON documents was read")
	}
	if _, err := ReadBundle([]byte("{not json")); err == nil {
		t.Error("a manifest that is not JSON was read")
	}
}

// TestBundleReadsTheManifestTheLauncherWrites: the accept path, field by field.
// The runner quotes the command, the version and the source's version and
// commit into the prompt and fetches the tree the last two name (D2404), so a
// field silently dropped by the codec would be a diagnosis about the wrong
// command or the wrong release.
func TestBundleReadsTheManifestTheLauncherWrites(t *testing.T) {
	man := bundleManifest(BundlePart{Name: "evidence/log.txt", Bytes: 15, SHA256: partSHA},
		BundlePart{Name: "changes.txt", Bytes: 512, SHA256: strings.Repeat("ab", 32)})
	data, err := json.Marshal(man)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadBundle(data)
	if err != nil {
		t.Fatalf("the launcher's own manifest was refused: %v", err)
	}
	if !reflect.DeepEqual(b, man) {
		t.Errorf("read back %+v, want %+v", b, man)
	}
}
