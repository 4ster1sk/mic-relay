package uiutil

import (
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
		candidates = []string{
			`C:\Windows\Fonts\YuGothM.ttc`,
			`C:\Windows\Fonts\YuGothR.ttc`,
			`C:\Windows\Fonts\meiryo.ttc`,
			`C:\Windows\Fonts\msgothic.ttc`,
		}
	case "linux":
		candidates = []string{
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/truetype/noto/NotoSansJP-Regular.ttf",
			"/usr/share/fonts/opentype/ipafont-gothic/ipagp.ttf",
			"/usr/share/fonts/opentype/ipafont-gothic/ipag.ttf",
			"/usr/share/fonts/truetype/fonts-japanese-gothic.ttf",
			"/usr/share/fonts/truetype/vlgothic/VL-Gothic-Regular.ttf",
			"/usr/share/fonts/truetype/takao-gothic/TakaoPGothic.ttf",
			"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
		}
	case "darwin":
		candidates = []string{
			"/System/Library/Fonts/ヒラギノ角ゴシック W3.ttc",
			"/System/Library/Fonts/Hiragino Sans GB.ttc",
		}
	}

	for _, p := range candidates {
		if res := loadFontResource(p); res != nil {
			return res
		}
	}
	return nil
}

func loadFontResource(path string) fyne.Resource {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	return fyne.NewStaticResource("jp-font", data)
}
