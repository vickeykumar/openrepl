---
title: Using the Go REPL (gointerpreter)
keywords: gointerpreter, go interpreter, go repl, golang repl, go prompt, loop does not run in the go repl, nothing printed, :r, :c, :x, clear the session
link: /docs/gointerpreter.html
---
At the go>> prompt of gointerpreter, type one Go statement and press Enter. An expression such as 12+31 or a variable name prints its value at once, and so does fmt.Println. A for or if block is only kept: type :r to run what you typed so far. :r does not run the editor; the Run button does. A block stays in the session and prints again later, and a statement with an error keeps failing: type :c to clear the session.
