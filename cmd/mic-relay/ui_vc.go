package main

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"mic-relay/internal/config"
	"mic-relay/internal/dsp"
	"mic-relay/internal/uiutil"
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

func configPresetToDSP(p config.VoiceChangerPreset) dsp.VoiceChangerPreset {
	return dsp.VoiceChangerPreset{
		Name: p.Name,
		VoiceChangerParams: dsp.VoiceChangerParams{
			PitchSemitones:  p.PitchSemitones,
			FormantRatio:    p.FormantRatio,
			ThroatFormant:   p.ThroatFormant,
			BrightFormant:   p.BrightFormant,
			Breathiness:     p.Breathiness,
			Softness:        p.Softness,
			HighPassEnabled: p.HighPassEnabled,
			HighPassHz:      p.HighPassHz,
			DeEsser:         p.DeEsser,
		}.Clamp(),
	}
}

func (ra *RelayApp) customVoiceChangerPresets() []config.VoiceChangerPreset {
	ra.cfgMu.Lock()
	defer ra.cfgMu.Unlock()
	if len(ra.cfg.TX.VoiceChanger.Presets) == 0 {
		return nil
	}
	out := make([]config.VoiceChangerPreset, len(ra.cfg.TX.VoiceChanger.Presets))
	copy(out, ra.cfg.TX.VoiceChanger.Presets)
	return out
}

func (ra *RelayApp) saveVoiceChangerPreset(name string, params dsp.VoiceChangerParams) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("プリセット名を入力してください")
	}
	clamped := params.Clamp()
	entry := config.VoiceChangerPreset{
		Name:            name,
		PitchSemitones:  clamped.PitchSemitones,
		FormantRatio:    clamped.FormantRatio,
		ThroatFormant:   clamped.ThroatFormant,
		BrightFormant:   clamped.BrightFormant,
		Breathiness:     clamped.Breathiness,
		Softness:        clamped.Softness,
		HighPassEnabled: clamped.HighPassEnabled,
		HighPassHz:      clamped.HighPassHz,
		DeEsser:         clamped.DeEsser,
	}

	overwritten := false
	ra.cfgMu.Lock()
	for i := range ra.cfg.TX.VoiceChanger.Presets {
		if ra.cfg.TX.VoiceChanger.Presets[i].Name == name {
			ra.cfg.TX.VoiceChanger.Presets[i] = entry
			overwritten = true
			break
		}
	}
	if !overwritten {
		ra.cfg.TX.VoiceChanger.Presets = append(ra.cfg.TX.VoiceChanger.Presets, entry)
	}
	ra.cfgMu.Unlock()

	ra.saveConfig()
	ra.rebuildVCMenu()
	return overwritten, nil
}

func (ra *RelayApp) deleteVoiceChangerPreset(name string) bool {
	removed := false
	ra.cfgMu.Lock()
	presets := ra.cfg.TX.VoiceChanger.Presets
	for i := range presets {
		if presets[i].Name == name {
			ra.cfg.TX.VoiceChanger.Presets = append(presets[:i], presets[i+1:]...)
			removed = true
			break
		}
	}
	ra.cfgMu.Unlock()

	if removed {
		ra.saveConfig()
		ra.rebuildVCMenu()
	}
	return removed
}

func (ra *RelayApp) applyVoiceChangerPreset(p dsp.VoiceChangerPreset, enable bool) {
	params := p.VoiceChangerParams.Clamp()
	if ra.voiceChanger != nil {
		ra.voiceChanger.SetConfig(params)
		if enable {
			ra.voiceChanger.SetEnabled(true)
		}
	}
	ra.cfgMu.Lock()
	ra.cfg.TX.VoiceChanger.PitchSemitones = params.PitchSemitones
	ra.cfg.TX.VoiceChanger.FormantRatio = params.FormantRatio
	ra.cfg.TX.VoiceChanger.ThroatFormant = params.ThroatFormant
	ra.cfg.TX.VoiceChanger.BrightFormant = params.BrightFormant
	ra.cfg.TX.VoiceChanger.Breathiness = params.Breathiness
	ra.cfg.TX.VoiceChanger.Softness = params.Softness
	ra.cfg.TX.VoiceChanger.HighPassEnabled = params.HighPassEnabled
	ra.cfg.TX.VoiceChanger.HighPassHz = params.HighPassHz
	ra.cfg.TX.VoiceChanger.DeEsser = params.DeEsser
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

func vcSliderRow(title string, valLabel *widget.Label, slider *uiutil.Slider) fyne.CanvasObject {
	lbl := widget.NewLabel(title)
	valLabel.Alignment = fyne.TextAlignTrailing
	leftWrap := container.NewGridWrap(fyne.NewSize(172, 28), lbl)
	rightWrap := container.NewGridWrap(fyne.NewSize(86, 28), valLabel)
	return container.NewBorder(nil, nil, leftWrap, rightWrap, slider)
}

func vcSectionHeader(title string) *widget.Label {
	lbl := widget.NewLabel(title)
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	return lbl
}

func (ra *RelayApp) buildVoiceChangerContent() fyne.CanvasObject {
	ra.cfgMu.Lock()
	vcCfg := ra.cfg.TX.VoiceChanger
	ra.cfgMu.Unlock()

	enableCheck := widget.NewCheck("ボイスチェンジャーを有効にする", func(on bool) {
		ra.setVoiceChangerEnabled(on)
	})
	enableCheck.Checked = vcCfg.Enabled

	// --- Core Pitch & Formant ---
	pitchVal := widget.NewLabel(fmt.Sprintf("%+.1f 半音", vcCfg.PitchSemitones))
	pitchSlider := uiutil.NewSlider(-12, 16)
	pitchSlider.Step = 0.5
	pitchSlider.Value = vcCfg.PitchSemitones

	formantVal := widget.NewLabel(fmt.Sprintf("%.2fx", vcCfg.FormantRatio))
	formantSlider := uiutil.NewSlider(0.75, 1.45)
	formantSlider.Step = 0.01
	formantSlider.Value = vcCfg.FormantRatio

	// --- Independent F1 / F2 Female Formant Shaping ---
	throatPct := math.Round((vcCfg.ThroatFormant - 1.0) * 100)
	throatVal := widget.NewLabel(fmt.Sprintf("%+.0f%%", throatPct))
	throatSlider := uiutil.NewSlider(-20, 30)
	throatSlider.Step = 1
	throatSlider.Value = throatPct

	brightPct := math.Round((vcCfg.BrightFormant - 1.0) * 100)
	brightVal := widget.NewLabel(fmt.Sprintf("%+.0f%%", brightPct))
	brightSlider := uiutil.NewSlider(-20, 30)
	brightSlider.Step = 1
	brightSlider.Value = brightPct

	// --- Voice Quality: Breathiness, Softness, De-Esser, Variable HPF ---
	breathVal := widget.NewLabel(fmt.Sprintf("%.0f%%", vcCfg.Breathiness*100))
	breathSlider := uiutil.NewSlider(0, 100)
	breathSlider.Step = 2
	breathSlider.Value = vcCfg.Breathiness * 100

	softVal := widget.NewLabel(fmt.Sprintf("%.0f%%", vcCfg.Softness*100))
	softSlider := uiutil.NewSlider(0, 100)
	softSlider.Step = 2
	softSlider.Value = vcCfg.Softness * 100

	deEssVal := widget.NewLabel(fmt.Sprintf("%.0f%%", vcCfg.DeEsser*100))
	deEssSlider := uiutil.NewSlider(0, 100)
	deEssSlider.Step = 2
	deEssSlider.Value = vcCfg.DeEsser * 100

	hpCheck := widget.NewCheck("胸声カット HPF", nil)
	hpCheck.Checked = vcCfg.HighPassEnabled

	hpHzVal := widget.NewLabel(fmt.Sprintf("%.0f Hz", vcCfg.HighPassHz))
	hpHzVal.Alignment = fyne.TextAlignTrailing
	hpHzSlider := uiutil.NewSlider(80, 280)
	hpHzSlider.Step = 5
	hpHzSlider.Value = vcCfg.HighPassHz
	if !vcCfg.HighPassEnabled {
		hpHzSlider.Disable()
	}

	suppressCallbacks := false
	currentParams := func() dsp.VoiceChangerParams {
		return dsp.VoiceChangerParams{
			PitchSemitones:  math.Round(pitchSlider.Value*2) / 2,
			FormantRatio:    math.Round(formantSlider.Value*100) / 100,
			ThroatFormant:   1.0 + math.Round(throatSlider.Value)/100.0,
			BrightFormant:   1.0 + math.Round(brightSlider.Value)/100.0,
			Breathiness:     math.Round(breathSlider.Value) / 100.0,
			Softness:        math.Round(softSlider.Value) / 100.0,
			HighPassEnabled: hpCheck.Checked,
			HighPassHz:      math.Round(hpHzSlider.Value/5) * 5,
			DeEsser:         math.Round(deEssSlider.Value) / 100.0,
		}.Clamp()
	}
	updateParams := func() {
		if suppressCallbacks {
			return
		}
		params := currentParams()

		pitchVal.SetText(fmt.Sprintf("%+.1f 半音", params.PitchSemitones))
		formantVal.SetText(fmt.Sprintf("%.2fx", params.FormantRatio))
		throatVal.SetText(fmt.Sprintf("%+.0f%%", (params.ThroatFormant-1.0)*100))
		brightVal.SetText(fmt.Sprintf("%+.0f%%", (params.BrightFormant-1.0)*100))
		breathVal.SetText(fmt.Sprintf("%.0f%%", params.Breathiness*100))
		softVal.SetText(fmt.Sprintf("%.0f%%", params.Softness*100))
		deEssVal.SetText(fmt.Sprintf("%.0f%%", params.DeEsser*100))
		hpHzVal.SetText(fmt.Sprintf("%.0f Hz", params.HighPassHz))

		if params.HighPassEnabled {
			hpHzSlider.Enable()
		} else {
			hpHzSlider.Disable()
		}

		if ra.voiceChanger != nil {
			ra.voiceChanger.SetConfig(params)
		}
		ra.cfgMu.Lock()
		ra.cfg.TX.VoiceChanger.PitchSemitones = params.PitchSemitones
		ra.cfg.TX.VoiceChanger.FormantRatio = params.FormantRatio
		ra.cfg.TX.VoiceChanger.ThroatFormant = params.ThroatFormant
		ra.cfg.TX.VoiceChanger.BrightFormant = params.BrightFormant
		ra.cfg.TX.VoiceChanger.Breathiness = params.Breathiness
		ra.cfg.TX.VoiceChanger.Softness = params.Softness
		ra.cfg.TX.VoiceChanger.HighPassEnabled = params.HighPassEnabled
		ra.cfg.TX.VoiceChanger.HighPassHz = params.HighPassHz
		ra.cfg.TX.VoiceChanger.DeEsser = params.DeEsser
		ra.cfgMu.Unlock()
		ra.syncMenuState()
	}

	sliders := []*uiutil.Slider{
		pitchSlider, formantSlider,
		throatSlider, brightSlider,
		breathSlider, softSlider, deEssSlider, hpHzSlider,
	}
	for _, sl := range sliders {
		sl.OnChanged = func(_ float64) { updateParams() }
		sl.OnChangeEnded = func(_ float64) { ra.saveConfig() }
	}
	hpCheck.OnChanged = func(_ bool) {
		updateParams()
		ra.saveConfig()
	}

	applyPresetUI := func(p dsp.VoiceChangerPreset) {
		ra.applyVoiceChangerPreset(p, true)
		params := p.VoiceChangerParams.Clamp()
		suppressCallbacks = true
		enableCheck.SetChecked(true)
		pitchSlider.SetValue(params.PitchSemitones)
		formantSlider.SetValue(params.FormantRatio)
		throatSlider.SetValue(math.Round((params.ThroatFormant - 1.0) * 100))
		brightSlider.SetValue(math.Round((params.BrightFormant - 1.0) * 100))
		breathSlider.SetValue(params.Breathiness * 100)
		softSlider.SetValue(params.Softness * 100)
		deEssSlider.SetValue(params.DeEsser * 100)
		hpCheck.SetChecked(params.HighPassEnabled)
		hpHzSlider.SetValue(params.HighPassHz)
		suppressCallbacks = false
		updateParams()
	}

	presetsGrid := container.NewGridWithColumns(2,
		widget.NewButton("女声ナチュラル", func() { applyPresetUI(dsp.PresetFemaleNatural) }),
		widget.NewButton("女声高め・カワボ", func() { applyPresetUI(dsp.PresetFemaleHigh) }),
		widget.NewButton("お姉さん・ウィスパー", func() { applyPresetUI(dsp.PresetFemaleWhisper) }),
		widget.NewButton("中性・少年声", func() { applyPresetUI(dsp.PresetAndrogynous) }),
		widget.NewButton("全パラメータ最大 (MAX)", func() { applyPresetUI(dsp.PresetMax) }),
	)

	presetNameEntry := widget.NewEntry()
	presetNameEntry.SetPlaceHolder("新しいプリセット名を入力...")
	presetStatusLabel := widget.NewLabel("")
	presetStatusLabel.Truncation = fyne.TextTruncateEllipsis
	customPresetsBox := container.NewVBox()

	var refreshCustomPresetsUI func()
	refreshCustomPresetsUI = func() {
		customs := ra.customVoiceChangerPresets()
		objs := make([]fyne.CanvasObject, 0, len(customs)+1)
		if len(customs) == 0 {
			emptyLbl := widget.NewLabel("保存済みのカスタムプリセットはありません")
			objs = append(objs, emptyLbl)
		} else {
			for _, p := range customs {
				presetCfg := p
				applyBtn := widget.NewButton(presetCfg.Name, func() {
					applyPresetUI(configPresetToDSP(presetCfg))
					presetNameEntry.SetText(presetCfg.Name)
					presetStatusLabel.SetText(fmt.Sprintf("プリセット「%s」を適用しました", presetCfg.Name))
				})
				delBtn := widget.NewButton("削除", func() {
					if ra.deleteVoiceChangerPreset(presetCfg.Name) {
						presetStatusLabel.SetText(fmt.Sprintf("プリセット「%s」を削除しました", presetCfg.Name))
						refreshCustomPresetsUI()
					}
				})
				objs = append(objs, container.NewBorder(nil, nil, nil, delBtn, applyBtn))
			}
		}
		customPresetsBox.Objects = objs
		customPresetsBox.Refresh()
	}
	refreshCustomPresetsUI()

	doSavePreset := func() {
		name := strings.TrimSpace(presetNameEntry.Text)
		overwritten, err := ra.saveVoiceChangerPreset(name, currentParams())
		if err != nil {
			presetStatusLabel.SetText(err.Error())
			return
		}
		if overwritten {
			presetStatusLabel.SetText(fmt.Sprintf("プリセット「%s」を上書き保存しました", name))
		} else {
			presetStatusLabel.SetText(fmt.Sprintf("プリセット「%s」を保存しました", name))
		}
		refreshCustomPresetsUI()
	}
	presetNameEntry.OnSubmitted = func(_ string) { doSavePreset() }
	savePresetBtn := widget.NewButton("現在の設定を保存", doSavePreset)

	customPresetRow := container.NewBorder(nil, nil, nil, savePresetBtn, presetNameEntry)

	var monitorCheck *widget.Check
	monitorCheck = widget.NewCheck("ローカル試聴 (自分のスピーカー/ヘッドホンで確認)", func(on bool) {
		if err := ra.setLocalMonitorEnabled(on); err != nil {
			monitorCheck.SetChecked(false)
			ra.showError("ローカル試聴エラー", err)
		}
	})
	monitorCheck.Checked = ra.monitorEnabled.Load()

	hint := widget.NewLabel("※ ハウリング防止のためヘッドホン・イヤホンでの試聴を推奨します。地声が低い場合はピッチを +9〜+14 半音付近まで上げて調整してください。")
	hint.Wrapping = fyne.TextWrapWord

	hpRow := container.NewBorder(
		nil, nil,
		container.NewGridWrap(fyne.NewSize(172, 28), hpCheck),
		container.NewGridWrap(fyne.NewSize(86, 28), hpHzVal),
		hpHzSlider,
	)

	body := container.NewVBox(
		enableCheck,
		presetsGrid,
		widget.NewSeparator(),
		vcSectionHeader("カスタムプリセット"),
		customPresetRow,
		presetStatusLabel,
		customPresetsBox,
		widget.NewSeparator(),
		vcSectionHeader("基本調整（音程・声道長）"),
		vcSliderRow("ピッチ (音程)", pitchVal, pitchSlider),
		vcSliderRow("フォルマント全体", formantVal, formantSlider),
		widget.NewSeparator(),
		vcSectionHeader("女声フォルマント微調整（F1 / F2）"),
		vcSliderRow("喉の細さ (F1 低域)", throatVal, throatSlider),
		vcSliderRow("声の明るさ (F2 高域)", brightVal, brightSlider),
		widget.NewSeparator(),
		vcSectionHeader("声質・息・仕上げ"),
		vcSliderRow("息成分 (ウィスパー)", breathVal, breathSlider),
		vcSliderRow("倍音の丸み (柔らかさ)", softVal, softSlider),
		vcSliderRow("歯擦音抑制 (サ行丸め)", deEssVal, deEssSlider),
		hpRow,
		widget.NewSeparator(),
		monitorCheck,
		hint,
	)

	return container.NewVScroll(body)
}

func (ra *RelayApp) showVoiceChangerDialog() {
	content := ra.buildVoiceChangerContent()
	d := dialog.NewCustom("ボイチェン詳細調整・ローカル試聴", "閉じる", content, ra.win)
	d.Resize(fyne.NewSize(580, 660))
	d.Show()
}
