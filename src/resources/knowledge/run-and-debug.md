---
title: Running and debugging code
keywords: run, execute, compile, debug, gdb, breakpoint, qemu, paused, ctrl enter, arguments, environment variables, env, editor, output
link: /about.html
---
Write code in the editor and press Run, or Ctrl+Enter, to compile and execute it in the terminal. Debug, or Shift+Ctrl+Enter, steps through C, C++, Go, Rust and assembly code with gdb. Program arguments and environment variables used by Run and Debug can be set in the editor, separated by spaces. You can also type directly in the terminal, which works like any REPL of that language. On a server that cannot trace programs, the program runs under QEMU and gdb connects to it: it starts paused, so set breakpoints and type c or run to start it. The assembly REPL needs the same tracing, so there use the editor's Run or Debug.
