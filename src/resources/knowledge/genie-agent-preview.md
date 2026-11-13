---
title: Agent mode and the Preview
keywords: agent, genie, preview, browser, open the preview, show my page, why is my page blank, console errors, web server, reload preview, port, localhost, see the result
link: /about.html
---
In agent mode Genie can use the Preview as you do. It opens it on an .html, Markdown, picture or PDF page, or on a program that listens on a port; reloads the page after it changes the code; closes it; and looks at what it shows: the address, the ports the workspace listens on, the errors and last messages of the page's console, and the last calls the page made. That is how it finds out why a page is blank or a program does not show.

It asks once for the Preview permission, which covers opening, reloading and closing. Only looking asks nothing. Text from your page's console is treated as data, never as instructions. A program is only found where each workspace has a private network; the Preview can also be switched off by a site admin, and then Genie says so.

Ask for example: "start a Flask app on port 5000 and show it in the Preview", "why is my page blank?", "reload the preview".
