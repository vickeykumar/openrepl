# LLD 07: AI features

Scope: `src/server/chatproxy.go`, `src/server/handlers.go` (`handleConfig`), `src/cookie/cookie.go` (request-count helpers), `src/utils/utils.go` (factors), `src/resources/js/common.js`, `src/resources/chat-widget/src/index.ts`.

The browser never talks to OpenAI directly. Every LLM call goes to `POST /chat/completions` on the gotty server, which checks the caller and forwards the body to `https://api.openai.com/v1/chat/completions` with the server's key. The one open model, Gemma 4 31B, goes instead to OpenRouter's `https://openrouter.ai/api/v1/chat/completions` with its own key (section 3).

## 1. Configuration

| Setting | Source | Default |
|---|---|---|
| OpenAI API key | `OPENREPL_OPENAI_API_KEY` as it is, or the file's `user.OpenaiAPIKey`, base64-encoded (`utils.OpenAIKey`) | empty (proxy calls fail upstream) |
| OpenRouter API key | `OPENREPL_OPENROUTER_API_KEY` as it is (env only, `utils.OpenRouterKey`) | empty (Gemma is not offered; the proxy answers it with `model_unavailable`) |
| Allowed host | `OPENREPL_HOST`, or the file's `user.host` (`utils.Host`) | `localhost` |
| Guest refill rate | `utils.GUEST_FACTOR = 0.33` requests/min, or the admin's setting (`utils.GuestFactor()`, LLD 13) | about 20 requests per hour |
| Signed-in refill rate | `utils.USER_FACTOR = 1` request/min, or the admin's setting (`utils.UserFactor()`) | 60 requests per hour |
| Switch | `SiteSettings.Genie.Disabled`: the proxy answers 503 `GenieDisabled` to everybody but admins | on |
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
    Q -- yes --> FWD["replace Authorization with the server key<br/>of the model's provider, RoundTrip<br/>to OpenAI or OpenRouter"]
    FWD --> DEC["non-admin: balance -= 1 (cookie)"]
    DEC --> COPY["copy upstream headers and body to client"]
```

- The body is cleaned before it is forwarded (`sanitizeChatBody` in `server/chatmodels.go`; the body is read with a 1 MB limit). Only `model`, `messages`, `stream`, `temperature`, `max_tokens`, `max_completion_tokens`, `reasoning_effort` and `response_format` get through, and `response_format` only as `{"type":"json_object"}`.
- An admin can change the numbers of Genie (LLD 13 section 3, "Genie limits"): what it is told about the page (the page reads them from `settings.js`, `genieContext`), how long its conversation is, the answer size of each model (`answerCap` replaces the built-in cap in `sanitizeChatBody`), and OpenRouter's time limit and hosts (`openRouterRouting`).
- An admin can switch models off and choose the default (LLD 13 section 3, `genie.disabledModels` and `genie.defaultModel`). A request for a model that is off is answered `503 model_unavailable`, code `model_disabled`, before the balance is touched; the pages read the same lists from `settings.js` and do not offer the model (`js/model-choice.js`), starting visitors on the default. A visitor whose page is still open sees a card with no "Try again" and a button for another model.
- Only three models are accepted: `gpt-6-luna`, `gpt-4o-mini` and `google/gemma-4-31b-it`. Any other name (or none) is answered by `gpt-4o-mini`, so older callers such as the blog editor keep working.
- Each model keeps only its own settings. Luna: `reasoning_effort` (`none`, `low`, `medium` or `high`, else `low`) and `max_completion_tokens` (default by effort 1000 / 2000 / 3000 / 4000, at most 7000); no `temperature` or `max_tokens`. 4o mini: `temperature` (0 to 2) and `max_tokens` (default 800, at most 3000); no effort. Gemma: the same as 4o mini, with `max_tokens` at most 4000. A request that is not a chat request (no `messages` list) gets a 400 with no balance charged.
- The model, effort and token lists exist in three places that have to match: `server/chatmodels.go`, `js/model-choice.js` and, for the allowed efforts, the tests in `server/chatmodels_test.go`. A model change is checked first with a `curl` of the exact request body against `/v1/chat/completions`, because models differ in which parameters they accept (Luna refuses `max_tokens`).
- OpenAI errors are returned with their original status and body (the proxy used to answer 200 for them; the status is now passed on, which is what lets the question generator's retry on 400 work). Proxy errors use OpenAI's error shape: `{"error":{"message","type","param","code"}}`.
- **OpenRouter (Gemma 4 31B only).** `sanitizeChatBody` returns the model with the body; for `google/gemma-4-31b-it` the proxy uses `openrouterEndpoint` and `utils.OpenRouterKey()`, sends `X-Title: OpenREPL`, and adds the host rule itself: `provider: {order: ["modelrun/fp4","coreweave/fp4"], only: [same], allow_fallbacks: false}`. ModelRun answers first and CoreWeave only when ModelRun cannot; no other host is allowed, so a request fails rather than go to one (OpenRouter then answers 404 "No allowed providers are available"). A `provider` field sent by the browser is dropped. Both can be changed in `openRouterHosts` (`server/chatmodels.go`).
- **When OpenRouter cannot answer** (no key set, a timeout, or any status of 400 or more: 402 no credit, 404 no host, 429, 5xx) the visitor gets `503 {"error":{"message":"Gemma 4 31B isn't available right now","type":"model_unavailable","code":"model_unavailable"}}`, nobody is charged a request, and the reason is only logged. Time limits (`openAITimeout` 90 s, `openRouterTimeout` 60 s) stay under Cloudflare's roughly 100 s. The Genie panel turns that error into a card with "Try again" and "Switch to GPT-6 Luna"; the New question dialog shows it as a message. Tests: `server/chatproxy_test.go`, with fake upstreams.
- The rate limit is stored in the signed session cookie, per browser session.

## 3a. What Genie knows about the site (`knowledge.go`, `knowledge_proxy.go`)

A request from the Genie panel or from the blog editor can ask the proxy to add what the site knows about itself. The model otherwise knows nothing about OpenREPL and invents features.

- **The sources.** Notes: Markdown files in `src/resources/knowledge/`, one topic each, compiled into the binary (`bindata/knowledge`, read with `AssetDir("knowledge")`; `Makefile` target `bindata/knowledge`). A note has a header with `title`, `keywords` and an optional `link`, then 80 to 220 words of text; its id is the file name. A note that fails its checks (no header, title or text, an id that is not letters, digits and dashes, a `link` that is not a path on this site: no scheme, no host, no `//`, no backslash or space) is logged and skipped. Blog posts: read with `FetchBlogDataMap`, their HTML turned into text and cut into passages of about 140 words (at most 24 per post), each with the post's title and link `/blog?name=...`. The post's description opens its first passage. Posts are not copied anywhere; the index is rebuilt when a post is saved or deleted (`knowledgeInvalidate` in `StoreBlogData` and `deleteBlogData`) and at the latest every 5 minutes (`postIndexTTL`), which also covers a change made through another instance.
- **The search.** BM25 over stemmed words, in memory (`Index.Search`). Title words count five times, keyword words twice, text words once. `stem` is a crude suffix stripper (share, shares, shared and sharing meet); it only has to treat both sides alike. Filler words are dropped. A blog passage scores 0.85 of what the same words would score in a note, so a note wins when they are close. Only hits with at least half the best score are kept.
- **Coverage.** Each hit also carries the share of the question's words, weighted by their rarity, that the passage contains. A word that no passage has counts for 0.4 of its weight (`unknownWeight`): it says the question is about something else, but people also pad questions ("I mean ..."), so one such word must not decide it. This is what keeps platform text out of ordinary coding questions ("why does my loop never end" matches the word *loop* in one note and has 25% coverage), where a plain score would not.
- **Two settings** (constants in `knowledge_proxy.go`). The Genie panel (`"context": "chat"`) needs a score of 2.0 and a coverage of 0.65 and takes at most 3 passages: it adds a note only when one clearly matches. The blog editor (`"context": "blog"`) needs a score of 1.5, no coverage, and takes at most 4: it wants the best ones. The thresholds were set with the example questions in `knowledge_test.go`; change them with those tests in view.
- **The request.** Two body fields, never sent on to the model (`sanitizeChatBody` builds a fresh body): `context` (`chat` or `blog`; anything else, or none, is ignored, which is what the practice page and New question send) and `context_hint`. The panel puts a speaker tag in front of what a user writes (`[user-k3J9x] `, so that people in a shared session can be told apart); `lastUserText` removes it, because its random id is a word no note has and used to push the coverage of every question under the threshold. A hint, when not blank, replaces the last user message as the text to search with: the blog editor sends the post title and the text it works on, because its prompt is mostly instructions that would match the wrong notes. The editor sends the field only for the tasks that write new text ("Continue writing", "Write about this").
- **The system message.** `addKnowledge` puts one system message in front of the conversation: the facts, each marked `[note]` or `[blog post]` with its title, at most 1,400 characters per passage and 5,000 in all, and the rule: use them to answer questions about OpenREPL, do not invent features, limits, prices or steps they do not mention, say you are not sure if they do not cover the question, use a blog post only if it answers the question and say it comes from a post. When nothing matched, the body is sent on unchanged.
- **The answer.** The response carries `X-OpenREPL-Context` (also listed in `Access-Control-Expose-Headers`): `none` when the request asked and nothing matched, else base64url of `[{"id","title","kind"}]`. Without the field the header is absent. The Genie panel shows a line under each reply: "Answered without OpenREPL notes", or "Used OpenREPL notes:" with the titles as buttons (`appendContextLine`). A title opens a card in the panel with one row per passage; a row loads its text from `GET /knowledge/<id>` the first time it is opened and shows it as plain text, with "Read more in the docs" (or "Read the post") when it has a link, opened in a new tab and only if it is a path on this site. One row is open at a time; Escape closes the card (and not the panel), as does sending the next message. The blog editor shows no line.
- **`GET /knowledge/<id>`.** Public, `Cache-Control: no-store`, GET and HEAD only. It returns `{id, kind, title, text, link}` for an id that the index holds, and 404 for everything else: the id must match `[a-z0-9-]{1,80}` and be a key of the index, so a path, `..`, an encoded slash or a file name is never looked at on disk. Blog passage ids are `post-<10 hex of sha1(name)>-<n>`.
- **What goes in the notes.** Only text that could be on a public page: the notes are sent to a third-party model. Nothing about keys, deployment, the admin dashboard or security. Edit a note, rebuild and deploy; a missed question is fixed by adding the missing word to the note's `keywords`. The privacy page says that Genie may add this text.
- **Not built, and where it would go.** Matching by meaning (embeddings) would replace the scoring inside `Index.Search`; an admin card for editing notes would add notes from the settings store next to the compiled-in ones in `theKnowledge`.

## 4. Genie assistant (chat widget)

- **Opening:** on the home page Genie no longer opens on load. It opens from the app bar's Ask Genie button, the floating button, or the note that appears after the first error in a terminal, which fills in that error (`setupGenieErrorNudge` in `scribbler.js`). On `/practice` it still opens on load, because it plays the interviewer.
- **Panel:** a dark panel docked to the bottom-right corner (a sheet across the bottom on phones), styled after the mockup's "Genie panel" (`chat-widget/src/widget.html` and `widget.css`). The header shows the title, "Reads <file>" from `#editor-filename`, the Peer chat switch and a close button. It is not modal: the page sets `closeOnOutsideClick = false`, so there is no backdrop or focus trap, and you can keep typing in the editor while it is open. It closes with the × button, Esc, or the app bar button (`ChatWidget.toggle`). While it is open, `body.genie-open` hides the floating "Ask Genie" button (`.genie-fab`) and marks the app bar button as pressed. Drag the top-left corner to resize it. Setting `closeOnOutsideClick = true` brings back the old modal behaviour.
- **Model and effort:** a chip in the composer ("Luna · Low") opens a small panel with a card for each model in two sections, OpenAI (GPT-6 Luna, GPT-4o mini) and OpenRouter (Gemma 4 31B, listed only when the server has an OpenRouter key: `config.js` sets `openrouter_enabled`), and four effort levels (Off, Low, Medium, High). The default is Luna with Low. The effort control is dimmed for 4o mini and Gemma, which do not think first, and the chip is hidden while Peer chat is on (those messages go to people, not a model). Under each reply a caption names the model and effort (or, for Gemma, "Gemma 4 31B · OpenRouter") that answered, and while Luna thinks the waiting bubble says "Luna is thinking…". Keyboard: Enter opens the chip, arrow keys move through the cards and levels, Esc closes the panel first and Genie second. The two lists, the saved choice (`localStorage` key `genie-model`) and the request fields come from `js/model-choice.js` (`window.ModelChoice`), loaded before `chat-widget.js`; the widget only draws them. Luna's request carries `reasoning_effort` and `max_completion_tokens`, 4o mini's `temperature` and `max_tokens`. The gpt-3.5-turbo that was once the widget's default is not available to every OpenAI project (a key can work for `gpt-4o-mini` and be refused for it with "Missing scopes: model.request").
- **Context:** a system message on every request with the language, the file name, the live editor code (cut at 12,000 characters) and the most recent 20 lines of the active terminal (cut at 4,000 characters, read through `recentText` of the xterm adapter, else from the DOM rows), followed by the conversation history. The header says "Reads <file> + terminal". Everything in it is sent to OpenAI, which the privacy policy says.
- **Site knowledge:** the request carries `context: "chat"` (not on the practice page), and a reply that came back with `X-OpenREPL-Context` gets the "Used OpenREPL notes" line and its note card (section 3a). The widget no longer carries a page-text dump or a documentation list in its system messages (`openreplkeywords.ts` was removed); the facts come from the server's notes, only when they match.
- **History sync:** every message is pushed to Firebase `chat-list/<dbpath>`. Shared viewers see the same conversation, and the master's `cleanup()` deletes it.
- **Output handling:** fenced code blocks get Insert and Replace file buttons, which call `window.insertcodesnippet` and `window.replacecodesnippet`. The payload is UTF-8 base64 (`btoa(unescape(encodeURIComponent(code)))`), decoded by `decodeGenieCode` in `index.html`, so non-ASCII code no longer breaks the reply. Replace is blocked in practice mode.

## 5. Practice question generation (`common.js`)

| Function | Prompt → output |
|---|---|
| `generateNewQuestion(topic, difficulty, customPrompt, languages)` | Asks for a DSA problem that is not in the stored titles, as JSON `{title, description, code_templates{lang:{template, multiline_comment_start, multiline_comment_end}}}`. `sanitizeJSONString` extracts the JSON. The function then adds `id`, `nameHyphenated`, `topic`, `difficulty`, `added` and `delimeter`. |
| `getCodeTemplate(nameHyphenated, language)` | Fetches or generates the template for a language not yet in `code_templates`, and caches it in the stored question. |

- Both go through `requestJSONFromOpenAI`, which sends the chosen model's fields (`questionRequestFields()`: the same choice as Genie's, with 3,000 more tokens of room for a long answer, and `temperature` from `localStorage.temperature` for 4o mini) and asks for JSON mode (`response_format: {"type":"json_object"}`), so a quote or a newline inside the text cannot make the reply unparseable. If the API answers 400, the request is sent once more without JSON mode and `sanitizeJSONString` tidies the reply. The prompts hold no `//` comments in their example JSON.
- The New question dialog has Model and Effort selects (`#qmodel`, `#qeffort`, filled by `initQuestionModelFields` in `common.js`) on the home page and on the practice page. They read and write the same saved choice as Genie. With Luna the Creativity field is disabled (Luna takes no temperature); with 4o mini and Gemma the Effort select is. The Model select groups its options by provider, like the panel.
- Topics come from the `topics` array in `common.js` (for example Two Pointers, DP, Graphs and System Design).
- Results are saved through `PracticeStore` in `localStorage.questions`, which keeps the latest 100, and for signed-in users in their account (LLD 05, 06).
