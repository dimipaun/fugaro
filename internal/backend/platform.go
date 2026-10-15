package backend

// Platform says where a process runs: the backend whose own signal claims
// it, or the local invocation (design compute-backend.md §2.5).
//
// Callers ask two different questions and must not confuse them:
//
//   - InCloud asks "is a launcher controlling this process?". It fails
//     closed: everything that is not the local invocation is in the cloud,
//     including a platform no backend claims. A run's security checks (the
//     routing-refusal escape knob, RequireProject, refuseOldJobEnv, the
//     loopback budget backend) read this, so an unrecognised or stripped
//     platform refuses rather than permits.
//   - Backend and Codec ask which platform claimed the process, for names
//     and for prices. A process no backend claims has no codec, so it has
//     no execution name either.
type Platform struct {
	// local is the invocation's own answer, and only ever set by
	// LocalPlatform: local mode is named by the invocation, never inferred
	// from a missing marker.
	local bool
	// codec is the backend whose platform signal claims this process, nil
	// when none does. It is LocalCodec for a local invocation.
	codec Codec
}

// LocalPlatform is a local invocation's platform. `fugaro exec
// --task-file` is the CLI's one local invocation, and it names the local
// case itself: a process with no marker is in the cloud, not local.
func LocalPlatform() Platform { return Platform{local: true, codec: LocalCodec()} }

// DetectPlatform resolves a process that is not a local invocation: the
// first backend whose own signal claims it, else none. A process no
// backend claims is still in the cloud (InCloud fails closed), and its
// codec is nil, so it has no name and no backend to price it.
func DetectPlatform(getenv func(string) string) Platform {
	for _, c := range codecs() {
		if c.On(getenv) {
			return Platform{codec: c}
		}
	}
	return Platform{}
}

// Local reports whether the invocation named this process local.
func (p Platform) Local() bool { return p.local }

// InCloud reports whether this process runs somewhere a launcher controls.
// It is the fail-closed answer: true for every process the invocation did
// not name local, whether or not a backend recognises its platform.
func (p Platform) InCloud() bool { return !p.local }

// Backend is the name of the backend that claims this process, Local for a
// local invocation, and "" when no backend claims it.
func (p Platform) Backend() string {
	if p.codec == nil {
		return ""
	}
	return p.codec.Backend()
}

// Codec is the claiming backend's codec, nil when none claims the process.
func (p Platform) Codec() Codec { return p.codec }

// ExecutionFromEnv is the canonical name of the run this process is, or ""
// when no backend claims it (a local invocation included). An error means
// the platform claimed the process but the job did not carry the names the
// backend needs.
func (p Platform) ExecutionFromEnv(getenv func(string) string) (string, error) {
	if p.codec == nil {
		return "", nil
	}
	return p.codec.FromEnv(getenv)
}

// OnPlatform reports whether some platform's own signal claims this
// process. It is the question whose answer is cosmetic -- the skill and
// update nudges stay quiet inside a job -- so unlike InCloud it does not
// fail closed: a local `fugaro ls` carries no signal and must keep
// nudging. A backend added to codecs is recognised here too, which is what
// keeps a Batch VM quiet.
func OnPlatform(getenv func(string) string) bool {
	return DetectPlatform(getenv).codec != nil
}
