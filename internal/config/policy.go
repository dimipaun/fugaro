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

// PolicyOf reads only the policy keys of a fugaro.yaml: unknown keys
// elsewhere and invalid values outside the policy are ignored, as ProjectOf
// does. The policy blocks themselves are strict: budget: and the agent:
// block (whose unknown keys are refused, so a misspelt max_run_tokens can't
// pass for "no limit") are checked for unknown keys, and the policy values
// are validated. An error means the file's policy is unusable.
func PolicyOf(data []byte) (Policy, error) {
	var root yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return Policy{}, nil
		}
		return Policy{}, fmt.Errorf("policy: %w", err)
	}
	var budgetNode, agentNode *yaml.Node
	if len(root.Content) == 1 && root.Content[0].Kind == yaml.MappingNode {
		m := root.Content[0]
		for i := 0; i+1 < len(m.Content); i += 2 {
			k, v := m.Content[i].Value, m.Content[i+1]
			switch k {
			case "budget", "agent":
				dst := &budgetNode
				if k == "agent" {
					dst = &agentNode
				}
				if *dst != nil {
					return Policy{}, fmt.Errorf("policy: %s: the key is defined twice", k)
				}
				*dst = v
			}
		}
	}

	// The policy blocks are strict: a misspelt key is an error, never "no
	// policy". Everything else in the file is the file's own business.
	var budget *Budget
	if budgetNode != nil && !isNull(budgetNode) {
		if budgetNode.Kind != yaml.MappingNode {
			return Policy{}, fmt.Errorf("policy: budget: must be a mapping")
		}
		budget = &Budget{}
		if err := strictDecode(budgetNode, budget); err != nil {
			return Policy{}, fmt.Errorf("policy: budget: %w", err)
		}
	}
	var a Agent
	if agentNode != nil && !isNull(agentNode) {
		if agentNode.Kind != yaml.MappingNode {
			return Policy{}, fmt.Errorf("policy: agent: must be a mapping")
		}
		// Only unknown keys are refused here: values of the agent's other
		// keys are not the policy's to judge.
		if err := strictDecode(agentNode, &a); err != nil {
			var te *yaml.TypeError
			if !errors.As(err, &te) {
				return Policy{}, fmt.Errorf("policy: agent: %w", err)
			}
			for _, m := range te.Errors {
				if strings.Contains(m, "not found in type") {
					return Policy{}, fmt.Errorf("policy: agent: %s", m)
				}
			}
		}
		// Re-read the two policy keys alone, so a bad value elsewhere in
		// agent: can't leak into them.
		a = Agent{}
		var only struct {
			MaxRunTokens    int64      `yaml:"max_run_tokens"`
			MaxOutputTokens RoleTokens `yaml:"max_output_tokens"`
		}
		sub := &yaml.Node{Kind: yaml.MappingNode}
		for i := 0; i+1 < len(agentNode.Content); i += 2 {
			if k := agentNode.Content[i].Value; k == "max_run_tokens" || k == "max_output_tokens" {
				sub.Content = append(sub.Content, agentNode.Content[i], agentNode.Content[i+1])
			}
		}
		if err := sub.Decode(&only); err != nil {
			return Policy{}, fmt.Errorf("policy: agent: %w", err)
		}
		a.MaxRunTokens, a.MaxOutputTokens = only.MaxRunTokens, only.MaxOutputTokens
	}

	ps := append(validateBudget(budget), validateAgentTokens(a)...)
	if len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return Policy{}, fmt.Errorf("policy: %s", strings.Join(msgs, "; "))
	}
	p := Policy{MaxRunTokens: a.MaxRunTokens, MaxOutputTokens: a.MaxOutputTokens}
	if b := budget; b != nil {
		p.Mode, p.PerRunUSD, p.AllowedModels = b.Mode, b.PerRunUSD, b.AllowedModels
	}
	return p, nil
}

func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

// strictDecode decodes n into v, refusing keys v doesn't have.
func strictDecode(n *yaml.Node, v any) error {
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode(v)
}
