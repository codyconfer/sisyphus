package daemon

import (
	"bufio"
	"context"
	"errors"
	"net"
	"time"
)

// ErrInUse reports that another process already listens on the socket or
// pipe. Listen returns it wrapped, so match with errors.Is.
var ErrInUse = errors.New("address already in use")

const (
	broadcastWriteTimeout = 10 * time.Second
	maxBroadcastConns     = 64
	maxDialFrame          = 4 * 1024 * 1024
	dialReadBuffer        = 64 * 1024
	peerDrainBuffer       = 512
)

// Broadcast serves subj over ln until ctx is cancelled: each accepted
// connection gets its own subscription (buffered to buffer) and receives one
// encoded, newline-terminated frame per published value. Peers that are not
// the same OS user are refused, at most 64 connections are served at once
// (extras are closed immediately), a value that fails to encode is skipped,
// and a connection whose write blocks past ten seconds is dropped.
// Broadcast blocks; run it in its own goroutine.
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
	departed := watchPeerDeparture(conn)
	for {
		select {
		case <-ctx.Done():
			return
		case <-departed:
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

func watchPeerDeparture(conn net.Conn) <-chan struct{} {
	departed := make(chan struct{})
	go func() {
		defer close(departed)
		buf := make([]byte, peerDrainBuffer)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()
	return departed
}

// DialOption customizes Dial. Nil options are ignored.
type DialOption func(*dialOptions)

type dialOptions struct {
	onClose func(error)
}

// WithDialClose registers fn to run once when the stream returned by Dial
// ends. fn receives why it ended: nil for a clean server-side close, the
// context error on cancellation, or the read error otherwise.
func WithDialClose(fn func(error)) DialOption {
	return func(o *dialOptions) { o.onClose = fn }
}

// Dial connects to the service endpoint at prefix/name (see Listen for how
// the two are used per platform) and returns a channel of decoded values,
// one per newline-terminated frame. Frames that fail to decode are skipped.
// The channel closes when the server closes the connection, a frame exceeds
// 4 MiB, or ctx is cancelled; use WithDialClose to learn which.
func Dial[T any](ctx context.Context, prefix, name string, decode func([]byte) (T, error), opts ...DialOption) (<-chan T, error) {
	var o dialOptions
	for _, apply := range opts {
		if apply != nil {
			apply(&o)
		}
	}
	conn, err := dialConn(ctx, prefix, name)
	if err != nil {
		return nil, err
	}
	out := make(chan T)
	go func() {
		var reason error
		defer close(out)
		defer func() {
			if o.onClose != nil {
				o.onClose(reason)
			}
		}()
		defer conn.Close()
		go func() {
			<-ctx.Done()
			_ = conn.Close()
		}()
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 0, dialReadBuffer), maxDialFrame)
		for sc.Scan() {
			v, err := decode(sc.Bytes())
			if err != nil {
				continue
			}
			select {
			case out <- v:
			case <-ctx.Done():
				reason = ctx.Err()
				return
			}
		}
		if cerr := ctx.Err(); cerr != nil {
			reason = cerr
			return
		}
		reason = sc.Err()
	}()
	return out, nil
}
