package main

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
)

func main() {
	const doc = `version: 1
project: aurora
gcp_project: aurora-prod
defaults:
  agent:
    levels:
      l2:
        model: none
      l3:
        none: true
`
	l, ps := config.ParseProjectLayer([]byte(doc), config.LayerAnchor{})
	fmt.Printf("problems=%v\n", ps)
	if l != nil {
		fmt.Printf("l2=%+v l3=%+v sets=%v\n", l.Defaults.Agent.Levels.L2, l.Defaults.Agent.Levels.L3, l.SetsRigor())
	}
}
