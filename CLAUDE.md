# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run locally
go run .

# Run tests
go test ./...

# Run a single package's tests
go test ./internal/payments/...

# Build binary
go build -o bot .

# Docker (preferred for full environment)
docker compose up -d --build
docker compose logs -f
```

## Architecture

**remnawave-notify-bot** is a Go Telegram bot that monitors Remnawave VPN panel subscriptions and notifies users before expiry. It also supports an optional admin-confirmed payment flow for extending subscriptions.

### Entry point

`main.go` loads config, starts the gocron scheduler, and starts the Telegram long-polling loop concurrently.

### Key packages under `internal/`

| Package | Role |
|---|---|
| `notify` | Daily job: fetches all Remnawave users, filters active ones with a Telegram ID, sends expiry warnings at 7/3/1-day thresholds |
| `payments` | Payment confirmation flow — user picks a tariff, reports payment, admin confirms, API extends subscription |
| `remnawave` | HTTP client for Remnawave panel API (`GET /api/users`, `PATCH /api/users`) |
| `telegram` | Thin wrapper around Telegram Bot API (sendMessage, GetUpdates, callback queries) |
| `store` | SQLite DB via modernc.org/sqlite — tables for tariffs, user snapshots, and payment requests |
| `scheduler` | gocron/v2 wrapper; runs the notify job daily at `RUN_AT` in the configured timezone |
| `config` | Loads and validates all config from environment variables |
| `textutil` | Russian plural grammar helper for day names |

### Data flow

1. **Scheduled notify job** (daily at `RUN_AT`, default 09:00 MSK):  
   `remnawave.Client` → paginated `GET /api/users` → filter active users with `telegramId` + `expireAt` → compute days remaining → send Telegram message at 7/3/1-day thresholds → persist user snapshot to SQLite.

2. **Telegram long-poll** (runs when `TELEGRAM_ADMIN_ID != 0`):  
   Handles `/start`, `/menu`, `/tariff`, `/payff` (pay for another user), and admin-only tariff management commands.

3. **Payment flow** (`payments` package):  
   User selects tariff → taps "Я оплатил" → admin receives confirmation request → admin confirms → `PATCH /api/users` extends subscription by N months (new expiry = `max(now, current_expiry) + months`).

### Configuration

All configuration is via environment variables (see `.env.example`). Key ones:

| Variable | Purpose |
|---|---|
| `REMNAWAVE_BASE_URL` / `REMNAWAVE_API_TOKEN` | Panel connection (required) |
| `TELEGRAM_BOT_TOKEN` | Bot token (required) |
| `TELEGRAM_ADMIN_ID` | Admin Telegram user ID; `0` disables payment flow |
| `TZ` / `RUN_AT` | Timezone and daily job time (default `Europe/Moscow`, `09:00`) |
| `DRY_RUN` | Log only, skip Telegram sends |
| `RUN_ON_START` | Run notify job immediately at startup |
| `DB_PATH` | SQLite path (default `/data/bot.db`, volume-mounted in Docker) |

### Persistence

SQLite is the only persistent store. In Docker it lives in a named volume (`botdata`). Schema migrations run automatically on startup in `store/store.go`.
