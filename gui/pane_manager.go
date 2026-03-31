package gui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type splitDir int

const (
	splitHorizontal splitDir = iota
	splitVertical
)

type paneNode struct {
	pane        *chatPane
	left, right *paneNode
	dir         splitDir
}

func (n *paneNode) isLeaf() bool { return n != nil && n.pane != nil }

func (n *paneNode) allPanes() []*chatPane {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return []*chatPane{n.pane}
	}
	return append(n.left.allPanes(), n.right.allPanes()...)
}

func (n *paneNode) buildWidget(showSeparators bool) fyne.CanvasObject {
	if n.isLeaf() {
		return n.pane.widget()
	}
	l := n.left.buildWidget(showSeparators)
	r := n.right.buildWidget(showSeparators)
	if n.dir == splitHorizontal {
		s := container.NewHSplit(l, r)
		s.SetOffset(0.5)
		if showSeparators {
			return newSplitWithSeparator(s, splitHorizontal)
		}
		return s
	}
	s := container.NewVSplit(l, r)
	s.SetOffset(0.5)
	if showSeparators {
		return newSplitWithSeparator(s, splitVertical)
	}
	return s
}

func (n *paneNode) split(target *chatPane, add *chatPane, dir splitDir) bool {
	if n.isLeaf() {
		if n.pane != target {
			return false
		}
		n.left = &paneNode{pane: n.pane}
		n.right = &paneNode{pane: add}
		n.dir = dir
		n.pane = nil
		return true
	}
	return n.left.split(target, add, dir) || n.right.split(target, add, dir)
}

func (n *paneNode) remove(target *chatPane) bool {
	if n == nil || n.isLeaf() {
		return false
	}
	if n.left.isLeaf() && n.left.pane == target {
		*n = *n.right
		return true
	}
	if n.right.isLeaf() && n.right.pane == target {
		*n = *n.left
		return true
	}
	return n.left.remove(target) || n.right.remove(target)
}

type paneManager struct {
	root           paneNode
	focused        *chatPane
	holder         *fyne.Container
	maxPanes       int
	showSeparators bool
	appFocused     bool
	onFocused      func(*chatPane)
	makePane       func() *chatPane
}

func newPaneManager(onFocused func(*chatPane), makePane func() *chatPane) *paneManager {
	pm := &paneManager{maxPanes: 8, onFocused: onFocused, appFocused: true, showSeparators: true}
	pm.makePane = makePane
	if pm.makePane == nil {
		pm.makePane = func() *chatPane {
			return newChatPane(
				func(cp *chatPane) { pm.setFocus(cp) },
				nil,
				nil,
				nil,
				nil,
				nil,
			)
		}
	}
	first := pm.makePane()
	pm.root = paneNode{pane: first}
	pm.focused = first
	first.setFocused(true)
	pm.holder = container.NewMax(pm.root.buildWidget(pm.showSeparators))
	pm.syncInputVisibility(false)
	return pm
}

func (pm *paneManager) widget() fyne.CanvasObject { return pm.holder }
func (pm *paneManager) focusedPane() *chatPane    { return pm.focused }
func (pm *paneManager) allPanes() []*chatPane     { return pm.root.allPanes() }

func (pm *paneManager) setFocus(p *chatPane) {
	if p == nil {
		return
	}
	if pm.focused == p {
		pm.syncInputVisibility(false)
		if pm.onFocused != nil {
			pm.onFocused(p)
		}
		return
	}
	if pm.focused != nil {
		pm.focused.setFocused(false)
	}
	pm.focused = p
	pm.focused.setFocused(true)
	pm.syncInputVisibility(true)
	if pm.onFocused != nil {
		pm.onFocused(p)
	}
}

func (pm *paneManager) splitFocused(dir splitDir, newPane *chatPane) {
	if pm.focused == nil || len(pm.allPanes()) >= pm.maxPanes || newPane == nil {
		return
	}
	pm.root.split(pm.focused, newPane, dir)
	pm.setFocus(newPane)
	pm.syncInputVisibility(true)
	pm.rebuild()
}

func (pm *paneManager) closeFocused() {
	panes := pm.allPanes()
	if len(panes) <= 1 || pm.focused == nil {
		return
	}
	var next *chatPane
	for i, p := range panes {
		if p == pm.focused {
			if i > 0 {
				next = panes[i-1]
			} else if i+1 < len(panes) {
				next = panes[i+1]
			}
			break
		}
	}
	pm.root.remove(pm.focused)
	pm.focused = next
	if pm.focused != nil {
		pm.focused.setFocused(true)
	}
	pm.syncInputVisibility(false)
	pm.rebuild()
}

func (pm *paneManager) rebuild() {
	pm.holder.Objects = []fyne.CanvasObject{pm.root.buildWidget(pm.showSeparators)}
	pm.holder.Refresh()
}

func (pm *paneManager) setShowSeparators(show bool) {
	if pm.showSeparators == show {
		return
	}
	pm.showSeparators = show
	pm.rebuild()
}

func (pm *paneManager) setAppFocused(focused bool) {
	if pm.appFocused == focused {
		return
	}
	pm.appFocused = focused
	pm.syncInputVisibility(false)
}

func (pm *paneManager) syncInputVisibility(reveal bool) {
	_ = reveal
	for _, p := range pm.allPanes() {
		p.setFocused(pm.appFocused && p == pm.focused)
		p.setInputVisible(true, false)
	}
}

// splitWithSeparator wraps a container.Split and draws a thin separator line
// over the split handle for visual clarity.
type splitWithSeparator struct {
	widget.BaseWidget
	split *container.Split
	dir   splitDir
	line  *canvas.Rectangle
}

func newSplitWithSeparator(split *container.Split, dir splitDir) *splitWithSeparator {
	w := &splitWithSeparator{
		split: split,
		dir:   dir,
		line:  canvas.NewRectangle(color.NRGBA{R: 132, G: 139, B: 165, A: 52}),
	}
	w.line.StrokeWidth = 0
	w.ExtendBaseWidget(w)
	return w
}

func (w *splitWithSeparator) CreateRenderer() fyne.WidgetRenderer {
	return &splitWithSeparatorRenderer{w: w, objs: []fyne.CanvasObject{w.split, w.line}}
}

type splitWithSeparatorRenderer struct {
	w    *splitWithSeparator
	objs []fyne.CanvasObject
}

func (r *splitWithSeparatorRenderer) Layout(size fyne.Size) {
	r.w.split.Resize(size)
	r.w.split.Move(fyne.NewPos(0, 0))
	offset := float32(r.w.split.Offset)
	if offset < 0 {
		offset = 0
	}
	if offset > 1 {
		offset = 1
	}
	if r.w.dir == splitHorizontal {
		x := size.Width*offset - 0.5
		if x < 0 {
			x = 0
		}
		r.w.line.Move(fyne.NewPos(x, 0))
		r.w.line.Resize(fyne.NewSize(1, size.Height))
		return
	}
	y := size.Height*offset - 0.5
	if y < 0 {
		y = 0
	}
	r.w.line.Move(fyne.NewPos(0, y))
	r.w.line.Resize(fyne.NewSize(size.Width, 1))
}

func (r *splitWithSeparatorRenderer) MinSize() fyne.Size {
	return r.w.split.MinSize()
}

func (r *splitWithSeparatorRenderer) Refresh() {
	base := colorToNRGBA(theme.Color(theme.ColorNameForeground))
	base.A = 52
	r.w.line.FillColor = base
	r.Layout(r.w.Size())
	canvas.Refresh(r.w.line)
	canvas.Refresh(r.w.split)
}

func (r *splitWithSeparatorRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *splitWithSeparatorRenderer) Destroy()                     {}

func colorToNRGBA(c color.Color) color.NRGBA {
	r, g, b, a := c.RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

// Pane layout serialization

type paneManagerState struct {
	Root          *paneStateNode `json:"root"`
	FocusedPaneID int            `json:"focusedPaneID"`
}

type paneStateNode struct {
	PaneID    int            `json:"paneID,omitempty"`
	ChannelID string         `json:"channelID,omitempty"`
	ThreadTS  string         `json:"threadTS,omitempty"`
	Dir       string         `json:"dir,omitempty"`
	Left      *paneStateNode `json:"left,omitempty"`
	Right     *paneStateNode `json:"right,omitempty"`
}

func (pm *paneManager) serializeState() (string, error) {
	state := paneManagerState{
		Root:          serializePaneNode(&pm.root),
		FocusedPaneID: -1,
	}
	if pm.focused != nil {
		state.FocusedPaneID = pm.focused.id
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (pm *paneManager) restoreState(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var state paneManagerState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return err
	}
	if state.Root == nil {
		return fmt.Errorf("invalid pane state root")
	}
	idMap := map[int]*chatPane{}
	root, err := pm.restoreNode(state.Root, idMap)
	if err != nil {
		return err
	}
	pm.root = *root
	pm.focused = idMap[state.FocusedPaneID]
	if pm.focused == nil {
		all := pm.allPanes()
		if len(all) > 0 {
			pm.focused = all[0]
		}
	}
	for _, p := range pm.allPanes() {
		p.setFocused(false)
	}
	if pm.focused != nil {
		pm.focused.setFocused(true)
	}
	paneIDCounter = maxPaneID(pm.allPanes()) + 1
	pm.rebuild()
	return nil
}

func (pm *paneManager) restoreNode(n *paneStateNode, idMap map[int]*chatPane) (*paneNode, error) {
	if n == nil {
		return nil, fmt.Errorf("nil node")
	}
	if n.Left == nil && n.Right == nil {
		p := pm.makePane()
		p.id = n.PaneID
		p.channelID = n.ChannelID
		p.threadTS = n.ThreadTS
		if strings.TrimSpace(p.threadTS) != "" {
			p.setThreadBanner("Thread view")
		} else {
			p.setThreadBanner("")
		}
		idMap[p.id] = p
		return &paneNode{pane: p}, nil
	}
	if n.Left == nil || n.Right == nil {
		return nil, fmt.Errorf("invalid split node")
	}
	left, err := pm.restoreNode(n.Left, idMap)
	if err != nil {
		return nil, err
	}
	right, err := pm.restoreNode(n.Right, idMap)
	if err != nil {
		return nil, err
	}
	dir := splitHorizontal
	if strings.ToLower(n.Dir) == "vertical" {
		dir = splitVertical
	}
	return &paneNode{left: left, right: right, dir: dir}, nil
}

func serializePaneNode(n *paneNode) *paneStateNode {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return &paneStateNode{
			PaneID:    n.pane.id,
			ChannelID: n.pane.channelID,
			ThreadTS:  n.pane.threadTS,
		}
	}
	dir := "horizontal"
	if n.dir == splitVertical {
		dir = "vertical"
	}
	return &paneStateNode{
		Dir:   dir,
		Left:  serializePaneNode(n.left),
		Right: serializePaneNode(n.right),
	}
}

func maxPaneID(panes []*chatPane) int {
	maxID := -1
	for _, p := range panes {
		if p != nil && p.id > maxID {
			maxID = p.id
		}
	}
	return maxID
}
