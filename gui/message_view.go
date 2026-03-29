package gui

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"net/url"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/stefan/slack-gui/api"
)

func renderMessageRow(m api.Message, isFromMe bool, mentionedMe bool, onThread func(api.Message), onReply func(api.Message), onMedia func(api.File), showHeader bool, inThreadView bool) fyne.CanvasObject {
	name := senderName(m)
	ts := canvas.NewText(formatHoverTimestamp(m.Time)+" ", color.NRGBA{R: 100, G: 106, B: 130, A: 0})
	ts.TextSize = hoverTimestampTextSize()
	hintText := "reply in thread"
	if m.ReplyCount > 0 {
		hintText = "view thread"
	}
	if inThreadView {
		hintText = ""
	}
	hint := canvas.NewText(hintText, color.NRGBA{R: 100, G: 106, B: 130, A: 0})
	hint.TextSize = hoverTimestampTextSize()

	body := widget.NewLabel(m.Text)
	body.Wrapping = fyne.TextWrapWord
	if isFromMe {
		body.Alignment = fyne.TextAlignTrailing
		body.Importance = widget.SuccessImportance
	}
	row := container.NewVBox(alignOutgoingRow(body, isFromMe))
	rowWithMeta := container.NewVBox()
	if quoted := strings.TrimSpace(m.ForwardedText); quoted != "" {
		preview := compactQuotedPreview(quoted)
		quoteText := widget.NewLabel("↪ " + preview)
		quoteText.Wrapping = fyne.TextWrapWord
		quoteText.TextStyle = fyne.TextStyle{Italic: true}
		quoteText.Importance = widget.LowImportance
		quoteTextRow := container.NewPadded(quoteText)
		quoteBg := canvas.NewRectangle(color.NRGBA{R: 92, G: 99, B: 126, A: 22})
		quoteBg.StrokeWidth = 0
		quoteBar := canvas.NewRectangle(color.NRGBA{R: 122, G: 162, B: 247, A: 170})
		quoteBar.SetMinSize(fyne.NewSize(1, 1))
		quoteContent := container.NewMax(quoteBg, quoteTextRow)
		quote := container.NewBorder(nil, nil, quoteBar, nil, quoteContent)
		rowWithMeta.Add(alignOutgoingRow(quote, isFromMe))
	}
	rowWithMeta.Add(row)
	openThread := func() {
		if inThreadView {
			return
		}
		if onThread == nil {
			return
		}
		onThread(m)
	}
	if !inThreadView && m.ReplyCount > 0 {
		threadLabel := fmt.Sprintf("%d repl%s · View thread", m.ReplyCount, pluralSuffix(m.ReplyCount))
		rowWithMeta.Add(alignOutgoingRow(newSubtleTapLabel(threadLabel, openThread), isFromMe))
	}

	var content *fyne.Container
	if showHeader {
		sender := canvas.NewText(name, senderColor(name, isFromMe))
		sender.TextStyle = fyne.TextStyle{Bold: true}
		sender.TextSize = hoverSenderTextSize()
		if isFromMe {
			sender.Alignment = fyne.TextAlignTrailing
			content = container.NewVBox(alignOutgoingRow(sender, true), rowWithMeta)
		} else {
			content = container.NewVBox(sender, rowWithMeta)
		}
	} else {
		content = container.NewVBox(rowWithMeta)
	}
	_ = onReply
	for _, f := range m.Files {
		ff := f
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = "file"
		}
		if f.IsImage() && strings.TrimSpace(f.BestImageURL()) != "" {
			rowWithMeta.Add(widget.NewHyperlink(name, mustParseURL(f.BestImageURL())))
			rowWithMeta.Add(alignOutgoingRow(newSubtleTapLabel("Open image", func() {
				if onMedia != nil {
					onMedia(ff)
				}
			}), isFromMe))
			continue
		}
		if strings.TrimSpace(f.Permalink) != "" {
			rowWithMeta.Add(widget.NewHyperlink(name, mustParseURL(f.Permalink)))
		} else {
			rowWithMeta.Add(widget.NewLabel(name))
		}
	}
	rowObj := newHoverMessageRow(content, ts, hint, openThread)
	rowCanvas := applyMessageSideIndent(rowObj)
	if !isFromMe && mentionedMe {
		bg := canvas.NewRectangle(color.NRGBA{R: 66, G: 53, B: 24, A: 120})
		return container.NewMax(bg, rowCanvas)
	}
	return rowCanvas
}

func pluralSuffix(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func senderName(m api.Message) string {
	if s := strings.TrimSpace(m.Username); s != "" {
		return s
	}
	if s := strings.TrimSpace(m.UserID); s != "" {
		return s
	}
	if strings.TrimSpace(m.BotID) != "" {
		return "bot"
	}
	return "unknown"
}

func senderColor(name string, isFromMe bool) color.Color {
	if isFromMe {
		return color.NRGBA{R: 125, G: 207, B: 255, A: 255}
	}
	palette := []color.NRGBA{
		{R: 122, G: 162, B: 247, A: 255},
		{R: 158, G: 206, B: 106, A: 255},
		{R: 224, G: 175, B: 104, A: 255},
		{R: 247, G: 118, B: 142, A: 255},
		{R: 187, G: 154, B: 247, A: 255},
		{R: 125, G: 207, B: 255, A: 255},
		{R: 231, G: 130, B: 132, A: 255},
		{R: 115, G: 218, B: 202, A: 255},
	}
	trimmed := strings.TrimSpace(strings.ToLower(name))
	if trimmed == "" {
		return palette[0]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(trimmed))
	return palette[int(h.Sum32())%len(palette)]
}

func threadRootTS(m api.Message) string {
	if ts := strings.TrimSpace(m.ThreadTS); ts != "" {
		return ts
	}
	return strings.TrimSpace(m.TS)
}

func isLastInSenderGroup(msgs []api.Message, idx int) bool {
	if idx+1 >= len(msgs) {
		return true
	}
	cur, next := msgs[idx], msgs[idx+1]
	if senderName(cur) != senderName(next) {
		return true
	}
	return next.Time.Sub(cur.Time) > 4*time.Minute
}

func hoverSenderTextSize() float32 {
	size := float32(theme.TextSize()) - 1
	if size < 8 {
		size = 8
	}
	return size
}

func hoverTimestampTextSize() float32 {
	size := hoverSenderTextSize() - 1
	if size < 8 {
		size = 8
	}
	return size
}

// messageMetaActionTextSize is for inline chat actions (e.g. view thread, open image) — smaller than body text.
func messageMetaActionTextSize() float32 {
	size := hoverTimestampTextSize() - 1
	if size < 8 {
		size = 8
	}
	return size
}

func formatHoverTimestamp(t time.Time) string {
	return t.Format("15:04")
}

func messageMentionsUser(text, userID string) bool {
	id := strings.TrimSpace(userID)
	if id == "" {
		return false
	}
	return strings.Contains(text, "<@"+id+">")
}

func applyMessageSideIndent(row fyne.CanvasObject) fyne.CanvasObject {
	return container.NewBorder(nil, nil, fixedWidthSpacer(8), fixedWidthSpacer(8), row)
}

func fixedWidthSpacer(width float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(width, 1))
	return r
}

func alignOutgoingRow(obj fyne.CanvasObject, isFromMe bool) fyne.CanvasObject {
	if !isFromMe {
		return obj
	}
	return container.NewBorder(nil, nil, layout.NewSpacer(), nil, obj)
}

func truncate(s string, n int) string {
	if n <= 0 || len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func compactQuotedPreview(s string) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if clean == "" {
		return ""
	}
	r := []rune(clean)
	if len(r) <= 160 {
		if len(r) <= 80 {
			return clean
		}
		return string(r[:80]) + "\n" + string(r[80:])
	}
	return string(r[:80]) + "\n" + string(r[80:160]) + "…"
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		u, _ = url.Parse("https://slack.com")
	}
	return u
}
