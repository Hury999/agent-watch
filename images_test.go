package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImagePaths(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "shot.png")
	spaced := filepath.Join(dir, "my shot.JPG")
	for _, p := range []string{plain, spaced} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := imagePaths("look at " + plain + " and \"" + spaced + "\" but not " + filepath.Join(dir, "missing.png") + " or notes.txt " + plain)
	if len(got) != 2 || got[0] != plain || got[1] != spaced {
		t.Errorf("imagePaths = %q", got)
	}
	if got := imagePaths("no images here"); len(got) != 0 {
		t.Errorf("imagePaths = %q, want none", got)
	}
}

func TestExpandImages(t *testing.T) {
	m := &model{images: []string{`C:\run\images\a.png`, "/run/images/b.png"}}
	got := m.expandImages("compare [Image #1] with [Image #2], [Image #3] stays")
	want := `compare "C:\run\images\a.png" with "/run/images/b.png", [Image #3] stays`
	if got != want {
		t.Errorf("expandImages = %q, want %q", got, want)
	}
}
