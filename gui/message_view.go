package gui

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	emoji "github.com/kyokomi/emoji/v2"
	"github.com/stefan/slack-gui/api"
)

var (
	twemojiCacheMu           sync.RWMutex
	twemojiCache             = map[string]fyne.Resource{}
	twemojiMissCache         = map[string]bool{}
	workspaceEmojiMu         sync.RWMutex
	workspaceEmojiURLByKey   = map[string]string{}
	workspaceEmojiAliasByKey = map[string]string{}
)

const reactionEmojiSize = float32(9)
const reactionCountSize = float32(8)

func renderMessageRow(m api.Message, isFromMe bool, mentionedMe bool, showTimestamps bool, onThread func(api.Message), onReply func(api.Message), onMedia func(api.File), showHeader bool, inThreadView bool) fyne.CanvasObject {
	name := senderName(m)
	ts := canvas.NewText(formatHoverTimestamp(m.Time), color.NRGBA{R: 100, G: 106, B: 130, A: 190})
	ts.TextSize = hoverTimestampTextSize()

	body := widget.NewLabel(renderSlackText(m.Text))
	body.Wrapping = fyne.TextWrapWord
	if isFromMe {
		body.Alignment = fyne.TextAlignTrailing
		body.Importance = widget.LowImportance
	}
	row := container.NewVBox(alignOutgoingRow(body, isFromMe))
	rowWithMeta := container.NewVBox()
	if quoted := strings.TrimSpace(renderSlackText(m.ForwardedText)); quoted != "" {
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
	if len(m.Reactions) > 0 {
		reactionRow := container.NewHBox()
		for _, reaction := range m.Reactions {
			emojiObj := newReactionEmojiView(reaction.Name)
			token := canvas.NewText(formatReactionToken(reaction.Name), color.NRGBA{R: 120, G: 126, B: 146, A: 220})
			token.TextSize = reactionCountSize
			count := canvas.NewText(fmt.Sprintf("%d", reaction.Count), color.NRGBA{R: 120, G: 126, B: 146, A: 210})
			count.TextSize = reactionCountSize
			count.TextStyle = fyne.TextStyle{}
			content := container.NewHBox(emojiObj, token, count)
			chipBg := canvas.NewRectangle(color.Transparent)
			chip := container.NewMax(chipBg, container.NewBorder(nil, nil, fixedWidthSpacer(2), fixedWidthSpacer(2), content))
			reactionRow.Add(chip)
		}
		rowWithMeta.Add(alignOutgoingRow(reactionRow, isFromMe))
	}

	var content *fyne.Container
	if showHeader {
		sender := canvas.NewText(name, senderColor(name, isFromMe))
		sender.TextStyle = fyne.TextStyle{Bold: true}
		sender.TextSize = hoverSenderTextSize()
		metaRow := []fyne.CanvasObject{sender}
		if showTimestamps {
			metaRow = append(metaRow, ts)
		}
		if isFromMe {
			sender.Alignment = fyne.TextAlignTrailing
			head := container.NewHBox(metaRow...)
			content = container.NewVBox(container.NewHBox(layout.NewSpacer(), head), rowWithMeta)
		} else {
			content = container.NewVBox(container.NewHBox(metaRow...), rowWithMeta)
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
			link := widget.NewHyperlink(name, mustParseURL(f.Permalink))
			link.Wrapping = fyne.TextWrapWord
			rowWithMeta.Add(link)
		} else {
			fileLabel := widget.NewLabel(name)
			fileLabel.Wrapping = fyne.TextWrapWord
			rowWithMeta.Add(fileLabel)
		}
	}
	rowCanvas := applyMessageSideIndent(content)
	if !showTimestamps {
		rowCanvas = newTimestampToggleRow(rowCanvas, formatHoverTimestamp(m.Time), isFromMe)
	}
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
	return container.NewBorder(nil, nil, fixedWidthSpacer(10), fixedWidthSpacer(10), row)
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

func renderSlackText(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	// Keep Slack shortcodes as text for predictable rendering across fonts.
	return text
}

func formatReactionToken(name string) string {
	trimmed := strings.Trim(strings.TrimSpace(name), ":")
	if trimmed == "" {
		return ":emoji:"
	}
	return ":" + trimmed + ":"
}

func newReactionEmojiView(name string) fyne.CanvasObject {
	token := formatReactionToken(name)
	if url, ok := resolveWorkspaceEmojiURL(name); ok {
		if res, ok := cachedTwemojiResource(url); ok && res != nil {
			img := canvas.NewImageFromResource(res)
			img.FillMode = canvas.ImageFillContain
			img.SetMinSize(fyne.NewSize(reactionEmojiSize, reactionEmojiSize))
			return img
		}
		fallback := widget.NewLabel(token)
		fallback.Importance = widget.LowImportance
		img := canvas.NewImageFromResource(nil)
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(fyne.NewSize(reactionEmojiSize, reactionEmojiSize))
		img.Hide()
		host := container.NewMax(fallback, img)
		go fetchTwemojiResource([]string{url}, func(res fyne.Resource) {
			if res == nil {
				return
			}
			fyne.Do(func() {
				img.Resource = res
				img.Show()
				fallback.Hide()
				host.Refresh()
			})
		})
		return host
	}
	unicode, ok := resolveReactionUnicode(name)
	if !ok {
		fallback := canvas.NewText(token, color.NRGBA{R: 120, G: 126, B: 146, A: 220})
		fallback.TextSize = reactionCountSize
		return fallback
	}
	unicode = strings.TrimSpace(unicode)
	if unicode == "" {
		fallback := canvas.NewText(token, color.NRGBA{R: 120, G: 126, B: 146, A: 220})
		fallback.TextSize = reactionCountSize
		return fallback
	}
	codes := twemojiCodeCandidates(unicode)
	if len(codes) == 0 {
		fallback := canvas.NewText(token, color.NRGBA{R: 120, G: 126, B: 146, A: 220})
		fallback.TextSize = reactionCountSize
		return fallback
	}
	for _, code := range codes {
		if res, ok := cachedTwemojiResource(code); ok && res != nil {
			img := canvas.NewImageFromResource(res)
			img.FillMode = canvas.ImageFillContain
			img.SetMinSize(fyne.NewSize(reactionEmojiSize, reactionEmojiSize))
			return img
		}
	}
	fallback := canvas.NewText(token, color.NRGBA{R: 120, G: 126, B: 146, A: 220})
	fallback.TextSize = reactionCountSize
	img := canvas.NewImageFromResource(nil)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(reactionEmojiSize, reactionEmojiSize))
	img.Hide()
	host := container.NewMax(fallback, img)
	go fetchTwemojiResource(codes, func(res fyne.Resource) {
		if res == nil {
			return
		}
		fyne.Do(func() {
			img.Resource = res
			img.Show()
			fallback.Hide()
			host.Refresh()
		})
	})
	return host
}

func resolveReactionUnicode(name string) (string, bool) {
	base := strings.ToLower(strings.Trim(strings.TrimSpace(name), ":"))
	if base == "" {
		return "", false
	}
	tone := ""
	if strings.Contains(base, "::") {
		parts := strings.SplitN(base, "::", 2)
		base = parts[0]
		tone = strings.TrimSpace(parts[1])
	}
	aliases := []string{base, strings.ReplaceAll(base, "-", "_"), strings.ReplaceAll(base, "_", "-")}
	special := map[string]string{
		"thumbsup":         "+1",
		"thumbsdown":       "-1",
		"heavy_plus_sign":  "heavy_plus_sign",
		"heavy_minus_sign": "heavy_minus_sign",
	}
	if mapped, ok := special[base]; ok {
		aliases = append(aliases, mapped)
	}
	knownUnicode := map[string]string{
		"face_with_peeking_eye": "\U0001FAE3",
		"melting_face":          "\U0001FAE0",
		"saluting_face":         "\U0001FAE1",
		"dotted_line_face":      "\U0001FAE5",
		"shaking_face":          "\U0001FAE8",
	}
	for _, alias := range aliases {
		if unicode, ok := knownUnicode[alias]; ok && strings.TrimSpace(unicode) != "" {
			return applySlackSkinTone(strings.TrimSpace(unicode), tone), true
		}
	}
	for _, alias := range aliases {
		token := ":" + alias + ":"
		if unicode, ok := emoji.CodeMap()[token]; ok && strings.TrimSpace(unicode) != "" {
			return applySlackSkinTone(strings.TrimSpace(unicode), tone), true
		}
	}
	// Fallback: search in code map with normalized alias variants.
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(strings.Trim(s, ":")))
		s = strings.ReplaceAll(s, "-", "_")
		return s
	}
	needle := norm(base)
	for token, unicode := range emoji.CodeMap() {
		if norm(token) == needle {
			if strings.TrimSpace(unicode) != "" {
				return applySlackSkinTone(strings.TrimSpace(unicode), tone), true
			}
		}
	}
	return "", false
}

func applySlackSkinTone(unicode, tone string) string {
	suffix := strings.ToLower(strings.TrimSpace(tone))
	if suffix == "" {
		return unicode
	}
	mod := ""
	switch suffix {
	case "skin-tone-2":
		mod = "\U0001F3FB"
	case "skin-tone-3":
		mod = "\U0001F3FC"
	case "skin-tone-4":
		mod = "\U0001F3FD"
	case "skin-tone-5":
		mod = "\U0001F3FE"
	case "skin-tone-6":
		mod = "\U0001F3FF"
	default:
		return unicode
	}
	return unicode + mod
}

func twemojiCodeCandidates(unicode string) []string {
	code := twemojiCodeFromUnicode(unicode)
	if code == "" {
		return nil
	}
	cands := []string{code}
	if strings.Contains(code, "-fe0f") {
		trimmed := strings.ReplaceAll(code, "-fe0f", "")
		trimmed = strings.TrimPrefix(trimmed, "fe0f-")
		if trimmed != "" && trimmed != code {
			cands = append(cands, trimmed)
		}
	}
	return cands
}

func twemojiCodeFromUnicode(unicode string) string {
	parts := make([]string, 0, len(unicode))
	for _, r := range unicode {
		parts = append(parts, fmt.Sprintf("%x", r))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "-")
}

func cachedTwemojiResource(code string) (fyne.Resource, bool) {
	twemojiCacheMu.RLock()
	defer twemojiCacheMu.RUnlock()
	if res, ok := twemojiCache[code]; ok {
		return res, true
	}
	if twemojiMissCache[code] {
		return nil, true
	}
	return nil, false
}

func fetchTwemojiResource(codes []string, onDone func(fyne.Resource)) {
	if len(codes) == 0 {
		onDone(nil)
		return
	}
	client := http.Client{Timeout: 4 * time.Second}
	for _, code := range codes {
		if strings.TrimSpace(code) == "" {
			continue
		}
		url := code
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			url = "https://cdn.jsdelivr.net/gh/twitter/twemoji@14.0.2/assets/72x72/" + code + ".png"
		}
		resp, err := client.Get(url)
		if err != nil {
			cacheMiss(code)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || readErr != nil || len(body) == 0 {
			cacheMiss(code)
			continue
		}
		res := fyne.NewStaticResource("emoji-image", body)
		twemojiCacheMu.Lock()
		for _, key := range codes {
			if strings.TrimSpace(key) != "" {
				twemojiCache[key] = res
			}
		}
		twemojiCacheMu.Unlock()
		onDone(res)
		return
	}
	onDone(nil)
}

func cacheMiss(code string) {
	twemojiCacheMu.Lock()
	twemojiMissCache[code] = true
	twemojiCacheMu.Unlock()
}

func setWorkspaceEmojiMap(raw map[string]string) {
	urls := map[string]string{}
	aliases := map[string]string{}
	for key, value := range raw {
		name := normalizeEmojiKey(key)
		if name == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "alias:") {
			aliases[name] = normalizeEmojiKey(strings.TrimPrefix(value, "alias:"))
			continue
		}
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			urls[name] = value
		}
	}
	workspaceEmojiMu.Lock()
	workspaceEmojiURLByKey = urls
	workspaceEmojiAliasByKey = aliases
	workspaceEmojiMu.Unlock()
}

func resolveWorkspaceEmojiURL(name string) (string, bool) {
	key := normalizeEmojiKey(name)
	if key == "" {
		return "", false
	}
	workspaceEmojiMu.RLock()
	defer workspaceEmojiMu.RUnlock()
	seen := map[string]bool{}
	for depth := 0; depth < 8 && key != ""; depth++ {
		if seen[key] {
			return "", false
		}
		seen[key] = true
		if url := strings.TrimSpace(workspaceEmojiURLByKey[key]); url != "" {
			return url, true
		}
		next := normalizeEmojiKey(workspaceEmojiAliasByKey[key])
		if next == "" || next == key {
			return "", false
		}
		key = next
	}
	return "", false
}

func normalizeEmojiKey(name string) string {
	key := strings.ToLower(strings.Trim(strings.TrimSpace(name), ":"))
	key = strings.TrimSpace(key)
	if strings.Contains(key, "::") {
		key = strings.SplitN(key, "::", 2)[0]
	}
	return key
}
