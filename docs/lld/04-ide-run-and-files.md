# LLD 04: IDE: Run/Debug, demos and files

Scope: `src/resources/meta/demos.xml`, `src/utils/{demo,utils}.go`, `src/server/utils.go` (`InitCommands2DemoMap`, `handleDemo`), `src/containers/container_linux.go` (`GetCommandArgs`), `src/filebrowser/filebrowser.go`, `src/server/handlers.go` (`handleFileBrowser`, `handleFileUpload`). Client: `src/resources/js/scribbler.js`, `src/js/src/gotty.ts`.

## 1. REPL catalog: `demos.xml`

`demos.xml` is embedded as `static/meta/demos.xml` and loaded lazily into `utils.Commands2DemoMap` (`map[command]Demo`). This happens on the first `/demo` call or when routes are set up.

```go
type Demo struct {
    Name     string   // backend command, also the WS route suffix: /ws_<Name>
    Prefix   string   // optional launcher placed before the command in REPL mode (no entry uses it today)
    Github   string   // link shown in the UI
    Codes    []Codes  // animated demo: []{Name, []Code{Prompt, Statement, Result}}
    Usage    []Usage  // meta-command table: []{Command, Description}
    Doc      string   // docs link, e.g. ./docs/gointerpreter.html
    Content  string   // starter code placed in the editor
    Compiler string   `json:"-"` // bash script used by Run; never sent to the browser
}
```

`GET /demo?q=<command>` returns `{"Status":"SUCCESS|FAILED","ErrorMsg":"","Demo":{…}}`. The page (`scribbler.js` → `actionOnchange`) uses the response to:

- type out the demo in the "Getting Started" panel (`PrintDemo`),
- fill the usage table (`PrintUsage`),
- set the GitHub and docs links,
- load `Content` into the editor, except on `/practice`.

Every REPL in the catalog has starter code in `Content`: a short program that prints something and shows the language's basics (a function, a loop or collection, string formatting). It must run as-is with **Run**. A shared code link (`?s=<id>`, LLD 06) replaces it once it has loaded.

## 2. Run and Debug pipeline

```mermaid
sequenceDiagram
    autonumber
    participant U as User
    participant S as scribbler.js
    participant G as GottyTerminal (gotty.ts)
    participant W as gotty WS handler
    participant C as containers.GetCommandArgs
    participant B as bash + Compiler script
    U->>S: ▶ Run (or debug)
    S->>S: SaveSelectedNodeToFile() (POST /ws_filebrowser?q=save) when a file is open
    S->>G: dispatch "optionrun" / "optiondebug" on each visible .terminal.active
    G->>G: Cleanup() old session, then after 500 ms spawnGotty(option, "optionrun")
    G->>W: WS init Payload {IdeLang, IdeContent=b64(editor), IdeFileName, CompilerFlags, EnvFlags, [CompilerOption=debug]}
    W->>C: Iscompiled(params) = true
    C->>C: SaveIdeContentToFile(IdeFileName) (arg0 = file path, or base64 content if no file)
    C->>B: /bin/bash -c "<Demo[cmd].Compiler>" arg0 "<CompilerFlags>"
    B-->>U: compiler and program output streamed on the PTY
    Note over W: cgroup limit 3× memLimit, file events deferred until the process exits
```

### Compiler script contract

A Run request executes `/bin/bash -c "$SCRIPT" "$ARG0" "$FLAGS"` (the `Prefix` is not used). The script can rely on:

| Input | Meaning |
|---|---|
| `$0` | Path of the saved editor file (`IdeFileName`), or the base64 editor content when no file is selected. Scripts decode it with `echo $0 \| base64 --decode`. |
| `$1` | The *Compiler/Repl Args* text box, passed as one string. |
| `$IdeLang` | The UI option value (`c`, `cpp`, `go`, `python`, …), so one backend (for example `cling`) can pick `gcc` or `g++`. |
| `$CompilerOption` | `debug` when **debug** was pressed. Scripts then add `-g` and launch `gdb`, `rust-gdb` and similar. |
| `$IdeFileName`, `$HOME` | The same file path, and the workspace directory. |
| client `EnvFlags` | Extra variables from the *Env Vars/Paths* box. |

Scripts usually write `test.<ext>` into `$HOME` when there is no file, compile it, run it, and `printf "\n"` at the end.

### Language routing on the client

`handleTerminalOptions` (`gotty.ts`):

- **`java`:** on `optionchange`, an iframe to `https://tryjshell.org`. On `optionrun`, the normal backend path (`/ws_java`).
- **`javascript`:** always an iframe to `./jsconsole.html`, which evaluates in the browser. The share button is disabled.
- **Everything else:** a WebSocket to `/ws_<option>`. `c`, `cpp` and `go` map server-side to `cling`, `cling` and `gointerpreter`.

## 3. File browser

### Backend object

```go
type Filebrowser struct {
    Root             string            // homedir, or the IDE file's directory for Run
    watcher          *fsnotify.Watcher // nil for HTTP (stateless) use
    eventwriter      Eventwriter       // the session's *webtty.WebTTY (WriteEvent)
    watcherMap       map[string]bool
    deferwatch       bool              // queue events, flush on Close()
    pendingnotiflist []*Event
    size             float64           // MB, computed at construction
}
type TreeNode struct { Id, Text, Type string; Children []TreeNode } // Type: "default" (dir) | "file"
type Event    struct { Name string; Op fsnotify.Op; Type string; NewName string }
```

There are three ways to construct it:

| Caller | `watch` | `deferwatch` | Behaviour |
|---|---|---|---|
| REPL session (`processWSConn`) | true | false | Recursive watch of the homedir. Every create, remove or write is pushed at once as a `'6'` Event frame. |
| Run session (`processWSConn`, `Iscompiled`) | true | true | Watches only the root (the IDE file's parent). Events are queued and flushed by `Close()` when the program exits, so build output doesn't flood the browser. |
| HTTP APIs | false | true | Stateless helper for tree, load, save, zip, ops and quota. |

When a directory is created (for example by unzipping), the watcher walks the new tree and adds a watch on each subdirectory. It also emits synthetic create events for everything inside, so the jstree view stays in sync. `Chmod` events are ignored. `fsnotify.Op` is a bitmask: Create=1, Write=2, Remove=4, Rename=8, Chmod=16.

### HTTP API (`/ws_filebrowser`)

`filepath` must start with the caller's homedir (from `cookie.GetOrUpdateHomeDir`), and defaults to the homedir.

| Method and query | Body | Result |
|---|---|---|
| `GET` | n/a | `TreeNode` JSON of `filepath` (jstree `core.data`). |
| `GET ?q=load&filepath=F` | n/a | base64 of file F (`text/plain`). |
| `GET ?q=zip&filepath=D` | n/a | `application/zip` stream of D (`Writezip`). |
| `GET ?q=usage` | n/a | `{usedMB, limitMB, guest, deleteAfterMinutes}` for the workspace card in the Files panel. |
| `POST ?q=save&filepath=F` | base64 content | Writes F. |
| `POST` | JSON `Event` | Performs the operation: `Op&Create` makes a dir or file (with `NewName`, it copies `Name → NewName`); `Op&Remove` deletes; `Op&Rename` renames `Name → NewName`. Creates are refused with 507 when over quota. |

Slaves (shared viewers) and forked tabs reach the owner's workspace by adding `jid=` or `homedir=` to these URLs (`preprocessurl` in `scribbler.js`).

### Upload (`POST /upload_file`)

- The form has two fields: `file`, and `checksum` (SHA-256 hex computed in the browser).
- The server parses the multipart form with 5 MB in memory, rejects the upload with 507 when over quota, and strips a UTF-8 BOM. It writes `homedir/<filename>` with mode 0644.
- A checksum mismatch is only logged.
- The browser enforces `MAX_FILESIZE = 20 MB` and says so in its error notice.

### Client integration (`scribbler.js`)

- **Tree.** jstree is fed by `GET /ws_filebrowser`, with context-menu operations mapped to the `Event` POSTs. File icons come from `filename2IconClass`.
- **Context menu.** Built in the jstree `contextmenu` config in `page/07-file-browser.js`. Folder nodes get New (File, Folder), Rename, Cut, Copy, Paste, Save (disabled), Download and Delete; file nodes drop New and enable Save. Items are grouped by separators, Delete is last and carries the class `ctx-danger`, and every item has an icon class (`ctx-i-*`, drawn as a mask in `ui-refresh.css`). Cut, Copy, Paste and Save show their Cmd or Ctrl shortcut. jstree runs the item whose `shortcut` key code is pressed while the menu is open, so a string shortcut (`"off"`) is used on disabled items to show the label without binding the key. Protected nodes (`state.disabled`) have Rename, Cut, Copy and Paste disabled.
- **Selecting a file.** `LoadSelectedNodeFromFile` loads it (`q=load`) into Ace and sets `editor.env.filename`, which becomes `IdeFileName` on Run. The previously open file is saved first (`SaveSelectedNodeToFile`), and the language is switched by extension (`changelangbyselectednode`).
- **Live events.** `setEventHandler(eventhandler)` applies `'6'` events and Firebase `filebrowser-event`s to the tree: create_node, delete_node, refresh.
- **Empty workspace.** `updateFilesEmptyState` shows `#files-empty` ("No files yet. Upload a file, or right-click the folder above to create one.") while the root folder has no children. It runs after jstree's ready, refresh, load, create, delete, move and copy events.
