---
title: Agent mode and your files
keywords: agent, files, files panel, file browser, folder, delete a file, create a file, new file, rename a file, move a file, copy, paste, cut, open a file, save, load a file in the editor, manage my files
link: /about.html
---
In agent mode it can look through your workspace for text without asking, since that changes nothing. It can also work in the Files panel as you do: list your files, open one in the editor, create files and folders, save, copy, cut, paste, rename, move and delete. It only uses paths it has seen listed. A hidden file (a name that starts with a dot) is listed only where you chose Show hidden files, and Genie asks you every time before it works on one; it never touches its own chat history or the inside of .git. Opening a file saves the open one first, then puts the new one in the editor. Listing, opening, creating, saving and copying can be allowed once or for the session. Renaming, moving and deleting are asked about every time, with the exact paths shown.

After you accept a change it made to the editor, the agent saves the file that is open (it asks for the files permission, once or for the session). If the code is only in the editor and no file is open, it tells you instead of making a file.
