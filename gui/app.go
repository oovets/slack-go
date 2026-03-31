package gui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/stefan/slack-gui/api"
)

const appID = "com.bluebubbles-tui.slackgui"

var privateChannelLockIcon = fyne.NewStaticResource("private-lock.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="#8f96ab" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="11" width="14" height="10" rx="2" ry="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></svg>`))

// Preference keys for persistent UI state.
const (
	prefShowChatList    = "ui.show_chat_list"
	prefShowTimestamps  = "ui.show_timestamps"
	prefCompactMode     = "ui.compact_mode"
	prefDarkMode        = "ui.dark_mode"
	prefFontSize        = "ui.font_size"
	prefBoldAll         = "ui.bold_all"
	prefFontFamily      = "ui.font_family"
	prefWindowWidth     = "ui.window_width"
	prefWindowHeight    = "ui.window_height"
	prefPaneLayoutState = "ui.pane_layout_state"
	prefPaneSeparators  = "ui.pane_separators"
	prefFavorites       = "ui.favorites"
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
	reloadDebounce        = 250 * time.Millisecond
	realtimeStartupDelay  = 700 * time.Millisecond
	postSendRefreshDelay  = 130 * time.Millisecond
	sidebarSearchDebounce = 90 * time.Millisecond
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
// and realtimeMu/realtimeStop/realtimeRunning, which are protected by mutexes.
type App struct {
	client         *api.Client
	info           *api.AuthInfo
	appToken       string
	users          map[string]string
	userByHandle   map[string]string
	userByID       map[string]string
	userInfoByID   map[string]api.UserInfo
	groupNameCache map[string]string

	realtimeStop    chan struct{}
	realtimeRunning bool
	realtimeMu      sync.Mutex
	// paneReloadMu protects paneReloadTimers, which is written from timer goroutines.
	paneReloadMu     sync.Mutex
	paneReloadTimers map[int]*time.Timer

	fyneApp  fyne.App
	win      fyne.Window
	appTheme *compactTheme

	channels         []api.Channel
	channelByID      map[string]api.Channel
	favorites        map[string]bool
	listItems        []chatListItem
	recentThreads    []threadListEntry
	sectionCollapsed map[string]bool

	channelSearch      *widget.Entry
	channelList        *widget.List
	chatListPane       *fixedWidthWrap
	rootContent        *fyne.Container
	statusLabel        *widget.Label
	statusActionButton *widget.Button
	statusActionLabel  string
	statusActionFn     func()
	showChatList       bool
	showTimestamps     bool

	paneManager *paneManager

	windowWidth        float32
	windowHeight       float32
	showPaneSeparators bool

	selectedChannelID        string
	selectedThreadTS         string
	isProgrammaticListSelect bool
	suppressSidebarSelect    bool
	sidebarHoverListID       int
	statusClearTimer         *time.Timer
	sidebarSearchTimer       *time.Timer

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

type quickSwitchItem struct {
	label    string
	subtitle string
	kind     string
	channel  api.Channel
	thread   *threadListEntry
	unread   bool
}

func New(c *api.Client, info *api.AuthInfo, appToken string) *App {
	return &App{
		client:             c,
		info:               info,
		appToken:           strings.TrimSpace(appToken),
		users:              map[string]string{},
		userByHandle:       map[string]string{},
		userByID:           map[string]string{},
		userInfoByID:       map[string]api.UserInfo{},
		groupNameCache:     map[string]string{},
		paneReloadTimers:   map[int]*time.Timer{},
		channelByID:        map[string]api.Channel{},
		favorites:          map[string]bool{},
		showChatList:       true,
		showPaneSeparators: true,
		windowWidth:        896,
		windowHeight:       820,
		sectionCollapsed:   map[string]bool{},
		sidebarHoverListID: -1,
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
		func(p *chatPane) {
			a.syncSidebarSelectionToFocusedPane(p)
			a.focusPaneInput(p)
		},
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
	a.statusLabel = widget.NewLabel("Ready")
	a.statusLabel.Importance = widget.LowImportance
	a.statusActionButton = widget.NewButton("", func() {
		a.invokeStatusAction()
	})
	a.statusActionButton.Importance = widget.LowImportance
	a.statusActionButton.Hide()
	statusBarInner := container.NewBorder(nil, nil, a.statusLabel, a.statusActionButton, nil)
	statusBar := container.NewPadded(statusBarInner)
	settingsButton := widget.NewButtonWithIcon("", theme.MenuDropDownIcon(), func() {
		a.openSettingsMenu()
	})
	settingsButton.Importance = widget.LowImportance
	settingsWrap := container.NewGridWrap(fyne.NewSize(18, 18), settingsButton)
	topBar := container.NewPadded(container.NewHBox(layout.NewSpacer(), settingsWrap))
	mainArea := container.NewBorder(nil, statusBar, a.chatListPane, nil, a.paneManager.widget())
	a.rootContent = container.NewBorder(topBar, nil, nil, nil, mainArea)
	a.win.SetContent(a.rootContent)
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
		if a.sidebarSearchTimer != nil {
			a.sidebarSearchTimer.Stop()
			a.sidebarSearchTimer = nil
		}
		a.saveWindowSizePreference()
		a.savePaneLayoutState()
	})

	if err := a.loadInitialData(); err != nil {
		dialog.ShowError(err, a.win)
	}
	a.setStatusTemporary("Ready", 2*time.Second)
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
	a.channelSearch.OnChanged = func(_ string) { a.scheduleSidebarFilterRebuild() }

	a.channelList = widget.NewList(
		func() int { return len(a.listItems) },
		func() fyne.CanvasObject {
			bg := canvas.NewRectangle(color.Transparent)
			accent := canvas.NewRectangle(color.Transparent)
			accent.SetMinSize(fyne.NewSize(3, 1))
			lock := canvas.NewImageFromResource(privateChannelLockIcon)
			lock.FillMode = canvas.ImageFillContain
			lock.SetMinSize(fyne.NewSize(7, 7))
			lock.Hide()
			lockWrap := container.NewGridWrap(fyne.NewSize(7, 7), lock)
			lbl := widget.NewLabel("channel")
			lbl.Wrapping = fyne.TextTruncate
			fav := newGlyph("☆", nil)
			fav.text.TextSize = 9
			fav.Hide()
			favWrap := container.NewGridWrap(fyne.NewSize(12, 12), fav)
			labelWrap := container.NewBorder(nil, nil, sidebarHSpacer(1), sidebarHSpacer(4), lbl)
			content := container.NewBorder(nil, nil, container.NewHBox(accent, lockWrap), favWrap, labelWrap)
			return newSidebarHoverRow(container.NewMax(bg, content), nil)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			bg, accent, lbl, fav, lock, hover := sidebarRowParts(obj)
			if bg == nil || accent == nil || lbl == nil || fav == nil || lock == nil || hover == nil {
				return
			}
			if id < 0 || id >= len(a.listItems) {
				return
			}
			item := a.listItems[id]
			hover.onHover = nil
			fav.Hide()
			lock.Hide()
			bg.FillColor = color.Transparent
			accent.FillColor = color.Transparent
			if item.channel == nil && item.thread == nil {
				if strings.TrimSpace(item.sectionKey) == "" {
					lbl.TextStyle = fyne.TextStyle{Bold: true}
					lbl.SetText(strings.ToUpper(strings.TrimSpace(item.header)))
				} else {
					lbl.TextStyle = fyne.TextStyle{Bold: true}
					lbl.SetText(a.sectionHeaderLabel(item.sectionKey, item.header, a.sectionItemCount(item.sectionKey)))
				}
				return
			}
			lbl.TextStyle = fyne.TextStyle{}
			if item.channel != nil {
				channelID := strings.TrimSpace(item.channel.ID)
				isFav := a.isFavorite(channelID)
				isPrivate := item.channel.IsPrivate && !item.channel.IsIM && !item.channel.IsMPIM
				selected := strings.TrimSpace(a.selectedChannelID) == strings.TrimSpace(item.channel.ID) && strings.TrimSpace(a.selectedThreadTS) == ""
				lbl.TextStyle = fyne.TextStyle{Bold: selected || a.channelHasUnread(*item.channel)}
				prefix := ""
				if selected {
					bg.FillColor = theme.Color(theme.ColorNameHover)
				}
				if isFav {
					fav.text.Text = "★"
					fav.text.Color = theme.Color(theme.ColorNamePrimary)
				} else {
					fav.text.Text = "☆"
					fav.text.Color = theme.Color(theme.ColorNameDisabled)
				}
				fav.onTap = func() {
					a.suppressSidebarSelect = true
					a.toggleFavoriteChannel(channelID)
				}
				if isFav || a.sidebarHoverListID == id {
					fav.Show()
				} else {
					fav.Hide()
				}
				if isPrivate && a.sidebarHoverListID == id {
					lock.Show()
				} else {
					lock.Hide()
				}
				hover.onHover = func(in bool) {
					if in {
						if a.sidebarHoverListID != id {
							a.sidebarHoverListID = id
							a.channelList.Refresh()
						}
						return
					}
					if a.sidebarHoverListID == id {
						a.sidebarHoverListID = -1
						a.channelList.Refresh()
					}
				}
				fav.Refresh()
				lbl.SetText(prefix + a.chatListLabel(*item.channel))
				return
			}
			selected := strings.TrimSpace(a.selectedThreadTS) == strings.TrimSpace(item.thread.ThreadTS) && strings.TrimSpace(a.selectedChannelID) == strings.TrimSpace(item.thread.ChannelID)
			lbl.TextStyle = fyne.TextStyle{Italic: !selected, Bold: selected}
			prefix := "↳ "
			if selected {
				bg.FillColor = theme.Color(theme.ColorNameHover)
			}
			lbl.SetText(prefix + strings.TrimSpace(item.thread.Title))
		},
	)
	a.channelList.HideSeparators = true
	a.channelList.OnSelected = func(id widget.ListItemID) {
		if a.suppressSidebarSelect {
			a.suppressSidebarSelect = false
			a.channelList.Unselect(id)
			return
		}
		if a.isProgrammaticListSelect {
			return
		}
		if id < 0 || id >= len(a.listItems) {
			return
		}
		item := a.listItems[id]
		if item.channel == nil && item.thread == nil {
			if strings.TrimSpace(item.sectionKey) == "" {
				a.channelList.Unselect(id)
				return
			}
			a.toggleSection(item.sectionKey)
			a.channelList.Unselect(id)
			return
		}
		if item.channel == nil && item.thread != nil {
			a.setSelectedSidebarThread(item.thread.ChannelID, item.thread.ThreadTS)
			a.openThreadFromList(*item.thread)
			a.channelList.Unselect(id)
			return
		}
		a.setSelectedSidebarChannel(item.channel.ID)
		a.assignChannelToFocusedPane(item.channel.ID)
	}

	left := container.NewBorder(
		a.channelSearch,
		nil, nil, nil,
		a.channelList,
	)
	a.chatListPane = newFixedWidthWrap(left, 106)
	a.chatListPane.show = a.showChatList
}

func sidebarRowParts(obj fyne.CanvasObject) (bg *canvas.Rectangle, accent *canvas.Rectangle, lbl *widget.Label, fav *glyph, lock *canvas.Image, hover *sidebarHoverRow) {
	rects := make([]*canvas.Rectangle, 0, 2)
	var walk func(fyne.CanvasObject)
	walk = func(cur fyne.CanvasObject) {
		if cur == nil {
			return
		}
		switch v := cur.(type) {
		case *canvas.Rectangle:
			rects = append(rects, v)
		case *canvas.Image:
			if lock == nil {
				lock = v
			}
		case *widget.Label:
			if lbl == nil {
				lbl = v
			}
		case *glyph:
			if fav == nil {
				fav = v
			}
		case *sidebarHoverRow:
			if hover == nil {
				hover = v
			}
			walk(v.content)
		case *fyne.Container:
			for _, child := range v.Objects {
				walk(child)
			}
		}
	}
	walk(obj)
	if len(rects) > 0 {
		bg = rects[0]
	}
	if len(rects) > 1 {
		accent = rects[1]
	}
	return bg, accent, lbl, fav, lock, hover
}

func sidebarHSpacer(width float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(width, 1))
	return r
}

func (a *App) loadInitialData() error {
	a.setStatus("Loading channels...")
	emojiNotice := ""
	if dir, err := a.client.UserDirectory(); err != nil {
		log.Printf("[SLACK-GUI] users directory failed: %v", err)
	} else {
		a.buildUserDirectory(dir)
	}
	if emojiMap, err := a.client.EmojiList(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "missing_scope") {
			log.Printf("[SLACK-GUI] emoji.list unavailable: missing scope. Add emoji:read in Slack app OAuth & Permissions and reinstall app.")
			emojiNotice = "emoji.list saknas (emoji:read)"
		} else {
			log.Printf("[SLACK-GUI] emoji.list failed: %v", err)
			emojiNotice = "emoji.list fel"
		}
	} else {
		setWorkspaceEmojiMap(emojiMap)
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
		a.isProgrammaticListSelect = true
		a.channelList.Select(idx)
		a.isProgrammaticListSelect = false
		item := a.listItems[idx]
		if item.channel != nil {
			a.setSelectedSidebarChannel(item.channel.ID)
		}
	}
	if strings.TrimSpace(emojiNotice) != "" {
		a.setStatusTemporary("Channels loaded · "+emojiNotice, 6*time.Second)
	} else {
		a.setStatusTemporary("Channels loaded", 2*time.Second)
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
	favorites := make([]api.Channel, 0)
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
		if a.isFavorite(ch.ID) {
			favorites = append(favorites, ch)
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
	sortByActivity := func(items []api.Channel) {
		sort.SliceStable(items, func(i, j int) bool {
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
	sortByActivity(favorites)
	sortByLatest(newMsgs)
	sortByActivity(dms)
	sortByActivity(groups)
	sortByActivity(rooms)
	sortByActivity(bots)
	sort.SliceStable(threads, func(i, j int) bool {
		return threads[i].LastActivity.After(threads[j].LastActivity)
	})

	a.listItems = a.listItems[:0]
	appendSectionHeader := func(title string) {
		a.listItems = append(a.listItems, chatListItem{header: title, sectionKey: ""})
	}
	appendChannels := func(items []api.Channel) {
		for i := range items {
			ch := items[i]
			a.listItems = append(a.listItems, chatListItem{channel: &ch})
		}
	}
	if len(favorites) > 0 {
		appendSectionHeader("Favorites")
		appendChannels(favorites)
	}
	if len(newMsgs) > 0 {
		appendSectionHeader("New")
		appendChannels(newMsgs)
	}
	if len(rooms) > 0 {
		appendSectionHeader("Channels")
		appendChannels(rooms)
	}
	if len(threads) > 0 {
		appendSectionHeader("Threads")
		for i := range threads {
			t := threads[i]
			a.listItems = append(a.listItems, chatListItem{thread: &t})
		}
	}
	if len(dms) > 0 {
		appendSectionHeader("Direct Messages")
		appendChannels(dms)
	}
	if len(groups) > 0 {
		appendSectionHeader("Groups")
		appendChannels(groups)
	}
	if len(bots) > 0 {
		appendSectionHeader("Bots & Apps")
		appendChannels(bots)
	}
	if len(a.listItems) == 0 {
		a.listItems = append(a.listItems, chatListItem{header: "No chats match your search", sectionKey: ""})
	}
	a.updateChatListWidth()
	if a.channelList != nil {
		a.channelList.Refresh()
		a.syncSidebarSelection()
	}
}

func (a *App) scheduleSidebarFilterRebuild() {
	if a.sidebarSearchTimer != nil {
		a.sidebarSearchTimer.Stop()
	}
	a.sidebarSearchTimer = time.AfterFunc(sidebarSearchDebounce, func() {
		fyne.Do(func() {
			a.rebuildFilteredChannels()
		})
	})
}

func (a *App) updateChatListWidth() {
	if a.chatListPane == nil {
		return
	}
	const (
		minWidth  = float32(92)
		maxWidth  = float32(134)
		charWidth = float32(6.0)
		padding   = float32(28)
	)
	target := minWidth
	for _, item := range a.listItems {
		label := ""
		switch {
		case item.channel != nil:
			label = a.chatListWidthLabel(*item.channel)
		case item.thread != nil:
			label = "↳ " + strings.TrimSpace(item.thread.Title)
		default:
			label = strings.TrimSpace(item.header)
		}
		if label == "" {
			continue
		}
		runes := []rune(label)
		if len(runes) > 30 {
			runes = runes[:30]
		}
		w := float32(len(runes))*charWidth + padding
		if w > target {
			target = w
		}
	}
	if target > maxWidth {
		target = maxWidth
	}
	if target < minWidth {
		target = minWidth
	}
	if a.chatListPane.width == target {
		return
	}
	a.chatListPane.width = target
	a.chatListPane.Refresh()
	if a.rootContent != nil {
		a.rootContent.Refresh()
	}
}

func (a *App) chatListWidthLabel(ch api.Channel) string {
	name := a.chatBaseName(ch)
	if ch.IsIM {
		return "@ " + name
	}
	if ch.IsMPIM {
		return "• " + name
	}
	if strings.TrimSpace(name) == "" {
		name = "channel"
	}
	return name
}

func (a *App) chatListLabel(ch api.Channel) string {
	marker := ""
	if a.channelHasUnread(ch) {
		marker = "● "
	}
	unread := ""
	if ch.UnreadCount > 0 {
		unread = fmt.Sprintf("  [%d]", ch.UnreadCount)
	} else if ch.HasUnread {
		unread = "  [new]"
	}
	if ch.IsIM {
		return marker + "@ " + a.chatBaseName(ch) + unread
	}
	if ch.IsMPIM {
		return marker + "• " + a.chatBaseName(ch) + unread
	}
	name := a.chatBaseName(ch)
	if name == "" {
		name = "channel"
	}
	return marker + name + unread
}

func (a *App) isFavorite(channelID string) bool {
	id := strings.TrimSpace(channelID)
	if id == "" {
		return false
	}
	if a.favorites == nil {
		a.favorites = map[string]bool{}
	}
	return a.favorites[id]
}

func (a *App) toggleFavoriteFocusedChat() {
	if a.paneManager == nil {
		return
	}
	p := a.paneManager.focusedPane()
	if p == nil || strings.TrimSpace(p.channelID) == "" {
		a.setStatusTemporary("No focused chat to favorite", 2*time.Second)
		return
	}
	a.toggleFavoriteChannel(p.channelID)
}

func (a *App) toggleFavoriteChannel(channelID string) {
	id := strings.TrimSpace(channelID)
	if id == "" {
		return
	}
	if a.favorites == nil {
		a.favorites = map[string]bool{}
	}
	if a.favorites[id] {
		delete(a.favorites, id)
		a.setStatusTemporary("Removed from favorites", 2*time.Second)
	} else {
		a.favorites[id] = true
		a.setStatusTemporary("Added to favorites", 2*time.Second)
	}
	a.saveFavoritesPreference()
	a.rebuildFilteredChannels()
}

func (a *App) saveFavoritesPreference() {
	if a.fyneApp == nil {
		return
	}
	ids := make([]string, 0, len(a.favorites))
	for id, enabled := range a.favorites {
		if !enabled {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	a.fyneApp.Preferences().SetString(prefFavorites, strings.Join(ids, ","))
}

func (a *App) channelHasUnread(ch api.Channel) bool {
	return ch.UnreadCount > 0 || ch.HasUnread
}

func (a *App) sectionItemCount(section string) int {
	for i, item := range a.listItems {
		if item.channel != nil || item.thread != nil {
			continue
		}
		if item.sectionKey != section {
			continue
		}
		count := 0
		for j := i + 1; j < len(a.listItems); j++ {
			next := a.listItems[j]
			if next.channel == nil && next.thread == nil {
				break
			}
			count++
		}
		return count
	}
	return 0
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
	a.setSelectedSidebarChannel(channelID)
	p.clearMessages()
	a.clearChannelUnreadState(channelID)
	a.focusPaneInput(p)
	a.schedulePaneScrollToBottom(p)
	a.savePaneLayoutState()
	a.setStatusTemporary("Switched to "+a.chatPrefix(ch)+p.channelName, 3*time.Second)
	go a.loadMessagesForPane(p)
}

func (a *App) clearChannelUnreadState(channelID string) {
	changed := false
	for i := range a.channels {
		if strings.TrimSpace(a.channels[i].ID) != strings.TrimSpace(channelID) {
			continue
		}
		if !a.channels[i].HasUnread && a.channels[i].UnreadCount == 0 {
			break
		}
		a.channels[i].HasUnread = false
		a.channels[i].UnreadCount = 0
		a.channelByID[channelID] = a.channels[i]
		changed = true
		break
	}
	if changed {
		a.rebuildFilteredChannels()
	}
}

func (a *App) markChannelUnreadState(channelID, latestTS string) {
	changed := false
	for i := range a.channels {
		if strings.TrimSpace(a.channels[i].ID) != strings.TrimSpace(channelID) {
			continue
		}
		if !a.channels[i].HasUnread {
			changed = true
		}
		a.channels[i].HasUnread = true
		a.channels[i].UnreadCount++
		changed = true
		if strings.TrimSpace(latestTS) != "" {
			if strings.TrimSpace(a.channels[i].LatestTS) != strings.TrimSpace(latestTS) {
				a.channels[i].LatestTS = latestTS
				changed = true
			}
		}
		a.channelByID[channelID] = a.channels[i]
		break
	}
	if changed {
		a.rebuildFilteredChannels()
	}
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
			if delay, rateLimited := retryDelayForError(err); rateLimited {
				secs := int(delay / time.Second)
				a.setStatusWithAction(
					"Rate limited while loading",
					fmt.Sprintf("Retry in %ds", secs),
					func() {
						go func() {
							time.Sleep(delay)
							a.loadMessagesForPane(p)
						}()
					},
				)
			} else {
				a.setStatusWithAction("Failed to load messages", "Retry", func() {
					go a.loadMessagesForPane(p)
				})
			}
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
		p.setMessages(msgs, a.info.UserID, a.info.UserID, a.showTimestamps, func(m api.Message) {
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
	for _, d := range []time.Duration{0, 120 * time.Millisecond} {
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
	a.setSelectedSidebarThread(p.channelID, rootTS)
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
	a.setSelectedSidebarChannel(p.channelID)
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
	var retrySend func()
	retrySend = func() {
		if err := a.client.PostMessage(p.channelID, text, threadTS); err != nil {
			dialog.ShowError(err, a.win)
			a.setStatusWithAction("Send failed", "Retry send", retrySend)
			return
		}
		a.setStatusTemporary("Message sent", 2*time.Second)
		p.input.SetText("")
		a.clearReplyTarget(p)
		go func() {
			time.Sleep(postSendRefreshDelay)
			a.loadMessagesForPane(p)
		}()
	}
	if err := a.client.PostMessage(p.channelID, text, threadTS); err != nil {
		if delay, rateLimited := retryDelayForError(err); rateLimited {
			secs := int(delay / time.Second)
			a.setStatusWithAction(
				"Rate limited while sending",
				fmt.Sprintf("Retry in %ds", secs),
				func() {
					go func() {
						time.Sleep(delay)
						retrySend()
					}()
				},
			)
		} else {
			a.setStatusWithAction("Send failed", "Retry send", retrySend)
		}
		dialog.ShowError(err, a.win)
		return
	}
	a.setStatusTemporary("Message sent", 2*time.Second)
	p.input.SetText("")
	a.clearReplyTarget(p)
	go func() {
		time.Sleep(postSendRefreshDelay)
		a.loadMessagesForPane(p)
	}()
}

func (a *App) setSelectedSidebarChannel(channelID string) {
	channelID = strings.TrimSpace(channelID)
	if a.selectedChannelID == channelID && a.selectedThreadTS == "" {
		return
	}
	a.selectedChannelID = channelID
	a.selectedThreadTS = ""
	if a.channelList != nil {
		a.channelList.Refresh()
	}
}

func (a *App) setSelectedSidebarThread(channelID, threadTS string) {
	channelID = strings.TrimSpace(channelID)
	threadTS = strings.TrimSpace(threadTS)
	if a.selectedChannelID == channelID && a.selectedThreadTS == threadTS {
		return
	}
	a.selectedChannelID = channelID
	a.selectedThreadTS = threadTS
	if a.channelList != nil {
		a.channelList.Refresh()
	}
}

func (a *App) syncSidebarSelection() {
	if a.channelList == nil {
		return
	}
	if strings.TrimSpace(a.selectedThreadTS) != "" {
		for i, item := range a.listItems {
			if item.thread == nil {
				continue
			}
			if strings.TrimSpace(item.thread.ChannelID) == strings.TrimSpace(a.selectedChannelID) && strings.TrimSpace(item.thread.ThreadTS) == strings.TrimSpace(a.selectedThreadTS) {
				a.isProgrammaticListSelect = true
				a.channelList.Select(i)
				a.isProgrammaticListSelect = false
				return
			}
		}
	}
	if strings.TrimSpace(a.selectedChannelID) == "" {
		return
	}
	for i, item := range a.listItems {
		if item.channel == nil {
			continue
		}
		if strings.TrimSpace(item.channel.ID) == strings.TrimSpace(a.selectedChannelID) {
			a.isProgrammaticListSelect = true
			a.channelList.Select(i)
			a.isProgrammaticListSelect = false
			return
		}
	}
}

func (a *App) setStatus(text string) {
	if a.statusLabel == nil {
		return
	}
	if a.statusClearTimer != nil {
		a.statusClearTimer.Stop()
		a.statusClearTimer = nil
	}
	a.statusLabel.SetText(strings.TrimSpace(text))
	a.clearStatusAction()
}

func (a *App) clearStatusAction() {
	a.statusActionLabel = ""
	a.statusActionFn = nil
	if a.statusActionButton != nil {
		a.statusActionButton.Hide()
	}
}

func (a *App) setStatusAction(label string, fn func()) {
	if a.statusActionButton == nil {
		return
	}
	label = strings.TrimSpace(label)
	if label == "" || fn == nil {
		a.clearStatusAction()
		return
	}
	a.statusActionLabel = label
	a.statusActionFn = fn
	a.statusActionButton.SetText(label)
	a.statusActionButton.Show()
	a.statusActionButton.Refresh()
}

func (a *App) invokeStatusAction() {
	if a.statusActionFn == nil {
		return
	}
	a.statusActionFn()
}

func (a *App) setStatusTemporary(text string, ttl time.Duration) {
	if a.statusLabel == nil {
		return
	}
	a.setStatus(text)
	if ttl <= 0 {
		return
	}
	marker := strings.TrimSpace(text)
	a.statusClearTimer = time.AfterFunc(ttl, func() {
		fyne.Do(func() {
			if a.statusLabel == nil {
				return
			}
			if strings.TrimSpace(a.statusLabel.Text) != marker {
				return
			}
			a.statusLabel.SetText("Ready")
			a.clearStatusAction()
		})
	})
}

func (a *App) setStatusWithAction(text, actionLabel string, actionFn func()) {
	a.setStatus(text)
	a.setStatusAction(actionLabel, actionFn)
}

func (a *App) setStatusTemporaryWithAction(text string, ttl time.Duration, actionLabel string, actionFn func()) {
	a.setStatusWithAction(text, actionLabel, actionFn)
	if ttl <= 0 {
		return
	}
	marker := strings.TrimSpace(text)
	a.statusClearTimer = time.AfterFunc(ttl, func() {
		fyne.Do(func() {
			if a.statusLabel == nil {
				return
			}
			if strings.TrimSpace(a.statusLabel.Text) != marker {
				return
			}
			a.statusLabel.SetText("Ready")
			a.clearStatusAction()
		})
	})
}

func (a *App) setStatusFromRealtime(text string) {
	fyne.Do(func() {
		a.setStatus(text)
	})
}

func (a *App) setStatusWithActionFromRealtime(text, actionLabel string, actionFn func()) {
	fyne.Do(func() {
		a.setStatusWithAction(text, actionLabel, actionFn)
	})
}

func (a *App) setStatusTemporaryFromRealtime(text string, ttl time.Duration) {
	fyne.Do(func() {
		a.setStatusTemporary(text, ttl)
	})
}

func (a *App) reconnectBackoffStatus(backoff time.Duration) string {
	return "Realtime disconnected, retry in " + strconv.Itoa(int(backoff/time.Second)) + "s"
}

func retryDelayForError(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(msg, "status 429") || strings.Contains(msg, "ratelimited") || strings.Contains(msg, "rate_limited") {
		return 4 * time.Second, true
	}
	return 0, false
}

func (a *App) restartRealtimeUpdates() {
	a.stopRealtimeUpdates()
	a.startRealtimeUpdates()
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

func (a *App) sectionHeaderLabel(section, title string, count int) string {
	arrow := "▼"
	if a.isSectionCollapsed(section) {
		arrow = "▶"
	}
	if count <= 0 {
		return fmt.Sprintf("%s %s", arrow, title)
	}
	return fmt.Sprintf("%s %s (%d)", arrow, title, count)
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

func (a *App) quickSwitchItems(query string) []quickSwitchItem {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]quickSwitchItem, 0, len(a.channels)+len(a.recentThreads))
	for _, ch := range a.channels {
		if !ch.IsIM && !ch.IsMPIM && !ch.IsMember {
			continue
		}
		name := strings.TrimSpace(a.chatBaseName(ch))
		if name == "" {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(name), q) {
			continue
		}
		prefix := "#"
		kind := "channel"
		if ch.IsIM {
			prefix = "@"
			kind = "dm"
		} else if ch.IsMPIM {
			prefix = "•"
			kind = "group"
		}
		out = append(out, quickSwitchItem{
			label:    fmt.Sprintf("%s %s", prefix, name),
			subtitle: strings.ToUpper(kind),
			kind:     kind,
			channel:  ch,
			unread:   a.channelHasUnread(ch),
		})
	}
	for i := range a.recentThreads {
		t := a.recentThreads[i]
		label := fmt.Sprintf("↳ %s (%s)", strings.TrimSpace(t.Title), strings.TrimSpace(t.ChannelLabel))
		if q != "" && !strings.Contains(strings.ToLower(label), q) {
			continue
		}
		tCopy := t
		out = append(out, quickSwitchItem{
			label:    label,
			subtitle: "THREAD",
			kind:     "thread",
			thread:   &tCopy,
			unread:   false,
		})
	}
	rank := func(item quickSwitchItem) int {
		score := 0
		lower := strings.ToLower(item.label)
		if q != "" {
			if strings.HasPrefix(lower, q) {
				score += 400
			} else if strings.Contains(lower, q) {
				score += 120
			}
		}
		if item.unread {
			score += 220
		}
		if item.kind == "thread" {
			score -= 30
		}
		if strings.TrimSpace(a.selectedChannelID) != "" && strings.TrimSpace(item.channel.ID) == strings.TrimSpace(a.selectedChannelID) {
			score += 20
		}
		return score
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := rank(out[i])
		right := rank(out[j])
		if left != right {
			return left > right
		}
		return strings.ToLower(out[i].label) < strings.ToLower(out[j].label)
	})
	if len(out) > 60 {
		return out[:60]
	}
	return out
}

func (a *App) openQuickSwitcher() {
	if a.win == nil {
		return
	}
	items := a.quickSwitchItems("")
	if len(items) == 0 {
		dialog.ShowInformation("Quick Switcher", "No channels or threads available yet.", a.win)
		return
	}

	var query *quickSwitchEntry
	status := widget.NewLabel("")
	status.Importance = widget.LowImportance

	selected := 0
	var list *widget.List
	var d dialog.Dialog
	openSelected := func() {
		if len(items) == 0 {
			return
		}
		if selected < 0 || selected >= len(items) {
			selected = 0
		}
		item := items[selected]
		if item.thread != nil {
			a.setSelectedSidebarThread(item.thread.ChannelID, item.thread.ThreadTS)
			a.openThreadFromList(*item.thread)
		} else if strings.TrimSpace(item.channel.ID) != "" {
			a.setSelectedSidebarChannel(item.channel.ID)
			a.assignChannelToFocusedPane(item.channel.ID)
		}
		if d != nil {
			d.Hide()
		}
	}
	moveSelected := func(delta int) {
		if len(items) == 0 {
			selected = -1
			list.Refresh()
			return
		}
		if selected < 0 {
			selected = 0
		} else {
			selected += delta
		}
		if selected < 0 {
			selected = len(items) - 1
		}
		if selected >= len(items) {
			selected = 0
		}
		list.Select(selected)
		list.Refresh()
	}
	query = newQuickSwitchEntry(moveSelected, openSelected, func() {
		if d != nil {
			d.Hide()
		}
	})
	query.SetPlaceHolder("Jump to channel, DM, or thread...")

	list = widget.NewList(
		func() int { return len(items) },
		func() fyne.CanvasObject {
			dot := canvas.NewText("●", theme.Color(theme.ColorNamePrimary))
			dot.TextSize = theme.CaptionTextSize()
			title := widget.NewLabel("item")
			title.Wrapping = fyne.TextTruncate
			meta := widget.NewLabel("meta")
			meta.Wrapping = fyne.TextTruncate
			meta.Importance = widget.LowImportance
			return container.NewBorder(nil, nil, dot, nil, container.NewVBox(title, meta))
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(items) {
				return
			}
			dot, title, meta := quickSwitchRowParts(obj)
			if dot == nil || title == nil || meta == nil {
				return
			}
			item := items[id]
			if item.unread {
				dot.Color = theme.Color(theme.ColorNamePrimary)
				meta.SetText(item.subtitle + " • UNREAD")
			} else {
				dot.Color = theme.Color(theme.ColorNameDisabled)
				meta.SetText(item.subtitle)
			}
			dot.Refresh()
			if id == selected {
				title.TextStyle = fyne.TextStyle{Bold: true}
				title.SetText("› " + item.label)
			} else {
				title.TextStyle = fyne.TextStyle{}
				title.SetText("  " + item.label)
			}
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		if id >= 0 && id < len(items) {
			selected = id
			list.Refresh()
		}
	}
	list.OnUnselected = func(_ widget.ListItemID) {
		selected = -1
		list.Refresh()
	}

	refreshState := func() {
		count := len(items)
		if count == 0 {
			status.SetText("No matches  •  Up/Down or Ctrl+N/Ctrl+P to navigate")
			selected = -1
		} else {
			status.SetText(fmt.Sprintf("%d result(s)  •  Up/Down or Ctrl+N/Ctrl+P  •  Enter to open  •  Esc to close", count))
			if selected < 0 || selected >= count {
				selected = 0
			}
			list.Select(selected)
		}
		list.Refresh()
	}

	query.OnChanged = func(s string) {
		items = a.quickSwitchItems(s)
		refreshState()
	}
	query.OnSubmitted = func(_ string) {
		openSelected()
	}

	openBtn := widget.NewButton("Open", func() { openSelected() })
	footer := container.NewBorder(nil, nil, status, openBtn, nil)
	content := container.NewBorder(query, footer, nil, nil, list)
	d = dialog.NewCustom("Quick Switcher", "Close", content, a.win)
	d.Resize(fyne.NewSize(560, 460))
	d.Show()
	refreshState()
	a.win.Canvas().Focus(query)
}

func quickSwitchRowParts(obj fyne.CanvasObject) (dot *canvas.Text, title *widget.Label, meta *widget.Label) {
	labels := make([]*widget.Label, 0, 2)
	var walk func(fyne.CanvasObject)
	walk = func(cur fyne.CanvasObject) {
		if cur == nil {
			return
		}
		switch v := cur.(type) {
		case *canvas.Text:
			if dot == nil {
				dot = v
			}
		case *widget.Label:
			labels = append(labels, v)
		case *fyne.Container:
			for _, child := range v.Objects {
				walk(child)
			}
		}
	}
	walk(obj)
	if len(labels) > 0 {
		title = labels[0]
	}
	if len(labels) > 1 {
		meta = labels[1]
	}
	return dot, title, meta
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
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyName("K"), Modifier: fyne.KeyModifierControl}, func(_ fyne.Shortcut) {
		a.openQuickSwitcher()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyName("T"), Modifier: fyne.KeyModifierControl}, func(_ fyne.Shortcut) {
		a.setShowTimestamps(!a.showTimestamps)
	})
}

func (a *App) buildSettingsMenu() *fyne.Menu {
	colorModeLabel := "Switch to Light Mode"
	if !a.appTheme.dark {
		colorModeLabel = "Switch to Dark Mode"
	}
	compactLabel := "Enable Compact Mode"
	if a.appTheme.compactMode {
		compactLabel = "Disable Compact Mode"
	}
	timestampsLabel := "Show Timestamps"
	if a.showTimestamps {
		timestampsLabel = "Hide Timestamps"
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
		}))
	}
	fontItem := fyne.NewMenuItem("Font", nil)
	fontItem.ChildMenu = fyne.NewMenu("", fontItems...)

	windowMenu := fyne.NewMenu("Window",
		fyne.NewMenuItem("New Window", func() { a.openNewWindow() }),
		fyne.NewMenuItem("Move Focused Pane to New Window", func() { a.moveFocusedPaneToNewWindow() }),
		fyne.NewMenuItem("Quick Switcher", func() { a.openQuickSwitcher() }),
		fyne.NewMenuItem(func() string {
			if a.paneManager != nil {
				if p := a.paneManager.focusedPane(); p != nil && a.isFavorite(p.channelID) {
					return "Unfavorite Focused Chat"
				}
			}
			return "Favorite Focused Chat"
		}(), func() { a.toggleFavoriteFocusedChat() }),
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
		fyne.NewMenuItem(timestampsLabel, func() { a.setShowTimestamps(!a.showTimestamps) }),
		fyne.NewMenuItem(separatorLabel, func() { a.togglePaneSeparators() }),
		fyne.NewMenuItem("Toggle Channel List", func() { a.toggleChatList() }),
		fyne.NewMenuItem(compactLabel, func() { a.setCompactMode(!a.appTheme.compactMode) }),
		fyne.NewMenuItem(colorModeLabel, func() { a.setDarkMode(!a.appTheme.dark) }),
	)
	items := make([]*fyne.MenuItem, 0, len(windowMenu.Items)+len(viewMenu.Items)+3)
	items = append(items, windowMenu.Items...)
	items = append(items, fyne.NewMenuItemSeparator())
	items = append(items, viewMenu.Items...)
	return fyne.NewMenu("", items...)
}

func (a *App) openSettingsMenu() {
	if a.win == nil {
		return
	}
	menu := a.buildSettingsMenu()
	pos := fyne.NewPos(a.win.Canvas().Size().Width-6, 18)
	widget.ShowPopUpMenuAtPosition(menu, a.win.Canvas(), pos)
}

func (a *App) syncSidebarSelectionToFocusedPane(p *chatPane) {
	if p == nil {
		return
	}
	if strings.TrimSpace(p.threadTS) != "" && strings.TrimSpace(p.channelID) != "" {
		a.setSelectedSidebarThread(p.channelID, p.threadTS)
		return
	}
	if strings.TrimSpace(p.channelID) != "" {
		a.setSelectedSidebarChannel(p.channelID)
	}
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
}

func (a *App) setCompactMode(enabled bool) {
	a.appTheme.compactMode = enabled
	a.fyneApp.Preferences().SetBool(prefCompactMode, enabled)
	a.fyneApp.Settings().SetTheme(a.appTheme)
	a.refreshPanesForTheme()
}

func (a *App) setDarkMode(enabled bool) {
	a.appTheme.dark = enabled
	a.fyneApp.Preferences().SetBool(prefDarkMode, enabled)
	a.fyneApp.Settings().SetTheme(a.appTheme)
	a.refreshPanesForTheme()
}

func (a *App) setShowTimestamps(enabled bool) {
	a.showTimestamps = enabled
	if a.fyneApp != nil {
		a.fyneApp.Preferences().SetBool(prefShowTimestamps, enabled)
	}
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
		a.setSelectedSidebarThread(p.channelID, p.threadTS)
		p.setThreadBanner("Thread view")
	} else {
		a.setSelectedSidebarChannel(p.channelID)
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
	a.showTimestamps = prefs.BoolWithFallback(prefShowTimestamps, false)
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
	a.favorites = map[string]bool{}
	for _, id := range strings.Split(prefs.StringWithFallback(prefFavorites, ""), ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		a.favorites[id] = true
	}
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
	case fyne.KeyName("K"):
		a.openQuickSwitcher()
		return true
	case fyne.KeyName("T"):
		a.setShowTimestamps(!a.showTimestamps)
		return true
	}
	return false
}
