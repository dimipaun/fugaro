package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Policy is the cost and model policy a fugaro.yaml sets: the budget: block
// plus agent.max_run_tokens and agent.max_output_tokens. A zero field sets
// nothing (0 is "no value", not "no cap"), so a file without these keys is
// the zero Policy.
type Policy struct {
	Mode            string
	PerRunUSD       float64
	MaxRunTokens    int64
	MaxOutputTokens RoleTokens
	AllowedModels   []string
}

// PolicyOf reads only the policy keys of a fugaro.yaml, leniently: unknown
// keys elsewhere and invalid values outside the policy are ignored, as
// ProjectOf does. The policy keys themselves are validated; an error means
// the file's policy is unusable (it does not decode, or breaks a rule).
func PolicyOf(data []byte) (Policy, error) {
	var doc struct {
		Budget *Budget `yaml:"budget"`
		Agent  struct {
			MaxRunTokens    int64      `yaml:"max_run_tokens"`
			MaxOutputTokens RoleTokens `yaml:"max_output_tokens"`
		} `yaml:"agent"`
	}
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return Policy{}, nil
		}
		return Policy{}, fmt.Errorf("policy: %w", err)
	}
	a := Agent{MaxRunTokens: doc.Agent.MaxRunTokens, MaxOutputTokens: doc.Agent.MaxOutputTokens}
	ps := append(validateBudget(doc.Budget), validateAgentTokens(a)...)
	if len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return Policy{}, fmt.Errorf("policy: %s", strings.Join(msgs, "; "))
	}
	p := Policy{MaxRunTokens: a.MaxRunTokens, MaxOutputTokens: a.MaxOutputTokens}
	if b := doc.Budget; b != nil {
		p.Mode, p.PerRunUSD, p.AllowedModels = b.Mode, b.PerRunUSD, b.AllowedModels
	}
	return p, nil
}
