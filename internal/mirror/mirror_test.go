package mirror

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func (e *env) plan() (*Plan, error) {
	return e.m.Plan(context.Background(), e.srcRef, e.dstRef, "")
}

func (e *env) copy() (Result, error) {
	p, err := e.plan()
	if err != nil {
		return Result{}, err
	}
	return e.m.Copy(context.Background(), p)
}

func (e *env) destTag() string { return e.dst2.tags[dstRepo+":1.2.3"] }

func TestMirrorCopiesTheAmd64ImageDigestVerified(t *testing.T) {
	e := newEnv(t)
	res, err := e.copy()
	if err != nil || !res.Changed {
		t.Fatalf("copy: %+v %v", res, err)
	}
	if got := e.destTag(); got != e.img.digest || res.Digest != e.img.digest {
		t.Fatalf("destination tag is %s, want the amd64 image %s", got, e.img.digest)
	}
	for _, b := range append([][]byte{e.img.cfg}, e.img.layers...) {
		if string(e.dst2.blobs[dg(b)]) != string(b) {
			t.Fatalf("blob %s missing or different at the destination", dg(b))
		}
	}
	if string(e.dst2.manifests[e.img.digest]) != string(e.img.manifest) {
		t.Fatal("the manifest was not pushed byte for byte")
	}
}

func TestMirrorVerifiesDigest(t *testing.T) {
	e := newEnv(t)
	e.src.tamperBlob = func(d string, b []byte) []byte {
		if d == dg(e.img.layers[1]) {
			c := append([]byte(nil), b...)
			c[0] ^= 0xff // same length, other bytes
			return c
		}
		return b
	}
	e.dst.lax = true // even a destination that would take it
	_, err := e.copy()
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want a digest mismatch", err)
	}
	if e.destTag() != "" || e.dst.count("PUT", "/manifests/") != 0 {
		t.Fatal("a tag moved after a tampered blob")
	}
	if _, ok := e.dst2.blobs[dg(e.img.layers[1])]; ok {
		t.Fatal("the tampered blob was finalized at the destination")
	}
}

func TestTamperedBlobLengthRefused(t *testing.T) {
	for name, f := range map[string]func([]byte) []byte{
		"short": func(b []byte) []byte { return b[:len(b)-1] },
		"long":  func(b []byte) []byte { return append(append([]byte(nil), b...), 'x') },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.src.tamperBlob = func(d string, b []byte) []byte {
				if d == dg(e.img.layers[0]) {
					return f(b)
				}
				return b
			}
			e.dst.lax = true
			if _, err := e.copy(); err == nil {
				t.Fatal("a blob of the wrong length was accepted")
			}
			if e.destTag() != "" {
				t.Fatal("a tag moved")
			}
		})
	}
}

func TestTamperedManifestRejected(t *testing.T) {
	t.Run("header digest differs from the bytes", func(t *testing.T) {
		e := newEnv(t)
		e.src.headerDigest = func(string) string { return dg([]byte("something else")) }
		if _, err := e.plan(); !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a manifest fetched by digest that does not hash to it", func(t *testing.T) {
		e := newEnv(t)
		// The index names the amd64 image by its digest; the source serves other bytes there.
		other := makeImage("amd64", "other-layer")
		e.sst.manifests[e.img.digest] = other.manifest
		if _, err := e.plan(); !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a config that is not its digest", func(t *testing.T) {
		e := newEnv(t)
		e.sst.blobs[dg(e.img.cfg)] = []byte(`{"os":"linux","architecture":"amd65"}`)
		if _, err := e.plan(); !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestWrongExpectedDigestRefused(t *testing.T) {
	e := newEnv(t)
	_, err := e.m.Plan(context.Background(), e.srcRef, e.dstRef, dg([]byte("not this release")))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v", err)
	}
	top := e.sst.tags[srcRepo+":1.2.3"]
	if _, err := e.m.Plan(context.Background(), e.srcRef, e.dstRef, top); err != nil {
		t.Fatalf("the right digest is refused: %v", err)
	}
}

func TestMirrorIdempotent(t *testing.T) {
	e := newEnv(t)
	if _, err := e.copy(); err != nil {
		t.Fatal(err)
	}
	srcBlobGets := e.src.count("GET", "/blobs/")
	dstWrites := e.dst.writes()
	p, err := e.plan()
	if err != nil || !p.Present {
		t.Fatalf("second plan: %+v %v", p, err)
	}
	res, err := e.m.Copy(context.Background(), p)
	if err != nil || res.Changed || res.Description != "No changes" {
		t.Fatalf("second copy: %+v %v", res, err)
	}
	if e.dst.writes() != dstWrites {
		t.Fatal("the second copy wrote to the destination")
	}
	// The plan reads the (small) config again to check the platform, and no layer.
	if got := e.src.count("GET", "/blobs/"); got-srcBlobGets > 1 {
		t.Fatalf("the second run read %d blobs from the source", got-srcBlobGets)
	}
}

func TestMirrorRefusesUnreadableSource(t *testing.T) {
	for name, set := range map[string]func(*env){
		"private package (token refused)": func(e *env) {
			e.src.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
		},
		"no such tag": func(e *env) { delete(e.sst.tags, srcRepo+":1.2.3") },
		"asks for basic credentials": func(e *env) {
			e.src.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
				w.WriteHeader(401)
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			set(e)
			_, err := e.plan()
			if !errors.Is(err, ErrUnreadableSource) {
				t.Fatalf("err = %v", err)
			}
			if e.dst.writes() != 0 {
				t.Fatal("the destination was written")
			}
		})
	}
}

func TestMirrorRefusesMissingAmd64(t *testing.T) {
	t.Run("an index without it", func(t *testing.T) {
		e := newEnv(t)
		e.sst.publishIndex(srcRepo, "1.2.3", map[string]image{"arm64": makeImage("arm64", "a")})
		if _, err := e.plan(); !errors.Is(err, ErrNoAmd64) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a single arm64 image", func(t *testing.T) {
		e := newEnv(t)
		im := makeImage("arm64", "a")
		e.sst.put(im)
		e.sst.tags[srcRepo+":1.2.3"] = im.digest
		if _, err := e.plan(); !errors.Is(err, ErrNoAmd64) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an index that says amd64 for an arm64 image", func(t *testing.T) {
		e := newEnv(t)
		e.sst.publishIndex(srcRepo, "1.2.3", map[string]image{"amd64": makeImage("arm64", "lie")})
		if _, err := e.plan(); !errors.Is(err, ErrNoAmd64) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestMirrorInterruptedResumes(t *testing.T) {
	e := newEnv(t, "one-one-one", "two-two-two", "three-three")
	e.dst.failPatch = 3 // the third blob upload fails
	if _, err := e.copy(); err == nil {
		t.Fatal("an interrupted copy succeeded")
	}
	if e.destTag() != "" || e.dst.count("PUT", "/manifests/") != 0 {
		t.Fatal("a tag moved by a partial copy")
	}
	have := len(e.dst2.blobs)
	if have == 0 || have >= 4 {
		t.Fatalf("expected a partial set of blobs, have %d", have)
	}
	e.dst.failPatch = 0
	before := e.dst.count("POST", "/blobs/uploads/")
	res, err := e.copy()
	if err != nil || e.destTag() != e.img.digest {
		t.Fatalf("resume: %+v %v", res, err)
	}
	if sent := e.dst.count("POST", "/blobs/uploads/") - before; sent != 4-have || res.BlobsKept != have {
		t.Fatalf("the rerun started %d uploads and kept %d blobs; %d were already there", sent, res.BlobsKept, have)
	}
}

func TestManifestIsPushedLast(t *testing.T) {
	e := newEnv(t)
	if _, err := e.copy(); err != nil {
		t.Fatal(err)
	}
	lastBlob, manifest := -1, -1
	for i, q := range e.dst.requests() {
		switch {
		case q.Method == "PUT" && strings.Contains(q.Path, "/blobs/uploads/"):
			lastBlob = i
		case q.Method == "PUT" && strings.Contains(q.Path, "/manifests/"):
			manifest = i
		}
	}
	if manifest < 0 || lastBlob < 0 || manifest < lastBlob {
		t.Fatalf("the manifest was put at %d, the last blob at %d", manifest, lastBlob)
	}
}

func TestTagMovedBetweenStepsRefused(t *testing.T) {
	e := newEnv(t)
	p, err := e.plan()
	if err != nil {
		t.Fatal(err)
	}
	// The release tag is moved to another image after the plan.
	other := makeImage("amd64", "malicious")
	e.sst.publishIndex(srcRepo, "1.2.3", map[string]image{"amd64": other})
	_, err = e.m.Copy(context.Background(), p)
	if !errors.Is(err, ErrTagMoved) {
		t.Fatalf("err = %v", err)
	}
	if e.destTag() != "" {
		t.Fatal("a moved tag was copied")
	}
}

func TestPlanMakesNoWrites(t *testing.T) {
	e := newEnv(t)
	p, err := e.plan()
	if err != nil {
		t.Fatal(err)
	}
	if e.dst.writes() != 0 || e.src.writes() != 0 {
		t.Fatalf("a plan wrote: %+v", e.dst.requests())
	}
	if p.Present || p.TransferBytes != p.TotalBytes || p.TotalBytes == 0 || len(p.Blobs) != 3 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestSourceAllowlist(t *testing.T) {
	ok := []string{"ghcr.io/dimipaun/fugaro-go:1.2.3", "ghcr.io/dimipaun/fugaro-history:0.1.0"}
	for _, s := range ok {
		if _, err := ParseSource(DefaultSources, s); err != nil {
			t.Errorf("%s refused: %v", s, err)
		}
	}
	bad := []string{
		"ghcr.io/evil/fugaro-go:1.2.3",
		"evil.example/dimipaun/fugaro-go:1.2.3",
		"ghcr.io/dimipaunx/fugaro-go:1.2.3",
		"ghcr.io/dimipaun/../evil/x:1",
		"ghcr.io@evil.example/dimipaun/fugaro-go:1",
		"ghcr.io/dimipaun/fugaro-go",
		"ghcr.io/dimipaun/fugaro-go@sha256:" + strings.Repeat("a", 64),
		"ghcr.io/dimipaun:1",
		"docker.io/library/alpine:3",
	}
	for _, s := range bad {
		if _, err := ParseSource(DefaultSources, s); err == nil {
			t.Errorf("%s was allowed", s)
		}
	}
	if _, err := ParseSource([]string{"ghcr.io/acme"}, "ghcr.io/acme/fugaro-go:1"); err != nil {
		t.Errorf("a fork's explicit source is refused: %v", err)
	}
	if _, err := ParseSource([]string{"ghcr.io/acme"}, "ghcr.io/dimipaun/fugaro-go:1"); err == nil {
		t.Error("a fork's override still allowed the default")
	}
	// Plan and Copy enforce it too, not only the parser.
	e := newEnv(t)
	e.srcRef = Ref{srcHost, "evil/fugaro-go", "1.2.3"}
	if _, err := e.plan(); err == nil || len(e.src.requests()) != 0 {
		t.Fatalf("a source outside the allowlist was contacted: %v %d", err, len(e.src.requests()))
	}
	for _, p := range []string{"ghcr.io", "ghcr.io/", "https://ghcr.io/x", "ghcr.io/a b", "ghcr.io/OWNER"} {
		if ValidSourcePrefix(p) == nil {
			t.Errorf("source prefix %q accepted", p)
		}
	}
	if ValidSourcePrefix("ghcr.io/acme") != nil {
		t.Error("ghcr.io/acme refused")
	}
}

func TestDestinationIsOnlyTheProjectsOwnRegistry(t *testing.T) {
	d, err := Dest("us-east5-docker.pkg.dev/proj-abcde", "proj-abcde", "fugaro-go", "1.2.3")
	if err != nil || d.Host != "us-east5-docker.pkg.dev" || d.Repo != "proj-abcde/fugaro-base/fugaro-go" {
		t.Fatalf("Dest = %+v %v", d, err)
	}
	for _, c := range [][3]string{
		{"us-east5-docker.pkg.dev/other-proj", "proj-abcde", "fugaro-go"}, // another project
		{"evil.example/proj-abcde", "proj-abcde", "fugaro-go"},            // another host
		{"us-east5-docker.pkg.dev/proj-abcde/x", "proj-abcde", "fugaro-go"},
		{"ghcr.io/proj-abcde", "proj-abcde", "fugaro-go"},
		{"us-east5-docker.pkg.dev/proj-abcde", "proj-abcde", "../other/x"}, // traversal in the name
		{"us-east5-docker.pkg.dev/proj-abcde", "proj-abcde", "a/b"},
		{"", "proj-abcde", "fugaro-go"},
	} {
		if _, err := Dest(c[0], c[1], c[2], "1"); err == nil {
			t.Errorf("Dest(%q, %q, %q) was allowed", c[0], c[1], c[2])
		}
	}
	// A hand-built Ref outside the shape is refused by Plan and Copy.
	e := newEnv(t)
	for _, bad := range []Ref{
		{"evil.example", "proj-abcde/fugaro-base/x", "1"},
		{dstHost, "other/not-base/x", "1"},
		{dstHost, "proj-abcde/fugaro-base", "1"},
	} {
		e.dstRef = bad
		if _, err := e.plan(); err == nil {
			t.Errorf("destination %s was allowed", bad)
		}
	}
	if len(e.dst.requests()) != 0 {
		t.Fatal("the destination was contacted")
	}
}

func TestNoCredentialsToTheSource(t *testing.T) {
	e := newEnv(t)
	if _, err := e.copy(); err != nil {
		t.Fatal(err)
	}
	for _, q := range e.src.requests() {
		if strings.Contains(q.Auth, dstTok) {
			t.Fatalf("the destination credential reached the source: %+v", q)
		}
		if q.Auth != "" && q.Auth != "Bearer "+srcTok {
			t.Fatalf("the source got an Authorization that is not its own anonymous token: %+v", q)
		}
	}
	if e.src.count("GET", "/token") == 0 {
		t.Fatal("the anonymous token flow was not exercised")
	}
}

func TestCredentialOnlyToTheDestination(t *testing.T) {
	e := newEnv(t)
	if _, err := e.copy(); err != nil {
		t.Fatal(err)
	}
	for _, q := range e.dst.requests() {
		if q.Auth != "Bearer "+dstTok {
			t.Fatalf("a destination request without the user's token: %+v", q)
		}
	}
}

func TestUploadLocationOnAnotherHostRefused(t *testing.T) {
	e := newEnv(t)
	var leaked []string
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = append(leaked, r.Header.Get("Authorization"))
		w.WriteHeader(202)
	}))
	defer evil.Close()
	e.dst.locationHost = evil.URL
	_, err := e.copy()
	if err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("err = %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("the credential went to another host: %v", leaked)
	}
	if e.destTag() != "" {
		t.Fatal("a tag moved")
	}
}

func TestRedirectHandling(t *testing.T) {
	t.Run("a blob redirect to a CDN carries no credentials", func(t *testing.T) {
		e := newEnv(t)
		cdn := newReg(t, e.sst)
		e.src.redirectBlobTo = cdn.srv.URL
		if _, err := e.copy(); err != nil {
			t.Fatal(err)
		}
		if cdn.count("GET", "/cdn/") == 0 {
			t.Fatal("the redirect was not followed")
		}
		for _, q := range cdn.requests() {
			if q.Auth != "" {
				t.Fatalf("Authorization sent to the redirect target: %+v", q)
			}
		}
	})
	t.Run("a redirect to plain http elsewhere is refused", func(t *testing.T) {
		e := newEnv(t)
		e.m.allowHTTPRedirect = false
		cdn := newReg(t, e.sst)
		e.src.redirectBlobTo = cdn.srv.URL
		_, err := e.copy()
		if err == nil || !strings.Contains(err.Error(), "not https") {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(err.Error(), "SECRET-SIG") {
			t.Fatalf("the redirect's signature is in the error: %v", err)
		}
	})
	t.Run("the destination's redirects are not followed", func(t *testing.T) {
		e := newEnv(t)
		hit := false
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
		defer other.Close()
		e.dst.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
		})
		if _, err := e.copy(); err == nil {
			t.Fatal("a redirecting destination succeeded")
		}
		if hit {
			t.Fatal("the destination's redirect was followed with the credential")
		}
	})
	t.Run("a token endpoint that is plain http elsewhere is not asked", func(t *testing.T) {
		e := newEnv(t)
		asked := false
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked = true }))
		defer other.Close()
		e.src.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+other.URL+`/token"`)
			w.WriteHeader(401)
		})
		if _, err := e.plan(); !errors.Is(err, ErrUnreadableSource) || asked {
			t.Fatalf("err = %v, asked = %v", err, asked)
		}
	})
}

func TestSizeCaps(t *testing.T) {
	t.Run("a blob over the cap", func(t *testing.T) {
		e := newEnv(t)
		e.m.Limits = DefaultLimits
		e.m.Limits.Blob = 10
		if _, err := e.plan(); err == nil {
			t.Fatal("an oversized layer was planned")
		}
	})
	t.Run("the whole image over the cap", func(t *testing.T) {
		e := newEnv(t)
		e.m.Limits = DefaultLimits
		e.m.Limits.Total = 40
		if _, err := e.plan(); err == nil {
			t.Fatal("an oversized image was planned")
		}
	})
	t.Run("too many layers", func(t *testing.T) {
		e := newEnv(t)
		e.m.Limits = DefaultLimits
		e.m.Limits.Layers = 1
		if _, err := e.plan(); err == nil {
			t.Fatal("too many layers were planned")
		}
	})
	t.Run("a manifest over the cap", func(t *testing.T) {
		e := newEnv(t)
		e.m.Limits = DefaultLimits
		e.m.Limits.Manifest = 50
		if _, err := e.plan(); err == nil {
			t.Fatal("an oversized manifest was read")
		}
	})
	t.Run("a foreign layer", func(t *testing.T) {
		raw := []byte(`{"schemaVersion":2,"config":{"digest":"` + dg([]byte("c")) + `","size":1},"layers":[{"digest":"` + dg([]byte("l")) + `","size":1,"urls":["http://evil/x"]}]}`)
		if _, _, err := parseImage(raw, mtOCIManifest, DefaultLimits); err == nil {
			t.Fatal("a foreign layer was accepted")
		}
	})
}

func TestNoSecretInErrorsOrLog(t *testing.T) {
	var lines []string
	check := func(t *testing.T, texts ...string) {
		t.Helper()
		for _, s := range texts {
			for _, secret := range []string{dstTok, srcTok, "UPLOAD-STATE-SECRET", "SECRET-SIG"} {
				if strings.Contains(s, secret) {
					t.Fatalf("%q appears in %q", secret, s)
				}
			}
		}
	}
	e := newEnv(t)
	e.m.Log = func(s string) { lines = append(lines, s) }
	cdn := newReg(t, e.sst)
	e.src.redirectBlobTo = cdn.srv.URL
	if _, err := e.copy(); err != nil {
		t.Fatal(err)
	}
	check(t, lines...)
	if len(lines) == 0 {
		t.Fatal("nothing was logged")
	}
	// Failures: a refused destination, a failed upload, a bad blob.
	e2 := newEnv(t)
	e2.dst.failPatch = 1
	_, err := e2.copy()
	check(t, err.Error())
	e3 := newEnv(t)
	e3.dst.bearer = "another-token"
	_, err = e3.copy()
	if err == nil {
		t.Fatal("a refused credential succeeded")
	}
	check(t, err.Error())
}

func TestUnauthorizedDestinationSaysWhatToDo(t *testing.T) {
	e := newEnv(t)
	e.dst.bearer = "another-token"
	_, err := e.plan()
	if err == nil || !strings.Contains(err.Error(), "artifactregistry.writer") {
		t.Fatalf("err = %v", err)
	}
}
