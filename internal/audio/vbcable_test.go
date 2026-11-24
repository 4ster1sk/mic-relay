package audio

import "testing"

func TestSortPlaybackDevicesPrioritizesVBCable(t *testing.T) {
	devs := []DeviceInfo{
		{Name: "Speakers (Realtek High Definition Audio)", IsDefault: true},
		{Name: "Headphones (USB Audio)"},
		{Name: "CABLE Input (VB-Audio Virtual Cable)"},
		{Name: "VoiceMeeter Input (VB-Audio VoiceMeeter VAIO)"},
	}
	SortPlaybackDevices(devs)

	if devs[0].Name != "CABLE Input (VB-Audio Virtual Cable)" {
		t.Fatalf("expected VB-Cable first, got %q", devs[0].Name)
	}
	if !devs[0].IsVirtual {
		t.Fatalf("expected VB-Cable to be flagged IsVirtual=true")
	}
}

func TestIsPotentialFeedbackLoop(t *testing.T) {
	if !IsPotentialFeedbackLoop("Mic Relay Virtual Mic", "Mic Relay Virtual Input", true) {
		t.Fatalf("expected PipeWire virtual mic self-loop to be flagged")
	}
	if !IsPotentialFeedbackLoop("CABLE Output (VB-Audio Virtual Cable)", "CABLE Input (VB-Audio Virtual Cable)", true) {
		t.Fatalf("expected VB-Cable self-loop to be flagged")
	}
	if IsPotentialFeedbackLoop("HyperX QuadCast S", "CABLE Input (VB-Audio Virtual Cable)", true) {
		t.Fatalf("unexpected false positive for physical mic")
	}
	if IsPotentialFeedbackLoop("CABLE Output (VB-Audio Virtual Cable)", "CABLE Input (VB-Audio Virtual Cable)", false) {
		t.Fatalf("should not warn when RX is not running")
	}
}
