#!/usr/bin/env bash
# Checks a running OpenREPL server (the Docker image) from the outside.
#   scripts/ci/smoke.sh http://localhost:8080
# Waits for it to answer, then checks pages, scripts, the contact form and that the
# admin API is closed to a visitor. Prints one line per check and exits 1 if any fails.
set -uo pipefail

BASE="${1:-http://localhost:8080}"
BASE="${BASE%/}"
fail=0

echo "waiting for $BASE ..."
up=0
for i in $(seq 1 90); do
  if curl -fsS -o /dev/null --max-time 3 "$BASE/"; then up=1; break; fi
  sleep 2
done
if [ "$up" != 1 ]; then echo "the server did not answer in 3 minutes"; exit 1; fi

# check <name> <expected status> <curl args...>  (body in $body)
check() {
  local name="$1" want="$2"; shift 2
  local out code
  out="$(curl -sS --max-time 20 -w '\n%{http_code}' "$@" 2>&1)"
  code="${out##*$'\n'}"
  body="${out%$'\n'*}"
  if [ "$code" = "$want" ]; then echo "ok    $name ($code)"; else echo "FAIL  $name: wanted $want, got $code"; fail=1; fi
}
# has <name> <text>: the last body has the text
has() {
  if [[ "$body" == *"$2"* ]]; then echo "ok    $1"; else echo "FAIL  $1: no \"$2\" in the answer"; fail=1; fi
}

check "home page" 200 "$BASE/";                          has "home page names OpenREPL" "OpenREPL"
check "theme script" 200 "$BASE/js/theme.js"; has "theme.js has the three choices" "setThemeChoice"
check "page script" 200 "$BASE/js/scribbler.js"
check "widget script" 200 "$BASE/js/chat-widget.js"
check "stylesheet" 200 "$BASE/css/ui-refresh.css";       has "light theme block in the stylesheet" "Light theme: the top and bottom"
check "practice page" 200 "$BASE/practice/dsa-questions"
check "privacy page" 200 "$BASE/privacy.html"
check "terms page" 200 "$BASE/terms.html"
check "about page" 200 "$BASE/about.html"
check "health check" 200 "$BASE/healthz"

# the contact form refuses an empty message and thanks (and drops) an obvious spam one
check "feedback: empty message" 400 -X POST -d 'message=' "$BASE/feedback"
check "feedback: spam is thanked" 200 -X POST --data-urlencode 'message=Best casino [url=https://x.example]play[/url]' "$BASE/feedback"
has "feedback: spam answer" "Thanks"
check "feedback: GET goes to the dashboard" 302 "$BASE/feedback"

# the admin API is closed to a visitor who has not signed in
check "admin API closed" 401 "$BASE/admin/feedback"
check "admin write closed" 401 -X POST "$BASE/admin/feedback/spam/delete"

if [ "$fail" != 0 ]; then echo "SMOKE TEST FAILED"; exit 1; fi
echo "smoke test passed"
