package web

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipMin is the smallest body worth compressing.
const gzipMin = 1024

// zipper gzips responses with at most as many compressors at once as it has
// slots; a response that finds none free goes as it is. Pages carry their
// CSRF token masked, so compressing them gives it away to no one.
type zipper struct {
	slots   chan struct{}
	writers sync.Pool
}

func newZipper(slots int) *zipper {
	z := &zipper{slots: make(chan struct{}, slots)}
	z.writers.New = func() any {
		w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return w
	}
	return z
}

func (z *zipper) zip(body []byte) ([]byte, bool) {
	select {
	case z.slots <- struct{}{}:
	default:
		return nil, false
	}
	defer func() { <-z.slots }()
	var buf bytes.Buffer
	buf.Grow(len(body) / 3)
	w, _ := z.writers.Get().(*gzip.Writer)
	defer z.writers.Put(w)
	w.Reset(&buf)
	if _, err := w.Write(body); err != nil {
		return nil, false
	}
	if err := w.Close(); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// acceptsGzip reports whether r takes a gzipped response.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		coding, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			if k, v, _ := strings.Cut(strings.TrimSpace(p), "="); strings.EqualFold(k, "q") {
				q, err := strconv.ParseFloat(v, 64)
				return err == nil && q > 0
			}
		}
		return true
	}
	return false
}

// write sends an HTML body, gzipped when the browser takes it and it is
// worth it.
func (h *Handler) write(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Add("Vary", "Accept-Encoding")
	if len(body) >= gzipMin && acceptsGzip(r) {
		if zipped, ok := h.zipper.zip(body); ok {
			hd.Set("Content-Encoding", "gzip")
			body = zipped
		}
	}
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// gzipAll compresses the files of fsys that shrink, by name.
func gzipAll(fsys fs.FS) (map[string][]byte, error) {
	out := make(map[string][]byte)
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if _, err := w.Write(raw); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		if buf.Len() < len(raw) {
			out[name] = buf.Bytes()
		}
		return nil
	})
	return out, err
}
