package config

import (
	"bytes"
	_ "embed"
)

// Example is the annotated fugaro.yaml template printed by `fugaro config example`,
// naming the placeholder project "example".
//
//go:embed example.yaml
var Example []byte

const exampleProjectLines = "# the Fugaro project; fugaro init --config-only writes your project config\nproject: example\n"

// ExampleFor is Example naming the given project. Anything that is not a
// project name (ProjectNameRE) leaves the placeholder "example", so the
// output never carries a name that validation would refuse.
func ExampleFor(project string) []byte {
	if !ProjectNameRE.MatchString(project) {
		return Example
	}
	return bytes.Replace(Example, []byte(exampleProjectLines), []byte("project: "+project+"\n"), 1)
}
