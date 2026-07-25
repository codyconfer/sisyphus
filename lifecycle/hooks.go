package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Scripts holds optional bash and PowerShell script bodies for a lifecycle
// hook. Callers supply whichever platforms they support; Select picks one.
type Scripts struct {
	Bash       string
	PowerShell string
}

// RunFunc executes a shell script with the given interpreter kind
// ("bash" or "powershell").
type RunFunc func(kind, script string) error

// Run is the process-level shell runner; tests may replace it.
var Run = DefaultRun

// Select picks which script to run for the current platform.
// Preference: bash on Unix, PowerShell on Windows. If the preferred script is
// empty, the other is used when present. Returns kind, script, ok.
func Select(s Scripts) (kind, script string, ok bool) {
	bash := strings.TrimSpace(s.Bash)
	ps := strings.TrimSpace(s.PowerShell)
	preferPS := runtime.GOOS == "windows"
	if preferPS {
		if ps != "" {
			return "powershell", ps, true
		}
		if bash != "" {
			return "bash", bash, true
		}
		return "", "", false
	}
	if bash != "" {
		return "bash", bash, true
	}
	if ps != "" {
		return "powershell", ps, true
	}
	return "", "", false
}

// RunScripts runs the selected script for s, or nil when nothing to run.
// Missing interpreters return an error; callers typically warn and continue.
func RunScripts(s Scripts) error {
	kind, script, ok := Select(s)
	if !ok {
		return nil
	}
	return Run(kind, script)
}

// DefaultRun executes script with the named interpreter.
func DefaultRun(kind, script string) error {
	switch kind {
	case "bash":
		return runBash(script)
	case "powershell":
		return runPowerShell(script)
	default:
		return fmt.Errorf("unknown shell kind %q", kind)
	}
}

func runBash(script string) error {
	bin, err := LookBash()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bash hook: %w", err)
	}
	return nil
}

func runPowerShell(script string) error {
	bin, err := LookPowerShell()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("powershell hook: %w", err)
	}
	return nil
}

// LookBash finds bash or sh on PATH.
func LookBash() (string, error) {
	if p, err := exec.LookPath("bash"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("sh"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("bash/sh not found on PATH")
}

// LookPowerShell finds pwsh or powershell on PATH.
func LookPowerShell() (string, error) {
	if p, err := exec.LookPath("pwsh"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("powershell"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("pwsh/powershell not found on PATH")
}
