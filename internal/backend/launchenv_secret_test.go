package backend

import (
	"strings"
	"testing"
)

// secretValue is a value built at run time, never a literal that looks like a
// credential: the tests only need a string they can search for.
func secretValue() string { return strings.Repeat("k", 43) }

// TestLaunchEnvCarriesSecretEntriesAndNeverQuotesThem: SecretEnv's entries
// come back from LaunchEnv beside Env's, marked Secret, so a backend can tell
// which values it must not put where a wider audience reads them; and no
// refusal of a secret entry quotes its value (the launcher's seal key is
// the one producer, D15).
func TestLaunchEnvCarriesSecretEntriesAndNeverQuotesThem(t *testing.T) {
	v := secretValue()
	got, err := LaunchEnv(LaunchSpec{Env: []string{"PLAIN=1"}, SecretEnv: []string{"FUGARO_BUDGET_TOKEN=" + v}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (EnvVar{Name: "PLAIN", Value: "1"}) || got[1] != (EnvVar{Name: "FUGARO_BUDGET_TOKEN", Value: v, Secret: true}) {
		t.Fatalf("LaunchEnv = %+v", got)
	}
	for name, spec := range map[string]LaunchSpec{
		"not NAME=value":     {SecretEnv: []string{v}},
		"an empty name":      {SecretEnv: []string{"=" + v}},
		"a reserved name":    {SecretEnv: []string{"FUGARO_RUN=" + v}},
		"named in Env too":   {Env: []string{"FUGARO_BUDGET_TOKEN=1"}, SecretEnv: []string{"FUGARO_BUDGET_TOKEN=" + v}},
		"named twice as one": {SecretEnv: []string{"FUGARO_BUDGET_TOKEN=" + v, "FUGARO_BUDGET_TOKEN=" + v}},
		// A launcher bug that runs the name into the value, in either list:
		// the refusal must not quote the entry, which carries the value.
		"plain, no =":  {Env: []string{"FUGARO_BUDGET_TOKEN" + v}},
		"secret, no =": {SecretEnv: []string{"FUGARO_BUDGET_TOKEN" + v}},
	} {
		_, err := LaunchEnv(spec, "FUGARO_RUN")
		if err == nil {
			t.Errorf("%s: LaunchEnv accepted %+v", name, spec)
			continue
		}
		if strings.Contains(err.Error(), v) {
			t.Errorf("%s: the refusal quotes the secret's value: %v", name, err)
		}
	}
}
