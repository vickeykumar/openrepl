#!/usr/bin/env bash
# Which of OpenRouter's free models answer right now?
#
#   OPENREPL_OPENROUTER_API_KEY=sk-or-... scripts/test-openrouter-free-models.sh [options] [model-id ...]
#
# Without model ids it takes every free chat model from OpenRouter's public
# catalog (zero price in and out, text out only) and sends each one a tiny
# request: reply with {"ok": true}, in JSON mode where the model takes it.
# A model passes when the answer holds {"ok": true}. The key is read from the
# environment and never printed.
#
# Options:
#   --dry-run     list the models that would be tried, send nothing (needs no key)
#   --tokens N    answer budget of each request (default 800: models that think first
#                 spend it before they answer, so a small number looks like a failure)
#   --delay SEC   wait between requests (default 4: the free tier allows 20 a minute)
#   --yes         do not ask before sending
#
# The free tier allows 50 requests a day (1,000 once $10 of credit has been bought
# at any time) for the whole account, and the same limits apply to your visitors,
# so a run uses as many of them as there are models. A 429 usually means that the
# provider's shared pool is busy, not that the model is gone: try it again later.

set -u

dry=0
tokens=800
delay=4
assume_yes=0
models=()
while [ $# -gt 0 ]; do
	case $1 in
	--dry-run) dry=1 ;;
	--tokens) tokens=${2:?--tokens needs a number}; shift ;;
	--delay) delay=${2:?--delay needs a number of seconds}; shift ;;
	--yes | -y) assume_yes=1 ;;
	-h | --help) sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	-*) echo "unknown option: $1" >&2; exit 2 ;;
	*) models+=("$1") ;;
	esac
	shift
done

for tool in curl python3; do
	command -v "$tool" >/dev/null 2>&1 || { echo "$tool is needed" >&2; exit 2; }
done
case $tokens$delay in *[!0-9.]*) echo "--tokens and --delay take numbers" >&2; exit 2 ;; esac

if [ "$dry" -eq 0 ] && [ -z "${OPENREPL_OPENROUTER_API_KEY:-}" ]; then
	echo "Set OPENREPL_OPENROUTER_API_KEY first (the key is only read from the environment)." >&2
	exit 2
fi

# "id json" for each free chat model, from the public catalog
catalog() {
	curl -s --max-time 40 https://openrouter.ai/api/v1/models | python3 -c '
import json, sys
try:
    data = json.load(sys.stdin)["data"]
except Exception as e:
    sys.exit("could not read the model catalog: %s" % e)
for m in sorted(data, key=lambda m: m["id"]):
    p = m.get("pricing", {})
    try:
        free = float(p.get("prompt", "1")) == 0 and float(p.get("completion", "1")) == 0
    except ValueError:
        free = False
    out = (m.get("architecture") or {}).get("output_modalities") or ["text"]
    if not free or out != ["text"] or "content-safety" in m["id"]:
        continue
    print(m["id"], "json" if "response_format" in (m.get("supported_parameters") or []) else "plain")
'
}

list=$(catalog) || exit 1
if [ ${#models[@]} -gt 0 ]; then
	picked=""
	for m in "${models[@]}"; do
		line=$(printf '%s\n' "$list" | awk -v id="$m" '$1 == id')
		picked="$picked${line:-$m plain}"$'\n'
	done
	list=${picked%$'\n'}
fi
count=$(printf '%s\n' "$list" | grep -c .)
[ "$count" -gt 0 ] || { echo "no models to try" >&2; exit 1; }

echo "$count model(s):"
printf '%s\n' "$list" | awk '{ printf "  %-52s %s\n", $1, ($2 == "json" ? "(JSON mode)" : "") }'
if [ "$dry" -eq 1 ]; then
	echo "(dry run: nothing was sent)"
	exit 0
fi

echo
echo "This sends $count request(s) and uses as many of the account's free-tier requests today"
echo "(50 a day, 1,000 after \$10 of credit was ever bought), at $delay s apart."
if [ "$assume_yes" -eq 0 ] && [ -t 0 ]; then
	printf 'Go on? [y/N] '
	read -r answer
	case $answer in y | Y | yes) ;; *) echo "stopped"; exit 1 ;; esac
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# one request: prints a status word and the facts after it
try() {
	local model=$1 mode=$2 body http secs
	body=$(python3 - "$model" "$mode" "$tokens" <<'EOF'
import json, sys
model, mode, tokens = sys.argv[1], sys.argv[2], int(sys.argv[3])
req = {"model": model, "max_tokens": tokens,
       "messages": [{"role": "user", "content": 'Reply with the JSON {"ok": true}'}]}
if mode == "json":
    req["response_format"] = {"type": "json_object"}
print(json.dumps(req))
EOF
	)
	read -r http secs < <(curl -s --max-time 120 -o "$work/answer.json" -w '%{http_code} %{time_total}\n' \
		https://openrouter.ai/api/v1/chat/completions \
		-H "Authorization: Bearer $OPENREPL_OPENROUTER_API_KEY" -H "Content-Type: application/json" -d "$body")
	python3 - "$work/answer.json" "${http:-000}" "${secs:-0}" <<'EOF'
import json, sys
path, http, secs = sys.argv[1], sys.argv[2], float(sys.argv[3])
try:
    d = json.load(open(path))
except Exception:
    print("FAIL   no answer (HTTP %s, %.1fs)" % (http, secs)); sys.exit()
if "error" in d:
    e = d["error"]; md = e.get("metadata") or {}
    why = (md.get("raw") or e.get("message") or "")[:110].replace("\n", " ")
    word = {"429": "BUSY  ", "402": "CREDIT", "404": "GONE  ", "401": "KEY   "}.get(str(e.get("code")), "FAIL  ")
    print("%s %s %s%s" % (word, e.get("code"), why, ("  [" + md["provider_name"] + "]") if md.get("provider_name") else ""))
    sys.exit()
try:
    c = d["choices"][0]; text = (c["message"].get("content") or "").strip()
    u = d.get("usage") or {}; think = (u.get("completion_tokens_details") or {}).get("reasoning_tokens", 0)
    facts = "%.1fs, %s tokens (%s thinking), %s, finish=%s" % (secs, u.get("completion_tokens", "?"), think, d.get("provider", "?"), c.get("finish_reason"))
    ok = '"ok"' in text and "true" in text.lower()
    if ok: print("PASS   " + facts)
    elif not text: print("EMPTY  the budget went on thinking, no answer: " + facts)
    else: print("ODD    answered %r: %s" % (text[:60], facts))
except Exception as e:
    print("FAIL   unexpected answer: %s" % str(d)[:120])
EOF
}

pass=0
results=""
n=0
while read -r model mode; do
	[ -n "$model" ] || continue
	n=$((n + 1))
	result=$(try "$model" "$mode")
	printf '%-52s %s\n' "$model" "$result"
	results="$results$result"$'\n'
	[ "$n" -lt "$count" ] && sleep "$delay"
done <<<"$list"

pass=$(printf '%s' "$results" | grep -c '^PASS')
echo
echo "$pass of $count passed."
echo "PASS = answered {\"ok\": true}.  BUSY = rate-limited (try later).  EMPTY = thought through the whole budget (raise --tokens)."
echo "CREDIT = 402, check the account balance.  GONE = no such model or host.  KEY = the key was refused."
