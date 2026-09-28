# LLD 07: AI features

Scope: `src/server/chatproxy.go`, `src/server/handlers.go` (`handleConfig`), `src/cookie/cookie.go` (request-count helpers), `src/utils/utils.go` (factors), `src/resources/js/common.js`, `src/resources/chat-widget/src/index.ts`.

The browser never talks to OpenAI directly. Every LLM call goes to `POST /chat/completions` on the gotty server, which checks the caller and forwards the body to `https://api.openai.com/v1/chat/completions` with the server's key.

## 1. Configuration

| Setting | Source | Default |
|---|---|---|
| OpenAI API key | `GitConfig["user.OpenaiAPIKey"]`, base64-encoded | empty (proxy calls fail upstream) |
| Allowed host | `GitConfig["user.host"]` | `localhost` |
| Guest refill rate | `utils.GUEST_FACTOR = 0.33` requests/min | about 20 requests per hour |
| Signed-in refill rate | `utils.USER_FACTOR = 1` request/min | 60 requests per hour |
| Bucket cap | `factor × DEADLINE_MINUTES (60)` | about 20 for guests, 60 for users |

## 2. Per-session access token

`GET /config.js` (`handleConfig`) creates an opaque token for the page:

1. It reuses the `ACCESS_SECRET` in the session cookie, or generates a new random 128-bit value.
2. It computes `openai_access_token = AES-GCM-Encrypt(defaultToken, secret)` (`encoder.Encrypt`, URL-safe base64).
3. It stores `ACCESS_TOKEN` and `ACCESS_SECRET` in the `user-session` cookie, and writes `var openai_access_token = '…'` into the script.

The chat widget and `common.js` send the token as `Authorization: Bearer <token>`.

## 3. Proxy pipeline (`handleChatProxy`)

```mermaid
flowchart TD
    A["POST /chat/completions"] --> O{"Origin and Referer<br/>host contains user.host?"}
    O -- no --> E403["403 invalid_domain_origin"]
    O -- yes --> T{"Bearer token present?"}
    T -- no --> E400["400 invalid_request_error"]
    T -- yes --> D{"Decrypt(token, cookie ACCESS_SECRET)<br/>== server key?"}
    D -- no --> E401["401 Unauthorized"]
    D -- yes --> ADM{"IsUserAdmin?"}
    ADM -- yes --> FWD
    ADM -- no --> R["refill bucket:<br/>balance += elapsed_min × factor, capped"]
    R --> Q{"balance > 0?"}
    Q -- no --> E429["429 TooManyRequests<br/>(asks guests to log in)"]
    Q -- yes --> FWD["replace Authorization with server key,<br/>RoundTrip to OpenAI"]
    FWD --> DEC["non-admin: balance -= 1 (cookie)"]
    DEC --> COPY["copy upstream headers and body to client"]
```

- The request body is passed through unchanged, so the client chooses `model`, `messages`, `temperature` and `max_tokens`.
- OpenAI errors are returned with their original status and body. Proxy errors use OpenAI's error shape: `{"error":{"message","type","param","code"}}`.
- The rate limit is stored in the signed session cookie, per browser session.

## 4. Genie assistant (chat widget)

- **Model:** `gpt-3.5-turbo` (the widget default; `index.html` does not override it).
- **Context:** a system message with the live editor content, followed by the conversation history.
- **History sync:** every message is pushed to Firebase `chat-list/<dbpath>`. Shared viewers see the same conversation, and the master's `cleanup()` deletes it.
- **Output handling:** fenced code blocks get Insert and Replace buttons, which call `window.insertcodesnippet` and `window.replacecodesnippet` (base64 payload). Replace is blocked in practice mode.

## 5. Practice question generation (`common.js`)

| Function | Prompt → output |
|---|---|
| `generateNewQuestion(topic, difficulty, customPrompt, languages)` | Asks for a DSA problem that is not in the stored titles, as JSON `{title, description, code_templates{lang:{template, multiline_comment_start, multiline_comment_end}}}`. `sanitizeJSONString` extracts the JSON. The function then adds `id`, `nameHyphenated`, `topic`, `difficulty`, `added` and `delimeter`. |
| `getCodeTemplate(nameHyphenated, language)` | Fetches or generates the template for a language not yet in `code_templates`, and caches it in the stored question. |

- Both call `getResponseFromOpenAI(openai_access_token, prompt, {baseUri: "/chat/completions"})`: model `gpt-4o-mini`, `max_tokens` 800, and `temperature` from `localStorage.temperature` (default 0.3).
- Topics come from the `topics` array in `common.js` (for example Two Pointers, DP, Graphs and System Design).
- Results are stored only in `localStorage.questions`, which keeps the latest 100.
