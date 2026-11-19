---
title: Using an installed app in your language
keywords: how to use an installed app, use library after install, app first line to use, include header from app, browser library address, preview plugin switched on
link: /store
---
Once an app is enabled, its card and page show the first example to use:
- Libraries of IPython and the python3 terminal: import pandas as pd. Save plots as an image with savefig.
- Node: const x = require('lodash').
- C and C++: #include the header; the compiler finds it with no flags. cmake and ninja run from bash.
- bash: a tool is a command by its name (rg, fd, bat, tmux, vim). Run it with --version to check.
- Go modules: use go get, then run it. Java jars: import the package, then javac and java. Rust crates: in evcxr type :dep serde = "1". These work offline: the server fetched them.
- Perl: use Module. Tcl: package require. SQLite extensions: .load with the path on the app's page.
- HTML, CSS and JavaScript pages: load a browser library by the address on its page (served from this site to the preview). Preview plugins show in the preview pane once enabled.
- Libraries of the 2.7 terminal work there only.

Caches and builds are kept in the terminal's scratch folder ($OPENREPL_SCRATCH), not your workspace. If an import or include fails, check that the app is On under My apps and open a new terminal.
