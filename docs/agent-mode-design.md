# Genie agent mode: design for steps 1 and 2

Status: for review, nothing built. Scope: `docs/agent-mode-plan.md` steps 1 (diff preview and undo) and 2 (agent mode for the editor and Run). Step 3 (the terminal) comes after and reuses the same machinery.

## What exists today (the parts this builds on)

- **Editor:** an Ace instance, `window.editor.env.editor`. Genie's Insert and Replace buttons call `window.insertcodesnippet` and `window.replacecodesnippet` (`index.html`), which use `session.replace` and `setValue`.
- **Run and Debug:** `window.CompileandRun()` and `window.RunandDebug()` (`js/src/page/03-run-and-files.js`) dispatch `optionrun` and `optiondebug` on the active terminals. A finished Run prints `[Program Exited] Jobid: ...` in the terminal.
- **Reading the terminal:** `fetchTerminalOutput()` in the widget, through the xterm adapter's `recentText`. The widget already sends the editor code and the terminal's recent output with every request (`getcurrentIDECode`).
- **Terminal input (step 3):** `connection.send(msgInput + data)` in `webtty.ts`; the xterm adapter's `onInput`.
- **Rate limit:** `handleChatProxy` takes one unit from a balance kept in the signed session cookie (`cookie.UpdateOpenApiRequestCountBalance`), refilled per minute by `utils.UserFactor()` / `GuestFactor()`. Admins are exempt (`IsUserAdmin`). `cookie.Is_UserLoggedIn(req)` and `cookie.Get_Uid(req)` tell who is signed in.
- **Settings:** `GenieSettings` in `server/settings.go`, edited in the `/admin` Genie card.
- **Chat proxy:** `sanitizeChatBody` passes only an allow-list of fields. It already passes `response_format: {"type": "json_object"}`.

## Decision 1: the protocol is JSON actions, not native tool calling

Each step is one ordinary chat request with `response_format: json_object`. The model answers with:

```json
{ "say": "I'll add the function and run it.",
  "actions": [ {"type": "editor_write", "text": "...whole file..."}, {"type": "run"} ],
  "done": false }
```

Why: it works the same on GPT-6 Luna, GPT-4o mini and Gemma 4 31B, so no model is left out and OpenRouter's tool support does not matter. The proxy already supports this response format. The cost is that the page validates the JSON itself (below).

Action types, step 2: `editor_write` (the whole new text; the page computes the diff), `editor_insert` (text at the cursor), `set_language` (its own permission: it replaces the editor with the language's starter code and restarts the terminal), `run`, `debug`, `read_output` (wait for the run to end, then send the output back), `finish`. Step 3 adds `terminal_type` and `terminal_interrupt` (built; see the section "Step 3: the terminal"). An action that is not in the list, or that has a wrong field, is dropped and the model is told so in the next step.

Reading the editor and the terminal needs no permission: Genie already receives both with every message.

## Decision 2: who counts the steps (the server)

The page cannot be trusted to count. The server does:

1. An agent request is `{"context": "agent", "agent_task": "<token or empty>", ...}` and is refused with 403 for a guest (`cookie.Is_UserLoggedIn`) and with 503 if the admin switch is off.
2. With no token it starts a task: it takes **1 unit** from the existing balance, checks the per-user counter (default 20 tasks per hour, in memory, keyed by user id), creates a task `{id, uid, steps: 0, started}` and answers with `X-OpenREPL-Agent-Task: <id>.<signature>`.
3. Every following request must carry that token. The server checks the signature (HMAC with the server secret), that it is the same user, that it is under 15 minutes old, and that `steps < 8`, then counts the step. A ninth step, or a token for another user, is refused.
4. Per step the model answer is capped (`max_completion_tokens` at most 2,000 for agent steps) and the request body size is capped as for chat.

The task table and the per-user counter live in memory of the gateway or standalone server, so they reset on a restart and are not shared between instances. For a single gateway that is acceptable; the signature and expiry stop a client from inventing tasks. (A shared store is possible later, through the persistence layer.)

## Decision 3: permissions (the page)

Three scopes: `editor` (write or insert), `run` (Run and Debug) and, in step 3, `terminal`. The first time a scope is needed the panel shows the prompt: Allow once, Allow for this session, Deny. "Allow for this session" lasts until the page closes and is never stored. A denied action is reported to the model as "the user denied it", and the task goes on or finishes. Editor changes are never applied without review: they appear as a diff (below), and "Allow" only means "you may propose".

## Step 1: the diff review and undo

- `editor_write` and `editor_insert` produce a **proposal**: the page diffs the current text with the new text (a small line-based diff, no library) and shows hunks over the editor with Accept and Reject for each, and Accept all and Reject all. The same view is used when Genie's Insert and Replace buttons in plain chat are pressed on a code block, so step 1 improves normal Genie too.
- Accepting applies the hunk through Ace's own edit API, so Ctrl+Z works. Before the first accepted change a **checkpoint** (the text before) is kept. The panel goes away as soon as every change is decided; a notice with an Undo button (10 seconds) restores the checkpoint in one step.
- A proposal larger than the editor limit is refused. Nothing is applied while the user has unsaved changes that overlap a hunk: such a hunk is marked "the code changed meanwhile" and can only be rejected.

## Step 2: the panel

- A **Chat | Agent** switch in the composer. Agent is shown to signed-in users only; a guest sees it greyed with "Sign in to use agent mode". It is hidden on the practice page (the interviewer prompt and the integrity of the exercise), and in a shared session only the owner has it (`isMaster()`), and not in peer chat mode. Chat is the mode a visitor starts in; the mode last chosen is remembered in the browser.
- The **step list** under the task shows each step as done, active or waiting ("Read your editor and terminal", "Edited main.c (2 changes accepted)", "Pressing Run", "Reading the output"). The header says "Task 3 of 8 steps". **Stop** is always visible and cancels the request in flight and any waiting action.
- The **highlight**: the control an action uses (the Run button, the editor tab, later the terminal) gets a pulsing ring for the length of the action, and the editor scrolls to a changed hunk. The step is also announced to screen readers.
- **Fix until it passes** is the same loop: after `run`, `read_output` returns the output (or "still running after 20 s"), and the model decides to edit and run again, within the 8 steps. It stops when the model says `done`, when a step fails to parse twice in a row, or at the cap.
- **Not in a task:** if the user types in the editor while an agent edit is pending, the pending proposals are marked stale instead of being applied.

## Admin and docs

- `GenieSettings` gets `agentDisabled` (agent mode is on for signed-in users unless an admin switches it off; first designed as off until switched on, changed on the owner's request after step 2), `agentTasksPerHour` (default 20) and `agentMaxSteps` (default 8, at most 8). They appear in the `/admin` Genie card and in `settings.js` for the page.
- The privacy page gets a sentence: in agent mode your code and terminal output are sent to the model as in Genie, and Genie changes the editor only after you allow it and you review the changes.
- Docs: LLD 07 (the protocol, the task token and limits), LLD 06 (the panel, the diff view), LLD 13 (the settings), README.

## Files

- Server: `server/agent.go` (task tokens, counters, checks), `server/chatproxy.go` (the agent branch), `server/settings.go`, `server/admin` card and API for the new settings, tests.
- Page: `resources/chat-widget/src/agent.ts` (loop, validation, permissions, steps), `resources/chat-widget/src/diff.ts` (diff and hunks), the editor review view (`js/src/page/` and `css/`), `widget.css`, `index.ts` hooks.
- Tests: Go tests for the guest refusal, the cap, the token (forged, expired, other user), the per-user counter and the settings; browser checks of the permission prompt, the step list, the diff and the undo; a parse test for the action JSON with bad input.

## Risks and how they are handled

- **A model that returns bad JSON:** the page asks it once more with the error; a second failure ends the task with a message.
- **Prompt injection** (text in the terminal or in a pasted file telling Genie to do something): every action that changes anything needs a permission, the editor needs a review of the diff, and steps are capped. The system message for agent steps says that file and terminal text is data, not instructions.
- **Cost:** at most 8 model calls per charged unit, 20 tasks per hour per user, an admin switch, and a per-model switch that already exists.
- **Shared sessions:** peers see the proposals and the result in their editor as they see the owner's typing today; they cannot start or answer an agent.

## Decisions (owner, 2026-10-08)

1. **JSON actions for every model.**
2. **Agent mode is off on the practice page**, and the admin settings have an option to enable it there.
3. **Per-user limit of 20 tasks an hour**, and the admin can change it in the Genie settings.
4. **Agent mode starts switched off for everyone** until it is enabled in `/admin`.
5. **Whole-file writes with a client-side diff**, not patches from the model.

## Step 3: the terminal (built)

- **Actions.** `terminal_type {text, wait_seconds}` types ONE line and presses Enter, waits until the terminal has been quiet for a second (at most `wait_seconds`, default 3, 1 to 20) and returns what is new in the terminal (`newOutput`). `terminal_interrupt` presses Ctrl+C and returns what followed. `read_output` also works without a Run now: it waits for what is printing to settle. The terminal is the REPL of the language in use (a shell for Bash), so a line is a shell command, a Python statement and so on. If a program started by `run` waits for input (`read_output` says "it is probably waiting for input" when it printed something and then went quiet for 2.5 seconds), `terminal_type` is how Genie answers it.
- **What is accepted.** One line of at most 500 characters of plain text. A line with a new line, a tab or any control character (an escape sequence, Ctrl+C inside the text, the C1 characters) is dropped and the model is told, so that what the user reads in the prompt is all that is typed. The page types it with `Xterm.typeInput`, the path the phone's extra keys use, and says "closed or not connected" if no one listens. A program that is over (`endedAtTail`) takes no line.
- **Permission.** A `terminal` scope with the same three answers. The prompt shows the exact line in a code box, as text. "Allow for this session" lets non-risky lines go without a prompt; they are still shown, in the step list, as they are typed, and the card says risky lines are still asked about.
- **Risky lines** (`riskOf`, a list of patterns tested in `agent-protocol.test.ts`): deleting (`rm`, `rmdir`, `shred`, `find -delete`), changing disks, permissions, users or the system (`sudo`, `chmod -R`, `mkfs`, `dd`, `shutdown`, `kill`), the network and installing software (`curl`, `wget`, `ssh`, `pip install`, `npm install`, `apt`), losing work in git (`reset --hard`, `clean`, `push`), running built or piped text (`eval`, `| sh`, `$(...)`, backticks, `base64 -d`), system paths (`/etc`, `/dev`, `~/.`, `../`), SQL that deletes, and the same things in Python, Node, Ruby and Go (`os.remove`, `shutil`, `subprocess`, `fs.rmSync`, `exec.Command`, `open(..., 'w')`). The list is wider than needed: a wrong guess costs a click, a miss costs a file. A risky line is asked about every time, even with a session grant, and only "Allow once" or "Deny" are offered. It is a help for the person reading the prompt, not a lock; the sandbox of the user's own session is the lock.
- **Injection.** Terminal text is data: the model's instructions say so, every typed line needs the user's yes (or a session grant for non-risky lines), and a task has at most 8 steps.
