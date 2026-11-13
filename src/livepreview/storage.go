package livepreview

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
)

// The page in the frame has no origin of its own, so the browser gives it no localStorage, sessionStorage or cookies of its
// own (document.cookie and the two stores throw). The preview gives it ones that work: the server keeps them, and the page
// gets them in its own script (StorageInfo.script), so that a read is synchronous as in a browser; a write goes back to the
// server in the background (POST /preview/<token>/s). Nothing of the site's own storage is touched.

const (
	// MaxArea is the most one store of one page may hold, as JSON.
	MaxArea = 5 << 20
	// maxStoreKeys bounds the number of stores the server keeps; the oldest goes first.
	maxStoreKeys = 4096
)

// Store keeps the storage of previewed pages in memory: for each workspace and scope (the folder of a page, or the port of a
// program) a "local" store and the "cookie" store; and a "session" store for each viewer. It does not outlive the server.
type Store struct {
	mu    sync.Mutex
	m     map[string]json.RawMessage
	order []string
}

// NewStore makes an empty Store.
func NewStore() *Store { return &Store{m: map[string]json.RawMessage{}} }

func storeKey(area, home, viewer, scope string) string {
	if area == "session" {
		return "session|" + home + "|" + viewer + "|" + scope
	}
	return area + "|" + home + "|" + scope
}

var errBadArea = errors.New("storage: an area is local, session or cookie")

// Set replaces one area of a scope. data has to be a JSON object that is not larger than MaxArea.
func (s *Store) Set(area, home, viewer, scope string, data json.RawMessage) error {
	if area != "local" && area != "session" && area != "cookie" {
		return errBadArea
	}
	d := bytes.TrimSpace(data)
	if len(d) == 0 || d[0] != '{' || !json.Valid(d) {
		return errors.New("storage: the data has to be a JSON object")
	}
	if len(d) > MaxArea {
		return errors.New("storage: more than 5 MB")
	}
	k := storeKey(area, home, viewer, scope)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[k]; !ok {
		if len(s.order) >= maxStoreKeys {
			delete(s.m, s.order[0])
			s.order = s.order[1:]
		}
		s.order = append(s.order, k)
	}
	s.m[k] = append(json.RawMessage{}, d...)
	return nil
}

// Snapshot is the JSON object {"local":{...},"session":{...},"cookie":{...}} that a page starts with.
func (s *Store) Snapshot(home, viewer, scope string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	b.WriteByte('{')
	for i, a := range []string{"local", "session", "cookie"} {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"` + a + `":`)
		if d, ok := s.m[storeKey(a, home, viewer, scope)]; ok {
			b.Write(d)
		} else {
			b.WriteString("{}")
		}
	}
	b.WriteByte('}')
	return b.String()
}

// StorageInfo says whose storage a page gets and where it writes it back.
type StorageInfo struct {
	Store  *Store
	Home   string
	Viewer string
	URL    string // "/preview/<token>/s"
}

// script is the script that gives a page its storage, for the scope; "" when there is no store.
func (si *StorageInfo) script(scope string) string {
	if si == nil || si.Store == nil {
		return ""
	}
	snap := strings.ReplaceAll(si.Store.Snapshot(si.Home, si.Viewer, scope), "<", `\u003c`)
	return "<script>(function(){var S=" + snap + ",U=" + jsString(si.URL) + ",SC=" + jsString(scope) + storageJS
}

// storageScope is the scope of a file of the workspace: its folder (the files of a folder are one site).
func storageScope(rel string) string {
	d := path.Dir(path.Clean("/" + rel))
	if d != "/" {
		d += "/"
	}
	return "f:" + d
}

// ProgramScope is the scope of a program: its port.
func ProgramScope(port int) string { return "p:" + itoa(port) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	return string(b[i:])
}

// HandleStorage takes the writes of a page: POST /preview/<token>/s with {"scope":..., "area":..., "data":{...}} as text.
func HandleStorage(rw http.ResponseWriter, req *http.Request, st *Store, home, viewer string) {
	SandboxHeaders(rw.Header())
	if req.Method == http.MethodOptions {
		rw.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		rw.Header().Set("Access-Control-Allow-Headers", "*")
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	if req.Method != http.MethodPost || st == nil {
		rw.Header().Set("Allow", "POST, OPTIONS")
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(rw, req.Body, MaxArea+8192))
	if err != nil {
		http.Error(rw, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	var in struct {
		Scope string          `json:"scope"`
		Area  string          `json:"area"`
		Data  json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &in) != nil || in.Scope == "" || len(in.Scope) > 300 {
		http.Error(rw, "bad request", http.StatusBadRequest)
		return
	}
	if err := st.Set(in.Area, home, viewer, in.Scope, in.Data); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// storageJS is the rest of the script: the three stores and document.cookie, over S (the snapshot).
const storageJS = `,N=window.fetch;
function own(o,k){return Object.prototype.hasOwnProperty.call(o,k)}
var T={};function save(a,d){clearTimeout(T[a]);T[a]=setTimeout(function(){try{N.call(window,U,{method:"POST",headers:{"Content-Type":"text/plain"},body:JSON.stringify({scope:SC,area:a,data:d}),keepalive:true})}catch(e){}},40)}
function mk(a){var d=S[a]||(S[a]={});var m={getItem:function(k){k=String(k);return own(d,k)?d[k]:null},setItem:function(k,v){d[String(k)]=String(v);save(a,d)},removeItem:function(k){delete d[String(k)];save(a,d)},clear:function(){for(var k in d)delete d[k];save(a,d)},key:function(i){var ks=Object.keys(d);return i>=0&&i<ks.length?ks[i]:null}};
return new Proxy(m,{get:function(t,p){if(p==="length")return Object.keys(d).length;if(p in t)return t[p];if(typeof p==="string"&&own(d,p))return d[p];return undefined},set:function(t,p,v){if(typeof p==="string"){d[p]=String(v);save(a,d)}return true},deleteProperty:function(t,p){if(typeof p==="string"&&own(d,p)){delete d[p];save(a,d)}return true},has:function(t,p){return p in t||own(d,p)},ownKeys:function(){return Object.keys(d)},getOwnPropertyDescriptor:function(t,p){return own(d,p)?{value:d[p],writable:true,enumerable:true,configurable:true}:undefined}})}
var L=mk("local"),SS=mk("session");
function def(o,n,v){try{Object.defineProperty(o,n,{configurable:true,get:function(){return v}})}catch(e){}}
def(window,"localStorage",L);def(window,"sessionStorage",SS);
var C=S.cookie||(S.cookie={});
function live(){var now=Date.now(),out=[];for(var k in C){var c=C[k];if(c.e&&c.e<=now){delete C[k]}else{out.push(k+"="+c.v)}}return out.join("; ")}
try{Object.defineProperty(document,"cookie",{configurable:true,get:function(){return live()},set:function(s){var p=String(s).split(";"),nv=p[0],i=nv.indexOf("=");if(i<0)return;var k=nv.slice(0,i).trim(),v=nv.slice(i+1).trim(),e=0;
for(var j=1;j<p.length;j++){var x=p[j].trim(),q=x.indexOf("="),an=(q<0?x:x.slice(0,q)).toLowerCase(),av=q<0?"":x.slice(q+1);if(an==="max-age"){e=Date.now()+parseInt(av,10)*1000}else if(an==="expires"){var t=Date.parse(av);if(!isNaN(t))e=t}}
if(e&&e<=Date.now()){delete C[k]}else{C[k]={v:v,e:e}}save("cookie",C)}})}catch(e){}
})();</script>`
