# Giveback build progress

Handoff notes between build sessions. Read this first, then `/mnt/c/Users/Srinivas/Downloads/giveback.md` (the full brief: in scope, out of scope, rules).

**Deadline:** 2026-11-30 13:30 IST (midnight PST Nov 29). **Build window:** until 2026-11-27 00:00 IST; aim to submit Nov 26.

## Rules the user set (binding)

- Nothing from the brief's "Out of scope" list unless asked.
- Giveback never decides the gift amount, and never guesses silently: every answer shows the original handwritten line, and it asks "Is this them?" when unsure.
- Webhooks, not polling, so the Sprite can sleep.
- Models: **always `gemini-4-hydrogen`; fall back to `claude-haiku-5-5` only when Gemini fails.** Implemented in `internal/vision`.
- Go, chosen for fast startup.
- The user prefers going **step by step**, one action at a time, waiting for their screenshot or confirmation.

## Plan (24h of work) and status

| # | Step | Status |
|---|---|---|
| 0 | Sprite, connectors, public URL, Telegram webhook, sleep/wake | ✅ done |
| 1 | Riskiest piece: read real handwritten pages, crop each line, read amounts twice, match a real card | 🟡 card half done; notebook pages pending |
| 2 | Ledger (SQLite), add pages by photo in Telegram, confirm flagged amounts | — |
| 3 | Telugu relation words, family tree, "Is this them?" buttons, remember answers | — |
| 4 | Reply: cropped line, amount, year, function, "about ₹X today" (CPI-IW with linking factors, cited); gold by weight only | — |
| 5 | Sleep/wake hardening, dedupe, wake-time logging | — |
| 6 | Household split: new Sprite `giveback-<name>`, copy data, second bot, timed | — |
| 7 | Buffer (1h) | — |
| 8 | Blur/replace private details, 60–90 s video, README, submit, post tagging @flydotio (5h) | — |

## Infrastructure (as built)

- Fly org `srinivas-745`, Sprite **`giveback-bandaru`**, URL `https://giveback-bandaru-b6t3j.sprites.app` (auth: public).
- Connectors (Fly dashboard → Connectors), both granted to name prefix `giveback-`:
  - Google Gemini (user's own AI Studio key): `https://api.sprites.dev/v1/gateway/gemini/_etoR37tem2Nad0NLj32jQ/v1beta`
  - Anthropic (user's Console key, ~$4 credit): `https://api.sprites.dev/v1/gateway/anthropic/6cVOjdQxyUe2P1S92YnQxg/`
- Telegram bot **@giveback_bandaru_bot**; token at `~/.config/giveback/bot1.token` (local, mode 600). A second bot for the new household is still to be made (step 6).
- `scripts/deploy.sh <sprite> <token-file>` builds, pushes the binary and secrets, runs the `giveback` service on :8080, and sets the webhook with a secret token.
- Tools: Go at `~/.local/go/bin`, `sprite` CLI at `~/.local/bin`.

## Findings that differ from the docs or the brief

- **There is no Sprite fork.** No fork/clone/"new Sprite from checkpoint" anywhere in the CLI or API. The household split must be `sprite create` + copying files; describe it honestly as a copy, not a native fork.
- The dashboard has 59 connectors (Gemini, Anthropic, OpenAI, OpenRouter, Box, …), more than the docs list. All need your own key.
- **Sleep keeps the process in memory.** After 10+ minutes idle, `sprite info` showed `cold`, yet the same process (same uptime) answered the next message; `/proc/uptime` showed no reboot. Replies therefore do not pay a process restart.
- The Sprite's built-in docs say "services keep the sprite alive"; in practice it paused with the service running.
- `sprite file push` needs `-p`, or it wrongly reports "directory does not exist".
- Gemini free tier: `gemini-flash-latest` / `gemini-3.8-flash` often 503 ("high demand"); `gemini-2.5-flash` is closed to new users. `gemini-4-hydrogen` answered every time so far.
- Connector request bodies are limited to 8 MB, and the Gemini Files API is not forwarded: send images inline and shrink photos first.

## Step 1 results so far: reading the wedding card

Card: `/mnt/c/Users/Srinivas/Downloads/jayaprakash.jpeg` (a real card, kept out of git; real names and a phone number, so blur or replace it in the public video). `cmd/readcard` on the Sprite:

| | gemini-4-hydrogen | claude-haiku-5-5 (forced fallback) |
|---|---|---|
| Time | 16.5 s | 9.9 s |
| Hosts | ✅ Butham Muthyalu, Vimala (deceased) | ✅ same |
| Bride's parents kept apart | ✅ Puram Nagaiah, Ramana as `bride_parent` | ✅ |
| Errors | none seen | invented surname "Garla" for Pavan Kumar (గార్ల is the honorific "garu") |

**Open issue: 16.5 s is too slow for a snappy reply.** Next things to try: shrink the 3417×2530 photo to about 1600 px before sending, show "typing…" at once, and tell the family "Reading the card…" so the wait is visible.

## Next session: start here

1. **Notebook pages: done by hand.** The user wrote both pages (copy sheet: `docs/handwriting-sheet.html`). Photos in `testdata/notebook/` (page1, page1-rotated, page2); kept out of git until the user agrees to publish their handwriting. The answer key `docs/synthetic-notebook.md` matches what was written (row 1.1 changed to "ముత్యాలు భూతం").
2. Build the page reader: one request per page returns lines with text, amount digits, and a bounding box. Read amounts a second time with a different prompt and flag any disagreement. Crop each line with Go `image` (+ `golang.org/x/image/draw` for resizing). Score against `docs/synthetic-notebook.md`.
3. Matching card hosts to ledger lines: transliteration-aware fuzzy match (Telugu script and English), surname-first order, initials ("B." → Butham), relation words (మామ → mother's brother) resolved through the family tree; rank, then ask "Is this them?".
