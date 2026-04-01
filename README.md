# Slack GUI

Standalone Slack desktop client built with Go and Fyne.

## Project Layout

- `api` - Slack API client (channels, history, threads, media)
- `gui` - Fyne UI (pane layout, message rendering, realtime updates)

## Requirements

- Go 1.24+
- Slack token with scopes for conversations, history, posting, and users

Recommended Slack scopes:

- `channels:history`
- `channels:read`
- `groups:history`
- `groups:read`
- `im:history`
- `im:read`
- `mpim:history`
- `mpim:read`
- `chat:write`
- `users:read`
- `users:read.email` (optional)
- `emoji:read` (optional, enables workspace emoji/reaction mapping)

## Configuration

Credential priority:

1. `SLACK_BOT_TOKEN`
2. `SLACK_TOKEN`
3. `.slack_config.json` in project root

Optional environment variables:

```bash
export SLACK_BOT_TOKEN="xoxb-..."
export SLACK_APP_TOKEN="xapp-..." # optional, enables Socket Mode
export SLACK_API_BASE_URL="https://slack.com/api" # optional
export SLACK_CONFIG_PATH="/path/to/.slack_config.json" # optional override
```

Auto-discovered config locations:

- `./.slack_config.json`

## Build and Run

```bash
go mod tidy
go build -o slack-gui ./cmd/slack-gui
./slack-gui
```

Logs are written to `~/.slack-gui.log`.

## Release Checklist

```bash
go fmt ./...
go test ./...
go build -o slack-gui ./cmd/slack-gui
```

Optional packaging build:

```bash
./packaging/linux/build.sh --version 0.1.0
```

## Linux Packaging

Packaging assets and scripts are in `packaging/linux`.

Build release artifacts:

```bash
./packaging/linux/build.sh --version 0.1.0
```

This can produce:
- tar.gz bundle
- AppImage (if `appimagetool` is installed)
- `.deb` and `.rpm` (if `nfpm` is installed)

## Key Features

- Channel list with search and unread indicators
- Multi-pane layout (horizontal/vertical split)
- Threads and inline replies
- Media/file links and image previews
- Realtime updates (Socket Mode with RTM fallback)
- Persistent UI state (window size, layout, view preferences)

## Keyboard Shortcuts

- `Ctrl+H` split focused pane horizontally
- `Ctrl+J` split focused pane vertically
- `Ctrl+W` close focused pane
- `Ctrl+S` toggle channel list
- `Ctrl+N` open new window
- `Ctrl+K` open quick switcher
