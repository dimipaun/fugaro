package runner

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
)

// ProviderCredential is the credential func of a gateway route for p: it
// reads p's key from env (the runner's own environment, where the job
// mounted it as p.SecretEnv()) and nowhere else, so the agent, whose
// environment is built without it, never has it. It is checked now, before
// the gateway starts: a missing or too-short key (too short to redact
// safely) is an error that names the variable and never the value. The
// returned func hands the gateway the key per call; an error there means
// the call is not sent.
//
// The value is registered for redaction already, because the job lists the
// variable in SecretEnvsVar (addMountedSecrets); the second return is the
// value so a caller that builds its own redactor can add it.
func ProviderCredential(env []string, p config.ModelProvider) (cred func() (string, error), value string, err error) {
	name := p.SecretEnv()
	value = envLookup(env, name)
	switch {
	case p.Secret == "":
		return nil, "", errors.New("model provider has no secret")
	case value == "":
		return nil, "", fmt.Errorf("the key of model provider secret %s is not mounted (%s is not set in the runner environment): run fugaro secrets set %s", p.Secret, name, p.Secret)
	case !plainKey(value):
		return nil, "", fmt.Errorf("%s holds whitespace, a control character or a non-ASCII character: the key was pasted with extra text (re-run fugaro secrets set %s)", name, p.Secret)
	case len(value) < minSecretLen:
		return nil, "", fmt.Errorf("%s is shorter than %d bytes, so it cannot be redacted safely", name, minSecretLen)
	}
	return func() (string, error) { return value, nil }, value, nil
}

// plainKey reports whether s is printable ASCII without spaces, as every
// provider key is; a trailing newline or a space from a paste would make the
// header invalid or the redaction miss the real value.
func plainKey(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] >= 0x7f {
			return false
		}
	}
	return true
}
