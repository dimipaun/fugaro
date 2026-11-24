package config

// The `file` field of a secret entry (C1, D3327, F19): a secret a run should
// see as a PATH rather than as an environment variable's value. It is the
// repository's own declaration — the runner writes the value to a 0400 file and
// sets the variable to that path (IS2) — so what this file holds is the two
// things the declaration must say: it is a boolean, and it still names the
// variable that receives the path.

import (
	"strings"
	"testing"
)

// secretFileHead is the smallest repository whose one workflow declares
// secrets.
const secretFileHead = minimalRepo + "git: { provider: github }\nworkflows:\n  web:\n    commands: { build: make, test: make test }\n    secrets:\n"

func TestSecretFileRequiresEnv(t *testing.T) {
	t.Run("a file secret parses", func(t *testing.T) {
		cfg, problems := Parse([]byte(secretFileHead + "      - { name: deploy-token, env: DEPLOY_TOKEN, file: true }\n"))
		if len(problems) > 0 {
			t.Fatalf("the decoder refused a file secret: %v", problems)
		}
		s := cfg.Workflows["web"].Secrets
		if len(s) != 1 {
			t.Fatalf("the workflow declares %d secrets, want 1", len(s))
		}
		if !s[0].File {
			t.Errorf("the secret's File is false: a file secret is the whole of what this key declares")
		}
		if s[0].Name != "deploy-token" || s[0].Env != "DEPLOY_TOKEN" {
			t.Errorf("the secret reads as %+v, want name deploy-token under DEPLOY_TOKEN", s[0])
		}
	})

	t.Run("an ordinary secret is not a file", func(t *testing.T) {
		cfg, problems := Parse([]byte(secretFileHead + "      - { name: deploy-token, env: DEPLOY_TOKEN }\n"))
		if len(problems) > 0 {
			t.Fatalf("unexpected problems: %v", problems)
		}
		if s := cfg.Workflows["web"].Secrets[0]; s.File {
			t.Error("a secret that names no file is read as a file secret")
		}
	})

	t.Run("a file secret still names its variable", func(t *testing.T) {
		// The variable is where the PATH goes, so it is the one field a file
		// secret may not leave out: an entry with no variable would leave the
		// value on a disk nothing points at.
		_, problems := Parse([]byte(secretFileHead + "      - { name: deploy-token, file: true }\n"))
		if !hasProblem(problems, "workflows.web.secrets[0].env", "receives the path", 0) {
			t.Fatalf("a file secret with no env: %v", problems)
		}
	})

	t.Run("file is a boolean", func(t *testing.T) {
		// The strict decoder refuses a string where a boolean goes, so a
		// fugaro.yaml that writes file: "true" is a typo rather than a file
		// secret (the schema refuses it too: TestSchemaSecretFile).
		_, problems := Parse([]byte(secretFileHead + "      - { name: deploy-token, env: DEPLOY_TOKEN, file: \"true\" }\n"))
		if len(problems) == 0 {
			t.Fatal("the decoder read a string as a boolean")
		}
	})
}

func TestSchemaSecretFile(t *testing.T) {
	sch := compileABSchema(t, "fugaro.schema.json")
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"a file secret", "      - { name: deploy-token, env: DEPLOY_TOKEN, file: true }\n", true},
		{"an ordinary secret", "      - { name: deploy-token, env: DEPLOY_TOKEN }\n", true},
		{"file false", "      - { name: deploy-token, env: DEPLOY_TOKEN, file: false }\n", true},
		// The two surfaces have to agree: the schema is what an editor and a
		// repository's CI check the file against, and a key it accepted while
		// the decoder refused it would be a red check nobody can reproduce.
		{"a string where the boolean goes", "      - { name: deploy-token, env: DEPLOY_TOKEN, file: \"true\" }\n", false},
		{"a file secret with no env", "      - { name: deploy-token, file: true }\n", false},
		{"a key the entry does not have", "      - { name: deploy-token, env: DEPLOY_TOKEN, path: /run/secrets/token }\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := abSchemaAccepts(t, sch, secretFileHead+tc.body); got != tc.ok {
				t.Errorf("the schema accepted=%v, want %v", got, tc.ok)
			}
			_, problems := Parse([]byte(secretFileHead + tc.body))
			if (len(problems) == 0) != tc.ok {
				t.Errorf("the decoder accepted=%v, want %v: %v", len(problems) == 0, tc.ok, problems)
			}
		})
	}
}

// The refusal's own sentence is pinned above by its fragment; what this holds
// is that nothing in it repeats a value the file wrote (the no-echo rule the
// service's refusals keep too).
func TestSecretFileRefusalEchoesNoValue(t *testing.T) {
	_, problems := Parse([]byte(secretFileHead + "      - { name: deploy-token, file: true }\n"))
	for _, p := range problems {
		if strings.Contains(p.Message, "deploy-token") {
			t.Errorf("the refusal repeats what the file wrote: %s", p.Message)
		}
	}
}
