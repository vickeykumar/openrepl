# Genie agent mode: task list

Status: step 1 built 2026-10-08 (diff review and undo, uncommitted until approved); steps 2 and 3 not started. Agreed 2026-10-08. The design (with mockups) is the first piece of work and is reviewed before any code.

## Decisions

- **Who can use it:** signed-in users only. Guests see "Sign in to use agent mode". Admins are exempt from the limits.
- **Cost:** 1 unit of the existing rate limit per task, up to 8 steps per task, and a token cap per step.
- **Models:** all three (GPT-6 Luna, GPT-4o mini, Gemma 4 31B), with one JSON action format that works with any of them.
- **Safety:** read-only by default. Genie asks before each kind of action.

## Step 1: Diff preview and undo

1. Genie's code suggestions show as a diff in the editor, with Accept and Reject per hunk.
2. Every applied change is a checkpoint, with a one-click "undo what Genie did".

## Step 2: Agent mode for the editor and Run

3. **Server (chat proxy):**
   - Agent requests carry a fixed set of actions defined on the server: write or replace editor text, open or create a file, press Run or Debug, and read the output.
   - One unit is charged per task, the 8-step and per-step token caps are enforced, and tasks are counted per user on the server (for example per hour), so clearing cookies does not reset the limit.
   - Agent requests from guests are refused.
   - The model answers in JSON actions for every model (no native tool calling); the page validates them (`docs/agent-mode-design.md`).
4. **Chat panel loop:** request, then action, then result back to Genie. A Stop button is always visible.
5. **Permission prompts:** Allow once, Allow for this session, or Deny, per kind of action.
6. **Visible actions:** a highlight on the control Genie uses, and a live step list in the chat ("Typing in editor", "Pressing Run", "Reading output").
7. **Fix until it passes:** run, read the error, edit, rerun, within the 8 steps.
8. **Shared sessions:** only the session owner can start an agent. Peers see the changes as they see typing.
9. **Admin:** an agent-mode switch in `/admin` next to the model switches (off by default), a switch to allow it on the practice page (off by default), and settings for the tasks per hour (default 20) and the steps per task (default 8, at most 8).

## Step 3: Terminal

10. Genie types a line into the terminal and can press Ctrl+C. It shows the exact text before running it, and always asks for risky commands such as `rm -rf`.
11. It reads the terminal output back to continue the task.

## With each step

12. **Privacy page:** a sentence that agent mode sends the code and terminal output to the model and acts only after the user allows it.
13. **Tests:** the actions, the limits, the permissions, the guest refusal and the diff handling, plus browser checks of the prompts and the step list.
14. **Docs:** LLD 07, 06 and 13, and the README.

## Not included (separate features, later)

- Inline completions as you type (ghost text, accepted with Tab).
- Right-click actions on selected code (explain, fix, add comments, write tests, convert).
- Formatting and linting.
- A stdin box.
- Practice integration (hints without spoilers, solution review, complexity).

## Order of work

Step 1, then step 2, then step 3. Each is reviewed and committed before the next one starts. The design for steps 1 and 2, with mockups of the diff view, the permission prompt and the step list, comes first.
