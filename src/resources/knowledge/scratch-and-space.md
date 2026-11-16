---
title: Scratch folder and workspace size in the status bar
keywords: scratch, workspace full, disk quota, 50 MB, bytecode, cache folder, build folder, cmake build, out of space, no space left, status bar, connected
link: /about.html
---
At the bottom right of the terminal, beside Connected, the bar shows two sizes: Workspace (your files, against the 50 MB limit) and Scratch (a folder for this terminal only).

Scratch is for what programs write only to run faster or to build: bytecode, caches of Go, mypy, npm and matplotlib, build folders, temporary files. It is outside your workspace, is not saved, synced or shared, and is removed when the terminal ends. The variable $OPENREPL_SCRATCH holds its path, for example cmake -B $OPENREPL_SCRATCH/build.

The bar turns amber at 80 percent and red at 100. At the soft cap (256 MB for a terminal, set by the admin) the cache folders are cleared first. If the terminal still fills it, the terminal is stopped; your files are safe.

If a workspace is full, delete files you made, since caches and builds no longer count there. Put things you want to keep in the workspace, not in scratch.
