---
title: App Store (libraries and tools)
keywords: app store, install library, install package, pip install, npm install, flask, numpy, requests, conda, no module named, module not found, command not found, install tool, apps button, built in, request an app
link: /about.html
---
The App Store lets signed-in users add curated libraries and tools to their terminals. Open it with **Apps** or visit /store; guests are asked to sign in.

Choose the store for an app that should be enabled in your REPL. For a project or user-local install, use a Bash package manager; see /doc.html#installing-apps for conda-forge, pip, npm, Go, Cargo and Maven examples. Terminal downloads use the managed proxy only when the administrator enables Internet for REPLs and the terminal belongs to a signed-in non-admin. Guests have no proxy; admins use direct network. Store downloads are server-managed and do not depend on terminal egress.

Find an app, press **Install**, then turn it on under **My apps**. New terminals get the enabled app; existing terminals keep their current setup. Safe apps install at once. Other apps can be requested for an admin to approve. An installed app is shared, while its switch only affects your terminals. Each card shows how to use the app and its installed size. Built-in entries are already available. Genie can offer an install and asks first.
