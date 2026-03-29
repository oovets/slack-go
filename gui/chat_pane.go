package gui

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/stefan/slack-gui/api"
)

// paneIDCounter assigns unique IDs to chat panes. All pane creation happens on
// the Fyne main goroutine, so no locking is needed.
var paneIDCounter int

type chatPane struct {
	id           int
	root         *paneSurface
	panel        *fyne.Container
	title        *widget.Label
	viewport     *fyne.Container
	msgBox       *fyne.Container
	msgScroll    *container.Scroll
	input        *focusEntry
	inputCard    *fyne.Container
	inputGap     *canvas.Rectangle
	inputVisible bool
	revealAnim   *fyne.Animation
	replyHolder  *fyne.Container
	replyLabel   *widget.Label
	threadHolder *fyne.Container
	threadLabel  *widget.Label

	channelID   string
	channelName string
	threadTS    string
	replyTarget *api.Message

	inputBg *canvas.Rectangle
}

func newChatPane(onActivate func(*chatPane), onSend func(*chatPane), onExitThread func(*chatPane), onCancelReply func(*chatPane), onResized func(*chatPane), onShortcut func(fyne.Shortcut) bool) *chatPane {
	p := &chatPane{id: paneIDCounter}
	paneIDCounter++
	p.title = widget.NewLabel("Select a channel")
	p.title.Importance = widget.HighImportance
	p.msgBox = container.NewVBox()
	p.msgScroll = container.NewVScroll(p.msgBox)
	p.input = newFocusEntry(func() {
		if onActivate != nil {
			onActivate(p)
		}
	}, onShortcut, func() {
		if strings.TrimSpace(p.threadTS) != "" {
			if onExitThread != nil {
				onExitThread(p)
			}
			return
		}
		if p.replyTarget != nil && onCancelReply != nil {
			onCancelReply(p)
		}
	})
	p.input.Wrapping = fyne.TextWrapWord
	p.input.SetMinRowsVisible(2)
	p.input.OnSubmitted = func(_ string) {
		if onSend != nil {
			onSend(p)
		}
	}
	p.replyLabel = widget.NewLabel("")
	p.threadLabel = widget.NewLabel("")
	p.threadHolder = container.NewBorder(nil, nil, nil, widget.NewButton("Back", func() {
		if onExitThread != nil {
			onExitThread(p)
		}
	}), p.threadLabel)
	p.threadHolder.Hide()
	p.replyHolder = container.NewBorder(nil, nil, nil, widget.NewButton("×", func() {
		if onCancelReply != nil {
			onCancelReply(p)
		}
	}), p.replyLabel)
	p.replyHolder.Hide()

	p.inputGap = canvas.NewRectangle(color.Transparent)
	p.inputGap.SetMinSize(fyne.NewSize(1, 0))
	p.inputBg = canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	inputHPad := float32(8)
	entryRow := container.NewMax(
		p.inputBg,
		container.NewBorder(nil, nil, fixedWidthSpacer(inputHPad), fixedWidthSpacer(inputHPad), p.input),
	)
	p.inputCard = container.NewVBox(p.threadHolder, p.replyHolder, entryRow, p.inputGap)
	p.inputVisible = true
	p.viewport = container.NewBorder(nil, p.inputCard, nil, nil, p.msgScroll)
	p.viewport.Objects = []fyne.CanvasObject{p.msgScroll, p.inputCard}
	p.panel = container.NewMax(p.viewport)
	p.root = newPaneSurface(p.panel, func() {
		if onActivate != nil {
			onActivate(p)
		}
	}, func() {
		if onResized != nil {
			onResized(p)
		}
	})
	return p
}

func (p *chatPane) widget() fyne.CanvasObject { return p.root }

func (p *chatPane) setFocused(focused bool) {
	_ = focused
}

func (p *chatPane) setTitle(t string) { p.title.SetText(t) }

func (p *chatPane) setThreadBanner(text string) {
	if p.threadHolder == nil || p.threadLabel == nil {
		return
	}
	if strings.TrimSpace(text) == "" {
		p.threadLabel.SetText("")
		p.threadHolder.Hide()
		return
	}
	p.threadLabel.SetText(text)
	p.threadHolder.Show()
}

func (p *chatPane) setInputVisible(visible bool, reveal bool) {
	if p.inputVisible == visible {
		return
	}
	if p.revealAnim != nil {
		p.revealAnim.Stop()
		p.revealAnim = nil
	}
	p.inputVisible = visible
	if visible {
		if reveal {
			revealSpacer := canvas.NewRectangle(color.Transparent)
			start := float32(14)
			revealSpacer.SetMinSize(fyne.NewSize(1, start))
			p.viewport.Objects = []fyne.CanvasObject{p.msgScroll, container.NewVBox(revealSpacer, p.inputCard)}
			p.panel.Refresh()
			p.revealAnim = fyne.NewAnimation(130*time.Millisecond, func(f float32) {
				h := start * (1 - f)
				revealSpacer.SetMinSize(fyne.NewSize(1, h))
				p.panel.Refresh()
			})
			p.revealAnim.Curve = fyne.AnimationEaseOut
			p.revealAnim.Start()
			return
		}
		p.viewport.Objects = []fyne.CanvasObject{p.msgScroll, p.inputCard}
	} else {
		hiddenSpacer := canvas.NewRectangle(color.Transparent)
		hiddenSpacer.SetMinSize(fyne.NewSize(1, 10))
		p.viewport.Objects = []fyne.CanvasObject{p.msgScroll, hiddenSpacer}
	}
	p.panel.Refresh()
	for _, d := range []time.Duration{0, 80 * time.Millisecond, 180 * time.Millisecond, 320 * time.Millisecond} {
		d := d
		go func() {
			if d > 0 {
				time.Sleep(d)
			}
			fyne.Do(func() {
				p.msgScroll.ScrollToBottom()
			})
		}()
	}
}

func (p *chatPane) clearMessages() {
	p.msgBox.Objects = nil
	p.msgBox.Refresh()
}

func (p *chatPane) refreshForTheme() {
	if p.inputBg != nil {
		p.inputBg.FillColor = theme.Color(theme.ColorNameInputBackground)
		p.inputBg.Refresh()
	}
	p.replyLabel.Refresh()
	p.threadLabel.Refresh()
	p.title.Refresh()
	p.msgBox.Refresh()
	p.panel.Refresh()
}

func (p *chatPane) setMessages(msgs []api.Message, currentUserID, selfUserID string, onThread func(api.Message), onReply func(api.Message), onMedia func(api.File)) {
	p.msgBox.Objects = nil
	inThreadView := strings.TrimSpace(p.threadTS) != ""
	for i, m := range msgs {
		showHeader := isLastInSenderGroup(msgs, i)
		isFromMe := strings.TrimSpace(m.UserID) != "" && strings.TrimSpace(m.UserID) == strings.TrimSpace(currentUserID)
		mentionedMe := messageMentionsUser(m.Text, selfUserID)
		p.msgBox.Add(renderMessageRow(m, isFromMe, mentionedMe, onThread, onReply, onMedia, showHeader, inThreadView))
	}
	p.msgBox.Refresh()
	for _, d := range []time.Duration{0, 60 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond, 900 * time.Millisecond, 1400 * time.Millisecond} {
		go func(delay time.Duration) {
			if delay > 0 {
				time.Sleep(delay)
			}
			fyne.Do(func() {
				p.msgScroll.ScrollToBottom()
			})
		}(d)
	}
}
