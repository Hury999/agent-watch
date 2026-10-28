package main

// Images in messages: a path in the text (drag a file into the terminal, or ctrl+v a clipboard
// image, which is saved under the run and its path inserted) is what the agent gets to look at.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var imagePath = regexp.MustCompile(`(?i)"([^"]+\.(?:png|jpe?g|gif|webp))"|'([^']+\.(?:png|jpe?g|gif|webp))'|(\S+\.(?:png|jpe?g|gif|webp))`)

// imagePaths lists the existing image files a message names, quoted or not.
func imagePaths(text string) []string {
	var paths []string
	for _, m := range imagePath.FindAllStringSubmatch(text, -1) {
		p := m[1] + m[2] + m[3]
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && !contains(paths, p) {
			paths = append(paths, p)
		}
	}
	return paths
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// clipboardImage saves the clipboard's image as a PNG in dir and returns its path; false if the
// clipboard holds no image or the platform has no tool to read it.
func clipboardImage(dir string) (string, bool) {
	if os.MkdirAll(dir, 0o755) != nil {
		return "", false
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+".png")
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; $i = [System.Windows.Forms.Clipboard]::GetImage(); if ($i -eq $null) { exit 1 }; $i.Save($env:AW_IMAGE, [System.Drawing.Imaging.ImageFormat]::Png)`
		cmd = exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
		cmd.Env = append(os.Environ(), "AW_IMAGE="+path)
	case "darwin":
		cmd = exec.Command("pngpaste", path)
	default:
		cmd = exec.Command("sh", "-c", `wl-paste --type image/png > "$1" 2>/dev/null || xclip -selection clipboard -t image/png -o > "$1" 2>/dev/null`, "sh", path)
	}
	var out bytes.Buffer
	cmd.Stderr = &out
	if cmd.Run() != nil {
		os.Remove(path)
		return "", false
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		os.Remove(path)
		return "", false
	}
	return path, true
}

// expandImages swaps the [Image #n] markers in the input for the saved files' paths.
func (m *model) expandImages(text string) string {
	for i, path := range m.images {
		text = strings.ReplaceAll(text, fmt.Sprintf("[Image #%d]", i+1), `"`+path+`"`)
	}
	return text
}
