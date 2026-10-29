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

// ProviderOf reads fugaro.yaml's git.provider leniently (like ProjectOf), ""
// when the git: block or the key is absent. It does not validate the value.
//
// It is the same reader ProjectOf and GCPProjectOf are, for the same reason:
// the git provider is one of the three things a checkout says about itself
// that a caller needs before the file is known to be valid (the other two
// being the project and the GCP project), and a file that is otherwise
// broken still names the host its repository lives on. A checkout that is
// not a checkout's own fugaro.yaml is not read here; the caller decides
// whether the value is one config.Providers carries.
func ProviderOf(data []byte) (string, error) {
	return nestedString(data, "git", "provider", "a git provider")
}

// topLevelString reads the top-level string key of a fugaro.yaml. what is
// the wording ("a project name", "a GCP project ID") of the error for a
// value that is not a string.
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

// nestedString is topLevelString for a key inside a top-level block: it
// reads <block>.<key> the same lenient way. A block that is absent or is not
// a mapping names no value, which is not an error (the strict decode is what
// refuses a malformed git: block, where the file is read as a whole).
func nestedString(data []byte, block, key, what string) (string, error) {
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
		if k.Kind != yaml.ScalarNode || k.Value != block {
			continue
		}
		if v.Kind != yaml.MappingNode {
			return "", nil
		}
		val, found := "", false
		for j := 0; j+1 < len(v.Content); j += 2 {
			fk, fv := v.Content[j], v.Content[j+1]
			if fk.Kind != yaml.ScalarNode || fk.Value != key {
				continue
			}
			if found {
				return "", fmt.Errorf("line %d: a second %s.%s:", fk.Line, block, key)
			}
			found = true
			switch {
			case fv.Kind == yaml.ScalarNode && fv.ShortTag() == "!!null":
				// key: with no value names none.
			case fv.Kind == yaml.ScalarNode && fv.ShortTag() == "!!str":
				val = fv.Value
			default:
				return "", fmt.Errorf("line %d: %s.%s: must be a string (%s)", fv.Line, block, key, what)
			}
		}
		return val, nil
	}
	return "", nil
}
