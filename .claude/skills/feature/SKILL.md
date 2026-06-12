---
name: feature
description: Deliver a complete feature end-to-end using the standard remnaWake pipeline — confirm a spec, plan tasks, implement each via TDD, run the full test suite, commit with a clean message, and open a PR. Use when asked to build/add/implement a feature, or when the user runs /feature.
---

# Feature pipeline

Own a feature from spec to PR in one pass. Follow the six steps in order; do not
skip ahead. The whole loop is governed by `CLAUDE.md` (TDD-first, bilingual docs,
Windows/PowerShell git rules, scope discipline) — re-read those sections if unsure.

## 1. Write / confirm the spec

- Restate the request as a focused spec: the user-facing behavior, the affected
  surfaces (bot flow, Mini App API, store schema), and explicit out-of-scope
  items.
- If anything material is ambiguous, ask a short round of clarifying questions
  **before** writing code. Separate the "think" and "build" phases.
- Get a quick confirmation on the spec before implementing. Keep it tight — a few
  bullets, not a document.

## 2. Plan the tasks

- Break the spec into discrete, reviewable tasks (use TaskCreate/TaskUpdate to
  track them). Each task should be independently testable.
- Map each task to the files it touches: most logic lives in `internal/payments`
  behind the small interfaces (`BotSender`, `Extender`, `Finder`, `Creator`,
  `Registrar`); new callbacks need a prefix case in
  `internal/payments/callbacks.go`; schema changes go through idempotent
  `ensure*Columns` migrations in `internal/store/store.go`.
- Honor the bot/webapp parity rule: shared logic goes in extracted helpers so the
  bot behaves identically with `WEBAPP_URL` unset.

## 3. TDD each task

For every task, in this order:

1. **Write the failing test first** — real SQLite in `t.TempDir()` plus the fakes
   in `payments_test.go` / `server_test.go`. Follow those patterns; do not add
   mocks.
2. Run it and confirm it fails for the right reason.
3. Implement the minimum to make it pass.
4. Refactor while green.

Respect state-machine and DRY_RUN invariants: status transitions stay race-safe
via conditional `UPDATE ... WHERE status = ...`; confirm paths must honor
`s.dryRun`. New service errors are sentinel values mapped in
`internal/webapp/server.go` and the bot callbacks.

## 4. Run the full suite

```powershell
go build ./...
go vet ./...
go test ./...
```

All three must be clean before moving on. For a live check of the Mini App /
admin API, the `run-remnawake` skill's smoke driver runs fully offline.

## 5. Commit with a clean message

- Branch first if on `main`/`dev` is not the intended target; keep the change
  scoped strictly to the feature (do not touch `install.sh` prompts/warnings or
  unrelated files).
- Update user-facing docs **bilingually** (Russian + English) as part of the same
  change when the feature is user-visible.
- **Windows/PowerShell: never use here-strings for the commit message.** Use
  `git commit -m "..."` with a simple quoted subject (or a temp file). End the
  message with the required `Co-Authored-By` trailer.

## 6. Open a PR

- **Before pushing, verify a git `origin` remote exists** (`git remote -v`). If
  missing, set it up and confirm with the user before pushing — especially before
  any push to `main`.
- Push the branch and open the PR with `gh pr create`, summarizing the spec and
  the tests added.
- Report the PR link. Stop and ask the user only if a test cannot be made to pass
  after a couple of honest attempts, or a decision genuinely needs their input.
