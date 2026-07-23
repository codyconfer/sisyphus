//go:build !linux

package daemon

import "net"

func allowPeer(conn net.Conn) bool { return true }
