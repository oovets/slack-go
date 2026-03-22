# Slack GUI

Standalone Slack desktop client built with Go and Fyne.

## Project Layout

- `api` - Slack API client (channels, history, threads, media)
- `gui` - Fyne UI (pane layout, message rendering, realtime updates)

## Requirements

- Go 1.24+
- Slack token with scopes for conversations, history, posting, and users

## Configuration

Credential priority:

1. `SLACK_BOT_TOKEN`
2. `SLACK_TOKEN`
3. `slack_config.json`

Optional environment variables:

```bash
export SLACK_BOT_TOKEN="xoxb-..."
export SLACK_APP_TOKEN="xapp-..." # optional, enables Socket Mode
export SLACK_API_BASE_URL="https://slack.com/api" # optional
export SLACK_CONFIG_PATH="/path/to/slack_config.json" # optional
```

Auto-discovered config locations:

- `~/.config/slack_rust/slack_config.json`
- `~/.config/slack/slack_config.json`
- `~/Code/slack_rust/config/slack_config.json`
- `config/slack_config.json`

## Build and Run

```bash
go mod tidy
go build -o slack-gui ./cmd/slack-gui
./slack-gui
```

Logs are written to `~/.slack-gui.log`.

## Key Features

- Channel list with search and unread indicators
- Multi-pane layout (horizontal/vertical split)
- Threads and inline replies
- Media/file links and image previews
- Realtime updates (Socket Mode with RTM fallback)
- Persistent UI state (window size, layout, view preferences)

## Resource Comparison

Test window: 30s (warmup 5s, interval 1s)

| App | Avg CPU % | Avg RAM (MiB) | Peak RAM (MiB) | Samples |
|---|---:|---:|---:|---:|
| Slack GO | 44.82 | 249.4 | 265.6 | 30 |
| Slack Desktop | 91.47 | 1327.8 | 1498.4 | 30 |

## Keyboard Shortcuts

- `Ctrl+H` split focused pane horizontally
- `Ctrl+J` split focused pane vertically
- `Ctrl+W` close focused pane
- `Ctrl+S` toggle channel list
- `Ctrl+N` open new window
