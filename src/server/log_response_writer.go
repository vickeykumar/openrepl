package server

import (
	"bufio"
	"net"
	"net/http"
)

type logResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *logResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the writer inside (to flush a streamed answer).
func (w *logResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *logResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, _ := w.ResponseWriter.(http.Hijacker)
	w.status = http.StatusSwitchingProtocols
	return hj.Hijack()
}
