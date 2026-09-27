package config

import _ "embed"

// Example is the annotated fugaro.yaml template printed by `fugaro config example`.
//
//go:embed example.yaml
var Example []byte
