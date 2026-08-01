//go:build linux

package ipc

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func allowPeer(conn net.Conn) bool {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return true
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return false
	}
	if credErr != nil || cred == nil {
		return false
	}
	return int(cred.Uid) == os.Getuid()
}
