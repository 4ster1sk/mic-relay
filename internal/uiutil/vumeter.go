package uiutil

import (
	"image/color"
	"math"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"mic-relay/internal/dsp"
)

// VUMeter is a lightweight Fyne widget rendering a real-time dB audio meter with peak hold.
type VUMeter struct {
	widget.BaseWidget
	mu         sync.RWMutex
	rmsDB      float32
	peakHoldDB float32
}

func NewVUMeter() *VUMeter {
	m := &VUMeter{
		rmsDB:      dsp.MinDB,
		peakHoldDB: dsp.MinDB,
	}
	m.ExtendBaseWidget(m)
	return m
}

func clampDB(db float32) float32 {
	if db < dsp.MinDB {
		return dsp.MinDB
	}
	if db > 0 {
		return 0
	}
	return db
}

func (m *VUMeter) SetLevels(rmsDB, peakHoldDB float32) {
	rmsDB = clampDB(rmsDB)
	peakHoldDB = clampDB(peakHoldDB)
	m.mu.Lock()
	if math.Abs(float64(rmsDB-m.rmsDB)) < 0.2 && math.Abs(float64(peakHoldDB-m.peakHoldDB)) < 0.2 {
		m.mu.Unlock()
		return
	}
	m.rmsDB = rmsDB
	m.peakHoldDB = peakHoldDB
	m.mu.Unlock()
	m.Refresh()
}

func (m *VUMeter) MinSize() fyne.Size {
	return fyne.NewSize(160, 18)
}

func (m *VUMeter) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.NRGBA{R: 0x0F, G: 0x17, B: 0x2A, A: 0xFF})
	bg.CornerRadius = 4
	bg.StrokeColor = color.NRGBA{R: 0x33, G: 0x41, B: 0x55, A: 0xFF}
	bg.StrokeWidth = 1

	bar := canvas.NewRectangle(color.NRGBA{R: 0x22, G: 0xC5, B: 0x5E, A: 0xFF})
	bar.CornerRadius = 3

	peak := canvas.NewRectangle(color.NRGBA{R: 0xFA, G: 0xCC, B: 0x15, A: 0xFF})

	return &vuMeterRenderer{
		meter: m,
		bg:    bg,
		bar:   bar,
		peak:  peak,
		objs:  []fyne.CanvasObject{bg, bar, peak},
	}
}

type vuMeterRenderer struct {
	meter *VUMeter
	bg    *canvas.Rectangle
	bar   *canvas.Rectangle
	peak  *canvas.Rectangle
	objs  []fyne.CanvasObject
	size  fyne.Size
}

func dbToNorm(db float32) float32 {
	if db <= dsp.MinDB {
		return 0
	}
	if db >= 0 {
		return 1
	}
	return (db - dsp.MinDB) / (0 - dsp.MinDB)
}

func (r *vuMeterRenderer) Layout(size fyne.Size) {
	r.size = size
	r.bg.Resize(size)
	r.bg.Move(fyne.NewPos(0, 0))
	r.updateBars()
}

func (r *vuMeterRenderer) updateBars() {
	r.meter.mu.RLock()
	rms := r.meter.rmsDB
	peakHold := r.meter.peakHoldDB
	r.meter.mu.RUnlock()

	innerW := r.size.Width - 4
	innerH := r.size.Height - 4
	if innerW < 0 {
		innerW = 0
	}
	if innerH < 0 {
		innerH = 0
	}

	norm := dbToNorm(rms)
	barW := innerW * norm
	r.bar.Move(fyne.NewPos(2, 2))
	r.bar.Resize(fyne.NewSize(barW, innerH))

	switch {
	case rms > -6:
		r.bar.FillColor = color.NRGBA{R: 0xEF, G: 0x44, B: 0x44, A: 0xFF} // Red
	case rms > -16:
		r.bar.FillColor = color.NRGBA{R: 0xFA, G: 0xCC, B: 0x15, A: 0xFF} // Yellow
	default:
		r.bar.FillColor = color.NRGBA{R: 0x22, G: 0xC5, B: 0x5E, A: 0xFF} // Green
	}

	pNorm := dbToNorm(peakHold)
	if pNorm > 0.01 {
		px := 2 + innerW*pNorm - 2
		if px < 2 {
			px = 2
		}
		r.peak.Show()
		r.peak.Move(fyne.NewPos(px, 2))
		r.peak.Resize(fyne.NewSize(3, innerH))
	} else {
		r.peak.Hide()
	}
}

func (r *vuMeterRenderer) MinSize() fyne.Size {
	return r.meter.MinSize()
}

func (r *vuMeterRenderer) Refresh() {
	r.updateBars()
	canvas.Refresh(r.bar)
	canvas.Refresh(r.peak)
}

func (r *vuMeterRenderer) Objects() []fyne.CanvasObject {
	return r.objs
}

func (r *vuMeterRenderer) Destroy() {}
