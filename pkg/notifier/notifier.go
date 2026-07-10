package notifier

import (
	"strings"

	"github.com/gen2brain/beeep"
)

const defaultAppName = "Patris Export"

// Toast describes a native desktop notification request.
type Toast struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

// Show displays a best-effort native OS notification.
func Show(toast Toast, iconPNG []byte) error {
	title := strings.TrimSpace(toast.Title)
	if title == "" {
		title = defaultAppName
	}

	message := strings.TrimSpace(toast.Message)
	if message == "" {
		message = "Notification"
	}

	beeep.AppName = defaultAppName
	if len(iconPNG) > 0 {
		return beeep.Notify(title, message, iconPNG)
	}
	return beeep.Notify(title, message, "")
}
