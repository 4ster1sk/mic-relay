package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"mic-relay/internal/protocol"
)

type CodecMode string

const (
	CodecModePCM      CodecMode = "pcm"
	CodecModeOpus96   CodecMode = "opus96"
	CodecModeOpus48   CodecMode = "opus48"
)

type PeerPreset struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

type TXConfig struct {
	InputDevice      string       `json:"input_device"`
	GainDB           float64      `json:"gain_db"`
	NoiseSuppression bool         `json:"noise_suppression"`
	NoiseGateEnabled bool         `json:"noise_gate_enabled"`
	GateThresholdDB  float64      `json:"gate_threshold_db"`
	CodecMode        CodecMode    `json:"codec_mode"`
	TargetHost       string       `json:"target_host"`
	TargetPort       int          `json:"target_port"`
	Presets          []PeerPreset `json:"presets"`
}

type RXConfig struct {
	OutputDevice          string  `json:"output_device"`
	AutoCreatePipeWireMic bool    `json:"auto_create_pipewire_mic"`
	OutputVolumePercent   float64 `json:"output_volume_percent"`
	ListenPort            int     `json:"listen_port"`
	DisplayName           string  `json:"display_name"`
	TargetBufferMs        int     `json:"target_buffer_ms"`
}

type Config struct {
	TX             TXConfig `json:"tx"`
	RX             RXConfig `json:"rx"`
	MinimizeToTray bool     `json:"minimize_to_tray"`
	ActiveTab      string   `json:"active_tab"`
}

func Default() Config {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "mic-relay-" + runtime.GOOS
	}
	defaultTab := "tx"
	if runtime.GOOS == "windows" {
		defaultTab = "rx"
	}
	return Config{
		TX: TXConfig{
			GainDB:           0.0,
			NoiseSuppression: true,
			NoiseGateEnabled: false,
			GateThresholdDB:  -45.0,
			CodecMode:        CodecModePCM,
			TargetPort:       protocol.DefaultAudioPort,
			Presets:          []PeerPreset{},
		},
		RX: RXConfig{
			AutoCreatePipeWireMic: runtime.GOOS == "linux",
			OutputVolumePercent:   100.0,
			ListenPort:            protocol.DefaultAudioPort,
			DisplayName:           hostname,
			TargetBufferMs:        30,
		},
		MinimizeToTray: true,
		ActiveTab:      defaultTab,
	}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mic-relay", "config.json"), nil
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}
	if cfg.TX.TargetPort <= 0 {
		cfg.TX.TargetPort = protocol.DefaultAudioPort
	}
	if cfg.RX.ListenPort <= 0 {
		cfg.RX.ListenPort = protocol.DefaultAudioPort
	}
	if cfg.RX.TargetBufferMs < 10 || cfg.RX.TargetBufferMs > 150 {
		cfg.RX.TargetBufferMs = 30
	}
	if cfg.TX.CodecMode == "" {
		cfg.TX.CodecMode = CodecModePCM
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
