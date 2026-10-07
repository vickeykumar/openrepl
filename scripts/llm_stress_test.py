#!/usr/bin/env python3
"""Stress test for a chat model that Genie might use.

Sends N requests shaped like Genie's (editor code and terminal output in a
system message, then a question) to an OpenAI-style chat endpoint, several at a
time, and reports how many answered, how fast, which hosts answered, what it
cost, and whether the answers were usable. Python 3 standard library only.

The key is read from an environment variable, or asked for with a hidden prompt
(nothing you type shows on screen), and is never printed.

Examples (with no key in the environment it asks for one):
  python3 scripts/llm_stress_test.py                          # OpenRouter, Gemma 4 31B, 50 requests
  python3 scripts/llm_stress_test.py --preset openai-luna     # OpenAI key from $KEY, or asked for
  python3 scripts/llm_stress_test.py -n 20 --concurrency 3
  python3 scripts/llm_stress_test.py --dry-run                # show one request, send nothing
  python3 scripts/llm_stress_test.py --mode question          # the practice page's question generator

--mode question sends the app's real "new question" prompt (five different
topics) in JSON mode and checks that every reply parses as JSON and holds a
title, a description and a template for each language.
"""
import argparse
import collections
import concurrent.futures
import getpass
import json
import os
import re
import statistics
import sys
import threading
import time
import urllib.error
import urllib.request

PRESETS = {
    "openrouter-gemma31": dict(
        url="https://openrouter.ai/api/v1/chat/completions",
        key_env="ORKEY", model="google/gemma-4-31b-it", style="plain"),
    "openrouter-gemma26": dict(
        url="https://openrouter.ai/api/v1/chat/completions",
        key_env="ORKEY", model="google/gemma-4-26b-a4b-it", style="plain"),
    "google-gemma31": dict(
        url="https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
        key_env="GKEY", model="gemma-4-31b-it", style="plain"),
    "openai-luna": dict(
        url="https://api.openai.com/v1/chat/completions",
        key_env="KEY", model="gpt-6-luna", style="reasoning"),
    "openai-mini": dict(
        url="https://api.openai.com/v1/chat/completions",
        key_env="KEY", model="gpt-4o-mini", style="plain"),
}

# Five everyday questions, each with the code and terminal output Genie would
# see. "expect" is a word an answer to it should contain, to catch empty or
# off-topic replies (a rough check, not a grade).
CASES = [
    dict(language="Python", file="main.py",
         code="def add_item(x, items=[]):\n    items.append(x)\n    return items\nprint(add_item(1))\nprint(add_item(2))",
         output="[1]\n[1, 2]",
         question="Why does the second call print [1, 2]? I expected [2].",
         expect=["none"]),
    dict(language="C", file="main.c",
         code='#include <stdio.h>\nint main(void) {\n    int a[5] = {1, 2, 3, 4, 5};\n    for (int i = 0; i <= 5; i++)\n        printf("%d\\n", a[i]);\n    return 0;\n}',
         output="1\n2\n3\n4\n5\n32767",
         question="Why does it print a strange extra number at the end?",
         expect=["bound", "<= 5", "i < 5", "off-by-one", "off by one"]),
    dict(language="Python", file="total.py",
         code="def total(items):\n    s = 0\n    for it in items:\n        s += it\n    return summ\nprint(total([1, 2, 3]))",
         output="Traceback (most recent call last):\n  File \"total.py\", line 6, in <module>\n    print(total([1, 2, 3]))\nNameError: name 'summ' is not defined",
         question="What does this error mean and how do I fix it?",
         expect=["summ"]),
    dict(language="JavaScript", file="user.js",
         code='async function getUser() { return { name: "Ada" }; }\nconst u = getUser();\nconsole.log(u.name);',
         output="undefined",
         question="Why is u.name undefined?",
         expect=["await", "promise"]),
    dict(language="Go", file="main.go",
         code='package main\n\nimport (\n    "fmt"\n    "os"\n)\n\nfunc main() {\n    x := 5\n    fmt.Println("hi")\n}',
         output='./main.go:5:5: "os" imported and not used\n./main.go:9:5: declared and not used: x',
         question="Why won't this compile?",
         expect=["not used", "unused", "declared"]),
]


QUESTION_PROMPT = """Generate a unique data structure and algorithm coding question based on these criteria:

- **Topic:** {TOPIC}
- **Difficulty Level:** {LEVEL}
- **Programming Languages:** {LANGS}

### **Question Requirements**:
1. The question should be a real-world problem related to the given topic.
2. The problem should have a **clear problem statement** with necessary constraints.
3. **Do not explicitly mention the topic** in the title or description. The user should figure it out after reading.
4. Strictly Format the description so that **no line exceeds 100 characters** for better readability.
5. Use **stick figure drawings** whenever necessary to visually explain the problem.
6. Provide at least **two sample test cases** in the question description.
7. Ensure the problem is suitable for implementation in these languages(comma separated): {LANGS}.
8. **Do NOT generate a question with any of these already generated questions(comma separated):**

### **Output Format**:
```json
{
  "title": "Title of the problem",
  "description": "Detailed problem description with sample test cases...",
  "code_templates": {
    "language name": {
      "template": "Provide a function signature and a main function to verify the solution.",
      "multiline_comment_start": "String for multiline comment start.",
      "multiline_comment_end": "String for multiline comment end."
    }
  }
}
```
**Ensure that the output strictly follows the JSON format above.** It must be one valid JSON object with no comments, every quote and newline inside a string escaped, and one entry in "code_templates" for each of these languages: {LANGS}.
"""
QUESTION_LANGS = ["Python", "C++", "Go"]
QUESTION_CASES = [("Two Pointers", "Medium"), ("Graphs", "Hard"), ("Dynamic Programming", "Easy"),
                  ("Strings", "Medium"), ("Heaps", "Hard")]


def check_question(text, languages):
    """(valid JSON, complete) for a generated question."""
    text = re.sub(r"<thought>.*?</thought>", "", text, flags=re.S).strip()
    fenced = re.search(r"```(?:json)?\s*(.*?)```", text, flags=re.S)
    if fenced:
        text = fenced.group(1).strip()
    try:
        question = json.loads(text)
    except ValueError:
        return False, False
    if not isinstance(question, dict):
        return True, False
    templates = question.get("code_templates")
    complete = (isinstance(question.get("title"), str) and question["title"].strip() != ""
                and isinstance(question.get("description"), str) and len(question["description"]) > 100
                and isinstance(templates, dict)
                and all(any(k.lower().replace(" ", "") == lang.lower().replace(" ", "") and isinstance(v, dict) and v.get("template")
                            for k, v in templates.items()) for lang in languages))
    return True, bool(complete)


def build_body(case, args):
    if args.mode == "question":
        topic, level = QUESTION_CASES[case % len(QUESTION_CASES)]
        prompt = (QUESTION_PROMPT.replace("{TOPIC}", topic).replace("{LEVEL}", level)
                  .replace("{LANGS}", ", ".join(QUESTION_LANGS)))
        body = {"model": args.model, "messages": [{"role": "user", "content": prompt}]}
        if args.style == "reasoning":
            body["reasoning_effort"] = args.effort
            body["max_completion_tokens"] = args.max_tokens
        else:
            body["temperature"] = 0.3
            body["max_tokens"] = args.max_tokens
        if not args.no_json_mode:
            body["response_format"] = {"type": "json_object"}
        if args.extra:
            body.update(args.extra)
        return body
    return build_chat_body(case, args)


def build_chat_body(case, args):
    context = "\n".join([
        "Openrepl IDE real-time context of what the user is working on. Use it whenever the user asks to debug, explain or fix their code, or an error, without pasting anything.",
        "Language: " + case["language"],
        "File: " + case["file"],
        "--- Editor code ---",
        case["code"],
        "--- Terminal output (the most recent lines of the active terminal) ---",
        case["output"],
    ])
    body = {
        "model": args.model,
        "messages": [{"role": "system", "content": context},
                     {"role": "user", "content": case["question"]}],
    }
    if args.style == "reasoning":
        body["reasoning_effort"] = args.effort
        body["max_completion_tokens"] = args.max_tokens
    else:
        body["temperature"] = 0.5
        body["max_tokens"] = args.max_tokens
    if args.extra:
        body.update(args.extra)
    return body


class Breaker:
    """Stops the run after several refusals in a row (bad key, no credit)."""

    def __init__(self, limit=5):
        self.limit, self.count, self.tripped = limit, 0, threading.Event()
        self.lock = threading.Lock()

    def note(self, status):
        with self.lock:
            self.count = self.count + 1 if status in (401, 402, 403) else 0
            if self.count >= self.limit:
                self.tripped.set()


def one_request(i, args, key, breaker):
    case = CASES[i % len(CASES)]
    case_arg = i if args.mode == "question" else case
    result = dict(i=i, ok=False, json_ok=None, status=None, secs=None, provider=None, cost=None,
                  finish=None, tokens=None, thought=False, empty=False,
                  relevant=False, error=None)
    if breaker.tripped.is_set():
        result["error"] = "skipped (several refusals in a row)"
        return result
    data = json.dumps(build_body(case_arg, args)).encode()
    request = urllib.request.Request(args.url, data=data, method="POST", headers={
        "Authorization": "Bearer " + key, "Content-Type": "application/json"})
    start = time.time()
    try:
        with urllib.request.urlopen(request, timeout=args.timeout) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as e:
        status, raw = e.code, e.read()
    except Exception as e:  # timeout, connection reset, DNS
        result["secs"] = time.time() - start
        result["error"] = type(e).__name__ + ": " + str(e)[:80]
        breaker.note(None)
        return result
    result["secs"] = time.time() - start
    result["status"] = status
    breaker.note(status)
    try:
        reply = json.loads(raw)
        if isinstance(reply, list) and reply:  # Google wraps its errors in a list
            reply = reply[0]
    except ValueError:
        result["error"] = "not JSON: " + raw[:60].decode("utf-8", "replace")
        return result
    if status != 200 or "choices" not in reply:
        err = reply.get("error") if isinstance(reply, dict) else None
        message = err.get("message") if isinstance(err, dict) else str(err)
        result["error"] = "HTTP %s: %s" % (status, str(message)[:90])
        return result
    choice = reply["choices"][0]
    text = (choice.get("message") or {}).get("content") or ""
    usage = reply.get("usage") or {}
    result.update(
        ok=bool(text.strip()), provider=reply.get("provider"),
        cost=usage.get("cost"), finish=choice.get("finish_reason"),
        tokens=usage.get("total_tokens"), thought="<thought>" in text,
        empty=not text.strip(),
        relevant=any(word in text.lower() for word in case["expect"]))
    if args.mode == "question":
        json_ok, complete = check_question(text, QUESTION_LANGS)
        result.update(json_ok=json_ok, relevant=complete, text=text[:8000])
        if not json_ok:
            result["error"] = "answer is not valid JSON"
        elif not complete:
            result["error"] = "JSON is missing a title, description or language template"
    if result["empty"]:
        result["error"] = "empty answer (finish: %s)" % choice.get("finish_reason")
    return result


def percentile(values, p):
    if not values:
        return None
    ordered = sorted(values)
    return ordered[min(len(ordered) - 1, int(round(p / 100 * (len(ordered) - 1))))]


def report(results, wall, args):
    total = len(results)
    ok = [r for r in results if r["ok"]]
    times = [r["secs"] for r in ok]
    print("\n" + "=" * 64)
    print("RESULTS: %s (%s), %d requests, %d at a time" % (args.model, args.preset, total, args.concurrency))
    print("=" * 64)
    print("Answered:      %d of %d  (%.0f%%)" % (len(ok), total, 100.0 * len(ok) / max(total, 1)))
    print("Wall time:     %.1f s  (%.2f requests/s)" % (wall, total / wall if wall else 0))
    if times:
        print("Answer time:   fastest %.1fs | median %.1fs | p90 %.1fs | p95 %.1fs | slowest %.1fs"
              % (min(times), statistics.median(times), percentile(times, 90), percentile(times, 95), max(times)))
    statuses = collections.Counter(str(r["status"]) if r["status"] else "no reply" for r in results)
    print("HTTP status:   " + ", ".join("%s x%d" % kv for kv in sorted(statuses.items())))
    errors = collections.Counter(r["error"] for r in results if r["error"])
    if errors:
        print("Errors:")
        for message, count in errors.most_common(6):
            print("   %3d x %s" % (count, message))
    hosts = collections.Counter(r["provider"] for r in ok if r["provider"])
    if hosts:
        print("Hosts:         " + ", ".join("%s x%d" % kv for kv in hosts.most_common()))
        by_host = collections.defaultdict(list)
        for r in ok:
            if r["provider"]:
                by_host[r["provider"]].append(r["secs"])
        for host, host_times in sorted(by_host.items(), key=lambda kv: -len(kv[1])):
            print("   %-14s median %.1fs, slowest %.1fs" % (host, statistics.median(host_times), max(host_times)))
    costs = [r["cost"] for r in ok if isinstance(r["cost"], (int, float))]
    if costs:
        print("Cost:          total $%.5f | average $%.5f | per 1,000 messages about $%.2f"
              % (sum(costs), sum(costs) / len(costs), 1000 * sum(costs) / len(costs)))
    tokens = [r["tokens"] for r in ok if r["tokens"]]
    if tokens:
        print("Tokens:        average %d per request" % (sum(tokens) / len(tokens)))
    print("Finish:        " + ", ".join("%s x%d" % kv for kv in collections.Counter(r["finish"] for r in ok).most_common()))
    if args.mode == "question":
        print("Valid JSON:    %d of %d replies parse as JSON" % (sum(bool(r["json_ok"]) for r in ok), len(ok)))
        print("Complete:      %d of %d have a title, a description and a template for every language"
              % (sum(r["relevant"] for r in ok), len(ok)))
    else:
        print("Quality check: %d of %d answers mention the expected word" % (sum(r["relevant"] for r in ok), len(ok)))
    print("Thinking text: %d answers contain <thought> (should be 0)" % sum(r["thought"] for r in ok))
    print("-" * 64)
    if args.mode == "question":
        ok = [r for r in ok if r["relevant"]]
    rate = 100.0 * len(ok) / max(total, 1)
    p90 = percentile(times, 90) if times else None
    verdict = []
    verdict.append(("usable questions: %s" if args.mode == "question" else "answered: %s") % ("good (95%+)" if rate >= 95 else "borderline (80-95%)" if rate >= 80 else "poor (under 80%)"))
    if p90 is not None:
        verdict.append("speed: %s" % ("good (p90 under 10s)" if p90 < 10 else "slow-ish (p90 10-20s)" if p90 < 20 else "too slow (p90 over 20s)"))
    print("Rule of thumb: " + " | ".join(verdict))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--preset", default="openrouter-gemma31", choices=sorted(PRESETS))
    parser.add_argument("-n", "--requests", type=int, default=50)
    parser.add_argument("-c", "--concurrency", type=int, default=5, help="requests in flight at once (default 5)")
    parser.add_argument("--stagger", type=float, default=0.2, help="seconds between request starts (default 0.2)")
    parser.add_argument("--mode", choices=["chat", "question"], default="chat",
                        help="chat: Genie-style questions (default); question: the practice page's question generator")
    parser.add_argument("--no-json-mode", action="store_true", help="question mode: do not ask for response_format json_object")
    parser.add_argument("--max-tokens", type=int, default=None, help="answer limit (default 1500 for chat, 4000 for question)")
    parser.add_argument("--effort", default="low", help="reasoning effort for the 'reasoning' style (default low)")
    parser.add_argument("--timeout", type=float, default=90.0)
    parser.add_argument("--url")
    parser.add_argument("--model")
    parser.add_argument("--key-env", dest="key_env")
    parser.add_argument("--style", choices=["plain", "reasoning"])
    parser.add_argument("--extra", type=json.loads, metavar="JSON",
                        help='JSON merged into every request body, e.g. \'{"provider":{"sort":"latency"}}\' to ask OpenRouter for its fastest host')
    parser.add_argument("--save", help="write every result to this JSON file")
    parser.add_argument("--dry-run", action="store_true", help="print one request and stop")
    parser.add_argument("-y", "--yes", action="store_true", help="do not ask before starting")
    args = parser.parse_args()
    for field, value in PRESETS[args.preset].items():
        if getattr(args, field) is None:
            setattr(args, field, value)
    if args.max_tokens is None:
        args.max_tokens = 4000 if args.mode == "question" else 1500

    if args.dry_run:
        print(json.dumps(build_body(0 if args.mode == "question" else CASES[0], args), indent=2))
        print("\nWould send %d requests to %s using the key in $%s." % (args.requests, args.url, args.key_env))
        return
    key = os.environ.get(args.key_env, "").strip()
    if not key:
        if not sys.stdin.isatty():
            sys.exit("No key: set $%s, or run this in a terminal so it can ask for one." % args.key_env)
        try:
            key = getpass.getpass("Paste the API key for %s (hidden, press Enter): " % args.preset).strip()
        except (KeyboardInterrupt, EOFError):
            sys.exit("\nCancelled, nothing was sent.")
        if not key:
            sys.exit("No key entered, nothing was sent.")

    print("Provider:  %s\nModel:     %s\nRequests:  %d, %d at a time, %.1fs apart\nKey:       %s (not shown)"
          % (args.preset, args.model, args.requests, args.concurrency, args.stagger,
             "from $" + args.key_env if os.environ.get(args.key_env, "").strip() else "typed in"))
    print("Cost:      each request is about 1,000 tokens in and 400 to 900 out; the provider's price per 1M tokens decides the total.")
    if not args.yes:
        try:
            input("Press Enter to start, or Ctrl+C to cancel. ")
        except KeyboardInterrupt:
            sys.exit("\nCancelled, nothing was sent.")

    # first use of urllib looks up proxy settings, which can take a moment on
    # some machines; do it now so it is not counted in the first answers
    urllib.request.getproxies()
    urllib.request.proxy_bypass(urllib.request.urlparse(args.url).hostname or "")

    breaker, results = Breaker(), []
    started = time.time()
    done = 0
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as pool:
            futures = []
            for i in range(args.requests):
                futures.append(pool.submit(one_request, i, args, key, breaker))
                time.sleep(args.stagger)
            for future in concurrent.futures.as_completed(futures):
                result = future.result()
                results.append(result)
                done += 1
                label = "ok  %5.1fs %s" % (result["secs"] or 0, result["provider"] or "") if result["ok"] else "FAIL " + str(result["error"])
                print("[%2d/%d] #%-2d %s" % (done, args.requests, result["i"], label))
                if breaker.tripped.is_set() and done == 1:
                    print("Stopping: several 401/402/403 refusals in a row (bad key or no credit).")
    except KeyboardInterrupt:
        print("\nInterrupted; summary of what finished:")
    wall = time.time() - started
    results.sort(key=lambda r: r["i"])
    report(results, wall, args)
    if args.save:
        with open(args.save, "w") as f:
            json.dump(results, f, indent=1)
        print("Saved: " + args.save)


if __name__ == "__main__":
    main()
