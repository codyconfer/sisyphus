//go:build !windows

package backup

func renameHeld(error) bool {
	return false
}
