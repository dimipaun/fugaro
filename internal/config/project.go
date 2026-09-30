package config

import (
	"bytes"
	"errors"
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
// belongs to. "" when absent. An error only for YAML that doesn't decode.
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
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value == "project" && v.Kind == yaml.ScalarNode {
			return v.Value, nil
		}
	}
	return "", nil
}
