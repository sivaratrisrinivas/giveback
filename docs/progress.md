# Giveback build progress

Handoff notes between build sessions. Read this first, then `/mnt/c/Users/Srinivas/Downloads/giveback.md` (the full brief: in scope, out of scope, rules).

**Deadline:** 2026-11-30 13:30 IST (midnight PST Nov 29). **Build window:** until 2026-11-27 00:00 IST; aim to submit Nov 26.

## Rules the user set (binding)

- Nothing from the brief's "Out of scope" list unless asked.
- Giveback never decides the gift amount, and never guesses silently: every answer shows the original handwritten line, and it asks "Is this them?" when unsure.
- Webhooks, not polling, so the Sprite can sleep.
- Models: **always `gemini-4-hydrogen`; fall back to `claude-haiku-5-5` only when Gemini fails.** Implemented in `internal/vision`.
- Go, chosen for fast startup.
- **Sessions are capped at 150k tokens of context.** Near the cap, stop at a clean point, update this file, commit and push, and give the user an exact prompt to continue in a new session.
- The user prefers going **step by step**, one action at a time, waiting for their screenshot or confirmation.

## Plan (24h of work) and status

Reordered 2026-11-25 after checking the hackathon playbook: get a thin end-to-end flow working in Telegram first, so we can demo at every checkpoint ("Can we demo right now?"), then deepen it. Nothing was added or dropped; steps 3–6 just come after the slice.

| # | Step | Status |
|---|---|---|
| 0 | Sprite, connectors, public URL, Telegram webhook, sleep/wake | ✅ done |
| 1 | Riskiest piece: read real handwritten pages, crop each line, read amounts twice, read a real card | ✅ (all-hydrogen re-run pending billing) |
| 2 | **Thin end-to-end slice:** send page photos to the bot → SQLite ledger with crops; forward a card → simple name match → reply with the cropped handwritten line, amount, function, year; flagged amounts shown as "please check". Demoable after this step. | — next |
| 3 | Better matching + "Is this them?": transliteration-aware fuzzy match, surname-first, initials, Telugu relation words via a family tree, Yes/No buttons, remember answers | — |
| 4 | "About ₹X today" (CPI-IW with linking factors, cited); gold by weight only; confirm flagged amounts in chat | — |
| 5 | Sleep/wake hardening, dedupe, wake-time logging; per-request token/cost logging | — |
| 6 | Household split: new Sprite `giveback-<name>`, copy data, second bot, timed | — |
| 7 | Buffer (1h). No new features after this point. | — |
| 8 | Submission (5h): blur/replace private details; 60–90 s video with voiceover + captions (CanVid on Windows), asleep→awake moment at the centre; architecture diagram + 4–6 annotated screenshots; GitHub README with install steps, name every Fly/Sprites technology in "How we built it", our Sprites findings as "what we learned"; submit early (aim Nov 26); post tagging @flydotio | — |

**Billing:** the user is turning on billing for the AI Studio key (~$10, budget alert $10) on the night of Nov 25. Estimated cost: a page $0.03–0.08, a card $0.02–0.04 (unmeasured; step 5 adds logging). Until then Gemini free quota may be exhausted and Haiku (~$4 credit) covers.

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
- **Gemini free tier has a small daily quota for `gemini-4-hydrogen`.** About 25 page-sized requests in ~30 minutes of testing used it up: after that every call got 429 "You exceeded your current quota" with no `retryDelay`, for 10+ minutes. Free-tier daily quotas reset at midnight Pacific (13:30 IST). This affects the demo: either enable billing on the AI Studio key or keep test runs few. `internal/vision` now retries Gemini once on 503, or on a 429 that asks for ≤ 20 s, and only then falls back to Haiku.
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

## Step 1 results: reading the notebook pages

`internal/notebook` + `cmd/readpage` (run on the Sprite: `./readpage notebook/page1.jpeg notebook/page1-rotated.jpeg notebook/page2.jpeg`; it saves crops and `read1.json` under `crops/`, prints a score table against `docs/synthetic-notebook.md`, which it parses directly).

How it works:
1. **Read 1**, one request per page: heading, date, rotation, and per line the text, name, place, amount digits, gold sovereigns, item, an `unclear` bit, and a box (0–1000).
2. **Sideways photo → turn it and read again.** Models place boxes and read far better upright, and the turn they claimed is checked. Costs one extra request, only for sideways photos.
3. **Crops are snapped to the ink.** Model boxes are loose (even hydrogen's clipped "1,116/" and spilled into the next line), so a box only says roughly where a line is: cut at the emptiest pixel row between neighbouring lines, and widen sideways until the ink stops. Faint show-through falls below the ink threshold.
4. **Read 2**, one request per page with all the crops: "list every digit of the money at the end of the line". Any disagreement with read 1 (cash or gold), an `unclear` line, or a missing crop flags the line.

| Photo | Run | Models (read 1 / 2) | Text CER | Amounts right | Flagged (false alarms) | Wrong & not flagged |
|---|---|---|---|---|---|---|
| page1 | 2 | hydrogen / haiku | 0.8% (చామల → చావల) | 10/10 | 1 (1) | 0 |
| page2 | 2 | hydrogen / haiku | 0.0% | 8/8 | 1 (1) | 0 |
| page1 | 4 (final code) | haiku / haiku | 25.1% | 10/10 | 6 (6) | 0 |
| page1-rotated | 4 (final code) | haiku / haiku | 9.3% | 10/10 | 4 (4) | 0 |
| page2 | 4 (final code) | haiku / haiku | 16.1% | 7/8 | 3 (2) | 0 |

- **0 wrong amounts went unflagged in any run** (4 runs, 3 photos each). The one wrong amount (Haiku, 2.5 "2 కాసులు" read as 25) was caught by read 2.
- Hydrogen reads the Telugu almost perfectly; Haiku invents Telugu names (మల్లేశం for ముత్యాలు) but marks them `unclear`, so they are flagged.
- Read 2 by Haiku keeps reading 1.5 "1116" as "116": a false alarm, not a miss. Hydrogen read 2 (run 1, page2) agreed on every line.
- Times: hydrogen read 1 13–18 s per page; Haiku 13–33 s. Not on the reply path (pages are added once).
- Not yet seen: **hydrogen on the turned photo with the final code** (quota ran out). Re-run `readpage` once after the quota resets.

## Next session: start here

1. **Notebook pages: done by hand.** The user wrote both pages (copy sheet: `docs/handwriting-sheet.html`). Photos in `testdata/notebook/` (page1, page1-rotated, page2); kept out of git until the user agrees to publish their handwriting. The answer key `docs/synthetic-notebook.md` matches what was written (row 1.1 changed to "ముత్యాలు భూతం").
2. Page reader built (above). Once billing is on: re-run `readpage` on the Sprite for an all-hydrogen table, and update this file.
3. Build step 2, the thin slice (plan table). Keep matching simple for now (normalised name/surname overlap); the full matcher is step 3.
