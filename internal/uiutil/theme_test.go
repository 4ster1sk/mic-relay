package uiutil

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

func TestIsValidSingleFaceFontRejectsCollectionsAndJunk(t *testing.T) {
	if isValidSingleFaceFont([]byte("ttcf\x00\x01\x00\x00\x00\x00\x00\x02")) {
		t.Fatalf("expected TTC font collection to be rejected")
	}
	if isValidSingleFaceFont([]byte("not a font at all")) {
		t.Fatalf("expected invalid font bytes to be rejected")
	}
	if isValidSingleFaceFont(nil) {
		t.Fatalf("expected nil bytes to be rejected")
	}
}

func TestNewAppThemeFontSafe(t *testing.T) {
	th := NewAppTheme()
	res := th.Font(fyne.TextStyle{})
	if res == nil {
		t.Fatalf("expected non-nil font resource (either JP TTF or default fallback)")
	}
	if !isValidSingleFaceFont(res.Content()) {
		t.Fatalf("resolved theme font failed single-face TTF validation")
	}
}

func TestAppThemeModesDarkLightAndSystem(t *testing.T) {
	darkBg := color.NRGBA{R: 0x1B, G: 0x16, B: 0x21, A: 0xFF}
	lightBg := color.NRGBA{R: 0xE8, G: 0xE3, B: 0xEB, A: 0xFF}
	darkAccent := color.NRGBA{R: 0x71, G: 0x39, B: 0x99, A: 0xFF}
	lightAccent := color.NRGBA{R: 0x8D, G: 0x34, B: 0x99, A: 0xFF}

	// Forced dark ignores system light variant
	td := NewIdleThemeForMode(ThemeDark)
	if got := td.Color(theme.ColorNameBackground, theme.VariantLight); got != darkBg {
		t.Fatalf("ThemeDark background = %#v, want %#v", got, darkBg)
	}
	if got := td.Color(theme.ColorNamePrimary, theme.VariantLight); got != darkAccent {
		t.Fatalf("ThemeDark primary = %#v, want %#v", got, darkAccent)
	}

	// Forced light ignores system dark variant
	tl := NewIdleThemeForMode(ThemeLight)
	if got := tl.Color(theme.ColorNameBackground, theme.VariantDark); got != lightBg {
		t.Fatalf("ThemeLight background = %#v, want %#v", got, lightBg)
	}
	if got := tl.Color(theme.ColorNamePrimary, theme.VariantDark); got != lightAccent {
		t.Fatalf("ThemeLight primary = %#v, want %#v", got, lightAccent)
	}

	// System mode follows passed variant
	ts := NewIdleThemeForMode(ThemeSystem)
	if got := ts.Color(theme.ColorNameBackground, theme.VariantDark); got != darkBg {
		t.Fatalf("ThemeSystem dark background = %#v, want %#v", got, darkBg)
	}
	if got := ts.Color(theme.ColorNameBackground, theme.VariantLight); got != lightBg {
		t.Fatalf("ThemeSystem light background = %#v, want %#v", got, lightBg)
	}
}
