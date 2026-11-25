---
title: Agent mode: shell commands in your workspace
keywords: agent, bash action, shell command, bash command, command output, exit code, commands switch, sandbox, editor sharing off, inspect saved workspace, source inspection
link: /about.html
---
In Agent mode, Genie can run one shell command in the workspace, whatever language is selected. Use it to search a project, run tests or builds, use Git, or unpack an archive. Before it runs, Genie shows the exact command; the chat shows its exit status and output.

Commands use the terminal sandbox: workspace access only, no direct network or interactive input, 30 seconds by default and 120 seconds maximum. When the admin enables Internet for REPLs, signed-in non-admin workspaces get the managed proxy for supported tools such as pip, npm, Maven, Cargo, curl and Git over HTTPS. Guests have no proxy; admins use direct network. A requested Bash install can run if it fits the time limit; installs ask first. If a download fails, stop and explain that egress may be unavailable. See the [package installation guide](/docs/install-apps.html).

If editor sharing is off, Genie uses focused, non-modifying Bash commands such as `rg`, `sed` or `cat` on one concise saved source excerpt when a task needs it. Do not ask the user to enable editor sharing. The unsaved editor buffer is still unavailable, but Genie first tries another saved source or a narrower edit; it asks only when no saved source can answer the request or no safe focused edit is possible. Avoid dumping large or minified content; search for the relevant text and inspect just the matching excerpt.

Running commands has its own permission. Risky actions such as deleting, moving, `git reset`, or piping a script into a shell always ask. `sudo` and actions outside the workspace are never skipped. Use a Bash terminal for a server or other long-running program. An admin can turn Agent commands off.
