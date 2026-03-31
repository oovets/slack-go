package gui

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"github.com/gorilla/websocket"
)

func (a *App) startRealtimeUpdates() {
	if a.client == nil {
		return
	}
	a.realtimeMu.Lock()
	if a.realtimeRunning {
		a.realtimeMu.Unlock()
		return
	}
	a.realtimeStop = make(chan struct{})
	a.realtimeRunning = true
	a.realtimeMu.Unlock()
	a.setStatusFromRealtime("Connecting realtime...")
	go a.runRealtimeLoop(a.realtimeStop)
}

func (a *App) stopRealtimeUpdates() {
	a.realtimeMu.Lock()
	if !a.realtimeRunning {
		a.realtimeMu.Unlock()
		return
	}
	stop := a.realtimeStop
	a.realtimeStop = nil
	a.realtimeRunning = false
	a.realtimeMu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (a *App) runRealtimeLoop(stop <-chan struct{}) {
	backoff := time.Second
	for {
		select {
		case <-stop:
			return
		default:
		}

		var err error
		if strings.TrimSpace(a.appToken) != "" {
			err = a.runSocketModeSession(stop)
		} else {
			err = a.runRTMSession(stop)
		}
		if err != nil {
			log.Printf("[SLACK-GUI] realtime listener disconnected: %v", err)
			a.setStatusWithActionFromRealtime(a.reconnectBackoffStatus(backoff), "Reconnect now", func() {
				go a.restartRealtimeUpdates()
			})
		}

		select {
		case <-stop:
			a.setStatusTemporaryFromRealtime("Realtime stopped", 2*time.Second)
			return
		case <-time.After(backoff):
		}
		a.setStatusFromRealtime("Reconnecting realtime...")
		if backoff < 15*time.Second {
			backoff *= 2
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}
		}
	}
}

func (a *App) runSocketModeSession(stop <-chan struct{}) error {
	wsURL, err := a.client.OpenSocketModeURL(a.appToken)
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	a.setStatusTemporaryFromRealtime("Realtime connected (socket mode)", 3*time.Second)

	go func() {
		<-stop
		_ = conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}

		if envelopeID, _ := env["envelope_id"].(string); strings.TrimSpace(envelopeID) != "" {
			ack := map[string]string{"envelope_id": envelopeID}
			if b, err := json.Marshal(ack); err == nil {
				_ = conn.WriteMessage(websocket.TextMessage, b)
			}
		}

		typeStr, _ := env["type"].(string)
		if typeStr == "events_api" {
			payload, ok := env["payload"].(map[string]any)
			if !ok {
				continue
			}
			evt, ok := payload["event"].(map[string]any)
			if !ok {
				continue
			}
			a.handleRealtimeEvent(evt)
		}
	}
}

func (a *App) runRTMSession(stop <-chan struct{}) error {
	wsURL, err := a.client.RTMConnectURL()
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	a.setStatusTemporaryFromRealtime("Realtime connected", 3*time.Second)

	go func() {
		<-stop
		_ = conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var evt map[string]any
		if err := json.Unmarshal(raw, &evt); err != nil {
			continue
		}
		a.handleRealtimeEvent(evt)
	}
}

// handleRealtimeEvent processes incoming Slack events. All mutations to shared
// state (channels, panes) are dispatched via fyne.Do, which runs them on the
// Fyne main goroutine — so no additional locking is needed for UI fields.
func (a *App) handleRealtimeEvent(evt map[string]any) {
	if evt == nil {
		return
	}
	typ, _ := evt["type"].(string)
	if typ != "message" {
		return
	}
	channelID, _ := evt["channel"].(string)
	if strings.TrimSpace(channelID) == "" {
		return
	}
	threadTS, _ := evt["thread_ts"].(string)
	ts, _ := evt["ts"].(string)
	subtype, _ := evt["subtype"].(string)

	if subtype == "message_changed" {
		if m, ok := evt["message"].(map[string]any); ok {
			if v, ok := m["thread_ts"].(string); ok {
				threadTS = v
			}
			if v, ok := m["ts"].(string); ok {
				ts = v
			}
		}
	}
	if subtype == "message_deleted" {
		if v, ok := evt["deleted_ts"].(string); ok {
			ts = v
		}
	}

	fyne.Do(func() {
		if subtype != "message_deleted" && !a.focusedPaneShowingChannel(channelID) {
			a.markChannelUnreadState(channelID, ts)
		}
		if a.paneManager == nil {
			return
		}
		for _, p := range a.paneManager.allPanes() {
			if strings.TrimSpace(p.channelID) != strings.TrimSpace(channelID) {
				continue
			}
			paneThread := strings.TrimSpace(p.threadTS)
			if paneThread != "" {
				eventRoot := strings.TrimSpace(threadTS)
				if eventRoot == "" {
					eventRoot = strings.TrimSpace(ts)
				}
				if eventRoot != paneThread {
					continue
				}
			}
			a.schedulePaneReload(p)
		}
	})
}

// schedulePaneReload debounces message reloads for a pane. paneReloadMu
// protects the timer map; the actual reload runs on a separate goroutine and
// dispatches UI work via fyne.Do.
func (a *App) schedulePaneReload(p *chatPane) {
	if p == nil {
		return
	}
	a.paneReloadMu.Lock()
	defer a.paneReloadMu.Unlock()
	if t, ok := a.paneReloadTimers[p.id]; ok {
		t.Stop()
	}
	a.paneReloadTimers[p.id] = time.AfterFunc(reloadDebounce, func() {
		a.loadMessagesForPane(p)
		a.paneReloadMu.Lock()
		delete(a.paneReloadTimers, p.id)
		a.paneReloadMu.Unlock()
	})
}
