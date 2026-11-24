package main

import (
	"fmt"
	"math"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"mic-relay/internal/dsp"
)

// monitorRing is a tiny thread-safe ring buffer used for low-latency local voice preview.
type monitorRing struct {
	mu  sync.Mutex
	buf []float32
}

func newMonitorRing() *monitorRing {
	return &monitorRing{buf: make([]float32, 0, 4800)}
}

func (m *monitorRing) Write(samples []float32) {
	m.mu.Lock()
	m.buf = append(m.buf, samples...)
	// Keep monitor latency tight (cap at ~60ms = 2880 samples @ 48kHz)
	const maxCap = 2880
	if len(m.buf) > maxCap {
		drop := len(m.buf) - maxCap
		copy(m.buf, m.buf[drop:])
		m.buf = m.buf[:maxCap]
	}
	m.mu.Unlock()
}

func (m *monitorRing) Read(dst []float32) {
	m.mu.Lock()
	n := len(dst)
	if len(m.buf) >= n {
		copy(dst, m.buf[:n])
		copy(m.buf, m.buf[n:])
		m.buf = m.buf[:len(m.buf)-n]
	} else {
		avail := len(m.buf)
		copy(dst[:avail], m.buf)
		for i := avail; i < n; i++ {
			dst[i] = 0
		}
		m.buf = m.buf[:0]
	}
	m.mu.Unlock()
}

func (m *monitorRing) Clear() {
	m.mu.Lock()
	m.buf = m.buf[:0]
	m.mu.Unlock()
}

func (ra *RelayApp) setVoiceChangerEnabled(on bool) {
	if ra.voiceChanger != nil {
		ra.voiceChanger.SetEnabled(on)
	}
	ra.cfgMu.Lock()
	ra.cfg.TX.VoiceChanger.Enabled = on
	ra.cfgMu.Unlock()
	ra.syncMenuState()
	ra.syncTrayState()
	ra.saveConfig()
}

func (ra *RelayApp) applyVoiceChangerPreset(p dsp.VoiceChangerPreset, enable bool) {
	if ra.voiceChanger != nil {
		ra.voiceChanger.SetParams(p.PitchSemitones, p.FormantRatio, p.Softness, p.HighPassEnabled)
		if enable {
			ra.voiceChanger.SetEnabled(true)
		}
	}
	ra.cfgMu.Lock()
	ra.cfg.TX.VoiceChanger.PitchSemitones = p.PitchSemitones
	ra.cfg.TX.VoiceChanger.FormantRatio = p.FormantRatio
	ra.cfg.TX.VoiceChanger.Softness = p.Softness
	ra.cfg.TX.VoiceChanger.HighPassEnabled = p.HighPassEnabled
	if enable {
		ra.cfg.TX.VoiceChanger.Enabled = true
	}
	ra.cfgMu.Unlock()
	ra.syncMenuState()
	ra.syncTrayState()
	ra.saveConfig()
}

func (ra *RelayApp) setLocalMonitorEnabled(on bool) error {
	if !on {
		ra.monitorEnabled.Store(false)
		ra.monitorPlayer.Stop()
		ra.monitorBuf.Clear()
		ra.syncMenuState()
		return nil
	}
	if !ra.capturer.IsRunning() {
		if err := ra.startMicMonitor(); err != nil {
			return err
		}
	}
	ra.monitorBuf.Clear()
	if err := ra.monitorPlayer.Start("", func(dst []float32) {
		ra.monitorBuf.Read(dst)
	}); err != nil {
		return err
	}
	ra.monitorEnabled.Store(true)
	ra.syncMenuState()
	return nil
}

func (ra *RelayApp) showVoiceChangerDialog() {
	ra.cfgMu.Lock()
	vcCfg := ra.cfg.TX.VoiceChanger
	ra.cfgMu.Unlock()

	enableCheck := widget.NewCheck("ボイスチェンジャーを有効にする", func(on bool) {
		ra.setVoiceChangerEnabled(on)
	})
	enableCheck.Checked = vcCfg.Enabled

	pitchVal := widget.NewLabel(fmt.Sprintf("%+.1f 半音", vcCfg.PitchSemitones))
	pitchSlider := widget.NewSlider(-12, 12)
	pitchSlider.Step = 0.5
	pitchSlider.Value = vcCfg.PitchSemitones

	formantVal := widget.NewLabel(fmt.Sprintf("%.2fx", vcCfg.FormantRatio))
	formantSlider := widget.NewSlider(0.75, 1.45)
	formantSlider.Step = 0.02
	formantSlider.Value = vcCfg.FormantRatio

	softVal := widget.NewLabel(fmt.Sprintf("%.0f%%", vcCfg.Softness*100))
	softSlider := widget.NewSlider(0, 100)
	softSlider.Step = 5
	softSlider.Value = vcCfg.Softness * 100

	hpCheck := widget.NewCheck("胸声カット HPF (145Hz) を有効にする", nil)
	hpCheck.Checked = vcCfg.HighPassEnabled

	updateParams := func() {
		p := math.Round(pitchSlider.Value*2) / 2
		f := math.Round(formantSlider.Value*100) / 100
		s := math.Round(softSlider.Value) / 100.0
		hp := hpCheck.Checked
		pitchVal.SetText(fmt.Sprintf("%+.1f 半音", p))
		formantVal.SetText(fmt.Sprintf("%.2fx", f))
		softVal.SetText(fmt.Sprintf("%.0f%%", s*100))
		if ra.voiceChanger != nil {
			ra.voiceChanger.SetParams(p, f, s, hp)
		}
		ra.cfgMu.Lock()
		ra.cfg.TX.VoiceChanger.PitchSemitones = p
		ra.cfg.TX.VoiceChanger.FormantRatio = f
		ra.cfg.TX.VoiceChanger.Softness = s
		ra.cfg.TX.VoiceChanger.HighPassEnabled = hp
		ra.cfgMu.Unlock()
		ra.syncMenuState()
	}

	pitchSlider.OnChanged = func(_ float64) { updateParams() }
	pitchSlider.OnChangeEnded = func(_ float64) { ra.saveConfig() }
	formantSlider.OnChanged = func(_ float64) { updateParams() }
	formantSlider.OnChangeEnded = func(_ float64) { ra.saveConfig() }
	softSlider.OnChanged = func(_ float64) { updateParams() }
	softSlider.OnChangeEnded = func(_ float64) { ra.saveConfig() }
	hpCheck.OnChanged = func(_ bool) {
		updateParams()
		ra.saveConfig()
	}

	applyPresetUI := func(p dsp.VoiceChangerPreset) {
		ra.applyVoiceChangerPreset(p, true)
		enableCheck.SetChecked(true)
		pitchSlider.SetValue(p.PitchSemitones)
		formantSlider.SetValue(p.FormantRatio)
		softSlider.SetValue(p.Softness * 100)
		hpCheck.SetChecked(p.HighPassEnabled)
	}

	presetsRow := container.NewGridWithColumns(3,
		widget.NewButton("女声ナチュラル", func() { applyPresetUI(dsp.PresetFemaleNatural) }),
		widget.NewButton("女声高め", func() { applyPresetUI(dsp.PresetFemaleHigh) }),
		widget.NewButton("中性・少年声", func() { applyPresetUI(dsp.PresetAndrogynous) }),
	)

	var monitorCheck *widget.Check
	monitorCheck = widget.NewCheck("ローカル試聴 (自分のスピーカー/ヘッドホンで確認)", func(on bool) {
		if err := ra.setLocalMonitorEnabled(on); err != nil {
			monitorCheck.SetChecked(false)
			ra.showError("ローカル試聴エラー", err)
		}
	})
	monitorCheck.Checked = ra.monitorEnabled.Load()

	hint := widget.NewLabel("※ ハウリング防止のためヘッドホン・イヤホンでの試聴を推奨します")
	hint.Wrapping = fyne.TextWrapWord

	body := container.NewVBox(
		enableCheck,
		presetsRow,
		widget.NewSeparator(),
		container.NewBorder(nil, nil, widget.NewLabel("ピッチ (音程)"), pitchVal, pitchSlider),
		container.NewBorder(nil, nil, widget.NewLabel("フォルマント (声質)"), formantVal, formantSlider),
		container.NewBorder(nil, nil, widget.NewLabel("柔らかさ (息・丸み)"), softVal, softSlider),
		hpCheck,
		widget.NewSeparator(),
		monitorCheck,
		hint,
	)

	d := dialog.NewCustom("ボイチェン詳細調整・ローカル試聴", "閉じる", body, ra.win)
	d.Resize(fyne.NewSize(470, 400))
	d.Show()
}
