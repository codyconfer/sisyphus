//go:build !linux && !darwin && !freebsd

package ipc

import "net"

func allowPeer(conn net.Conn) bool { return true }
