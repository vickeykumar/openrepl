---
title: Using the Go REPL (gomacro)
keywords: gomacro, go interpreter, go repl, golang repl, go prompt, gomacro prompt, import fmt, nothing printed, undefined fmt, fmt is undefined, why is fmt undefined, :help, :quit, // int
link: /docs/gomacro.html
---
At the gomacro> prompt, type one Go statement and press Enter. It runs as soon as it is complete, and an expression such as 12+31 or a variable name prints its value and its type: a bare number such as 12+31 shows as {int 43} // untyped.Lit, and a variable or a calculation with one shows as 42 // int. Import a package before you use it: import "fmt", then fmt.Println("hi"). A for or if block runs when its braces close, so there is nothing to type to run it; the lines of a block that is not closed yet go on to the next line. A line with an error only prints the error. :help lists the commands and :quit leaves; the Run button, not the prompt, runs the program in the editor.
