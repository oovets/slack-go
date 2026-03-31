package gui

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// fixedWidthWrap wraps a child widget with a fixed minimum width and can be
// hidden by setting show=false (returns zero size).
type fixedWidthWrap struct {
	widget.BaseWidget
	child fyne.CanvasObject
	width float32
	show  bool
}

func newFixedWidthWrap(child fyne.CanvasObject, width float32) *fixedWidthWrap {
	w := &fixedWidthWrap{child: child, width: width, show: true}
	w.ExtendBaseWidget(w)
	return w
}

func (w *fixedWidthWrap) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.child)
}
func (w *fixedWidthWrap) MinSize() fyne.Size {
	if !w.show {
		return fyne.NewSize(0, 0)
	}
	return fyne.NewSize(w.width, w.child.MinSize().Height)
}

// subtleTapLabel is muted, small text that reads as metadata but behaves as a tap target (no button chrome).
type subtleTapLabel struct {
	widget.BaseWidget
	text  *canvas.Text
	onTap func()
}

func newSubtleTapLabel(label string, onTap func()) *subtleTapLabel {
	t := &subtleTapLabel{
		text:  canvas.NewText(label, color.NRGBA{R: 95, G: 100, B: 122, A: 210}),
		onTap: onTap,
	}
	t.text.TextSize = messageMetaActionTextSize()
	t.text.TextStyle = fyne.TextStyle{}
	t.ExtendBaseWidget(t)
	return t
}

func (t *subtleTapLabel) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.text)
}

func (t *subtleTapLabel) MinSize() fyne.Size {
	s := fyne.MeasureText(t.text.Text, t.text.TextSize, t.text.TextStyle)
	return fyne.NewSize(s.Width+4, s.Height+2)
}

func (t *subtleTapLabel) Tapped(_ *fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *subtleTapLabel) TappedSecondary(_ *fyne.PointEvent) {}

func (t *subtleTapLabel) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

// paneSurface is a transparent hit-test surface wrapping a chat pane's content.
// It captures taps (to focus the pane) and resize events (to trigger scroll-to-bottom).
type paneSurface struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	onActivate func()
	onResize   func()
}

func newPaneSurface(content fyne.CanvasObject, onActivate func(), onResize func()) *paneSurface {
	s := &paneSurface{content: content, onActivate: onActivate, onResize: onResize}
	s.ExtendBaseWidget(s)
	return s
}

func (s *paneSurface) CreateRenderer() fyne.WidgetRenderer {
	return &paneSurfaceRenderer{surface: s}
}

type paneSurfaceRenderer struct {
	surface  *paneSurface
	lastSize fyne.Size
}

func (r *paneSurfaceRenderer) Layout(size fyne.Size) {
	r.surface.content.Move(fyne.NewPos(0, 0))
	r.surface.content.Resize(size)
	if size != r.lastSize {
		r.lastSize = size
		if r.surface.onResize != nil {
			r.surface.onResize()
		}
	}
}

func (r *paneSurfaceRenderer) MinSize() fyne.Size {
	return r.surface.content.MinSize()
}

func (r *paneSurfaceRenderer) Refresh() {
	canvas.Refresh(r.surface.content)
}

func (r *paneSurfaceRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.surface.content}
}

func (r *paneSurfaceRenderer) Destroy() {}

func (s *paneSurface) Tapped(_ *fyne.PointEvent) {
	if s.onActivate != nil {
		s.onActivate()
	}
}
func (s *paneSurface) TappedSecondary(_ *fyne.PointEvent) {
	if s.onActivate != nil {
		s.onActivate()
	}
}
func (s *paneSurface) MouseIn(_ *desktop.MouseEvent) {
	if s.onActivate != nil {
		s.onActivate()
	}
}
func (s *paneSurface) MouseOut()                        {}
func (s *paneSurface) MouseMoved(_ *desktop.MouseEvent) {}

// sidebarHoverRow provides hover callbacks for list row content without adding visual chrome.
type sidebarHoverRow struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onHover func(bool)
}

func newSidebarHoverRow(content fyne.CanvasObject, onHover func(bool)) *sidebarHoverRow {
	r := &sidebarHoverRow{content: content, onHover: onHover}
	r.ExtendBaseWidget(r)
	return r
}

func (r *sidebarHoverRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.content)
}

func (r *sidebarHoverRow) MouseIn(_ *desktop.MouseEvent) {
	if r.onHover != nil {
		r.onHover(true)
	}
}

func (r *sidebarHoverRow) MouseOut() {
	if r.onHover != nil {
		r.onHover(false)
	}
}

func (r *sidebarHoverRow) MouseMoved(_ *desktop.MouseEvent) {}

// glyph is a small icon-like tap target using a text character.
type glyph struct {
	widget.BaseWidget
	text  *canvas.Text
	onTap func()
}

func newGlyph(label string, onTap func()) *glyph {
	g := &glyph{text: canvas.NewText(label, theme.Color(theme.ColorNameForeground)), onTap: onTap}
	g.text.TextSize = 10
	g.ExtendBaseWidget(g)
	return g
}

func (g *glyph) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(g.text) }
func (g *glyph) MinSize() fyne.Size {
	s := fyne.MeasureText(g.text.Text, g.text.TextSize, fyne.TextStyle{})
	return fyne.NewSize(s.Width+4, s.Height+2)
}
func (g *glyph) Tapped(_ *fyne.PointEvent) {
	if g.onTap != nil {
		g.onTap()
	}
}
func (g *glyph) TappedSecondary(_ *fyne.PointEvent) {}

// timestampToggleRow reveals a message timestamp on tap with no hover animation.
type timestampToggleRow struct {
	widget.BaseWidget
	host    *fyne.Container
	ts      *canvas.Text
	visible bool
}

func newTimestampToggleRow(content fyne.CanvasObject, timestamp string, alignRight bool) *timestampToggleRow {
	ts := canvas.NewText(strings.TrimSpace(timestamp), color.NRGBA{R: 100, G: 106, B: 130, A: 190})
	ts.TextSize = hoverTimestampTextSize()
	ts.Hide()
	tsRow := fyne.CanvasObject(ts)
	if alignRight {
		tsRow = alignOutgoingRow(ts, true)
	}
	host := container.NewVBox(tsRow, content)
	r := &timestampToggleRow{host: host, ts: ts}
	r.ExtendBaseWidget(r)
	return r
}

func (r *timestampToggleRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.host)
}

func (r *timestampToggleRow) Tapped(_ *fyne.PointEvent) {
	r.visible = !r.visible
	if r.visible {
		r.ts.Show()
	} else {
		r.ts.Hide()
	}
	r.host.Refresh()
}

func (r *timestampToggleRow) TappedSecondary(_ *fyne.PointEvent) {}

// quickSwitchEntry adds command-palette style keyboard handling to an Entry.
type quickSwitchEntry struct {
	widget.Entry
	onMove   func(delta int)
	onSubmit func()
	onClose  func()
}

func newQuickSwitchEntry(onMove func(int), onSubmit func(), onClose func()) *quickSwitchEntry {
	e := &quickSwitchEntry{onMove: onMove, onSubmit: onSubmit, onClose: onClose}
	e.ExtendBaseWidget(e)
	return e
}

func (e *quickSwitchEntry) TypedKey(key *fyne.KeyEvent) {
	if key == nil {
		e.Entry.TypedKey(key)
		return
	}
	switch key.Name {
	case fyne.KeyDown:
		if e.onMove != nil {
			e.onMove(1)
			return
		}
	case fyne.KeyUp:
		if e.onMove != nil {
			e.onMove(-1)
			return
		}
	case fyne.KeyReturn, fyne.KeyEnter:
		if e.onSubmit != nil {
			e.onSubmit()
			return
		}
	case fyne.KeyEscape:
		if e.onClose != nil {
			e.onClose()
			return
		}
	}
	e.Entry.TypedKey(key)
}

func (e *quickSwitchEntry) TypedShortcut(shortcut fyne.Shortcut) {
	custom, ok := shortcut.(*desktop.CustomShortcut)
	if ok && custom.Modifier&fyne.KeyModifierControl != 0 {
		key := fyne.KeyName(strings.ToUpper(string(custom.KeyName)))
		switch key {
		case fyne.KeyName("N"), fyne.KeyName("J"):
			if e.onMove != nil {
				e.onMove(1)
				return
			}
		case fyne.KeyName("P"), fyne.KeyName("K"):
			if e.onMove != nil {
				e.onMove(-1)
				return
			}
		}
	}
	e.Entry.TypedShortcut(shortcut)
}

// focusEntry is a multi-line Entry that tracks focus state and intercepts
// Escape (to exit threads / cancel reply) and Ctrl shortcuts.
type focusEntry struct {
	widget.Entry
	onFocused  func()
	onShortcut func(fyne.Shortcut) bool
	onEscape   func()
	focused    bool
}

func newFocusEntry(onFocused func(), onShortcut func(fyne.Shortcut) bool, onEscape func()) *focusEntry {
	e := &focusEntry{onFocused: onFocused, onShortcut: onShortcut, onEscape: onEscape}
	e.MultiLine = true
	e.ExtendBaseWidget(e)
	return e
}

func (e *focusEntry) FocusGained() {
	e.Entry.FocusGained()
	e.focused = true
	if e.onFocused != nil {
		e.onFocused()
	}
}

func (e *focusEntry) FocusLost() {
	e.Entry.FocusLost()
	e.focused = false
}

func (e *focusEntry) IsFocused() bool { return e.focused }

func (e *focusEntry) CreateRenderer() fyne.WidgetRenderer {
	return &focusEntryRenderer{inner: e.Entry.CreateRenderer()}
}

type focusEntryRenderer struct {
	inner fyne.WidgetRenderer
}

func (r *focusEntryRenderer) Layout(size fyne.Size)        { r.inner.Layout(size) }
func (r *focusEntryRenderer) MinSize() fyne.Size           { return r.inner.MinSize() }
func (r *focusEntryRenderer) Destroy()                     { r.inner.Destroy() }
func (r *focusEntryRenderer) Objects() []fyne.CanvasObject { return r.inner.Objects() }
func (r *focusEntryRenderer) Refresh() {
	r.inner.Refresh()
	for _, obj := range r.inner.Objects() {
		clearStrokeRecursive(obj)
		clearFillRecursive(obj)
	}
}

func clearStrokeRecursive(obj fyne.CanvasObject) {
	if obj == nil {
		return
	}
	if line, ok := obj.(*canvas.Line); ok {
		if line.StrokeWidth != 0 || line.StrokeColor != color.Transparent {
			line.StrokeWidth = 0
			line.StrokeColor = color.Transparent
			line.Refresh()
		}
	}
	if rect, ok := obj.(*canvas.Rectangle); ok {
		if rect.StrokeWidth != 0 || rect.StrokeColor != color.Transparent {
			rect.StrokeWidth = 0
			rect.StrokeColor = color.Transparent
			rect.Refresh()
		}
	}
	if c, ok := obj.(*fyne.Container); ok {
		for _, child := range c.Objects {
			clearStrokeRecursive(child)
		}
	}
}

func clearFillRecursive(obj fyne.CanvasObject) {
	if obj == nil {
		return
	}
	if rect, ok := obj.(*canvas.Rectangle); ok {
		if rect.FillColor != color.Transparent {
			rect.FillColor = color.Transparent
			rect.Refresh()
		}
	}
	if c, ok := obj.(*fyne.Container); ok {
		for _, child := range c.Objects {
			clearFillRecursive(child)
		}
	}
}

func (e *focusEntry) TypedShortcut(shortcut fyne.Shortcut) {
	if e.onShortcut != nil && e.onShortcut(shortcut) {
		return
	}
	e.Entry.TypedShortcut(shortcut)
}

func (e *focusEntry) TypedKey(key *fyne.KeyEvent) {
	if key != nil && key.Name == fyne.KeyEscape {
		if e.onEscape != nil {
			e.onEscape()
			return
		}
	}
	e.Entry.TypedKey(key)
}

// hoverMessageRow shows timestamp and thread-hint metadata on mouse-over with a
// fade animation. Tapping opens the thread.
type hoverMessageRow struct {
	widget.BaseWidget
	host      *fyne.Container
	content   *fyne.Container
	tsLabel   *canvas.Text
	hintLabel *canvas.Text
	tsAnim    *fyne.Animation
	metaShown bool
	onTap     func()
}

func newHoverMessageRow(content *fyne.Container, tsLabel *canvas.Text, hintLabel *canvas.Text, onTap func()) *hoverMessageRow {
	tsLabel.Hide()
	hintLabel.Hide()
	host := container.NewVBox(container.NewBorder(nil, nil, tsLabel, hintLabel, content))
	r := &hoverMessageRow{
		content:   content,
		tsLabel:   tsLabel,
		hintLabel: hintLabel,
		host:      host,
		onTap:     onTap,
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *hoverMessageRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.host)
}

func (r *hoverMessageRow) MouseIn(_ *desktop.MouseEvent) {
	if r.metaShown || r.tsLabel == nil {
		return
	}
	r.metaShown = true
	r.animateMeta(true)
}

func (r *hoverMessageRow) MouseOut() {
	if !r.metaShown {
		return
	}
	r.metaShown = false
	r.animateMeta(false)
}

func (r *hoverMessageRow) MouseMoved(_ *desktop.MouseEvent) {}

func (r *hoverMessageRow) animateMeta(visible bool) {
	if r.tsAnim != nil {
		r.tsAnim.Stop()
	}
	tsCol := color.NRGBA{R: 100, G: 106, B: 130, A: 180}
	hintCol := color.NRGBA{R: 100, G: 106, B: 130, A: 170}
	if visible {
		r.tsLabel.Color = color.NRGBA{R: tsCol.R, G: tsCol.G, B: tsCol.B, A: 0}
		r.hintLabel.Color = color.NRGBA{R: hintCol.R, G: hintCol.G, B: hintCol.B, A: 0}
		r.tsLabel.Show()
		r.hintLabel.Show()
		r.host.Refresh()
		r.tsAnim = fyne.NewAnimation(120*time.Millisecond, func(f float32) {
			r.tsLabel.Color = color.NRGBA{R: tsCol.R, G: tsCol.G, B: tsCol.B, A: uint8(float32(tsCol.A) * f)}
			r.hintLabel.Color = color.NRGBA{R: hintCol.R, G: hintCol.G, B: hintCol.B, A: uint8(float32(hintCol.A) * f)}
			canvas.Refresh(r.tsLabel)
			canvas.Refresh(r.hintLabel)
		})
		r.tsAnim.Curve = fyne.AnimationEaseOut
		r.tsAnim.Start()
		return
	}
	startTsA := uint8(255)
	if c, ok := r.tsLabel.Color.(color.NRGBA); ok {
		startTsA = c.A
	}
	startHintA := uint8(255)
	if c, ok := r.hintLabel.Color.(color.NRGBA); ok {
		startHintA = c.A
	}
	const dur = 110 * time.Millisecond
	r.tsAnim = fyne.NewAnimation(dur, func(f float32) {
		r.tsLabel.Color = color.NRGBA{R: tsCol.R, G: tsCol.G, B: tsCol.B, A: uint8(float32(startTsA) * (1 - f))}
		r.hintLabel.Color = color.NRGBA{R: hintCol.R, G: hintCol.G, B: hintCol.B, A: uint8(float32(startHintA) * (1 - f))}
		canvas.Refresh(r.tsLabel)
		canvas.Refresh(r.hintLabel)
	})
	r.tsAnim.Curve = fyne.AnimationEaseIn
	r.tsAnim.Start()
	time.AfterFunc(dur, func() {
		fyne.Do(func() {
			if !r.metaShown {
				r.tsLabel.Hide()
				r.hintLabel.Hide()
				r.host.Refresh()
			}
		})
	})
}

func (r *hoverMessageRow) Tapped(_ *fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

func (r *hoverMessageRow) TappedSecondary(_ *fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}
