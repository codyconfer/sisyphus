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

type Config struct {
	Name        string
	DisplayName string
	Description string
	Arguments   []string
	UserService bool
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

type Service struct {
	svc  kservice.Service
	prog *program
}

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
	if cfg.UserService && runtime.GOOS != "windows" {
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

func (s *Service) Install() error   { return s.svc.Install() }
func (s *Service) Uninstall() error { return s.svc.Uninstall() }
func (s *Service) Start() error     { return s.svc.Start() }
func (s *Service) Stop() error      { return s.svc.Stop() }
func (s *Service) Restart() error   { return s.svc.Restart() }
func (s *Service) Platform() string { return s.svc.Platform() }

func (s *Service) Status() (string, error) {
	st, err := s.svc.Status()
	switch st {
	case kservice.StatusRunning:
		return "running", err
	case kservice.StatusStopped:
		return "stopped", err
	default:
		return "unknown", err
	}
}
