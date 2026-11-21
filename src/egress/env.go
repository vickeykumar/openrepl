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
	}
}

// Allowed says whether a REPL may use the proxy: the proxy is on, the person is signed in and is not an admin (an admin's REPL is not sandboxed
// and reaches the network directly).
func Allowed(signedIn, admin bool) bool { return Enabled() && signedIn && !admin }
