---
title: App Store (libraries and tools)
keywords: app store, install library, install package, pip install, flask, numpy, requests, conda, no module named, module not found, command not found, install tool, apps button
link: /about.html
---
The App Store lets signed-in users add libraries and tools to their terminals. Open it with the Apps button above the editor (a quick dialog; the full page is /store) or the command menu entry "App Store". Guests do not have it: sign in first.

pip install does not work in the terminals (they have no internet). Use the store instead: find the app (for example flask), press Install, then turn its switch On. New terminals have it; for an open terminal, run the line shown under My apps. Then python3 can import it.

It has Python libraries and tools (conda-forge), Node libraries (npm), languages such as Lua, Ruby, Julia and R, and editor plugins (samples that show a line in the editor's status bar). Search finds packages on conda-forge and npm, and an app's page shows its size before you install it.

Apps on the safe list install at once. Languages and anything else you Request, and an admin decides; you are told the answer. Installed means installed for everyone, and the switch only changes your own terminals. You can uninstall an app when nobody else has it on; an admin can always remove it. If an install fails, you see why and can Report to admin. Genie can offer to install one too, and asks first.
