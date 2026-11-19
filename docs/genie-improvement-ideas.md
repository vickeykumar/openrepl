# Genie: improvement ideas (review list)

Written 2026-11-19 from a multi-role review (architect, UX researcher, UI/UX designer, frontend engineer, backend reliability and feature engineer).
Status: the **top five were built on 2026-11-19** (owner: "build all five as per ur recommendations as per canvas"), uncommitted until approved; LLD 19 section 12 says what was built and what differs from the canvas. The rest of this list is proposals only: each item needs a slot (date and start time inside working hours), a plan, and the owner's approval before any build.

## Top five (ranked)

1. **Failure log from real use** (backend / architect) — changed 2026-11-19 by the owner from a scheduled test set (it would spend real tokens, and the free OpenRouter limit is 50 requests a day).
   On the free plan Genie reports failed steps, "stuck" events and every task outcome to a separate `genie-events.log`; the admin gets a "Genie" log section with filters, a pass rate per model and a download. Metadata only (model, action, file extension, short scrubbed error, counts), never the user's text, files or name. The privacy notes say so. Paid plans later: no collection, and a report for the admin (not now). Canvas: claude.ai/artifact/6NKLrHQDc5M1Jfo4YMp3QP. A small fixed test set built from the worst logged failures stays a later idea.
2. **Prompt built from what is needed** (architect / performance)
   The agent prompt lists about 45 actions and a long guide on every step. Send only the groups that matter (preview actions only when the pane is open, file actions when there are files). Fewer tokens, less confusion for small models.
3. **Resume after reload or disconnect** (backend)
   A running task lives in the page; a reload, dropped connection or closed tab loses it. Save a checkpoint in the chat file and offer "Resume this task".
4. **"Stuck" detection with a one-click fix** (UX)
   When the same step fails three times (like the `editor_replace` loops), stop and offer "Try this step with a stronger model" instead of burning the step budget.
5. **"Review all changes" panel** (UX / UI)
   One list of every file the task changed, with accept, reject and undo per file. The design exists in the canvas but was not built.

## UX research

- **Show what Genie sees:** a "Context" view listing editor, terminal, preview, notes and chat history with a size estimate. Explains failures like "it can't see my terminal" and builds trust.
- **First-run help:** a short tour of Chat versus Agent, the model picker and the starter chips. The /genie page exists but nothing leads a new user to it at the right moment.
- **Search across chats**, and pin or star a chat.

## UI / UX design

- **Calmer task cards:** collapse finished steps with a one-line summary so a 30-step task stays readable.
- **States:** loading, empty and error states for every Genie surface, in dark and light. The light theme is not yet checked for the waiting bar, the arrow, the preview cursor and the gutter grips.
- **Auto model choice:** a router that picks a small model for plain questions and a strong one for edits, so users need not understand the picker.

## Frontend engineering

- **Split the widget:** `index.ts` is about 4,000 lines and every change touches it. Split into tabs, store, tasks, picker.
- **Load agent code only in Agent mode** to shrink first load.
- **Long chats:** check scroll and render cost with hundreds of messages.
- **Accessibility pass** on the new elements (waiting bar, arrow, chips, cursor); extend the existing a11y spec to Genie.

## Backend performance and reliability

- **Provider fallback:** if OpenRouter or Cloudflare fails or is slow, try the next model with a visible note; add a circuit breaker.
- **Health in admin:** a Genie health card with time to first token, error rate by model and by reason, and an alert threshold.
- **Credits:** decide when to leave shadow mode; per-task budget and a clear "left" line.

## Backend features

- **"Remember this" writes to GENIE.md,** so project notes grow from use.
- **Test-runner awareness:** detect the project's test command and return failures as structured results.
- **Read-only replay link** for a task, to share what Genie did.

## Still open from the preview-agent work (not asked, for the record)

- A browser test for the admin toggle "Genie may act in the Preview".
- Real-model checks of the preview agent, the IDE guide and the weak-model fixes.
- Light-theme check of the new UI (waiting bar, arrow, cursor, grips).
- The Go server must be rebuilt and restarted (8082) and the page hard-reloaded to see the bridge and cursor.
- Closed by the owner (do not start): preview phases 3 and 4, sign-in half state (PR 158), Pi worker apt bug, fork "Connection lost".
