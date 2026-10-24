---
title: The IDE follows the editor theme
keywords: theme, editor theme, colors, colours, dark, light, monokai, solarized, terminal colors, status bar, appearance, ide
link: /
---
When you pick an editor theme in the status bar at the bottom of the editor, the whole IDE takes its colours from it, not just the editor. The editor, the terminal and the file browser use the theme's own background, and the app bar, tabs, status bar, menus and borders use darker shades of it. A light theme gives a pale bar and soft borders, and a dark theme gives darker ones. The terminal's colours follow too, so programs that print coloured text, such as compiler errors or a Python traceback, show it in colours that match the theme. A REPL that prints plain text shows it in the theme's text colour. The choice is remembered in this browser. Monokai is the default theme. OpenREPL Dark, the IDE's earlier look, is still in the list. The Genie button in the app bar opens Genie pinned beside the IDE, and a pinned Genie uses the same theme colours. When Genie floats (not pinned) it follows the page's own light or dark theme instead.
