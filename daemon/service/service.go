//go:build !nodaemon

package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	kservice "github.com/kardianos/service"
)

// optionRunWait is kardianos/service's documented hook for replacing the
// "block until SIGTERM" wait inside Run. Setting it lets a fatal work error
// unwind Run instead of leaving the service up doing nothing.
const optionRunWait = "RunWait"

// stopWait bounds how long Stop waits for the work function to return.
const stopWait = 10 * time.Second

// fatalStopGrace is how long a fatal work error waits for the platform runner
// to unwind Run on its own (it does when RunWait is honored: systemd, launchd,
// sysv, upstart, openrc, freebsd, solaris, aix) before asking the service
// manager to stop us. The Windows service runner ignores RunWait and only stops
// on an SCM request, so the fallback is what terminates it there.
const fatalStopGrace = 5 * time.Second

// Scope selects whether the service is managed system-wide or per-user.
type Scope int

const (
	// ScopeSystem manages a system-wide service (the default).
	ScopeSystem Scope = iota
	// ScopeUser manages a per-user service (systemd user unit, launchd
	// agent). Ignored on Windows, which has no per-user services.
	ScopeUser
)

// Config describes the service to the OS service manager: its identifier
// (Name), human-facing DisplayName and Description, the Arguments the
// installed unit starts the binary with, and its Scope.
type Config struct {
	Name        string
	DisplayName string
	Description string
	Arguments   []string
	Scope       Scope
}

type program struct {
	run       func(ctx context.Context) error
	cancel    context.CancelFunc
	done      chan error
	fatal     chan struct{}
	stopped   chan struct{}
	stopGrace time.Duration

	started   atomic.Bool
	fatalOnce sync.Once
	stopOnce  sync.Once

	mu  sync.Mutex
	err error
}

func newProgram(run func(ctx context.Context) error) *program {
	return &program{
		run:       run,
		done:      make(chan error, 1),
		fatal:     make(chan struct{}),
		stopped:   make(chan struct{}),
		stopGrace: fatalStopGrace,
	}
}

func (p *program) Start(s kservice.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.started.Store(true)
	go func() {
		err := p.run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			p.setErr(err)
			logFatal(s, err)
			p.signalFatal(s)
		}
		p.done <- err
	}()
	return nil
}

func (p *program) Stop(_ kservice.Service) error {
	p.stopOnce.Do(func() { close(p.stopped) })
	if p.cancel != nil {
		p.cancel()
	}
	if p.started.Load() {
		select {
		case <-p.done:
		case <-time.After(stopWait):
		}
	}
	return p.Err()
}

// runWait blocks until the service is signaled to stop or the work function
// fails fatally, whichever comes first.
func (p *program) runWait() {
	sig := make(chan os.Signal, 3)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	select {
	case <-sig:
	case <-p.fatal:
	}
}

// signalFatal unblocks runWait and, if the platform runner does not unwind on
// its own within fatalStopGrace, asks the service manager to stop the service.
func (p *program) signalFatal(s kservice.Service) {
	p.fatalOnce.Do(func() {
		close(p.fatal)
		if s == nil {
			return
		}
		go func() {
			t := time.NewTimer(p.stopGrace)
			defer t.Stop()
			select {
			case <-p.stopped:
			case <-t.C:
				_ = s.Stop()
			}
		}()
	})
}

func (p *program) setErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		p.err = err
	}
}

// Err reports the fatal work error, if the work function returned one.
func (p *program) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func logFatal(s kservice.Service, err error) {
	if s != nil {
		if lg, lerr := s.Logger(nil); lerr == nil && lg != nil {
			_ = lg.Error(err.Error())
			return
		}
	}
	slog.Error("service work function failed", "err", err)
}

// Service is a managed OS service around one work function.
type Service struct {
	svc  kservice.Service
	prog *program
}

// New builds a Service whose work function is run. run receives a context
// that is cancelled when the service manager stops the service; it should
// block until then. A non-Canceled error from run is fatal: it is logged,
// unwinds Run, and on platforms that ignore that (Windows) the service asks
// its own manager to stop it.
func New(cfg Config, run func(ctx context.Context) error) (*Service, error) {
	p := newProgram(run)
	svc, err := kservice.New(p, serviceConfig(cfg, p))
	if err != nil {
		return nil, err
	}
	return &Service{svc: svc, prog: p}, nil
}

func serviceConfig(cfg Config, p *program) *kservice.Config {
	opt := kservice.KeyValue{}
	if cfg.Scope == ScopeUser && runtime.GOOS != "windows" {
		opt["UserService"] = true
	}
	if p != nil {
		opt[optionRunWait] = p.runWait
	}
	return &kservice.Config{
		Name:        cfg.Name,
		DisplayName: cfg.DisplayName,
		Description: cfg.Description,
		Arguments:   cfg.Arguments,
		Option:      opt,
	}
}

// Interactive reports whether the process is running interactively (e.g.
// from a terminal) rather than under the OS service manager.
func Interactive() bool { return kservice.Interactive() }

// Run is the OS service entrypoint: it blocks until the service manager stops
// the service or the work function fails, and returns that failure so the
// caller can exit non-zero and let the supervisor restart it.
func (s *Service) Run() error {
	err := s.svc.Run()
	if err == nil && s.prog != nil {
		err = s.prog.Err()
	}
	return err
}

// Install registers the service with the OS service manager.
func (s *Service) Install() error { return s.svc.Install() }

// Uninstall removes the service from the OS service manager.
func (s *Service) Uninstall() error { return s.svc.Uninstall() }

// Start asks the OS service manager to start the installed service.
func (s *Service) Start() error { return s.svc.Start() }

// Stop asks the OS service manager to stop the installed service.
func (s *Service) Stop() error { return s.svc.Stop() }

// Restart asks the OS service manager to restart the installed service.
func (s *Service) Restart() error { return s.svc.Restart() }

// Platform names the underlying service system (e.g. "linux-systemd").
func (s *Service) Platform() string { return s.svc.Platform() }

// State is the coarse service status reported by Status.
type State int

// The coarse states Status can report.
const (
	StateUnknown State = iota
	StateRunning
	StateStopped
	StateNotInstalled
)

func (s State) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateStopped:
		return "stopped"
	case StateNotInstalled:
		return "not installed"
	default:
		return "unknown"
	}
}

// Status asks the OS service manager for the service's state. The State is
// meaningful even when the error is non-nil: a service that is not installed
// comes back as StateNotInstalled together with the underlying error.
func (s *Service) Status() (State, error) {
	st, err := s.svc.Status()
	switch st {
	case kservice.StatusRunning:
		return StateRunning, err
	case kservice.StatusStopped:
		return StateStopped, err
	default:
		if errors.Is(err, kservice.ErrNotInstalled) {
			return StateNotInstalled, err
		}
		return StateUnknown, err
	}
}
