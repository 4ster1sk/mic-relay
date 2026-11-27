package uiutil

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"os"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// AppTheme provides a clean dark UI palette and automatic Japanese system font resolution.
type AppTheme struct {
	base   fyne.Theme
	jpFont fyne.Resource
}

func NewAppTheme() fyne.Theme {
	return &AppTheme{
		base:   theme.DefaultTheme(),
		jpFont: findJapaneseFont(),
	}
}

// NewIdleTheme returns a lightweight theme without loading any external Japanese
// font bytes. Switching to this theme when the window is parked in the tray lets
// Go GC the ~13MB system font buffer and triggers Fyne's internal font cache reset.
func NewIdleTheme() fyne.Theme {
	return &AppTheme{
		base: theme.DefaultTheme(),
	}
}

func (t *AppTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0x17, G: 0x1E, B: 0x2E, A: 0xFF}
	case theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 0x1E, G: 0x29, B: 0x3B, A: 0xFF}
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 0x0F, G: 0x17, B: 0x2A, A: 0xFF}
	case theme.ColorNameButton:
		return color.NRGBA{R: 0x33, G: 0x41, B: 0x55, A: 0xFF}
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 0x25, G: 0x63, B: 0xEB, A: 0xFF}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 0xF1, G: 0xF5, B: 0xF9, A: 0xFF}
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 0x94, G: 0xA3, B: 0xB8, A: 0xFF}
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 0x33, G: 0x41, B: 0x55, A: 0xFF}
	default:
		return t.base.Color(name, theme.VariantDark)
	}
}

func (t *AppTheme) Font(style fyne.TextStyle) fyne.Resource {
	if t.jpFont != nil {
		return t.jpFont
	}
	return t.base.Font(style)
}

func (t *AppTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return t.base.Icon(name)
}

func (t *AppTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameText {
		return 13
	}
	return t.base.Size(name)
}

func findJapaneseFont() fyne.Resource {
	if envPath := os.Getenv("MIC_RELAY_FONT"); envPath != "" {
		if res := loadFontResource(envPath); res != nil {
			return res
		}
	}

	var candidates []string
	switch runtime.GOOS {
	case "windows":
		// Single-face TTF/OTF Japanese fonts only (Fyne rejects .ttc font collections)
		candidates = []string{
			`C:\Windows\Fonts\yugothm.ttf`,
			`C:\Windows\Fonts\yugothr.ttf`,
			`C:\Windows\Fonts\YuGothM.ttf`,
			`C:\Windows\Fonts\YuGothR.ttf`,
			`C:\Windows\Fonts\NotoSansJP-Regular.ttf`,
		}
	case "linux":
		// Single-face TTF/OTF Japanese fonts only (Fyne rejects .ttc font collections)
		candidates = []string{
			"/usr/share/fonts/truetype/noto/NotoSansJP-Regular.ttf",
			"/usr/share/fonts/opentype/noto/NotoSansCJKjp-Regular.otf",
			"/usr/share/fonts/opentype/ipafont-gothic/ipagp.ttf",
			"/usr/share/fonts/opentype/ipafont-gothic/ipag.ttf",
			"/usr/share/fonts/truetype/fonts-japanese-gothic.ttf",
			"/usr/share/fonts/truetype/vlgothic/VL-Gothic-Regular.ttf",
			"/usr/share/fonts/truetype/takao-gothic/TakaoPGothic.ttf",
			"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
			"/usr/share/fonts/truetype/biz-udgothic/BIZUDGothic-Regular.ttf",
		}
	}

	for _, p := range candidates {
		if res := loadFontResource(p); res != nil {
			return res
		}
	}
	return nil
}

func isValidSingleFaceFont(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	// Reject TrueType/OpenType font collections (.ttc / .otc) which Fyne does not support
	if bytes.HasPrefix(data, []byte("ttcf")) {
		return false
	}
	// Validate SFNT single-face header without full-table parsing to avoid a ~10MB+ temporary spike.
	tag := data[:4]
	isSFNT := bytes.Equal(tag, []byte{0x00, 0x01, 0x00, 0x00}) ||
		bytes.Equal(tag, []byte("OTTO")) ||
		bytes.Equal(tag, []byte("true")) ||
		bytes.Equal(tag, []byte("typ1"))
	if !isSFNT {
		return false
	}
	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	return numTables > 0 && len(data) >= 12+numTables*16
}

func loadFontResource(path string) fyne.Resource {
	data, err := os.ReadFile(path)
	if err != nil || !isValidSingleFaceFont(data) {
		return nil
	}
	return fyne.NewStaticResource("jp-font.ttf", data)
}
