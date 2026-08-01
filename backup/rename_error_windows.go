//go:build windows

package backup

import (
	"errors"

	"golang.org/x/sys/windows"
)

func renameHeld(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
