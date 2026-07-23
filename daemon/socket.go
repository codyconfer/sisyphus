package daemon

import (
	"bufio"
	"context"
	"net"
	"time"
)

const (
	broadcastWriteTimeout = 10 * time.Second
	maxBroadcastConns     = 64
)

func Broadcast[T any](ctx context.Context, ln net.Listener, subj *Subject[T], buffer int, encode func(T) ([]byte, error)) {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	slots := make(chan struct{}, maxBroadcastConns)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if !allowPeer(conn) {
			_ = conn.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		go func() {
			defer func() { <-slots }()
			broadcastConn(ctx, conn, subj, buffer, encode)
		}()
	}
}

func broadcastConn[T any](ctx context.Context, conn net.Conn, subj *Subject[T], buffer int, encode func(T) ([]byte, error)) {
	defer conn.Close()
	sub := subj.Subscribe(buffer)
	defer subj.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-sub:
			if !ok {
				return
			}
			b, err := encode(v)
			if err != nil {
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(broadcastWriteTimeout))
			if _, err := conn.Write(append(b, '\n')); err != nil {
				return
			}
		}
	}
}

func Dial[T any](ctx context.Context, name string, decode func([]byte) (T, error)) (<-chan T, error) {
	conn, err := dialConn(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make(chan T)
	go func() {
		defer close(out)
		defer conn.Close()
		go func() {
			<-ctx.Done()
			_ = conn.Close()
		}()
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			v, err := decode(sc.Bytes())
			if err != nil {
				continue
			}
			select {
			case out <- v:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
