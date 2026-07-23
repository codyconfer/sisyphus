//go:build !linux && !darwin && !freebsd

package daemon

import "net"

func allowPeer(conn net.Conn) bool { return true }
