# remnawave-notify-bot

**English** · [Русский](README.ru.md)

Lightweight Go service that polls a [Remnawave](https://remna.st) panel once a
day and notifies users on Telegram before their subscription expires (7 / 3 / 1
days out). Optionally, an admin can confirm a payment straight from Telegram and
the bot extends the subscription by one month.

## Features

- One daily cron run (`go-co-op/gocron/v2`) in the configured timezone.
- Remnawave authentication via the panel API token (`Authorization: Bearer ...`).
- Fetches users with `GET /api/users`, paginated by `start/size`.
- Processes only `status == "ACTIVE"`; users without a `telegramId` are skipped.
- Sends messages through the Telegram Bot API (`sendMessage`).
- "Я оплатил" (*I paid*) inline button in notifications, with an admin-confirmed
  flow that extends the subscription via `PATCH /api/users`.
- Welcome message on the `/start` command.
- Structured logs to stdout (`log/slog` JSON).
- Multi-stage Docker image based on `distroless/static`, `restart: unless-stopped`.

## Notification text

```
⏰ Подписка истекает через 7 дней. Для продления оплатите подписку.
⏰ Подписка истекает через 3 дня. Для продления оплатите подписку.
⏰ Подписка истекает через 1 день. Для продления оплатите подписку.
```

The Russian word for "day" (день / дня / дней) is grammatically agreed with the
number.

If `TELEGRAM_ADMIN_ID` is set, an inline **"Я оплатил"** button is attached to
the message. Use the admin's **Telegram user ID** here, not a group/channel chat
ID.

## Payment confirmation flow (optional)

Enabled only when `TELEGRAM_ADMIN_ID` is set:

1. A user taps **"Я оплатил"** under an expiry notification.
2. If the admin has configured tariffs, the bot shows month/price options on the
   message (e.g. `1 мес. — 150₽`, `3 мес. — 450₽`, plus a «← Назад» button) and
   the user picks one. With **no tariffs configured** it falls back to a single
   1-month request.
3. The bot sends the **admin** the client's details and the chosen months/price
   with a **"Подтвердить оплату"** (*Confirm payment*) button.
4. The admin confirms. The bot calls `PATCH /api/users` (with `uuid` in the body)
   and extends the subscription by the **chosen number of months** (from
   `max(now, current expiry)`), then reports the new expiry.

Prices are **informational** — the bot does not process money. The user pays you
externally and you confirm manually.

### Commands

**User commands** (anyone who messages the bot):

| Command   | What it does                                            |
| --------- | ------------------------------------------------------- |
| `/start`  | Show the welcome message                                |
| `/menu`   | Open the menu (💵 Тарифы and 💳 Оплатить за другого buttons) |
| `/tariff` | Show the current tariffs/prices                         |
| `/payff`  | Pay for another user (subscribers only)                 |
| `/cancel` | Cancel the current `/payff` step                        |
| `/help`   | Same as `/menu`                                         |

**Admin-only commands** (only the account whose ID equals `TELEGRAM_ADMIN_ID`;
silently ignored for everyone else):

| Command                      | What it does                  |
| ---------------------------- | ----------------------------- |
| `/tariffs`                   | List current tariffs          |
| `/settariff <months> <price>`| Add or update a tariff        |
| `/deltariff <months>`        | Remove a tariff               |

Tariffs and payment state are stored in SQLite (`DB_PATH`, default `/data/bot.db`,
kept in the `botdata` Docker volume), so they survive restarts.

### Paying for another user (`/payff`)

Any existing subscriber can pay for someone else:

1. Send `/payff` to the bot.
2. Enter the target's **Remnawave username** or **Telegram ID** (a value that is
   all digits is looked up by Telegram ID, otherwise by username).
3. Pick a period from the tariff buttons (or the single 1-month option when no
   tariffs are configured). `/cancel` or the **Отмена** button aborts.
4. The admin receives a request naming both the payer and the target, with a
   **"Подтвердить оплату"** button. Confirming extends the target's subscription.

Only existing subscribers may start `/payff`; every request is still gated by
admin confirmation.

**Discovering it:** the bot registers a command menu on startup (via
`setMyCommands`), so users get the blue **Menu** button and `/` autocomplete for
`/start`, `/menu`, `/tariff`, `/payff`, `/help`, `/cancel`. Sending `/menu` (or
`/help`) replies with **💵 Тарифы** and **💳 Оплатить за другого** buttons.
`/tariff` lets any user see the current prices.

Safeguards:

- Only the configured admin can confirm payments or manage tariffs.
- Confirmation is **idempotent**: repeated taps will not extend twice.
- New expiry = `max(now, current expiry)` + chosen months, so a late confirmation
  never lands in the past.

## Welcome message

On `/start` the bot replies with:

```
⏰ Привет! Я бот-напоминалка: если ваша подписка на КВН скоро закончится, я сообщу об этом заранее — за 7, 3 или 1 день до окончания.

Меню и команды:
/menu — открыть меню с кнопками
/tariff — посмотреть текущие тарифы
/payff — оплатить подписку за другого пользователя
/cancel — отменить текущее действие

После оплаты нажмите «Я оплатил» — администратор получит уведомление и подтвердит продление.
```

## Configuration (.env)

| Variable               | Required | Default          | Description                                                  |
| ---------------------- | -------- | ---------------- | ------------------------------------------------------------ |
| `REMNAWAVE_BASE_URL`   | yes      | —                | Base URL of the panel                                        |
| `REMNAWAVE_API_TOKEN`  | yes      | —                | Remnawave panel API token                                    |
| `TELEGRAM_BOT_TOKEN`   | yes      | —                | Telegram bot token (from @BotFather)                         |
| `TELEGRAM_PARSE_MODE`  | no       | `HTML`           | `HTML` / `MarkdownV2` / empty                                |
| `TELEGRAM_ADMIN_ID`    | no       | `0`              | Admin Telegram user ID for the "Я оплатил" flow (`0` = off)  |
| `TZ`                   | no       | `Europe/Moscow`  | IANA timezone                                                |
| `RUN_AT`               | no       | `09:00`          | Local time of the daily run (`HH:MM`)                        |
| `LOG_LEVEL`            | no       | `info`           | `debug` / `info` / `warn` / `error`                          |
| `HTTP_TIMEOUT`         | no       | `15s`            | HTTP request timeout (Go duration)                           |
| `DRY_RUN`              | no       | `false`          | Log instead of sending to Telegram                           |
| `RUN_ON_START`         | no       | `true`           | Run the job immediately on start                             |
| `DB_PATH`              | no       | `/data/bot.db`   | SQLite database file path                                    |
| `CURRENCY`             | no       | `₽`              | Currency label shown next to tariff prices                   |

## Running

### Install on your server (recommended)

One command downloads the project to **`/opt/remnaWake`** and launches the
interactive installer. Installing under `/opt` needs root, so run it as root or
with `sudo`:

```bash
curl -fsSL https://raw.githubusercontent.com/Nakedjustice/remnaWake/main/get.sh | sudo bash
```

To install somewhere else (a writable path needs no root), set `TARGET_DIR`:

```bash
curl -fsSL https://raw.githubusercontent.com/Nakedjustice/remnaWake/main/get.sh | TARGET_DIR="$HOME/remnaWake" bash
```

Prefer to do it by hand?

```bash
sudo git clone https://github.com/Nakedjustice/remnaWake.git /opt/remnaWake
cd /opt/remnaWake
sudo ./install.sh
```

The installer asks for the panel URL, API token, bot token, admin Telegram ID,
timezone and run time, writes a locked-down `.env` (mode `600`), and offers to
build and start the container with Docker Compose. Requires `git` (or `curl`/
`wget`) plus Docker Engine + the Compose plugin.

### Local (development)

```bash
cp .env.example .env
# edit .env
go mod download
go run .
```

### Docker

```bash
cp .env.example .env
# edit .env and set TELEGRAM_ADMIN_ID to enable the "Я оплатил" flow
docker compose up -d --build
docker compose logs -f
```

## Behavior

- If the Remnawave response reports `total > size`, the service fetches the
  remaining pages.
- On `401` from `/api/users`, check `REMNAWAVE_API_TOKEN`. The error is logged and
  the job does not crash.
- Telegram `429` is logged with `retry_after`; the remaining users are still
  processed.
- When `TELEGRAM_ADMIN_ID` is set, the bot runs long polling for `callback_query`
  and `message` updates to handle the "Я оплатил" button and the `/start` command.
- Deduplication is an exact match of the remaining days (`7/3/1`) on the run date.
  No state is persisted.

## Deploy your own copy

To host the bot from your own GitHub repo (so the one-line installer above works
for you and your users), push the project to a **public** repo:

```bash
# from the project directory
git remote add origin https://github.com/<you>/<repo>.git
git push -u origin main
```

Then point the download URLs at your repo by replacing
`Nakedjustice/remnaWake` (and the branch `main`) in `get.sh`, `README.md` and
`README.ru.md`, and the `module` path in `go.mod`. Your `.env` is gitignored and
is never pushed, so your API tokens stay private.

After pushing, verify the installer is reachable:

```bash
curl -fsSL https://raw.githubusercontent.com/<you>/<repo>/main/get.sh | head -5
```
