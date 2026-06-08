# /register — self-service Telegram linking

**Date:** 2026-06-08
**Branch:** invitation-function (work continues here or a new branch)

## Goal

Let an existing Remnawave subscriber bind their own Telegram ID to their panel
account so the bot can send them expiry notifications. Today a user's
`telegramId` must be set manually in the panel; `/register` makes it self-service.

## User flow

1. User taps `/register` (slash command or the menu button). The bot replies:
   > Введите имя вашего профила (Можно посмотреть в приложении). /cancel — отмена.
2. User sends their Remnawave username.
3. Bot looks the username up via the existing `Finder.FindByUsername`:
   - **Not found** → "Профиль с таким именем не найден. Попробуйте ещё раз."
     (stays in the flow; user can retry or /cancel)
   - **Found, no `telegramId`** → bot shows a confirmation:
     "Привязать ваш Telegram к профилю «X»?" with buttons `[Привязать] [Отмена]`
   - **Found, already linked to *this* user** → "Этот профиль уже привязан к
     вашему Telegram." (idempotent — no write, flow ends)
   - **Found, linked to *someone else*** → "Этот профиль уже привязан к другому
     Telegram. Обратитесь к администратору." (refuse — no write, flow ends)
4. On **Привязать**, the bot writes the requester's chat ID as `telegramId`
   via `PATCH /api/users` and replies:
   > ✅ Готово! Ваш Telegram привязан к профилю «X».

The requester's private-chat `chatID` equals their Telegram user ID, which is
the value written.

### Why a confirmation step

Writing `telegramId` is a panel mutation, and the username lookup is what tells
us whether the account is free, already ours, or taken. So the bot looks the
account up, reports its status, and commits only on the button tap. This matches
the existing `/invite` and `/payff` confirmation patterns. (If immediate binding
on username entry were preferred, the confirm step would be dropped — but we keep
it for consistency and safety.)

### Eligibility

Unlike `/payff` and `/invite`, `/register` does **not** gate on the requester
already being a subscriber (`FindByTelegramID`). The whole point is to link users
who are *not yet* associated with any account, so no eligibility check is applied
before asking for the username.

## Architecture

The flow mirrors `/invite`'s structure but is simpler: it holds only in-memory
conversation state and performs an immediate write — there is **no DB table**,
because there is no pending request to persist for admin review.

### Components

| File | Change |
|---|---|
| `internal/remnawave/client.go` | New `SetTelegramID(ctx, uuid string, telegramID int64) error` → `PATCH /api/users` with body `{uuid, telegramId}`. Same header/error handling as `ExtendSubscriptionByUUID`. |
| `internal/payments/payments.go` | New `Registrar` interface (`SetTelegramID(ctx, uuid string, telegramID int64) error`); add `registrar Registrar` field and `registers map[int64]*registerState` to `Service`; thread `registrar` through `New(...)` and initialize the map. |
| `internal/payments/register.go` | **New file.** `registerState` (target username + UUID, requester TGID, whether confirm is pending, `createdAt`); `getRegister`/`setRegister`/`clearRegister` with TTL (10 min, matching invite/gift); `StartRegisterFlow`, `beginRegisterFlow`, `handleMenuRegister`, `handleRegisterUsernameInput`, `showRegisterConfirm`, `handleRegisterConfirm`, `handleRegisterCancel`. |
| `internal/payments/callbacks.go` | Dispatch `reg_confirm` and `reg_cancel`. |
| `internal/payments/gift.go` | `HandleText`: add register state to the `/cancel` check; after the invite handler, chain `handleRegisterUsernameInput`. `SendMenu`: add a "🔗 Привязать аккаунт" button (`menu:register`) and a `/register` text line. |
| `main.go` | Route `/register` in `pollTelegramCallbacks`; add `{Command: "register", ...}` to `userBotCommands()`; add an `rwRegistrar` adapter wrapping `*remnawave.Client` and pass it into `payments.New(...)`. |

### State machine (`registerState`)

```
(no state) --/register or menu:register--> awaiting-username
awaiting-username --valid username, account free--> awaiting-confirm (show buttons)
awaiting-username --not found--> awaiting-username (retry message)
awaiting-username --linked to self / linked to other--> (clear, terminal message)
awaiting-confirm --reg_confirm--> write telegramId, clear, success
awaiting-confirm --reg_cancel / /cancel--> clear, "Отменено."
```

TTL expiry (10 min) drops the state, identical to `getInvite`/`getGift`.

### Data flow for the write

`handleRegisterConfirm` → `Registrar.SetTelegramID(ctx, uuid, requesterTGID)`
→ `*remnawave.Client.SetTelegramID` → `PATCH /api/users {uuid, telegramId}`.

`dryRun`: when set, log the intended write and skip the HTTP call, mirroring the
confirm/approve handlers (`s.dryRun` branch returning a "(dry-run)" success).

## Error handling

- Lookup error (`FindByUsername` returns err) → "Ошибка, попробуйте позже.",
  state preserved so the user can retry.
- Write error (`SetTelegramID` returns err) → log it, reply
  "Ошибка привязки. Попробуйте позже." State cleared (the confirm message's
  keyboard is removed to prevent re-taps).
- Username validation reuses `isValidUsername` (3–32 chars, letters/digits/`_`);
  a leading `/` re-prompts rather than being treated as a username, matching the
  invite input handler.

## Testing

Table-driven tests for the `register.go` state machine using the existing
payments fakes (as in `gift_flow_test.go`), with a fake `Registrar` recording
calls. Cases:

- username not found → retry message, no write, state still awaiting-username
- account free → confirm shown; `reg_confirm` → `SetTelegramID` called with the
  requester's TGID and the looked-up UUID; success message
- already linked to self → idempotent success, no write
- already linked to other → refusal, no write
- `dryRun` true → no write, dry-run success message
- `SetTelegramID` returns error → error message, no panic, keyboard cleared
- TTL expiry mid-flow → stale state dropped

## Out of scope

- No persisted `register_requests` table (no admin approval path).
- No change to how `/invite` creates users or to notification scheduling.
- No bulk/admin re-link command.
