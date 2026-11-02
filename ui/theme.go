package ui

import (
	"image/color"
	"math/rand/v2"

	"charm.land/lipgloss/v2"
	"github.com/Gitguy268/needle/config"
)

// Styles are the lipgloss styles built from the config theme.
type Styles struct {
	Accent, Text, Dim, Select, Bold, Error lipgloss.Style
	// Cursor marks the selected row of the focused list.
	Cursor lipgloss.Style
	// Background and Foreground recolour the terminal; nil keeps its own.
	Background, Foreground color.Color
}

// NewStyles builds styles from a theme. Empty colours keep the terminal default.
func NewStyles(t config.Theme) Styles {
	fg := func(c string) lipgloss.Style {
		s := lipgloss.NewStyle()
		if c != "" {
			s = s.Foreground(lipgloss.Color(c))
		}
		return s
	}
	termColor := func(c string) color.Color {
		if c == "" {
			return nil
		}
		return lipgloss.Color(c)
	}
	return Styles{
		Accent: fg(t.Accent).Bold(true),
		Text:   fg(t.Text),
		Dim:    fg(t.Dim),
		Select: fg(t.Select).Bold(true),
		Bold:   fg(t.Text).Bold(true),
		Error:  fg("1"),
		Cursor: cursor(t.Select, t.Background),

		Background: termColor(t.Background),
		Foreground: termColor(t.Foreground),
	}
}

func cursor(c, text string) lipgloss.Style {
	if c == "" {
		return lipgloss.NewStyle().Reverse(true)
	}
	if text == "" {
		text = "0"
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(c)).Foreground(lipgloss.Color(text)).Bold(true)
}

// Quote picks one of the theme's lines, or "" when it has none.
func Quote(t config.Theme) string {
	if len(t.Quotes) == 0 {
		return ""
	}
	return t.Quotes[rand.IntN(len(t.Quotes))]
}
