// Package images holds the container image sources (design §7): a base
// image per directory (images/web-node/), scripts the base images share
// (images/common/), and the derived-image template and Cloud Build config
// (images/derived/). It embeds the template so the fugaro binary can render
// it.
package images

import _ "embed"

// DerivedTemplate is images/derived/Dockerfile.tmpl, a text/template
// rendered by internal/image.Render.
//
//go:embed derived/Dockerfile.tmpl
var DerivedTemplate string
