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

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"webtty"
	"filebrowser"
	"utils"
	"cookie"
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
	cookie.GetOrUpdateHomeDir(w, r, cookie.Get_Uid(r))	// move this to file browser handler later

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
	req.ParseForm()
	log.Printf("method: ", req.Method, " Form: ", req.Form, " body: ", req.Body)
	if req.Method == "GET" {
		fb := filebrowser.New("/tmp/home", nil)
		tree, err := fb.GetJsonTree()
	    if err != nil {
	        log.Println("Error: ",err)
	    }
	    log.Println("file Tree: ", tree)
		rw.Write(utils.JsonMarshal(tree))
	}
}
