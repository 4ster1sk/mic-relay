package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"sync"

	"github.com/gen2brain/malgo"

	"mic-relay/internal/protocol"
)

func preferredBackends() []malgo.Backend {
	switch runtime.GOOS {
	case "linux":
		// Prefer PulseAudio backend (PipeWire's pipewire-pulse compatibility layer)
		// so all PipeWire physical/virtual mics appear with readable names.
		return []malgo.Backend{malgo.BackendPulseaudio, malgo.BackendAlsa}
	case "windows":
		return []malgo.Backend{malgo.BackendWasapi}
	default:
		return nil
	}
}

// ListCaptureDevices enumerates available microphone input devices.
func ListCaptureDevices() ([]DeviceInfo, error) {
	ctx, err := malgo.InitContext(preferredBackends(), malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("audio context init failed: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("list capture devices: %w", err)
	}

	out := make([]DeviceInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, DeviceInfo{
			ID:        info.ID.String(),
			Name:      info.Name(),
			IsDefault: info.IsDefault != 0,
		})
	}
	return out, nil
}

// Capturer captures mono 48kHz float32 audio in 10ms (480-sample) chunks via malgo.
type Capturer struct {
	mu      sync.Mutex
	ctx     *malgo.AllocatedContext
	device  *malgo.Device
	running bool
}

func NewCapturer() *Capturer {
	return &Capturer{}
}

// Start opens the capture device matching deviceName (empty = system default)
// and delivers fixed 480-sample (10ms) mono 48kHz float32 frames to onFrame.
func (c *Capturer) Start(deviceName string, onFrame func(frame []float32)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}

	ctx, err := malgo.InitContext(preferredBackends(), malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("capture context init: %w", err)
	}

	devCfg := malgo.DefaultDeviceConfig(malgo.Capture)
	devCfg.Capture.Format = malgo.FormatF32
	devCfg.Capture.Channels = protocol.Channels
	devCfg.SampleRate = protocol.SampleRate
	devCfg.PeriodSizeInMilliseconds = 10
	devCfg.Periods = 2

	if deviceName != "" {
		infos, err := ctx.Devices(malgo.Capture)
		if err != nil {
			_ = ctx.Uninit()
			ctx.Free()
			return fmt.Errorf("入力デバイス一覧の取得に失敗: %w", err)
		}
		found := false
		for i := range infos {
			if infos[i].Name() == deviceName {
				id := infos[i].ID
				devCfg.Capture.DeviceID = id.Pointer()
				found = true
				break
			}
		}
		if !found {
			_ = ctx.Uninit()
			ctx.Free()
			return fmt.Errorf("指定された入力マイク %q が見つかりません", deviceName)
		}
	}

	// Accumulator inside audio callback -> exact 480-sample (10ms) frames
	const frameLen = protocol.FrameSamples10ms
	acc := make([]float32, 0, frameLen*2)
	frameBuf := make([]float32, frameLen)

	callbacks := malgo.DeviceCallbacks{
		Data: func(_ []byte, input []byte, framecount uint32) {
			n := int(framecount)
			if n <= 0 || len(input) < n*4 {
				return
			}
			for i := 0; i < n; i++ {
				bits := binary.LittleEndian.Uint32(input[i*4 : i*4+4])
				acc = append(acc, math.Float32frombits(bits))
			}
			for len(acc) >= frameLen {
				copy(frameBuf, acc[:frameLen])
				copy(acc, acc[frameLen:])
				acc = acc[:len(acc)-frameLen]
				if onFrame != nil {
					onFrame(frameBuf)
				}
			}
		},
	}

	dev, err := malgo.InitDevice(ctx.Context, devCfg, callbacks)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return fmt.Errorf("capture device init: %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		_ = ctx.Uninit()
		ctx.Free()
		return fmt.Errorf("capture device start: %w", err)
	}

	c.ctx = ctx
	c.device = dev
	c.running = true
	return nil
}

func (c *Capturer) Stop() {
	c.mu.Lock()
	dev := c.device
	ctx := c.ctx
	c.device = nil
	c.ctx = nil
	c.running = false
	c.mu.Unlock()

	if dev != nil {
		_ = dev.Stop()
		dev.Uninit()
	}
	if ctx != nil {
		_ = ctx.Uninit()
		ctx.Free()
	}
}

func (c *Capturer) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}
