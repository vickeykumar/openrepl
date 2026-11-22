---
title: Agent mode and your files
keywords: agent, files, files panel, file browser, folder, delete a file, create a file, new file, rename a file, move a file, copy, paste, cut, open a file, save, load a file in the editor, manage my files, editor sharing off, not sharing editor, saved code
link: /about.html
---
In agent mode it can look through your workspace for text without asking, since that changes nothing. It can also work in the Files panel as you do: list your files, open one in the editor, create files and folders, save, copy, cut, paste, rename, move and delete. It only uses paths it has seen listed and asks you before it touches a hidden file you chose to show. Opening a file saves the open one first, then puts the new one in the editor. Listing, opening, creating, saving and copying can be allowed once or for the session. Renaming, moving and deleting are asked about every time, with the exact paths shown.

If editor sharing is off, the current editor buffer is not sent. When a task needs project code, Genie finds and inspects the saved workspace files with focused, read-only Bash commands instead of asking you to turn sharing on. For a requested edit, it makes a focused change based on the saved file and does not replace an unseen editor buffer. If an unsaved change conflicts, it tries another saved file or a narrower change first. It asks one concise question only when no saved source is available or a safe focused edit is impossible.

After you accept a change it made to the editor, the agent saves the file that is open (it asks for the files permission, once or for the session). If the code is only in the editor and no file is open, it tells you instead of making a file.
