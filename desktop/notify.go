// Package desktop sends OS desktop notifications.
//
// It is intentionally untagged and independent of daemon/ui (tray) so callers
// can notify without pulling in systray. Icon bytes are passed by value so this
// package does not import sisyphus/daemon.
package desktop

import (
	"os"
	"sync"

	"github.com/gen2brain/beeep"
)

// Icon is a named image payload for a notification.
type Icon struct {
	Name  string
	MIME  string
	Bytes []byte
}

// IconFrom builds an Icon from raw fields — the bridge for asset types other
// packages define with the same shape (for example daemon.Asset).
func IconFrom(name, mime string, data []byte) Icon {
	return Icon{Name: name, MIME: mime, Bytes: data}
}

// Notification is a desktop toast.
type Notification struct {
	Title   string
	Message string
	Icon    Icon
}

// Notify shows an OS desktop notification.
func Notify(n Notification) error {
	return beeep.Notify(n.Title, n.Message, iconPath(n.Icon))
}

var (
	iconMu    sync.Mutex
	iconPaths = map[string]string{}
)

func iconPath(a Icon) string {
	if len(a.Bytes) == 0 {
		return ""
	}
	iconMu.Lock()
	defer iconMu.Unlock()
	if p, ok := iconPaths[a.Name]; ok {
		return p
	}
	ext := ".png"
	switch a.MIME {
	case "image/x-icon", "image/vnd.microsoft.icon":
		ext = ".ico"
	case "image/svg+xml":
		ext = ".svg"
	case "image/jpeg":
		ext = ".jpg"
	}
	f, err := os.CreateTemp("", "sisyphus-desktop-*"+ext)
	if err != nil {
		return ""
	}
	if _, err := f.Write(a.Bytes); err != nil {
		f.Close()
		return ""
	}
	f.Close()
	iconPaths[a.Name] = f.Name()
	return f.Name()
}
