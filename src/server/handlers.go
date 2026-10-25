package server

import (
	"bytes"
	"containers"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"archive/zip"
	"cookie"
	"crypto/sha256"
	"encoder"
	"encoding/base64"
	"filebrowser"
	"gateway"
	"io"
	"io/ioutil"
	"meters"
	"os"
	"path/filepath"
	"sort"
	"utils"
	"webtty"
	"workspace"
	"wsync"
)

func writeWorkspaceFile(home *wsync.Home, rel string, data []byte) error {
	if rel == "" {
		return wsync.ErrUnsafePath
	}
	return home.WriteFile(wsync.Entry{
		Path: rel, Type: wsync.File, Size: int64(len(data)),
		ModTime: time.Now().UnixNano(), Mode: 0600,
	}, bytes.NewReader(data))
}

func workspaceTree(home *wsync.Home, root, rel string) (filebrowser.TreeNode, error) {
	entry, err := home.Lstat(rel)
	if err != nil {
		return filebrowser.TreeNode{}, err
	}
	label := filepath.Base(rel)
	if rel == "" {
		label = filepath.Base(root)
	}
	node := filebrowser.TreeNode{Id: root, Text: label, Type: "file"}
	if rel != "" {
		node.Id = filepath.Join(root, filepath.FromSlash(rel))
	}
	if entry.Type != wsync.Dir {
		return node, nil
	}
	node.Type = "default"
	names, err := home.ListDir(rel)
	if err != nil {
		return filebrowser.TreeNode{}, err
	}
	sort.Strings(names)
	for _, name := range names {
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		child, err := workspaceTree(home, root, childRel)
		if err != nil {
			// Unsupported special files are omitted from the browser.
			if err == wsync.ErrNotRegular {
				continue
			}
			return filebrowser.TreeNode{}, err
		}
		node.Children = append(node.Children, child)
	}
	return node, nil
}

func workspaceUsage(home *wsync.Home, rel string) (int64, error) {
	entry, err := home.Lstat(rel)
	if err != nil {
		return 0, err
	}
	if entry.Type == wsync.File {
		return entry.Size, nil
	}
	if entry.Type != wsync.Dir {
		return 0, nil
	}
	names, err := home.ListDir(rel)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, name := range names {
		child := name
		if rel != "" {
			child = rel + "/" + name
		}
		size, err := workspaceUsage(home, child)
		if err != nil {
			if err == wsync.ErrNotRegular {
				continue
			}
			return 0, err
		}
		total += size
	}
	return total, nil
}

func writeWorkspaceZip(home *wsync.Home, rootRel string, zw *zip.Writer) error {
	root, err := home.Lstat(rootRel)
	if err != nil {
		return err
	}
	var walk func(string) error
	walk = func(rel string) error {
		entry, err := home.Lstat(rel)
		if err != nil {
			return err
		}
		if entry.Type == wsync.Dir {
			names, err := home.ListDir(rel)
			if err != nil {
				return err
			}
			sort.Strings(names)
			for _, name := range names {
				child := name
				if rel != "" {
					child = rel + "/" + name
				}
				if err := walk(child); err != nil && err != wsync.ErrNotRegular {
					return err
				}
			}
			return nil
		}
		if entry.Type != wsync.File {
			return nil // do not follow or archive symlinks
		}
		file, err := home.OpenRead(rel)
		if err != nil {
			return err
		}
		defer file.Close()
		name := filepath.Base(rel)
		if root.Type == wsync.Dir {
			name, err = filepath.Rel(rootRel, rel)
			if err != nil {
				return err
			}
			name = filepath.ToSlash(name)
		}
		header := &zip.FileHeader{Name: name}
		header.SetMode(os.FileMode(entry.Mode & 0700))
		header.SetModTime(time.Unix(0, entry.ModTime))
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, file)
		return err
	}
	return walk(rootRel)
}

func processWorkspaceEvent(w http.ResponseWriter, home *wsync.Home, root string, event filebrowser.Event) {
	rel, err := workspace.RelativePath(root, event.Name)
	if err != nil || rel == "" {
		http.Error(w, "invalid workspace path", http.StatusBadRequest)
		return
	}
	dest := ""
	if event.NewName != "" {
		dest, err = workspace.RelativePath(root, event.NewName)
		if err != nil || dest == "" {
			http.Error(w, "invalid workspace destination", http.StatusBadRequest)
			return
		}
	}
	if event.Op&fsnotify.Create == fsnotify.Create {
		if dest != "" {
			err = copyWorkspacePath(home, rel, dest)
		} else if event.Type == "folder" || event.Type == "default" {
			err = home.PutDir(wsync.Entry{Path: rel, Type: wsync.Dir, Mode: 0700})
		} else {
			err = writeWorkspaceFile(home, rel, nil)
		}
	} else if event.Op&fsnotify.Remove == fsnotify.Remove {
		err = removeWorkspacePath(home, rel)
	} else if event.Op&fsnotify.Rename == fsnotify.Rename {
		err = home.Rename(rel, dest)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func removeWorkspacePath(home *wsync.Home, rel string) error {
	entry, err := home.Lstat(rel)
	if err != nil {
		return err
	}
	if entry.Type == wsync.Dir {
		names, err := home.ListDir(rel)
		if err != nil {
			return err
		}
		for _, name := range names {
			if err := removeWorkspacePath(home, rel+"/"+name); err != nil {
				return err
			}
		}
	}
	return home.Remove(rel)
}

func copyWorkspacePath(home *wsync.Home, from, to string) error {
	entry, err := home.Lstat(from)
	if err != nil {
		return err
	}
	switch entry.Type {
	case wsync.Dir:
		entry.Path = to
		entry.Mode = 0700
		if err := home.PutDir(entry); err != nil {
			return err
		}
		names, err := home.ListDir(from)
		if err != nil {
			return err
		}
		for _, name := range names {
			if err := copyWorkspacePath(home, from+"/"+name, to+"/"+name); err != nil && err != wsync.ErrNotRegular {
				return err
			}
		}
		return nil
	case wsync.Symlink:
		entry.Path = to
		entry.Link, err = home.Readlink(from)
		if err != nil {
			return err
		}
		return home.PutSymlink(entry)
	case wsync.File:
		file, err := home.OpenRead(from)
		if err != nil {
			return err
		}
		defer file.Close()
		entry.Path, entry.Mode = to, (entry.Mode&0700)|0600
		return home.WriteFile(entry, file)
	default:
		return wsync.ErrNotRegular
	}
}

func updateparams(params *url.Values, payload map[string]string) {
	for key, value := range payload {
		params.Set(key, value)
	}
}

func (server *Server) fetchRequestedPayload(w http.ResponseWriter, r *http.Request) (req_payload map[string]string) {
	req_payload = make(map[string]string)
	if isTrusted(r) {
		// On a worker the gateway has already decided who the user is.
		uid, homedir, privilege := server.trustedIdentity(r)
		req_payload[utils.UidKey] = uid
		req_payload[utils.HOME_DIR_KEY] = homedir
		req_payload[utils.USER_PRIVILEGE_KEY] = privilege
		return
	}
	uid, homedir := server.requestIdentity(w, r)
	req_payload[utils.UidKey] = uid
	req_payload[utils.HOME_DIR_KEY] = homedir
	if IsUserAdmin(w, r) {
		req_payload[utils.USER_PRIVILEGE_KEY] = utils.ADMIN
	} else {
		req_payload[utils.USER_PRIVILEGE_KEY] = utils.GUEST
	}
	return
}

func (server *Server) generateHandleWS(ctx context.Context, cancel context.CancelFunc, counter *counter, commands ...string) http.HandlerFunc {
	once := new(int64)

	go func() {
		select {
		case <-counter.timer().C:
			cancel()
		case <-ctx.Done():
		}
	}()

	return func(w http.ResponseWriter, r *http.Request) {
		var command string
		if len(commands) > 0 {
			command = commands[0]
		}
		if server.options.Once {
			success := atomic.CompareAndSwapInt64(once, 0, 1)
			if !success {
				http.Error(w, "Server is shutting down", http.StatusServiceUnavailable)
				return
			}
		}

		num := counter.add(1)
		// the dashboard's live map counts the terminals of each program
		replName := replLabel(command)
		meters.GaugeAdd(meters.Repl+replName, 1)
		wieght := containers.GetCommandWieght(command)
		totalWieght := counter.addWieght(int(wieght))
		closeReason := "unknown reason"
		closeCode := websocket.CloseNormalClosure

		defer func() {
			meters.GaugeAdd(meters.Repl+replName, -1)
			num := counter.done()
			totalWieght := counter.removeWieght(int(wieght))
			log.Printf(
				"Connection closed by %s: %s, connections: %d/%d, TotalUsage(MB): %d",
				closeReason, r.RemoteAddr, num, server.options.MaxConnection, totalWieght,
			)

			if server.options.Once {
				cancel()
			}
		}()

		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}

		req_payload := server.fetchRequestedPayload(w, r)
		if err := server.waitWorkspace(r, req_payload[utils.HOME_DIR_KEY]); err != nil {
			http.Error(w, "workspace is synchronizing, please try again", http.StatusServiceUnavailable)
			return
		}
		// any cookie needs to be saved before upgrading to websocket
		conn, err := server.upgrader.Upgrade(w, r, nil)
		if err != nil {
			closeReason = err.Error()
			log.Println("Can not upgrade connection: " + closeReason)
			return
		}

		defer func() {
			log.Println("close status: ", closeCode)
			conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, closeReason), time.Now().Add(time.Second))
			//wait for 1 sec deadline to write the buffers
			time.Sleep(2 * time.Second)
			conn.Close()
		}()

		// placed this statement here as we need to notify the closereason and close
		if int64(server.options.MaxConnection) != 0 {
			if num > server.options.MaxConnection || totalWieght > server.options.MaxConnection {
				closeReason = "exceeding max number of connections"
				WriteMessageToTerminal(conn, closeReason+", Please try after sometimes. ")
				return
			}
		}

		log.Printf("New client (uid: %s) connected: %s, connections: %d/%d, TotalUsage(MB): %d",
			req_payload[utils.UidKey], r.RemoteAddr, num, server.options.MaxConnection, totalWieght,
		)

		log.Println("Connection upgraded successfully: ")
		err = server.processWSConn(ctx, conn, r, command, req_payload)

		switch err {
		case ctx.Err():
			closeReason = "cancelation"
		case webtty.ErrSlaveClosed:
			closeReason = server.factory.Name()
		case errSlaveKilled:
			closeReason = server.factory.Name() + ": killed"
		case errSessionEnded:
			closeReason = gateway.EndedReason
		case webtty.ErrMasterClosed:
			closeReason = "client"
		default:
			closeReason = fmt.Sprintf("an error: %s", err)
		}
		log.Println("WS connection closed due to: " + closeReason)
	}
}

// process websocket connection for uid (user)
// req_payload is initial payload carried by request
// Note: Any time consuming API in this same routing will lead to performance issue with websocket
func (server *Server) processWSConn(ctx context.Context, conn *websocket.Conn, r *http.Request, command string, req_payload map[string]string) error {
	conn.SetWriteDeadline(time.Now().Add(utils.DEADLINE_MINUTES * time.Minute)) // only 15 min sessions for services are allowed

	// An admin who ends the session cancels the request (gateway/terminals.go).
	ctx, endTerminal := context.WithCancel(ctx)
	defer endTerminal()
	go func() {
		select {
		case <-r.Context().Done():
			endTerminal()
		case <-ctx.Done():
		}
	}()

	typ, initLine, err := conn.ReadMessage()
	if err != nil {
		return errors.Wrapf(err, "failed to authenticate websocket connection")
	}
	if typ != websocket.TextMessage {
		return errors.New("failed to authenticate websocket connection: invalid message type")
	}
	log.Println("init message read: ", typ, string(initLine))
	var init InitMessage
	err = json.Unmarshal(initLine, &init)
	if err != nil {
		return errors.Wrapf(err, "failed to authenticate websocket connection")
	}
	if init.AuthToken != server.credentialFor(r) {
		return errors.New("failed to authenticate websocket connection")
	}

	queryPath := "?"
	if server.options.PermitArguments && init.Arguments != "" {
		queryPath = init.Arguments
	}

	query, err := url.Parse(queryPath)
	if err != nil {
		return errors.Wrapf(err, "failed to parse arguments")
	}
	params := query.Query()
	updateparams(&params, req_payload)  // update the params with reqest payload
	updateparams(&params, init.Payload) // update the params with init payload by ws conn
	//log.Println("updated params: ", params)

	var slave Slave
	slave, err = server.factory.NewWithCommand(command, params)
	if err != nil {
		return errors.Wrapf(err, "failed to create backend")
	}
	defer slave.Close()

	// Let a fork link opened from another session find this process.
	defer server.announce(r).jidOpen(slave.WindowTitleVariables()["pid"])()

	titleVars := server.titleVariables(
		[]string{"server", "master", "slave"},
		map[string]map[string]interface{}{
			"server": server.serverTitleVariables(command),
			"master": map[string]interface{}{
				"remote_addr": conn.RemoteAddr(),
			},
			"slave": slave.WindowTitleVariables(),
		},
	)

	titleBuf := new(bytes.Buffer)
	err = server.titleTemplate.Execute(titleBuf, titleVars)
	if err != nil {
		return errors.Wrapf(err, "failed to fill window title template")
	}
	log.Println("template executed successfully: ", titleVars)
	opts := []webtty.Option{
		webtty.WithWindowTitle(titleBuf.Bytes()),
	}
	if server.options.PermitWrite {
		opts = append(opts, webtty.WithPermitWrite())
	}
	if server.options.EnableReconnect {
		opts = append(opts, webtty.WithReconnect(server.options.ReconnectTime))
	}
	if server.options.Width > 0 {
		opts = append(opts, webtty.WithFixedColumns(server.options.Width))
	}
	if server.options.Height > 0 {
		opts = append(opts, webtty.WithFixedRows(server.options.Height))
	}
	if server.options.Preferences != nil {
		opts = append(opts, webtty.WithMasterPreferences(server.options.Preferences))
	}

	tty, err := webtty.New(&wsWrapper{conn}, slave, opts...)
	if err != nil {
		return errors.Wrapf(err, "failed to create webtty")
	}

	homedir := req_payload[utils.HOME_DIR_KEY]
	root := homedir
	deferwatch := utils.Iscompiled(params)
	if deferwatch {
		// watch only parent/filename being run, will betaken care by browser
		filename := utils.GetIdeFileName(params)
		if filename != "" {
			root = filename
		}
	}
	// defer any browser notifications till compilation and execution is done, (performance)
	fb, err := filebrowser.New(root, tty, true, deferwatch)
	if err != nil {
		log.Println("failed to create filebrowser: ", err)
		//error in filebrowser, let the user delete unwanted files and reconnect again
		WriteMessageToTerminal(conn, err.Error())
		return err
	} else {
		fb.StartWatching()
	}
	defer fb.Close()

	log.Println("running webtty: ")
	err = tty.Run(ctx)
	if err == context.Canceled && r.Context().Err() != nil {
		return errSessionEnded
	}
	if err == webtty.ErrSlaveClosed {
		// tell the browser when the program was killed (usually the memory limit)
		if er, ok := slave.(interface{ ExitReason() string }); ok && er.ExitReason() == "killed" {
			return errSlaveKilled
		}
	}

	return err
}

// errSlaveKilled means the command ended because it was killed, which is how
// the container's memory limit stops a program.
var errSlaveKilled = errors.New("slave killed")

// errSessionEnded means an admin ended the session while this terminal ran.
var errSessionEnded = errors.New("session ended")

func (server *Server) errorHandler(w http.ResponseWriter, r *http.Request, status int) {
	w.WriteHeader(status)
	if status == http.StatusNotFound {
		indexVars := map[string]interface{}{
			"title": "404 Page Not Found",
			"body":  template.HTML("<h1>404 Page Not Found</h1>"),
		}
		indexTemplate, err := template.New("index").Parse(CommonTemplate)
		if err != nil {
			log.Println("index template parse failed") // must be valid
			w.Write([]byte("404 Page Not Found"))
			return
		}
		indexBuf := new(bytes.Buffer)
		err = indexTemplate.Execute(indexBuf, indexVars)
		if err != nil {
			http.Error(w, "Internal Server Error", 500)
			return
		}

		w.Write(indexBuf.Bytes())
	}
}

func (server *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if _, isLangPage := langPageFor(r.URL.Path); r.URL.Path != "/" && r.URL.Path != "/practice" && !isLangPage {
		server.errorHandler(w, r, http.StatusNotFound)
		return
	}
	uid := cookie.Get_Uid(r)
	var homedir string
	if ownsWorkspace(r) {
		// we need this here as first API to be hit to generate homedir and save it to cookie
		_, homedir = server.requestIdentity(w, r)
	} else {
		// The session runs on a worker, which creates the workspace itself.
		// Still refresh the guest session cookie as the call above would.
		cookie.UpdateGuestSessionCookieAge(w, r, utils.DEADLINE_MINUTES*60)
	}
	defer func() {
		if uid == "" && homedir != "" {
			// reset the job to delete the guests working dir after a certain deadline
			jobname := utils.REMOVE_JOB_KEY + homedir
			utils.GottyJobs.ResetJob(jobname, utils.DEADLINE_MINUTES*time.Minute, func() {
				utils.RemoveDir(homedir)
			})
		}
	}()

	titleVars := server.titleVariables(
		[]string{"server", "master"},
		map[string]map[string]interface{}{
			"server": server.options.TitleVariables,
			"master": map[string]interface{}{
				"remote_addr": r.RemoteAddr,
			},
		},
	)

	titleBuf := new(bytes.Buffer)
	err := server.titleTemplate.Execute(titleBuf, titleVars)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		log.Println("Error while executing title template: ", err.Error())
		return
	}

	indexVars := map[string]interface{}{
		"title": titleBuf.String(),
		"Page":  indexPageFor(r), // title, meta tags and hero for "/" or a language page (T12)
	}

	indexBuf := new(bytes.Buffer)
	err = server.indexTemplate.Execute(indexBuf, indexVars)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		log.Println("Error while executing Index template: ", err.Error())
		return
	}

	w.Write(indexBuf.Bytes())
}

func (server *Server) handleAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	// @TODO hashing?
	w.Write([]byte("var gotty_auth_token = '" + server.options.Credential + "';"))
}

func (server *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Write([]byte("var gotty_term = '" + server.options.Term + "';"))

	w.Write(firebaseConfigJS())

	_, secret := cookie.GetOpenApiAccessToken(r)
	// check if already a secret present from prev active session.
	if string(secret) == "" {
		secret = encoder.GenerateLargePrime().Bytes()
	}
	access_token, err := encoder.Encrypt([]byte(openAIToken()), secret)
	if err == nil {
		err = cookie.SetOpenApiAccessToken(w, r, access_token, secret)
		if err != nil {
			log.Println("Error: saving access_token: ", err)
		}
		w.Write([]byte("var openai_access_token = '" + string(access_token) + "';"))
		// the page offers the OpenRouter models only when the server can call them
		w.Write([]byte("\nvar openrouter_enabled = " + strconv.FormatBool(utils.OpenRouterKey() != "") + ";"))
	} else {
		log.Println("Error: encrypting access_token: ", err)
	}
}

// builtinFirebaseConfig is the production Firebase project's web config, used
// unless OPENREPL_FIREBASE_CONFIG names another one.
const builtinFirebaseConfig = "CmNvbnN0IGZpcmViYXNlY29uZmlnID0gewogIGFwaUtleTogIkFJemFTeUFTZ0FhUnY2eVhV" +
	"SlFWY0hhQV9sUkZWTXk5QVlaZVJscyIsCiAgYXV0aERvbWFpbjogIm9wZW5yZXBsLWFwcC5m" +
	"aXJlYmFzZWFwcC5jb20iLAogIHByb2plY3RJZDogIm9wZW5yZXBsLWFwcCIsCiAgZGF0YWJh" +
	"c2VVUkw6ICJodHRwczovL29wZW5yZXBsLWFwcC1kZWZhdWx0LXJ0ZGIuZmlyZWJhc2Vpby5j" +
	"b20iCn07Cgo="

// firebaseConfigJS is the part of config.js that defines `firebaseconfig`.
func firebaseConfigJS() []byte {
	if js, ok := utils.FirebaseConfigJS(); ok {
		return js
	}
	decoded, err := base64.StdEncoding.DecodeString(builtinFirebaseConfig)
	if err != nil {
		log.Println("Error: decoding the built-in firebase config: ", err)
		return nil
	}
	return decoded
}

// serverTitleVariables returns the title variables for one connection. The
// command is per connection, so it is set on a copy rather than on the shared
// options map.
func (server *Server) serverTitleVariables(command string) map[string]interface{} {
	vars := make(map[string]interface{}, len(server.options.TitleVariables)+1)
	for k, v := range server.options.TitleVariables {
		vars[k] = v
	}
	if command != "" {
		vars["command"] = command
	}
	return vars
}

// titleVariables merges maps in a specified order.
// varUnits are name-keyed maps, whose names will be iterated using order.
func (server *Server) titleVariables(order []string, varUnits map[string]map[string]interface{}) map[string]interface{} {
	titleVars := map[string]interface{}{}

	for _, name := range order {
		vars, ok := varUnits[name]
		if !ok {
			panic("title variable name error")
		}
		for key, val := range vars {
			titleVars[key] = val
		}
	}

	// safe net for conflicted keys
	for _, name := range order {
		titleVars[name] = varUnits[name]
	}

	return titleVars
}

func (server *Server) handleFileBrowser(rw http.ResponseWriter, req *http.Request) {
	bodybuf, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Println("error reading body : ", err)
	}
	log.Println("body: ", string(bodybuf))
	req.ParseForm()
	log.Println("method: ", req.Method, " Form: ", req.Form, " body: ", req.Body)
	uid, homedir := server.requestIdentity(rw, req)
	if err := server.waitWorkspace(req, homedir); err != nil {
		http.Error(rw, "workspace is synchronizing, please try again", http.StatusServiceUnavailable)
		return
	}
	//command := req.Form.Get("command")
	defer func() {
		if uid == "" {
			// reset the job to delete the guests working dir after a certain deadline
			jobname := utils.REMOVE_JOB_KEY + homedir
			utils.GottyJobs.ResetJob(jobname, utils.DEADLINE_MINUTES*time.Minute, func() {
				utils.RemoveDir(homedir)
			})
		}
	}()
	if req.Method == http.MethodPost {
		defer func() {
			if homedir != "" {
				if err := workspace.ReconcileOwnership(utils.HOME_DIR, homedir); err != nil {
					log.Printf("workspace ownership reconciliation failed for %s: %v", homedir, err)
				}
			}
		}()
	}

	query := req.Form.Get("q")
	path := req.Form.Get("filepath")
	if path == "" {
		path = homedir
	}
	relPath, pathErr := workspace.RelativePath(homedir, path)
	if pathErr != nil {
		http.Error(rw, "path is outside the workspace", http.StatusNotFound)
		return
	}
	home, homeErr := wsync.OpenHome(utils.HOME_DIR, filepath.Base(homedir), false)
	if homeErr != nil {
		http.Error(rw, "workspace is unavailable", http.StatusInternalServerError)
		return
	}
	defer home.Close()
	if _, err := home.Lstat(relPath); err != nil {
		http.Error(rw, "path is not available inside the workspace", http.StatusNotFound)
		return
	}
	usageBytes, err := workspaceUsage(home, relPath)
	if err != nil {
		http.Error(rw, "workspace path is unavailable", http.StatusNotFound)
		return
	}

	if req.Method == "GET" {
		if query == "load" {
			entry, err := home.Lstat(relPath)
			if err != nil || entry.Type != wsync.File {
				log.Println("Not a regular file: ", path, err)
				http.Error(rw, path+" is not a Valid file", http.StatusBadRequest)
				return
			}
			file, err := home.OpenRead(relPath)
			if err != nil {
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
			defer file.Close()
			content, err := ioutil.ReadAll(io.LimitReader(file, entry.Size+1))
			if err != nil {
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}

			// encode the content in base64
			encoded := base64.StdEncoding.EncodeToString(content)

			// set the response header and write the encoded content
			rw.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(rw, encoded)
		} else if query == "zip" {
			filename := filepath.Base(path)
			// Set the content type and attachment header
			rw.Header().Set("Content-Type", "application/zip")
			rw.Header().Set("Content-Disposition", "attachment; filename="+filename+".zip")

			// Create a new zip archive
			zipWriter := zip.NewWriter(rw)
			defer zipWriter.Close()
			if err := writeWorkspaceZip(home, relPath, zipWriter); err != nil {
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
		} else if query == "usage" {
			// workspace size for the Files panel (T7)
			used := float64(usageBytes) / (1024 * 1024)
			rw.Header().Set("Content-Type", "application/json")
			rw.Header().Set("Cache-Control", "no-store")
			rw.Write(utils.JsonMarshal(map[string]interface{}{
				"usedMB":             used,
				"limitMB":            filebrowser.MAXDISKUSAGE_MB,
				"guest":              uid == "",
				"deleteAfterMinutes": utils.DEADLINE_MINUTES,
			}))
		} else {
			tree, err := workspaceTree(home, homedir, relPath)
			if err != nil {
				log.Printf("file browser tree failed for %q: %v", path, err)
				http.Error(rw, "could not read this workspace path", http.StatusInternalServerError)
				return
			}
			log.Println("file Tree: ", tree)
			rw.Header().Set("Content-Type", "application/json")
			rw.Write(utils.JsonMarshal(tree))
		}
	} else if req.Method == "POST" {
		if query == "save" {
			decoded, err := base64.StdEncoding.DecodeString(string(bodybuf))
			if err != nil {
				http.Error(rw, err.Error(), http.StatusBadRequest)
				return
			}
			if usageBytes+int64(len(decoded)) > int64(filebrowser.MAXDISKUSAGE_MB)*1024*1024 {
				http.Error(rw, "workspace disk quota exceeded", http.StatusInsufficientStorage)
				return
			}

			// Write the decoded data to a file
			err = writeWorkspaceFile(home, relPath, decoded)
			if err != nil {
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
			// Return a success response
			rw.WriteHeader(http.StatusOK)
		} else {
			var event filebrowser.Event
			err := json.Unmarshal(bodybuf, &event)
			if err != nil {
				log.Println("failed unmarhalling body : ", err)
				http.Error(rw, err.Error(), http.StatusBadRequest)
				return
			}
			if _, err := workspace.RelativePath(homedir, event.Name); err != nil {
				http.Error(rw, "event path is outside the workspace", http.StatusNotFound)
				return
			}
			if event.NewName != "" {
				if _, err := workspace.RelativePath(homedir, event.NewName); err != nil {
					http.Error(rw, "event destination is outside the workspace", http.StatusNotFound)
					return
				}
			}
			if event.Op&fsnotify.Create == fsnotify.Create {
				needed := int64(0)
				if event.NewName != "" {
					sourceRel, _ := workspace.RelativePath(homedir, event.Name)
					needed, err = workspaceUsage(home, sourceRel)
					if err != nil {
						http.Error(rw, "copy source is unavailable", http.StatusNotFound)
						return
					}
				}
				if usageBytes+needed > int64(filebrowser.MAXDISKUSAGE_MB)*1024*1024 {
					http.Error(rw, "workspace disk quota exceeded", http.StatusInsufficientStorage)
					return
				}
			}
			processWorkspaceEvent(rw, home, homedir, event)
		}
	}
}

func (server *Server) handleFileUpload(w http.ResponseWriter, req *http.Request) {
	// Parse the form data and get the file and its properties
	req.ParseMultipartForm(5 << 20) // Limit the amount of memory used to parse the form data
	log.Println("method: ", req.Method, " Form: ", req.Form, " body: ", req.Body)
	_, homedir := server.requestIdentity(w, req)
	if err := server.waitWorkspace(req, homedir); err != nil {
		http.Error(w, "workspace is synchronizing, please try again", http.StatusServiceUnavailable)
		return
	}
	defer func() {
		if err := workspace.ReconcileOwnership(utils.HOME_DIR, homedir); err != nil {
			log.Printf("workspace ownership reconciliation failed for %s: %v", homedir, err)
		}
	}()

	home, err := wsync.OpenHome(utils.HOME_DIR, filepath.Base(homedir), false)
	if err != nil {
		http.Error(w, "workspace is unavailable", http.StatusInternalServerError)
		return
	}
	defer home.Close()
	usageBytes, err := workspaceUsage(home, "")
	if err != nil {
		http.Error(w, "workspace is unavailable", http.StatusInternalServerError)
		return
	}
	if usageBytes >= int64(filebrowser.MAXDISKUSAGE_MB)*1024*1024 {
		errstr := fmt.Sprintf("Max Disk Usage Limit of %dMB Reached for : %s Please delete unwanted files and try again.\n", filebrowser.MAXDISKUSAGE_MB, homedir)
		http.Error(w, errstr, http.StatusInsufficientStorage)
		return
	}

	file, handler, err := req.FormFile("file")
	if err != nil {
		log.Println("Failed to retrieve file from request: ", err)
		http.Error(w, "Failed to retrieve file from request", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := handler.Filename
	if strings.ContainsAny(filename, "/\\\x00") || wsync.ValidRel(filename) != nil {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	// Get the checksum of the file from the form data
	checksum := req.FormValue("checksum")

	// Create a buffer to store the file contents
	buf := bytes.NewBuffer(nil)
	if _, err := io.Copy(buf, file); err != nil {
		log.Println("Failed to read file from request: ", err)
		http.Error(w, "Failed to read file from request", http.StatusInternalServerError)
		return
	}

	// Remove the BOM from the file contents, if it has
	if bytes.HasPrefix(buf.Bytes(), []byte("\xef\xbb\xbf")) {
		buf.Next(3)
	}

	//log.Println("buffer: ", buf.String())
	localchecksum := fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
	// Verify the checksum of the file
	if localchecksum != checksum {
		log.Println("Checksum verification failed: still proceeding..", localchecksum, checksum)
		//http.Error(w, "Checksum verification failed", http.StatusBadRequest)
		//return
	}
	if usageBytes+int64(buf.Len()) > int64(filebrowser.MAXDISKUSAGE_MB)*1024*1024 {
		http.Error(w, "workspace disk quota exceeded", http.StatusInsufficientStorage)
		return
	}

	err = writeWorkspaceFile(home, filename, buf.Bytes())
	if err != nil {
		log.Println("Failed to save file on server: ", err)
		http.Error(w, "Failed to save file on server", http.StatusInternalServerError)
		return
	}

	// Return a success response
	w.Write([]byte("File uploaded successfully"))
}
