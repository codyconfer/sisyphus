//go:build !nodaemon

package tray

import "fyne.io/systray"

// Config configures a Tray: its Title and Tooltip text, the Icons shown
// per State (states without an icon keep the previous one), OnReady run once
// the tray is up, and OnQuit run when the user picks Quit.
type Config struct {
	Title   string
	Tooltip string
	Icons   *Icons
	OnReady func()
	OnQuit  func()
}

// Tray is a system tray icon with a Quit menu item, reflecting a State in
// its icon and tooltip.
type Tray struct {
	cfg  Config
	quit *systray.MenuItem
}

// NewTray returns a Tray for cfg. Nothing appears until Run.
func NewTray(cfg Config) *Tray { return &Tray{cfg: cfg} }

// Run shows the tray (initially in StateInactive) and blocks until Stop is
// called or the user quits. systray requires it on the main goroutine on
// some platforms, so treat it as the program's UI loop.
func (t *Tray) Run() { systray.Run(t.onReady, t.onExit) }

func (t *Tray) onReady() {
	if t.cfg.Title != "" {
		systray.SetTitle(t.cfg.Title)
	}
	t.SetState(StateInactive)
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

// SetState switches the tray icon to s's icon (when the config's Icons has
// one with bytes) and appends the state name to the tooltip ("daemon" when
// no Tooltip was configured).
func (t *Tray) SetState(s State) {
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

// Stop removes the tray icon and unblocks Run. It does not call OnQuit.
func (t *Tray) Stop() { systray.Quit() }
