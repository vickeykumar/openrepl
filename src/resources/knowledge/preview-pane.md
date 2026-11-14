---
title: Preview a web page or program
keywords: preview, web page, html, browser, localhost, live preview, see my page, listening, open the preview, no preview button, preview button missing
link: /about.html
---
The Preview shows a web page of your workspace, or a program of yours that listens on a port, in a sandboxed frame beside the editor. It takes the terminal's place; the terminal and its programs keep running.

Ways to open it: the Preview button above the editor (shown for an .html, Markdown, picture or PDF page, and with a dot while a program listens on a port from 1024 up); the right-click "Open preview"; the command menu (the ⌘K button, or Ctrl/Cmd+Shift+P) entry "Open the preview", which works when nothing is open or running; Ctrl+Alt+V. Run on an .html page opens it too. A program that starts listening is offered to you.

No button? Nothing that can be shown is open and no program listens. Open an .html page, start a program in the terminal (python3 -m http.server 8000), or use the command menu. Programs are found only where each workspace has a private network.

The Ports list shows open ports and lines to start Python, Flask, Node, Vite, Next.js and Angular servers ("Type it" does not press Enter).  The page runs in a sandbox: it cannot see your account. Its localStorage, sessionStorage and cookies work, and so do a program's server logins. An admin can switch the preview off, or off for guests.
