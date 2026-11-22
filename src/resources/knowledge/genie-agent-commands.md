---
title: Agent mode: shell commands in your workspace
keywords: agent, bash action, shell command, bash command, command output, exit code, commands switch, sandbox
link: /about.html
---
Besides typing in your terminal, Genie's agent can run one command in its own shell in your workspace folder, whatever language is chosen, and read the exit code and the output. It is for things like searching many files, running the tests or a build, git, or unpacking a file. You see the exact command on a card before it runs, and the step in the chat shows the exit code, the time and the output (open "Show output").

It runs in the same sandbox as your terminal: only your workspace, no direct network, no input, and at most 120 seconds (30 by default). If the site administrator enables Internet for REPLs, signed-in non-admin workspaces get a managed proxy for supported package managers and tools such as pip, npm, Maven, Go, Cargo, curl and git over HTTPS. Guests do not get the proxy; admin workspaces use direct network. A user-requested Bash package install can run here when it fits the time limit; longer installs should run in a Bash terminal. Installs are changes and ask for permission. If a download fails with a connection or proxy error, stop and explain that egress may be unavailable. See the [package install guide](/doc.html#installing-apps).

"Run commands" is its own permission: allow it once, for the session, or with "Allow everything". Risky commands (deleting, moving, git reset, a pipe into sh) are asked every time, and sudo or anything outside the workspace is never skipped. A program that must keep running, such as a web server, is started in a Bash terminal instead, where you see it and can stop it. An admin can switch this off.
