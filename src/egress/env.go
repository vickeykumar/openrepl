package egress

import "fmt"

// Env is what the REPL's environment gets so that its tools use the proxy: the standard variables (upper and lower case) and the ones pip and
// npm read. 127.0.0.1 and localhost stay direct: that is the REPL's own network.
func Env() []string {
	u := fmt.Sprintf("http://127.0.0.1:%d", Port)
	return []string{
		"HTTP_PROXY=" + u, "HTTPS_PROXY=" + u, "ALL_PROXY=" + u,
		"http_proxy=" + u, "https_proxy=" + u, "all_proxy=" + u,
		"NO_PROXY=localhost,127.0.0.1,::1", "no_proxy=localhost,127.0.0.1,::1",
		"PIP_PROXY=" + u, "npm_config_proxy=" + u, "npm_config_https_proxy=" + u,
		// the JVM, Maven and Gradle read system properties, not HTTPS_PROXY; Perl's LWP reads the proxy variables only when asked
		"MAVEN_OPTS=" + javaProxy, "GRADLE_OPTS=" + javaProxy, "PERL_LWP_ENV_PROXY=1",
	}
}

// javaProxy is the proxy as system properties (Maven, Gradle); the JVM already keeps localhost and 127.* direct (the default nonProxyHosts, which has a | that a launcher script could misread, so it is not repeated here). Not JAVA_TOOL_OPTIONS: every java start would print "Picked up JAVA_TOOL_OPTIONS".
var javaProxy = fmt.Sprintf("-Dhttp.proxyHost=127.0.0.1 -Dhttp.proxyPort=%d -Dhttps.proxyHost=127.0.0.1 -Dhttps.proxyPort=%d", Port, Port)

// Allowed says whether a REPL may use the proxy: the proxy is on, the person is signed in and is not an admin (an admin's REPL is not sandboxed
// and reaches the network directly).
func Allowed(signedIn, admin bool) bool { return Enabled() && signedIn && !admin }
