//go:build nodaemon

// Package service is empty in `nodaemon` builds: OS service install/start/stop
// is compiled out along with the rest of daemon mode, and the kardianos/service
// dependency it wraps is left out of the binary. Importing this package under
// the tag is a compile error at the first use, which is the intent. This file
// exists only so the directory still declares a buildable package.
package service
