package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The canvas of the Preview pane (docs/lld/21-genie-canvas.md): boards that Genie draws and the user moves, retypes and
// pins notes on. Everything lives in the workspace, in .genie/canvas/<name>/ (a workspace can hold several canvases; the
// first is "main"):
//
//	canvas.json              the layout: the boards (frame, title, which version is current), the pins, the last changes
//	boards/<id>.html        one plain web page for each board: open it alone in Preview, copy it, edit it in the editor
//	versions/<id>/<n>.html  every version of a board, for Undo, Redo and Compare
//
// A change to a board is a new version, so Undo and Redo move along the versions of that board and Genie's changes can
// be taken back. One request, one operation:
//
//	POST /genie_canvas {"op":"list"} / {"op":"open","canvas":"login-flow"}   the canvases of the workspace; make or open one
//	(every other call names its canvas: "canvas":"login-flow", "main" when left out)
//	POST /genie_canvas {"op":"state","since":rev}                          the boards, the pins, the last changes ("unchanged" when since is the revision)
//	POST /genie_canvas {"op":"read","id":"pricing"}                         the HTML of the current version
//	POST /genie_canvas {"op":"put","id":"pricing","title":..,"html":..,"x":..,"by":"genie"|"user","label":".."}
//	POST /genie_canvas {"op":"layout","boards":{"pricing":{"x":0,"y":0,"w":880,"h":640}}}   move and size (not a version)
//	POST /genie_canvas {"op":"undo"|"redo"|"versions"|"restore","id":..,"n":..}
//	POST /genie_canvas {"op":"duplicate"|"delete","id":..}
//	POST /genie_canvas {"op":"pin","pin":{...}} / {"op":"unpin","id":..} / {"op":"pinsent","ids":[..]}
//
// It runs where the workspace is (execution-bound in the gateway, like genie_chats). The owner and the collaborators
// of a shared session (who name the owner's workspace with its signature) reach it; a fork does not. An admin can switch
// it off (CanvasOff); the page hides the tab and Genie's actions then.

const (
	canvasDirName     = ".genie"
	canvasSubdir      = "canvas"
	canvasIndexFile   = "canvas.json"
	maxCanvasBoards   = 24
	maxBoardBytes     = 512 << 10
	maxBoardVersions  = 30
	maxCanvasPins     = 200
	maxCanvasLog      = 60
	maxPinText        = 2000
	canvasPerMinute   = 360
	maxCanvasBody     = 700 << 10
	canvasGap         = 80
	canvasMinSide     = 40
	canvasMaxSide     = 8000
	canvasDefaultW    = 880
	canvasDefaultH    = 640
	canvasPlaceStep   = 40
	canvasPlaceWidth  = 3000
	canvasPlaceHeight = 24000
)

var boardIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
var pinIDPattern = regexp.MustCompile(`^[a-z0-9]{4,16}$`)

type canvasBoard struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Head  int     `json:"head"` // the version that is shown
	Max   int     `json:"max"`  // the newest version there is (more than Head after an Undo)
	By    string  `json:"by"`   // who wrote the current version: "genie" or "user"
	At    int64   `json:"at"`
	// Vers says who wrote each version and what they did, by version number, for the list of versions
	Vers map[string]canvasVer `json:"vers,omitempty"`
}

type canvasVer struct {
	By    string `json:"by"`
	Label string `json:"label"`
	At    int64  `json:"at"`
}

type canvasPin struct {
	ID     string  `json:"id"`
	Board  string  `json:"board"`
	X      float64 `json:"x"` // where on the board, as a part of its width and height (0 to 1)
	Y      float64 `json:"y"`
	Text   string  `json:"text"`
	El     string  `json:"el"`   // the part of the board it points at: a selector and the first words of its text
	Sent   bool    `json:"sent"` // Genie has been given it
	By     string  `json:"by"`
	At     int64   `json:"at"`
	Answer string  `json:"answer,omitempty"` // what Genie said to it (canvas_note)
}

type canvasLog struct {
	At    int64  `json:"at"`
	By    string `json:"by"`
	Board string `json:"board"`
	Text  string `json:"text"`
	Rev   int64  `json:"rev"`
}

type canvasIndex struct {
	V      int           `json:"v"`
	Rev    int64         `json:"rev"`
	Boards []canvasBoard `json:"boards"`
	Pins   []canvasPin   `json:"pins"`
	Log    []canvasLog   `json:"log"`
}

type canvasFrame struct {
	X *float64 `json:"x"`
	Y *float64 `json:"y"`
	W *float64 `json:"w"`
	H *float64 `json:"h"`
}

type canvasRequest struct {
	Op     string                 `json:"op"`
	Canvas string                 `json:"canvas"`
	ID     string                 `json:"id"`
	Title  string                 `json:"title"`
	HTML   *string                `json:"html"`
	By     string                 `json:"by"`
	Label  string                 `json:"label"`
	N      int                    `json:"n"`
	Since  int64                  `json:"since"`
	Boards map[string]canvasFrame `json:"boards"`
	Pin    *canvasPin             `json:"pin"`
	IDs    []string               `json:"ids"`
	canvasFrame
}

// one workspace is changed by one request at a time
var canvasLocks sync.Map

func canvasLock(dir string) *sync.Mutex {
	m, _ := canvasLocks.LoadOrStore(dir, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func canvasRoot(realHome string) string {
	return filepath.Join(realHome, canvasDirName, canvasSubdir)
}

// canvasFolder is the folder of one canvas of the workspace (made when create is set).
func canvasFolder(realHome, name string, create bool) (string, error) {
	dir := filepath.Join(canvasRoot(realHome), name)
	for _, d := range []string{filepath.Join(realHome, canvasDirName), canvasRoot(realHome), dir} {
		if st, err := os.Lstat(d); err == nil && !st.IsDir() {
			return "", fmt.Errorf("%s is not a folder", d)
		}
	}
	if create {
		for _, d := range []string{"boards", "versions"} {
			if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}

// canvasNames are the canvases of the workspace, the ones that have a layout file, newest first.
func canvasNames(realHome string) []map[string]interface{} {
	out := []map[string]interface{}{}
	ents, err := os.ReadDir(canvasRoot(realHome))
	if err != nil {
		return out
	}
	for _, e := range ents {
		if !e.IsDir() || !boardIDPattern.MatchString(e.Name()) {
			continue
		}
		st, err := os.Stat(filepath.Join(canvasRoot(realHome), e.Name(), canvasIndexFile))
		if err != nil {
			continue
		}
		ix := readCanvasIndex(filepath.Join(canvasRoot(realHome), e.Name()))
		out = append(out, map[string]interface{}{"name": e.Name(), "boards": len(ix.Boards), "at": st.ModTime().Unix()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["at"].(int64) > out[j]["at"].(int64) })
	return out
}

func readCanvasIndex(dir string) canvasIndex {
	ix := canvasIndex{V: 1, Boards: []canvasBoard{}, Pins: []canvasPin{}, Log: []canvasLog{}}
	b, err := os.ReadFile(filepath.Join(dir, canvasIndexFile))
	if err != nil {
		return ix
	}
	if json.Unmarshal(b, &ix) != nil {
		return canvasIndex{V: 1, Boards: []canvasBoard{}, Pins: []canvasPin{}, Log: []canvasLog{}}
	}
	if ix.Boards == nil {
		ix.Boards = []canvasBoard{}
	}
	if ix.Pins == nil {
		ix.Pins = []canvasPin{}
	}
	if ix.Log == nil {
		ix.Log = []canvasLog{}
	}
	return ix
}

func writeCanvasIndex(dir string, ix *canvasIndex) error {
	ix.V = 1
	ix.Rev++
	b, err := json.MarshalIndent(ix, "", " ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, canvasIndexFile), b)
}

// boardFile is the file of one version of a board.
func boardFile(dir, id string, n int) string {
	return filepath.Join(dir, "versions", id, strconv.Itoa(n)+".html")
}

// headFile is the plain page of a board, the one that is shown now.
func headFile(dir, id string) string {
	return filepath.Join(dir, "boards", id+".html")
}

// syncHead makes boards/<id>.html the version that is shown.
func syncHead(dir string, b *canvasBoard) error {
	data, err := os.ReadFile(boardFile(dir, b.ID, b.Head))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "boards"), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(headFile(dir, b.ID), data)
}

func (ix *canvasIndex) find(id string) int {
	for i := range ix.Boards {
		if ix.Boards[i].ID == id {
			return i
		}
	}
	return -1
}

func clampSide(v float64) float64 {
	if v < canvasMinSide {
		return canvasMinSide
	}
	if v > canvasMaxSide {
		return canvasMaxSide
	}
	return float64(int(v + 0.5))
}

func cleanCoord(v float64) float64 {
	if v < -100000 {
		return -100000
	}
	if v > 100000 {
		return 100000
	}
	return float64(int(v + 0.5))
}

func overlaps(a, b canvasBoard) bool {
	return a.X < b.X+b.W+canvasGap && a.X+a.W+canvasGap > b.X && a.Y < b.Y+b.H+canvasGap && a.Y+a.H+canvasGap > b.Y
}

// placeBoard finds the first free spot for a board of this size, scanning rows from the top left, a gap away from the others.
func placeBoard(ix *canvasIndex, w, h float64) (float64, float64) {
	for y := 0.0; y < canvasPlaceHeight; y += canvasPlaceStep {
		for x := 0.0; x < canvasPlaceWidth; x += canvasPlaceStep {
			c := canvasBoard{X: x, Y: y, W: w, H: h}
			free := true
			for _, b := range ix.Boards {
				if overlaps(c, b) {
					free = false
					break
				}
			}
			if free {
				return x, y
			}
		}
	}
	return 0, 0
}

func addCanvasLog(ix *canvasIndex, by, board, text string) {
	if by != "user" {
		by = "genie"
	}
	ix.Log = append(ix.Log, canvasLog{At: time.Now().Unix(), By: by, Board: board, Text: truncRunes(text, 160), Rev: ix.Rev + 1})
	if len(ix.Log) > maxCanvasLog {
		ix.Log = ix.Log[len(ix.Log)-maxCanvasLog:]
	}
}

func truncRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func cleanTitle(s, fallback string) string {
	s = truncRunes(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s), 80)
	if s == "" {
		return fallback
	}
	return s
}

// pruneBoardVersions removes the versions older than the newest maxBoardVersions (never the one that is shown).
func pruneBoardVersions(dir string, b *canvasBoard) {
	for n := b.Max - maxBoardVersions; n >= 1; n-- {
		if n == b.Head {
			continue
		}
		if os.Remove(boardFile(dir, b.ID, n)) != nil {
			break
		}
	}
}

// writeBoardVersion stores html as the next version of the board and makes it the shown one (a version after an Undo
// replaces the ones that were undone).
func writeBoardVersion(dir string, b *canvasBoard, html string, by string, label string) error {
	if err := os.MkdirAll(filepath.Join(dir, "versions", b.ID), 0o700); err != nil {
		return err
	}
	for n := b.Head + 1; n <= b.Max; n++ {
		os.Remove(boardFile(dir, b.ID, n))
	}
	b.Max = b.Head + 1
	if err := writeFileAtomic(boardFile(dir, b.ID, b.Max), []byte(html)); err != nil {
		b.Max = b.Head
		return err
	}
	b.Head = b.Max
	if err := syncHead(dir, b); err != nil {
		return err
	}
	if by != "user" {
		by = "genie"
	}
	b.By = by
	b.At = time.Now().Unix()
	if b.Vers == nil {
		b.Vers = map[string]canvasVer{}
	}
	b.Vers[strconv.Itoa(b.Head)] = canvasVer{By: by, Label: truncRunes(label, 120), At: b.At}
	for k := range b.Vers {
		if n, err := strconv.Atoi(k); err != nil || n > b.Head || n <= b.Head-maxBoardVersions {
			delete(b.Vers, k)
		}
	}
	pruneBoardVersions(dir, b)
	return nil
}

func validBoardHTML(s string) string {
	if len(s) > maxBoardBytes {
		return fmt.Sprintf("A board is at most %d KB of HTML.", maxBoardBytes>>10)
	}
	if !utf8.ValidString(s) {
		return "The board is not text."
	}
	return ""
}

func (server *Server) handleGenieCanvas(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if GetSiteSettings().CanvasOff {
		jsonError(rw, http.StatusForbidden, "off", "The canvas is switched off on this site.")
		return
	}
	req.ParseForm()
	// a fork names its workspace by a link too, and is not for the canvas; a shared session's signed workspace is
	if req.Form.Get("jid") != "" {
		jsonError(rw, http.StatusForbidden, "fork", "A fork has no canvas of its own.")
		return
	}
	uid, home := server.requestIdentity(rw, req)
	var in canvasRequest
	if err := json.NewDecoder(http.MaxBytesReader(rw, req.Body, maxCanvasBody)).Decode(&in); err != nil {
		jsonError(rw, http.StatusBadRequest, "bad_request", "send {\"op\": ...}")
		return
	}
	who := uid
	if who == "" {
		who = home
	}
	if !chatsAllowed("canvas:"+who, time.Now()) {
		jsonError(rw, http.StatusTooManyRequests, "rate", "Too many requests in a minute: wait a little.")
		return
	}
	os.MkdirAll(home, 0o755)
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		jsonError(rw, http.StatusNotFound, "no_workspace", "The workspace is not there yet.")
		return
	}
	if in.Op == "list" {
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(map[string]interface{}{"canvases": canvasNames(realHome)})
		return
	}
	name := in.Canvas
	if name == "" {
		name = "main"
	}
	if !boardIDPattern.MatchString(name) {
		jsonError(rw, http.StatusBadRequest, "bad_canvas", "A canvas name is lower case letters, digits and hyphens.")
		return
	}
	writes := in.Op != "state" && in.Op != "read" && in.Op != "versions"
	dir, err := canvasFolder(realHome, name, writes)
	if err != nil {
		jsonError(rw, http.StatusInternalServerError, "folder", "The canvas folder could not be used.")
		return
	}
	reply := func(v interface{}) {
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(v)
	}
	lock := canvasLock(dir)
	lock.Lock()
	defer lock.Unlock()
	ix := readCanvasIndex(dir)
	limits := map[string]int{"boards": maxCanvasBoards, "kb": maxBoardBytes >> 10, "versions": maxBoardVersions, "pins": maxCanvasPins}
	stateOf := func() map[string]interface{} {
		log := ix.Log
		if len(log) > 30 {
			log = log[len(log)-30:]
		}
		return map[string]interface{}{"enabled": true, "canvas": name, "rev": ix.Rev, "boards": ix.Boards, "pins": ix.Pins, "log": log, "limits": limits}
	}
	needBoard := func() (int, bool) {
		if !boardIDPattern.MatchString(in.ID) {
			jsonError(rw, http.StatusBadRequest, "bad_id", "A board name is lower case letters, digits and hyphens.")
			return -1, false
		}
		i := ix.find(in.ID)
		if i < 0 {
			jsonError(rw, http.StatusNotFound, "no_board", "There is no board with that name.")
			return -1, false
		}
		return i, true
	}
	save := func() bool {
		if err := writeCanvasIndex(dir, &ix); err != nil {
			jsonError(rw, http.StatusInternalServerError, "write", "The canvas could not be saved.")
			return false
		}
		return true
	}
	done := func() { reply(stateOf()) }

	switch in.Op {
	case "open":
		if _, err := os.Stat(filepath.Join(dir, canvasIndexFile)); err != nil {
			if !save() {
				return
			}
		}
		done()
	case "state":
		if in.Since != 0 && in.Since == ix.Rev {
			reply(map[string]interface{}{"enabled": true, "unchanged": true, "rev": ix.Rev})
			return
		}
		done()
	case "read":
		i, ok := needBoard()
		if !ok {
			return
		}
		b := ix.Boards[i]
		n := b.Head
		if in.N > 0 {
			n = in.N // an older version, for Compare
		}
		data, err := os.ReadFile(boardFile(dir, b.ID, n))
		if err != nil {
			jsonError(rw, http.StatusNotFound, "no_file", "The board's file is not there.")
			return
		}
		reply(map[string]interface{}{"board": b, "html": string(data), "n": n})
	case "put":
		if !boardIDPattern.MatchString(in.ID) {
			jsonError(rw, http.StatusBadRequest, "bad_id", "A board name is lower case letters, digits and hyphens.")
			return
		}
		if in.HTML == nil {
			jsonError(rw, http.StatusBadRequest, "no_html", "Send the board's html.")
			return
		}
		if msg := validBoardHTML(*in.HTML); msg != "" {
			jsonError(rw, http.StatusRequestEntityTooLarge, "too_big", msg)
			return
		}
		i := ix.find(in.ID)
		isNew := i < 0
		if isNew {
			if len(ix.Boards) >= maxCanvasBoards {
				jsonError(rw, http.StatusConflict, "too_many", fmt.Sprintf("A canvas holds at most %d boards: delete one first.", maxCanvasBoards))
				return
			}
			w, h := float64(canvasDefaultW), float64(canvasDefaultH)
			if in.W != nil {
				w = clampSide(*in.W)
			}
			if in.H != nil {
				h = clampSide(*in.H)
			}
			x, y := placeBoard(&ix, w, h)
			if in.X != nil {
				x = cleanCoord(*in.X)
			}
			if in.Y != nil {
				y = cleanCoord(*in.Y)
			}
			ix.Boards = append(ix.Boards, canvasBoard{ID: in.ID, Title: cleanTitle(in.Title, in.ID), X: x, Y: y, W: w, H: h})
			i = len(ix.Boards) - 1
		} else {
			b := &ix.Boards[i]
			if strings.TrimSpace(in.Title) != "" {
				b.Title = cleanTitle(in.Title, b.ID)
			}
			if in.X != nil {
				b.X = cleanCoord(*in.X)
			}
			if in.Y != nil {
				b.Y = cleanCoord(*in.Y)
			}
			if in.W != nil {
				b.W = clampSide(*in.W)
			}
			if in.H != nil {
				b.H = clampSide(*in.H)
			}
		}
		label := in.Label
		if label == "" {
			if isNew {
				label = "added the board " + ix.Boards[i].Title
			} else {
				label = "changed the board " + ix.Boards[i].Title
			}
		}
		if err := writeBoardVersion(dir, &ix.Boards[i], *in.HTML, in.By, label); err != nil {
			if isNew {
				ix.Boards = ix.Boards[:len(ix.Boards)-1]
			}
			jsonError(rw, http.StatusInternalServerError, "write", "The board could not be saved.")
			return
		}
		addCanvasLog(&ix, in.By, in.ID, label)
		if !save() {
			return
		}
		done()
	case "layout":
		for id, f := range in.Boards {
			i := ix.find(id)
			if i < 0 {
				continue
			}
			b := &ix.Boards[i]
			if f.X != nil {
				b.X = cleanCoord(*f.X)
			}
			if f.Y != nil {
				b.Y = cleanCoord(*f.Y)
			}
			if f.W != nil {
				b.W = clampSide(*f.W)
			}
			if f.H != nil {
				b.H = clampSide(*f.H)
			}
		}
		if !save() {
			return
		}
		done()
	case "undo", "redo", "restore":
		i, ok := needBoard()
		if !ok {
			return
		}
		b := &ix.Boards[i]
		switch in.Op {
		case "undo":
			if b.Head <= 1 {
				jsonError(rw, http.StatusConflict, "nothing", "There is nothing to undo on this board.")
				return
			}
			if _, err := os.Stat(boardFile(dir, b.ID, b.Head-1)); err != nil {
				jsonError(rw, http.StatusConflict, "nothing", "The older version is gone: there is nothing to undo.")
				return
			}
			b.Head--
			addCanvasLog(&ix, in.By, b.ID, "undid a change on "+b.Title)
		case "redo":
			if b.Head >= b.Max {
				jsonError(rw, http.StatusConflict, "nothing", "There is nothing to redo on this board.")
				return
			}
			b.Head++
			addCanvasLog(&ix, in.By, b.ID, "redid a change on "+b.Title)
		case "restore":
			data, err := os.ReadFile(boardFile(dir, b.ID, in.N))
			if err != nil || in.N < 1 || in.N > b.Max {
				jsonError(rw, http.StatusNotFound, "no_version", "That version is not there.")
				return
			}
			if err := writeBoardVersion(dir, b, string(data), in.By, "went back to version "+strconv.Itoa(in.N)); err != nil {
				jsonError(rw, http.StatusInternalServerError, "write", "The board could not be saved.")
				return
			}
			addCanvasLog(&ix, in.By, b.ID, "went back to version "+strconv.Itoa(in.N)+" of "+b.Title)
		}
		if err := syncHead(dir, b); err != nil {
			jsonError(rw, http.StatusInternalServerError, "write", "The board could not be saved.")
			return
		}
		if !save() {
			return
		}
		done()
	case "versions":
		i, ok := needBoard()
		if !ok {
			return
		}
		b := ix.Boards[i]
		type ver struct {
			N     int    `json:"n"`
			At    int64  `json:"at"`
			By    string `json:"by"`
			Label string `json:"label"`
		}
		vs := []ver{}
		for n := b.Max; n >= 1 && len(vs) < maxBoardVersions; n-- {
			if st, err := os.Stat(boardFile(dir, b.ID, n)); err == nil {
				v := ver{N: n, At: st.ModTime().Unix()}
				if r, ok := b.Vers[strconv.Itoa(n)]; ok {
					v.By, v.Label, v.At = r.By, r.Label, r.At
				}
				vs = append(vs, v)
			}
		}
		reply(map[string]interface{}{"id": b.ID, "head": b.Head, "max": b.Max, "versions": vs})
	case "duplicate":
		i, ok := needBoard()
		if !ok {
			return
		}
		if len(ix.Boards) >= maxCanvasBoards {
			jsonError(rw, http.StatusConflict, "too_many", fmt.Sprintf("A canvas holds at most %d boards: delete one first.", maxCanvasBoards))
			return
		}
		src := ix.Boards[i]
		data, err := os.ReadFile(boardFile(dir, src.ID, src.Head))
		if err != nil {
			jsonError(rw, http.StatusNotFound, "no_file", "The board's file is not there.")
			return
		}
		newID := src.ID
		for k := 2; ; k++ {
			suffix := "-" + strconv.Itoa(k)
			base := src.ID
			if len(base)+len(suffix) > 40 {
				base = base[:40-len(suffix)]
			}
			if ix.find(base+suffix) < 0 {
				newID = base + suffix
				break
			}
		}
		cp := canvasBoard{ID: newID, Title: cleanTitle(src.Title+" copy", newID), W: src.W, H: src.H}
		cp.X, cp.Y = placeBoard(&ix, cp.W, cp.H)
		if err := writeBoardVersion(dir, &cp, string(data), "user", "duplicated "+src.Title); err != nil {
			jsonError(rw, http.StatusInternalServerError, "write", "The board could not be saved.")
			return
		}
		ix.Boards = append(ix.Boards, cp)
		addCanvasLog(&ix, in.By, newID, "duplicated the board "+src.Title)
		if !save() {
			return
		}
		reply(map[string]interface{}{"id": newID, "state": stateOf()})
	case "delete":
		i, ok := needBoard()
		if !ok {
			return
		}
		b := ix.Boards[i]
		os.RemoveAll(filepath.Join(dir, "versions", b.ID))
		os.Remove(headFile(dir, b.ID))
		ix.Boards = append(ix.Boards[:i], ix.Boards[i+1:]...)
		pins := ix.Pins[:0]
		for _, p := range ix.Pins {
			if p.Board != b.ID {
				pins = append(pins, p)
			}
		}
		ix.Pins = pins
		addCanvasLog(&ix, in.By, b.ID, "deleted the board "+b.Title)
		if !save() {
			return
		}
		done()
	case "pin":
		p := in.Pin
		if p == nil || ix.find(p.Board) < 0 {
			jsonError(rw, http.StatusBadRequest, "bad_pin", "A pin is on a board that is there.")
			return
		}
		text := truncRunes(p.Text, maxPinText)
		if text == "" {
			jsonError(rw, http.StatusBadRequest, "bad_pin", "Write the note.")
			return
		}
		fx, fy := p.X, p.Y
		if fx < 0 {
			fx = 0
		}
		if fx > 1 {
			fx = 1
		}
		if fy < 0 {
			fy = 0
		}
		if fy > 1 {
			fy = 1
		}
		by := "user"
		if p.By == "genie" {
			by = "genie"
		}
		found := -1
		for k := range ix.Pins {
			if ix.Pins[k].ID == p.ID && p.ID != "" {
				found = k
			}
		}
		if found >= 0 {
			q := &ix.Pins[found]
			q.Text, q.X, q.Y = text, fx, fy
			if p.Sent {
				q.Sent = true
			}
			if p.Answer != "" {
				q.Answer = truncRunes(p.Answer, 500)
				q.Sent = true
			}
		} else {
			if len(ix.Pins) >= maxCanvasPins {
				jsonError(rw, http.StatusConflict, "too_many", fmt.Sprintf("A canvas holds at most %d pins.", maxCanvasPins))
				return
			}
			id := p.ID
			if !pinIDPattern.MatchString(id) {
				id = strconv.FormatInt(time.Now().UnixNano(), 36)
				if len(id) > 12 {
					id = id[len(id)-12:]
				}
			}
			ix.Pins = append(ix.Pins, canvasPin{ID: id, Board: p.Board, X: fx, Y: fy, Text: text, El: truncRunes(p.El, 200), Sent: p.Sent, By: by, At: time.Now().Unix(), Answer: truncRunes(p.Answer, 500)})
		}
		if !save() {
			return
		}
		done()
	case "unpin":
		pins := ix.Pins[:0]
		for _, p := range ix.Pins {
			if p.ID != in.ID {
				pins = append(pins, p)
			}
		}
		ix.Pins = pins
		if !save() {
			return
		}
		done()
	case "pinsent":
		mark := map[string]bool{}
		for _, id := range in.IDs {
			mark[id] = true
		}
		for k := range ix.Pins {
			if mark[ix.Pins[k].ID] {
				ix.Pins[k].Sent = true
			}
		}
		if !save() {
			return
		}
		done()
	default:
		jsonError(rw, http.StatusBadRequest, "bad_op", "That is not an operation of the canvas.")
	}
}

// canvasBoardIDs is the list of the board names of an index, for tests and the knowledge of the page.
func canvasBoardIDs(ix canvasIndex) []string {
	out := make([]string, 0, len(ix.Boards))
	for _, b := range ix.Boards {
		out = append(out, b.ID)
	}
	sort.Strings(out)
	return out
}
