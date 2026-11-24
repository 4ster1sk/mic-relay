package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := Default()
	cfg.TX.NoiseSuppression = true
	cfg.TX.CodecMode = CodecModeOpus96
	cfg.TX.TargetHost = "100.64.0.5"
	cfg.TX.Presets = []PeerPreset{{Name: "VRC-Win", Host: "100.64.0.5", Port: 48101}}
	cfg.TX.VoiceChanger.Enabled = true
	cfg.TX.VoiceChanger.PitchSemitones = 11.5
	cfg.TX.VoiceChanger.FormantRatio = 1.24
	cfg.TX.VoiceChanger.ThroatFormant = 1.12
	cfg.TX.VoiceChanger.BrightFormant = 1.15
	cfg.TX.VoiceChanger.Breathiness = 0.38
	cfg.TX.VoiceChanger.Softness = 0.68
	cfg.TX.VoiceChanger.HighPassEnabled = true
	cfg.TX.VoiceChanger.HighPassHz = 205.0
	cfg.TX.VoiceChanger.DeEsser = 0.50
	cfg.RX.TargetBufferMs = 50
	cfg.MinimizeToTray = true

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.TX.CodecMode != CodecModeOpus96 || got.TX.TargetHost != "100.64.0.5" || len(got.TX.Presets) != 1 || got.RX.TargetBufferMs != 50 {
		t.Fatalf("loaded config mismatch: %+v", got)
	}
	vc := got.TX.VoiceChanger
	if !vc.Enabled || vc.PitchSemitones != 11.5 || vc.ThroatFormant != 1.12 || vc.BrightFormant != 1.15 ||
		vc.Breathiness != 0.38 || vc.HighPassHz != 205.0 || vc.DeEsser != 0.50 {
		t.Fatalf("loaded voice changer config mismatch: %+v", vc)
	}
}

func TestConfigLoadBackwardCompatibleLegacyVC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")

	// Legacy v2 JSON missing new v3 voice changer fields
	legacy := `{
  "tx": {
    "voice_changer": {
      "enabled": true,
      "pitch_semitones": 5.5,
      "formant_ratio": 1.20,
      "softness": 0.40,
      "high_pass_enabled": true
    }
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load legacy: %v", err)
	}
	vc := got.TX.VoiceChanger
	if !vc.Enabled || vc.PitchSemitones != 5.5 || vc.FormantRatio != 1.20 || vc.Softness != 0.40 {
		t.Fatalf("legacy VC fields were not preserved: %+v", vc)
	}
	def := Default().TX.VoiceChanger
	if vc.ThroatFormant != def.ThroatFormant || vc.BrightFormant != def.BrightFormant ||
		vc.Breathiness != def.Breathiness || vc.HighPassHz != def.HighPassHz || vc.DeEsser != def.DeEsser {
		t.Fatalf("missing v3 VC fields should inherit defaults: got=%+v wantDefaults=%+v", vc, def)
	}
}
