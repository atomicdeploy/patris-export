package web

import (
	_ "embed"
)

//go:embed dist/index.html
var IndexHTML []byte
