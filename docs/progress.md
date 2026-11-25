# Giveback build progress

Handoff notes between build sessions. Read this first, then `/mnt/c/Users/Srinivas/Downloads/giveback.md` (the full brief: in scope, out of scope, rules).

**Deadline:** 2026-11-30 13:30 IST (midnight PST Nov 29). **Build window:** until 2026-11-27 00:00 IST; aim to submit Nov 26.

## Rules the user set (binding)

- Nothing from the brief's "Out of scope" list unless asked.
- Giveback never decides the gift amount, and never guesses silently: every answer shows the original handwritten line, and it asks "Is this them?" when unsure.
- Webhooks, not polling, so the Sprite can sleep.
- Models: **always `gemini-4-hydrogen`; fall back to `claude-haiku-5-5` only when Gemini fails.** Implemented in `internal/vision`.
- **Exception approved by the user (2026-11-25): read 2 (the amount check on line crops) uses `claude-haiku-5-5` directly.** Read 1 (the Telugu text) stays hydrogen-first. Reason: no Gemini billing is possible before Nov 30 (AI Studio asks ₹1000 prepayment), so hydrogen's free daily quota (resets 13:30 IST, ~25 page requests) must be saved. Implemented: `notebook.Models{Page: reader, Amounts: reader.Haiku()}`.
- **Free-tier discipline:** no live model calls during development (tests replay saved `read1.json` answers); never read the same photo twice (hash it); live calls only at checkpoints; record the demo just after a 13:30 IST reset.
- Go, chosen for fast startup.
- At step 8, or for any hackathon strategy question, check the hackathon-playbook MCP (its `submitting` and `pitching` checklists). Not needed while building steps 2–6.
- **Sessions are capped at 150k tokens of context.** Near the cap, stop at a clean point, update this file, commit and push, and give the user an exact prompt to continue in a new session.
- The user prefers going **step by step**, one action at a time, waiting for their screenshot or confirmation.

## Plan (24h of work) and status

Reordered 2026-11-25 after checking the hackathon playbook: get a thin end-to-end flow working in Telegram first, so we can demo at every checkpoint ("Can we demo right now?"), then deepen it. Nothing was added or dropped; steps 3–6 just come after the slice.

| # | Step | Status |
|---|---|---|
| 0 | Sprite, connectors, public URL, Telegram webhook, sleep/wake | ✅ done |
| 1 | Riskiest piece: read real handwritten pages, crop each line, read amounts twice, read a real card | ✅ (all-hydrogen re-run pending billing) |
| 2 | **Thin end-to-end slice:** send page photos to the bot → SQLite ledger with crops; forward a card → simple name match → reply with the cropped handwritten line, amount, function, year; flagged amounts shown as "please check". Demoable after this step. | ✅ works live (2026-11-25, read by the Haiku fallback: hydrogen was 503) |
| 3 | Better matching + "Is this them?": transliteration-aware fuzzy match, surname-first, initials, Telugu relation words via a family tree, Yes/No buttons, remember answers | — |
| 4 | "About ₹X today" (CPI-IW with linking factors, cited); gold by weight only; confirm flagged amounts in chat | — |
| 5 | Sleep/wake hardening, dedupe, wake-time logging; per-request token/cost logging | — |
| 6 | Household split: new Sprite `giveback-<name>`, copy data, second bot, timed | — |
| 7 | Buffer (1h). No new features after this point. | — |
| 8 | Submission (5h): blur/replace private details; 60–90 s video with voiceover + captions (CanVid on Windows), asleep→awake moment at the centre; architecture diagram + 4–6 annotated screenshots; GitHub README with install steps, name every Fly/Sprites technology in "How we built it", our Sprites findings as "what we learned"; submit early (aim Nov 26); post tagging @flydotio | — |

**Billing:** not possible before Nov 30 (AI Studio needs a ₹1000 prepayment). Stay on the Gemini free tier with the discipline in the rules above. Anthropic credit $4.29 is ample: Haiku 5.5 is $0.10/$0.50 per MTok, about $0.003 per page.

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

## Step 2: the thin slice (as built, 2026-11-25)

- `internal/ledger`: SQLite (`modernc.org/sqlite`, pure Go, so `CGO_ENABLED=0` still works) at `~/giveback/data/ledger.db` on the Sprite, and one JPEG per line under `data/crops/<page>/`. Pages are unique by photo SHA-256; both raw model answers are stored with each page. Telegram update IDs are stored too, so a re-delivery is dropped even after a restart.
- `internal/match`: simple first matcher. Normalised name words (lower case, titles like Sri/garu/గారు and initials dropped), compared within each script. 2+ shared words = "Found in the notebook"; 1 shared word = "Is this them? Only the name X matches." Same town ranks first. No transliteration yet (step 3), so an English-only card name will not find a Telugu-script notebook line.
- `internal/bot`: `/pages` → each photo is a page (or a caption containing "page"); `/done` ends it. Any other photo is an invitation card: hosts are read (`internal/card`), matched, and each hit is sent as the cropped handwritten line with function, date, line number, the line as read, the gift (₹ with Indian grouping, gold in sovereigns, items), and "⚠️ Please check the handwriting: …" for flagged lines. Plain text is a name lookup (no model call). Photos over 2000 px are shrunk first.
- `main.go`: all model calls go through `vision.Cached` under `data/cache/`. Work runs inside the webhook request (keeps the Sprite awake) with its own 4-minute context, so it finishes even if Telegram gives up waiting and re-delivers.
- Tests (`go test ./...`, no model calls): `internal/bot` TestThinSlice adds page1 + page2 from saved answers, checks a repeat photo and a re-delivered update cost 0 model calls, forwards a faked card for Kothapalli Venkata Ramana Reddy and gets both of his lines back as pictures.
- A page first read by the Haiku fallback is read again when its photo is sent again, replacing the old reading. Fallback answers are not cached, so hydrogen gets another chance. The log now says why Gemini was skipped.

**Live checkpoint, 2026-11-25 ~23:00 IST** (`/pages`, page1 + page2 as Files, `/done`, the jayaprakash card, "Ramana Reddy"):
- The whole flow worked in Telegram. Pages took ~25 s each (read 1 ~16 s + read 2 5–7 s), the card 27 s, a typed lookup 1.8 s.
- **Telegram delivers webhook updates one at a time, in order:** page 2 arrived only after page 1's reply (delivery lag 25 s, then 36 s). Nothing was re-delivered, and there was no timeout at 25 s.
- **Every read fell back to Haiku:** hydrogen answered 503 "high demand" (checked with one tiny call), not 429 quota. So the Telugu was Haiku-quality: page 2 line 1 read as "ఖూహం ముఖ్యులు - విమల", so the card's Butham Muthyalu was matched only on "విమల" (weak, asked about). The Chityal Muthyalu lines were correctly shown as "Is this them?", and "Ramana Reddy" found both of his lines as strong matches. Flags showed as intended (B. Narsimha 1116 vs 116).
- The ledger on the Sprite still holds these Haiku readings (18 lines). Re-sending page1/page2 when hydrogen is up replaces them.

## Next session: start here

1. **Notebook pages: done by hand.** The user wrote both pages (copy sheet: `docs/handwriting-sheet.html`). Photos in `testdata/notebook/` (page1, page1-rotated, page2); kept out of git until the user agrees to publish their handwriting. The answer key `docs/synthetic-notebook.md` matches what was written (row 1.1 changed to "ముత్యాలు భూతం").
2. Page reader built (above). Follow-ups done 2026-11-25: read 2 calls Haiku directly (`vision.Reader.Haiku()`); `vision.Cached` keeps every model answer on disk keyed by a hash of route + prompt + image bytes, so the same photo never reaches a model twice (`readpage` caches under `crops/cache/`); tests replay `testdata/replay/*.read1.json` (saved from run 4, Haiku read 1) through the real crop + flag pipeline with no model calls (skipped if the photos are absent). After the next 13:30 IST quota reset, run `readpage` once on all three photos for the final table.
3. Step 2 done (section above). When hydrogen is not 503 (try one tiny call first), re-send page1 and page2 to the bot in `/pages` mode to replace the Haiku readings, then forward the card again. Log: `sprite exec -s giveback-bandaru -- tail -f /.sprite/logs/services/giveback.log`.
4. Next: step 3 (better matching + "Is this them?" Yes/No buttons, remember answers). It must handle the card's English-only names against Telugu-script lines (transliteration), given-name-first order, initials, and relation words.
