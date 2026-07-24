package service

import (
	"context"
	"runtime"
	"time"

	kservice "github.com/kardianos/service"
)

type Config struct {
	Name        string
	DisplayName string
	Description string
	Arguments   []string
	UserService bool
}

type program struct {
	run    func(ctx context.Context) error
	cancel context.CancelFunc
	done   chan error
}

func (p *program) Start(_ kservice.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan error, 1)
	go func() { p.done <- p.run(ctx) }()
	return nil
}

func (p *program) Stop(_ kservice.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
		}
	}
	return nil
}

type Service struct {
	svc kservice.Service
}

func New(cfg Config, run func(ctx context.Context) error) (*Service, error) {
	opt := kservice.KeyValue{}
	if cfg.UserService && runtime.GOOS != "windows" {
		opt["UserService"] = true
	}
	svc, err := kservice.New(&program{run: run}, &kservice.Config{
		Name:        cfg.Name,
		DisplayName: cfg.DisplayName,
		Description: cfg.Description,
		Arguments:   cfg.Arguments,
		Option:      opt,
	})
	if err != nil {
		return nil, err
	}
	return &Service{svc: svc}, nil
}

func Interactive() bool { return kservice.Interactive() }

func (s *Service) Run() error       { return s.svc.Run() }
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
