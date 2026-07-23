package daemon

import "context"

type Subject[T any] struct {
	sub   chan chan T
	unsub chan (<-chan T)
	pub   chan T
	stop  chan struct{}
	done  chan struct{}
}

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

func (s *Subject[T]) Unsubscribe(c <-chan T) {
	select {
	case s.unsub <- c:
	case <-s.done:
	}
}

func (s *Subject[T]) Next(v T) {
	select {
	case s.pub <- v:
	case <-s.done:
	}
}

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

func (s *Subject[T]) Close() {
	select {
	case s.stop <- struct{}{}:
	default:
	}
}
