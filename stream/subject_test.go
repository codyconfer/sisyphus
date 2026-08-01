package stream

import (
	"testing"
	"time"
)

func TestSubjectFansOutToAllSubscribers(t *testing.T) {
	s := NewSubject[int]()
	defer s.Close()

	a := s.Subscribe(4)
	b := s.Subscribe(4)

	s.Next(1)
	s.Next(2)

	for _, ch := range []<-chan int{a, b} {
		for want := 1; want <= 2; want++ {
			select {
			case got := <-ch:
				if got != want {
					t.Fatalf("want %d, got %d", want, got)
				}
			case <-time.After(time.Second):
				t.Fatalf("subscriber did not receive %d", want)
			}
		}
	}
}

func TestSubjectOnlyDeliversAfterSubscribe(t *testing.T) {
	s := NewSubject[int]()
	defer s.Close()

	s.Next(1)
	late := s.Subscribe(4)
	s.Next(2)

	select {
	case got := <-late:
		if got != 2 {
			t.Fatalf("late subscriber should only see post-subscribe events, got %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("late subscriber received nothing")
	}
}

func TestSubjectCloseClosesSubscribers(t *testing.T) {
	s := NewSubject[int]()
	ch := s.Subscribe(1)
	s.Close()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("subscriber channel should be closed after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber channel was not closed")
	}
}

func TestSubjectSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	s := NewSubject[int]()
	defer s.Close()

	slow := s.Subscribe(1)
	fast := s.Subscribe(16)
	_ = slow

	for i := range 10 {
		s.Next(i)
	}

	got := 0
	for {
		select {
		case <-fast:
			got++
			if got >= 5 {
				return
			}
		case <-time.After(time.Second):
			t.Fatalf("fast subscriber blocked by slow one; got %d", got)
		}
	}
}

func TestSubjectCloseIdempotent(t *testing.T) {
	s := NewSubject[int]()
	s.Close()
	s.Close()
	s.Next(1)
}
