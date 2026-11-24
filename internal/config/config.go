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

type VoiceChangerConfig struct {
	Enabled         bool    `json:"enabled"`
	PitchSemitones  float64 `json:"pitch_semitones"`
	FormantRatio    float64 `json:"formant_ratio"`
	ThroatFormant   float64 `json:"throat_formant"`
	BrightFormant   float64 `json:"bright_formant"`
	Breathiness     float64 `json:"breathiness"`
	Softness        float64 `json:"softness"`
	HighPassEnabled bool    `json:"high_pass_enabled"`
	HighPassHz      float64 `json:"high_pass_hz"`
	DeEsser         float64 `json:"de_esser"`
}

type TXConfig struct {
	InputDevice      string             `json:"input_device"`
	GainDB           float64            `json:"gain_db"`
	NoiseSuppression bool               `json:"noise_suppression"`
	NoiseGateEnabled bool               `json:"noise_gate_enabled"`
	GateThresholdDB  float64            `json:"gate_threshold_db"`
	PushToTalk       bool               `json:"push_to_talk"`
	VoiceChanger     VoiceChangerConfig `json:"voice_changer"`
	CodecMode        CodecMode          `json:"codec_mode"`
	TargetHost       string             `json:"target_host"`
	TargetPort       int                `json:"target_port"`
	Presets          []PeerPreset       `json:"presets"`
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
			VoiceChanger: VoiceChangerConfig{
				Enabled:         false,
				PitchSemitones:  7.5,
				FormantRatio:    1.18,
				ThroatFormant:   1.06,
				BrightFormant:   1.06,
				Breathiness:     0.22,
				Softness:        0.55,
				HighPassEnabled: true,
				HighPassHz:      110.0,
				DeEsser:         0.35,
			},
			CodecMode:  CodecModePCM,
			TargetPort: protocol.DefaultAudioPort,
			Presets:    []PeerPreset{},
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
	if cfg.TX.VoiceChanger.PitchSemitones < -12 || cfg.TX.VoiceChanger.PitchSemitones > 16 {
		cfg.TX.VoiceChanger.PitchSemitones = 7.5
	}
	if cfg.TX.VoiceChanger.FormantRatio < 0.75 || cfg.TX.VoiceChanger.FormantRatio > 1.45 {
		cfg.TX.VoiceChanger.FormantRatio = 1.18
	}
	if cfg.TX.VoiceChanger.ThroatFormant < 0.80 || cfg.TX.VoiceChanger.ThroatFormant > 1.30 {
		cfg.TX.VoiceChanger.ThroatFormant = 1.06
	}
	if cfg.TX.VoiceChanger.BrightFormant < 0.80 || cfg.TX.VoiceChanger.BrightFormant > 1.30 {
		cfg.TX.VoiceChanger.BrightFormant = 1.06
	}
	if cfg.TX.VoiceChanger.Breathiness < 0 || cfg.TX.VoiceChanger.Breathiness > 1 {
		cfg.TX.VoiceChanger.Breathiness = 0.22
	}
	if cfg.TX.VoiceChanger.Softness < 0 || cfg.TX.VoiceChanger.Softness > 1 {
		cfg.TX.VoiceChanger.Softness = 0.55
	}
	if cfg.TX.VoiceChanger.HighPassHz < 80 || cfg.TX.VoiceChanger.HighPassHz > 280 {
		cfg.TX.VoiceChanger.HighPassHz = 110.0
	}
	if cfg.TX.VoiceChanger.DeEsser < 0 || cfg.TX.VoiceChanger.DeEsser > 1 {
		cfg.TX.VoiceChanger.DeEsser = 0.35
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
