#!/usr/bin/env bash
# Split a household: give a son's or daughter's new home its own Giveback, on
# its own Sprite, carrying the whole notebook so far. Times every phase.
#
#   scripts/split.sh <from-sprite> <from-household> <new-household> <new-bot-token-file>
#   scripts/split.sh giveback-bandaru Bandaru Ravi ~/.config/giveback/bot2.token
#
# The new Sprite is giveback-<new-household in lower case>, so the connectors
# granted to the name prefix "giveback-" reach it with no extra step.
#
# Sprites have no fork, so this is a copy: the old Sprite writes a consistent
# snapshot of its ledger (the bot keeps running), the snapshot moves to the new
# Sprite, and the new household's bot starts on it. From then on the two
# notebooks grow apart.
set -euo pipefail

FROM=${1:?usage: split.sh <from-sprite> <from-household> <new-household> <new-bot-token-file>}
FROM_NAME=${2:?missing <from-household>}
NEW_NAME=${3:?missing <new-household>}
TOKEN_FILE=${4:?missing <new-bot-token-file>}
TO="giveback-$(echo "$NEW_NAME" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9' '-' | sed 's/^-//; s/-$//')"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
SNAP="/tmp/split-$TO"

[[ -s "$TOKEN_FILE" ]] || { echo "no bot token in $TOKEN_FILE" >&2; exit 1; }
if sprite info -s "$TO" >/dev/null 2>&1; then
  echo "$TO already exists" >&2; exit 1
fi

echo "› building (not timed)"
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$WORK/giveback" .)

T0=$(date +%s.%N)
lap() { # name of the phase that just ended
  local now; now=$(date +%s.%N)
  printf '⏱  %-34s %5.1f s\n' "$1" "$(echo "$now - ${LAST:-$T0}" | bc)"
  LAST=$now
}

echo "› 1. creating Sprite $TO"
sprite create --skip-console "$TO"
sprite config update -s "$TO" --url-auth public >/dev/null # Telegram must reach the webhook
lap "new Sprite created"

echo "› 2. snapshot of the $FROM_NAME notebook on $FROM (its bot keeps running)"
sprite file push -p -s "$FROM" "$WORK/giveback" /tmp/giveback-copy
sprite exec -s "$FROM" --no-stdin -- bash -c "
  set -e; chmod 755 /tmp/giveback-copy; rm -rf '$SNAP' '$SNAP.tgz'
  /tmp/giveback-copy copy-for '$SNAP' '$FROM_NAME' '$NEW_NAME'
  tar czf '$SNAP.tgz' -C '$SNAP' . && rm -rf '$SNAP' /tmp/giveback-copy
  du -h '$SNAP.tgz' | cut -f1"
lap "snapshot made"

echo "› 3. moving it to $TO"
sprite file pull -s "$FROM" "$SNAP.tgz" "$WORK/notebook.tgz"
sprite exec -s "$FROM" --no-stdin -- rm -f "$SNAP.tgz"
sprite file push -p -s "$TO" "$WORK/notebook.tgz" /tmp/notebook.tgz
sprite exec -s "$TO" --no-stdin -- bash -c '
  set -e; mkdir -p ~/giveback/data && tar xzf /tmp/notebook.tgz -C ~/giveback/data && rm /tmp/notebook.tgz'
lap "notebook moved"

echo "› 4. starting the $NEW_NAME household's bot"
GIVEBACK_BIN="$WORK/giveback" "$ROOT/scripts/deploy.sh" "$TO" "$TOKEN_FILE"
lap "bot running, webhook set"

URL=$(sprite info -s "$TO" | awk '/^URL:/ {print $2}')
for _ in $(seq 30); do
  if curl -fsS -m 10 "$URL/" 2>/dev/null; then break; fi
  sleep 1
done
lap "answering at its URL"
printf '⏱  %-34s %5.1f s\n' "total" "$(echo "$(date +%s.%N) - $T0" | bc)"
echo "✓ the $NEW_NAME household has its own Giveback: $TO ($URL)"
