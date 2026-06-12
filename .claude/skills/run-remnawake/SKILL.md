---
name: run-remnawake
description: Build, run, smoke-test, or manually drive the remnaWake service (Telegram bot + Mini App HTTP API) locally without a real Telegram token or Remnawave panel. Use when asked to run the app, start the server, verify a change works live, or hit the /api endpoints.
---

# Run remnaWake

Go service: daily notify cron + Telegram bot + optional Mini App HTTP server.
The bot/cron sides need a real Telegram token and Remnawave panel and cannot be
driven offline — but the Mini App server (and the SQLite-backed admin API) runs
fully offline with fake credentials. The driver for that is
`.claude/skills/run-remnawake/smoke.ps1` (paths relative to repo root).

There is no dotenv loader: `go run .` reads **process env vars only** (the
`.env` file is consumed by docker compose, not by the binary).

## Prerequisites

- Go toolchain (repo builds with the version on this machine; CGO not needed —
  SQLite is pure-Go `modernc.org/sqlite`).
- Windows PowerShell 5.1+ (the driver uses .NET HMACSHA256 and `Invoke-RestMethod`).

## Run (agent path) — the smoke driver

```powershell
powershell -NoProfile -File .claude\skills\run-remnawake\smoke.ps1
```

What it does (≈15 s, exits 0 on PASS):

1. `go build -o bin\remnawake-smoke.exe .`
2. Launches the binary with fake env: dead panel (`http://127.0.0.1:9`), fake
   bot token, `WEBAPP_LISTEN=127.0.0.1:18086`, temp `DB_PATH`, `DRY_RUN=true`,
   `RUN_ON_START=false`.
3. Signs Telegram Mini App `initData` itself (HMAC chain
   `WebAppData → botToken → data-check-string`, same algorithm as
   `internal/webapp/initdata.go`) for admin user 42.
4. Asserts: `GET /` serves the embedded frontend; `/api/admin` is 401 without
   auth and 200 with it; `POST /api/admin/tariff` and
   `POST /api/admin/screenshot-toggle` mutate state visible in the panel JSON;
   `GET /api/me` returns 502 (panel unreachable — proves auth passed).
5. Stops the process and deletes the temp DB.

To keep the server up for manual poking:

```powershell
powershell -NoProfile -File .claude\skills\run-remnawake\smoke.ps1 -KeepRunning
```

It prints the PID and a ready-to-use `Authorization: tma <initData>` header.
Then drive any endpoint in `internal/webapp/server.go`, e.g.:

```powershell
Invoke-RestMethod -Uri http://127.0.0.1:18086/api/admin -Headers @{ Authorization = 'tma <printed-value>' }
```

Stop with `Stop-Process -Id <pid> -Force`.

## Direct invocation (most PRs need only this)

Most changes land in `internal/payments` behind small interfaces with fakes —
the test suite drives bot flows end-to-end against real SQLite without any
network:

```powershell
go build ./...
go vet ./...
go test ./...
go test ./internal/payments/ -run TestName
```

## Run (human path)

Real deployment is docker compose with a populated `.env` (see `README`).
Locally `go run .` works only with all of `REMNAWAVE_BASE_URL`,
`REMNAWAVE_API_TOKEN`, `TELEGRAM_BOT_TOKEN` set in the environment — with real
values it long-polls Telegram and serves the Mini App if `WEBAPP_URL` is set.
Not usable for verification without real credentials; use the driver instead.

## Gotchas

- **`DB_PATH` defaults to `/data/bot.db`** (a Docker volume path). On a local
  run always set `DB_PATH` or the store fails / writes to `C:\data`.
- **`WEBAPP_URL` must be `https://...`** (config validation) but it is never
  fetched — it only enables the server and is sent to Telegram for the menu
  button. Any https placeholder works; the actual bind address is
  `WEBAPP_LISTEN`.
- **Fake bot token = expected log noise**: `getMe`/`setMyCommands`/
  `setChatMenuButton` warn with Telegram 401, and the update poller logs
  `telegram get updates failed` every ~5 s. Harmless; the HTTP server and
  scheduler run fine.
- **`/api/me` requires the panel** (it resolves profiles via the Remnawave
  API) → 502 offline. `/api/admin*` endpoints read/write SQLite only and work
  fully offline — they are the right surface for offline verification.
- **`RUN_ON_START` defaults to true** and immediately sweeps the panel; set
  `RUN_ON_START=false` offline or the log fills with panel errors.
- **Admin = `TELEGRAM_ADMIN_ID`**: the initData user id must be in that list
  or every `/api/admin*` call is 403. The driver uses 42 for both.

## Troubleshooting

- `401 invalid initData` from `/api/*`: the data-check-string must be
  `key=value` pairs **sorted by key, joined with `\n`, hash excluded**, and the
  `user` value must be raw JSON in the HMAC but URL-encoded in the header. The
  driver's `New-InitData` is a known-good reference.
- `a previous smoke server is still running`: a `-KeepRunning` server from an
  earlier run holds the exe image and the port; stop it as the message says
  (`Stop-Process -Name remnawake-smoke -Force`) and rerun.
- `server exited early` / `server did not come up`: the log tail is printed
  automatically; logs are per-run files in `%TEMP%\remnawake-smoke-<id>.log`
  (kept on failure, swept on PASS). A different port can be forced with
  `-Port`.
- Build artifacts land in `bin\` (gitignored).
