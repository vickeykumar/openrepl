package livepreview

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// A program that is previewed is served under /preview/<token>/p/<port>/, but its pages are written for "/": a link
// to /static/x.css or a fetch of /api/items would reach OpenREPL's own site. Three layers bring them back, in order of
// how sure they are: the absolute links of the HTML are rewritten here; a small script added to the page puts the
// prefix on what fetch, XMLHttpRequest, WebSocket and EventSource are given; the pages of OpenREPL itself answer 404
// to the rest. (The Referer cannot be relied on: a page without an origin sends none that tells the preview.)

// maxRewrite is the largest HTML page that is read to be rewritten; a bigger one is passed on as it is.
const maxRewrite = 8 << 20

var attrRe = regexp.MustCompile(`(?i)\s(?:src|href|action|poster|data|formaction)\s*=\s*["']/`)
var headRe = regexp.MustCompile(`(?i)<head[^>]*>`)

// RewriteHTML puts prefix on the absolute links of page (src, href, action, ...: "/x" becomes prefix+"/x", while "//host/x",
// "#x", "x" and "http://..." stay) and adds the script of layer two at the top of the head.
func RewriteHTML(page []byte, prefix string) []byte {
	var buf bytes.Buffer
	last := 0
	for _, m := range attrRe.FindAllIndex(page, -1) {
		slash := m[1] - 1 // the "/" that starts the address
		rest := page[slash:]
		// "//host/x" is another host, and an address that has the prefix already is left as it is
		if len(rest) > 1 && rest[1] == '/' || bytes.HasPrefix(rest, []byte(prefix+"/")) {
			continue
		}
		buf.Write(page[last:slash])
		buf.WriteString(prefix)
		last = slash
	}
	buf.Write(page[last:])
	out := buf.Bytes()
	script := []byte(clientScript(prefix))
	if loc := headRe.FindIndex(out); loc != nil {
		return append(append(append([]byte{}, out[:loc[1]]...), script...), out[loc[1]:]...)
	}
	// no head: the script goes before everything, after a doctype if there is one
	i := 0
	if bytes.HasPrefix(bytes.ToLower(bytes.TrimSpace(out)), []byte("<!doctype")) {
		if j := bytes.IndexByte(out, '>'); j >= 0 {
			i = j + 1
		}
	}
	return append(append(append([]byte{}, out[:i]...), script...), out[i:]...)
}

// InjectBridge adds BridgeScript to a page of the workspace: at the top of its head, or before everything after a doctype.
// The page's own addresses stay as they are (the files are served by their relative paths).
func InjectBridge(page []byte) []byte { return injectAtTop(page, BridgeScript) }

// injectAtTop puts a script at the top of the head of a page (or before everything, after a doctype, when there is no head).
func injectAtTop(page []byte, s string) []byte {
	script := []byte(s)
	if loc := headRe.FindIndex(page); loc != nil {
		return append(append(append([]byte{}, page[:loc[1]]...), script...), page[loc[1]:]...)
	}
	i := 0
	if bytes.HasPrefix(bytes.ToLower(bytes.TrimSpace(page)), []byte("<!doctype")) {
		if j := bytes.IndexByte(page, '>'); j >= 0 {
			i = j + 1
		}
	}
	return append(append(append([]byte{}, page[:i]...), script...), page[i:]...)
}

// HTMLHook is the Options.Hook that rewrites the HTML answers of a program.
func HTMLHook(prefix string) func(*http.Response) error { return HTMLHookWith(prefix, nil, "") }

// HTMLHookWith is HTMLHook for a page that also gets the storage of st, for scope.
func HTMLHookWith(prefix string, st *StorageInfo, scope string) func(*http.Response) error {
	return func(res *http.Response) error {
		if done, err := wrapPlain(res); done || err != nil {
			return err
		}
		if !strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/html") || res.StatusCode == http.StatusSwitchingProtocols {
			return nil
		}
		if n, err := strconv.Atoi(res.Header.Get("Content-Length")); err == nil && n > maxRewrite {
			return nil
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, maxRewrite+1))
		if err != nil {
			return err
		}
		if len(body) > maxRewrite {
			// too big to rewrite: what was read, then the rest, unchanged
			res.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(body), res.Body), res.Body}
			return nil
		}
		res.Body.Close()
		out := RewriteHTML(body, prefix)
		if s := st.script(scope); s != "" {
			out = injectAtTop(out, s)
		}
		res.Body = io.NopCloser(bytes.NewReader(out))
		res.ContentLength = int64(len(out))
		res.Header.Set("Content-Length", strconv.Itoa(len(out)))
		res.Header.Del("Content-Encoding")
		res.Header.Del("Etag")
		return nil
	}
}

// clientScript is layer two: addresses that start with "/" (or name this host) get the prefix, for fetch, XMLHttpRequest,
// WebSocket and EventSource; two keys that the editor uses are passed up to it.
func clientScript(prefix string) string {
	return `<script>(function(){var P=` + jsString(prefix) + `;
function fix(u){try{var a=new URL(u,location.href);if(a.host===location.host&&a.pathname.indexOf(P+"/")!==0&&a.pathname!==P){a.pathname=P+a.pathname}return a.href}catch(e){return u}}
function rel(u){return typeof u==="string"&&u.charAt(0)==="/"&&u.charAt(1)!=="/"&&u.indexOf(P+"/")!==0?P+u:u}
var F=window.fetch;if(F){window.fetch=function(i,o){if(typeof i==="string"){i=rel(i)}else if(i&&i.url){var u=fix(i.url);if(u!==i.url){i=new Request(u,i)}}return F.call(this,i,o)}}
var X=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(m,u){var a=[].slice.call(arguments);a[1]=typeof u==="string"?(u.charAt(0)==="/"?rel(u):fix(u)):u;return X.apply(this,a)};
function wrap(name){var O=window[name];if(!O)return;var W=function(u,p){u=fix(String(u));return p===undefined?new O(u):new O(u,p)};W.prototype=O.prototype;for(var k in O){try{W[k]=O[k]}catch(e){}}window[name]=W}
wrap("WebSocket");wrap("EventSource");
})();</script>` + BridgeScript
}

// BridgeScript is added to every page the preview shows (a program's and a file of the workspace): it reports what the page
// does to the pane around it with postMessage, and passes the keys that the editor uses. The pane takes messages only from
// its own frame. What it reports: the address and title of the page (Back and Forward, the address bar), what it writes to the
// console and its uncaught errors (the Console tab), the calls it makes with fetch and XMLHttpRequest (the Requests tab).
const BridgeScript = `<script>(function(){var M="openrepl";
function post(m){try{m[M]=m.t;parent.postMessage(m,"*")}catch(e){}}
function str(v){try{if(typeof v==="string")return v;if(v instanceof Error)return v.stack||String(v);return JSON.stringify(v)}catch(e){return String(v)}}
function nav(){post({t:"nav",href:location.href,title:document.title})}
addEventListener("load",nav);addEventListener("popstate",nav);addEventListener("hashchange",nav);
["pushState","replaceState"].forEach(function(k){var o=history[k];history[k]=function(){var r=o.apply(this,arguments);setTimeout(nav,0);return r}});
["log","info","warn","error","debug"].forEach(function(k){var o=console[k];console[k]=function(){try{post({t:"console",level:k,text:[].map.call(arguments,str).join(" ").slice(0,2000)})}catch(e){}return o.apply(this,arguments)}});
addEventListener("error",function(e){post({t:"console",level:"error",text:(e.message||"Error")+(e.filename?" ("+e.filename.split("/").pop()+":"+e.lineno+")":"")})});
addEventListener("unhandledrejection",function(e){post({t:"console",level:"error",text:"Unhandled promise rejection: "+str(e.reason)})});
var F=window.fetch;if(F){window.fetch=function(i,o){var t0=Date.now(),m=(o&&o.method)||(i&&i.method)||"GET",u=typeof i==="string"?i:(i&&i.url)||"";return F.apply(this,arguments).then(function(r){post({t:"request",method:m,url:u,status:r.status,ms:Date.now()-t0});return r},function(e){post({t:"request",method:m,url:u,status:0,ms:Date.now()-t0});throw e})}}
var XO=XMLHttpRequest.prototype.open,XS=XMLHttpRequest.prototype.send;XMLHttpRequest.prototype.open=function(m,u){this._or=[m,u];return XO.apply(this,arguments)};
XMLHttpRequest.prototype.send=function(){var x=this,t0=Date.now();x.addEventListener("loadend",function(){var r=x._or||["GET",""];post({t:"request",method:r[0],url:String(r[1]),status:x.status,ms:Date.now()-t0})});return XS.apply(this,arguments)};
var esc=0;
addEventListener("keydown",function(e){var k=e.key;
if(e.ctrlKey&&!e.shiftKey&&!e.altKey&&!e.metaKey&&(k==="` + "`" + `"||e.code==="Backquote")){e.preventDefault();post({t:"key",key:"terminal"});return}
if(e.ctrlKey&&e.altKey&&!e.shiftKey&&!e.metaKey&&(k==="v"||e.code==="KeyV")){e.preventDefault();post({t:"key",key:"preview"});return}
if(e.ctrlKey&&!e.shiftKey&&!e.altKey&&!e.metaKey&&(k==="l"||k==="L")){e.preventDefault();post({t:"key",key:"address"});return}
if(k==="F6"){e.preventDefault();post({t:"key",key:"focus"});return}
if(k==="Escape"){var n=Date.now();if(n-esc<700){post({t:"key",key:"escape"});esc=0}else{esc=n}}
},true);
})();</script>`

func jsString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == 0x2028 || r == 0x2029:
			b.WriteString(`\u`)
			b.WriteString(strings.Repeat("0", 4-len(strconv.FormatInt(int64(r), 16))))
			b.WriteString(strconv.FormatInt(int64(r), 16))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
