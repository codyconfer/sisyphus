package lifecycle

import (
	"path/filepath"
	"testing"
)

func TestInstallRejectsPathsOutsideHome(t *testing.T) {
	home := t.TempDir()
	cases := []InstallSpec{
		{Home: home, Dirs: []string{filepath.Join("..", "outside")}},
		{Home: home, Files: []FileSeed{{RelPath: filepath.Join("..", "outside.yaml")}}},
	}
	for _, spec := range cases {
		if _, err := Install(spec); err == nil {
			t.Fatal("Install should reject a path outside home")
		}
	}
}

func TestNukeRejectsFilesystemRoot(t *testing.T) {
	if _, err := Nuke(string(filepath.Separator), InstallSpec{}); err == nil {
		t.Fatal("Nuke should reject filesystem root")
	}
}

func TestNukeReinstallsApplicationHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "app")
	if _, err := Install(InstallSpec{
		Home:  home,
		Files: []FileSeed{{RelPath: "old.yaml", Content: []byte("old")}},
	}); err != nil {
		t.Fatal(err)
	}

	created, err := Nuke(home, InstallSpec{
		Files: []FileSeed{{RelPath: "config.yaml", Content: []byte("new")}},
	})
	if err != nil {
		t.Fatalf("Nuke: %v", err)
	}
	if len(created) != 1 || created[0] != filepath.Join(home, "config.yaml") {
		t.Fatalf("created = %v", created)
	}
	if Exists(filepath.Join(home, "old.yaml")) {
		t.Fatal("old file should be removed")
	}
}
