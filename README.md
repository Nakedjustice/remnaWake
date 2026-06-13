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
- **Win-back messages** 1 and 3 days *after* expiry (configurable via
  `WINBACK_DAYS` / `WINBACK_ENABLED`) with the same payment button.
- Every notification is deduplicated in SQLite, so restarts never double-send;
  failed sends are retried on the next daily run, and rate-limited (429)
  Telegram calls are retried automatically.
- **`/stats`** — admin report: panel users (active / expiring soon / expired /
  linked), payments and revenue for the last 30 days, gifts and invites.
- **Russian or English** chat interface via `BOT_LANG` (default Russian).
- Sends messages through the Telegram Bot API (`sendMessage`).
- **"Я оплатил"** (*I paid*) inline button in notifications, with an admin-confirmed
  flow that extends the subscription via `PATCH /api/users`.
- **`/register`** — self-service Telegram linking: a user sends their
  subscription link (or Remnawave profile name) — even just pasting the link
  into the chat works — the bot finds the account and links their Telegram ID
  without admin involvement.
- **`/invite`** — existing subscribers can request a new user to be created in the
  panel; admin approves and the new subscription URL is sent back to the inviter.
- **`/gift`** — buy a subscription as a gift without naming the recipient: after
  admin confirmation the buyer gets a unique one-time code and a `t.me` deep link
  to forward; whoever opens it gets the months added to their profile (or a brand
  new profile created on the spot).
- **`/mygifts`** — list the gift codes you bought with their current status
  (pending / issued / activated / rejected / revoked) and re-request the message
  with the redemption link if the original one was lost.
- **`/me`** — personal cabinet: linked profile(s) with status and expiry date,
  the subscription URL, a gifts/invites summary, plus buttons to renew, gift,
  invite, and re-link. Also opens via the persistent **«👤 Личный кабинет»**
  reply button installed on `/start`; unlinked users are offered the linking flow.
- Welcome message on the `/start` command, with a **🔗 Привязать аккаунт** button
  that walks new users through linking (and warns that without it no notifications arrive).
- **Telegram Mini App** — a web version of the personal cabinet (profiles,
  expiry, subscription link, tariffs with a renew button, gifts) opened from
  the chat menu button; enabled by setting `WEBAPP_URL` (see below).
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

Every reminder is recorded in SQLite before it is sent, so a restart (or the
run-on-start pass plus the daily run landing on the same day) can never deliver
the same notice twice; a failed Telegram send is retried on the next daily run.

### Win-back after expiry

When a subscription has already **expired** (panel status `EXPIRED`, or still
`ACTIVE` with a past expiry date), the bot sends a win-back message 1 and 3
days after the expiry date:

```
⛔️ ivan, ваша подписка истекла 10.06.2026.
Чтобы продолжить пользоваться сервисом, продлите подписку.
```

The message carries the same **"Я оплатил"** button, and the regular
confirmation flow extends the subscription from `max(now, old expiry)`.
`DISABLED` / `LIMITED` accounts are never contacted. Tune the days with
`WINBACK_DAYS` (default `1,3`) or turn the feature off with
`WINBACK_ENABLED=false`.

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

**Payment receipt (optional).** The admin can require proof of payment: toggle
**«📸 Чек об оплате»** in the `/admin` menu or the switch in the Mini App admin
panel. When enabled, after picking a tariff the user must send a **photo,
screenshot, or PDF file** of the receipt (banks often export receipts as PDF;
images sent as uncompressed files are accepted too) before the request is
created; the admin then receives the request as a photo or document message
with the receipt attached and the same confirm/reject buttons. Other file
types are rejected with a hint. A caption on the receipt is forwarded to the
admins; `/cancel` also works as a caption. The pending attachment step expires
after 10 minutes (`/cancel` aborts it); a receipt arriving later gets a notice
asking to start the renewal again. Renewals started from the
Mini App switch to the bot chat for this step. With the toggle off the flow is
unchanged.

### Commands

**User commands** (anyone who messages the bot):

| Command      | What it does                                                          |
| ------------ | --------------------------------------------------------------------- |
| `/start`     | Show the welcome message                                              |
| `/me`        | Personal cabinet: subscription status, link, gifts/invites, actions   |
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
| `/stats`                     | 📊 Statistics: panel users (total / active / expiring in 7 days / expired / linked to Telegram), payments confirmed in the last 30 days with revenue, pending requests, gift codes by status, pending invites |

The `/admin` menu also has a **📊 Статистика** button (same report as `/stats`),
a **🎁 Подарочные коды** section listing all issued (not yet activated)
gift codes, each with a one-tap revoke button, and a **🛡 Сквад по умолчанию**
picker that selects the panel internal squad new users (created via `/gift` and
`/invite`) are added to. When no squad is selected, the squad named
`Default-Squad` (seeded by a stock Remnawave install) is used; if neither can
be resolved, user creation fails with a visible error instead of producing a
profile without access to any nodes. The same picker is available in the Mini
App admin panel.

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
do it themselves without contacting the admin. The easiest way is to simply
paste the **subscription link** into the chat — no command needed: the bot
resolves the profile by the short UUID at the end of the link (via
`GET /api/users/by-short-uuid/{shortUuid}`) and offers to link it.

The explicit flow also accepts both inputs:

1. Send `/register` to the bot (or tap **🔗 Привязать аккаунт** in the menu).
2. Send your **subscription link** or your **Remnawave profile name** (both
   visible in the VPN app).
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

On `/start` the bot replies with a message that lists its functions and walks
through linking an account step by step (no link = no notifications), plus a
**🔗 Привязать аккаунт** inline button that launches the same flow as
`/register`:

```
👋 Привет! Я бот для управления вашей подпиской. Вот что я умею:

⏰ Напоминания — предупрежу об окончании подписки за 7, 3 и 1 день.
💳 Продление — после оплаты нажмите «Я оплатил» под напоминанием, и администратор подтвердит продление.
👤 Личный кабинет — статус подписки, ссылка и подарки: /me.
🎁 Подарок (/gift) — подарить подписку человеку, у которого есть Telegram: он сам активирует подарок в этом боте.
➕ Приглашение (/invite) — оформить подписку тому, у кого нет Telegram или кто не может им пользоваться: вы получите готовую ссылку и передадите её сами.

❗️ Сначала привяжите свой Telegram к профилю подписки — без привязки я не узнаю, какая подписка ваша, и напоминания приходить не будут.

Как привязать аккаунт — по шагам:
1. Откройте приложение, в котором вы пользуетесь подпиской, и скопируйте ссылку на подписку.
2. Отправьте эту ссылку мне обычным сообщением в этот чат — команда /register не нужна.
3. Я найду ваш профиль и спрошу «Привязать ваш Telegram к профилю …?» — нажмите кнопку «Привязать».
4. Когда придёт сообщение «✅ Готово!», привязка завершена и напоминания включены.

Нет ссылки под рукой? Нажмите «🔗 Привязать аккаунт» ниже (или отправьте /register) и введите имя профиля (например, ivan_petrov).
Если ошиблись или передумали — отправьте /cancel и начните заново.

Все команды:
/me — личный кабинет: статус подписки, ссылка, подарки
/menu — открыть меню с кнопками
/register — привязать свой Telegram к профилю
/tariff — посмотреть текущие тарифы
/gift — подарить подписку (получателю с Telegram)
/mygifts — мои подарочные подписки и их статус
/invite — оформить подписку человеку без Telegram
/cancel — отменить текущее действие
/help — помощь

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
| `WEBAPP_URL`           | no       | —                | Public HTTPS URL of the Telegram Mini App (empty = mini app off) |
| `WEBAPP_LISTEN`        | no       | `:8080`          | Local bind address for the mini app server (behind your reverse proxy) |
| `WINBACK_ENABLED`      | no       | `true`           | Send "subscription expired" win-back messages after expiry   |
| `WINBACK_DAYS`         | no       | `1,3`            | Days **after** expiry to send the win-back message (comma-separated) |
| `BOT_LANG`             | no       | `ru`             | Bot chat language: `ru` / `en`; also the Mini App's default language until the user picks one |

## Telegram Mini App

When `WEBAPP_URL` is set, the bot serves a Mini App version of the personal
cabinet and registers it as the chat **menu button** (plus an
«🖥 Открыть мини-приложение» button inside `/me`). The app shows linked
profiles with status and expiry, the subscription link with one-tap copy,
payment requisites, gift/invite summaries, and lets the user pick a tariff and
send a renewal request straight to the admin.

The Mini App has a **language selector** (🇷🇺 Russian / 🇬🇧 English) in the top
right corner. It defaults to the user's Telegram language (falling back to
`BOT_LANG`) and remembers an explicit choice in the browser. The switch
translates the whole Mini App interface, including subscription and gift
statuses. Free-form admin text (payment requisites) is shown exactly as the
admin entered it.

Requirements:

- The mini app is served by the bot itself on `WEBAPP_LISTEN` (default `:8080`).
  Telegram only opens Mini Apps over **HTTPS**, so put a reverse proxy
  (nginx / Caddy / Traefik) in front and point `WEBAPP_URL` at the public
  HTTPS address that forwards to the container's port 8080.
- Requests are authenticated by validating Telegram `initData` (HMAC signed
  with the bot token) — no extra secrets or logins needed.

Example with the bundled compose file: uncomment the `ports` section, then
proxy `https://bot.example.com` → `127.0.0.1:8080` and set
`WEBAPP_URL=https://bot.example.com`.

### Reverse proxy templates

Replace `bot.example.com` with your domain in either template.

**Caddy** (`/etc/caddy/Caddyfile`) — TLS certificates are obtained and renewed
automatically:

```caddyfile
bot.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

**nginx** (`/etc/nginx/sites-available/remnawake-webapp`) — assumes
certificates from certbot (`certbot --nginx -d bot.example.com` can also add
the TLS bits for you):

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name bot.example.com;

    ssl_certificate     /etc/letsencrypt/live/bot.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/bot.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

server {
    listen 80;
    server_name bot.example.com;
    return 301 https://$host$request_uri;
}
```

Enable and reload:

```bash
sudo ln -s /etc/nginx/sites-available/remnawake-webapp /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

### Running alongside remnawave (containerised Caddy)

If you already run [remnawave](https://remna.st) with its own **containerised**
Caddy, don't use the `127.0.0.1:8080` example above. Inside the Caddy container
`127.0.0.1` points at Caddy itself, not the host — just like remnawave's panel
and subscription page, the bot has to be reached by its **container name** over
the shared docker network.

**1. Join the bot to remnawave's network.** In the bot's `docker-compose.yml`,
add the external network (and skip the `ports:` block — Caddy reaches the bot
over the docker network, so port 8080 never has to touch the host):

```yaml
services:
  bot:
    image: ghcr.io/nakedjustice/remnawake:main
    pull_policy: always
    container_name: remnaWake-bot
    restart: unless-stopped
    env_file:
      - ./.env
    environment:
      DB_PATH: /data/bot.db
    volumes:
      - botdata:/data
    networks:
      - remnawave-network

networks:
  remnawave-network:
    name: remnawave-network
    external: true

volumes:
  botdata:
```

**2. Add a site to your existing `Caddyfile`** next to the remnawave entries,
proxying to the bot's container name and port:

```caddyfile
https://bot.example.com {
    reverse_proxy remnaWake-bot:8080
}
```

**3. Point `WEBAPP_URL` at that host** in `.env`:

```
WEBAPP_URL=https://bot.example.com
```

Then bring the bot up and reload Caddy:

```bash
docker compose up -d            # in the remnaWake directory
docker compose restart caddy    # in your caddy directory
```

Make sure `bot.example.com` resolves to this server in DNS before reloading, or
Caddy can't issue the certificate.

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
