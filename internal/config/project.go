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

// GCPProjectRE is the shape of a GCP project ID (the same pattern infra.ValidProjectID uses).
var GCPProjectRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// ProjectOf reads fugaro.yaml's top-level project: leniently (unknown and
// invalid fields ignored), so a broken config still says which project it
// belongs to. "" when absent or null. An error for YAML that doesn't
// decode, a project: that isn't a string, or two project: keys.
func ProjectOf(data []byte) (string, error) {
	return topLevelString(data, "project", "a project name")
}

// GCPProjectOf reads the top-level gcp_project: of a fugaro.yaml leniently
// (like ProjectOf), "" when absent. It does not validate the value.
func GCPProjectOf(data []byte) (string, error) {
	return topLevelString(data, "gcp_project", "a GCP project ID")
}

// topLevelString reads the top-level string key of a fugaro.yaml; what is
// "a project name"-style wording for the not-a-string error.
func topLevelString(data []byte, key, what string) (string, error) {
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
	val, found := "", false
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.Value != key {
			continue
		}
		if found {
			return "", fmt.Errorf("line %d: a second %s:", k.Line, key)
		}
		found = true
		switch {
		case v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null":
			// key: with no value names none.
		case v.Kind == yaml.ScalarNode && v.ShortTag() == "!!str":
			val = v.Value
		default:
			return "", fmt.Errorf("line %d: %s: must be a string (%s)", v.Line, key, what)
		}
	}
	return val, nil
}
