package ui

import (
	"fyne.io/systray"

	"github.com/codyconfer/sisyphus/daemon"
)

type TrayConfig struct {
	Title   string
	Tooltip string
	Icons   *daemon.StateIcons
	OnReady func()
	OnQuit  func()
}

type Tray struct {
	cfg  TrayConfig
	quit *systray.MenuItem
}

func NewTray(cfg TrayConfig) *Tray { return &Tray{cfg: cfg} }

func (t *Tray) Run() { systray.Run(t.onReady, t.onExit) }

func (t *Tray) onReady() {
	if t.cfg.Title != "" {
		systray.SetTitle(t.cfg.Title)
	}
	t.SetState(daemon.StateInactive)
	t.quit = systray.AddMenuItem("Quit", "Stop and exit")
	go func() {
		<-t.quit.ClickedCh
		if t.cfg.OnQuit != nil {
			t.cfg.OnQuit()
		}
		systray.Quit()
	}()
	if t.cfg.OnReady != nil {
		t.cfg.OnReady()
	}
}

func (t *Tray) onExit() {}

func (t *Tray) SetState(s daemon.State) {
	if t.cfg.Icons != nil {
		if a, ok := t.cfg.Icons.Get(s); ok && len(a.Bytes) > 0 {
			systray.SetIcon(a.Bytes)
		}
	}
	tip := t.cfg.Tooltip
	if tip == "" {
		tip = "daemon"
	}
	systray.SetTooltip(tip + " — " + s.String())
}

func (t *Tray) Stop() { systray.Quit() }
