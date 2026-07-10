package web

import (
	_ "embed"
)

//go:embed dist/viewer.html
var ViewerHTML []byte

//go:embed dist/welcome.html
var WelcomeHTML []byte

//go:embed assets/notification.ogg
var NotificationAudio []byte

//go:embed assets/favicon.ico
var FaviconICO []byte

//go:embed assets/patris-api-icon.png
var AppIconPNG []byte
