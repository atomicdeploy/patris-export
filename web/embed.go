package web

import (
	_ "embed"
)

//go:embed dist/viewer.html
var ViewerHTML []byte

//go:embed dist/welcome.html
var WelcomeHTML []byte

//go:embed assets/notification.wav
var NotificationAudio []byte
