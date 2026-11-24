//go:build linux

package audio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// VirtualMic manages a dynamic PipeWire virtual microphone created via pw-loopback.
type VirtualMic struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	cmd    *exec.Cmd
	active bool
}

func NewVirtualMic() *VirtualMic {
	return &VirtualMic{}
}

// Start spawns pw-loopback creating a Playback Sink ("Mic Relay Virtual Input")
// bridged to a Capture Source ("Mic Relay Virtual Mic").
func (v *VirtualMic) Start() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.active {
		return nil
	}

	pwPath, err := exec.LookPath("pw-loopback")
	if err != nil {
		return errors.New("pw-loopback が見つかりません (pipewire-bin パッケージをインストールしてください)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	captureProps := fmt.Sprintf(
		`media.class=Audio/Sink node.name=mic_relay_sink node.description="%s" audio.position=[MONO]`,
		PipeWireVirtualSinkName,
	)
	playbackProps := fmt.Sprintf(
		`media.class=Audio/Source node.name=mic_relay_mic node.description="%s" audio.position=[MONO]`,
		PipeWireVirtualMicName,
	)

	cmd := exec.CommandContext(ctx, pwPath,
		"--capture-props="+captureProps,
		"--playback-props="+playbackProps,
	)
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("pw-loopback の起動に失敗しました: %w", err)
	}

	v.cancel = cancel
	v.cmd = cmd
	v.active = true

	// Reap child process when terminated without clobbering a newer Start()
	go func(started *exec.Cmd) {
		_ = started.Wait()
		v.mu.Lock()
		if v.cmd == started {
			v.active = false
			v.cmd = nil
		}
		v.mu.Unlock()
	}(cmd)

	// Poll briefly (up to 2s) until PipeWire exposes the newly created virtual sink
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if devs, err := ListPlaybackDevices(); err == nil {
			for _, d := range devs {
				if d.Name == PipeWireVirtualSinkName {
					return nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (v *VirtualMic) Stop() {
	v.mu.Lock()
	cancel := v.cancel
	v.cancel = nil
	v.active = false
	v.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (v *VirtualMic) IsActive() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.active
}

// Supported returns true if pw-loopback is available on this Linux host.
func VirtualMicSupported() bool {
	_, err := exec.LookPath("pw-loopback")
	return err == nil
}
