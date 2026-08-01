package lifecycle

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelectPrefersPlatformShell(t *testing.T) {
	both := Scripts{
		Bash:       "echo bash",
		PowerShell: "Write-Host ps",
	}
	kind, script, ok := Select(both)
	if !ok {
		t.Fatal("expected selection")
	}
	if runtime.GOOS == "windows" {
		if kind != "powershell" || script != "Write-Host ps" {
			t.Fatalf("windows select = %s %q", kind, script)
		}
	} else if kind != "bash" || script != "echo bash" {
		t.Fatalf("unix select = %s %q", kind, script)
	}
}

func TestSelectFallsBackWhenPreferredEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		kind, script, ok := Select(Scripts{Bash: "echo only"})
		if !ok || kind != "bash" || script != "echo only" {
			t.Fatalf("got %s %q ok=%v", kind, script, ok)
		}
		return
	}
	kind, script, ok := Select(Scripts{PowerShell: "Write-Host only"})
	if !ok || kind != "powershell" || script != "Write-Host only" {
		t.Fatalf("got %s %q ok=%v", kind, script, ok)
	}
}

func TestSelectEmpty(t *testing.T) {
	if _, _, ok := Select(Scripts{}); ok {
		t.Fatal("expected no selection")
	}
}

func TestRunScriptsWithUsesRunner(t *testing.T) {
	var gotKind, gotScript string
	run := func(kind, script string) error {
		gotKind, gotScript = kind, script
		return nil
	}

	s := Scripts{Bash: "echo hi", PowerShell: "Write-Host hi"}
	if err := RunScriptsWith(s, run); err != nil {
		t.Fatal(err)
	}
	wantKind, wantScript, _ := Select(s)
	if gotKind != wantKind || gotScript != wantScript {
		t.Fatalf("ran %s %q, want %s %q", gotKind, gotScript, wantKind, wantScript)
	}
}

func TestRunScriptsWithNoopWhenEmpty(t *testing.T) {
	run := func(string, string) error {
		t.Fatal("runner should not be called")
		return nil
	}
	if err := RunScriptsWith(Scripts{}, run); err != nil {
		t.Fatal(err)
	}
}

func TestRunScriptsWithPropagatesError(t *testing.T) {
	run := func(string, string) error { return errors.New("boom") }
	err := RunScriptsWith(Scripts{Bash: "x", PowerShell: "y"}, run)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v", err)
	}
}

func TestDefaultRunBashWritesFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash hook smoke is unix-oriented")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		if _, err2 := exec.LookPath("sh"); err2 != nil {
			t.Skip("no bash/sh on PATH")
		}
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ok")
	script := "printf ok > " + marker
	if err := DefaultRun("bash", script); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "ok" {
		t.Fatalf("marker = %q", b)
	}
}
