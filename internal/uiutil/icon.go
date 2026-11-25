package uiutil

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"

	"fyne.io/fyne/v2"
)

var (
	iconOnce       sync.Once
	appIcon        fyne.Resource
	trayIconTXOnce sync.Once
	trayIconTX     fyne.Resource
	trayIconRXOnce sync.Once
	trayIconRX     fyne.Resource
)

func renderBaseIcon() *image.NRGBA {
	const size = 64
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	bg := color.NRGBA{R: 0x25, G: 0x63, B: 0xEB, A: 0xFF}
	fg := color.NRGBA{R: 0xF8, G: 0xFA, B: 0xFC, A: 0xFF}
	accent := color.NRGBA{R: 0x34, G: 0xD3, B: 0x99, A: 0xFF}

	// Rounded circle background
	cx, cy := float64(size)/2, float64(size)/2
	r2 := 29.0 * 29.0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			if dx*dx+dy*dy <= r2 {
				img.SetNRGBA(x, y, bg)
			}
		}
	}

	// Microphone capsule (center)
	for y := 14; y <= 35; y++ {
		for x := 26; x <= 38; x++ {
			img.SetNRGBA(x, y, fg)
		}
	}
	// Stand stem + base
	for y := 39; y <= 49; y++ {
		for x := 30; x <= 34; x++ {
			img.SetNRGBA(x, y, fg)
		}
	}
	for y := 47; y <= 51; y++ {
		for x := 22; x <= 42; x++ {
			img.SetNRGBA(x, y, fg)
		}
	}
	// Bidirectional signal dots (left & right)
	for y := 22; y <= 28; y++ {
		for x := 14; x <= 20; x++ {
			img.SetNRGBA(x, y, accent)
		}
		for x := 44; x <= 50; x++ {
			img.SetNRGBA(x, y, accent)
		}
	}

	return img
}

func drawBottomRightBadge(img *image.NRGBA, dot color.NRGBA) {
	const (
		cx      = 50.0
		cy      = 50.0
		outerR2 = 12.5 * 12.5
		innerR2 = 9.5 * 9.5
	)
	border := color.NRGBA{R: 0x0F, G: 0x17, B: 0x2A, A: 0xFF}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			d2 := dx*dx + dy*dy
			if d2 <= innerR2 {
				img.SetNRGBA(x, y, dot)
			} else if d2 <= outerR2 {
				img.SetNRGBA(x, y, border)
			}
		}
	}
}

func encodeIconResource(name string, img *image.NRGBA) fyne.Resource {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return fyne.NewStaticResource(name, buf.Bytes())
}

// AppIcon generates a crisp 64x64 microphone relay icon as a PNG fyne.Resource.
func AppIcon() fyne.Resource {
	iconOnce.Do(func() {
		appIcon = encodeIconResource("mic-relay-icon.png", renderBaseIcon())
	})
	return appIcon
}

// TrayIcon returns the tray icon for the current TX/RX running state.
// When TX or RX is active, a bottom-right circular status dot is overlaid.
func TrayIcon(txRunning, rxRunning bool) fyne.Resource {
	switch {
	case txRunning:
		trayIconTXOnce.Do(func() {
			img := renderBaseIcon()
			drawBottomRightBadge(img, color.NRGBA{R: 0xEF, G: 0x44, B: 0x44, A: 0xFF}) // Red (TX live)
			trayIconTX = encodeIconResource("mic-relay-icon-tx.png", img)
		})
		return trayIconTX
	case rxRunning:
		trayIconRXOnce.Do(func() {
			img := renderBaseIcon()
			drawBottomRightBadge(img, color.NRGBA{R: 0x22, G: 0xC5, B: 0x5E, A: 0xFF}) // Green (RX active)
			trayIconRX = encodeIconResource("mic-relay-icon-rx.png", img)
		})
		return trayIconRX
	default:
		return AppIcon()
	}
}
