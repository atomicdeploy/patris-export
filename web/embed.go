package web

import (
	_ "embed"
)

//go:embed dist/index.html
var IndexHTML []byte

//go:embed dist/welcome.html
var WelcomeHTML []byte
