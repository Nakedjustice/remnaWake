# Pay for another user (`/payff`) — Design

## Problem

Today a subscriber can only pay for **their own** renewal, via the «Я оплатил»
inline button attached to an expiry reminder. There is no way for one person to
pay for someone else (a friend, a family member, a second account). This feature
adds a `/payff` command that lets an existing subscriber gift a renewal to
another user, identified by Remnawave username or Telegram ID, with the same
admin-confirmation step that gates every extension today.

## Goals

- A subscriber runs `/payff`, names a target, picks a period, and the admin
  confirms — extending the **target's** subscription.
- Reuse the existing tariff catalogue, the pending→confirmed payment-request
  state machine, and the admin confirm button. No parallel payment system.
- Keep the `payments` package decoupled from `remnawave` (interfaces only),
  mirroring the existing `Extender` pattern.

## Non-goals

- Self-service payment processing — admin confirmation stays manual, as today.
- Persisting in-flight conversation state across restarts (sessions are seconds
  long; a restart just means re-running `/payff`).
- Restricting which *target* statuses are payable (paying for an expired or
  disabled user is allowed and renews them).

## Decisions (from brainstorming)

- **Conversation state:** in-memory `map[chatID]*giftState`, guarded by a mutex,
  with a created-at timestamp and a 10-minute TTL checked on access.
- **Eligibility:** only existing subscribers may run `/payff`. The payer is
  resolved by their chat ID (which equals their Telegram ID in a private chat).
- **Identifier disambiguation:** auto-detect — an all-digits entry is looked up
  by Telegram ID, anything else by username.
- **Admin notification:** names **both** the payer and the target.
- **Abort:** a `/cancel` command and an «Отмена» button; no "back to re-enter"
  step.

## Flow

1. **`/payff`.** Resolve the payer via `by-telegram-id(chatID)`.
   - Not a subscriber → reply «Эта команда доступна только подписчикам.» and stop.
   - Subscriber → create `giftState{step: awaitingIdentifier, payer…}` and ask:
     «Введите имя пользователя или Telegram ID того, кому оплачиваете.»
2. **Typed identifier** (free-text message while a chat is mid-flow). Auto-detect:
   all-digits → `by-telegram-id`, else → `by-username`.
   - Not found → «Пользователь не найден, попробуйте ещё раз.» (stay in flow)
   - Telegram ID resolves to >1 subscription → «У этого Telegram ID несколько
     подписок, введите имя пользователя.» (stay in flow)
   - Exactly one match → store the target in state, advance to
     `awaitingTariff`, and show the tariff keyboard:
     «Оплата для {username} (до {date}). Выберите период:» with an «Отмена»
     button. If no tariffs are configured, fall back to a single 1-month option,
     exactly like the self-pay flow.
3. **Tariff tap** (`gpick:{months}`). Read the target + payer from state, look up
   the tariff price, create a `pending` payment request carrying **both** target
   and payer fields, clear the keyboard and the conversation state, and DM the
   admin: «💳 {payer} оплатил подписку для {target} — N мес. — {price}» with a
   «Подтвердить оплату» button (`ok:{reqID}`).
4. **Admin confirm** (`ok:{reqID}`, unchanged handler). Extend the target's UUID
   by N months from `max(now, expireAt)`, mark the request confirmed
   (idempotent), and remove the button.

## Components

### `internal/remnawave/client.go`

Two read-only lookups returning the same `User` shape used elsewhere:

- `GetUserByTelegramID(ctx, telegramID int64) ([]User, error)` —
  `GET /api/users/by-telegram-id/{telegramId}`. Returns an array (a Telegram ID
  may map to multiple subscriptions). HTTP 404 → empty slice, not an error.
- `GetUserByUsername(ctx, username string) (*User, error)` —
  `GET /api/users/by-username/{username}`, single user in the usual
  `{response: …}` envelope. HTTP 404 → `nil, nil`. The username is
  `url.PathEscape`d before interpolation.

Both reuse `setRequestHeaders` (bearer auth) and the existing non-2xx error
handling. New response-envelope types live in `types.go`.

### `internal/payments`

- **Conversation state** on `Service`:
  ```go
  type giftStep int
  const (
      stepAwaitingIdentifier giftStep = iota
      stepAwaitingTariff
  )
  type giftState struct {
      step      giftStep
      payerName string
      payerTGID int64
      target    *Subscriber // set once the identifier resolves
      createdAt time.Time
  }
  ```
  Stored in `map[int64]*giftState` guarded by a `sync.Mutex`. Reads check the
  10-minute TTL and drop stale entries.
- **`Finder` interface** (payments-local return type, to avoid importing
  `remnawave`):
  ```go
  type Subscriber struct {
      RemnawaveID int64
      UUID        string
      Username    string
      TelegramID  int64
      ExpireAt    time.Time
  }
  type Finder interface {
      FindByTelegramID(ctx context.Context, telegramID int64) ([]Subscriber, error)
      FindByUsername(ctx context.Context, username string) (*Subscriber, error)
  }
  ```
  A thin adapter in `main.go` wraps `*remnawave.Client`, converting
  `[]remnawave.User` → `[]Subscriber`. `Service` gains a `finder Finder` field.
- **Handlers:**
  - `StartGiftFlow(ctx, m) bool` — handles `/payff`; resolves payer, seeds state.
  - `HandleText(ctx, m) bool` — returns true when it consumes a free-text message
    for a chat that is mid-flow (the typed identifier); also handles `/cancel`.
  - Gift callbacks `gpick:{months}` and `gcancel`, dispatched from
    `HandleCallback` alongside the existing `pay:`/`pick:`/`back:`/`ok:` cases.
- **Admin notification** formatter naming payer and target; sent via
  `SendPlainWithKeyboard` (no `parse_mode`), as today.

### `internal/store`

- Add nullable columns to `payment_requests`: `payer_telegram_id INTEGER` and
  `payer_username TEXT`. Update the `CREATE TABLE` for fresh installs **and** run
  an idempotent migration that reads `PRAGMA table_info(payment_requests)` and
  `ALTER TABLE … ADD COLUMN` for any missing column (protects existing local
  DBs).
- `PaymentRequest` struct gains `PayerTelegramID int64` and `PayerUsername
  string`; `CreatePaymentRequest` writes them; `GetPaymentRequest` reads them
  (back-compat: NULL → zero value).

### `main.go`

In `pollTelegramCallbacks`, route messages in this order, before the admin-command
gate:

```
/start          → welcome
/payff          → pay.StartGiftFlow
/cancel or text → pay.HandleText (consumes only when chat is mid-flow)
else            → pay.HandleAdminCommand
```

Wire the `Finder` adapter into `payments.New`.

## Edge cases & safety

- Typed username is `url.PathEscape`d before path interpolation; the raw
  identifier is length-capped (reject absurdly long input) before any lookup.
- Telegram-ID parse uses `strconv.ParseInt`; non-parseable all-digit overflow is
  treated as "not found".
- Admin notifications remain `parse_mode`-free, so user-controlled names cannot
  break Telegram's HTML parser.
- Abandoned sessions expire by TTL; `/cancel`, the «Отмена» button, or a fresh
  `/payff` all clear state.
- Confirm stays admin-only and idempotent — no change.
- Dry-run: lookups are read-only; the reused confirm path already honours
  `dryRun`.

## Testing

- **Client** (`httptest`): both lookups assert method, exact path, bearer header,
  username escaping, 404 → empty/nil, and correct array vs single decode.
- **Payments** (table-driven, fake `Finder`/`BotSender`/`Extender` + in-memory
  `store`):
  - payer-not-subscriber rejection,
  - username vs TGID auto-detect,
  - TGID multi-match prompt (stays in flow),
  - not-found retry (stays in flow),
  - happy path creates a `pending` request with payer fields populated and DMs
    the admin,
  - `/cancel` and TTL expiry clear state.
- **Store:** round-trip a request with payer columns; migration adds the columns
  to a DB created without them.

## Verification

- `go build ./...`, `go vet ./...`, `gofmt -l` clean.
- `go test ./...` green.
- Manual (live panel): subscriber `/payff` → name a second account → pick a
  period → admin confirms → panel shows the target's expiry pushed out and the
  admin sees the «{payer} оплатил для {target}» line.
