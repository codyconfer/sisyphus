package daemon

import (
	"os"
	"sync"

	"github.com/gen2brain/beeep"
)

type Notification struct {
	Title   string
	Message string
	Icon    Asset
}

func Notify(n Notification) error {
	return beeep.Notify(n.Title, n.Message, iconPath(n.Icon))
}

var (
	iconMu    sync.Mutex
	iconPaths = map[string]string{}
)

func iconPath(a Asset) string {
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
	f, err := os.CreateTemp("", "sisyphus-asset-*"+ext)
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
