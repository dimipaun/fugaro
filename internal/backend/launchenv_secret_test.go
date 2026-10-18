package backend

import (
	"encoding/json"
	"fmt"
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

// TestLaunchSpecNeverPrintsASecretValue: a LaunchSpec printed with any fmt
// verb, or marshalled to JSON, shows a SecretEnv entry's name and never its
// value (the budget seal key, D15), so a future debug line or error that
// formats a spec cannot leak it. Env's values, which are not secrets, still
// print, and the spec itself is unchanged.
func TestLaunchSpecNeverPrintsASecretValue(t *testing.T) {
	secret := strings.Repeat("S", 43)
	spec := LaunchSpec{RunID: "r1", Env: []string{"PLAIN=visible"}, SecretEnv: []string{"FUGARO_BUDGET_TOKEN=" + secret, "MALFORMED" + secret}}
	js, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	outs := map[string]string{
		"%v": fmt.Sprintf("%v", spec), "%+v": fmt.Sprintf("%+v", spec), "%#v": fmt.Sprintf("%#v", spec), "%s": fmt.Sprintf("%s", spec),
		"&%+v": fmt.Sprintf("%+v", &spec), "String": spec.String(), "GoString": spec.GoString(), "json": string(js),
		"nested": fmt.Sprintf("%+v", struct{ S LaunchSpec }{spec}),
	}
	for how, out := range outs {
		if strings.Contains(out, secret) {
			t.Errorf("%s prints the secret value: %s", how, out)
		}
		if !strings.Contains(out, "FUGARO_BUDGET_TOKEN") || !strings.Contains(out, "visible") || !strings.Contains(out, "r1") {
			t.Errorf("%s lost what is not secret: %s", how, out)
		}
	}
	if spec.SecretEnv[0] != "FUGARO_BUDGET_TOKEN="+secret {
		t.Fatal("formatting changed the spec")
	}
}

// TestASecretEnvVarNeverPrintsItsValue: LaunchEnv's entry for a SecretEnv
// value (Secret true) prints its name and never its value under any verb or
// as JSON, alone or in the slice LaunchEnv returns; a plain entry prints as
// before.
func TestASecretEnvVarNeverPrintsItsValue(t *testing.T) {
	secret := secretValue()
	vars, err := LaunchEnv(LaunchSpec{Env: []string{"PLAIN=visible"}, SecretEnv: []string{"FUGARO_BUDGET_TOKEN=" + secret}})
	if err != nil || len(vars) != 2 || !vars[1].Secret {
		t.Fatalf("LaunchEnv = %+v, %v", vars, err)
	}
	js, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	for how, out := range map[string]string{
		"%v": fmt.Sprintf("%v", vars), "%+v": fmt.Sprintf("%+v", vars), "%#v": fmt.Sprintf("%#v", vars), "%s": fmt.Sprintf("%s", vars[1]),
		"&": fmt.Sprintf("%+v", &vars[1]), "String": vars[1].String(), "json": string(js),
	} {
		if strings.Contains(out, secret) {
			t.Errorf("%s prints the secret value: %s", how, out)
		}
		if !strings.Contains(out, "FUGARO_BUDGET_TOKEN") || !strings.Contains(out, "visible") && how != "%s" && how != "&" && how != "String" {
			t.Errorf("%s lost what is not secret: %s", how, out)
		}
	}
	if vars[1].Value != secret {
		t.Fatal("formatting changed the entry")
	}
}
