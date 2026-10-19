package backend

import (
	"encoding/json"
	"fmt"
	"strings"
)

// launchSpecFields is LaunchSpec without its methods, so the methods below
// can print and marshal a copy without calling themselves.
type launchSpecFields LaunchSpec

// redactedValue is what a SecretEnv entry's value prints as.
const redactedValue = "<redacted>"

// redacted is a copy of s whose SecretEnv entries keep their names and lose
// their values: NAME=<redacted>, or <redacted> alone for an entry that is not
// NAME=value (its text may be a name run into a value). s is not changed.
func (s LaunchSpec) redacted() launchSpecFields {
	c := launchSpecFields(s)
	if s.SecretEnv != nil {
		c.SecretEnv = make([]string, len(s.SecretEnv))
		for i, kv := range s.SecretEnv {
			if name, _, ok := strings.Cut(kv, "="); ok && name != "" {
				c.SecretEnv[i] = name + "=" + redactedValue
			} else {
				c.SecretEnv[i] = redactedValue
			}
		}
	}
	return c
}

// Format prints the spec under any verb with SecretEnv's values redacted
// (LaunchSpec.SecretEnv: today the budget seal key, D15), so a debug line or
// an error that formats a spec cannot carry the key.
func (s LaunchSpec) Format(f fmt.State, verb rune) {
	fmt.Fprintf(f, fmt.FormatString(f, verb), s.redacted())
}

// String is the spec as %v prints it, SecretEnv's values redacted.
func (s LaunchSpec) String() string { return fmt.Sprintf("%+v", s.redacted()) }

// GoString is the spec as %#v prints it, SecretEnv's values redacted.
func (s LaunchSpec) GoString() string { return fmt.Sprintf("%#v", s.redacted()) }

// MarshalJSON marshals the spec with SecretEnv's values redacted: nothing
// reads a LaunchSpec back from JSON, so the redacted form loses nothing a
// reader needs.
func (s LaunchSpec) MarshalJSON() ([]byte, error) { return json.Marshal(s.redacted()) }

// envVarFields is EnvVar without its methods.
type envVarFields EnvVar

// redacted is a copy of v whose value is redactedValue when it is a secret.
func (v EnvVar) redacted() envVarFields {
	c := envVarFields(v)
	if v.Secret {
		c.Value = redactedValue
	}
	return c
}

// Format prints the entry under any verb with a secret value redacted, so a
// backend that formats what LaunchEnv returned cannot print the seal key.
func (v EnvVar) Format(f fmt.State, verb rune) {
	fmt.Fprintf(f, fmt.FormatString(f, verb), v.redacted())
}

// String is the entry as %v prints it, a secret value redacted.
func (v EnvVar) String() string { return fmt.Sprintf("%+v", v.redacted()) }

// MarshalJSON marshals the entry with a secret value redacted.
func (v EnvVar) MarshalJSON() ([]byte, error) { return json.Marshal(v.redacted()) }
