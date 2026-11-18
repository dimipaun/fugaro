package voicesvc

// The provider call's tests: the endpoints are constants of this tree's own,
// the request is written by this service and carries nothing of the caller
// except the payload, and the key is read from Secret Manager and never appears
// in an error.
//
// Hermetic throughout (Global Constraint 10): the provider's transport is a
// hook in this test, and the Secret Manager is internal/gcpfake. No test in this
// file opens a socket to a provider, and none of them needs an account.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// testKeyValue is the key these tests put in the container, and testKeySecret
// is the container's name. Neither is shaped like a provider's credential: the
// fixtures in this tree carry no secret-shaped literal, and what is being
// tested is that a value read from Secret Manager reaches one header and no log
// line, which any string shows.
const (
	testKeyValue  = "not-a-real-key-value-for-the-tests"
	testKeySecret = "voice-provider-keys"
)

// TestEveryEndpointIsHTTPSAtItsOwnVendorsHost is §8's first rule, as a test:
// every address this service dials is a constant, it is https, and it is at the
// host of the provider the engine names. A typo in a host is a call that carries
// the installation's key somewhere else, which is the one failure this file
// exists to prevent.
func TestEveryEndpointIsHTTPSAtItsOwnVendorsHost(t *testing.T) {
	hosts := map[string]string{
		"deepgram":   "api.deepgram.com",
		"google":     "texttospeech.googleapis.com",
		"elevenlabs": "api.elevenlabs.io",
	}
	if len(engines) != len(hosts) {
		t.Fatalf("the engine table holds %d entries and this test knows %d hosts: add the new one here too", len(engines), len(hosts))
	}
	for _, e := range engines {
		if len(e.name) == 0 {
			t.Fatal("an engine carries no name")
		}
		if e.endpoint == "" {
			t.Errorf("%s (%s) names no endpoint", e.name, e.direction)
			continue
		}
		u, err := url.Parse(e.endpoint)
		if err != nil {
			t.Errorf("%s: the endpoint does not parse: %v", e.name, err)
			continue
		}
		if u.Scheme != "https" {
			t.Errorf("%s: the endpoint's scheme is %q: a call carrying the installation's credential is TLS or it is not made (§8)", e.name, u.Scheme)
		}
		if want := hosts[e.name]; u.Host != want {
			t.Errorf("%s: the endpoint's host is %q, want %q", e.name, u.Host, want)
		}
		if u.User != nil {
			t.Errorf("%s: the endpoint carries userinfo", e.name)
		}
	}
}

// TestNoDocumentCarriesAnAddress: the two files a person edits — the policy node
// and the register — carry names, switches and terms and never a destination.
// §8's rule is that the hop resolves a name, so an address in either one could
// point this service anywhere and the gate would still admit the call.
func TestNoDocumentCarriesAnAddress(t *testing.T) {
	reg := loadRegister(t)
	for _, name := range reg.Names() {
		row := loadRegisterRow(t, name)
		for _, field := range []struct{ what, value string }{
			{"name", row.Name},
			{"provider", row.Provider},
			{"note", row.Note},
			{"directions", strings.Join(row.Directions, " ")},
		} {
			if strings.Contains(field.value, "://") {
				t.Errorf("%s: the register row's %s carries an address: %q", name, field.what, field.value)
			}
		}
	}
}

// TestEngineRequestWritesTheWholeCall: every part of the outbound request is
// this service's own — the method, the URL, the two headers and the body — so a
// caller cannot add a header, extend the URL or name another host.
func TestEngineRequestWritesTheWholeCall(t *testing.T) {
	for _, e := range engines {
		t.Run(e.name+" "+e.direction.String(), func(t *testing.T) {
			in := callInput{audio: strings.NewReader("audio-bytes"), audioType: "audio/webm", audioLength: 11}
			if !e.audio {
				in = callInput{text: "the text to speak"}
			}
			req, err := e.request(context.Background(), in, "a-credential")
			if err != nil {
				t.Fatalf("the request could not be written: %v", err)
			}
			if req.Method != http.MethodPost {
				t.Errorf("the method is %s, want POST", req.Method)
			}
			if got := req.URL.String(); got != e.endpoint {
				t.Errorf("the URL is %s, want the engine's own constant %s", got, e.endpoint)
			}
			cred := req.Header.Values(e.auth.header)
			if len(cred) != 1 || cred[0] != e.auth.scheme+"a-credential" {
				t.Errorf("the credential header %s is %v, want one value %q: the credential travels in exactly one header of exactly one request (§4.4 point 4)", e.auth.header, cred, e.auth.scheme+"a-credential")
			}
			// The header set is exactly two: the media type and the credential.
			// Nothing of the caller's travels here, because nothing of the
			// caller's is passed to this function except the payload.
			if len(req.Header) != 2 {
				t.Errorf("the request carries %d headers (%v), want 2: the media type and the credential", len(req.Header), req.Header)
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("the body could not be read: %v", err)
			}
			if e.audio {
				// The audio direction's body is the caller's own bytes, handed
				// on as they are: the only thing this service adds is the media
				// type it folded, and the length the caller declared.
				if ct := req.Header.Get("Content-Type"); ct != "audio/webm" {
					t.Errorf("the audio request's Content-Type is %q, want the caller's folded media type audio/webm", ct)
				}
				if req.ContentLength != 11 {
					t.Errorf("the audio request's Content-Length is %d, want the caller's declared 11", req.ContentLength)
				}
				if string(body) != "audio-bytes" {
					t.Errorf("the body is %q, want the caller's own bytes", body)
				}
				return
			}
			if ct := req.Header.Get("Content-Type"); ct != e.contentType {
				t.Errorf("the Content-Type is %q, want %q", ct, e.contentType)
			}
			if !json.Valid(body) {
				t.Fatalf("the text direction's body is not JSON: %q", body)
			}
			if !strings.Contains(string(body), "the text to speak") {
				t.Errorf("the body does not carry the caller's text:\n%s", body)
			}
		})
	}
}

// TestEngineRequestRefusesWhatItCannotWrite: the audio direction has no payload
// builder, so a request that carries no body is this tree's own failure rather
// than a call made with an empty payload.
func TestEngineRequestRefusesWhatItCannotWrite(t *testing.T) {
	e, ok := engineFor("deepgram", STT)
	if !ok {
		t.Fatal("the engine table holds no audio engine")
	}
	if _, err := e.request(context.Background(), callInput{}, "a-credential"); err == nil {
		t.Fatal("an audio request with no body was written")
	}
	broken := engine{name: "x", direction: TTS}
	if _, err := broken.request(context.Background(), callInput{text: "x"}, ""); err == nil {
		t.Fatal("an engine with no payload builder wrote a request")
	}
}

// TestTheTextsPayloadIsTheVendorsOwnShape: the two JSON bodies, as the vendors
// document them, and each carries the caller's text verbatim.
func TestTheTextsPayloadIsTheVendorsOwnShape(t *testing.T) {
	raw, err := googleSynthesizePayload("salut")
	if err != nil {
		t.Fatalf("google's payload: %v", err)
	}
	var got struct {
		Input struct {
			Text string `json:"text"`
		} `json:"input"`
		Voice struct {
			LanguageCode string `json:"languageCode"`
		} `json:"voice"`
		AudioConfig struct {
			AudioEncoding string `json:"audioEncoding"`
		} `json:"audioConfig"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("google's payload does not decode: %v", err)
	}
	if got.Input.Text != "salut" || got.Voice.LanguageCode != speakLanguage || got.AudioConfig.AudioEncoding != speakEncoding {
		t.Errorf("google's payload is %+v, want the text, %s and %s", got, speakLanguage, speakEncoding)
	}

	raw, err = elevenLabsPayload("salut")
	if err != nil {
		t.Fatalf("elevenlabs' payload: %v", err)
	}
	var el struct {
		Text    string `json:"text"`
		ModelID string `json:"model_id"`
	}
	if err := json.Unmarshal(raw, &el); err != nil {
		t.Fatalf("elevenlabs' payload does not decode: %v", err)
	}
	if el.Text != "salut" || el.ModelID != elevenLabsModel {
		t.Errorf("elevenlabs' payload is %+v, want the text and %s", el, elevenLabsModel)
	}
	// The voice id is in the endpoint's own path for that provider, so a caller
	// cannot choose a voice through the body.
	if strings.Contains(string(raw), elevenLabsVoiceID) {
		t.Error("elevenlabs' payload carries the voice id: the voice is the endpoint's, not the caller's")
	}
}

// TestAudioMediaType: the caller's media type is folded to its canonical
// spelling and nothing of the caller's text reaches the outbound header.
func TestAudioMediaType(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   string
		ok     bool
	}{
		{"audio/webm", "audio/webm", true},
		{"audio/wav; codecs=1", "audio/wav", true},
		{"AUDIO/MP4", "audio/mp4", true},
		{"audio/ogg;charset=utf-8", "audio/ogg", true},
		{" audio/webm ", "audio/webm", true},
		{"text/plain", "", false},
		{"application/json", "", false},
		{"", "", false},
		{"audio/", "", false},
		// A header value that could inject one: the type is rebuilt from the
		// parsed media type, so no byte of this reaches the provider.
		{"audio/webm\r\nX-Evil: 1", "", false},
		{"audio/webm\nX-Evil: 1", "", false},
	} {
		got, err := audioMediaType(tc.header)
		if tc.ok && err != nil {
			t.Errorf("audioMediaType(%q) failed: %v", tc.header, err)
			continue
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("audioMediaType(%q) = %q, want a refusal", tc.header, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("audioMediaType(%q) = %q, want %q", tc.header, got, tc.want)
		}
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("audioMediaType(%q) = %q: a header value this service writes carries no newline", tc.header, got)
		}
	}
}

// keyFixture is a SecretManagerKeys over the fake, with the container seeded.
type keyFixture struct {
	t    *testing.T
	f    *gcpfake.Secrets
	keys *SecretManagerKeys
	now  time.Time
}

func newKeyFixture(t *testing.T, document string) *keyFixture {
	t.Helper()
	f := gcpfake.NewSecrets(t)
	svc, err := secretmanager.NewService(context.Background(),
		option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/"))
	if err != nil {
		t.Fatalf("the fixture's Secret Manager client could not be built: %v", err)
	}
	f.Seed(testKeySecret, map[string]string{"fugaro": "managed"}, []byte(document))
	f.AllowAccess()
	x := &keyFixture{t: t, f: f, now: testNow}
	x.keys = &SecretManagerKeys{
		Project: testProject, Secret: testKeySecret, Service: svc,
		TTL: time.Minute, Now: func() time.Time { return x.now },
	}
	return x
}

// TestSecretManagerKeysReadsTheDocument: the happy path, and the shape of the
// read — the container's newest version, as this service's own identity.
func TestSecretManagerKeysReadsTheDocument(t *testing.T) {
	x := newKeyFixture(t, `{"deepgram":"`+testKeyValue+`","elevenlabs":"another-value"}`)
	key, err := x.keys.Key(context.Background(), "deepgram")
	if err != nil {
		t.Fatalf("the key could not be read: %v", err)
	}
	if key != testKeyValue {
		t.Errorf("the key is %q, want the value the document holds", key)
	}
	key, err = x.keys.Key(context.Background(), "elevenlabs")
	if err != nil || key != "another-value" {
		t.Errorf("the second member is %q (%v), want the document's other value", key, err)
	}
	// The key document is the same one the credentials service reads: this hop
	// reads a value back only at the two moments the operator's own commands
	// do, and the Terraform grant beside it is one accessor role.
	if got := x.f.Accessed(); len(got) != 1 || got[0] != testKeySecret {
		t.Errorf("the fake served the access call for %v, want exactly one read of %s: the document is read once and cached", got, testKeySecret)
	}
	if len(x.f.Deleted()) != 0 {
		t.Errorf("the read deleted %v", x.f.Deleted())
	}
}

// TestSecretManagerKeysCachesForTTL: a second call inside the TTL pays no round
// trip, and one past it reads again. The price of the cache — a rotated key
// takes up to the TTL to be used — is stated on the type; this is that the
// deadline is the one it states.
func TestSecretManagerKeysCachesForTTL(t *testing.T) {
	x := newKeyFixture(t, `{"deepgram":"`+testKeyValue+`"}`)
	for i := 0; i < 3; i++ {
		if _, err := x.keys.Key(context.Background(), "deepgram"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := x.f.Accessed(); len(got) != 1 {
		t.Errorf("three calls inside the TTL made %d reads, want 1", len(got))
	}
	x.now = x.now.Add(time.Minute)
	if _, err := x.keys.Key(context.Background(), "deepgram"); err != nil {
		t.Fatalf("the call past the TTL: %v", err)
	}
	if got := x.f.Accessed(); len(got) != 2 {
		t.Errorf("a call past the TTL made %d reads in total, want 2", len(got))
	}
}

// TestSecretManagerKeysRefusesAnAbsentMember: the document is missing a
// provider, which is the state an installation that opted in and set one key is
// in. The refusal names the ENGINE and not the document's contents.
func TestSecretManagerKeysRefusesAnAbsentMember(t *testing.T) {
	x := newKeyFixture(t, `{"deepgram":"`+testKeyValue+`"}`)
	_, err := x.keys.Key(context.Background(), "elevenlabs")
	if !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("the refusal is %v, want ErrKeyUnavailable", err)
	}
	if !strings.Contains(err.Error(), "elevenlabs") {
		t.Errorf("the refusal does not name the engine: %v", err)
	}
	if strings.Contains(err.Error(), testKeyValue) {
		t.Errorf("the refusal carries another provider's key: %v", err)
	}
	// An empty member is the same refusal: a key presented as a credential
	// would come back 401, which reads as the wrong key rather than as none.
	x2 := newKeyFixture(t, `{"deepgram":""}`)
	if _, err := x2.keys.Key(context.Background(), "deepgram"); !errors.Is(err, ErrKeyUnavailable) {
		t.Errorf("an empty member answered %v, want ErrKeyUnavailable", err)
	}
}

// TestSecretManagerKeysNeverQuotesTheDocument is the security half of this
// file. A document an operator pasted without quotes is the realistic way a key
// reaches a sentence: encoding/json's own message names the character it choked
// on, and the character a malformed KEY DOCUMENT chokes on is a byte of a live
// credential. So the refusal says what the document should be and nothing about
// what it contained — and neither does anything that wraps it.
func TestSecretManagerKeysNeverQuotesTheDocument(t *testing.T) {
	for _, document := range []string{
		// A bare key where an object belongs: json's own message would be
		// `invalid character 'n' looking for beginning of value`.
		testKeyValue,
		// An object whose value lost its quotes, one member in.
		`{"deepgram":` + testKeyValue + `}`,
		// Truncated mid-value.
		`{"deepgram":"` + testKeyValue[:8],
		// Two documents: the second would be counted as a member.
		`{"deepgram":"a"}{"elevenlabs":"b"}`,
		// A value of the wrong type.
		`{"deepgram":1}`,
		// Not an object at all.
		`["` + testKeyValue + `"]`,
		``,
	} {
		x := newKeyFixture(t, document)
		_, err := x.keys.Key(context.Background(), "deepgram")
		if err == nil {
			t.Errorf("the document %q was read as a key document", document)
			continue
		}
		if !errors.Is(err, ErrKeyUnavailable) {
			t.Errorf("the document %q answered %v, want ErrKeyUnavailable", document, err)
		}
		// Every prefix of the document that is a real byte of it must be
		// absent. The first few bytes are checked rather than the whole thing,
		// because a fragment is what a message quotes.
		for n := 4; n <= len(document); n++ {
			if strings.Contains(err.Error(), document[:n]) {
				t.Errorf("the refusal quotes %d bytes of the document: %v", n, err)
				break
			}
		}
		if strings.Contains(err.Error(), testKeyValue) {
			t.Errorf("the refusal carries a key-shaped value: %v", err)
		}
	}
}

// TestParseKeyDocumentRefusesAShapeItCannotRead: the shape rules on their own,
// without a fake, so a rule that changes is caught here first.
func TestParseKeyDocumentRefusesAShapeItCannotRead(t *testing.T) {
	for _, doc := range []string{``, `null`, `[]`, `"x"`, `{"a":1}`, `{"a":"b"}{"c":"d"}`, `{"a":"b"} trailing`, `{`, `{"a":}`} {
		if _, err := parseKeyDocument([]byte(doc)); err == nil {
			t.Errorf("parseKeyDocument(%q) parsed", doc)
		} else if !errors.Is(err, ErrKeyUnavailable) {
			t.Errorf("parseKeyDocument(%q) = %v, want ErrKeyUnavailable", doc, err)
		}
	}
	got, err := parseKeyDocument([]byte(`{"a":"b","c":""}`))
	if err != nil {
		t.Fatalf("a well-formed document was refused: %v", err)
	}
	if got["a"] != "b" {
		t.Errorf("the document decoded to %v", got)
	}
	// An empty document is not an empty object: a container an operator created
	// and never filled must refuse rather than answer "" for every engine.
	if _, err := parseKeyDocument([]byte(`{}`)); err != nil {
		t.Errorf("an empty object is a well-formed document and was refused: %v", err)
	}
}

// TestSecretManagerKeysWithoutAClientRefuses: a process built without a client
// or without a container name must refuse. It is the fail-closed answer at this
// level, and the alternative — an empty key presented to a provider — reads at
// the provider as this installation's credential being wrong.
func TestSecretManagerKeysWithoutAClientRefuses(t *testing.T) {
	for _, k := range []*SecretManagerKeys{
		{},
		{Project: testProject},
		{Secret: testKeySecret},
		{Project: testProject, Secret: testKeySecret},
	} {
		if _, err := k.Key(context.Background(), "deepgram"); !errors.Is(err, ErrKeyUnavailable) {
			t.Errorf("a key source without a client or a container answered %v, want ErrKeyUnavailable", err)
		}
	}
}

// TestSecretManagerKeysUnreadableContainerIsState: a container that is not there
// — the state an installation that opted in and set no key is in — is a state
// failure and not a refusal of the caller, and the message names the container
// and carries nothing of its value.
func TestSecretManagerKeysUnreadableContainerIsState(t *testing.T) {
	f := gcpfake.NewSecrets(t)
	f.AllowAccess()
	svc, err := secretmanager.NewService(context.Background(),
		option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/"))
	if err != nil {
		t.Fatalf("the client could not be built: %v", err)
	}
	keys := &SecretManagerKeys{Project: testProject, Secret: testKeySecret, Service: svc}
	_, err = keys.Key(context.Background(), "deepgram")
	if !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("a missing container answered %v, want ErrKeyUnavailable", err)
	}
	if !strings.Contains(err.Error(), testKeySecret) {
		t.Errorf("the refusal does not name the container, which is what an operator needs: %v", err)
	}
	// A container with no version at all: created and never filled.
	f.Seed("empty-container", map[string]string{"fugaro": "managed"}, nil)
	keys.Secret = "empty-container"
	if _, err := keys.Key(context.Background(), "deepgram"); !errors.Is(err, ErrKeyUnavailable) {
		t.Errorf("a container with no version answered %v, want ErrKeyUnavailable", err)
	}
	// A payload that is not base64: the read succeeded and the value is broken.
	f.Seed("broken-payload", map[string]string{"fugaro": "managed"}, []byte("x"))
	if _, err := keys.Key(context.Background(), "deepgram"); err == nil {
		t.Error("a container whose payload is not base64 was read")
	}
}

// TestKeyDocumentIsBounded: a container an operator filled with something else
// is not read into this process's memory. The bound is generous — the document
// is a handful of keys — and it is a bound.
func TestKeyDocumentIsBounded(t *testing.T) {
	if maxKeyDocumentBytes <= 0 || maxKeyDocumentBytes > 1<<20 {
		t.Errorf("the key document's bound is %d bytes: it is a handful of keys, and a bound that large is not one", maxKeyDocumentBytes)
	}
	big := `{"deepgram":"` + strings.Repeat("k", maxKeyDocumentBytes) + `"}`
	f := gcpfake.NewSecrets(t)
	f.AllowAccess()
	f.Seed(testKeySecret, map[string]string{"fugaro": "managed"}, []byte(big))
	svc, err := secretmanager.NewService(context.Background(),
		option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/"))
	if err != nil {
		t.Fatalf("the client could not be built: %v", err)
	}
	keys := &SecretManagerKeys{Project: testProject, Secret: testKeySecret, Service: svc}
	_, err = keys.Key(context.Background(), "deepgram")
	if !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("an oversized document answered %v, want ErrKeyUnavailable", err)
	}
	if strings.Contains(err.Error(), "kkkk") {
		t.Errorf("the refusal quotes the document: %v", err)
	}
}

// loadRegister reads the register in this binary, and stops the test when it
// does not load.
func loadRegister(t *testing.T) *Register {
	t.Helper()
	reg, err := LoadRegister()
	if err != nil {
		t.Fatalf("the register in this binary does not load: %v", err)
	}
	return reg
}
