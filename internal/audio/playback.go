package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	"github.com/gen2brain/malgo"

	"mic-relay/internal/protocol"
)

// ListPlaybackDevices enumerates playback (render) devices sorted with VB-Cable / Virtual Input first.
func ListPlaybackDevices() ([]DeviceInfo, error) {
	ctx, err := malgo.InitContext(preferredBackends(), malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("audio context init failed: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	infos, err := ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, fmt.Errorf("list playback devices: %w", err)
	}

	out := make([]DeviceInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, DeviceInfo{
			ID:        info.ID.String(),
			Name:      info.Name(),
			IsDefault: info.IsDefault != 0,
		})
	}
	SortPlaybackDevices(out)
	return out, nil
}

// Player outputs mono 48kHz float32 audio to a chosen playback endpoint (e.g. VB-Cable or PipeWire sink).
type Player struct {
	mu      sync.Mutex
	ctx     *malgo.AllocatedContext
	device  *malgo.Device
	running bool
}

func NewPlayer() *Player {
	return &Player{}
}

// Start opens the playback device matching deviceName (empty = system default)
// and repeatedly invokes fillSamples(buf) to pull audio for output.
func (p *Player) Start(deviceName string, fillSamples func(dst []float32)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return nil
	}

	ctx, err := malgo.InitContext(preferredBackends(), malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("playback context init: %w", err)
	}

	devCfg := malgo.DefaultDeviceConfig(malgo.Playback)
	devCfg.Playback.Format = malgo.FormatF32
	devCfg.Playback.Channels = protocol.Channels
	devCfg.SampleRate = protocol.SampleRate
	devCfg.PeriodSizeInMilliseconds = 10
	devCfg.Periods = 2

	if deviceName != "" {
		infos, err := ctx.Devices(malgo.Playback)
		if err != nil {
			_ = ctx.Uninit()
			ctx.Free()
			return fmt.Errorf("出力デバイス一覧の取得に失敗: %w", err)
		}
		found := false
		for i := range infos {
			if infos[i].Name() == deviceName {
				id := infos[i].ID
				devCfg.Playback.DeviceID = id.Pointer()
				found = true
				break
			}
		}
		if !found {
			_ = ctx.Uninit()
			ctx.Free()
			return fmt.Errorf("指定された出力デバイス %q が見つかりません", deviceName)
		}
	}

	var scratch []float32
	callbacks := malgo.DeviceCallbacks{
		Data: func(output []byte, _ []byte, framecount uint32) {
			n := int(framecount)
			if n <= 0 || len(output) < n*4 {
				return
			}
			if cap(scratch) < n {
				scratch = make([]float32, n)
			} else {
				scratch = scratch[:n]
			}
			if fillSamples != nil {
				fillSamples(scratch)
			} else {
				for i := range scratch {
					scratch[i] = 0
				}
			}
			for i := 0; i < n; i++ {
				binary.LittleEndian.PutUint32(output[i*4:i*4+4], math.Float32bits(scratch[i]))
			}
		},
	}

	dev, err := malgo.InitDevice(ctx.Context, devCfg, callbacks)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return fmt.Errorf("playback device init: %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		_ = ctx.Uninit()
		ctx.Free()
		return fmt.Errorf("playback device start: %w", err)
	}

	p.ctx = ctx
	p.device = dev
	p.running = true
	return nil
}

func (p *Player) Stop() {
	p.mu.Lock()
	dev := p.device
	ctx := p.ctx
	p.device = nil
	p.ctx = nil
	p.running = false
	p.mu.Unlock()

	if dev != nil {
		_ = dev.Stop()
		dev.Uninit()
	}
	if ctx != nil {
		_ = ctx.Uninit()
		ctx.Free()
	}
}

func (p *Player) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}
