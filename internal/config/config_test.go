package config

import (
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
}
