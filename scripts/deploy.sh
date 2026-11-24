#!/usr/bin/env bash
# Build Giveback, copy it to a Sprite, run it as a service, and point a Telegram bot at it.
#
#   scripts/deploy.sh <sprite-name> <bot-token-file>
#
# Secrets never pass through command-line arguments on the Sprite: the bot token and
# webhook secret are pushed as files readable only by the sprite user.
set -euo pipefail

SPRITE=${1:?usage: deploy.sh <sprite-name> <bot-token-file>}
TOKEN_FILE=${2:?usage: deploy.sh <sprite-name> <bot-token-file>}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BUILD=$(mktemp -d)
trap 'rm -rf "$BUILD"' EXIT

echo "› building linux/amd64 binary"
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$BUILD/giveback" .)

# The webhook secret proves a request really came from Telegram. Keep one per bot.
SECRET_FILE="$(dirname "$TOKEN_FILE")/$(basename "$TOKEN_FILE" .token).webhook-secret"
if [[ ! -s "$SECRET_FILE" ]]; then
  (umask 077; head -c 32 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 40 > "$SECRET_FILE")
fi

echo "› copying binary and secrets to $SPRITE"
sprite exec -s "$SPRITE" --no-stdin -- bash -c 'mkdir -p ~/giveback ~/.config/giveback && chmod 700 ~/.config/giveback'
sprite file push -p -s "$SPRITE" "$BUILD/giveback" /home/sprite/giveback/giveback.new
sprite file push -p -s "$SPRITE" "$TOKEN_FILE" /home/sprite/.config/giveback/bot.token
sprite file push -p -s "$SPRITE" "$SECRET_FILE" /home/sprite/.config/giveback/webhook.secret
sprite exec -s "$SPRITE" --no-stdin -- bash -c '
  chmod 600 ~/.config/giveback/bot.token ~/.config/giveback/webhook.secret
  chmod 755 ~/giveback/giveback.new && mv ~/giveback/giveback.new ~/giveback/giveback
  if sprite-env services get giveback >/dev/null 2>&1; then
    sprite-env services restart giveback
  else
    sprite-env services create giveback --cmd /home/sprite/giveback/giveback --dir /home/sprite/giveback --http-port 8080 --no-stream
  fi'

URL=$(sprite info -s "$SPRITE" | awk '/^URL:/ {print $2}')
echo "› pointing Telegram webhook at $URL/telegram"
TOKEN=$(tr -d '[:space:]' < "$TOKEN_FILE")
curl -sS "https://api.telegram.org/bot$TOKEN/setWebhook" \
  --data-urlencode "url=$URL/telegram" \
  --data-urlencode "secret_token=$(cat "$SECRET_FILE")" \
  --data-urlencode 'allowed_updates=["message","callback_query"]' \
  -d drop_pending_updates=true | grep -oE '"(ok|description)":[^,}]*'
echo "✓ deployed to $SPRITE"
