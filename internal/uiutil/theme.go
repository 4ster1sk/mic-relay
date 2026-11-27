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

// ThemeMode controls whether AppTheme follows the OS theme or forces dark/light.
type ThemeMode int

const (
	ThemeSystem ThemeMode = iota
	ThemeDark
	ThemeLight
)

// AppTheme provides a mi.tomadoi.com-inspired purple dark/light UI palette
// and automatic Japanese system font resolution.
type AppTheme struct {
	base   fyne.Theme
	mode   ThemeMode
	jpFont fyne.Resource
}

func NewAppTheme() fyne.Theme {
	return NewAppThemeForMode(ThemeDark)
}

func NewAppThemeForMode(mode ThemeMode) fyne.Theme {
	return &AppTheme{
		base:   theme.DefaultTheme(),
		mode:   mode,
		jpFont: findJapaneseFont(),
	}
}

// NewIdleTheme returns a lightweight theme without loading any external Japanese
// font bytes. Switching to this theme when the window is parked in the tray lets
// Go GC the ~13MB system font buffer and triggers Fyne's internal font cache reset.
func NewIdleTheme() fyne.Theme {
	return NewIdleThemeForMode(ThemeDark)
}

func NewIdleThemeForMode(mode ThemeMode) fyne.Theme {
	return &AppTheme{
		base: theme.DefaultTheme(),
		mode: mode,
	}
}

func (t *AppTheme) effectiveVariant(systemVariant fyne.ThemeVariant) fyne.ThemeVariant {
	switch t.mode {
	case ThemeDark:
		return theme.VariantDark
	case ThemeLight:
		return theme.VariantLight
	default:
		return systemVariant
	}
}

func (t *AppTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	eff := t.effectiveVariant(variant)
	if eff == theme.VariantLight {
		// Light palette inspired by mi.tomadoi.com ("むらさきいろのあかるいやつ")
		switch name {
		case theme.ColorNameBackground:
			return color.NRGBA{R: 0xE8, G: 0xE3, B: 0xEB, A: 0xFF}
		case theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground:
			return color.NRGBA{R: 0xF7, G: 0xF5, B: 0xF9, A: 0xFF}
		case theme.ColorNameInputBackground:
			return color.NRGBA{R: 0xF7, G: 0xF5, B: 0xF9, A: 0xFF}
		case theme.ColorNameInputBorder:
			return color.NRGBA{R: 0xC4, G: 0xB9, B: 0xCF, A: 0xFF}
		case theme.ColorNameButton:
			return color.NRGBA{R: 0xDC, G: 0xD4, B: 0xE3, A: 0xFF}
		case theme.ColorNameHeaderBackground:
			return color.NRGBA{R: 0xF0, G: 0xEC, B: 0xF2, A: 0xFF}
		case theme.ColorNamePrimary:
			return color.NRGBA{R: 0x8D, G: 0x34, B: 0x99, A: 0xFF}
		case theme.ColorNameFocus:
			return color.NRGBA{R: 0x8D, G: 0x34, B: 0x99, A: 0x4D}
		case theme.ColorNameSelection:
			return color.NRGBA{R: 0x8D, G: 0x34, B: 0x99, A: 0x33}
		case theme.ColorNameHover:
			return color.NRGBA{R: 0x8D, G: 0x34, B: 0x99, A: 0x1A}
		case theme.ColorNameForeground:
			return color.NRGBA{R: 0x3F, G: 0x3F, B: 0x3F, A: 0xFF}
		case theme.ColorNamePlaceHolder:
			return color.NRGBA{R: 0x7A, G: 0x70, B: 0x82, A: 0xFF}
		case theme.ColorNameSeparator:
			return color.NRGBA{R: 0xCE, G: 0xC5, B: 0xD6, A: 0xFF}
		case theme.ColorNameWarning:
			return color.NRGBA{R: 0xEC, G: 0xB6, B: 0x37, A: 0xFF}
		case theme.ColorNameError:
			return color.NRGBA{R: 0xEC, G: 0x41, B: 0x37, A: 0xFF}
		case theme.ColorNameSuccess:
			return color.NRGBA{R: 0x86, G: 0xB3, B: 0x00, A: 0xFF}
		default:
			return t.base.Color(name, theme.VariantLight)
		}
	}

	// Dark palette inspired by mi.tomadoi.com ("むらさきいろのやつ")
	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0x1B, G: 0x16, B: 0x21, A: 0xFF}
	case theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground:
		return color.NRGBA{R: 0x2B, G: 0x24, B: 0x35, A: 0xFF}
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 0x14, G: 0x10, B: 0x1A, A: 0xFF}
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 0x3A, G: 0x31, B: 0x47, A: 0xFF}
	case theme.ColorNameButton:
		return color.NRGBA{R: 0x33, G: 0x2B, B: 0x3F, A: 0xFF}
	case theme.ColorNameHeaderBackground:
		return color.NRGBA{R: 0x23, G: 0x1D, B: 0x2B, A: 0xFF}
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 0x71, G: 0x39, B: 0x99, A: 0xFF}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 0x71, G: 0x39, B: 0x99, A: 0x66}
	case theme.ColorNameSelection:
		return color.NRGBA{R: 0x71, G: 0x39, B: 0x99, A: 0x4D}
	case theme.ColorNameHover:
		return color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0x14}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 0xDA, G: 0xDA, B: 0xDA, A: 0xFF}
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 0x9C, G: 0x92, B: 0xA8, A: 0xFF}
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 0x35, G: 0x2D, B: 0x42, A: 0xFF}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 0xEC, G: 0xB6, B: 0x37, A: 0xFF}
	case theme.ColorNameError:
		return color.NRGBA{R: 0xEC, G: 0x41, B: 0x37, A: 0xFF}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 0x86, G: 0xB3, B: 0x00, A: 0xFF}
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
