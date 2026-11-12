// Package fileload answers the file browser's "load": a file's bytes as base64, for the editor.
//
// Three ways, chosen by the query:
//   - plain: the whole file;
//   - max=<bytes>: the whole file, unless it is bigger than that: then nothing is read, the answer is 413 and the
//     header X-File-Size says how big it is. The editor is a shared document with a hard limit on what it can take,
//     and a file over that is not worth reading, encoding and sending only to be thrown away;
//   - length=<bytes> (and offset=<bytes>): a piece of the file, for the read-only viewer of big files. The piece is
//     at most MaxChunk bytes; X-File-Size, X-Chunk-Offset and X-Chunk-Length say where it is.
package fileload

import (
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"strconv"
)

// MaxChunk is the most one request reads with length=.
const MaxChunk = 4 << 20

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
		rw.Header().Set("X-Chunk-Offset", strconv.FormatInt(offset, 10))
		rw.Header().Set("X-Chunk-Length", strconv.Itoa(len(buf)))
		write(rw, buf)
		return
	}

	if m := req.Form.Get("max"); m != "" {
		if max, err := strconv.ParseInt(m, 10, 64); err == nil && max > 0 && size > max {
			http.Error(rw, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	write(rw, content)
}

func write(rw http.ResponseWriter, b []byte) {
	rw.Header().Set("Content-Type", "text/plain")
	rw.Write([]byte(base64.StdEncoding.EncodeToString(b)))
}
