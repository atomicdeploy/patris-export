package web

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"sync"
)

//go:embed dist/viewer.html
var ViewerHTML []byte

//go:embed dist/welcome.html
var WelcomeHTML []byte

//go:embed dist/charmap.html
var CharmapHTML []byte

//go:embed assets/notification.ogg
var NotificationAudio []byte

//go:embed assets/favicon.ico
var FaviconICO []byte

//go:embed assets/patris-api-icon.png
var AppIconPNG []byte

var (
	resourceOnce sync.Once
	resourceInfo ResourceInfo
)

// ResourceInfo identifies the exact web assets embedded in this executable.
type ResourceInfo struct {
	Version string `json:"version"`
	Viewer  int    `json:"viewer_bytes"`
	Welcome int    `json:"welcome_bytes"`
	Charmap int    `json:"charmap_bytes"`
	Audio   int    `json:"audio_bytes"`
	Icon    int    `json:"icon_bytes"`
	Favicon int    `json:"favicon_bytes"`
}

// Resources returns a stable fingerprint for the embedded web UI and assets.
func Resources() ResourceInfo {
	resourceOnce.Do(func() {
		hash := sha256.New()
		hash.Write(ViewerHTML)
		hash.Write(WelcomeHTML)
		hash.Write(CharmapHTML)
		hash.Write(NotificationAudio)
		hash.Write(FaviconICO)
		hash.Write(AppIconPNG)
		sum := hash.Sum(nil)

		resourceInfo = ResourceInfo{
			Version: hex.EncodeToString(sum[:12]),
			Viewer:  len(ViewerHTML),
			Welcome: len(WelcomeHTML),
			Charmap: len(CharmapHTML),
			Audio:   len(NotificationAudio),
			Icon:    len(AppIconPNG),
			Favicon: len(FaviconICO),
		}
	})
	return resourceInfo
}
