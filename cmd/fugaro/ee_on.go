//go:build ee

// The one assembly point (D303 as amended, R6): the ee build tag compiles this
// file and nothing else outside ee/, and the blank import below is what runs
// ee/register's init — which is how a tagged binary reports the enterprise
// edition and how the seams the moved features fill are set.
package main

import _ "github.com/dimipaun/fugaro/ee/register"
