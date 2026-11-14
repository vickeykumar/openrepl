---
title: Preview a web page or program
keywords: preview, address bar, new tab, move the preview, dock preview, pop out preview, web page, html, browser, localhost, live preview, see my page, listening, open the preview, no preview button, preview button missing
link: /about.html
---
The Preview shows a web page of your workspace, or a program of yours that listens on a port, in a sandboxed frame beside the editor. It takes the terminal's place; the terminal and its programs keep running.

Ways to open it: the Preview button above the editor (shown for an .html, Markdown, picture or PDF page, and with a dot while a program listens on a port from 1024 up); the right-click "Open preview"; the command menu (the ⌘K button, or Ctrl/Cmd+Shift+P) entry "Open the preview", which works when nothing is open or running; Ctrl+Alt+V. Run on an .html page opens it too. A program that starts listening is offered to you.

No button? Nothing that can be shown is open and no program listens. Open an .html page, start a program in the terminal (python3 -m http.server 8000), or use the command menu. Programs are found only where each workspace has a private network.

The Ports list shows open ports and lines to start Python, Flask, Node, Vite, Next.js and Angular servers ("Type it" does not press Enter).  The page runs in a sandbox: it cannot see your account. Its localStorage, sessionStorage and cookies work, and so do a program's server logins. An admin can switch the preview off, or off for guests.

The pane works like a small browser. Type in its address bar and press Enter: a file of your workspace (web/index.html, counted from your home folder), or a port of your programs (5000, 5000/api, localhost:5000, 127.0.0.1:5000/x); it loads in the tab you are in, and Back returns to the page before. Other websites are not loaded here. The + beside the tabs opens an empty tab with suggestions (your pages, the ports that listen, what you opened lately). The line at the bottom says "sandboxed page".

Move the pane: drag its grip (the dots at the left of its bar) onto the editor to cover the editor and keep the terminal beside it, back onto the terminal to cover the terminal again, or out of the browser window to open it in its own window. The "..." menu has "Dock over the editor" and "Pop out to its own window" for the keyboard. The place is remembered in this browser only.
