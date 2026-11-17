---
title: Agent mode, plan, checks, carry on and project notes
keywords: genie.md, project notes, notes for the agent, carry on, paused at the step limit, undo this task, agent plan, plan list, fix attempts, edit one part, check before done, not checked
link: /about.html
---
In agent mode the card starts with a plan that the agent ticks off as it goes. It looks things up in your workspace without asking, because that changes nothing, and it edits one part of your code instead of rewriting everything, so you review a few changes. After it changes code it runs the program and checks what it prints. If that fails it tries a fix, twice at most, then says what still fails. A task that could not be checked says "Not checked" instead of claiming it works.

At the end the card shows what was checked, what changed, the steps and time used, and Undo this task, which puts everything back as it was unless you changed it since.

When a task uses its steps the card pauses and asks. Carry on continues the same task, up to three times. A GENIE.md in your workspace gives it notes about your project (how to run it, the style, what not to touch); its first part is read at the start of a task.

Genie knows how the editor and the terminal work: it sees both, so "run my code" or "what does this error mean" needs no explanation from you. In Chat it answers; for anything that has to be done it points you to Agent. A small model does Agent mode less well, so the panel says so and offers the model the site suggests with one click. An empty chat shows starter questions.
