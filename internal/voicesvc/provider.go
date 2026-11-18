package voicesvc

// The provider call: which endpoint each engine is called at, how the call is
// authenticated, and where the credential comes from (design §4.4 point 4, §8;
// mobile plan Task P5.3.5).
//
// Two rules are the whole of this file, and both are §8's:
//
//   - **TLS to a named constant endpoint per provider name, never a URL from
//     the policy node.** Every address this service dials is a constant spelled
//     in the table below, so a policy node — or a register row, or a caller —
//     that carried an address would still reach nothing: the name selects an
//     entry of this table and the entry carries the URL. A name with no entry
//     is a refusal (the register gate), and a test holds each endpoint to
//     https and to its own vendor's host.
//
//   - **The key never leaves this service.** §4.4 point 4: the hop holds the
//     provider key in the installation's Secret Manager and the app never sees
//     it. It is read per call here (ProviderKeys), it is put in exactly one
//     header of exactly one request, and the code that writes the response
//     scrubs it back out of the bytes on the way to the caller (stream.go) —
//     because a provider that echoes the credential it was handed must not be
//     able to hand it to the phone through this service.
//
// The table is deliberately small and closed. §7's register is the owners'
// reading of three providers; OpenAI is not among them and is not here, because
// its terms could not be read on 2026-10-19 and a register that admitted an
// engine whose retention nobody read would be the opposite of what the register
// is for.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	secretmanager "google.golang.org/api/secretmanager/v1"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// The endpoints, one constant per engine, and the parameters this hop sends
// with each call. §8's first rule is why they are here and not in a policy node
// or in the register: a destination is this service's own text, and the two
// files a person edits (register.json, the policy node) carry names, switches
// and terms and never an address.
const (
	// deepgramListenEndpoint is Deepgram's streaming listen route with the
	// model §7 read the price of (Nova-3 Multilingual) and its automatic
	// language detection. The caller's audio is the request body and the
	// container it is in is what the Content-Type says, so no encoding or
	// sample-rate parameter is guessed at here.
	deepgramListenEndpoint = "https://api.deepgram.com/v1/listen?model=nova-3&language=multi&smart_format=true"

	// googleSynthesizeEndpoint is the Text-to-Speech v1 synthesize route.
	// Google's streaming AUDIO path is the v2 gRPC API and this service's
	// callers speak HTTP, so this hop serves the text direction for Google
	// only — which is what register.json's row says and what the register gate
	// enforces.
	googleSynthesizeEndpoint = "https://texttospeech.googleapis.com/v1/text:synthesize"

	// elevenLabsStreamEndpoint is the streaming route rather than the plain
	// one: this hop's answer is a stream, and the plain route holds the whole
	// clip before it answers.
	elevenLabsStreamEndpoint = "https://api.elevenlabs.io/v1/text-to-speech/" + elevenLabsVoiceID + "/stream?output_format=mp3_44100_128"

	// elevenLabsVoiceID is the voice the hop asks for, and elevenLabsModel is
	// the model that speaks the language below: §7 read no price, no language
	// and no voice for this provider, so both are this service's own pick and
	// Check 85 is where the owner replaces them.
	elevenLabsVoiceID = "21m00Tcm4TlvDq8ikWAM"
	elevenLabsModel   = "eleven_multilingual_v2"

	// speakLanguage and speakEncoding are what every TTS call asks Google for:
	// the language §7 read listed for this installation's users (ro-RO), and
	// MP3, which is what a phone plays without a codec of its own.
	speakLanguage = "ro-RO"
	speakEncoding = "MP3"
)

// cloudPlatformScope is the scope this service's own access token is minted
// with, for the one engine that is called as the installation's identity rather
// than with a provider key.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// credentialFrom says which credential fills the header below.
type credentialFrom int

const (
	// fromProviderKey is the engine's own key, out of the installation's
	// Secret Manager (ProviderKeys).
	fromProviderKey credentialFrom = iota
	// fromServiceAccount is this service's own Google identity: a provider
	// whose API an installation can already reach needs no key of its own, and
	// no secret of it exists.
	fromServiceAccount
)

// engineAuth is how one engine's request is authenticated.
type engineAuth struct {
	// header is the header the credential travels in.
	header string
	// scheme precedes the credential in that header, and is "" for a header
	// that carries it bare.
	scheme string
	// from says which credential it is.
	from credentialFrom
}

// engine is one name's call. The table below is the register's counterpart: the
// register says which names and directions this hop holds, and an engine says
// how each of those is reached.
type engine struct {
	// name is the register's name and the policy's name, matched exactly.
	name string
	// direction is which way the payload goes.
	direction Direction
	// endpoint is the constant URL above.
	endpoint string
	// auth is the credential this call presents.
	auth engineAuth
	// audio is whether the caller's body is the payload, forwarded as it
	// arrives, or this service writes one.
	audio bool
	// contentType is the Content-Type of a body this service writes.
	contentType string
	// payload builds that body, and is nil for the audio direction.
	payload func(text string) ([]byte, error)
}

// engineFor returns the engine for a name and a direction, and whether this hop
// holds one. A name whose register row does not serve the direction has no
// entry here either: TestRegisterAndEnginesAgree holds the two to one answer,
// so the gate and the table can never disagree about what this hop can do.
func engineFor(name string, dir Direction) (engine, bool) {
	for _, e := range engines {
		if e.name == name && e.direction == dir {
			return e, true
		}
	}
	return engine{}, false
}

// engines is every call this hop can make. Three entries, one per register row
// direction, and nothing else reaches a provider.
var engines = []engine{
	{
		name:        "deepgram",
		direction:   STT,
		endpoint:    deepgramListenEndpoint,
		auth:        engineAuth{header: HeaderAuth, scheme: "Token ", from: fromProviderKey},
		audio:       true,
		contentType: "",
	},
	{
		name:        "google",
		direction:   TTS,
		endpoint:    googleSynthesizeEndpoint,
		auth:        engineAuth{header: HeaderAuth, scheme: authScheme, from: fromServiceAccount},
		payload:     googleSynthesizePayload,
		contentType: contentTypeJSON,
	},
	{
		name:        "elevenlabs",
		direction:   TTS,
		endpoint:    elevenLabsStreamEndpoint,
		auth:        engineAuth{header: "xi-api-key", from: fromProviderKey},
		contentType: contentTypeJSON,
		payload:     elevenLabsPayload,
	},
}

// callInput is what one call needs to be written: the payload, and nothing a
// policy node or the register said about the provider.
type callInput struct {
	// audio is the caller's body, forwarded as it arrives (the STT direction),
	// and audioType and audioLength describe it: the media type is the
	// caller's, folded to its canonical spelling, and the length is what the
	// caller declared or -1 when it declared nothing.
	audio       io.Reader
	audioType   string
	audioLength int64
	// text is what is spoken (the TTS direction). It has already been bounded
	// and validated as UTF-8 by the request reader.
	text string
}

// request builds this engine's one outbound call. cred is the credential
// resolved for the engine's own auth.from, and the error is this tree's own:
// the two payload builders are called with text the reader has already bounded,
// so a failure here is a programming error rather than a caller's.
//
// Nothing about the request is copied from the caller except the payload: the
// method, the URL, the two headers and the body's media type are all written
// here, so a caller cannot add a header, extend the URL or name another host.
func (e engine) request(ctx context.Context, in callInput, cred string) (*http.Request, error) {
	var body io.Reader
	var contentType string
	length := int64(-1)
	switch {
	case e.audio:
		if in.audio == nil {
			return nil, fmt.Errorf("voicesvc: the engine %s (%s) forwards the caller's body and the request carries none", e.name, e.direction)
		}
		body, contentType, length = in.audio, in.audioType, in.audioLength
	case e.payload == nil:
		return nil, fmt.Errorf("voicesvc: the engine %s (%s) has no payload builder", e.name, e.direction)
	default:
		raw, err := e.payload(in.text)
		if err != nil {
			return nil, err
		}
		body, contentType, length = bytes.NewReader(raw), e.contentType, int64(len(raw))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = length
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(e.auth.header, e.auth.scheme+cred)
	return req, nil
}

// googleSynthesizePayload is Google's synthesize request: the text, the
// language, and the encoding the phone plays.
func googleSynthesizePayload(text string) ([]byte, error) {
	type input struct {
		Text string `json:"text"`
	}
	type voice struct {
		LanguageCode string `json:"languageCode"`
	}
	type audioConfig struct {
		AudioEncoding string `json:"audioEncoding"`
	}
	return json.Marshal(struct {
		Input       input       `json:"input"`
		Voice       voice       `json:"voice"`
		AudioConfig audioConfig `json:"audioConfig"`
	}{
		Input:       input{Text: text},
		Voice:       voice{LanguageCode: speakLanguage},
		AudioConfig: audioConfig{AudioEncoding: speakEncoding},
	})
}

// elevenLabsPayload is ElevenLabs' request: the text and the model, and no
// voice setting — the voice is in the endpoint's own path.
func elevenLabsPayload(text string) ([]byte, error) {
	return json.Marshal(struct {
		Text    string `json:"text"`
		ModelID string `json:"model_id"`
	}{Text: text, ModelID: elevenLabsModel})
}

// audioMediaType reads the caller's audio media type and answers it in its
// canonical spelling, or an error when it is not audio.
//
// The canonical spelling is the point and it is not tidiness: the value goes
// into a header of an outbound request, and rebuilding it from the PARSED media
// type means no byte of the caller's text reaches that header — no parameter,
// no newline, no second header. A type this service does not recognise as audio
// is refused rather than forwarded, because the provider would read the body as
// whatever the type said.
func audioMediaType(header string) (string, error) {
	mt, _, err := mime.ParseMediaType(header)
	if err != nil {
		return "", fmt.Errorf("the request carries no usable Content-Type for its audio: name the container it sends (audio/wav, audio/mp4, audio/ogg, audio/webm), which is what a provider reads the bytes by")
	}
	if !strings.HasPrefix(mt, "audio/") {
		return "", fmt.Errorf("the request's Content-Type is not an audio type: the audio direction forwards the body as audio, so the type is what a provider reads the bytes by")
	}
	return mt, nil
}

// ErrKeyUnavailable is a provider key this service could not read or does not
// hold. It is state and not a refusal of the caller: the app's answer is the
// same (§4.5, local), and an operator's answer is that the installation's
// provider key is missing, unreadable or does not name this engine.
var ErrKeyUnavailable = errors.New("voicesvc: the provider key could not be read")

// ProviderKeys is where a provider's credential comes from. It is an interface so
// this package's tests need no Secret Manager and no cloud (Global Constraint
// 10), and the production implementation is SecretManagerKeys below.
type ProviderKeys interface {
	// Key returns the key for one register name. An implementation refuses —
	// with ErrKeyUnavailable — rather than answering an empty key: an empty
	// key would be presented to the provider as a credential, and the provider
	// would answer 401, which reads as the wrong failure.
	Key(ctx context.Context, provider string) (string, error)
}

// TokenSource mints this service's own access token, for the engine that is
// called as the installation's identity rather than with a provider key.
type TokenSource interface {
	// Token returns a bearer token for this service's own service account.
	Token(ctx context.Context) (string, error)
}

// GoogleTokenSource mints this service's own access token from the identity
// Cloud Run already runs it as (voice.tf's account, whose only grants are the
// two reads voice.tf spells out).
func GoogleTokenSource(ctx context.Context) (TokenSource, error) {
	src, err := google.DefaultTokenSource(ctx, cloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("voicesvc: this service's own identity is unavailable, so the engine called as the installation cannot be reached: %w", err)
	}
	return serviceAccountToken{src: src}, nil
}

// serviceAccountToken adapts an oauth2 token source to TokenSource.
type serviceAccountToken struct{ src oauth2.TokenSource }

func (s serviceAccountToken) Token(ctx context.Context) (string, error) {
	tok, err := s.src.Token()
	if err != nil {
		return "", fmt.Errorf("voicesvc: this service's own access token could not be minted: %w", err)
	}
	return tok.AccessToken, nil
}

// providerKeyTTL is how long a provider-key document read is reused. A call
// would otherwise pay a Secret Manager round trip before it reaches the
// provider, on a path whose whole point is the end of a spoken turn (D46); the
// price is that a rotated key takes up to this long to be used, and that is
// stated here rather than discovered.
const providerKeyTTL = 5 * time.Minute

// maxKeyDocumentBytes bounds the provider-key document. It is three keys of a
// few hundred bytes at most, and the bound is what keeps a container an
// operator filled with something else from being read into this process's
// memory.
const maxKeyDocumentBytes = 64 << 10

// SecretManagerKeys reads the installation's provider-key document out of
// Secret Manager, as this service's own identity.
//
// The document is a JSON object of register names to keys —
// `{"deepgram":"…","elevenlabs":"…"}` — and it is one container for every
// provider rather than one per provider, so a provider added to the register is
// a member added to a value the operator already sets and not a new resource,
// a new grant and a new environment name. The container and its one accessor
// grant are in deploy/terraform/gcp/modules/installation/secrets.tf beside the
// installation's other secrets; the environment names the container's resource
// name (internal/cli/voice_serve.go).
//
// It is safe for concurrent use, and it caches the parsed document for TTL: a
// call reads Secret Manager at most once per TTL, and a read that FAILED is not
// cached — a container that comes back is picked up on the next call, and a
// container that is still broken is answered with its own failure rather than
// with a stale document.
type SecretManagerKeys struct {
	// Project is the installation's GCP project.
	Project string
	// Secret is the container's ID.
	Secret string
	// Service is the Secret Manager client. nil is a source that refuses:
	// a process built without one must not read an empty key.
	Service *secretmanager.Service
	// TTL is how long a parsed document is served. 0 is providerKeyTTL.
	TTL time.Duration
	// Now is the clock. nil is time.Now.
	Now func() time.Time

	mu     sync.Mutex
	keys   map[string]string
	readAt time.Time
}

func (k *SecretManagerKeys) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

func (k *SecretManagerKeys) ttl() time.Duration {
	if k.TTL > 0 {
		return k.TTL
	}
	return providerKeyTTL
}

// Key answers one register name's key.
func (k *SecretManagerKeys) Key(ctx context.Context, provider string) (string, error) {
	keys, err := k.document(ctx)
	if err != nil {
		return "", err
	}
	key, ok := keys[provider]
	if !ok || key == "" {
		// The name is the register's own (the handler checked it before it got
		// here), so naming it is safe and is what an operator needs: the
		// document is missing a member, not the value of one.
		return "", fmt.Errorf("%w: the installation's provider-key document names no key for %s, so this service cannot make a call to that engine", ErrKeyUnavailable, provider)
	}
	return key, nil
}

// document returns the parsed provider-key document, reading it when the cached
// one has expired.
func (k *SecretManagerKeys) document(ctx context.Context) (map[string]string, error) {
	k.mu.Lock()
	if k.keys != nil && k.now().Before(k.readAt.Add(k.ttl())) {
		keys := k.keys
		k.mu.Unlock()
		return keys, nil
	}
	k.mu.Unlock()
	raw, err := k.read(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := parseKeyDocument(raw)
	if err != nil {
		return nil, err
	}
	k.mu.Lock()
	k.keys, k.readAt = keys, k.now()
	k.mu.Unlock()
	return keys, nil
}

// read reads the container's newest version as this service's own identity:
// projects/<project>/secrets/<secret>/versions/latest:access, the one Secret
// Manager permission the hop holds (Global Constraint 12's shape, one service
// over).
func (k *SecretManagerKeys) read(ctx context.Context) ([]byte, error) {
	if k.Service == nil || k.Project == "" || k.Secret == "" {
		return nil, fmt.Errorf("%w: this service has no Secret Manager client or no provider-key container to read", ErrKeyUnavailable)
	}
	name := "projects/" + k.Project + "/secrets/" + k.Secret + "/versions/latest"
	r, err := k.Service.Projects.Secrets.Versions.Access(name).Context(ctx).Do()
	if err != nil {
		// The error is Secret Manager's own. It names the container — a name
		// and not a value — and carries no byte of the secret, because a
		// refusal to read a value cannot quote one.
		return nil, fmt.Errorf("%w: reading the provider-key container %s: %v", ErrKeyUnavailable, k.Secret, err)
	}
	if r.Payload == nil || r.Payload.Data == "" {
		return nil, fmt.Errorf("%w: the provider-key container %s has no version with a value", ErrKeyUnavailable, k.Secret)
	}
	raw, err := base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: the provider-key container's payload is not base64", ErrKeyUnavailable)
	}
	if len(raw) > maxKeyDocumentBytes {
		return nil, fmt.Errorf("%w: the provider-key container holds more than this service's bound of %d bytes", ErrKeyUnavailable, maxKeyDocumentBytes)
	}
	return raw, nil
}

// parseKeyDocument decodes the provider-key document strictly, and never
// repeats the decoder's own sentence.
//
// That is the whole of why this function exists rather than a `json.Unmarshal`
// at its one call site: a syntax error from encoding/json names the character
// it choked on ("invalid character 's' looking for beginning of value"), and
// the character a malformed KEY DOCUMENT chokes on is a byte of a credential —
// a document an operator pasted without quotes would put the first byte of a
// live key into a sentence that this service logs and answers with. The
// refusal below says what the document should be and nothing about what it
// contained.
func parseKeyDocument(raw []byte) (map[string]string, error) {
	// The members are the register's names, so this decode is not strict about
	// WHICH names it holds: the set of engines is the register's (and a
	// document may carry a key for one a later release adds), and the gate that
	// decides which names exist is the register. What is strict is the shape —
	// an object of names to strings, one document, and nothing else.
	dec := json.NewDecoder(bytes.NewReader(raw))
	var keys map[string]string
	if err := dec.Decode(&keys); err != nil {
		return nil, fmt.Errorf("%w: the installation's provider-key container does not hold a JSON object of provider names to keys", ErrKeyUnavailable)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: the installation's provider-key container holds more than one document", ErrKeyUnavailable)
	}
	if keys == nil {
		return nil, fmt.Errorf("%w: the installation's provider-key container holds no object of provider names to keys", ErrKeyUnavailable)
	}
	return keys, nil
}
