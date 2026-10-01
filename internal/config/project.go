package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"

	"gopkg.in/yaml.v3"
)

// ProjectNameRE is a Fugaro project name: 1 to 40 characters of a-z, 0-9
// and '-', starting and ending with a letter or digit. It is the one rule
// for the name in fugaro.yaml, the project configs and the flags.
var ProjectNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// ProjectOf reads fugaro.yaml's top-level project: leniently (unknown and
// invalid fields ignored), so a broken config still says which project it
// belongs to. "" when absent or null. An error for YAML that doesn't
// decode, a project: that isn't a string, or two project: keys.
func ProjectOf(data []byte) (string, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	if len(doc.Content) == 0 {
		return "", nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", errors.New("fugaro.yaml is not a mapping")
	}
	project, found := "", false
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.Value != "project" {
			continue
		}
		if found {
			return "", fmt.Errorf("line %d: a second project:", k.Line)
		}
		found = true
		switch {
		case v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null":
			// project: with no value names none.
		case v.Kind == yaml.ScalarNode && v.ShortTag() == "!!str":
			project = v.Value
		default:
			return "", fmt.Errorf("line %d: project: must be a string (a project name)", v.Line)
		}
	}
	return project, nil
}
