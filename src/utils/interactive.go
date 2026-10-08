package utils

import "strings"

// A command whose interactive terminal runs another program than the command
// name says. "java" is the name of the language: its Run button compiles and
// runs a file (the compiler script in demos.xml), and its REPL is jshell. The
// memory limit, the weight and the route (/ws_java) stay the language's.
//
// jshell is started with one JVM (--execution local runs the user's snippets
// in the tool's own JVM instead of a second one) and small limits: the heap is
// 48 MB, the C1 compiler only, the serial collector, one compiler thread and a
// small code cache. Measured with OpenJDK 11, that takes a session from about
// 375 MB to about 150 MB, and from 4.8 s to start to 1.9 s. See LLD 09.
var interactiveCommands = map[string][]string{
	"java": {
		"jshell",
		"--execution", "local",
		"-J-Xmx48m", "-J-Xms8m",
		"-J-XX:TieredStopAtLevel=1",
		"-J-XX:+UseSerialGC",
		"-J-Xss512k",
		"-J-XX:CICompilerCount=1",
		"-J-XX:ReservedCodeCacheSize=16m",
		"-J-XX:MaxMetaspaceSize=64m",
		// no INFO lines from the JDK in the terminal (it prints one when the
		// preferences folder is created, the first time a home runs jshell)
		"-J-Djava.util.logging.config.file=/dev/null",
	},
}

// InteractiveCommand returns the program and arguments that the interactive
// terminal of a command runs, if that is not the command itself.
func InteractiveCommand(command string) ([]string, bool) {
	alt, ok := interactiveCommands[strings.TrimSpace(command)]
	if !ok {
		return nil, false
	}
	return append([]string(nil), alt...), true
}
