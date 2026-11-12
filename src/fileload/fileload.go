// Package fileload answers the file browser's "load": a file's bytes as base64, for the editor.
//
// Three ways, chosen by the query:
//   - plain: the whole file;
//   - max=<bytes>: the whole file, unless it is bigger than that: then nothing is read, the answer is 413 and the
//     header X-File-Size says how big it is. The editor is a shared document with a hard limit on what it can take,
//     and a file over that is not worth reading, encoding and sending only to be thrown away;
//   - length=<bytes> (and offset=<bytes>): a piece of the file, for the read-only viewer of big files. The piece is
//     at most MaxChunk bytes; X-File-Size, X-Chunk-Offset and X-Chunk-Length say where it is.
//
// The body is base64 text (the original form), or with enc=raw the bytes themselves (application/octet-stream) which
// are gzip-compressed when the browser accepts it and there is enough to gain: base64 makes a file a third bigger, and
// text compresses to a fraction of its size. The browser undoes the gzip by itself.
package fileload

import (
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// MaxChunk is the most one request reads with length=.
const MaxChunk = 4 << 20

// Bodies shorter than this are not compressed: the saving is nothing and the header costs more.
const minGzip = 1024

// Every answer carries an ETag made of the file's modification time and size, and "Cache-Control: no-cache", which
// lets a browser keep the answer but makes it ask the server before using it again. A file that has not changed is
// then answered with an empty 304 and nothing is read or sent; the browser's cache fills in the body, so the page
// needs no code for it. A file that was written since has another tag and is sent in full. The tag is weak (W/): it
// says "the same file", not "the same bytes on the wire".
func etagOf(info os.FileInfo) string {
	return fmt.Sprintf(`W/"%x-%x"`, info.ModTime().UnixNano(), info.Size())
}

func notModified(req *http.Request, etag string) bool {
	have := strings.TrimPrefix(etag, "W/")
	for _, tag := range strings.Split(req.Header.Get("If-None-Match"), ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == have {
			return true
		}
	}
	return false
}

// Serve writes the answer for the file at path (the caller has checked that the path is allowed). req.Form must be
// parsed.
func Serve(rw http.ResponseWriter, req *http.Request, path string) {
	info, err := os.Stat(path)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(rw, path+" is not a Valid file", http.StatusBadRequest)
		return
	}
	size := info.Size()
	rw.Header().Set("X-File-Size", strconv.FormatInt(size, 10))
	etag := etagOf(info)

	if l := req.Form.Get("length"); l != "" {
		length, err := strconv.ParseInt(l, 10, 64)
		if err != nil || length < 1 {
			http.Error(rw, "bad length", http.StatusBadRequest)
			return
		}
		if length > MaxChunk {
			length = MaxChunk
		}
		offset := int64(0)
		if o := req.Form.Get("offset"); o != "" {
			if offset, err = strconv.ParseInt(o, 10, 64); err != nil || offset < 0 {
				http.Error(rw, "bad offset", http.StatusBadRequest)
				return
			}
		}
		if offset > size {
			offset = size
		}
		if offset+length > size {
			length = size - offset
		}
		rw.Header().Set("ETag", etag)
		rw.Header().Set("Cache-Control", "private, no-cache")
		rw.Header().Set("X-Chunk-Offset", strconv.FormatInt(offset, 10))
		rw.Header().Set("X-Chunk-Length", strconv.FormatInt(length, 10))
		if notModified(req, etag) {
			rw.WriteHeader(http.StatusNotModified)
			return
		}
		buf := make([]byte, length)
		if length > 0 {
			f, err := os.Open(path)
			if err != nil {
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
			defer f.Close()
			n, err := f.ReadAt(buf, offset)
			if err != nil && err != io.EOF {
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
			buf = buf[:n]
		}
		rw.Header().Set("X-Chunk-Length", strconv.Itoa(len(buf)))
		write(rw, req, buf)
		return
	}

	if m := req.Form.Get("max"); m != "" {
		if max, err := strconv.ParseInt(m, 10, 64); err == nil && max > 0 && size > max {
			http.Error(rw, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
	}
	rw.Header().Set("ETag", etag)
	rw.Header().Set("Cache-Control", "private, no-cache")
	if notModified(req, etag) {
		rw.WriteHeader(http.StatusNotModified)
		return
	}
	content, err := os.ReadFile(path)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	write(rw, req, content)
}

func write(rw http.ResponseWriter, req *http.Request, b []byte) {
	if req.Form.Get("enc") != "raw" {
		rw.Header().Set("Content-Type", "text/plain")
		rw.Write([]byte(base64.StdEncoding.EncodeToString(b)))
		return
	}
	rw.Header().Set("Content-Type", "application/octet-stream")
	rw.Header().Add("Vary", "Accept-Encoding")
	if len(b) >= minGzip && acceptsGzip(req) {
		rw.Header().Set("Content-Encoding", "gzip")
		zw, _ := gzip.NewWriterLevel(rw, gzip.BestSpeed)
		zw.Write(b)
		zw.Close()
		return
	}
	rw.Write(b)
}

func acceptsGzip(req *http.Request) bool {
	for _, part := range strings.Split(req.Header.Get("Accept-Encoding"), ",") {
		name := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.EqualFold(name, "gzip") && !strings.Contains(part, "q=0") {
			return true
		}
	}
	return false
}
