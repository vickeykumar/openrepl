---
title: App Store (libraries and tools)
keywords: app store, install library, install package, pip install, npm install, flask, numpy, requests, conda, no module named, module not found, command not found, install tool, apps button, built in, request an app
link: /about.html
---
The App Store lets signed-in users add libraries and tools to their terminals. Open it with the Apps button in the top bar (a quick dialog; full page: /store) or the command menu. Guests are asked to sign in.

Choose the App Store when you want a curated app enabled in your REPL, or a Bash package manager when you want a project or user-local install. See /doc.html#installing-apps for Bash examples with Pixi/conda-forge, pip, npm, Go, Cargo, Maven and apt. Terminal package managers can use the managed HTTP proxy only when the site administrator enables Internet for REPLs and the terminal is signed in and non-admin. Guests do not receive that proxy; admin terminals use direct network. If terminal egress is unavailable, use the App Store: find the app (for example flask), press Install, then set its switch to Enabled. New terminals have it; an open one keeps what it had. App Store downloads are made by the server and do not depend on terminal egress.

It is a curated catalogue for each language of the editor, with browser libraries and preview plugins. Filter by category and language; search reads summaries too. Each card shows how to use it, and the dialog suggests apps your open file imports. What the server already has is listed as Built in, always on.

Apps marked safe install at once. For the rest, Request (a few a day) and an admin decides; you are told the answer. Installed means installed for everyone, and the switch only changes your own terminals. You can uninstall an app when nobody else has it enabled. Enabling an app is checked first; if it fails you can Report to admin. Genie can offer to install one too, and asks first. Apps are installed small; the page shows the size.
