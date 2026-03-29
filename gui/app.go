package gui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/stefan/slack-gui/api"
)

const appID = "com.bluebubbles-tui.slackgui"

// Preference keys for persistent UI state.
const (
	prefShowChatList    = "ui.show_chat_list"
	prefCompactMode     = "ui.compact_mode"
	prefDarkMode        = "ui.dark_mode"
	prefFontSize        = "ui.font_size"
	prefBoldAll         = "ui.bold_all"
	prefFontFamily      = "ui.font_family"
	prefWindowWidth     = "ui.window_width"
	prefWindowHeight    = "ui.window_height"
	prefPaneLayoutState = "ui.pane_layout_state"
	prefPaneSeparators  = "ui.pane_separators"
)

// Section keys for the sidebar channel list.
const (
	sectionNew     = "new"
	sectionThreads = "threads"
	sectionDMs     = "dms"
	sectionGroups  = "groups"
	sectionRooms   = "rooms"
	sectionBots    = "bots"
)

// Timing constants for scroll-to-bottom retries after layout changes.
// Multiple attempts are needed because Fyne lays out widgets asynchronously.
const (
	reloadDebounce       = 250 * time.Millisecond
	realtimeStartupDelay = 700 * time.Millisecond
	postSendRefreshDelay = 130 * time.Millisecond
)

// Image preview window dimensions.
const (
	imagePreviewMinWidth  = float32(560)
	imagePreviewMinHeight = float32(420)
	imageWindowWidth      = float32(700)
	imageWindowHeight     = float32(520)
)

// App is the top-level application controller. All fields are accessed only
// from the Fyne main goroutine (via fyne.Do) except paneReloadMu/paneReloadTimers
// which are protected by paneReloadMu, and realtimeStop/realtimeStopOnce which
// are written once before the goroutine starts.
type App struct {
	client       *api.Client
	info         *api.AuthInfo
	appToken     string
	users        map[string]string
	userByHandle map[string]string
	userByID     map[string]string
	userInfoByID map[string]api.UserInfo
	groupNameCache map[string]string

	realtimeStop     chan struct{}
	realtimeStopOnce sync.Once
	// paneReloadMu protects paneReloadTimers, which is written from timer goroutines.
	paneReloadMu     sync.Mutex
	paneReloadTimers map[int]*time.Timer

	fyneApp  fyne.App
	win      fyne.Window
	appTheme *compactTheme

	channels         []api.Channel
	channelByID      map[string]api.Channel
	listItems        []chatListItem
	recentThreads    []threadListEntry
	sectionCollapsed map[string]bool

	channelSearch *widget.Entry
	channelList   *widget.List
	chatListPane  *fixedWidthWrap
	rootContent   *fyne.Container
	showChatList  bool

	paneManager *paneManager

	windowWidth        float32
	windowHeight       float32
	showPaneSeparators bool

	initialChannelID string
	initialThreadTS  string
}

type chatListItem struct {
	header     string
	sectionKey string
	channel    *api.Channel
	thread     *threadListEntry
}

type threadListEntry struct {
	ChannelID    string
	ChannelLabel string
	ThreadTS     string
	Title        string
	LastActivity time.Time
}

func New(c *api.Client, info *api.AuthInfo, appToken string) *App {
	return &App{
		client:           c,
		info:             info,
		appToken:         strings.TrimSpace(appToken),
		users:            map[string]string{},
		userByHandle:     map[string]string{},
		userByID:         map[string]string{},
		userInfoByID:     map[string]api.UserInfo{},
		groupNameCache:   map[string]string{},
		paneReloadTimers: map[int]*time.Timer{},
		channelByID:      map[string]api.Channel{},
		showChatList:     true,
		showPaneSeparators: true,
		windowWidth:      896,
		windowHeight:     820,
		sectionCollapsed: map[string]bool{},
	}
}

func (a *App) SetInitialOpen(channelID, threadTS string) {
	a.initialChannelID = strings.TrimSpace(channelID)
	a.initialThreadTS = strings.TrimSpace(threadTS)
}

func (a *App) Run() {
	a.fyneApp = app.NewWithID(appID)
	a.appTheme = newCompactTheme()
	a.loadUIState()
	a.fyneApp.Settings().SetTheme(a.appTheme)

	a.win = a.fyneApp.NewWindow("Slack")
	a.win.Resize(fyne.NewSize(a.windowWidth, a.windowHeight))

	a.buildSidebar()
	a.paneManager = newPaneManager(
		func(p *chatPane) { a.focusPaneInput(p) },
		func() *chatPane {
			return newChatPane(
				func(cp *chatPane) { a.paneManager.setFocus(cp) },
				func(cp *chatPane) { a.sendFromPane(cp) },
				func(cp *chatPane) { a.exitThreadInPane(cp) },
				func(cp *chatPane) { a.clearReplyTarget(cp) },
				func(cp *chatPane) { a.schedulePaneScrollToBottom(cp) },
				a.handleInputShortcut,
			)
		},
	)
	a.paneManager.setShowSeparators(a.showPaneSeparators)
	a.restorePaneLayoutState()

	a.rootContent = container.NewBorder(nil, nil, a.chatListPane, nil, a.paneManager.widget())
	a.win.SetContent(a.rootContent)
	a.win.SetMainMenu(a.buildMainMenu())
	a.registerShortcuts()
	a.fyneApp.Lifecycle().SetOnExitedForeground(func() {
		fyne.Do(func() {
			a.paneManager.setAppFocused(false)
		})
	})
	a.fyneApp.Lifecycle().SetOnEnteredForeground(func() {
		fyne.Do(func() {
			a.paneManager.setAppFocused(true)
			a.focusPaneInput(a.paneManager.focusedPane())
		})
	})
	a.win.SetOnClosed(func() {
		a.stopRealtimeUpdates()
		a.saveWindowSizePreference()
		a.savePaneLayoutState()
	})

	if err := a.loadInitialData(); err != nil {
		dialog.ShowError(err, a.win)
	}
	a.applyInitialOpen()
	a.refreshPaneTitles()
	a.focusPaneInput(a.paneManager.focusedPane())
	// Delay realtime startup slightly to avoid competing with initial full loads.
	time.AfterFunc(realtimeStartupDelay, func() {
		a.startRealtimeUpdates()
	})
	a.win.ShowAndRun()
}

func (a *App) buildSidebar() {
	a.channelSearch = widget.NewEntry()
	a.channelSearch.SetPlaceHolder("Search chats…")
	a.channelSearch.OnChanged = func(_ string) { a.rebuildFilteredChannels() }

	a.channelList = widget.NewList(
		func() int { return len(a.listItems) },
		func() fyne.CanvasObject {
			lbl := widget.NewLabel("channel")
			lbl.Wrapping = fyne.TextTruncate
			return lbl
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(a.listItems) {
				return
			}
			item := a.listItems[id]
			if item.channel == nil && item.thread == nil {
				obj.(*widget.Label).TextStyle = fyne.TextStyle{Bold: true}
				obj.(*widget.Label).SetText(a.sectionHeaderLabel(item.sectionKey, item.header))
				return
			}
			obj.(*widget.Label).TextStyle = fyne.TextStyle{}
			if item.channel != nil {
				obj.(*widget.Label).SetText(a.chatListLabel(*item.channel))
				return
			}
			obj.(*widget.Label).SetText("↳ " + strings.TrimSpace(item.thread.Title))
		},
	)
	a.channelList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(a.listItems) {
			return
		}
		item := a.listItems[id]
		if item.channel == nil && item.thread == nil {
			a.toggleSection(item.sectionKey)
			a.channelList.Unselect(id)
			return
		}
		if item.channel == nil && item.thread != nil {
			a.openThreadFromList(*item.thread)
			a.channelList.Unselect(id)
			return
		}
		a.assignChannelToFocusedPane(item.channel.ID)
	}

	left := container.NewBorder(
		a.channelSearch,
		nil, nil, nil,
		a.channelList,
	)
	a.chatListPane = newFixedWidthWrap(left, 130)
	a.chatListPane.show = a.showChatList
}

func (a *App) loadInitialData() error {
	if dir, err := a.client.UserDirectory(); err != nil {
		log.Printf("[SLACK-GUI] users directory failed: %v", err)
	} else {
		a.buildUserDirectory(dir)
	}
	channels, err := a.client.ListChannels(200)
	if err != nil {
		return err
	}
	sort.Slice(channels, func(i, j int) bool {
		return strings.ToLower(channels[i].Name) < strings.ToLower(channels[j].Name)
	})
	a.channels = channels
	a.channelByID = map[string]api.Channel{}
	for _, ch := range channels {
		a.channelByID[ch.ID] = ch
	}
	a.refreshChannelDisplayNames()
	a.refreshRecentThreads()
	a.rebuildFilteredChannels()

	for _, p := range a.paneManager.allPanes() {
		if strings.TrimSpace(p.channelID) != "" {
			go a.loadMessagesForPane(p)
		}
	}
	if idx := a.firstSelectableListIndex(); idx >= 0 {
		a.channelList.Select(idx)
	}
	return nil
}

func (a *App) refreshRecentThreads() {
	entries := make([]threadListEntry, 0, len(a.channels))
	candidates := make([]api.Channel, 0, len(a.channels))
	for _, ch := range a.channels {
		if strings.TrimSpace(ch.ID) == "" {
			continue
		}
		if !ch.HasUnread && strings.TrimSpace(ch.LatestTS) == "" {
			continue
		}
		candidates = append(candidates, ch)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return api.ParseSlackTSOrZero(candidates[i].LatestTS).After(api.ParseSlackTSOrZero(candidates[j].LatestTS))
	})
	if len(candidates) > 24 {
		candidates = candidates[:24]
	}
	for _, ch := range candidates {
		if strings.TrimSpace(ch.ID) == "" {
			continue
		}
		msgs, err := a.client.ChannelHistory(ch.ID, 80, a.users)
		if err != nil {
			continue
		}
		byRoot := map[string]threadListEntry{}
		for _, m := range msgs {
			root := strings.TrimSpace(m.ThreadTS)
			if root == "" {
				if m.ReplyCount <= 0 {
					continue
				}
				root = strings.TrimSpace(m.TS)
			}
			if root == "" {
				continue
			}
			existing, ok := byRoot[root]
			if !ok || m.Time.After(existing.LastActivity) {
				title := strings.TrimSpace(m.Text)
				if title == "" {
					title = "(no text)"
				}
				byRoot[root] = threadListEntry{
					ChannelID:    ch.ID,
					ChannelLabel: a.chatListLabel(ch),
					ThreadTS:     root,
					Title:        truncate(title, 70),
					LastActivity: m.Time,
				}
			}
		}
		for _, t := range byRoot {
			entries = append(entries, t)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastActivity.After(entries[j].LastActivity)
	})
	if len(entries) > 10 {
		entries = entries[:10]
	}
	a.recentThreads = entries
}

func (a *App) refreshChannelDisplayNames() {
	for i := range a.channels {
		ch := &a.channels[i]
		switch {
		case ch.IsIM:
			if n := strings.TrimSpace(a.dmDisplayName(ch.UserID)); n != "" {
				ch.DisplayName = n
			}
		case ch.IsMPIM:
			participants := a.resolveGroupParticipantNames(ch.ID)
			if len(participants) > 0 {
				ch.DisplayName = strings.Join(participants, ", ")
			}
		default:
			ch.DisplayName = ch.Name
		}
	}
	a.channelByID = map[string]api.Channel{}
	for _, ch := range a.channels {
		a.channelByID[ch.ID] = ch
	}
}

func (a *App) resolveGroupParticipantNames(channelID string) []string {
	if cached := strings.TrimSpace(a.groupNameCache[channelID]); cached != "" {
		return strings.Split(cached, ", ")
	}
	memberIDs, err := a.client.ConversationMembers(channelID)
	if err != nil {
		log.Printf("[SLACK-GUI] conversations.members failed for %s: %v", channelID, err)
		return nil
	}
	out := make([]string, 0, len(memberIDs))
	selfID := strings.TrimSpace(a.info.UserID)
	for _, id := range memberIDs {
		id = strings.TrimSpace(id)
		if id == "" || id == selfID {
			continue
		}
		name := strings.TrimSpace(a.users[id])
		if name == "" {
			name = id
		}
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	if len(out) > 0 {
		a.groupNameCache[channelID] = strings.Join(out, ", ")
	}
	return out
}

func (a *App) rebuildFilteredChannels() {
	query := strings.ToLower(strings.TrimSpace(a.channelSearch.Text))
	newMsgs := make([]api.Channel, 0)
	dms := make([]api.Channel, 0)
	groups := make([]api.Channel, 0)
	rooms := make([]api.Channel, 0)
	bots := make([]api.Channel, 0)
	threads := make([]threadListEntry, 0, len(a.recentThreads))
	for _, ch := range a.channels {
		if !ch.IsIM && !ch.IsMPIM && !ch.IsMember {
			continue
		}
		if ch.IsIM && strings.TrimSpace(a.chatBaseName(ch)) == "direct message" {
			continue
		}
		candidate := strings.ToLower(strings.TrimSpace(a.chatBaseName(ch)))
		if query != "" && !strings.Contains(candidate, query) {
			continue
		}
		if ch.HasUnread || ch.UnreadCount > 0 {
			newMsgs = append(newMsgs, ch)
			continue
		}
		switch {
		case ch.IsIM:
			if a.isBotOrAppDM(ch.UserID) {
				bots = append(bots, ch)
			} else {
				dms = append(dms, ch)
			}
		case ch.IsMPIM:
			groups = append(groups, ch)
		default:
			rooms = append(rooms, ch)
		}
	}
	for _, t := range a.recentThreads {
		match := strings.Contains(strings.ToLower(strings.TrimSpace(t.Title)), query) ||
			strings.Contains(strings.ToLower(strings.TrimSpace(t.ChannelLabel)), query)
		if query == "" || match {
			threads = append(threads, t)
		}
	}
	sortByName := func(items []api.Channel) {
		sort.SliceStable(items, func(i, j int) bool {
			leftUnread := items[i].HasUnread || items[i].UnreadCount > 0
			rightUnread := items[j].HasUnread || items[j].UnreadCount > 0
			if leftUnread != rightUnread {
				return leftUnread
			}
			leftLatest := api.ParseSlackTSOrZero(items[i].LatestTS)
			rightLatest := api.ParseSlackTSOrZero(items[j].LatestTS)
			if !leftLatest.Equal(rightLatest) {
				return leftLatest.After(rightLatest)
			}
			return strings.ToLower(a.chatBaseName(items[i])) < strings.ToLower(a.chatBaseName(items[j]))
		})
	}
	sortByLatest := func(items []api.Channel) {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].UnreadCount != items[j].UnreadCount {
				return items[i].UnreadCount > items[j].UnreadCount
			}
			leftLatest := api.ParseSlackTSOrZero(items[i].LatestTS)
			rightLatest := api.ParseSlackTSOrZero(items[j].LatestTS)
			if !leftLatest.Equal(rightLatest) {
				return leftLatest.After(rightLatest)
			}
			return strings.ToLower(a.chatBaseName(items[i])) < strings.ToLower(a.chatBaseName(items[j]))
		})
	}
	sortByLatest(newMsgs)
	sortByName(dms)
	sortByName(groups)
	sortByName(rooms)
	sortByName(bots)
	sort.SliceStable(threads, func(i, j int) bool {
		return threads[i].LastActivity.After(threads[j].LastActivity)
	})

	a.listItems = a.listItems[:0]
	appendSection := func(key, title string, items []api.Channel) {
		if len(items) == 0 && key != sectionThreads {
			return
		}
		a.listItems = append(a.listItems, chatListItem{header: title, sectionKey: key})
		if a.isSectionCollapsed(key) {
			return
		}
		for i := range items {
			ch := items[i]
			a.listItems = append(a.listItems, chatListItem{channel: &ch})
		}
	}
	appendSection(sectionNew, "New Messages", newMsgs)
	appendSection(sectionThreads, "Threads", nil)
	if !a.isSectionCollapsed(sectionThreads) {
		for i := range threads {
			t := threads[i]
			a.listItems = append(a.listItems, chatListItem{thread: &t})
		}
	}
	appendSection(sectionDMs, "Direct Messages", dms)
	appendSection(sectionGroups, "Group Chats", groups)
	appendSection(sectionRooms, "Channels", rooms)
	appendSection(sectionBots, "Bots & Apps", bots)
	if a.channelList != nil {
		a.channelList.Refresh()
	}
}

func (a *App) chatListLabel(ch api.Channel) string {
	unread := ""
	if ch.UnreadCount > 0 {
		unread = fmt.Sprintf(" ● %d", ch.UnreadCount)
	} else if ch.HasUnread {
		unread = " ●"
	}
	if ch.IsIM {
		return "@ " + a.chatBaseName(ch) + unread
	}
	if ch.IsMPIM {
		return "◉ " + a.chatBaseName(ch) + unread
	}
	name := a.chatBaseName(ch)
	if name == "" {
		name = "channel"
	}
	return "# " + name + unread
}

func (a *App) chatBaseName(ch api.Channel) string {
	name := strings.TrimSpace(ch.DisplayName)
	if name == "" {
		name = strings.TrimSpace(ch.Name)
	}
	if ch.IsMPIM {
		name = a.stripSelfFromGroupName(name)
	}
	if name == "" {
		switch {
		case ch.IsIM:
			return "direct message"
		case ch.IsMPIM:
			return "group"
		default:
			return "channel"
		}
	}
	return name
}

func (a *App) chatPrefix(ch api.Channel) string {
	if ch.IsIM {
		return "@"
	}
	if ch.IsMPIM {
		return "◉ "
	}
	return "#"
}

func (a *App) chatInputPlaceholder(ch api.Channel) string {
	name := a.chatBaseName(ch)
	if ch.IsIM {
		return "Message @" + name
	}
	if ch.IsMPIM {
		return "Message " + name
	}
	return "Message #" + name
}

func (a *App) stripSelfFromGroupName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	selfCandidates := map[string]struct{}{}
	if self := strings.TrimSpace(a.info.UserName); self != "" {
		selfCandidates[strings.ToLower(self)] = struct{}{}
	}
	if selfDisplay := strings.TrimSpace(a.users[a.info.UserID]); selfDisplay != "" {
		selfCandidates[strings.ToLower(selfDisplay)] = struct{}{}
	}
	parts := strings.Split(name, ",")
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		clean := strings.TrimSpace(p)
		if clean == "" {
			continue
		}
		if _, ok := selfCandidates[strings.ToLower(clean)]; ok {
			continue
		}
		filtered = append(filtered, clean)
	}
	if len(filtered) == 0 {
		return ""
	}
	return strings.Join(filtered, ", ")
}

func (a *App) refreshPaneTitles() {
	for _, p := range a.paneManager.allPanes() {
		if p.channelID == "" {
			p.setTitle("Select a channel")
			continue
		}
		ch, ok := a.channelByID[p.channelID]
		if !ok {
			p.setTitle("#unknown")
			continue
		}
		name := a.chatBaseName(ch)
		p.channelName = name
		if strings.TrimSpace(p.threadTS) != "" {
			p.setTitle(a.chatPrefix(ch) + name + " — thread")
		} else {
			p.setTitle(a.chatPrefix(ch) + name + " — " + a.info.TeamName)
		}
		p.input.SetPlaceHolder(a.chatInputPlaceholder(ch))
	}
}

func (a *App) assignChannelToFocusedPane(channelID string) {
	p := a.paneManager.focusedPane()
	if p == nil {
		return
	}
	ch, ok := a.channelByID[channelID]
	if !ok {
		return
	}
	p.channelID = channelID
	p.channelName = a.chatBaseName(ch)
	p.threadTS = ""
	p.setThreadBanner("")
	p.replyTarget = nil
	p.setTitle(fmt.Sprintf("%s%s — %s", a.chatPrefix(ch), p.channelName, a.info.TeamName))
	p.input.SetPlaceHolder(a.chatInputPlaceholder(ch))
	p.clearMessages()
	a.clearChannelUnreadState(channelID)
	a.focusPaneInput(p)
	a.schedulePaneScrollToBottom(p)
	a.savePaneLayoutState()
	go a.loadMessagesForPane(p)
}

func (a *App) clearChannelUnreadState(channelID string) {
	for i := range a.channels {
		if strings.TrimSpace(a.channels[i].ID) != strings.TrimSpace(channelID) {
			continue
		}
		a.channels[i].HasUnread = false
		a.channels[i].UnreadCount = 0
		a.channelByID[channelID] = a.channels[i]
		break
	}
	a.rebuildFilteredChannels()
}

func (a *App) markChannelUnreadState(channelID, latestTS string) {
	for i := range a.channels {
		if strings.TrimSpace(a.channels[i].ID) != strings.TrimSpace(channelID) {
			continue
		}
		a.channels[i].HasUnread = true
		a.channels[i].UnreadCount++
		if strings.TrimSpace(latestTS) != "" {
			a.channels[i].LatestTS = latestTS
		}
		a.channelByID[channelID] = a.channels[i]
		break
	}
	a.rebuildFilteredChannels()
}

func (a *App) focusedPaneShowingChannel(channelID string) bool {
	if a.paneManager == nil {
		return false
	}
	focused := a.paneManager.focusedPane()
	if focused == nil {
		return false
	}
	if !a.paneManager.appFocused {
		return false
	}
	return strings.TrimSpace(focused.channelID) == strings.TrimSpace(channelID)
}

func (a *App) loadMessagesForPane(p *chatPane) {
	if p == nil || strings.TrimSpace(p.channelID) == "" {
		return
	}
	channelID := p.channelID
	threadTS := strings.TrimSpace(p.threadTS)
	var (
		msgs []api.Message
		err  error
	)
	if threadTS == "" {
		msgs, err = a.client.ChannelHistory(channelID, 120, a.users)
	} else {
		msgs, err = a.client.ThreadReplies(channelID, threadTS, 120, a.users)
	}
	fyne.Do(func() {
		if p.channelID != channelID || strings.TrimSpace(p.threadTS) != threadTS {
			return
		}
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		if threadTS == "" {
			p.setThreadBanner("")
		} else if len(msgs) > 0 {
			root := msgs[0]
			p.setThreadBanner(fmt.Sprintf("Thread: %s — %s", senderName(root), truncate(strings.TrimSpace(root.Text), 80)))
		} else {
			p.setThreadBanner("Thread")
		}
		for i := range msgs {
			msgs[i].Text = a.formatMentionsForDisplay(msgs[i].Text)
			msgs[i].ForwardedText = a.formatMentionsForDisplay(msgs[i].ForwardedText)
		}
		p.setMessages(msgs, a.info.UserID, a.info.UserID, func(m api.Message) {
			a.openThreadInPane(p, m)
		}, func(m api.Message) {
			a.setReplyTarget(p, m)
		}, func(f api.File) {
			a.openMediaDialog(f)
		})
		a.schedulePaneScrollToBottom(p)
	})
}

// schedulePaneScrollToBottom retries ScrollToBottom at increasing intervals.
// Multiple attempts are needed because Fyne widget layout is asynchronous and
// the scroll container may not know its final size immediately.
func (a *App) schedulePaneScrollToBottom(p *chatPane) {
	if p == nil {
		return
	}
	for _, d := range []time.Duration{0, 40 * time.Millisecond, 100 * time.Millisecond, 220 * time.Millisecond, 420 * time.Millisecond, 800 * time.Millisecond, 1400 * time.Millisecond} {
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

func (a *App) openThreadInPane(p *chatPane, m api.Message) {
	rootTS := threadRootTS(m)
	if p == nil || rootTS == "" {
		return
	}
	p.threadTS = rootTS
	p.replyTarget = nil
	p.replyHolder.Hide()
	p.setThreadBanner(fmt.Sprintf("Thread: %s — %s", senderName(m), truncate(strings.TrimSpace(m.Text), 80)))
	p.setTitle(fmt.Sprintf("#%s — thread", p.channelName))
	a.schedulePaneScrollToBottom(p)
	a.savePaneLayoutState()
	go a.loadMessagesForPane(p)
}

func (a *App) exitThreadInPane(p *chatPane) {
	if p == nil || strings.TrimSpace(p.threadTS) == "" {
		return
	}
	p.threadTS = ""
	p.setThreadBanner("")
	p.replyTarget = nil
	p.replyHolder.Hide()
	p.setTitle(fmt.Sprintf("#%s — %s", p.channelName, a.info.TeamName))
	a.schedulePaneScrollToBottom(p)
	a.savePaneLayoutState()
	go a.loadMessagesForPane(p)
}

func (a *App) setReplyTarget(p *chatPane, m api.Message) {
	p.replyTarget = &m
	p.replyLabel.SetText(fmt.Sprintf("Replying to %s: %s", senderName(m), truncate(m.Text, 80)))
	p.replyHolder.Show()
}

func (a *App) clearReplyTarget(p *chatPane) {
	p.replyTarget = nil
	p.replyHolder.Hide()
	p.replyLabel.SetText("")
}

func (a *App) sendFromPane(p *chatPane) {
	if p == nil {
		return
	}
	text := strings.TrimSpace(p.input.Text)
	text = a.expandOutgoingMentions(text)
	if text == "" || strings.TrimSpace(p.channelID) == "" {
		return
	}
	threadTS := p.threadTS
	if p.replyTarget != nil {
		threadTS = threadRootTS(*p.replyTarget)
	}
	if err := a.client.PostMessage(p.channelID, text, threadTS); err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	p.input.SetText("")
	a.clearReplyTarget(p)
	go func() {
		time.Sleep(postSendRefreshDelay)
		a.loadMessagesForPane(p)
	}()
}

func (a *App) buildUserDirectory(dir []api.UserInfo) {
	a.userByHandle = map[string]string{}
	a.userByID = map[string]string{}
	a.userInfoByID = map[string]api.UserInfo{}
	a.users = map[string]string{}
	for _, u := range dir {
		id := strings.TrimSpace(u.ID)
		if id == "" {
			continue
		}
		a.userInfoByID[id] = u
		display := strings.TrimSpace(u.DisplayName)
		if display == "" {
			display = strings.TrimSpace(u.RealName)
		}
		if display == "" {
			display = strings.TrimSpace(u.Username)
		}
		if display != "" {
			a.userByID[id] = display
			a.users[id] = display
		}
		if h := strings.TrimSpace(u.Username); h != "" {
			a.userByHandle[strings.ToLower(h)] = id
		}
	}
}

func (a *App) dmDisplayName(userID string) string {
	id := strings.TrimSpace(userID)
	if id == "" {
		return ""
	}
	if n := strings.TrimSpace(a.users[id]); n != "" {
		return n
	}
	if n := strings.TrimSpace(a.userByID[id]); n != "" {
		return n
	}
	info, err := a.client.UserInfo(id)
	if err != nil || info == nil {
		return ""
	}
	a.userInfoByID[id] = *info
	if h := strings.TrimSpace(info.Username); h != "" {
		a.userByHandle[strings.ToLower(h)] = id
	}
	name := strings.TrimSpace(info.DisplayName)
	if name == "" {
		name = strings.TrimSpace(info.RealName)
	}
	if name == "" {
		name = strings.TrimSpace(info.Username)
	}
	if name != "" {
		a.userByID[id] = name
		a.users[id] = name
	}
	return name
}

func (a *App) firstSelectableListIndex() int {
	for i, item := range a.listItems {
		if item.channel != nil {
			return i
		}
	}
	return -1
}

func (a *App) isBotOrAppDM(userID string) bool {
	id := strings.TrimSpace(userID)
	if id == "" {
		return false
	}
	info, ok := a.userInfoByID[id]
	if !ok {
		fetched, err := a.client.UserInfo(id)
		if err != nil || fetched == nil {
			return false
		}
		info = *fetched
		a.userInfoByID[id] = info
	}
	return info.IsBot || info.IsAppUser
}

func (a *App) isSectionCollapsed(section string) bool {
	if a.sectionCollapsed == nil {
		a.sectionCollapsed = map[string]bool{}
	}
	collapsed, ok := a.sectionCollapsed[section]
	if !ok {
		if section == sectionNew {
			return false
		}
		return true
	}
	return collapsed
}

func (a *App) toggleSection(section string) {
	if strings.TrimSpace(section) == "" {
		return
	}
	if a.sectionCollapsed == nil {
		a.sectionCollapsed = map[string]bool{}
	}
	a.sectionCollapsed[section] = !a.isSectionCollapsed(section)
	a.rebuildFilteredChannels()
}

func (a *App) sectionHeaderLabel(section, title string) string {
	arrow := "▼"
	if a.isSectionCollapsed(section) {
		arrow = "▶"
	}
	return fmt.Sprintf("%s %s", arrow, title)
}

func (a *App) openThreadFromList(t threadListEntry) {
	if strings.TrimSpace(t.ChannelID) == "" || strings.TrimSpace(t.ThreadTS) == "" {
		return
	}
	p := a.paneManager.focusedPane()
	if p == nil {
		return
	}
	ch, ok := a.channelByID[t.ChannelID]
	if !ok {
		return
	}
	p.channelID = t.ChannelID
	p.channelName = a.chatBaseName(ch)
	p.threadTS = strings.TrimSpace(t.ThreadTS)
	p.replyTarget = nil
	p.setThreadBanner("Thread: " + strings.TrimSpace(t.Title))
	p.input.SetPlaceHolder(a.chatInputPlaceholder(ch))
	p.setTitle(fmt.Sprintf("%s%s — thread", a.chatPrefix(ch), p.channelName))
	a.schedulePaneScrollToBottom(p)
	a.savePaneLayoutState()
	go a.loadMessagesForPane(p)
}

var mentionHandleRE = regexp.MustCompile(`(^|[\s(\[{])@([a-zA-Z0-9._-]{1,80})`)
var mentionSlackIDRE = regexp.MustCompile(`<@([A-Z0-9]+)>`)

func (a *App) expandOutgoingMentions(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return mentionHandleRE.ReplaceAllStringFunc(text, func(m string) string {
		g := mentionHandleRE.FindStringSubmatch(m)
		if len(g) != 3 {
			return m
		}
		prefix := g[1]
		handle := strings.ToLower(g[2])
		id := strings.TrimSpace(a.userByHandle[handle])
		if id == "" {
			return m
		}
		return prefix + "<@" + id + ">"
	})
}

func (a *App) formatMentionsForDisplay(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return mentionSlackIDRE.ReplaceAllStringFunc(text, func(m string) string {
		g := mentionSlackIDRE.FindStringSubmatch(m)
		if len(g) != 2 {
			return m
		}
		id := strings.TrimSpace(g[1])
		if id == "" {
			return m
		}
		if name := strings.TrimSpace(a.userByID[id]); name != "" {
			return "@" + name
		}
		if name := strings.TrimSpace(a.users[id]); name != "" {
			return "@" + name
		}
		return "@user"
	})
}

func (a *App) openMediaDialog(f api.File) {
	imageURL := strings.TrimSpace(f.BestImageURL())
	if imageURL == "" {
		if strings.TrimSpace(f.Permalink) != "" {
			u := mustParseURL(f.Permalink)
			dialog.ShowCustom("Media", "Close", widget.NewHyperlink(f.Name, u), a.win)
			return
		}
		dialog.ShowInformation("Media", "No preview URL available for this file.", a.win)
		return
	}
	go func() {
		data, _, err := a.client.FetchPrivateURL(imageURL)
		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(err, a.win)
				return
			}
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				dialog.ShowError(err, a.win)
				return
			}
			cimg := canvas.NewImageFromImage(img)
			cimg.FillMode = canvas.ImageFillContain
			cimg.SetMinSize(fyne.NewSize(imagePreviewMinWidth, imagePreviewMinHeight))
			w := a.fyneApp.NewWindow("Media: " + f.Name)
			w.SetContent(container.NewPadded(cimg))
			w.Resize(fyne.NewSize(imageWindowWidth, imageWindowHeight))
			w.Show()
		})
	}()
}

func (a *App) registerShortcuts() {
	c := a.win.Canvas()
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyName("H"), Modifier: fyne.KeyModifierControl}, func(_ fyne.Shortcut) {
		p := a.newPane()
		a.paneManager.splitFocused(splitHorizontal, p)
		a.focusPaneInput(a.paneManager.focusedPane())
		a.savePaneLayoutState()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyName("J"), Modifier: fyne.KeyModifierControl}, func(_ fyne.Shortcut) {
		p := a.newPane()
		a.paneManager.splitFocused(splitVertical, p)
		a.focusPaneInput(a.paneManager.focusedPane())
		a.savePaneLayoutState()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyName("W"), Modifier: fyne.KeyModifierControl}, func(_ fyne.Shortcut) {
		a.paneManager.closeFocused()
		a.focusPaneInput(a.paneManager.focusedPane())
		a.savePaneLayoutState()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyName("S"), Modifier: fyne.KeyModifierControl}, func(_ fyne.Shortcut) {
		a.toggleChatList()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyName("N"), Modifier: fyne.KeyModifierControl}, func(_ fyne.Shortcut) {
		a.openNewWindow()
	})
}

func (a *App) buildMainMenu() *fyne.MainMenu {
	colorModeLabel := "Switch to Light Mode"
	if !a.appTheme.dark {
		colorModeLabel = "Switch to Dark Mode"
	}
	compactLabel := "Enable Compact Mode"
	if a.appTheme.compactMode {
		compactLabel = "Disable Compact Mode"
	}
	separatorLabel := "Hide Pane Separators"
	if !a.showPaneSeparators {
		separatorLabel = "Show Pane Separators"
	}

	fontItems := make([]*fyne.MenuItem, 0)
	for _, name := range a.appTheme.availableFamilies() {
		n := name
		label := n
		if n == a.appTheme.curFamily {
			label = "✓ " + n
		}
		fontItems = append(fontItems, fyne.NewMenuItem(label, func() {
			a.setFont(n)
			a.win.SetMainMenu(a.buildMainMenu())
		}))
	}
	fontItem := fyne.NewMenuItem("Font", nil)
	fontItem.ChildMenu = fyne.NewMenu("", fontItems...)

	windowMenu := fyne.NewMenu("Window",
		fyne.NewMenuItem("New Window", func() { a.openNewWindow() }),
		fyne.NewMenuItem("Move Focused Pane to New Window", func() { a.moveFocusedPaneToNewWindow() }),
	)
	viewMenu := fyne.NewMenu("View",
		fyne.NewMenuItem("A+ Larger", func() {
			if a.appTheme.fontSize < 20 {
				a.appTheme.fontSize++
				a.fyneApp.Preferences().SetInt(prefFontSize, int(a.appTheme.fontSize))
				a.fyneApp.Settings().SetTheme(a.appTheme)
				a.refreshPanesForTheme()
			}
		}),
		fyne.NewMenuItem("A- Smaller", func() {
			if a.appTheme.fontSize > 8 {
				a.appTheme.fontSize--
				a.fyneApp.Preferences().SetInt(prefFontSize, int(a.appTheme.fontSize))
				a.fyneApp.Settings().SetTheme(a.appTheme)
				a.refreshPanesForTheme()
			}
		}),
		fyne.NewMenuItem("Toggle Bold", func() {
			a.appTheme.boldAll = !a.appTheme.boldAll
			a.fyneApp.Preferences().SetBool(prefBoldAll, a.appTheme.boldAll)
			a.fyneApp.Settings().SetTheme(a.appTheme)
			a.refreshPanesForTheme()
		}),
		fontItem,
		fyne.NewMenuItem(separatorLabel, func() { a.togglePaneSeparators() }),
		fyne.NewMenuItem("Toggle Channel List", func() { a.toggleChatList() }),
		fyne.NewMenuItem(compactLabel, func() { a.setCompactMode(!a.appTheme.compactMode) }),
		fyne.NewMenuItem(colorModeLabel, func() { a.setDarkMode(!a.appTheme.dark) }),
	)
	return fyne.NewMainMenu(windowMenu, viewMenu)
}

func (a *App) toggleChatList() {
	a.showChatList = !a.showChatList
	a.chatListPane.show = a.showChatList
	a.chatListPane.Refresh()
	a.rootContent.Refresh()
	a.fyneApp.Preferences().SetBool(prefShowChatList, a.showChatList)
}

func (a *App) togglePaneSeparators() {
	a.showPaneSeparators = !a.showPaneSeparators
	if a.paneManager != nil {
		a.paneManager.setShowSeparators(a.showPaneSeparators)
	}
	if a.fyneApp != nil {
		a.fyneApp.Preferences().SetBool(prefPaneSeparators, a.showPaneSeparators)
	}
	if a.win != nil {
		a.win.SetMainMenu(a.buildMainMenu())
	}
}

func (a *App) setCompactMode(enabled bool) {
	a.appTheme.compactMode = enabled
	a.fyneApp.Preferences().SetBool(prefCompactMode, enabled)
	a.fyneApp.Settings().SetTheme(a.appTheme)
	a.win.SetMainMenu(a.buildMainMenu())
	a.refreshPanesForTheme()
}

func (a *App) setDarkMode(enabled bool) {
	a.appTheme.dark = enabled
	a.fyneApp.Preferences().SetBool(prefDarkMode, enabled)
	a.fyneApp.Settings().SetTheme(a.appTheme)
	a.win.SetMainMenu(a.buildMainMenu())
	a.refreshPanesForTheme()
}

func (a *App) setFont(family string) {
	if _, ok := a.appTheme.fonts[family]; !ok {
		return
	}
	a.appTheme.curFamily = family
	a.fyneApp.Preferences().SetString(prefFontFamily, family)
	a.fyneApp.Settings().SetTheme(a.appTheme)
	a.refreshPanesForTheme()
}

func (a *App) refreshPanesForTheme() {
	for _, p := range a.paneManager.allPanes() {
		p.refreshForTheme()
		if strings.TrimSpace(p.channelID) != "" {
			go a.loadMessagesForPane(p)
		}
	}
}

func (a *App) openNewWindow() {
	a.launchWindowForPane("", "")
}

func (a *App) moveFocusedPaneToNewWindow() {
	p := a.paneManager.focusedPane()
	if p == nil {
		a.openNewWindow()
		return
	}
	if !a.launchWindowForPane(p.channelID, p.threadTS) {
		return
	}
	if len(a.paneManager.allPanes()) > 1 {
		a.paneManager.closeFocused()
		a.focusPaneInput(a.paneManager.focusedPane())
		a.savePaneLayoutState()
		return
	}
	p.channelID = ""
	p.channelName = ""
	p.threadTS = ""
	p.setThreadBanner("")
	p.clearMessages()
	p.setTitle("Select a channel")
}

func (a *App) launchWindowForPane(channelID, threadTS string) bool {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		log.Printf("[SLACK-GUI] unable to resolve executable: %v", err)
		return false
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(),
		"SLACK_OPEN_CHANNEL_ID="+strings.TrimSpace(channelID),
		"SLACK_OPEN_THREAD_TS="+strings.TrimSpace(threadTS),
	)
	if err := cmd.Start(); err != nil {
		log.Printf("[SLACK-GUI] unable to start new window: %v", err)
		return false
	}
	return true
}

func (a *App) applyInitialOpen() {
	if strings.TrimSpace(a.initialChannelID) == "" {
		return
	}
	p := a.paneManager.focusedPane()
	if p == nil {
		return
	}
	ch, ok := a.channelByID[a.initialChannelID]
	if !ok {
		return
	}
	p.channelID = ch.ID
	p.channelName = a.chatBaseName(ch)
	p.threadTS = strings.TrimSpace(a.initialThreadTS)
	if p.threadTS != "" {
		p.setThreadBanner("Thread view")
	} else {
		p.setThreadBanner("")
	}
	p.input.SetPlaceHolder(a.chatInputPlaceholder(ch))
	p.setTitle(fmt.Sprintf("%s%s — %s", a.chatPrefix(ch), p.channelName, a.info.TeamName))
	a.schedulePaneScrollToBottom(p)
	a.savePaneLayoutState()
	go a.loadMessagesForPane(p)
}

func (a *App) newPane() *chatPane {
	return newChatPane(
		func(cp *chatPane) { a.paneManager.setFocus(cp) },
		func(cp *chatPane) { a.sendFromPane(cp) },
		func(cp *chatPane) { a.exitThreadInPane(cp) },
		func(cp *chatPane) { a.clearReplyTarget(cp) },
		func(cp *chatPane) { a.schedulePaneScrollToBottom(cp) },
		a.handleInputShortcut,
	)
}

func (a *App) focusPaneInput(p *chatPane) {
	if p == nil || a.win == nil {
		return
	}
	a.win.Canvas().Focus(p.input)
}

func (a *App) loadUIState() {
	if a.fyneApp == nil || a.appTheme == nil {
		return
	}
	prefs := a.fyneApp.Preferences()
	a.showChatList = prefs.BoolWithFallback(prefShowChatList, true)
	a.appTheme.dark = prefs.BoolWithFallback(prefDarkMode, a.appTheme.dark)
	a.appTheme.compactMode = prefs.BoolWithFallback(prefCompactMode, false)
	fontSize := prefs.IntWithFallback(prefFontSize, int(a.appTheme.fontSize))
	if fontSize < 8 {
		fontSize = 8
	}
	if fontSize > 20 {
		fontSize = 20
	}
	a.appTheme.fontSize = float32(fontSize)
	a.appTheme.boldAll = prefs.BoolWithFallback(prefBoldAll, a.appTheme.boldAll)
	if family := prefs.StringWithFallback(prefFontFamily, a.appTheme.curFamily); family != "" {
		if _, ok := a.appTheme.fonts[family]; ok {
			a.appTheme.curFamily = family
		}
	}
	a.windowWidth = float32(prefs.FloatWithFallback(prefWindowWidth, 896))
	a.windowHeight = float32(prefs.FloatWithFallback(prefWindowHeight, 820))
	a.showPaneSeparators = prefs.BoolWithFallback(prefPaneSeparators, true)
	if a.windowWidth < 700 {
		a.windowWidth = 700
	}
	if a.windowHeight < 500 {
		a.windowHeight = 500
	}
}

func (a *App) saveWindowSizePreference() {
	if a.fyneApp == nil || a.win == nil {
		return
	}
	size := a.win.Canvas().Size()
	prefs := a.fyneApp.Preferences()
	prefs.SetFloat(prefWindowWidth, float64(size.Width))
	prefs.SetFloat(prefWindowHeight, float64(size.Height))
}

func (a *App) savePaneLayoutState() {
	if a.fyneApp == nil || a.paneManager == nil {
		return
	}
	raw, err := a.paneManager.serializeState()
	if err != nil {
		log.Printf("[SLACK-GUI] serialize layout failed: %v", err)
		return
	}
	a.fyneApp.Preferences().SetString(prefPaneLayoutState, raw)
}

func (a *App) restorePaneLayoutState() {
	if a.fyneApp == nil || a.paneManager == nil {
		return
	}
	raw := a.fyneApp.Preferences().StringWithFallback(prefPaneLayoutState, "")
	if strings.TrimSpace(raw) == "" {
		return
	}
	if err := a.paneManager.restoreState(raw); err != nil {
		log.Printf("[SLACK-GUI] restore layout failed: %v", err)
	}
}

func (a *App) handleInputShortcut(shortcut fyne.Shortcut) bool {
	custom, ok := shortcut.(*desktop.CustomShortcut)
	if !ok {
		return false
	}
	if custom.Modifier&fyne.KeyModifierControl == 0 {
		return false
	}
	key := fyne.KeyName(strings.ToUpper(string(custom.KeyName)))
	switch key {
	case fyne.KeyName("H"):
		p := a.newPane()
		a.paneManager.splitFocused(splitHorizontal, p)
		a.focusPaneInput(a.paneManager.focusedPane())
		a.savePaneLayoutState()
		return true
	case fyne.KeyName("J"):
		p := a.newPane()
		a.paneManager.splitFocused(splitVertical, p)
		a.focusPaneInput(a.paneManager.focusedPane())
		a.savePaneLayoutState()
		return true
	case fyne.KeyName("W"):
		a.paneManager.closeFocused()
		a.focusPaneInput(a.paneManager.focusedPane())
		a.savePaneLayoutState()
		return true
	case fyne.KeyName("S"):
		a.toggleChatList()
		return true
	case fyne.KeyName("N"):
		a.openNewWindow()
		return true
	}
	return false
}
