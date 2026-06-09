# Multiple Admins Design

**Date:** 2026-06-09  
**Branch:** multiple-admins  
**Status:** Approved

## Overview

Allow the bot to be configured with multiple Telegram admin IDs. All admins receive payment and invite notifications. Any admin can confirm/reject a request; when one acts, the inline button is removed from all other admins' copies of the message. Admin management commands (tariff, requisites) are per-session — each admin operates independently.

---

## Section 1: Config & env var parsing

**File:** `internal/config/config.go`

- `TelegramConfig.AdminID int64` → `TelegramConfig.AdminIDs []int64`
- Env var name stays `TELEGRAM_ADMIN_ID` (no rename)
- Parsing: split value on commas, trim whitespace, parse each token as `int64`
- `0` remains the "disabled" sentinel — absent env var or value `"0"` → empty slice → admin-gated features disabled
- Validation: rejects any non-numeric token with a clear error message
- Single-ID usage (e.g. `TELEGRAM_ADMIN_ID=123456`) continues to work unchanged

**Example values:**
```
TELEGRAM_ADMIN_ID=123456           # single admin (existing behaviour)
TELEGRAM_ADMIN_ID=123456,789012    # two admins
TELEGRAM_ADMIN_ID=0                # disabled
```

---

## Section 2: Telegram Bot — message ID extraction

**File:** `internal/telegram/bot.go`

- Add `sendMessageResponse` struct with `apiResponse` embedded + `Result Message` field
- `sendMessage` now decodes into `sendMessageResponse` and returns the `Result.MessageID`
- `SendPlainWithKeyboard` signature: `error` → `(int64, error)` (returns sent message ID)
- All existing callsites `_ = s.bot.SendPlainWithKeyboard(...)` → `_, _ = ...`
- `BotSender` interface in `internal/payments/payments.go` updated to match

---

## Section 3: Payments Service — multi-admin core

**Files:** `internal/payments/payments.go`, `callbacks.go`, `commands.go`, `invite.go`, `gift.go`

### 3a. Admin set + helper

- `adminID int64` → `adminIDs []int64` on `Service`
- `New(...)` accepts `adminIDs []int64` instead of `adminID int64`
- Add `isAdmin(id int64) bool` — linear scan (slice is always tiny)
- Add `isEnabled() bool` — `len(s.adminIDs) > 0`
- All `s.adminID == 0` / `s.adminID != 0` checks replaced with `!s.isEnabled()` / `s.isEnabled()`
- All `cb.From.ID != s.adminID` / `m.Chat.ID != s.adminID` checks replaced with `!s.isAdmin(...)`

### 3b. Per-admin input state

- `adminInput adminInputState` → `adminInput map[int64]adminInputState` (keyed by admin chat ID)
- All input flow functions gain a `chatID int64` parameter
- State reads/writes operate on `s.adminInput[chatID]`
- `HandleAdminCommand` passes `m.Chat.ID` through the call chain
- Admin menu callbacks pass `cb.From.ID` through the call chain
- All command reply functions (`cmdSetRequisites`, `cmdShowRequisites`, `cmdListTariffs`, `cmdSetTariff`, `cmdDelTariff`, `SendAdminMenu`, `sendAdminTariffs`, `sendAdminDelList`, `sendAdminRequisites`) gain a `chatID int64` parameter replacing the hardcoded `s.adminID`

### 3c. Broadcast notifications + message ref tracking

New type (in `payments.go`):
```go
type adminMsgRef struct {
    chatID    int64
    messageID int64
}
```

New fields on `Service` (protected by `mu`):
```go
payMsgs    map[int64][]adminMsgRef  // payment request ID → admin message refs
inviteMsgs map[int64][]adminMsgRef  // invite request ID  → admin message refs
```

**On notification send** (`createRequestAndNotify`, `handleInviteSubmit`):
- Loop over `s.adminIDs`
- Call `SendPlainWithKeyboard` for each admin, collect `(msgID, err)`
- Append `adminMsgRef{chatID: adminID, messageID: msgID}` to the appropriate map entry
- Log send errors per admin but continue to remaining admins

**On confirm/reject** (`handleConfirm`, `handleInviteApprove`, `handleInviteReject`):
- After successful action, retrieve refs from the map
- Loop over refs, call `EditMessageReplyMarkup(ctx, ref.chatID, ref.messageID, nil)` for each (this covers the acting admin's own message too — remove the pre-existing separate `EditMessageReplyMarkup(cb.Message...)` call in these handlers)
- Delete the entry from the map
- Log edit errors but treat as non-fatal

**Restart behaviour:** Maps are in-memory only. On restart, refs are lost. Tapping a stale button returns "already processed" (or "not found") from the DB — acceptable for V1.

---

## Section 4: Install script

**File:** `install.sh`

### 4a. Validator (`v_admin_id`)

Current behaviour: accepts single integer or `0`.

New behaviour:
- Split input on commas
- Trim whitespace from each token
- Validate each token matches `^[0-9]+$`
- Error message updated to mention comma-separated format
- The `0`-disables warning fires only when the entire value is exactly `"0"`

### 4b. Prompt & summary

- Prompt: `"Telegram admin user ID(s) (numeric, comma-separated; 0 to disable payments & invites)"` 
- Summary line: `Admin ID(s) : $TELEGRAM_ADMIN_ID`

---

## Affected files summary

| File | Change |
|------|--------|
| `internal/config/config.go` | `AdminID int64` → `AdminIDs []int64`, comma-split parsing |
| `internal/config/config_test.go` | Update tests |
| `internal/telegram/bot.go` | `SendPlainWithKeyboard` returns `(int64, error)`, add `sendMessageResponse` |
| `internal/telegram/bot_test.go` | Update tests |
| `internal/payments/payments.go` | `adminIDs []int64`, `isAdmin()`, `isEnabled()`, `adminMsgRef`, ref maps |
| `internal/payments/callbacks.go` | Broadcast notifications, ref tracking, multi-admin auth checks |
| `internal/payments/commands.go` | Per-admin input state map, `chatID` param on all reply fns |
| `internal/payments/invite.go` | Broadcast invite notifications, ref tracking, multi-admin auth checks |
| `internal/payments/gift.go` | `s.adminID` → `isEnabled()` / `isAdmin()`, broadcast gift payment notifications, ref tracking |
| `main.go` | Pass `cfg.Telegram.AdminIDs`, loop `SetMyCommandsForChat` |
| `install.sh` | Comma-aware validator, updated prompt & summary |

---

## Out of scope (V1)

- Persisting admin message refs to DB (restart recovery)
- Role distinction between admins (all admins are equal)
- Dynamic admin management via bot commands
