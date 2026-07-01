# 🌙 remnaWake

**English** · [Русский](README.ru.md)

Lightweight Go service that polls a [Remnawave](https://remna.st) panel once a
day and reminds users on Telegram before their subscription expires (7 / 3 / 1
days out). An admin can confirm a payment straight from the chat and the bot
extends the subscription by the chosen number of months.

## ✨ Features

- ⏰ **Expiry reminders** 7 / 3 / 1 days before expiry, plus **win-back**
  messages 1 and 3 days *after* expiry (`WINBACK_DAYS` / `WINBACK_ENABLED`).
- 💳 **Payment confirmation** — user taps **«Я оплатил»**, admin confirms, the
  bot extends the subscription via `PATCH /api/users`. Optional automatic
  online payments via **Platega** (SBP / cards) and/or **Telegram Stars** (⭐).
- 👤 **Personal cabinet** (`/me`) — linked profiles, status, expiry,
  subscription link, gifts/invites and quick actions.
- 🔗 **Self-service linking** (`/register`) — paste a subscription link or
  profile name and the bot links your Telegram ID, no admin needed.
- 🎁 **Gifts** (`/gift`, `/mygifts`) — buy a subscription as a transferable
  one-time code / `t.me` deep link; recipient activates it themselves.
- ➕ **Invites** (`/invite`) — subscribers request a new panel account; admin
  approves and the new subscription URL is sent back.
- 🎁 **Free trial** (`/trial` or the Mini App, optional) — a brand-new user
  creates a shaped trial profile once per Telegram ID, with configurable
  duration, total traffic, HWID/device limit, and an optional dedicated squad
  (`TRIAL_ENABLED`). Admins can change all trial settings at runtime from the
  bot admin menu or the Mini App admin panel.
- 🎉 **Referral bonus** (optional) — an approved invite grants bonus days to the
  inviter and/or the invitee (`REFERRAL_ENABLED`). Runtime-configurable from the
  bot admin menu and the Mini App admin panel.
- 🔔 **Notification preferences** — users mute expiry reminders and win-back
  messages independently, from the bot **🔔 Notifications** button or the Mini
  App cabinet.
- 📊 **Stats** (`/stats`) — panel users, 30-day payments & revenue, gifts,
  invites.
- 🩺 **Proxy monitoring** (optional) — bundles
  [xray-checker](https://github.com/kutovoys/xray-checker) as a sidecar; admins
  see per-proxy health in `/admin` and the Mini App and get a DM when a proxy
  goes down or recovers (`XRAY_CHECKER_URL`).
- 🖥 **Telegram Mini App** — web version of the cabinet (set `WEBAPP_URL`).
- 🗃 **Reliable** — every notification deduplicated in SQLite (no double-sends
  across restarts), failed sends retried next run, Telegram `429` auto-retried.
- 🌍 **Russian or English** interface (`BOT_LANG`, default Russian).
- 🐳 Distroless multi-stage Docker image, `restart: unless-stopped`, structured
  JSON logs to stdout.

## 🔔 Notifications

```
⏰ ivan, ваша подписка истекает 17.06.2026 — через 7 дней.
Для продления оплатите подписку.
```

Each message names the user's **Remnawave profile** and the **exact expiry
date** (`DD.MM.YYYY`); the Russian word for "day" agrees grammatically with the
number. Only `ACTIVE` users with a `telegramId` are notified;
`DISABLED` / `LIMITED` accounts are never contacted.

If `TELEGRAM_ADMIN_ID` is set (use the admin's **Telegram user ID**, not a
group/channel ID), an inline **«Я оплатил»** button is attached. Every reminder
is recorded in SQLite before sending, so a restart can never deliver the same
notice twice; failed sends are retried on the next daily run.

**Win-back after expiry** — when a subscription has expired (status `EXPIRED`,
or `ACTIVE` with a past date), the bot sends a win-back message with the same
button 1 and 3 days later; confirmation extends from `max(now, old expiry)`.

```
⛔️ ivan, ваша подписка истекла 10.06.2026.
Чтобы продолжить пользоваться сервисом, продлите подписку.
```

## 💳 Payment confirmation (optional)

Enabled only when `TELEGRAM_ADMIN_ID` is set:

1. User taps **«Я оплатил»**. If **payment requisites** are set
   (`/setrequisites`), the bot sends them first (e.g. card / SBP details).
2. If tariffs are configured, the bot shows month/price options
   (`1 мес. — 150₽`, …); otherwise it falls back to a single 1-month request.
3. The **admin** receives the client's details and chosen months/price with a
   **«Подтвердить оплату»** button.
4. On confirmation the bot calls `PATCH /api/users` and extends by the chosen
   months from `max(now, current expiry)`, then reports the new expiry.

Prices are **informational** — the bot does not process money in P2P mode; the
user pays you externally and you confirm manually.

### ⚡ Automatic providers: Platega & Telegram Stars (optional)

Besides P2P you can enable the **Platega** gateway (SBP / cards) and/or
**Telegram Stars** (the native in-app XTR currency) for *automatic* online
payments. Enable any combination at runtime via **💳 Способы оплаты** in
`/admin` or the Mini App admin panel — **several can be on at once**; when more
than one is enabled the user picks the method at checkout.

- 🏦 **Platega** — set `PLATEGA_MERCHANT_ID` + `PLATEGA_SECRET` (see
  `.env.example`). Picking a tariff returns an **«Оплатить»** link and a
  **«🔄 Проверить оплату»** button; Platega calls back `POST /platega/callback`
  (same HTTP server as the Mini App — set it as the notification URL in the
  Platega dashboard) and the bot re-verifies and extends.
- ⭐ **Telegram Stars** — set `TELEGRAM_STARS_ENABLED=true` and a
  `TELEGRAM_STARS_RATE` (price units per Star; the Stars charged for a tariff is
  `ceil(price / rate)`), then turn it on in the admin **Способы оплаты** picker.
  No extra credentials and **no webhook** — it uses the bot's own token and the
  bot sends a native Telegram invoice (currency `XTR`) right in the chat (the
  Mini App opens an in-app invoice). Confirmation arrives over `getUpdates`
  (`pre_checkout_query` → `successful_payment`).

All confirmations are **idempotent** — duplicate webhooks, button taps or
payment events never extend a subscription twice.

### 📸 Payment receipt (optional)

Toggle **«📸 Чек об оплате»** in `/admin` or the Mini App to require proof of
payment: after picking a tariff the user must attach a receipt before the
request is created; the admin receives it with the usual confirm/reject buttons.
The bot chat and Mini App both support this step. JPEG/PNG photos are limited to
10 MB; PDF, WebP, and HEIC documents to 50 MB. The step expires after 10 minutes
(`/cancel` aborts in chat).

## ⌨️ Commands

**User** (anyone messaging the bot):

| Command     | What it does                                                  |
| ----------- | ------------------------------------------------------------ |
| `/start`    | Welcome message + account-linking walkthrough                |
| `/me`       | Personal cabinet: status, link, gifts/invites, actions       |
| `/menu` · `/help` | Open the menu with inline buttons                      |
| `/tariff`   | Show current tariffs/prices                                  |
| `/gift`     | Buy a gift subscription, get a shareable code (subscribers)  |
| `/mygifts`  | List your gift codes, their status, and re-send the link     |
| `/invite`   | Invite a new user to the panel (subscribers)                 |
| `/register` | Link your Telegram to an existing Remnawave profile          |
| `/cancel`   | Cancel the current `/gift` / `/invite` / `/register` step     |

**Admin** (only `TELEGRAM_ADMIN_ID`; silently ignored otherwise):

| Command                       | What it does                  |
| ----------------------------- | ----------------------------- |
| `/tariffs`                    | List current tariffs          |
| `/settariff <months> <price>` | Add or update a tariff        |
| `/deltariff <months>`         | Remove a tariff               |
| `/setrequisites`              | Set payment requisites shown after «Я оплатил» (two-step) |
| `/requisites`                 | Show the saved payment requisites |
| `/stats`                      | 📊 Panel users, 30-day payments & revenue, pending requests, gift codes, pending invites |

The **`/admin`** menu also offers 📊 statistics, a 🎁 gift-codes browser (by
buyer → used/not-used → individual codes, with one-tap revoke), and a
🛡 **default-squad** picker for new users created via `/gift` and `/invite`
(falls back to the stock `Default-Squad`; user creation fails visibly if no
squad can be resolved). The same controls live in the Mini App admin panel.

In the Mini App, the **payment history** merges subscription payments, gift
codes and invites into one list: filter it by type (payments / gifts / invites)
and delete any record from history (deletion is local-only — it never reverses
an applied panel change, and removing an unredeemed gift code makes it unusable).

- 👤 **Manage user** — look a panel user up by **profile name** or
  **subscription link**, then edit them directly: extend or shorten the expiry
  (±30/±90 days or an exact number), set the device (HWID) limit, set the
  traffic limit in GB (0 = unlimited), choose the traffic-reset strategy,
  add/remove internal squads, and **disable or enable** the subscription —
  without opening the Remnawave panel. In the Mini App this is a dedicated page
  with a searchable table of every panel user; in the bot you look the user up
  by name or link.
- 🔄 **Traffic reset (new users)** — choose the traffic-reset strategy
  (`NO_RESET` / `DAY` / `WEEK` / `MONTH`) applied to **newly created** users
  (`/gift`, `/invite`); it does not rewrite existing users.

Both live in `/admin` and in the Mini App admin panel.

Tariffs and payment state are stored in SQLite (`DB_PATH`, default
`/data/bot.db`, in the `botdata` Docker volume), so they survive restarts.

**Safeguards** — only the admin can confirm payments, approve invites/gifts,
revoke codes or manage tariffs; payment confirmation is idempotent; new expiry
= `max(now, current expiry) + chosen months`, so a late confirmation never
lands in the past.

## 🎁 Gifting (`/gift`)

A gift needs no recipient up front — the buyer gets a transferable code:

1. Send `/gift` (or tap **🎁 Подарить подписку**); the bot shows requisites and
   tariff buttons.
2. Pick a period → the admin gets the request with **«✅ Подтвердить оплату»** /
   **«❌ Отклонить»**.
3. On confirmation the buyer receives a one-time code + deep link
   `https://t.me/<bot>?start=gift_<CODE>`.
4. The recipient opens it (or sends `/start gift_<CODE>`): an existing linked
   profile is **extended**; otherwise the bot asks for a username, **creates** a
   profile, links their Telegram and sends the subscription URL (clock starts at
   activation).
5. The buyer is notified on activation.

Each code is strictly **single-use** (atomic DB claim — no double-spend),
survives restarts, and stays valid until activated or revoked. Only subscribers
may start `/gift`; every purchase is admin-gated. Check anytime with `/mygifts`
(**📦 Мои подарки**) — issued codes get a **🔗** button to re-send a lost link.

## ➕ Inviting (`/invite`)

Any subscriber can request a new Remnawave account:

1. Send `/invite`.
2. Enter the desired **username** (3–32 chars, letters / digits / underscore).
3. The bot shows the cost (1-month tariff) + **«Отправить заявку»** (`/cancel`
   aborts).
4. The admin gets **«✅ Одобрить»** / **«❌ Отклонить»**; on approval the bot
   creates the user and sends the subscription URL back to the inviter.

## 🔗 Linking (`/register`)

Already have a Remnawave account but no Telegram link? Just **paste the
subscription link** into the chat — no command needed: the bot resolves the
profile by its short UUID (`GET /api/users/by-short-uuid/{shortUuid}`) and
offers to link it. The explicit flow:

1. Send `/register` (or tap **🔗 Привязать аккаунт**).
2. Send your **subscription link** or **profile name**.
3. The bot looks it up; if unlinked, it asks for confirmation.
4. Tap **«Привязать»** — the bot calls `PATCH /api/users` and links you
   immediately.

Already linked to you → acknowledged idempotently. Linked to someone else →
refused (contact the admin). Session expires after 10 minutes. On startup the
bot registers its command menu via `setMyCommands`, so users get the blue
**Menu** button and `/` autocomplete.

## 🔄 Auto-update (optional)

With `AUTOUPDATE_ENABLED=true` the bot periodically checks the registry for a
newer `AUTOUPDATE_IMAGE` and DMs every admin when one is published, with
**🔄 Установить сейчас** / **Позже** buttons (interval
`AUTOUPDATE_CHECK_INTERVAL`, default `6h`).

Because the bot runs as a distroless, unprivileged container, **one-tap
install** is delegated to a [Watchtower](https://containrrr.dev/watchtower/)
sidecar in HTTP-API trigger-only mode. `install.sh configure` can generate the
Watchtower service in `docker-compose.override.yml`, set
`AUTOUPDATE_ENABLED=true`, `WATCHTOWER_URL=http://watchtower:8080` and create a
shared `WATCHTOWER_TOKEN`. The bot calls Watchtower's `/v1/update` on
**Install now**; only Watchtower holds the Docker socket. With `WATCHTOWER_URL`
empty the feature is notify-only and shows the manual
`docker compose pull && docker compose up -d` command.

The generated Watchtower service uses `containrrr/watchtower:latest` with
`pull_policy: always` and `DOCKER_API_VERSION=1.40`. If an older install logs
`client version 1.25 is too old`, rerun `./install.sh configure`, or refresh the
sidecar with `docker compose pull watchtower` and `docker compose up -d watchtower`.

## 🩺 Xray Checker proxy monitoring (optional)

Bundles [xray-checker](https://github.com/kutovoys/xray-checker) as an optional
companion container so admins can see whether the proxies their users connect
through are actually working. The sidecar probes connectivity through every
VLESS/VMess/Trojan/Shadowsocks config in a subscription and exposes the result
on its Prometheus `/metrics` endpoint (port `2112`).

`install.sh configure` can enable it: it asks for the subscription URL to
monitor, generates basic-auth credentials, sets `XRAY_CHECKER_URL=http://xray-checker:2112`,
and writes the `xray-checker` service into `docker-compose.override.yml`. Both
containers share the compose project's default network, so the bot reaches the
checker by name — no published port is required.

With `XRAY_CHECKER_URL` set the bot:

- adds a **🩺 Состояние прокси** button to the `/admin` menu and a **Proxy
  health** page to the Mini App admin panel, listing each proxy as ✅ up (with
  latency) or ❌ down;
- polls the checker every `XRAY_CHECKER_POLL_INTERVAL` (default `2m`) and DMs
  every admin when a proxy goes **down** or **recovers** (deduped across
  restarts, so enabling the feature never alerts for already-down proxies).

The bot only consumes the checker's metrics — it does not re-implement proxy
probing. Leave `XRAY_CHECKER_URL` empty to keep the feature off (the default).

### Full web dashboard

The Mini App proxy tab is intentionally compact. xray-checker also ships its
own roomy web dashboard, and you can let admins open it from the bot or a
browser by setting **`XRAY_CHECKER_PUBLIC_URL`** to where that dashboard is
reachable. When set, the bot adds a **🌐 Веб-панель прокси** button to the
`/admin` menu and the proxy-health card, and an **Open full dashboard** button
to the Mini App proxy tab. The link is independent of `XRAY_CHECKER_URL`, so it
works even without metrics polling. Because the dashboard sits behind the same
basic auth as `/metrics` (`METRICS_PROTECTED=true`), it stays admin-only — a
browser prompts for the checker credentials, no Telegram login required.

`install.sh configure` automates exposing it. When the checker is enabled it
asks *"Expose the Xray Checker web dashboard at a public URL?"*:

- **Same domain as the Mini App** (when `WEBAPP_URL` is set): it serves the
  dashboard under a sub-path (default `/checker`, so the dashboard is at
  `…/checker/`). It sets the sidecar's `METRICS_BASE_PATH`, rewrites
  `XRAY_CHECKER_URL` to include the prefix (so `/metrics` polling keeps working),
  and — when the bot runs behind the Remnawave Caddy — adds the route to the
  Caddy site for you (injecting it into an existing bot site if one is already
  there):

  ```caddy
  https://bot.example.com {
      redir /checker /checker/
      handle /checker/* {
          reverse_proxy remnaWake-xray-checker:2112
      }
      reverse_proxy remnaWake-bot:8080
  }
  ```

  The base path is preserved upstream (so it matches `METRICS_BASE_PATH`), and
  the bare `/checker` redirects to `/checker/` since that's where the sidecar
  serves the dashboard. Behind a host-level nginx/Caddy it publishes
  `127.0.0.1:<port>:2112` and prints the matching `location` + redirect snippet.
- **Your own domain** (no Mini App): you enter the full public URL and wire your
  own reverse proxy to the sidecar; the dashboard is served at that host's root.

## 🖥 Telegram Mini App

When `WEBAPP_URL` is set, the bot serves a Mini App version of the cabinet and
registers it as the chat **menu button** (plus an «🖥 Открыть мини-приложение»
button in `/me`). It shows linked profiles, status & expiry, the subscription
link with one-tap copy, requisites, gift/invite summaries, profile registration,
gift-code redemption, receipt upload, and Platega payment status checks. Users
can pick a tariff and finish the complete renewal flow without leaving the Mini
App. A 🇷🇺/🇬🇧 **language
selector** (top right) defaults to the user's Telegram language (falling back to
`BOT_LANG`) and remembers an explicit choice.

Admins also get a **Statistics** page with panel user totals, gift-code counts,
and pending invites. Its payment report provides 7/30/90-day revenue,
conversion and provider breakdowns, a daily trend, and searchable, paginated
renewal history for P2P, Platega, and Telegram Stars. Gateway transaction IDs
are shown when available.

**Requirements** — served by the bot on `WEBAPP_LISTEN` (default `:8080`).
Telegram only opens Mini Apps over **HTTPS**, so put a reverse proxy
(nginx / Caddy / Traefik) in front and point `WEBAPP_URL` at the public HTTPS
address. For host-level proxies the installer publishes
`127.0.0.1:${WEBAPP_HOST_PORT}:8080` (default host port `8080`); if you choose a
different host port, use it in the proxy target below. Requests are
authenticated by validating Telegram `initData` (HMAC signed with the bot token)
— no extra secrets.

### Reverse proxy templates

Replace `bot.example.com` with your domain.

**Caddy** (`/etc/caddy/Caddyfile`) — TLS is automatic:

```caddyfile
bot.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

**nginx** (`/etc/nginx/sites-available/remnawake-webapp`) — assumes certbot
certificates (`certbot --nginx -d bot.example.com`):

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

```bash
sudo ln -s /etc/nginx/sites-available/remnawake-webapp /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

### Alongside remnawave (containerised Caddy)

If you already run [remnawave](https://remna.st) with its own **containerised**
Caddy, don't use `127.0.0.1:8080` — inside the Caddy container that points at
Caddy itself. Reach the bot by its **container name** over the shared network.

**1. Join the bot to remnawave's network** (skip the `ports:` block). The
installer writes this as `docker-compose.override.yml`; the full shape is:

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

**2. Add a site to your existing `Caddyfile`**:

```caddyfile
https://bot.example.com {
    reverse_proxy remnaWake-bot:8080
}
```

**3. Point `WEBAPP_URL` at that host** in `.env`, then bring up the bot and
reload Caddy (make sure DNS resolves first, or Caddy can't issue the cert):

```bash
docker compose up -d            # in the remnaWake directory
docker compose restart caddy    # in your caddy directory
```

## ⚙️ Configuration (.env)

| Variable               | Required | Default          | Description                                                  |
| ---------------------- | -------- | ---------------- | ------------------------------------------------------------ |
| `REMNAWAKE_CHANNEL`    | no       | `main`           | Installer-selected release channel: `main` stable or `dev` unstable |
| `REMNAWAVE_BASE_URL`   | yes      | —                | Base URL of the panel                                        |
| `REMNAWAVE_API_TOKEN`  | yes      | —                | Remnawave panel API token                                    |
| `TELEGRAM_BOT_TOKEN`   | yes      | —                | Telegram bot token (from @BotFather)                         |
| `TELEGRAM_PARSE_MODE`  | no       | `HTML`           | `HTML` / `MarkdownV2` / empty                                |
| `TELEGRAM_ADMIN_ID`    | no       | `0`              | Admin Telegram user ID for payment/invite/register flows (`0` = off) |
| `TZ`                   | no       | `Europe/Moscow`  | IANA timezone                                                |
| `RUN_AT`               | no       | `09:00`          | Local time of the daily run (`HH:MM`)                        |
| `LOG_LEVEL`            | no       | `info`           | `debug` / `info` / `warn` / `error`                          |
| `HTTP_TIMEOUT`         | no       | `15s`            | HTTP request timeout (Go duration)                           |
| `DRY_RUN`              | no       | `false`          | Log instead of sending to Telegram                           |
| `RUN_ON_START`         | no       | `true`           | Run the job on start; reminders are still only sent within the `RUN_AT` hour |
| `DB_PATH`              | no       | `/data/bot.db`   | SQLite database file path                                    |
| `CURRENCY`             | no       | `₽`              | Currency label shown next to tariff prices                   |
| `WEBAPP_URL`           | no       | —                | Public HTTPS URL of the Mini App (empty = off)               |
| `WEBAPP_LISTEN`        | no       | `:8080`          | Local bind address for the mini app server                   |
| `WEBAPP_HOST_PORT`     | no       | `8080`           | Host-only reverse-proxy port; installer maps `127.0.0.1:<port>` to container port `8080` |
| `WINBACK_ENABLED`      | no       | `true`           | Send win-back messages after expiry                          |
| `WINBACK_DAYS`         | no       | `1,3`            | Days **after** expiry to send the win-back message           |
| `BOT_LANG`             | no       | `ru`             | Bot language: `ru` / `en` (also the Mini App default)        |
| `PLATEGA_MERCHANT_ID`  | no       | —                | Platega merchant id; set with `PLATEGA_SECRET` to enable it  |
| `PLATEGA_SECRET`       | no       | —                | Platega merchant secret                                      |
| `PLATEGA_METHOD`       | no       | `sbp`            | Platega payment method: `sbp` or `card`                      |
| `PLATEGA_CURRENCY`     | no       | `RUB`            | ISO currency code sent to the Platega API                    |
| `PLATEGA_RETURN_URL`   | no       | `https://t.me`   | Where Platega returns the user after payment                 |
| `TELEGRAM_STARS_ENABLED` | no     | `false`          | Enable native Telegram Stars (XTR) payments (uses the bot token; no webhook) |
| `TELEGRAM_STARS_RATE`  | no¹      | —                | Price units per Star; Stars charged = `ceil(price / rate)`. ¹Required when `TELEGRAM_STARS_ENABLED=true` |
| `TRIAL_ENABLED`        | no       | `false`          | Enable the one-time free trial for new users (no linked profile)            |
| `TRIAL_DAYS`           | no²      | `3`              | Trial length in days. ²Required (positive) when `TRIAL_ENABLED=true`        |
| `TRIAL_TRAFFIC_LIMIT_GB` | no     | `10`             | Total trial traffic allowance in GB; `0` means unlimited                    |
| `TRIAL_HWID_DEVICE_LIMIT` | no    | `1`              | Trial device limit; `0` means unlimited                                     |
| `TRIAL_SQUAD_UUID`       | no     | empty            | Dedicated trial squad UUID; empty inherits the default squad                |
| `REFERRAL_ENABLED`     | no       | `false`          | Reward approved invites with bonus days for inviter and/or invitee          |
| `REFERRAL_INVITER_BONUS_DAYS` | no | `30`            | Bonus days added to the inviter's own subscription per approved invite      |
| `REFERRAL_INVITEE_BONUS_DAYS` | no | `0`             | Extra days granted to the invited user on top of the 1-month invite term    |

## 🚀 Running

### Install on your server (recommended)

The bot runs from the pre-built GHCR image, so only `docker-compose.yml` and
`.env` land on your server. One command runs the interactive installer, which
writes those into **`/opt/remnaWake`** (needs root):

```bash
curl -fsSL https://raw.githubusercontent.com/Nakedjustice/remnaWake/main/get.sh | sudo bash
```

Install elsewhere (no root needed for a writable path) with `TARGET_DIR`:

```bash
curl -fsSL https://raw.githubusercontent.com/Nakedjustice/remnaWake/main/get.sh | TARGET_DIR="$HOME/remnaWake" bash
```

Or run the installer directly (installs into the current dir, override with
`REMNAWAKE_DIR`):

```bash
mkdir -p ~/remnaWake && cd ~/remnaWake
curl -fsSL https://raw.githubusercontent.com/Nakedjustice/remnaWake/main/install.sh | bash
```

The installer first asks for a release channel. `main` is the default stable
channel. `dev` uses the development branch and
`ghcr.io/nakedjustice/remnawake-dev:latest`; the installer shows an instability
warning and asks for confirmation before using it. It then asks for the panel
URL, tokens, admin ID, timezone, run time, and optional Mini App, Platega, Stars,
trial, referral and auto-update settings. On rerun it reuses existing `.env`
values as defaults, keeps timestamped backups, writes `.env` with mode `600`,
fetches `docker-compose.yml`, and writes local topology choices to
`docker-compose.override.yml`. Needs `curl` (or `wget`) + Docker Engine + the
Compose plugin — no `git` needed.

Maintenance helpers:

```bash
./install.sh configure   # first install walks every section; reopens the menu if .env exists
./install.sh menu        # jump straight to the reconfigure menu to edit one section
./install.sh doctor      # check Docker, .env, compose config, ports and updates
./install.sh update      # back up config, pull the image and restart
./install.sh backup      # copy .env and compose files into ./backups
```

### Local (development)

```bash
cp .env.example .env
# edit .env
go mod download
go run .
```

### Docker (pre-built image)

```bash
cp .env.example .env
# edit .env and set TELEGRAM_ADMIN_ID to enable payment/invite/register flows
docker compose up -d          # pulls ghcr.io/nakedjustice/remnawake:latest
docker compose logs -f
```

Update later:

```bash
./install.sh update
```

### Docker (build from source)

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

## 🧭 Behavior

- If the Remnawave response reports `total > size`, remaining pages are fetched.
- On `401` from `/api/users`, check `REMNAWAVE_API_TOKEN`; the error is logged
  and the job does not crash.
- Telegram `429` is logged with `retry_after`; remaining users are still
  processed.
- When `TELEGRAM_ADMIN_ID` is set, the bot long-polls `callback_query` and
  `message` updates to handle inline buttons and all commands.
- Deduplication is an exact match of the remaining days (`7/3/1`) on the run
  date.

## 📦 Deploy your own copy

To host the bot from your own GitHub repo (so the one-line installer works for
you), push the project to a **public** repo:

```bash
git remote add origin https://github.com/<you>/<repo>.git
git push -u origin main
```

Then replace `Nakedjustice/remnaWake` (and the branch `main`) in `get.sh`,
`README.md` and `README.ru.md`, and the `module` path in `go.mod`. Your `.env`
is gitignored and never pushed.

The `.github/workflows/docker-publish.yml` workflow publishes two independent
GHCR packages (names are lowercased):

- `main` → `ghcr.io/<you>/<repo>:latest` for stable deployments;
- `dev` → `ghcr.io/<you>/<repo>-dev:latest` for pre-merge testing.

Use the installer channel prompt to select the development package for a test
deployment before merging `dev` into `main`, or pull it manually:

```bash
docker pull ghcr.io/<you>/<repo>-dev:latest
```

Make both packages public under your repo's **Packages** settings if they must be
pulled without authentication. Until then, users can build from source with the
`docker-compose.build.yml` override above.

Verify the installer is reachable:

```bash
curl -fsSL https://raw.githubusercontent.com/<you>/<repo>/main/get.sh | head -5
```
