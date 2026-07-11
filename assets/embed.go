package assets

import _ "embed"

// AppIconPNG is the canonical cropped Patris API logo used by the web UI and
// the icon build pipeline.
//
//go:embed patris-api-icon.png
var AppIconPNG []byte
