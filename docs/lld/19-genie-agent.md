# 19. Genie agent and chat: efficiency, credits and plans

Built on branch `feature/genie-agent-efficiency`. The plan and the screens it was built from were on the design canvas "Genie Agent End Results". This document says what was built, where it lives, what it costs and what is not built.

Related: 07 (the `/chat/completions` proxy and the usage line), `docs/agent-mode-design.md` (the agent's protocol, permissions and Stop), 13 (admin).

## 1. The usage record, credits and plans (phase 0)

Every answer of a model is one **usage event**: counts only (tokens, credits, model, task and step), never a question, an answer, a file or code. `server/meter.go` is the one place.

- **Credits.** One credit is a tenth of a cent. A model's credits for a thousand tokens come from `GenieSettings.CreditWeights[model]` (sent, written), else from its `ModelPrices` (a dollar for a million tokens is a credit for a thousand), else from the cost the provider reported (OpenRouter), else 0. `creditsFor`.
- **Where it is counted.** `chatproxy.go` calls `noteMeter` next to `tokenTotals.add`, after the answer was sent. A step of a task is keyed by task and step (`meterLedger.seen`), so a counted step is not counted twice. A step the provider failed has no usage and is not counted.
- **Where it is stored.** The same database as the token totals (`genie_usage.db`, `tokens_db_handle`), under `m:<key>`; `meterRecord` holds this month's credits, steps, tasks and chat answers, and the all-time credits. The record also holds an optional `Plan` of that person. Counts for the admin ("How the agent is doing") are one record under `m:stats` (`agentStats`).
- **The plan.** `entitlementsOf(g, uid, admin)` is the one place that says what a person may do: credits for a month (0: no cap), credits for one task (0: none), the steps of a stretch, tasks an hour. The default plan is `GenieSettings.PlanName/PlanCredits/PlanBudget` with today's limits for the rest; a person's own plan (`planRecord`, set with `POST /admin/genie-plan`) replaces the numbers.
- **Shadow mode.** The record is on unless `MeterOff`. Nothing is charged or limited until an admin turns `CreditsOn`; users then see credits (`creditUsage` in the `X-OpenREPL-Usage` header and `GET /chat/usage`), a used-up month refuses with `credits_exhausted` (429, before a task is taken), and an admin is never limited. Guests keep their fixed request allowance and have no plan.
- **A task's own credits.** `agentTask.credits`; `GET /chat/usage?task=<token>` returns them for the user's own task.

Admin (`admin.js`): "Usage record and credits" in Site settings (switches, plan name, credits a month and a task, a weight per model next to the prices) and "How the agent is doing" in Genie usage: counts since the server began, and a baseline the admin records before a change. `GET/POST /admin/genie-measures`, `POST /admin/genie-plan`.

## 2. Sending less (phase 1)

`agent-task.ts` (plain code, tests in `test/agent-task.test.ts`):

- `compactHistory`: the last two results and answers of a task stay whole; older results are one line each (what each action did, not its output) and older answers lose the code they wrote. The first message is never touched; the page keeps the whole history.
- `contextToSend`: the editor and terminal text is sent again only when it changed; otherwise one line says so.
- Chat (`chat-send.ts`, `messagesToSend`): older answers lose the middle of long code blocks, and the editor and terminal text is not sent twice (the history already holds it at each message).

## 3. The task card (phase 1)

- **Plan.** The model sends `"plan": [...]` in its first step and `"plan_done": [n]` later (`parseStep`, `PlanState`). The card shows it above the steps.
- **Check before done.** `WorkTrack` follows accepted changes (`editor_write`, `editor_insert`, `editor_replace`) and what ran after them (`run`, `debug`, `bash`, `terminal_type`). A step that says `done` after a change that was not run is sent back once (not at the last step); a change after a check is a fix attempt, two at most while a check is known to fail (the page refuses a third edit and tells the model to say what fails). The card ends with Checked or Not checked, what changed (lines added and removed against the editor before the first change), and the steps, time, tokens and credits used.
- **Batching.** A prompt rule: independent reads in one step, up to three actions.
- **Lenient reader.** `extractJSON` mends a trailing comma, a comment and a BOM when an answer cannot be read as it is (it already took a fence and text around the JSON).
- **Outcomes.** The page reports how a task ended (`POST /chat/agent-outcome`: done, stopped, failed, unreadable, toolong; whether it changed code and checked it) for the counts in section 1. The server counts a task once, for its own user.

## 4. Editing a part and looking around (phase 2)

- `editor_replace` (`old` and `new`, or `start_line`, `end_line` and `new`): the page builds the file with that one part changed and goes through the same review as `editor_write` ("Genie wants to change this part of the file"). An `old` that is not found or is found twice is refused with the reason.
- `files_search` and `files_read`: read-only, no permission question. `POST /agent_files` (`server/agent_files.go`, execution-bound in the gateway like `agent_bash`): owner of a signed-in workspace only, paths relative to the workspace with no hidden name and no link out of it, text files up to 1 MB, 5,000 files and 4 seconds for a search, 40 matches, about 90 lines (3,600 characters) for a read with `next_line` to go on. It reads the file as saved on disk.
- Chat: **Apply as diff** under a code block that starts with a definition (`js/src/page/lib-region.mjs`): the definition is found in the editor by name and only that part is proposed.

## 5. Carrying on, notes and undo (phase 3)

- **Carry on.** The steps of a task are a stretch (the admin's `agentMaxSteps`, at most 8). When a stretch is used and work is left, the card pauses and asks. Yes sends `agent_carry`: `agentLedger.nextWith` starts a new stretch (steps count from 1, the task's own count and credits go on, the 15 minutes start again), at most three times (`maxAgentCarries`). The ceiling is never raised.
- **Budget.** With credits on and a plan with credits for a task, a step that would pass it is refused (`agent_budget`, 429) and the page asks; yes raises the task's budget by one more budget (it counts as a carry).
- **GENIE.md.** Read at the start of a task through `files_read` (first 80 lines, 1,500 characters at most), sent as hints inside the first message of the task. It adds up to that many characters to every step.
- **Undo this task.** `AgentRunner.undoTask`: each file the task changed is put back as it was before the first change, through the editor's own history, when nobody changed it since the task; a file that was open before is opened again. Files the task created are left.

## 6. Streaming and native tool calls

- **Streaming** (admin switch `StreamAnswers`, off by default). `sanitizeChatBody` keeps `stream` only when the switch is on and the model's host reports usage in a stream (not Cloudflare Workers AI). The proxy flushes each piece (`copyFlushing`; `logResponseWriter.Unwrap`). The page reads events (`sse.ts`), draws a chat answer as it grows, and shows the words of an agent step (`partialSay`) in the card while the JSON is written. A chat answer is told to the others of a shared session when whole. The 4 MB cap and the usage count work as for a whole answer.
- **Native tool calling** (admin switch `AgentNativeTools`, off by default, and a list of models, `NativeToolModels`). For a model on the list an agent step is sent with one function `step` whose parameters are the step object, the call forced and the JSON mode left out (`agent_tools.go`); the page reads the arguments of the call exactly as it reads the text. Until an admin saves a list, the list is the OpenAI models (`nativeToolModels`); the admin ticks one box for each model in Site settings, and an empty list is a choice (no model).
  - **Fallback.** If the host refuses a call that carried the tool (HTTP 400 or 422, or an error whose text is about tools or `tool_choice`), the proxy sends the same request once more as text JSON (`plainBody`: no tools, `response_format` json_object unless the model is `NoJSONMode`) and remembers the model as "no tools" in memory for an hour (`toolMemory`), so later steps skip the failed try. The user sees no error. The refused call is not an answer: no usage event, no step counted, no request charged (the first step's charge happens once, for the answer that came). Any other error (429, 5xx without tool words) is shown as it always was and is not remembered. A host that accepts the call and answers plain text needs nothing: the page reads both.
  - **Counts.** `agentStats` keeps the tries (`toolTries`), the refusals that went again as text (`toolFallbacks`) and the refusals of each model (`toolRefused`); Genie usage shows them in "How the agent is doing".

## 7. Not built, and limits

- One plan per person is stored and read, but there is no screen to set it yet (only the admin API).
- No balance or payment: credits are counted and can limit, nothing is sold. Two tabs spending at once, a server that restarts in the middle of a task and several gateways are not settled (the ledger of tasks is in memory, as before; the credits are stored).
- "Review changes" on the finished card (canvas) is not built: the review of each change is the one that was shown when the change was made.
- Streaming and native tool calling are not tried against the real providers in this change (the fallback is tested with a fake host that refuses the tool).

## 9. Chat history and chat tabs

Built after the design on the canvas "Genie Chat History". A chat is saved in its owner's own workspace, so it lives as long as the workspace does and follows it (snapshots, other devices); no server database holds anyone's conversations.

- **Files.** `.genie/chats/<id>.jsonl`, one JSON Lines file for each chat, and `.genie/tabs.json` (`{"open":[ids],"off":bool}`). A line is one thing that happened and lines are only added: `meta` (id, created, context `chat` or `practice:<question>`), `title` (the last wins), `state` (mode, model, effort; the last wins), `user`, `answer`, `task` (counts and names, no content). A line cut short by a crash is skipped when read. The editor and terminal text read for an answer is not saved. Export as Markdown is a view of the file, made by the server.
- **Server** (`server/genie_chats.go`, `POST /genie_chats`, execution-bound in the gateway): `state`, `read`, `append`, `tabs`, `off`, `delete`, `deleteAll`, `export`, `exportAll`. Only the owner of a workspace (a shared session's viewer names the owner's workspace and is refused, `not_owner`); guests have a workspace too unless `ChatHistoryNoGuests`; the whole feature is off with `ChatHistoryOff`. Lines are checked (kinds, allowed keys, 64 KB a message, 20 lines a call), the folder and its `chats` folder must be real folders of the workspace (checked before anything is made, so a link out is never followed), `tabs.json` is written whole and renamed into place. Limits (admin, Site settings): 50 chats, 8 open tabs, 200 KB a chat, days kept (0: for ever). Over its size a chat loses its oldest messages and keeps its head; over the number of chats the oldest closed chat goes, never an open one. 240 calls a minute for a person.
- **Panel** (`chat-tabs.ts`, `chat-store.ts`, `index.ts`): a strip of tabs under the header (+, a ⌄ list of every chat with search and Reopen, a menu on right-click, double-click to rename, drag to move, middle-click to close; keys Alt+T, Alt+W, Alt+arrows, Alt+1 to 8, Alt+H while the panel has the focus). Each tab has its own conversation, its own mode, its own draft (kept in this browser) and its own recap of agent tasks. The messages of the tab that is shown are in the panel; those of the others wait in a fragment, so an answer that arrives for a tab that is not shown goes to its own tab and leaves a dot. A ring means a task is running there, an amber dot that it needs the user (a toast offers Go to tab), a coral dot that an answer is ready.
- **One task at a time.** A task works in the editor and terminal, so only one runs in a workspace: while a tab runs one, Agent is off in the other tabs (with the reason on hover) and Send does not stop it from there. Chat is free in every tab. Closing the running tab asks. When a tab is shown again and the editor has another file or language than the chat was about, a note says so.
- **Writing.** Lines go through a queue (`ChatStore`), in order; when the workspace cannot be reached they wait and are tried again (every 8 seconds, when the browser is online again, or `ChatWidget.retrySave()`), and the strip says "not saved right now". A viewer, a site with chats off and a person who switched saving off get a single tab and no strip. Signing in or out starts again from the other workspace's chats.
- **Profile page** (`js/profile-chats.js`): the number and size of the chats, "Save my chats", "Download all as Markdown", "Delete all chats" (asks once). **Admin:** Site settings, "Genie chat history".
- **Checked in the code:** a "fork" is a second terminal in the same workspace (not a copy), so there is nothing to leave `.genie/` out of; a guest's workspace is not the one they get when they sign in, so guest chats do not follow a sign-in.
- **Not built:** the admin's count of how many people have chats and the space they use (an admin cannot read a chat, and the counts would need a scan of every workspace); a chat per question on the practice page is saved with its `context` but the History list there is not filtered by it yet.

## 8. Tests

Go: `genie_chats_test.go`, `agent_tools_test.go`, `meter_test.go`, `agent_files_test.go`, `agent_stream_test.go`, `agent_prompt_test.go`, additions in `agent_test.go`, `chatmodels_test.go`, `tokens_test.go`, `knowledge_test.go`. Widget (`node --test test/` in `src/resources/chat-widget`): `agent-task`, `chat-send`, `sse`, `chat-tabs`, `chat-store`, `agent-protocol`, `tokens`, and `agent-compiled` (the runner as the build tool leaves it, with a scripted model). Page script: `js/test/lib-region.test.mjs`.

Browser (`e2e/tests`, with `GENIE_LOCAL_ASSETS=1` before an image is built): `genie-efficiency`, `genie-chat-tabs` (the strip, a reload, closing and reopening, rename and delete, a background answer, one task at a time, the keys, the stale note, a workspace that fails, a viewer), `genie-profile-chats`, `genie-admin-credits`.
