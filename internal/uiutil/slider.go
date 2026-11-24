package uiutil

import (
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

var (
	_ fyne.Draggable    = (*Slider)(nil)
	_ fyne.Focusable    = (*Slider)(nil)
	_ fyne.Tappable     = (*Slider)(nil)
	_ fyne.Disableable  = (*Slider)(nil)
	_ desktop.Hoverable = (*Slider)(nil)
	_ desktop.Mouseable = (*Slider)(nil)
)

// Slider wraps widget.Slider and ignores drag events that did not start with a
// primary mouse press on this slider itself.
//
// In Fyne's desktop driver, holding a mouse button on another non-draggable
// widget (such as a button or checkbox) and passing the cursor over a slider
// can trigger Dragged() on the slider even though MouseDown() never occurred
// on it. Requiring an explicit MouseDown() on the slider prevents accidental
// value jumps.
type Slider struct {
	widget.Slider

	mu       sync.Mutex
	pressed  bool
	dragging bool
}

func NewSlider(min, max float64) *Slider {
	s := &Slider{}
	s.Min = min
	s.Max = max
	s.Step = 1
	s.Orientation = widget.Horizontal
	s.ExtendBaseWidget(s)
	return s
}

func (s *Slider) MouseDown(ev *desktop.MouseEvent) {
	if s.Disabled() || ev == nil || ev.Button != desktop.MouseButtonPrimary {
		return
	}
	s.mu.Lock()
	s.pressed = true
	s.dragging = false
	s.mu.Unlock()
}

func (s *Slider) MouseUp(ev *desktop.MouseEvent) {
	if ev != nil && ev.Button != desktop.MouseButtonPrimary {
		return
	}
	// Keep s.dragging intact here because Fyne invokes MouseUp before DragEnd
	// on release; DragEnd uses s.dragging to decide whether to fire OnChangeEnded.
	s.mu.Lock()
	s.pressed = false
	s.mu.Unlock()
}

func (s *Slider) MouseIn(ev *desktop.MouseEvent) {
	// Clear any stale pressed flag (e.g. if a previous press exited <2px before
	// dragging and released outside). Do not clear in MouseOut because Fyne fires
	// MouseOut before the first Dragged when a drag starts by crossing directly
	// onto an adjacent hoverable widget.
	s.mu.Lock()
	if !s.dragging {
		s.pressed = false
	}
	s.mu.Unlock()
	s.Slider.MouseIn(ev)
}

func (s *Slider) Dragged(ev *fyne.DragEvent) {
	s.mu.Lock()
	ok := s.pressed
	if ok {
		s.dragging = true
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	s.Slider.Dragged(ev)
}

func (s *Slider) DragEnd() {
	s.mu.Lock()
	wasDragging := s.dragging
	s.pressed = false
	s.dragging = false
	s.mu.Unlock()
	if !wasDragging {
		return
	}
	s.Slider.DragEnd()
}

func (s *Slider) Disable() {
	s.mu.Lock()
	s.pressed = false
	s.dragging = false
	s.mu.Unlock()
	s.Slider.Disable()
}
