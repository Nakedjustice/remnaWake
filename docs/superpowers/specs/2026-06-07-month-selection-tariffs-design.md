# Month-selection tariffs for the "Я оплатил" flow — Design

**Date:** 2026-06-07
**Status:** Approved (pending spec review)

## Context

Today the bot's payment flow is fixed and minimal: a user taps «Я оплатил» under
an expiry notification, the admin is DMed the client's details with a
«Подтвердить оплату» button, and confirming extends the subscription by a
hard-coded **one month** (`PATCH /api/users`). Prices and durations are not
configurable, and all payment state (`paidUsers`, `confirmedUUIDs`) lives in
memory and is lost on restart.

We want users to **choose how many months** to renew, from a set of options the
**admin configures at runtime** (each option has a price). Prices are
**informational only** — the bot does not process money. The user still pays
externally, taps the option matching what they paid, and the admin manually
confirms; the bot then extends by the **chosen** number of months.

Because the admin manages options live (not via env/redeploy), the bot gains a
small **SQLite** persistence layer. We also persist payment state, which retires
the current "restart loses in-flight requests" limitation.

## Goals

- Admin manages month/price tariffs by chatting with the bot (no file edits, no restart).
- User selects a tariff via a two-step inline-button flow; the admin confirms; the
  bot extends by the selected months.
- Tariffs **and** payment state persist in SQLite across restarts.
- Graceful fallback to today's single-button / 1-month behavior when no tariffs exist.
- No regression to the existing notification, scheduling, or `/start` behavior.

## Non-goals (YAGNI)

- No payment-provider integration (Telegram Payments, YooKassa, crypto, refunds).
- No decimal prices (integer whole units only).
- No per-user or time-limited promotional pricing.
- No web/admin UI — administration is entirely via Telegram commands.

## Architecture (Approach 2: extract a `payments` domain)

### New packages

- **`internal/store`** — SQLite persistence only, via `modernc.org/sqlite` (pure
  Go, so `CGO_ENABLED=0` and the static distroless build are unaffected). Opens
  the DB, runs `CREATE TABLE IF NOT EXISTS` migrations on `New`, and exposes typed
  methods. Knows nothing about Telegram or Remnawave.
- **`internal/payments`** — domain logic: tariff management, the payment-request
  lifecycle, admin-command parsing, and building Telegram keyboards/messages.
  Depends on `store`, a Telegram bot sender, and a subscription "extender".

### Changed components

- **`internal/notify`** — sheds all payment code (`paidUsers`, `confirmedUUIDs`,
  the confirm/extend handlers, payment keyboards). It keeps scanning and sending
  notifications; it asks `payments` for the «Я оплатил» button to attach, and on
  sending a notification it tells `payments`/`store` to remember that user
  (persisted snapshot).
- **`main.go`** poll loop — delegates `callback_query` updates and admin command
  messages to a `payments.Handler`; keeps the `/start` welcome.
- **`internal/telegram/bot.go`** — add `editMessageReplyMarkup` to swap a message's
  inline keyboard during the two-step flow. (`SendPlainWithKeyboard`,
  `AnswerCallbackQuery`, `SendPlain` already exist.)
- **`internal/config`** — add `DB_PATH` and `CURRENCY`.
- **`go.mod`, `Dockerfile`, `docker-compose.yml`** — add the SQLite driver and a
  persistent volume for the DB file.

### Dependency wiring (`main.go`)

```
store.New(cfg.DBPath)            -> *store.Store        (fatal on error)
payments.New(store, bot, rwClient, cfg.AdminID, cfg.Currency, logger)
notify.NewService(rwClient, bot, payments, logger, cfg.DryRun)
```

`payments` is passed into `notify` (for the button + remembering users) and into
the poll loop (for callbacks + commands).

## Data model (SQLite, 3 tables)

```sql
CREATE TABLE IF NOT EXISTS tariffs (
  months     INTEGER PRIMARY KEY,        -- duration in months, unique
  price      INTEGER NOT NULL,           -- whole currency units (integer)
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notified_users (
  remnawave_user_id INTEGER PRIMARY KEY, -- snapshot captured at notify time
  uuid              TEXT NOT NULL,
  username          TEXT NOT NULL,
  telegram_id       INTEGER NOT NULL,
  expire_at         TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS payment_requests (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  remnawave_user_id INTEGER NOT NULL,
  uuid              TEXT NOT NULL,
  username          TEXT NOT NULL,
  telegram_id       INTEGER NOT NULL,
  months            INTEGER NOT NULL,    -- chosen; 1 in the no-tariff fallback
  price             INTEGER NOT NULL,    -- snapshot of tariff price; 0 in fallback
  status            TEXT NOT NULL,       -- 'pending' | 'confirmed'
  created_at        TEXT NOT NULL,
  confirmed_at      TEXT
);
```

- `tariffs` is displayed ascending by `months`.
- `notified_users` persists what used to be the in-memory `paidUsers`, so
  «Я оплатил» works after a restart.
- `payment_requests.status` replaces the in-memory `confirmedUUIDs` set and
  provides idempotency.

SQLite is opened with WAL and a busy timeout (e.g. `?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)`)
so the daily scan and the poll loop can write concurrently. Go's `*sql.DB` is
safe for concurrent use.

## Flows

### User flow (two-step)

1. The expiry notification carries a single «Я оплатил» button → callback
   `pay:<userID>`. (Attached only when `TELEGRAM_ADMIN_ID` is set, as today.)
2. User taps it → `payments` loads tariffs:
   - **Tariffs exist:** swap the message keyboard (`editMessageReplyMarkup`) for one
     button per tariff — label `«3 мес. — 450₽»`, callback `pick:<userID>:<months>`
     — plus a «← Назад» button (`back:<userID>`) that restores the single «Я оплатил»
     button.
   - **No tariffs (fallback):** create a `pending` request with `months=1`,
     `price=0`; DM the admin «Подтвердить оплату»; confirming extends 1 month —
     i.e. today's behavior exactly.
3. User taps a tariff → `payments`:
   - Reads the `notified_users` snapshot for `userID`. If missing, answer
     «Не удалось найти данные, дождитесь следующего уведомления.»
   - Writes a `pending` `payment_requests` row with chosen `months` + `price`.
   - Clears the message keyboard and shows a toast «Заявка отправлена администратору».
   - DMs the admin the client details + chosen months/price + «Подтвердить оплату»
     → callback `ok:<requestID>`.
4. Admin taps confirm → `payments`:
   - Rejects the callback if `cb.From.ID != TELEGRAM_ADMIN_ID`.
   - Loads the request; if already `confirmed`, answer «Подписка уже была продлена.»
     (idempotent); if not found, «Заявка не найдена.»
   - Computes `newExpireAt = max(now, expire_at).AddDate(0, request.months, 0)`
     and calls the existing `ExtendSubscriptionByUUID(uuid, newExpireAt)`
     (`PATCH /api/users`, uuid in body). The month arithmetic lives in `payments`;
     the remnawave client method is unchanged.
   - Marks the request `confirmed` (sets `confirmed_at`), removes the admin button
     (`editMessageReplyMarkup` → empty), answers a success toast, and DMs the new
     expiry.

### Admin commands

Messages from the admin's chat only (others ignored except `/start`):

- `/tariffs` — list current tariffs (or «Тарифы не заданы»).
- `/settariff <months> <price>` — add/update (upsert). Validates `months ≥ 1`,
  `price ≥ 0`, both integers; replies with a usage string on bad input.
- `/deltariff <months>` — remove one (or report «not found»).
- `/help` — show the command list.

### Callback data (all < 64 bytes)

`pay:<userID>` · `pick:<userID>:<months>` · `back:<userID>` · `ok:<requestID>`

### Display

- Prices are integer whole units; currency is a single label `CURRENCY`
  (default `₽`).
- Month labels use the abbreviation `«мес.»` (`1 мес.`, `3 мес.`, `12 мес.`),
  avoiding Russian plural grammar.

## Config

| Variable    | Required | Default       | Description                          |
| ----------- | -------- | ------------- | ------------------------------------ |
| `DB_PATH`   | no       | `/data/bot.db`| SQLite database file path            |
| `CURRENCY`  | no       | `₽`           | Currency label shown next to prices  |

All existing variables are unchanged. The installer (`install.sh`) gains optional
prompts for these in its advanced section.

## Persistence & Docker

The runtime image is distroless **nonroot**, so the DB directory must be writable
by that user:

- Mount a **named Docker volume** at `/data` in `docker-compose.yml`; set
  `DB_PATH=/data/bot.db`.
- The Dockerfile pre-creates `/data` owned by nonroot — created in the build stage
  and `COPY --chown=nonroot:nonroot`ed into the runtime stage. Docker initializes
  the empty named volume from that path, so it inherits writable ownership (no host
  `chown` needed). The DB survives `docker compose down`/up.
- `store.New` also `os.MkdirAll`s the parent directory for local (non-Docker) runs.

## Testing

- **`store`** (temp-file DB): tariff upsert/list/delete; `notified_users` upsert;
  `payment_requests` create/confirm + idempotency (second confirm is a no-op).
- **`payments`**: command parsing/validation; keyboard building; callback dispatch;
  the no-tariff fallback; extend-by-N-months. Uses small interfaces (a bot sender
  and a subscription extender) so tests run with fakes and no network.
- **`notify`**: light edits for delegation; the `daysUntil` timezone regression
  test is unaffected.
- Existing `remnawave` and `config` tests stay green.

## Error handling

- `store.New` failure (open/migrate) → fatal at startup, like config load.
- DB errors mid-callback → log + user toast «Ошибка, попробуйте позже».
- Telegram edit/answer failures → log and continue.
- Bad admin command args → reply a short usage string.
- Idempotency enforced via `payment_requests.status`; unknown callback data ignored
  and logged at debug (as today).
- All tariff/command/payment features remain gated on `TELEGRAM_ADMIN_ID` being set.

## Interfaces (for isolation/testing)

```go
// payments depends on these narrow interfaces, not concrete types.
type BotSender interface {
    SendPlain(ctx, chatID int64, text string) error
    SendPlainWithKeyboard(ctx, chatID int64, text string, kb *telegram.InlineKeyboardMarkup) error
    AnswerCallbackQuery(ctx, id, text string) error
    EditMessageReplyMarkup(ctx, chatID, messageID int64, kb *telegram.InlineKeyboardMarkup) error
}

type Extender interface {
    ExtendSubscriptionByUUID(ctx, uuid string, newExpireAt time.Time) error
}
```

`*telegram.Bot` and `*remnawave.Client` satisfy these (the bot gains
`EditMessageReplyMarkup`).
