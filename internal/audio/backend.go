package audio

import (
	"sort"
	"strings"
)

const (
	PipeWireVirtualSinkName = "Mic Relay Virtual Input"
	PipeWireVirtualMicName  = "Mic Relay Virtual Mic"
)

// DeviceInfo describes an audio input or output endpoint.
type DeviceInfo struct {
	ID            string
	Name          string
	IsDefault     bool
	IsVirtual     bool
	PriorityScore int
}

// ScoreVirtualCable scores playback devices so VB-Cable / Virtual Audio Cable / PipeWire Virtual Input
// appear at the top of the output device selector.
func ScoreVirtualCable(name string) int {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "cable input") && strings.Contains(n, "vb-audio"):
		return 100
	case strings.Contains(n, "cable input"):
		return 95
	case strings.Contains(n, "mic relay virtual input") || strings.Contains(n, "mic_relay_sink"):
		return 92
	case strings.Contains(n, "vb-audio"):
		return 85
	case strings.Contains(n, "voicemeeter input"):
		return 80
	case strings.Contains(n, "virtual audio cable"):
		return 75
	case strings.Contains(n, "line 1") && strings.Contains(n, "virtual"):
		return 70
	default:
		return 0
	}
}

// SortPlaybackDevices sorts devices by PriorityScore descending, then IsDefault, then Name.
func SortPlaybackDevices(devs []DeviceInfo) {
	for i := range devs {
		devs[i].PriorityScore = ScoreVirtualCable(devs[i].Name)
		devs[i].IsVirtual = devs[i].PriorityScore > 0
	}
	sort.SliceStable(devs, func(i, j int) bool {
		if devs[i].PriorityScore != devs[j].PriorityScore {
			return devs[i].PriorityScore > devs[j].PriorityScore
		}
		if devs[i].IsDefault != devs[j].IsDefault {
			return devs[i].IsDefault
		}
		return devs[i].Name < devs[j].Name
	})
}

// IsPotentialFeedbackLoop returns true if selecting txInputName while rxOutputName is active
// would likely feed our own RX virtual microphone back into TX (causing howling/echo).
func IsPotentialFeedbackLoop(txInputName, rxOutputName string, rxRunning bool) bool {
	if !rxRunning || txInputName == "" {
		return false
	}
	in := strings.ToLower(txInputName)
	out := strings.ToLower(rxOutputName)

	// Linux PipeWire virtual mic self-loop
	if strings.Contains(in, "mic relay virtual mic") || strings.Contains(in, "mic_relay_mic") {
		return true
	}
	// Windows VB-Cable self-loop (CABLE Output captured while CABLE Input is played)
	if strings.Contains(in, "cable output") && strings.Contains(out, "cable input") {
		return true
	}
	// Generic Virtual Audio Cable Line 1 self-loop
	if strings.Contains(in, "virtual audio cable") && strings.Contains(out, "virtual audio cable") {
		return true
	}
	return false
}
