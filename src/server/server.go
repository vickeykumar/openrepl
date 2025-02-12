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

	handlers := server.setupHandlers(cctx, cancel, path, counter)
	srv, err := server.setupHTTPServer(handlers)
	if err != nil {
		return errors.Wrapf(err, "failed to setup an HTTP server")
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
		return errors.Wrapf(err, "failed to listen at `%s`", hostPort)
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

func (server *Server) setupHandlers(ctx context.Context, cancel context.CancelFunc, pathPrefix string, counter *counter) http.Handler {
	staticFileHandler := http.FileServer(
		&assetfs.AssetFS{Asset: Asset, AssetDir: AssetDir, Prefix: "static"},
	)

	var siteMux = http.NewServeMux()
	siteMux.HandleFunc(pathPrefix, server.handleIndex)
	siteMux.HandleFunc(pathPrefix+"practice/dsa-questions", handlePracticeQuestions)
	siteMux.HandleFunc(pathPrefix+"practice", server.handleIndex)
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
	siteMux.Handle(pathPrefix+"robots.txt", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"jsconsole.html", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"css/", http.StripPrefix(pathPrefix, staticFileHandler))

	siteMux.HandleFunc(pathPrefix+"auth_token.js", server.handleAuthToken)
	siteMux.HandleFunc(pathPrefix+"config.js", server.handleConfig)

	siteHandler := http.Handler(siteMux)

	if server.options.EnableBasicAuth {
		log.Printf("Using Basic Authentication")
		siteHandler = server.wrapBasicAuth(siteHandler, server.options.Credential)
	}

	withGz := gziphandler.GzipHandler(server.wrapHeaders(siteHandler))
	siteHandler = server.wrapLogger(withGz)

	wsMux := http.NewServeMux()
	wsMux.Handle("/", siteHandler)
	wsMux.HandleFunc(pathPrefix+"ws", server.generateHandleWS(ctx, cancel, counter))
	wsMux.HandleFunc(pathPrefix+"ws_c", server.generateHandleWS(ctx, cancel, counter, "cling"))
	wsMux.HandleFunc(pathPrefix+"ws_cpp", server.generateHandleWS(ctx, cancel, counter, "cling"))
	wsMux.HandleFunc(pathPrefix+"ws_go", server.generateHandleWS(ctx, cancel, counter, "gointerpreter"))

	// Expose all other APIs form Commands2DemoMap, refer utils.go
	if utils.Commands2DemoMap == nil {
		InitCommands2DemoMap()
	}
	for command, _ := range utils.Commands2DemoMap {
		log.Printf("Exposing API for %d\n", command)
		wsMux.HandleFunc(pathPrefix+"ws_"+command, server.generateHandleWS(ctx, cancel, counter, command))
	}

	siteHandler = http.Handler(wsMux)

	return siteHandler
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

func (server *Server) SetNewCommand(command string) {
	server.factory.SetNewCommand(command)
	server.options.TitleVariables["command"] = command
	log.Println("New Command set successfully: " + command)
}
