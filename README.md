# remnaWake

**English** · [Русский](README.ru.md)

Lightweight Go service that polls a [Remnawave](https://remna.st) panel once a
day and notifies users on Telegram before their subscription expires (7 / 3 / 1
days out). Optionally, an admin can confirm a payment straight from Telegram and
the bot extends the subscription by the chosen number of months.

## Features

- One daily cron run (`go-co-op/gocron/v2`) in the configured timezone.
- Remnawave authentication via the panel API token (`Authorization: Bearer ...`).
- Fetches users with `GET /api/users`, paginated by `start/size`.
- Processes only `status == "ACTIVE"`; users without a `telegramId` are skipped.
- Sends messages through the Telegram Bot API (`sendMessage`).
- **"Я оплатил"** (*I paid*) inline button in notifications, with an admin-confirmed
  flow that extends the subscription via `PATCH /api/users`.
- **`/register`** — self-service Telegram linking: a user enters their Remnawave
  profile name, the bot finds the account and links their Telegram ID without
  admin involvement.
- **`/invite`** — existing subscribers can request a new user to be created in the
  panel; admin approves and the new subscription URL is sent back to the inviter.
- **`/gift`** — buy a subscription as a gift without naming the recipient: after
  admin confirmation the buyer gets a unique one-time code and a `t.me` deep link
  to forward; whoever opens it gets the months added to their profile (or a brand
  new profile created on the spot).
- **`/mygifts`** — list the gift codes you bought with their current status
  (pending / issued / activated / rejected / revoked) and re-request the message
  with the redemption link if the original one was lost.
- Welcome message on the `/start` command, with a **🔗 Привязать аккаунт** button
  that walks new users through linking (and warns that without it no notifications arrive).
- Structured logs to stdout (`log/slog` JSON).
- Multi-stage Docker image based on `distroless/static`, `restart: unless-stopped`.

## Notification text

```
⏰ ivan, ваша подписка истекает 17.06.2026 — через 7 дней.
Для продления оплатите подписку.
```

Each message names the user's **Remnawave profile** and the **exact expiry date**
(`DD.MM.YYYY`), so the recipient can tell which subscription is ending. The Russian
word for "day" (день / дня / дней) is grammatically agreed with the number.

If `TELEGRAM_ADMIN_ID` is set, an inline **"Я оплатил"** button is attached to
the message. Use the admin's **Telegram user ID** here, not a group/channel chat
ID.

## Payment confirmation flow (optional)

Enabled only when `TELEGRAM_ADMIN_ID` is set:

1. A user taps **"Я оплатил"** under an expiry notification. If the admin has set
   **payment requisites** (see `/setrequisites` below), the bot first sends them to
   the user (e.g. a card number or SBP details).
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

| Command      | What it does                                                          |
| ------------ | --------------------------------------------------------------------- |
| `/start`     | Show the welcome message                                              |
| `/menu`      | Open the menu with inline buttons                                     |
| `/tariff`    | Show the current tariffs/prices                                       |
| `/gift`      | Buy a gift subscription and get a shareable code/link (subscribers only) |
| `/mygifts`   | List your purchased gift codes, their status, and re-send the link    |
| `/invite`    | Invite a new user to the panel (subscribers only)                     |
| `/register`  | Link your Telegram to an existing Remnawave profile                   |
| `/cancel`    | Cancel the current `/gift`, `/invite`, or `/register` step            |
| `/help`      | Same as `/menu`                                                       |

**Admin-only commands** (only the account whose ID equals `TELEGRAM_ADMIN_ID`;
silently ignored for everyone else):

| Command                      | What it does                  |
| ---------------------------- | ----------------------------- |
| `/tariffs`                   | List current tariffs          |
| `/settariff <months> <price>`| Add or update a tariff        |
| `/deltariff <months>`        | Remove a tariff               |
| `/setrequisites`             | Set the payment requisites shown to users after «Я оплатил» (two-step: send the command, then the text in the next message) |
| `/requisites`                | Show the currently saved payment requisites |

The `/admin` menu also has a **🎁 Подарочные коды** section listing all issued
(not yet activated) gift codes, each with a one-tap revoke button.

Tariffs and payment state are stored in SQLite (`DB_PATH`, default `/data/bot.db`,
kept in the `botdata` Docker volume), so they survive restarts.

### Menu buttons

When a user sends `/menu` (or `/help`), the bot replies with five inline buttons:

| Button                       | Action                                |
| ---------------------------- | ------------------------------------- |
| 💵 Тарифы                   | Show current tariff prices            |
| 🎁 Подарить подписку         | Start the gift-subscription flow      |
| 📦 Мои подарки               | List your gift codes and their status |
| 👤 Пригласить пользователя   | Start the invite-new-user flow        |
| 🔗 Привязать аккаунт         | Start the Telegram-linking flow       |

### Gifting a subscription (`/gift`)

A gift does not require knowing the recipient in advance — the buyer gets a
transferable code instead (this also covers paying for another user: just
forward them the link):

1. Send `/gift` to the bot (or tap **🎁 Подарить подписку** in the menu). The bot
   shows the payment requisites (if set) and the tariff buttons.
2. Pick a period. The admin receives the request with **«✅ Подтвердить оплату»**
   and **«❌ Отклонить»** buttons.
3. On confirmation the buyer receives a unique one-time code and a deep link like
   `https://t.me/<bot>?start=gift_<CODE>` to forward to anyone.
4. The recipient opens the link (or sends `/start gift_<CODE>`):
   - if they already have a profile linked to their Telegram, it is **extended**
     by the gifted months (with a choice when several profiles are linked);
   - if they have no profile, the bot asks for a desired username, **creates** a
     new profile in the panel, links their Telegram ID and sends the subscription
     URL. The subscription clock starts at activation, not at purchase.
5. The buyer is notified when the gift is activated.

Each code is strictly **single-use** (atomic claim in the database — concurrent
attempts can't double-spend), survives bot restarts, and stays valid until
activated or revoked by the admin. Only existing subscribers may start `/gift`;
every purchase is gated by admin confirmation.

The buyer can check their gifts at any time with `/mygifts` (or the **📦 Мои
подарки** menu button): the bot lists every purchased code with its status —
awaiting payment confirmation, issued/awaiting activation, activated (with the
recipient's profile name), rejected, or revoked. Issued codes get a **🔗** button
that re-sends the message with the redemption link, in case the original one was
lost. Only the buyer can request their own links.

### Inviting a new user (`/invite`)

Any existing subscriber can request a new Remnawave account to be created:

1. Send `/invite` to the bot.
2. Enter the desired **username** for the new user (3–32 characters, letters /
   digits / underscore).
3. The bot shows the cost (1-month tariff price) and a **"Отправить заявку"**
   button. `/cancel` aborts.
4. The admin receives a request with a **"✅ Одобрить"** and **"❌ Отклонить"**
   button. On approval, the bot creates the user in the panel and sends the new
   subscription URL back to the inviter.

Only existing subscribers may start `/invite`; every request is gated by admin
confirmation.

### Linking Telegram to a profile (`/register`)

Users who already have a Remnawave account but haven't linked their Telegram can
do it themselves without contacting the admin:

1. Send `/register` to the bot (or tap **🔗 Привязать аккаунт** in the menu).
2. Enter your **Remnawave profile name** (visible in the VPN app).
3. The bot looks up the account. If the profile exists and has no Telegram linked,
   it asks for confirmation.
4. Tap **"Привязать"** — the bot calls `PATCH /api/users` and links your Telegram
   ID to the profile immediately.

If the profile is already linked to your Telegram, the bot acknowledges it
idempotently. If it is linked to a different account, the bot refuses and asks
you to contact the admin. Session expires after 10 minutes of inactivity.

**Discovering it:** the bot registers a command menu on startup (via
`setMyCommands`), so users get the blue **Menu** button and `/` autocomplete for
all commands listed above. Sending `/menu` (or `/help`) shows the five inline
buttons.

Safeguards:

- Only the configured admin can confirm payments, approve invites/gifts, revoke
  gift codes, or manage tariffs.
- Payment confirmation is **idempotent**: repeated taps will not extend twice.
- New expiry = `max(now, current expiry)` + chosen months, so a late confirmation
  never lands in the past.

## Welcome message

On `/start` the bot replies with a message that explains how to link an account
and **why it matters** (no link = no notifications), plus a **🔗 Привязать
аккаунт** inline button that launches the same flow as `/register`:

```
⏰ Привет! Я бот-напоминалка: если ваша подписка на КВН скоро закончится, я сообщу об этом заранее — за 7, 3 или 1 день до окончания.

❗️ Чтобы получать уведомления, сначала привяжите свой Telegram к профилю подписки. Без привязки я не смогу понять, какая подписка ваша, и напоминания приходить не будут.

Как привязать:
1. Нажмите кнопку «🔗 Привязать аккаунт» ниже (или команду /register).
2. Введите имя вашего профиля — его можно посмотреть в приложении.
3. Подтвердите привязку — и всё готово, уведомления включены.

Меню и команды:
/menu — открыть меню с кнопками
/register — привязать свой Telegram к профилю
/tariff — посмотреть текущие тарифы
/gift — подарить подписку
/mygifts — мои подарочные подписки и их статус
/cancel — отменить текущее действие

После оплаты нажмите «Я оплатил» — администратор получит уведомление и подтвердит продление.

[🔗 Привязать аккаунт]
```

## Configuration (.env)

| Variable               | Required | Default          | Description                                                  |
| ---------------------- | -------- | ---------------- | ------------------------------------------------------------ |
| `REMNAWAVE_BASE_URL`   | yes      | —                | Base URL of the panel                                        |
| `REMNAWAVE_API_TOKEN`  | yes      | —                | Remnawave panel API token                                    |
| `TELEGRAM_BOT_TOKEN`   | yes      | —                | Telegram bot token (from @BotFather)                         |
| `TELEGRAM_PARSE_MODE`  | no       | `HTML`           | `HTML` / `MarkdownV2` / empty                                |
| `TELEGRAM_ADMIN_ID`    | no       | `0`              | Admin Telegram user ID for the payment/invite/register flows (`0` = off) |
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
pull the pre-built image and start the container with Docker Compose. Requires
`git` (or `curl`/`wget`) plus Docker Engine + the Compose plugin.

### Local (development)

```bash
cp .env.example .env
# edit .env
go mod download
go run .
```

### Docker (pre-built image)

A ready-to-run image is published to GHCR, so no local build is needed:

```bash
cp .env.example .env
# edit .env and set TELEGRAM_ADMIN_ID to enable payment/invite/register flows
docker compose up -d          # pulls ghcr.io/nakedjustice/remnawake:latest
docker compose logs -f
```

To update later, pull the newest image and recreate the container:

```bash
docker compose pull && docker compose up -d
```

### Docker (build from source)

If you changed the code (or run your own fork) and want to build locally instead
of pulling, use the build override:

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

## Behavior

- If the Remnawave response reports `total > size`, the service fetches the
  remaining pages.
- On `401` from `/api/users`, check `REMNAWAVE_API_TOKEN`. The error is logged and
  the job does not crash.
- Telegram `429` is logged with `retry_after`; the remaining users are still
  processed.
- When `TELEGRAM_ADMIN_ID` is set, the bot runs long polling for `callback_query`
  and `message` updates to handle inline buttons and all commands.
- Deduplication is an exact match of the remaining days (`7/3/1`) on the run date.
  No state is persisted for notifications.

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

The `.github/workflows/docker-publish.yml` workflow publishes a pre-built image
to **your** GHCR (`ghcr.io/<you>/<repo>`, lowercased) on every push to `main`.
Update the `image:` line in `docker-compose.yml` to match your lowercased
`ghcr.io/<you>/<repo>`. The package is created on the first successful run; make
it public under your repo's **Packages** settings so users can pull without
authenticating. Until then (or anytime), users can build from source with the
`docker-compose.build.yml` override shown above.

After pushing, verify the installer is reachable:

```bash
curl -fsSL https://raw.githubusercontent.com/<you>/<repo>/main/get.sh | head -5
```
