//go:build !nodaemon

package service

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	kservice "github.com/kardianos/service"
)

// fakeRunner stands in for a platform service runner. Its Run mirrors the unix
// implementations in kardianos/service: Interface.Start, then the RunWait hook,
// then Interface.Stop. wait may be replaced to emulate a runner that ignores
// RunWait (Windows) or an ordinary manager-requested stop.
type fakeRunner struct {
	prog *program
	wait func()

	stop     chan struct{}
	stopOnce sync.Once

	mu     sync.Mutex
	stops  int
	logged []string
}

func newFakeRunner(p *program) *fakeRunner {
	return &fakeRunner{prog: p, stop: make(chan struct{})}
}

func (f *fakeRunner) Run() error {
	if err := f.prog.Start(f); err != nil {
		return err
	}
	if f.wait != nil {
		f.wait()
	} else {
		f.prog.runWait()
	}
	return f.prog.Stop(f)
}

func (f *fakeRunner) Start() error { return nil }

func (f *fakeRunner) Stop() error {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
	f.stopOnce.Do(func() { close(f.stop) })
	return nil
}

func (f *fakeRunner) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}

func (f *fakeRunner) logs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.logged...)
}

func (f *fakeRunner) Restart() error   { return nil }
func (f *fakeRunner) Install() error   { return nil }
func (f *fakeRunner) Uninstall() error { return nil }
func (f *fakeRunner) String() string   { return "fake" }
func (f *fakeRunner) Platform() string { return "fake" }

func (f *fakeRunner) Logger(chan<- error) (kservice.Logger, error) {
	return &fakeLogger{run: f}, nil
}

func (f *fakeRunner) SystemLogger(errs chan<- error) (kservice.Logger, error) {
	return f.Logger(errs)
}

func (f *fakeRunner) Status() (kservice.Status, error) { return kservice.StatusRunning, nil }

type fakeLogger struct{ run *fakeRunner }

func (l *fakeLogger) record(v ...any) error {
	l.run.mu.Lock()
	defer l.run.mu.Unlock()
	parts := make([]string, 0, len(v))
	for _, x := range v {
		if s, ok := x.(string); ok {
			parts = append(parts, s)
			continue
		}
		parts = append(parts, "?")
	}
	l.run.logged = append(l.run.logged, strings.Join(parts, " "))
	return nil
}

func (l *fakeLogger) Error(v ...any) error                { return l.record(v...) }
func (l *fakeLogger) Warning(v ...any) error              { return l.record(v...) }
func (l *fakeLogger) Info(v ...any) error                 { return l.record(v...) }
func (l *fakeLogger) Errorf(f string, a ...any) error     { return l.record(f) }
func (l *fakeLogger) Warningf(f string, a ...any) error   { return l.record(f) }
func (l *fakeLogger) Infof(format string, a ...any) error { return l.record(format) }

func runAsync(t *testing.T, s *Service) <-chan error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run() }()
	return errCh
}

func TestRunReturnsFatalWorkError(t *testing.T) {
	fatal := errors.New("fatal: cannot reach backend")
	p := newProgram(func(context.Context) error {
		t := time.NewTimer(20 * time.Millisecond)
		defer t.Stop()
		<-t.C
		return fatal
	})
	f := newFakeRunner(p)
	s := &Service{svc: f, prog: p}

	errCh := runAsync(t, s)
	select {
	case err := <-errCh:
		if !errors.Is(err, fatal) {
			t.Fatalf("Run() err = %v, want %v", err, fatal)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after a fatal work error")
	}
	if got := p.Err(); !errors.Is(got, fatal) {
		t.Fatalf("program.Err() = %v, want %v", got, fatal)
	}
	logs := f.logs()
	found := false
	for _, l := range logs {
		if strings.Contains(l, "cannot reach backend") {
			found = true
		}
	}
	if !found {
		t.Fatalf("fatal error was not logged, logs = %q", logs)
	}
}

func TestRunReturnsNilOnManagerStop(t *testing.T) {
	p := newProgram(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	f := newFakeRunner(p)
	f.wait = func() {
		select {
		case <-f.stop:
		case <-p.fatal:
		}
	}
	s := &Service{svc: f, prog: p}

	errCh := runAsync(t, s)
	_ = f.Stop()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() err = %v, want nil on a clean stop", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after the service manager stopped it")
	}
}

func TestFatalStopsRunnerThatIgnoresRunWait(t *testing.T) {
	fatal := errors.New("fatal: backend gone")
	p := newProgram(func(context.Context) error { return fatal })
	p.stopGrace = 20 * time.Millisecond
	f := newFakeRunner(p)
	f.wait = func() { <-f.stop }
	s := &Service{svc: f, prog: p}

	errCh := runAsync(t, s)
	select {
	case err := <-errCh:
		if !errors.Is(err, fatal) {
			t.Fatalf("Run() err = %v, want %v", err, fatal)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() hung: fatal error did not stop a runner that ignores RunWait")
	}
	if n := f.stopCount(); n < 1 {
		t.Fatalf("service manager Stop calls = %d, want >= 1", n)
	}
}

func TestServiceConfigInstallsRunWaitHook(t *testing.T) {
	p := newProgram(func(context.Context) error { return nil })
	cfg := serviceConfig(Config{Name: "munin", Scope: ScopeUser}, p)
	wait, ok := cfg.Option[optionRunWait].(func())
	if !ok {
		t.Fatalf("RunWait option = %T, want func()", cfg.Option[optionRunWait])
	}
	if runtime.GOOS != "windows" {
		if v, _ := cfg.Option["UserService"].(bool); !v {
			t.Fatal("UserService option lost")
		}
	}
	p.signalFatal(nil)
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunWait did not return after a fatal work error")
	}
}

func TestStopWithoutStartDoesNotBlock(t *testing.T) {
	p := newProgram(func(context.Context) error { return nil })
	done := make(chan error, 1)
	go func() { done <- p.Stop(nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop() err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() blocked with no Start")
	}
}
