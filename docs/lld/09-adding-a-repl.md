# LLD 09: Adding a new REPL

Adding a language touches four layers. The example below adds **Lua**, using the `lua5.4` interpreter.

```mermaid
flowchart LR
    A["1. Install the runtime<br/>install_prerequisite.sh"] --> B["2. Register limits<br/>containers/container.go"]
    B --> C["3. Describe it<br/>resources/meta/demos.xml"]
    C --> D["4. Expose it in the UI<br/>resources/index.html (+ scribbler.js)"]
    D --> E["5. make all, then test"]
```

## 1. Install the runtime

Add the package to `install_prerequisite.sh`, for example `apt-get install -y --no-install-recommends lua5.4`. The executable must be findable with `exec.LookPath` inside the container, or placed in `/opt/gotty/bin`, which is appended to REPL `PATH`s.

## 2. Register memory limit and weight (required)

In `src/containers/container.go`, add the command name to `Commands2memLimitMap`:

```go
"lua5.4": 4, // MB; measure idle RSS of the REPL and add headroom
```

This map also decides which commands get a container. `InitContainers()` only creates parent cgroups for its keys. A command missing from the map would run **without namespaces or a memory limit**, and would count as weight 0 for admission control.

## 3. Add a `<Demo>` to `src/resources/meta/demos.xml`

`<Name>` must equal the command name. It becomes the WebSocket route `/ws_lua5.4` and the key used by `/demo?q=`.

```xml
<Demo>
    <Name>lua5.4</Name>
    <Github>https://github.com/lua/lua</Github>
    <Doc>./doc.html</Doc>
    <Codes>
        <Code><Prompt>&gt;</Prompt><Statement> print(1+2)</Statement><Result>3</Result></Code>
    </Codes>
    <Usage><Command>os.exit()</Command><Description>Exit the REPL.</Description></Usage>
    <Content>-- Welcome to OpenREPL!
print("hello")
</Content>
    <Compiler>
FILE=test.lua
if [ "$IdeFileName" != "" ]; then FILE="$IdeFileName"; else echo $0 | base64 --decode > $HOME/$FILE; FILE=$HOME/$FILE; fi
lua5.4 "$FILE" $1
printf "\n";
    </Compiler>
</Demo>
```

See LLD 04 §2 for the Compiler script contract (`$0`, `$1`, `$IdeLang`, `$CompilerOption`). Escape `<`, `>` and `&` in XML. `<Prefix>` can wrap the REPL (for example with `rlwrap`) in interactive mode only.

## 4. Expose it in the UI (`src/resources/index.html`)

- Add an option to `#optionlist`. Its `value` must be the command name, and `data-editor` is the Ace mode:

  ```html
  <option value="lua5.4" data-editor="lua">Lua</option>
  ```

- In the editor's `#select-lang` list, make sure the Ace mode is not `disabled`. `lua` is disabled today.
- Optional: map a file extension to the mode in `codeext2menuoption` (`scribbler.js`), so opening `*.lua` in the file browser switches the editor mode.
- Optional: if the REPL needs fixed extra arguments when selected, add them to `option2args` in `src/js/src/gotty.ts`. For example, C uses `arg=-xc&arg=-noruntime`.
- Optional: add a docs page under `src/resources/docs/` and point `<Doc>` at it.

## 5. Build and verify

```sh
cd src && make all && ../bin/gotty -w -p 8080
```

Checklist:

- [ ] `/demo?q=lua5.4` returns `"Status":"SUCCESS"`.
- [ ] Selecting **Lua** opens a working prompt, and the log shows `New client … connections` with the new weight.
- [ ] **Run** executes the editor content, and a file selected in the tree is saved and run.
- [ ] On a cgroup v1 host, `/sys/fs/cgroup/memory/lua5.4_container/<pid>/memory.limit_in_bytes` exists for a live session.
- [ ] **Fork REPL** and an extra terminal tab both work (nsenter into the same namespaces).
- [ ] The Docker image builds (`docker build .`), and CI passes.
