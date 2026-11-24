//go:build !linux

package audio

type VirtualMic struct{}

func NewVirtualMic() *VirtualMic { return &VirtualMic{} }
func (v *VirtualMic) Start() error { return nil }
func (v *VirtualMic) Stop()        {}
func (v *VirtualMic) IsActive() bool { return false }
func VirtualMicSupported() bool { return false }
