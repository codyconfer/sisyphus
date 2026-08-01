// Package storeerr holds the sentinel error shared by every sisyphus store
// package. Each store package aliases (or wraps) ErrUnavailable, so a caller
// can match errors.Is(err, storeerr.ErrUnavailable) without knowing which
// store produced the error.
package storeerr

import "errors"

// ErrUnavailable is returned when a method is called on a nil or closed store.
var ErrUnavailable = errors.New("store unavailable")
