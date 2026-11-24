package uiutil

import (
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

var (
	_ desktop.Mouseable = (*HoldButton)(nil)
	_ desktop.Hoverable = (*HoldButton)(nil)
	_ fyne.Draggable    = (*HoldButton)(nil)
)

// HoldButton is a push-to-talk button that fires callbacks on mouse press and release.
// Implementing fyne.Draggable ensures Fyne delivers DragEnd() even when the user drags
// the pointer outside the button or outside the application window before releasing.
type HoldButton struct {
	widget.Button
	OnPress   func()
	OnRelease func()

	mu       sync.Mutex
	pressed  bool
	dragging bool
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
	if b.Disabled() || ev == nil || ev.Button != desktop.MouseButtonPrimary {
		return
	}
	b.mu.Lock()
	if b.pressed {
		b.mu.Unlock()
		return
	}
	b.pressed = true
	b.dragging = false
	onPress := b.OnPress
	b.mu.Unlock()

	if onPress != nil {
		onPress()
	}
}

func (b *HoldButton) MouseUp(ev *desktop.MouseEvent) {
	if ev != nil && ev.Button != desktop.MouseButtonPrimary {
		return
	}
	b.Release()
}

func (b *HoldButton) MouseOut() {
	b.Button.MouseOut()

	// Fail-safe: if the pointer leaves the button before Fyne starts a drag
	// (e.g. <2px edge exit), Fyne will not deliver MouseUp or DragEnd on outside
	// release. Releasing here prevents PTT from ever sticking open.
	b.mu.Lock()
	shouldRelease := b.pressed && !b.dragging
	b.mu.Unlock()
	if shouldRelease {
		b.Release()
	}
}

func (b *HoldButton) Dragged(_ *fyne.DragEvent) {
	b.mu.Lock()
	if b.pressed {
		b.dragging = true
	}
	b.mu.Unlock()
}

func (b *HoldButton) DragEnd() {
	b.Release()
}

func (b *HoldButton) Disable() {
	b.Release()
	b.Button.Disable()
}

// Release transitions the button out of the pressed state and fires OnRelease once.
func (b *HoldButton) Release() {
	b.mu.Lock()
	if !b.pressed {
		b.dragging = false
		b.mu.Unlock()
		return
	}
	b.pressed = false
	b.dragging = false
	onRelease := b.OnRelease
	b.mu.Unlock()

	if onRelease != nil {
		onRelease()
	}
}
