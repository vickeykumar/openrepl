---
title: How much data the IDE may send me
keywords: data limit, data usage, megabytes, MB, daily limit, session limit, quota, too fast, output too fast, terminal stopped, limit reached
link: /profile
---
OpenREPL counts the data its IDE sends to you: terminal output, files opened in the editor, downloads, Genie answers and the Preview. Your profile page shows today's total by part. By default a guest gets 50 MB for the session (an hour) and a signed-in user 200 MB a day, resetting at 00:00 UTC, shown in your own time; the admin can change these, and your profile shows the limits that apply to you. At 80% a chip and a note appear. A terminal that prints too fast (by default over 20 MB in 10 seconds) is paused, then stopped, so a program that prints forever cannot flood the connection: add a stop condition and run it again. At the limit, terminal output, Genie questions, downloads and the Preview pause until the reset; saving and small files still work, and your files are never touched. Only sizes are counted, never what was sent.
