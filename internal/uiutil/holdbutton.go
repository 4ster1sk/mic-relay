package uiutil

import (
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// HoldButton is a push-to-talk button that fires callbacks on mouse press and release.
type HoldButton struct {
	widget.Button
	OnPress   func()
	OnRelease func()
}

func NewHoldButton(label string, onPress, onRelease func()) *HoldButton {
	b := &HoldButton{
		OnPress:   onPress,
		OnRelease: onRelease,
	}
	b.Text = label
	b.ExtendBaseWidget(b)
	return b
}

func (b *HoldButton) MouseDown(ev *desktop.MouseEvent) {
	if b.Disabled() || ev.Button != desktop.MouseButtonPrimary {
		return
	}
	if b.OnPress != nil {
		b.OnPress()
	}
}

func (b *HoldButton) MouseUp(ev *desktop.MouseEvent) {
	if ev.Button != desktop.MouseButtonPrimary {
		return
	}
	if b.OnRelease != nil {
		b.OnRelease()
	}
}
