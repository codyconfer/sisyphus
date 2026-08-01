package stream

import "context"

// Subject is a fan-out broadcaster: values published with Next are delivered
// to every current subscriber. Delivery is best-effort — a subscriber whose
// channel buffer is full simply misses that value — so it suits status-style
// streams where the latest value matters more than every value. All methods
// are safe for concurrent use; a Subject starts its goroutine in NewSubject
// and stops it at Close.
type Subject[T any] struct {
	sub   chan chan T
	unsub chan (<-chan T)
	pub   chan T
	stop  chan struct{}
	done  chan struct{}
}

// NewSubject returns a running Subject with no subscribers.
func NewSubject[T any]() *Subject[T] {
	s := &Subject[T]{
		sub:   make(chan chan T),
		unsub: make(chan (<-chan T)),
		pub:   make(chan T),
		stop:  make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *Subject[T]) run() {
	subs := map[chan T]struct{}{}
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			for ch := range subs {
				close(ch)
			}
			return
		case ch := <-s.sub:
			subs[ch] = struct{}{}
		case c := <-s.unsub:
			for ch := range subs {
				if ch == c {
					delete(subs, ch)
					close(ch)
					break
				}
			}
		case v := <-s.pub:
			for ch := range subs {
				select {
				case ch <- v:
				default:
				}
			}
		}
	}
}

// Subscribe registers a new subscriber and returns its channel, buffered to
// buffer values (negative means 0). The channel is closed on Unsubscribe or
// when the Subject closes; subscribing to a closed Subject returns an
// already-closed channel. Values published while the buffer is full are
// dropped for this subscriber.
func (s *Subject[T]) Subscribe(buffer int) <-chan T {
	if buffer < 0 {
		buffer = 0
	}
	ch := make(chan T, buffer)
	select {
	case s.sub <- ch:
		return ch
	case <-s.done:
		close(ch)
		return ch
	}
}

// Unsubscribe removes the subscriber whose channel is c and closes it.
// Unknown channels, and Subjects already closed, are ignored.
func (s *Subject[T]) Unsubscribe(c <-chan T) {
	select {
	case s.unsub <- c:
	case <-s.done:
	}
}

// Next publishes v to every current subscriber, skipping any whose buffer is
// full. On a closed Subject it does nothing.
func (s *Subject[T]) Next(v T) {
	select {
	case s.pub <- v:
	case <-s.done:
	}
}

// Pump publishes every value received on in until the channel closes or ctx
// is cancelled. It blocks, so run it in its own goroutine.
func (s *Subject[T]) Pump(ctx context.Context, in <-chan T) {
	for {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-in:
			if !ok {
				return
			}
			s.Next(v)
		}
	}
}

// Close stops the Subject and closes every subscriber channel. It does not
// block and is safe to call more than once.
func (s *Subject[T]) Close() {
	select {
	case s.stop <- struct{}{}:
	default:
	}
}
