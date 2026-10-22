package main

// How things look: colors, the Claude Code style transcript, markdown, spinners and number formats.

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

var (
	cAccent = lipgloss.Color("#D97757") // Claude's clay orange
	cText   = lipgloss.Color("#E6E6E6")
	cDim    = lipgloss.Color("#8A8A8A")
	cBorder = lipgloss.Color("#3C3C3C")
	cGreen  = lipgloss.Color("#4EBA65")
	cRed    = lipgloss.Color("#E5534B")
	cYellow = lipgloss.Color("#E3B341")
	cBlue   = lipgloss.Color("#6CB6FF")
	cPurple = lipgloss.Color("#B392F0")
	cUserBg = lipgloss.Color("#2B2B2B")
	cSelBg  = lipgloss.Color("#33302E")

	sAccent = lipgloss.NewStyle().Foreground(cAccent)
	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sBold   = lipgloss.NewStyle().Bold(true).Foreground(cText)
	sGreen  = lipgloss.NewStyle().Foreground(cGreen)
	sRed    = lipgloss.NewStyle().Foreground(cRed)
	sYellow = lipgloss.NewStyle().Foreground(cYellow)
)

func badge(name string) string {
	return lipgloss.NewStyle().Foreground(agentColors[name]).Render(name)
}

var spinFrames = []string{"·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"}

func spinFrame() string {
	return spinFrames[int(time.Now().UnixMilli()/120)%len(spinFrames)]
}

var verbs = []string{"Thinking", "Working", "Cooking", "Brewing", "Pondering", "Crafting", "Tinkering",
	"Conjuring", "Noodling", "Hatching", "Forging", "Wrangling", "Untangling", "Percolating"}

func randomVerb() string { return verbs[rand.Intn(len(verbs))] }

// shimmer lights up one moving letter of s, like Claude Code's spinner text.
func shimmer(s string) string {
	r := []rune(s)
	hot := int(time.Now().UnixMilli()/90) % (len(r) + 6)
	var b strings.Builder
	for i, c := range r {
		st := sAccent
		if i == hot || i == hot-1 {
			st = lipgloss.NewStyle().Foreground(lipgloss.Color("#F5B08E"))
		}
		b.WriteString(st.Render(string(c)))
	}
	return b.String()
}

func dur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func tokens(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%.1fm", float64(n)/1e6)
}

// meter draws a 0..1 fraction as a small colored bar.
func meter(f float64) string {
	const n = 6
	full := min(int(f*n+0.5), n)
	st := sGreen
	if f >= 0.8 {
		st = sRed
	} else if f >= 0.5 {
		st = sYellow
	}
	return st.Render(strings.Repeat("▰", full)) + sDim.Render(strings.Repeat("▱", n-full)) + " " + st.Render(fmt.Sprintf("%d%%", int(f*100+0.5)))
}

var mdRenderers = map[int]*glamour.TermRenderer{}

// markdown renders agent text like a chat app would; plain text if glamour fails.
func markdown(s string, w int) string {
	r, ok := mdRenderers[w]
	if !ok {
		cfg := styles.DarkStyleConfig
		zero := uint(0)
		cfg.Document.Margin = &zero
		cfg.Document.BlockPrefix, cfg.Document.BlockSuffix = "", ""
		cfg.Document.Color = nil
		// Headings without the "##" the dark style keeps: H1 as a clay label, the rest bold clay.
		accent, dark := string(cAccent), "#1A1A1A"
		cfg.Heading.Color = &accent
		cfg.H1.Color, cfg.H1.BackgroundColor = &dark, &accent
		for _, h := range []*ansi.StyleBlock{&cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
			h.Prefix = ""
		}
		// Inline code just blue, like Claude Code: the dark style's red reads like an error, its padded gray box breaks up sentences.
		code := string(cBlue)
		cfg.Code.Color, cfg.Code.BackgroundColor, cfg.Code.Prefix, cfg.Code.Suffix = &code, nil, "", ""
		var err error
		if r, err = glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(w)); err != nil {
			return lipgloss.NewStyle().Width(w).Render(s)
		}
		mdRenderers[w] = r
	}
	out, err := r.Render(s)
	if err != nil {
		return lipgloss.NewStyle().Width(w).Render(s)
	}
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

// hang puts first before a block's first line and indents the rest by its width.
func hang(first, block string) string {
	pad := strings.Repeat(" ", lipgloss.Width(first))
	lines := strings.Split(block, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = first + lines[i]
		} else {
			lines[i] = pad + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// renderBlock draws one transcript block at width w; prev is the kind of the block before it.
func renderBlock(l Line, prev string, w int) string {
	wrap := func(st lipgloss.Style, s string, indent int) string { return st.Width(max(w-indent, 1)).Render(s) }
	switch l.Kind {
	case "you":
		text := l.Text
		if lines := strings.Split(text, "\n"); len(lines) > 12 {
			text = strings.Join(lines[:12], "\n") + fmt.Sprintf("\n… +%d lines", len(lines)-12)
		}
		return lipgloss.NewStyle().Background(cUserBg).Foreground(cText).Width(w).Padding(0, 1).Render("› " + text)
	case "text":
		return hang(lipgloss.NewStyle().Foreground(cText).Render("⏺ "), markdown(l.Text, max(w-2, 10)))
	case "think":
		return hang(sDim.Render("✻ "), wrap(sDim.Italic(true), l.Text, 2))
	case "tool":
		call := sBold.Render(l.Text)
		if l.Arg != "" {
			call += sDim.Render("(" + l.Arg + ")")
		}
		return hang(sGreen.Render("⏺ "), lipgloss.NewStyle().Width(max(w-2, 1)).Render(call))
	case "out", "err":
		st, first := sDim, "  ⎿  "
		if l.Kind == "err" {
			st = sRed
		}
		if prev == "out" || prev == "err" {
			first = "     "
		} else if prev != "tool" {
			first = "  ✗  "
		}
		return hang(sDim.Render(first), wrap(st, l.Text, 5))
	case "done":
		return hang(sGreen.Render("✓ "), wrap(sDim, l.Text, 2))
	case "fail":
		return hang(sRed.Render("✗ "), wrap(sRed, l.Text, 2))
	}
	return hang(sAccent.Render("※ "), wrap(sDim, l.Text, 2))
}

// render draws a transcript. Blocks are cached on the lines, which only ever get appended.
func render(lines []Line, w int) string {
	var b strings.Builder
	prev := ""
	for i := range lines {
		l := &lines[i]
		if l.rw != w {
			l.r, l.rw = renderBlock(*l, prev, w), w
		}
		if i > 0 && l.Kind != "out" && !(l.Kind == "err" && (prev == "tool" || prev == "out")) {
			b.WriteByte('\n') // a blank line between blocks, like Claude Code
		}
		b.WriteString(l.r)
		b.WriteByte('\n')
		prev = l.Kind
	}
	return b.String()
}

// panel is a rounded box with its title set into the top border.
func panel(title, body string, w, h int, focused bool) string {
	bc := cBorder
	if focused {
		bc = cAccent
	}
	bs := lipgloss.NewStyle().Foreground(bc)
	t := " " + xansi.Truncate(title, max(w-6, 1), "…") + " "
	top := bs.Render("╭─") + t + bs.Render(strings.Repeat("─", max(w-3-lipgloss.Width(t), 0))+"╮")
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder(), false, true, true, true).BorderForeground(bc).
		Padding(0, 1).Width(w - 2).Height(max(h-2, 0)).MaxHeight(h - 1)
	return top + "\n" + box.Render(body)
}
