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
| 3 | Better matching + "Is this them?": transliteration-aware fuzzy match, surname-first, initials, Telugu relation words via a family tree, Yes/No buttons, remember answers | ✅ works live (2026-11-26 00:14 IST): `/family`, Yes/No buttons, remembered answers. Relation-word demo waits for a hydrogen re-read of the pages |
| 4 | "About ₹X today" (CPI-IW with linking factors, cited); gold by weight only; confirm flagged amounts in chat | ✅ works live (2026-11-26 ~00:56 IST): values with source, flagged amount confirmed by typing, gold corrected by weight |
| 5 | Sleep/wake hardening, dedupe, wake-time logging; per-request token/cost logging | ✅ works live (2026-11-26 ~01:30 IST): wake logged, per-call cost logged, `/usage`, graceful restart |
| 6 | Household split: new Sprite `giveback-<name>`, copy data, second bot, timed | ✅ works live (2026-11-26 ~03:51 IST): split in 54 s, `/start` in both bots tells the story |
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

## Step 3: matching and "Is this them?" (as built, 2026-11-25)

- `internal/match/translit.go`: Telugu script is romanised (`Roman`), then any spelling goes to a sound key (`Key`): no long vowels, no aspiration (th→t, bh→b), doubled letters once, -aiah/-ayya alike, final y→i, m before a consonant → n. Butham = Bhootham = భూతం, Reddy = రెడ్డి, Nagaiah = నాగయ్య. `SameName` also ignores an "a" between consonants (Narsimha = నరసింహ) and allows one slip in names of 6+ letters (Muthyalu/Mutyala).
- `internal/match/match.go` (`Matcher.Find`):
  - Word order doesn't matter, so given-name-first ముత్యాలు భూతం matches the card's Butham Muthyalu.
  - Initials (K., K.V., కె) support a match but never make one. A one-word match whose initial fits nobody on the card is dropped.
  - Sharing only the surname with a line that names someone else is dropped (Butham Narsimha vs ముత్యాలు భూతం).
  - The card's separate `surname` field counts as a name word, so "Vimala", surname Butham, strongly matches "భూతం ముత్యాలు - విమల".
  - Words in the line's text can add a match the name field missed. They never count against one.
  - Strengths: Confirmed (family said Yes) > Strong (2+ name words, or a linked relation word) > Weak (everything else, always asked). Lines the family said No to are hidden, and the reply says how many.
- `internal/match/kin.go`: 19 Telugu relation words, each with its meaning and whose surname that relative carries (father's side or mother's maiden side). Two-part spellings like "పెద్ద నాన్న" work too. A bare relation line (బాబాయి) is tied to a person only through the family tree:
  - `/family father = Bandaru Subba Rao` and `/family mother = Butham Lakshmi` (maiden name, surname first). A card host with surname Bandaru then gets an "Is this Bandaru Ramulu? The line says బాబాయి (father's younger brother), and Bandaru is your father's surname." question.
  - Yes on a bare relation line saves `బాబాయి = Bandaru Ramulu`. From then on every బాబాయి line is his (shown as "as you told me"), and it stops coming up for other Bandarus.
  - `/family` shows the tree; `/family babai = …` sets or (left empty) forgets a link.
  - Typing a relation word ("babai", "Vimala atha") finds those lines.
- `internal/bot`: Weak hits get "✅ Yes, it's them" / "❌ No" inline buttons with data `y:<askID>`. A tap is stored in `answers`, keyed by `match.PersonKey` (sorted sound keys, so the same person in any script or order). Then the buttons are removed and a "Noted: …" message follows.
- `internal/ledger`: new tables `family`, `asks`, `answers`. Re-reading a page (fallback → hydrogen) deletes the answers about its old lines, because line IDs change.
- Tests (no model calls): the answer key's 18 rows as written, plus cross-script pairs, initials, relation via family tree, and answers. `TestThinSlice` now taps No/Yes and checks the answers are remembered. `TestRelationAskAndRemember` covers `/family`, the relation question and the learned link.
- Seen on the current live ledger (Haiku readings): the real card's hosts Butham Muthyalu and Vimala match only "Muthyalu, Chityal" (asked) and "విమల అత్త" (asked, with the relation reason). His own lines were misread by Haiku (మల్లేశం పురం, ఫాతిం మున్నాలు), so **re-reading the pages with hydrogen is what makes the card demo work.**

**Live checkpoint, 2026-11-26 ~00:10 IST** (deployed 72dace4 + the `/family` multi-line fix):
- A message with both `/family` lines was first saved as one father name (bug). It is fixed: each line is handled on its own, and there's a test for it. The family tree on the Sprite is now right (father Bandaru Subba Rao, mother Butham Lakshmi).
- Typed "Muthyalu" → two "Is this Muthyalu?" photos with ✅/❌ buttons. No on line 1.10 and Yes on 2.8 → buttons removed, "Noted: …" replies. Typed again → only 2.8, "Found in the notebook (you confirmed this is Muthyalu)", plus "(1 line(s) you told me aren't them are left out.)". Replies took ~1 s.
- "Bandaru Ramulu" found nothing, correctly: **the live ledger's Haiku readings (a different run from `testdata/replay`) have no బాబాయి at all.** Line 1.7 was read as "పురుషోత్తం", 1.9 as "ఎల్లి", 1.3 as "బాబు నాన్న", and Muthyalu's own lines 1.1/2.1 as "మెట్లజీ పురాకం" and "ఖూహం ముఖ్యులు - విమల". Hydrogen still answered 503 at 00:10 IST.
- These test answers are dropped when the pages are re-read (line IDs change), which is fine.

## Step 4: today's value, gold, confirming amounts (as built, 2026-11-26)

- `internal/worth`: official CPI-IW only, copied as published, nothing estimated.
  - Yearly averages (April–March) 1993-94 … 2025-26: RBI Handbook of Statistics on Indian Economy 2025-26, **Table 34** "Consumer Price Index - Annual Average" (dated Jul 31, 2026), https://www.rbi.org.in/Scripts/PublicationsView.aspx?id=23859. Bases as printed: 1982=100 up to 2004-05 (so 1993-94 = 258, 2003-04 = 500), 2001=100 for 2005-06 … 2019-20, 2016=100 from 2020-21.
  - Linking factors from that table's notes: 4.63 (2001 → 1982 series) and 2.88 (2016 → 2001). old = new × factor, so a 1982-base figure ÷ (4.63 × 2.88) is on base 2016.
  - Today: **Aug 2026 = 154.4** (base 2016=100), the latest month on https://labourbureau.gov.in/CPI (its chart; RBI Table 163 agrees up to May 2026 = 150.8). Update `TodayIndex`/`TodayMonth` when a newer month is out.
  - The page date picks the year: 12-2-1994 → 1993-94 (₹116 → about ₹930, ×8.0); 8-6-2003 → 2003-04 (₹1,116 → about ₹4,600). Only a year written → the April year, and the reply says so. No clear four-digit year, or before 1993-94 → no value, says why. Rounded to 2 significant figures ("about").
  - Fetching notes: rbidocs.rbi.org.in (the XLSX/PDF) is behind a bot check; the www.rbi.org.in `PublicationsView.aspx?id=…` pages show the same table as HTML. The Labour Bureau's own monthly search covers only 2006 onward.
- Replies: under "Gave:" a line "That's about ₹X in today's money (prices are N× those of 1993-94)." and a one-line source (Labour Bureau, RBI Table 34, the then-index and base, the linking factors, Aug 2026 154.4).
- Gold is shown by weight only: "2 sovereigns of gold (16 g)" (1 sovereign = 8 g) and "Gold is shown by weight only, never turned into rupees."
- Flagged lines get **no** today's value ("I'll work it out once you confirm the amount"), and buttons on their own row: one per reading (read 1, and read 2 when they disagree, e.g. "✅ ₹1,116" / "✅ ₹116"), plus "✏️ Something else", which asks the family to type it ("1116", "Rs 1,116/-", "2 sovereigns", "1 kasu", "0", "cancel"). Confirming stores the gift, clears the flags (`lines.checked = 1`), and replies with today's value. Later replies say "(you checked this)". Callback data `c:<line>:<digits>:<gold>` or `c:<line>:x`; typing mode is chat mode `fix:<line>`.
- `internal/ledger`: new columns `second_digits`, `second_gold`, `checked`, added to old ledgers on open (tested). Lines read before this have no second reading stored, so they offer only read 1 + "Something else". Re-reading a page drops its confirmations, like answers.
- `internal/telegram`: `telegram.NewRow` between buttons starts a new row.
- **Live checkpoint, 2026-11-26 00:29–00:57 IST** (on the Haiku-read ledger, no model calls):
  - "Ramana Reddy": ₹116 (12-2-1994) → about ₹930 (8.0×), ₹516 (8-6-2003) → about ₹2,100 (4.1×), each with its source line.
  - "Butham Narsimha": the flagged 1,116/116 line showed no value and the buttons "✅ ₹1,116 / ✏️ Something else" under the Yes/No row; typed 1116 → "gave ₹1,116 … about ₹8,900".
  - "బావబావు" (Haiku's misreading of బాబాయి, with 2 కాసులు read as 25): "25 sovereigns of gold (200 g)", no rupees; "Something else" + "2 sovereigns" → corrected to 16 g.
  - Fixed after: a second tap on "Something else" repeated the prompt (the button stays visible). It now just shows "Type it in the message box".
  - Side note (step 3 matcher, not changed): typing only "Ramana Reddy" gives "Is this them?" because the line's initial K. is not in what was typed; a card with the full name matches strongly.
- Tests (no model calls): `internal/worth` (each base, year-only, no-guess cases, cite text), `TestTodayValueInCaption`, `TestConfirmFlaggedAmount` (tap a reading, then "Something else" + typed gold), `TestParseGift`, `TestMigrateOldLedger`.

## Step 5: sleep/wake, dedupe, logging (as built, 2026-11-26)

- `internal/wake`: a sleeping Sprite freezes the process, so the program only sees time jump. A 1 s ticker (and every request) checks; a jump of 5 s+ is logged as `wake: the Sprite slept for …` with both wall and monotonic clocks (we don't yet know if the monotonic clock counts a Sprite sleep: the live log will tell). The first request after a wake logs `first request after the Sprite woke …` and `woken by this message; from sending it to the reply: about Ns (delivery Ns + handling …)`. `GET /` shows "process up …, slept N time(s) …; the last sleep lasted … and ended … ago".
  - Go stamps a ticker tick with when it was *due*, which after a freeze is a time from before it; the watcher reads `time.Now()` instead.
  - Tested locally by freezing the process with `kill -STOP` for 8 s: detected as "slept 9s" on both clocks.
- `internal/vision/meter.go`: every model attempt, failed Gemini tries included, is a `Call` (model, ok/error, time, tokens in/out/thinking, cost) and gets a log line `model call: …`. Cost: Haiku 5.5 $0.10/$0.50 per MTok; Gemini $0 (free tier), tokens still counted. Calls are collected per update (`vision.WithMeter`), summed in one log line `update N: K model call(s) (F failed), C answer(s) from cache, tokens …, cost $…`, and stored in the new `calls` table. Cache hits are counted, not stored.
- `/usage` (not in the family help): per model, last 24 h and since the start: calls, failed, tokens, cost, average seconds; and the Sprite's wake status. For the README's cost numbers.
- Hardening: updates are now `Claim`ed (done = 0) and `Finish`ed after a successful reply. A re-delivery of an update whose try never finished (process restarted mid-reply) is handled once more (max 2 tries); finished ones are dropped as before. Old `updates` rows count as done (migration). Graceful shutdown on SIGTERM (deploy restarts) waits up to 2 min for a reply in progress. "typing…" is re-sent every 4 s while a model reads (Telegram clears it after ~5 s).
- Dedupe: `ledger.SamePage` spots another photo of a page already stored (different bytes, so a different hash): ≥ 3 lines, and ≥ 60% of its lines with cash or gold, have the same line number and amount. Then the page is not added again ("I already have this page"), unless the stored one was read by the Haiku fallback and the new one by hydrogen: then the new reading replaces it. (Photos of the same bytes were already caught by hash, and model answers by the cache.)
- **A forgotten `/done` swallowed a card** (found live): the chat was still in `/pages` mode from last session, so the forwarded card was "Added: Wedding Invitation … 0 lines". Fixed: a photo with no gift lines is never stored as a page ("I found no gift lines… send /done and forward it again"), and `/pages` mode expires after 30 minutes without a page (`chats.set_at`; an old row without a time counts as expired). The empty page 4 was deleted from the live ledger by hand.
- Tests (no model calls): fake Gemini/Claude servers for token and cost counting incl. a fallback and a cache hit; empty page not stored and `/pages` expiry (`TestForgottenPagesMode`); claim/finish/retry; usage sums; old-ledger migration of `updates`; a second and third photo of page 1 (fallback reading replaced, then "already have"); `/usage` text; wake detection with injected times. `go test -race ./...` passes.

**Live checkpoint, 2026-11-26 01:19–01:30 IST:**
- **The Sprite sleeps after only ~30 s–2 min idle** (not 10+ min as noted in step 0): `wake: the Sprite slept for 32s … 1m13s … 1m58s`. **The wall and monotonic clocks both count the sleep.**
- Waking costs nothing visible: "Ramana Reddy" woke it with **delivery lag 0 s**; the process ran again 3 ms before the request arrived; reply in 2 s. A card on wake: 17 s, all model time (two hydrogen 503s of ~4 s each + a 2 s wait + Haiku).
- Costs seen (`/usage` and logs): the card read as a page by Haiku 2,261 in / 440 out = $0.00045; the card read as a card by Haiku 2,150 in / 2,081 out = **$0.0013 per card**. Failed hydrogen tries are logged with 0 tokens.
- Deploy restart: `shutting down: finishing requests in progress` → `stopped` (graceful).
- Hydrogen: 503 at 01:05, 01:18 and 01:30 IST, and **429 "You exceeded your current quota" at 01:27 IST**, though almost every call tonight failed (503 tries may count against the free quota). So wait for the 13:30 IST reset before re-reading pages.

## Step 6: household split (as built, 2026-11-26)

- `ledger.CopyFor(dir, from, newName)` (`internal/ledger/household.go`): copies the database with SQLite `VACUUM INTO`, a consistent snapshot that's safe while the bot runs, then copies `crops/` and `cache/`. In the copy it clears `updates` (**Telegram update IDs are per bot: an old ID would make the new bot silently drop a new message**), `chats`, `asks`, `calls` and `copies`, and writes a `household` row (name, copied from, when, pages and lines carried). The old ledger gets its name, if it had none, and a `copies` row. Kept: pages, lines, crops, the family tree, Yes/No answers, confirmed amounts, and the model cache (the same photo is never read twice in either home).
- `giveback copy-for <out-dir> <this household> <new household>` runs it (main.go).
- `/start` and `/help` begin with "📒 This is the Ravi household's notebook. It began on 26 Nov 2026 as a copy of the Bandaru household's notebook (2 page(s), 18 line(s)); since then each household keeps its own." The old one says "• On … the Ravi household got its own copy (…)".
- `scripts/split.sh <from-sprite> <from-household> <new-household> <new-bot-token-file>`: builds (not timed), then times each phase: 1) `sprite create --skip-console giveback-<name>` + public URL, 2) push the binary to the old Sprite's /tmp, run copy-for, tar it, 3) pull, push and unpack it into the new Sprite's `~/giveback/data`, 4) `deploy.sh` (binary, service, webhook; `GIVEBACK_BIN` skips the second build), 5) wait for `GET /`. The connectors are granted to the `giveback-` prefix, so the new Sprite reaches them with no extra step (to be confirmed live).
- Tests (no model calls): `TestCopyForNewHousehold` (what is and isn't carried, update 7 is new in the copy, the two diverge, errors for an existing dir or same name) and `TestHouseholdNoteAfterSplit`.
- **Live split, 2026-11-26 ~03:34 IST** (`scripts/split.sh giveback-bandaru Bandaru Ravi ~/.config/giveback/bot2.token`, bot **@giveback_ravi_bot**, Sprite **`giveback-ravi`**, `https://giveback-ravi-b6t3j.sprites.app`): **54.4 s in all**: Sprite created 5.0 s (the create itself 1.7 s), snapshot 10.0 s (copy-for 29 ms, 132 KB tgz; the rest is waking the old Sprite and pushing the binary), moved 11.2 s, bot running + webhook 25.0 s, answering at its URL 3.1 s. The copy carries the Haiku readings (2 pages, 18 lines).
- Checked in Telegram: Ravi's `/start` begins "📒 This is the Ravi household's notebook. It began on 26 Nov 2026 as a copy of the Bandaru household's notebook (2 page(s), 18 line(s))…"; "Ramana Reddy" there returned both line pictures with today's values (no model call). Bandaru's `/start` (after deploying the step 6 code there: split.sh runs copy-for with its own temporary binary, so the old bot needs a deploy to *show* the note) says "• On 26 Nov 2026 the Ravi household got its own copy (2 page(s), 18 line(s))."
- Copying again for the same household name replaces its `copies` record (for the filmed re-split).
- **The connector grant by name prefix covers a new Sprite at once:** a tiny Haiku call from `giveback-ravi` answered 200 with no dashboard step.
- Dry run on a local pull of the live ledger: `copied 2 page(s), 18 line(s) … in 9ms`, 236 KB. Pulling the live data dir took 24 s (the Sprite woke).

## Live checkpoint 7a, 2026-11-26 23:19–23:38 IST (hydrogen page readings)

- Tiny hydrogen call: first try hung 40 s (curl timeout), second answered 200 in 11 s.
- `/pages`, page1 as File: "read by my backup reader. Reading it again…" → **hydrogen read 1 in 17.3 s**, Haiku read 2 5.1 s; heading పద్మ పెళ్ళి right; 1 flag (B. Narsimha 1116/116, the known false alarm). page2: hydrogen 503 once, retried, **hydrogen 14.8 s**; 1 flag: Haiku read 2 saw "2 కాసులు" as ₹250 (false alarm, safe side). Live ledger is now hydrogen-read (ledger pages 3 and 4, 18 lines).
- "Bandaru Ramulu": four "Is this Bandaru Ramulu?" questions (పెద్దనాన్న 1.3, బాబాయి 1.7 and 2.5, పిన్ని 1.9), each with the father's-surname reason. Yes on 1.7, No on 1.3 and 1.9, ✅ 2 sovereigns on 2.5 → typed again: 1.7 "you confirmed", 2.5 "బాబాయి is Bandaru Ramulu, as you told me … (you checked this)", "(2 line(s) you told me aren't them are left out.)".
- Jayaprakash card: **Butham Muthyalu strongly matched 1.1 (₹516 → about ₹4,100) and 2.1 (₹1,116 → about ₹4,600)**; Muthyalu, Chityal 1.10/2.8 asked, not merged; Vimala atha 2.3 asked with the mother's-maiden-surname reason.
- Problems seen: (1) **the card took 62 s**: hydrogen hung to its 45 s `PrimaryTimeout`, then Haiku read it in 8.5 s; Telegram re-delivered at 60 s and the dedupe dropped it. Proposed fix: PrimaryTimeout 25 s (hydrogen reads took 10–18 s). (2) The crop for 2.3 (విమల అత్త) shows only "– ల". (3) Tapping an amount button also removes the Yes/No buttons of that message.
- Finding: after a 19 h idle, the wake log said wall clock 19h23m but **monotonic 10m4s** (short sleeps count on both clocks), so a long sleep is not seen by the monotonic clock.
- `readpage` final run (pages 1 and 2 replayed from the bot's hydrogen cache, copied into `~/readpage/crops/cache`):

| Photo | Read 1 / 2 | Text CER | Amounts right | Flagged (false alarms) | Wrong & not flagged |
|---|---|---|---|---|---|
| page1 | hydrogen / haiku | 1.4% | 10/10 | 1 (1) | 0 |
| page2 | hydrogen / haiku | 0.0% | 8/8 | 1 (1) | 0 |
| page1-rotated | haiku / haiku (hydrogen 503 twice) | 12.1% | 10/10 | 3 (3) | 0 |

## 7b, 2026-11-26 23:40–23:50 IST: timeout fix and the filmed re-split

- Hydrogen `PrimaryTimeout` 45 s → 25 s (83270eb), deployed to giveback-bandaru.
- With the user's OK: `sprite destroy --force giveback-ravi`, then `scripts/split.sh giveback-bandaru Bandaru Ravi ~/.config/giveback/bot2.token`: **57.0 s** (created 4.3 s, snapshot 8.9 s with copy-for 32 ms / 136 KB, moved 11.8 s, bot + webhook 28.5 s, answering 3.4 s).
- @giveback_ravi_bot: `/start` tells the copy story; "Bandaru Ramulu" → 1.7 "you confirmed", 2.5 "as you told me … (you checked this)", 2 left out. **The family's answers travel with the copy, not just the pages.**

## Next session: start here

1. **Notebook pages: done by hand.** The user wrote both pages (copy sheet: `docs/handwriting-sheet.html`). Photos in `testdata/notebook/` (page1, page1-rotated, page2); kept out of git until the user agrees to publish their handwriting. The answer key `docs/synthetic-notebook.md` matches what was written (row 1.1 changed to "ముత్యాలు భూతం").
2. Page reader built (above). Follow-ups done 2026-11-25: read 2 calls Haiku directly (`vision.Reader.Haiku()`); `vision.Cached` keeps every model answer on disk keyed by a hash of route + prompt + image bytes, so the same photo never reaches a model twice (`readpage` caches under `crops/cache/`); tests replay `testdata/replay/*.read1.json` (saved from run 4, Haiku read 1) through the real crop + flag pipeline with no model calls (skipped if the photos are absent). After the next 13:30 IST quota reset, run `readpage` once on all three photos for the final table.
3. Step 2 done (section above). When hydrogen is not 503 (try one tiny call first), re-send page1 and page2 to the bot in `/pages` mode to replace the Haiku readings, then forward the card again. Log: `sprite exec -s giveback-bandaru -- tail -f /.sprite/logs/services/giveback.log`.
4. Step 3 works live (section above). **Still to show, once hydrogen is up:** deploy (`scripts/deploy.sh giveback-bandaru ~/.config/giveback/bot1.token`). If hydrogen is up (one tiny call first), re-send page1 + page2 in `/pages` mode. The family tree is already set. Type "Bandaru Ramulu" (expect "Is this Bandaru Ramulu? The line says బాబాయి …" with buttons), forward the jayaprakash card, tap Yes/No, and forward it again to see the answers remembered.
5. Step 4 works live (section above). **Hydrogen is on and off:** 503 at 00:17 IST on Nov 26, "ok" at 00:58, then 503 on page 1 at 01:01 (the bot retried once, then Haiku re-read it: page 1 is now ledger page 3, still Haiku), and 503 twice more at 01:03. Page 2 was not re-sent. So a tiny call succeeding doesn't mean the page will: re-send right after one, and expect to retry. Each failed try costs one Haiku re-read (~$0.003). Re-reading the pages with hydrogen drops the live confirmations (line IDs change), which is fine.
6. Step 5 works live (sections above). `/usage` shows model costs and the Sprite's sleeps; `GET /` shows the wake status.
7. **Next session, in order:**
   a. ✅ **Done 2026-11-26 23:38 IST** (section above). Was: **After 13:30 IST (quota reset)**, one tiny hydrogen call from the Sprite (`sprite exec -s giveback-bandaru --no-stdin -- sh -c 'curl -s -m 40 -o /dev/null -w "%{http_code}\n" -H "Content-Type: application/json" -d "{\"contents\":[{\"parts\":[{\"text\":\"Say ok\"}]}]}" https://api.sprites.dev/v1/gateway/gemini/_etoR37tem2Nad0NLj32jQ/v1beta/models/gemini-4-hydrogen:generateContent'`). If 200: user sends `/pages`, page1 (as File), check the log says `read 1 gemini-4-hydrogen` (or `model call: gemini-4-hydrogen ok`), then page2, then `/done`. A new photo of a Haiku-read page now replaces it too (same-page dedupe). Then "Bandaru Ramulu", then forward the jayaprakash card. Also run `readpage` once on the three photos for the final step 1 table if quota allows (it costs ~4–6 hydrogen calls; the pages come first).
   b. ✅ **Done 2026-11-26 23:50 IST** (section above). Was: Step 6 works live (section above). The current `giveback-ravi` carries the Haiku readings. **After 7a**, for the filmed split: ask the user before `sprite destroy giveback-ravi`, then `scripts/split.sh giveback-bandaru Bandaru Ravi ~/.config/giveback/bot2.token` again (bot2.token is saved; @giveback_ravi_bot) so the copy carries the hydrogen readings. Show the Sprite asleep (`sprite info` → cold) before forwarding the card.
   c. **Step 8 status (2026-11-27 00:05 IST):** README written (af92259). Step 7: known cosmetic bugs left alone on purpose (2.3 crop shows "– ల"; tapping an amount button removes that message's Yes/No buttons). User decisions: the real jayaprakash card may appear with the **phone number and every name except the matched hosts blurred**; the handwritten page photos are **published** in `testdata/notebook/` (no EXIF). Still to do: record the 60–90 s video (CanVid; asleep → card → wake → "Is this them?" → crop + ₹ then/now → split 57 s), 4–6 annotated screenshots into `docs/screenshots/` and the README's Demo section, submit on the Hacksgiving page, post tagging @flydotio.
   **Video prep done (2026-11-27 ~03:20 IST):** బాబాయి answers (lines 21/25/27) and the `బాబాయి` family link deleted from giveback-bandaru for filming (backup `~/giveback/data/ledger.before-video.db` on the Sprite); giveback-ravi destroyed (the user ran it; the auto-mode classifier blocks `sprite destroy` for Claude), so shot 9 runs `split.sh` live. Card with groom/bride/bride's parents/address/phone/well-wishers covered: `C:\Users\Srinivas\Downloads\jayaprakash-blurred.jpg` (hosts, date, venue kept). Shot list: 11 shots, ~93 s, recorded in Cap (Studio mode), in this conversation's last messages; order: page photos → `sprite info` cold → forward card → crop + ₹4,100 → ❌ Chityal Muthyalu → "Bandaru Ramulu" ✅ → split.sh 57 s → Ravi `/start` + "Bandaru Ramulu" → GitHub.
   **Video first cut (2026-11-27 evening IST):** all shots recorded (Cap, Telegram Web + terminal) and the voiceover recorded as one file, split per line. Stitched with `C:\Users\Srinivas\Videos\giveback-shots\work\build.py` (ffmpeg: chat panel cropped to hide other chats, waits sped up and tagged "sped up", Nirmala UI captions incl. బాబాయి, end card) → `C:\Users\Srinivas\Videos\giveback-shots\giveback-demo.mp4`, 101 s. The filmed split re-created giveback-ravi (57.2 s total) and the బాబాయి answer was re-confirmed on bandaru on camera. Next: user review of the cut, then screenshots, README Demo, submit, X post.
   **Animations (user said "make both", style chosen: anidoodle Ballpoint sketch / `pocketWatch`):** brief at `C:\Users\Srinivas\Videos\giveback-shots\work\anim\brief.md` (build notes appended).
   **Built 2026-11-27 evening IST:** user approved the look still (`work\anim\look-still.png`). Source: `~/art/giveback-anim` (anidoodle 0.10.1 scaffold; `src/canvas-core/bp.ts` biro kit, `gbScene1.ts` mandapam, `gbScenes.ts` scenes 2–5 + split, `opener.ts` cue table). Headless Chromium needs `export LD_LIBRARY_PATH=$HOME/art/libs/root/usr/lib/x86_64-linux-gnu` (nss libs unpacked without sudo). Render: `node tools/render.mjs opener|split --out out/x.mp4`, then copy to `work\anim\clipA-opener.mp4` (21.6 s, 648 f) and `clipB-split.mp4` (5.0 s, 150 f). Both 1920x1080, 30 fps, silent, no text; gate 14/14 each (opener declared `kind: "drawing"`: each scene draws itself in biro). No fresh-critic subagent review was run. Re-rendered `giveback-demo.mp4`: 101.5 s (shots 1–2 13.5 + 8.1 s, shot 9 10.9 s). User asked for real words in the notebook: the close-ups (writing, finger, fading) now show page 1 of `docs/synthetic-notebook.md` in biro (heading పద్మ పెళ్ళి 12-2-1994; బాబాయి Rs 216/- written live; finger + tick on ముత్యాలు భూతం Rs 516/-). Fonts: Kalam (Latin, handwritten) then Mallanna (Telugu), both OFL from google/fonts, embedded as base64 in `assets/fonts/kalam.ts`/`mallanna.ts`. Tiny table notebook and flipping pages stay scribbles. Re-rendered, still 101.5 s. **Audio polish (user asked):** `build.py` voice chain now uses ffmpeg `arnndn` with the RNNoise `bd` model (`work\rnnn\`, from github.com/richardpl/arnndn-models) + rumble cut, de-box, 3.2 kHz presence, de-ess, gentle compression: speech-to-pause gap over all 11 lines went from worst 18.7 / mean 27.2 dB to 28.4 / 44.6 dB. Music bed "Carefree" by Kevin MacLeod (CC BY 4.0, `work\music\Carefree.mp3`), -10 dB, sidechain-ducked under the voice, fades; credit in README `## Credits`. Final 101.5 s, -15.9 LUFS, peak -1.5 dBFS. Next: user watches the cut, then screenshots, README Demo, submit, X post.
   `build.py` (backup `build.before-anim.py`): shots 1–2 take clip A, cut where vo01 ends; shot 9 is clip B (0–5.0 s, covering "But when a son marries…", which ends at 4.8 s) then the split.sh terminal from 8.0 s (after the untimed build) to 85.3 s, sped up to fit the rest (~13x, tagged).
   Was: Then step 7 (1 h buffer, no new features) and step 8 (submission: check the hackathon-playbook MCP `submitting` and `pitching` checklists first).
   When `TodayMonth` gets old, take the newest month from https://labourbureau.gov.in/CPI.

## Next session: "Try it" README + public demo bot (user asked 2026-11-27 for both)

Why: people from the X post (`docs/x-post.md`) and judges can't try Giveback without a Fly account, two API keys, Go and BotFather.

**A. README "Try it yourself" (do first, ~15 min, no code).** Rewrite `## Run your own` as numbered steps: prerequisites; BotFather token; Fly Connectors (Gemini + Anthropic, granted to `giveback-*`) and the two gateway URLs in `internal/vision/vision.go`; `sprite create` + public URL; `scripts/deploy.sh`; then in Telegram `/start`, `/family father = Bandaru Subba Rao`, `/family mother = Butham Lakshmi`, `/pages` + `testdata/notebook/page1.jpeg` and `page2.jpeg` as Files + `/done`, type "Bandaru Ramulu", tap ✅, forward the sample card. Add a top line "Try it in 30 seconds: @<demo bot>" once B is live. Optional split step.

**B. Public demo bot (~2–3 h). New Sprite `giveback-demo` + new BotFather bot; never touch giveback-bandaru / giveback-ravi.**
1. Demo mode behind an env/flag read in `main.go` (e.g. `GIVEBACK_DEMO=1`), handled in `internal/bot/bot.go` `handle()` (line ~118):
   - read-only notebook: `/pages`, `/family` changes and `copy-for` refused with a friendly line ("This is a demo notebook…");
   - answers per chat: Yes/No taps, relation links and confirmed amounts are keyed by chat in demo mode so one visitor never changes what the next sees (ledger tables `answers`, `family` link rows, `lines.checked`; or a per-chat overlay table);
   - daily cap on card reads (e.g. 40/day total, 5/chat), then "the demo's card reader is resting until tomorrow; type a name instead"; typing names costs no model call;
   - `/start` text for strangers: what this is, the made-up Bandaru notebook, three things to try.
2. Seed: copy the hydrogen-read Bandaru ledger with `giveback copy-for` (pages, lines, crops, family tree, model cache) **after clearing its answers** so visitors get the questions.
3. Made-up sample card: an invented wedding invitation (fictional hosts, e.g. "Sri Butham Muthyalu & Smt. Vimala" — they are the synthetic notebook's people, not the real card) as an image in `testdata/`, forwardable by visitors. Never use the real jayaprakash card.
4. Tests (no model calls): demo refuses `/pages`; two chats' answers don't mix; the cap. Deploy with `scripts/deploy.sh giveback-demo <token>`; check `/usage` after a day.
5. Then: README top line + `docs/x-post.md` gets "Try it: t.me/<demo bot>".

