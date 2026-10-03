package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"html/template"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"regexp"
	"sync/atomic"
	noesctmpl "text/template"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"pkg/homedir"
	"pkg/randomstring"
	"webtty"
	"encoder"
	"utils"
)

const STATUS_SUCCESS = "SUCCESS"
const STATUS_FAILED = "FAILED"

// Server provides a webtty HTTP endpoint.
type Server struct {
	factory Factory
	options *Options

	upgrader      *websocket.Upgrader
	indexTemplate *template.Template
	titleTemplate *noesctmpl.Template

	// routes announces the jids and workspaces this node owns. It is nil in
	// standalone mode.
	routes *routeTracker
	// workerSync is the workspace-sync state of a worker.
	workerSync workerSyncState
	// terminals maps each WebSocket route (without the prefix) to the REPL
	// command it starts.
	terminals map[string]string
	// gatewayAdmin serves /admin/workers and /admin/sessions on a gateway.
	gatewayAdmin http.Handler
	// admin is what the admin dashboard needs (admin_core.go).
	admin adminState
	// workerCredential is the gateway's WebSocket auth token, received by a
	// worker when it registers.
	workerCredential atomic.Value
}

// credential is the token a WebSocket's init message must carry.
func (server *Server) credential() string {
	if v, ok := server.workerCredential.Load().(string); ok {
		return v
	}
	return server.options.Credential
}

func (server *Server) setCredential(token string) {
	server.workerCredential.Store(token)
}

// credentialFor is the token a WebSocket opened by r must carry. A request
// forwarded by the gateway carries the gateway's token. A visitor of a
// worker's own port gets the worker's own credential from /auth_token.js, so
// that is the one to expect.
func (server *Server) credentialFor(r *http.Request) string {
	if server.options.Mode == ModeWorker && !isTrusted(r) {
		return server.options.Credential
	}
	return server.credential()
}

// wrapSiteAuth applies basic auth to the site when a credential is set. A
// worker has already been authenticated by the gateway for the requests it
// forwards, so only visitors of the worker's own port are asked.
func (server *Server) wrapSiteAuth(handler http.Handler) http.Handler {
	if !server.options.EnableBasicAuth {
		return handler
	}
	log.Printf("Using Basic Authentication")
	asked := server.wrapBasicAuth(handler, server.options.Credential)
	if server.options.Mode != ModeWorker {
		return asked
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isTrusted(r) {
			handler.ServeHTTP(w, r)
			return
		}
		asked.ServeHTTP(w, r)
	})
}

// New creates a new instance of Server.
// Server will use the New() of the factory provided to handle each request.
func New(factory Factory, options *Options) (*Server, error) {
	indexData, err := Asset("static/index.html")
	if err != nil {
		panic("index not found") // must be in bindata
	}
	if options.IndexFile != "" {
		path := homedir.Expand(options.IndexFile)
		indexData, err = ioutil.ReadFile(path)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read custom index file at `%s`", path)
		}
	}
	indexTemplate, err := template.New("index").Parse(string(indexData))
	if err != nil {
		panic("index template parse failed") // must be valid
	}

	titleTemplate, err := noesctmpl.New("title").Funcs(noesctmpl.FuncMap{
	    "encodePID": encoder.EncodePID,
  	}).Parse(options.TitleFormat)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse window title format `%s`", options.TitleFormat)
	}

	var originChekcer func(r *http.Request) bool
	if options.WSOrigin != "" {
		matcher, err := regexp.Compile(options.WSOrigin)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to compile regular expression of Websocket Origin: %s", options.WSOrigin)
		}
		originChekcer = func(r *http.Request) bool {
			return matcher.MatchString(r.Header.Get("Origin"))
		}
	}

	return &Server{
		factory: factory,
		options: options,

		upgrader: &websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			Subprotocols:    webtty.Protocols,
			CheckOrigin:     originChekcer,
		},
		indexTemplate: indexTemplate,
		titleTemplate: titleTemplate,
	}, nil
}

// Run starts the main process of the Server.
// The cancelation of ctx will shutdown the server immediately with aborting
// existing connections. Use WithGracefullContext() to support gracefull shutdown.
func (server *Server) Run(ctx context.Context, options ...RunOption) error {
	cctx, cancel := context.WithCancel(ctx)
	opts := &RunOptions{gracefullCtx: context.Background()}
	for _, opt := range options {
		opt(opts)
	}

	counter := newCounter(time.Duration(server.options.Timeout) * time.Second)

	path := "/"
	if server.options.EnableRandomUrl {
		path = "/" + randomstring.Generate(server.options.RandomUrlLength) + "/"
	}

	handlers, err := server.setupHandlers(cctx, cancel, path, counter)
	if err != nil {
		cancel()
		return errors.Wrapf(err, "failed to setup the handlers")
	}
	if server.options.Mode == ModeWorker {
		defer cancel()
		// A worker is reached through the gateway. When the operator also
		// gave it a port, it serves that port too, so people on the same
		// network can open the worker directly.
		var local *http.Server
		if server.options.LocalListen {
			var localErr <-chan error
			local, localErr, err = server.serveLocal(handlers, path)
			if err != nil {
				return err
			}
			go func() {
				select {
				case err := <-localErr:
					if err != http.ErrServerClosed {
						log.Printf("The worker's own port stopped: %v", err)
						cancel()
					}
				case <-cctx.Done():
				}
			}()
		}
		return server.runWorker(cctx, handlers, counter, local)
	}
	srv, srvErr, err := server.serveLocal(handlers, path)
	if err != nil {
		return err
	}

	go func() {
		select {
		case <-opts.gracefullCtx.Done():
			srv.Shutdown(context.Background())
		case <-cctx.Done():
		}
	}()

	select {
	case err = <-srvErr:
		if err == http.ErrServerClosed { // by gracefull ctx
			err = nil
		} else {
			cancel()
		}
	case <-cctx.Done():
		srv.Close()
		err = cctx.Err()
	}

	conn := counter.count()
	if conn > 0 {
		log.Printf("Waiting for %d connections to be closed", conn)
	}
	counter.wait()

	return err
}

// serveLocal listens on --address and --port and serves handlers there in the
// background. The channel receives the error that ended the server.
func (server *Server) serveLocal(handlers http.Handler, path string) (*http.Server, <-chan error, error) {
	srv, err := server.setupHTTPServer(handlers)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to setup an HTTP server")
	}

	if server.options.PermitWrite {
		log.Printf("Permitting clients to write input to the PTY.")
	}
	if server.options.Once {
		log.Printf("Once option is provided, accepting only one client")
	}

	if server.options.Port == "0" {
		log.Printf("Port number configured to `0`, choosing a random port")
	}
	hostPort := net.JoinHostPort(server.options.Address, server.options.Port)
	listener, err := net.Listen("tcp", hostPort)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to listen at `%s`", hostPort)
	}

	scheme := "http"
	if server.options.EnableTLS {
		scheme = "https"
	}
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	log.Printf("HTTP server is listening at: %s", scheme+"://"+host+":"+port+path)
	if server.options.Address == "0.0.0.0" {
		for _, address := range listAddresses() {
			log.Printf("Alternative URL: %s", scheme+"://"+address+":"+port+path)
		}
	}

	srvErr := make(chan error, 1)
	go func() {
		var err error
		if server.options.EnableTLS {
			crtFile := homedir.Expand(server.options.TLSCrtFile)
			keyFile := homedir.Expand(server.options.TLSKeyFile)
			log.Printf("TLS crt file: " + crtFile)
			log.Printf("TLS key file: " + keyFile)

			err = srv.ServeTLS(listener, crtFile, keyFile)
		} else {
			err = srv.Serve(listener)
		}
		if err != nil {
			srvErr <- err
		}
	}()
	return srv, srvErr, nil
}

func (server *Server) setupHandlers(ctx context.Context, cancel context.CancelFunc, pathPrefix string, counter *counter) (http.Handler, error) {
	staticFileHandler := http.FileServer(
		&assetfs.AssetFS{Asset: Asset, AssetDir: AssetDir, Prefix: "static"},
	)

	var siteMux = http.NewServeMux()
	siteMux.HandleFunc(pathPrefix, server.handleIndex)
	siteMux.HandleFunc(pathPrefix+"practice/dsa-questions", handlePracticeQuestions)
	siteMux.HandleFunc(pathPrefix+"practice/progress", handlePracticeProgress)
	siteMux.HandleFunc(pathPrefix+"practice", server.handleIndex)
	siteMux.HandleFunc(pathPrefix+"sitemap.xml", handleSitemap)
	siteMux.HandleFunc(pathPrefix+"snippet", handleSnippet)
	siteMux.HandleFunc(pathPrefix+"s/", handleSnippetLink)
	siteMux.HandleFunc(pathPrefix+"feedback", handleFeedback)
	siteMux.HandleFunc(pathPrefix+"blog", handleBlog)
	siteMux.HandleFunc(pathPrefix+"demo", handleDemo)
	siteMux.HandleFunc(pathPrefix+"login", handleLoginSession)
	siteMux.HandleFunc(pathPrefix+"logout", handleLogoutSession)
	siteMux.HandleFunc(pathPrefix+"profile", handleUserProfile)
	siteMux.HandleFunc(pathPrefix+"chat/completions", handleChatProxy)
	siteMux.HandleFunc(pathPrefix+"ws_filebrowser", server.handleFileBrowser)
	siteMux.HandleFunc(pathPrefix+"upload_file", server.handleFileUpload)

	siteMux.Handle(pathPrefix+"js/", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"images/", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"media/", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"docs/", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"doc.html", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"editblog.html", server.wrapAdmin(http.StripPrefix(pathPrefix, staticFileHandler)))
	siteMux.Handle(pathPrefix+"about.html", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"references.html", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"privacy.html", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"terms.html", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"robots.txt", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"jsconsole.html", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"css/", http.StripPrefix(pathPrefix, staticFileHandler))

	siteMux.HandleFunc(pathPrefix+"auth_token.js", server.handleAuthToken)
	siteMux.HandleFunc(pathPrefix+"settings.js", handleSettingsJS)
	siteMux.HandleFunc(pathPrefix+"config.js", server.handleConfig)
	server.admin.counter = counter
	server.registerAdmin(siteMux, pathPrefix)
	GetSiteSettings() // apply the saved Genie rates before the first request

	siteHandler := http.Handler(siteMux)

	siteHandler = server.wrapSiteAuth(siteHandler)

	withGz := gziphandler.GzipHandler(server.wrapHeaders(siteHandler))
	siteHandler = server.wrapLogger(withGz)

	wsMux := http.NewServeMux()
	wsMux.Handle("/", siteHandler)
	wsMux.HandleFunc(pathPrefix+"ws", server.generateHandleWS(ctx, cancel, counter))
	wsMux.HandleFunc(pathPrefix+"ws_c", server.generateHandleWS(ctx, cancel, counter, "cling"))
	wsMux.HandleFunc(pathPrefix+"ws_cpp", server.generateHandleWS(ctx, cancel, counter, "cling"))
	wsMux.HandleFunc(pathPrefix+"ws_go", server.generateHandleWS(ctx, cancel, counter, "gointerpreter"))
	server.terminals = map[string]string{"ws": "", "ws_c": "cling", "ws_cpp": "cling", "ws_go": "gointerpreter"}

	// Expose all other APIs form Commands2DemoMap, refer utils.go
	if utils.Commands2DemoMap == nil {
		InitCommands2DemoMap()
	}
	for command, _ := range utils.Commands2DemoMap {
		log.Printf("Exposing API for %d\n", command)
		wsMux.HandleFunc(pathPrefix+"ws_"+command, server.generateHandleWS(ctx, cancel, counter, command))
		server.terminals["ws_"+command] = command
	}

	siteHandler = http.Handler(wsMux)

	if server.options.Mode == ModeGateway {
		gw, err := server.wrapGateway(ctx, siteHandler, pathPrefix, counter)
		if err != nil {
			return nil, err
		}
		return server.wrapControls(gw, pathPrefix), nil
	}
	if server.options.Mode == ModeWorker {
		// The gateway applies the switches before it forwards a terminal.
		return siteHandler, nil
	}

	return server.wrapControls(siteHandler, pathPrefix), nil
}

func (server *Server) setupHTTPServer(handler http.Handler) (*http.Server, error) {
	srv := &http.Server{
		Handler: handler,
	}

	if server.options.EnableTLSClientAuth {
		tlsConfig, err := server.tlsConfig()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to setup TLS configuration")
		}
		srv.TLSConfig = tlsConfig
	}

	return srv, nil
}

func (server *Server) tlsConfig() (*tls.Config, error) {
	caFile := homedir.Expand(server.options.TLSCACrtFile)
	caCert, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, errors.New("could not open CA crt file " + caFile)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, errors.New("could not parse CA crt file data in " + caFile)
	}
	tlsConfig := &tls.Config{
		ClientCAs:  caCertPool,
		ClientAuth: tls.RequireAndVerifyClientCert,
	}
	return tlsConfig, nil
}

