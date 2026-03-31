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
	inputTopGap  *canvas.Rectangle
	inputGap     *canvas.Rectangle
	inputVisible bool
	revealAnim   *fyne.Animation
	replyHolder  *fyne.Container
	replyLabel   *widget.Label
	threadHolder *fyne.Container
	threadLabel  *widget.Label
	hintLabel    *widget.Label

	channelID   string
	channelName string
	threadTS    string
	replyTarget *api.Message

	inputBg     *canvas.Rectangle
	inputBorder *canvas.Rectangle
	threadBg    *canvas.Rectangle
	replyBg     *canvas.Rectangle
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
	p.input.SetMinRowsVisible(1)
	p.input.OnSubmitted = func(_ string) {
		if onSend != nil {
			onSend(p)
		}
	}
	p.replyLabel = widget.NewLabel("")
	p.replyLabel.Wrapping = fyne.TextTruncate
	p.threadLabel = widget.NewLabel("")
	p.threadLabel.Wrapping = fyne.TextTruncate
	p.hintLabel = widget.NewLabel("Enter to send  |  Shift+Enter for new line  |  Ctrl+K quick switch")
	p.hintLabel.Importance = widget.LowImportance
	p.threadBg = canvas.NewRectangle(theme.Color(theme.ColorNameHover))
	p.replyBg = canvas.NewRectangle(theme.Color(theme.ColorNameHover))
	p.threadHolder = container.NewMax(p.threadBg, container.NewPadded(container.NewBorder(nil, nil, nil, widget.NewButton("Leave thread", func() {
		if onExitThread != nil {
			onExitThread(p)
		}
	}), p.threadLabel)))
	p.threadHolder.Hide()
	p.replyHolder = container.NewMax(p.replyBg, container.NewPadded(container.NewBorder(nil, nil, nil, widget.NewButton("Cancel", func() {
		if onCancelReply != nil {
			onCancelReply(p)
		}
	}), p.replyLabel)))
	p.replyHolder.Hide()

	p.inputTopGap = canvas.NewRectangle(color.Transparent)
	p.inputTopGap.SetMinSize(fyne.NewSize(1, 8))
	p.inputGap = canvas.NewRectangle(color.Transparent)
	p.inputGap.SetMinSize(fyne.NewSize(1, 0))
	p.inputBg = canvas.NewRectangle(color.Transparent)
	p.inputBorder = canvas.NewRectangle(color.Transparent)
	p.inputBorder.StrokeColor = color.Transparent
	p.inputBorder.StrokeWidth = 0
	inputHPad := float32(8)
	inputVPad := float32(3)
	inputTopPad := canvas.NewRectangle(color.Transparent)
	inputTopPad.SetMinSize(fyne.NewSize(1, inputVPad))
	inputBottomPad := canvas.NewRectangle(color.Transparent)
	inputBottomPad.SetMinSize(fyne.NewSize(1, inputVPad))
	entryRow := container.NewMax(
		p.inputBorder,
		p.inputBg,
		container.NewBorder(inputTopPad, inputBottomPad, fixedWidthSpacer(inputHPad), fixedWidthSpacer(inputHPad), p.input),
	)
	p.inputCard = container.NewVBox(p.inputTopGap, p.threadHolder, p.replyHolder, entryRow, p.hintLabel, p.inputGap)
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
	_ = reveal
	if p.inputVisible == visible {
		return
	}
	if p.revealAnim != nil {
		p.revealAnim.Stop()
		p.revealAnim = nil
	}
	p.inputVisible = visible
	if visible {
		p.viewport.Objects = []fyne.CanvasObject{p.msgScroll, p.inputCard}
	} else {
		hiddenSpacer := canvas.NewRectangle(color.Transparent)
		hiddenSpacer.SetMinSize(fyne.NewSize(1, 10))
		p.viewport.Objects = []fyne.CanvasObject{p.msgScroll, hiddenSpacer}
	}
	p.panel.Refresh()
	fyne.Do(func() {
		p.msgScroll.ScrollToBottom()
	})
	go func() {
		time.Sleep(90 * time.Millisecond)
		fyne.Do(func() {
			p.msgScroll.ScrollToBottom()
		})
	}()
}

func (p *chatPane) clearMessages() {
	p.msgBox.Objects = nil
	p.msgBox.Refresh()
}

func (p *chatPane) refreshForTheme() {
	if p.inputBg != nil {
		p.inputBg.FillColor = color.Transparent
		p.inputBg.Refresh()
	}
	if p.inputBorder != nil {
		p.inputBorder.StrokeColor = color.Transparent
		p.inputBorder.StrokeWidth = 0
		p.inputBorder.Refresh()
	}
	if p.threadBg != nil {
		p.threadBg.FillColor = theme.Color(theme.ColorNameHover)
		p.threadBg.Refresh()
	}
	if p.replyBg != nil {
		p.replyBg.FillColor = theme.Color(theme.ColorNameHover)
		p.replyBg.Refresh()
	}
	p.replyLabel.Refresh()
	p.threadLabel.Refresh()
	p.hintLabel.Refresh()
	p.title.Refresh()
	p.msgBox.Refresh()
	p.panel.Refresh()
}

func (p *chatPane) setMessages(msgs []api.Message, currentUserID, selfUserID string, showTimestamps bool, onThread func(api.Message), onReply func(api.Message), onMedia func(api.File)) {
	p.msgBox.Objects = nil
	inThreadView := strings.TrimSpace(p.threadTS) != ""
	if len(msgs) == 0 {
		empty := widget.NewLabel("No messages yet")
		empty.Importance = widget.LowImportance
		empty.Wrapping = fyne.TextWrapWord
		p.msgBox.Add(container.NewPadded(empty))
		p.msgBox.Refresh()
		return
	}
	for i, m := range msgs {
		isFromMe := strings.TrimSpace(m.UserID) != "" && strings.TrimSpace(m.UserID) == strings.TrimSpace(currentUserID)
		showHeader := isLastInSenderGroup(msgs, i)
		if isFromMe {
			// Always show own sender header so the name appears above outgoing text.
			showHeader = true
		}
		mentionedMe := messageMentionsUser(m.Text, selfUserID)
		p.msgBox.Add(renderMessageRow(m, isFromMe, mentionedMe, showTimestamps, onThread, onReply, onMedia, showHeader, inThreadView))
	}
	p.msgBox.Refresh()
	fyne.Do(func() {
		p.msgScroll.ScrollToBottom()
	})
	go func() {
		time.Sleep(120 * time.Millisecond)
		fyne.Do(func() {
			p.msgScroll.ScrollToBottom()
		})
	}()
}
