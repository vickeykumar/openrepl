# 21 · The Genie canvas (Preview pane)

Built 2026-11-25 from the ten boards of the canvas design "Genie canvas in the Preview pane" (claude.ai/artifact/X6MoKAe6ciH2n7vDJTmzgC):
boards 1 to 6 are the design, boards 7 to 10 list the tasks of phases 1 to 4.

## What it is

A tab of the Preview pane, beside the pages: an endless, zoomable surface of **boards**. A board is one plain HTML page. Genie draws boards
with its `canvas_*` actions; the user pans, zooms, moves, sizes, retypes text, pins notes, duplicates, deletes, exports and links boards.

## Where things live

| Part | File |
|---|---|
| The tab in the pane (a pseudo-tab after the page tabs, `canvas-root` in the pane, `is-canvas` class hides the page bar) | `src/js/src/page/27-preview.js` (`showCanvasTab`, `leaveCanvas`, `closeCanvasTab`), `src/resources/index.html` |
| The canvas page script (view, tools, boards, pins, versions, export, Genie's actions) | `src/js/src/page/37-canvas.js` |
| Pure code with node tests (zoom, fit, snapping, minimap, zip, board document, what Genie is told) | `src/js/src/page/lib-canvas.mjs`, `src/js/test/lib-canvas.test.mjs` |
| The server: one endpoint, `POST /genie_canvas` | `src/server/genie_canvas.go` (+ test) |
| Genie's actions | `agent-protocol.ts` (`canvas_open`, `canvas_board`, `canvas_arrange`, `canvas_read`, `canvas_note`), `agent.ts`, `index.ts` (chip, context), `src/server/agent.go` (prompt, group "preview") |
| The admin switch | Site settings, Preview: "Genie may draw on the canvas" (`SiteSettings.CanvasOff`, public `canvasOff`) |
| The user guide (a page of its own, linked from the docs page `doc.html`) | `src/resources/docs/canvas.html`; the Genie side of it is the short section `#canvas` of `genie.html` |
| Styles | end of `src/resources/css/ui-refresh.css` (`.cv-*`) |

## Storage (rule 1 of board 1: the canvas is a view of a folder)

```
.genie/canvas/<name>/canvas.json            layout: boards (frame, title, current version), pins, last changes, revision
.genie/canvas/<name>/boards/<board>.html    the page of a board, as shown now (open it alone in Preview, copy it, edit it)
.genie/canvas/<name>/versions/<board>/<n>.html   every version (30 kept), for Undo, Redo, Compare, Restore
```

A workspace can hold several canvases (`<name>`; the first is `main`). Snapshots and restore of the workspace cover the folder. Limits: 24 boards
per canvas, 512 KB of HTML per board, 200 pins, zoom 10 to 400 %.

## The endpoint

`POST /genie_canvas {"op": ..., "canvas": "main"}`. Ops: `list`, `open`, `state` (with `since` the revision: `unchanged` when nothing changed),
`read` (`n` for an older version), `put` (a new version; by `genie` or `user`), `layout` (move and size, not a version), `undo`, `redo`, `restore`,
`versions`, `duplicate`, `delete`, `pin`, `unpin`, `pinsent`. It runs where the workspace is (execution-bound in the gateway like `genie_chats`),
is rate limited like it, and counts as Genie traffic in the IDE data meter. A fork gets 403 `fork`; an admin's switch gives 403 `off`.

A new version after an Undo replaces the versions that were undone. Boards are placed in the first free spot, 80 px from the others.

## Boards run in a sandbox (rule 3 of board 1, "same sandbox as Preview")

Each board is an `<iframe sandbox="allow-scripts" srcdoc>`: no origin, no cookies of the site, no access to the page. It carries a strict policy
(`BOARD_POLICY`): scripts and styles inline, pictures and fonts only as `data:` or `blob:`, **no network, no frames, no forms**. Scripts work, so a clickable
prototype works. A small helper script is added to what is shown (never saved): it reports clicks on links, retyped text and pin spots to the page
with `postMessage`; the page accepts only messages whose source is one of its own board frames.

Text from a board that goes to Genie (`canvas_read`, the notes, the changes) is marked as data, never instructions.

## Phases, as built

1. **See it.** Canvas tab (a picker with the canvas name lists the canvases of the workspace and makes a new one); hand tool, Space and drag, wheel and Ctrl+wheel, pinch; zoom buttons and level; Fit all; minimap; Genie draws boards (`canvas_open` with a `plan` shows dashed frames, "drawing…" while one is written); boards off screen are paused (the frame is emptied) and loaded again when they come near; the admin switch; everyone in a shared session reads the same files and the page polls the revision every 3 s, so changes show live.
2. **Move it, talk about it.** Select; move and size with snapping to neighbour edges and the grid and a size label; the Board chip in Genie's box (the selected board goes with the next message); versions, Undo, Redo, Compare and Restore; the user's edit wins and Genie re-reads the board (it reads the file at each action).
3. **Edit it.** Double-click or Enter, then click text: only that element becomes editable (plain text) and is saved as a new version; pins (note to keep, or Send to Genie), the Comments list, `+ Board`, Duplicate, Delete; the user's edits become short lines ("you changed the text on Pricing") sent with the next message.
4. **Share it.** Export one board (.html), all boards (zip, written in the page), print or save as PDF; `href="#board:<name>"` in a board goes to that board; shared sessions see the canvas live.

## Genie

Context with each message (`canvasContext`, at most 1,800 characters): the boards, the selected one, what the user changed since the last message, the notes sent. Notes and changes are told once. Genie never reacts by itself (choice 7 of board 6).
`canvas_board` and the other writes ask like writing in the editor (scope `editor`); `canvas_open` and `canvas_read` ask nothing. A task draws at most 24 boards. After a write the canvas shows "Changed on the canvas: boards/<name>.html +n −m lines" with Undo and Show diff.

## Tools on a part (boards 11 to 15 of the design, built 2026-11-25)

A part is one element of a board. Phases A, B and C of board 15 are all built, with its six choices as recommended: changes stored as an **inline style** on that element; a **Select part mode** (not every click); **Shift-click** for several parts; **lock honoured by the tools and by Genie**; colours from hex, the board's own colours, the site accent and the eyedropper; **no new admin setting** (the canvas switch covers it).

| Part | Where |
|---|---|
| The helper script in every board: picks a part (outline with name and size, parent on a second click, Shift adds, Alt measures), applies a change, reports the new HTML | `BOARD_HELPER` in `src/js/src/page/lib-canvas.mjs` |
| The values that may be set (`validStyle`), colours, align and distribute maths, labels, `parseInline` | `lib-canvas.mjs` (node tests in `src/js/test/lib-canvas.test.mjs`) |
| The floating toolbar (Look, Shape, Guard, Line up, More), the panels, the locked-part check | `src/js/src/page/canvas-parts.js` |
| The part mode (`syncPart`: on whenever a board is selected), the Responsive check, Genie's context and the check on `canvas_board` | `37-canvas.js` |

**How a change travels.** A text part is made editable (`contenteditable="plaintext-only"`) when it is picked and saved on Esc or when another part is picked ("you changed the text of h1#t"). The page sends the board's helper `apply` (a live preview while a slider moves, then a kept change), `cmd` (duplicate, delete, wrap, lock, hide, order, shift, image, makevar, copystyle) or `cancel`. The helper checks every value with the same `validStyle` as the page (the function is copied into the helper: plain colours, lengths and a few keywords; no `url()`, `expression`, `;`, `{}`), sets it inline on that element and posts the board's HTML with a label ("you changed the fill of button#buy to #1b6ef3"). The page saves it as a new version with that label, so Undo, Compare, the change lines for Genie and the log all see it. A part inside `data-locked` refuses `apply` and every `cmd` except unlock.

**Phase A.** Part mode (automatic for the selected board: a click on the board is sent to its helper as `pickat`, and hover as `hover`, because the board keeps the pointer for moving), selection outline, parent and Shift-click, the toolbar (Fill, Corner, Text, Layout, Size, Lock, Note, More), the chip `Board: Pricing › button#buy` and the part in Genie's context.
**Phase B.** Panels for Text, Spacing (padding, margin, gap; linked sides), Border and shadow (none, soft, lifted, custom), Opacity; Copy and Paste style; Duplicate, Delete, Wrap.
**Phase C.** Picture (a PNG, JPEG, GIF or WebP of up to 200 KB, kept in the board as data; cover or contain; alt text), Align and distribute and same size (several parts), Order, Make it a style (a CSS variable in a `<style data-cv-vars>` block; every part with the same value uses it), Measure (hold Alt), Eyedropper (the browser's EyeDropper where it exists), Hide and Show all (`data-cv-hid`), the Responsive check (a board at 390, 768 or 1280 wide, scaled to fit; its size on the canvas does not change).

**Locks.** `data-locked` on the element. The tools refuse it. Genie is told the locked parts of every loaded board and `canvas_board` is refused when the new page changes or removes one (`lockedViolation`: the locked part's HTML must be the same).

Not built: dragging a part to a new place by the mouse (Align and the order do the work), editing the CSS of the page as text, a full properties panel.

**Its own window.** `Canvas.popOut()` (called by the pane's `popOut` when the Canvas tab is on, from the drag-out zone or More) opens a same-origin window and moves the canvas root into it, with the page's styles; the listeners of the boards' messages and of menu clicks are added to that window too, and the pane shows "Bring it back". Boards reload there (a part picked before the move is picked again). Closing the window, or the button, moves the root back.

## Choices of board 6 (all as recommended)

1 a tab in the Preview pane · 2 plain HTML · 3 scripts allowed in the sandbox · 4 move, size, retype, pins · 5 shared sessions: owner and collaborators edit (the app has no separate read-only viewer role, see below) · 6 admin switch, on by default · 7 Genie reads edits and notes only when the user sends.

## Not built, and what differs from the boards

- **Viewers who cannot edit.** The app's shared sessions have no read-only role: everyone who has the session link is a collaborator, so everyone can move and retype. Needs a viewer role in sessions first.
- **Real-time push.** Changes show within 3 s by polling, not by a socket.
- **Genie's file tools on boards** work (they are files), but the Review panel of the editor does not list canvas changes; the canvas has its own Undo, Show diff and Versions.
- The "drawing…" frame shows while the board is written, not while the model composes it (the page cannot see the model's thinking).
- A Figma board was dropped from the design at the owner's word; nothing about Figma is built.
- The canvas settings are not carried to workers (a worker has defaults), like chat history; the switch is enforced on the gateway and in the page.
