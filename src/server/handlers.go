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
	"sync/atomic"
	"time"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"webtty"
	"filebrowser"
	"utils"
	"cookie"
	"io"
	"io/ioutil"
	"encoding/base64"
	"crypto/sha256"
	"os"
	"archive/zip"
	"path/filepath"
)


func updateparams(params *url.Values, payload map[string]string) {
	for key, value := range payload {
		params.Set(key,value)
	}
}

func fetchRequestedPayload(w http.ResponseWriter, r *http.Request) (req_payload map[string]string) {
	req_payload = make(map[string]string)
	uid := cookie.Get_Uid(r)
	homedir := cookie.GetOrUpdateHomeDir(w, r, uid)
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
			server.SetNewCommand(command)
		}
		if server.options.Once {
			success := atomic.CompareAndSwapInt64(once, 0, 1)
			if !success {
				http.Error(w, "Server is shutting down", http.StatusServiceUnavailable)
				return
			}
		}

		num := counter.add(1)
		wieght := containers.GetCommandWieght(command)
		totalWieght := counter.addWieght(int(wieght))
		closeReason := "unknown reason"
		closeCode := websocket.CloseNormalClosure

		defer func() {
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

		req_payload := fetchRequestedPayload(w, r)
		// any cookie needs to be saved before upgrading to websocket
		conn, err := server.upgrader.Upgrade(w, r, nil)
		if err != nil {
			closeReason = err.Error()
			log.Println("Can not upgrade connection: " + closeReason)
			return
		}
		
		defer func() {
			log.Println("close status: ", closeCode)
			conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode , closeReason), time.Now().Add(time.Second))
			//wait for 1 sec deadline to write the buffers
			time.Sleep(2*time.Second)
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
		err = server.processWSConn(ctx, conn, req_payload)

		switch err {
		case ctx.Err():
			closeReason = "cancelation"
		case webtty.ErrSlaveClosed:
			closeReason = server.factory.Name()
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
func (server *Server) processWSConn(ctx context.Context, conn *websocket.Conn, req_payload map[string]string) error {
	conn.SetWriteDeadline(time.Now().Add(utils.DEADLINE_MINUTES * time.Minute)) // only 15 min sessions for services are allowed
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
	if init.AuthToken != server.options.Credential {
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
	updateparams(&params, req_payload)	// update the params with reqest payload
	updateparams(&params, init.Payload)	// update the params with init payload by ws conn
	//log.Println("updated params: ", params)

	var slave Slave
	slave, err = server.factory.New(params)
	if err != nil {
		return errors.Wrapf(err, "failed to create backend")
	}
	defer slave.Close()

	titleVars := server.titleVariables(
		[]string{"server", "master", "slave"},
		map[string]map[string]interface{}{
			"server": server.options.TitleVariables,
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

	return err
}

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
	if r.URL.Path != "/" {
		server.errorHandler(w, r, http.StatusNotFound)
		return
	}
	uid := cookie.Get_Uid(r)
	// we need this here as first API to be hit to generate homedir and save it to cookie
	homedir := cookie.GetOrUpdateHomeDir(w, r, uid)
	defer func () {
                if uid == "" {
                                // reset the job to delete the guests working dir after a certain deadline 
                                jobname := utils.REMOVE_JOB_KEY+homedir
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
		log.Println("Error while executing title template: ",err.Error())
		return
	}

	indexVars := map[string]interface{}{
		"title": titleBuf.String(),
	}

	indexBuf := new(bytes.Buffer)
	err = server.indexTemplate.Execute(indexBuf, indexVars)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		log.Println("Error while executing Index template: ",err.Error())
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
	
	fbconfig := "CmNvbnN0IGZpcmViYXNlY29uZmlnID0gewogIGFwaUtleTogIkFJemFTeUFTZ0Fh" +
	"UnY2eVhVSlFWY0hhQV9sUkZWTXk5QVlaZVJscyIsCiAgYXV0aERvbWFpbjogIm9wZW5yZXBsLWFwc" +
	"C5maXJlYmFzZWFwcC5jb20iLAogIHByb2plY3RJZDogIm9wZW5yZXBsLWFwcCIsCiAgZGF0YWJhc2" +
	"VVUkw6ICJodHRwczovL29wZW5yZXBsLWFwcC1kZWZhdWx0LXJ0ZGIuZmlyZWJhc2Vpby5jb20iCn07Cgo="

    decoded, err := base64.StdEncoding.DecodeString(fbconfig)
    if err==nil {
    	w.Write(decoded)
    }
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
	uid := cookie.Get_Uid(req)
	homedir := cookie.GetOrUpdateHomeDir(rw, req, uid)
	//command := req.Form.Get("command")
	defer func () {
		if uid == "" {
				// reset the job to delete the guests working dir after a certain deadline 
				jobname := utils.REMOVE_JOB_KEY+homedir
				utils.GottyJobs.ResetJob(jobname, utils.DEADLINE_MINUTES*time.Minute, func() {
					utils.RemoveDir(homedir)
				})
		}
	}()

	query := req.Form.Get("q")
	path := req.Form.Get("filepath")
	if path!="" && !strings.HasPrefix(path, homedir) {	// something fishy, bad request, return
		http.Error(rw, path+" not Found", http.StatusNotFound)
		return
	}
	if path == "" {
		path = homedir
	}
	fb, err := filebrowser.New(path, nil, false, true)	// without watcher on path directories, deferwatch=true
	if err != nil {
		log.Println("failed to create filebrowser: ", err)
		// still proceed as user can take necessary actions like free up the space
	}

	if req.Method == "GET" {
		if query == "load" {
			if !utils.IsFile(path) {
				log.Println("Not a File: ", path)
				http.Error(rw, path+" is not a Valid file", http.StatusBadRequest)
				return
			}
			content, err := ioutil.ReadFile(path)
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
			parentDir := filepath.Dir(homedir)	// get parent of homedir
			filename := strings.TrimPrefix(path, parentDir+"/")
			// Set the content type and attachment header
			rw.Header().Set("Content-Type", "application/zip")
			rw.Header().Set("Content-Disposition", "attachment; filename="+filename+".zip")

			// Create a new zip archive
			zipWriter := zip.NewWriter(rw)
			defer zipWriter.Close()
			fb.Writezip(zipWriter)
		} else {
			tree, err := fb.GetJsonTree()
		    if err != nil {
		        log.Println("Error: ",err)
		    }
		    log.Println("file Tree: ", tree)
			rw.Write(utils.JsonMarshal(tree))
		}
	} else if req.Method == "POST" {
		if query == "save" {
			decoded, err := base64.StdEncoding.DecodeString(string(bodybuf))
			if err != nil {
				http.Error(rw, err.Error(), http.StatusBadRequest)
				return
			}

			// Write the decoded data to a file
			err = ioutil.WriteFile(path, decoded, os.ModePerm)
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
		    if !strings.HasPrefix(event.Name, homedir) {	// something fishy, bad request, return
		    	http.Error(rw, event.Name+" not Found", http.StatusNotFound)
		    	return
		    }
		    fb.ProcessEventRequests(rw, req, event)
		}
	}
}

func (server *Server) handleFileUpload(w http.ResponseWriter, req *http.Request) {
	// Parse the form data and get the file and its properties
	req.ParseMultipartForm(5 << 20) // Limit the amount of memory used to parse the form data
	log.Println("method: ", req.Method, " Form: ", req.Form, " body: ", req.Body)
	uid := cookie.Get_Uid(req)
	homedir := cookie.GetOrUpdateHomeDir(w, req, uid)

	fb, err := filebrowser.New(homedir, nil, false, true)	// without watcher on path directories, deferwatch=true
	if err != nil {
		log.Println("failed to create filebrowser: ", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
        return
	}

	// prevent create requests if dir quota is reached
    if fb.GetSize() > float64(filebrowser.MAXDISKUSAGE_MB) {
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

	// Save the file to disk
	f, err := os.OpenFile(homedir+"/"+filename, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		log.Println("Failed to create file on server: ", err)
		http.Error(w, "Failed to create file on server", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	if _, err := io.Copy(f, buf); err != nil {
		log.Println("Failed to save file on server: ", err)
		http.Error(w, "Failed to save file on server", http.StatusInternalServerError)
		return
	}

	// Return a success response
	w.Write([]byte("File uploaded successfully"))
}
