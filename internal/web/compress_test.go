package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/core/secure"
	"github.com/ssenerg/gochat/internal/web/render"
	"github.com/ssenerg/gochat/internal/web/views"
)

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                          false,
		"gzip":                      true,
		"gzip, deflate, br":         true,
		"br;q=1.0, GZIP;q=0.5":      true,
		"gzip;q=0":                  false,
		"gzip;q=0.000":              false,
		"identity, deflate":         false,
		"x-gzip":                    false,
		"deflate;q=1, gzip ; q=0.1": true,
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", header)
		if got := acceptsGzip(r); got != want {
			t.Errorf("%q: got %v", header, got)
		}
	}
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWriteGzipsWhatIsWorthIt(t *testing.T) {
	h := &Handler{zipper: newZipper(1)}
	big := []byte(strings.Repeat("<li>a channel</li>", 200))
	write := func(body []byte, encoding string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", encoding)
		w := httptest.NewRecorder()
		h.write(w, r, http.StatusOK, body)
		return w
	}
	w := write(big, "gzip")
	if w.Header().Get("Content-Encoding") != "gzip" || w.Body.Len() >= len(big)/4 || !bytes.Equal(gunzip(t, w.Body.Bytes()), big) {
		t.Fatalf("gzipped %d bytes to %d", len(big), w.Body.Len())
	}
	if !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Fatal("no Vary: Accept-Encoding")
	}
	if w := write([]byte("<p>small</p>"), "gzip"); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("gzipped a small body")
	}
	if w := write(big, ""); w.Header().Get("Content-Encoding") != "" || !bytes.Equal(w.Body.Bytes(), big) {
		t.Fatal("gzipped for a browser that does not take it")
	}
	// With every compressor busy the body goes as it is.
	h.zipper.slots <- struct{}{}
	if w := write(big, "gzip"); w.Header().Get("Content-Encoding") != "" || !bytes.Equal(w.Body.Bytes(), big) {
		t.Fatal("gzipped past the bound")
	}
}

func TestStaticFilesGzipped(t *testing.T) {
	r, err := render.New(16)
	if err != nil {
		t.Fatal(err)
	}
	static, _ := fs.Sub(views.Files, "static")
	zipped, err := gzipAll(static)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{render: r, zipped: zipped}
	srv := h.staticFiles(http.StripPrefix(r.StaticPrefix, http.FileServerFS(static)))
	raw, _ := fs.ReadFile(static, "htmx.min.js")
	for _, encoding := range []string{"gzip", ""} {
		req := httptest.NewRequest(http.MethodGet, r.StaticPrefix+"htmx.min.js", nil)
		req.Header.Set("Accept-Encoding", encoding)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		body := w.Body.Bytes()
		if encoding != "" {
			if w.Header().Get("Content-Encoding") != "gzip" || len(body) >= len(raw)/2 {
				t.Fatalf("htmx.min.js: %d bytes gzipped to %d", len(raw), len(body))
			}
			body = gunzip(t, body)
		}
		if w.Code != http.StatusOK || !bytes.Equal(body, raw) || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
			t.Fatalf("%q: %d %q", encoding, w.Code, w.Header().Get("Content-Type"))
		}
	}
}

func TestGuardTakesMaskedTokens(t *testing.T) {
	h := &Handler{logger: zap.NewNop(), origin: "http://chat.test"}
	token := secure.Token()
	guarded := h.csrf(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	post := func(header, field string) int {
		form := url.Values{}
		if field != "" {
			form.Set(csrfField, field)
		}
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", h.origin)
		r.Header.Set("HX-Request", "true")
		r.AddCookie(&http.Cookie{Name: h.cookieName("csrf"), Value: token})
		if header != "" {
			r.Header.Set(csrfHeader, header)
		}
		w := httptest.NewRecorder()
		guarded.ServeHTTP(w, r)
		return w.Code
	}
	for name, c := range map[string]struct {
		header, field string
		want          int
	}{
		"bare header":   {token, "", http.StatusNoContent},
		"masked header": {secure.MaskToken(token), "", http.StatusNoContent},
		"masked field":  {"", secure.MaskToken(token), http.StatusNoContent},
		"other masked":  {secure.MaskToken(secure.Token()), "", http.StatusForbidden},
		"none":          {"", "", http.StatusForbidden},
	} {
		if got := post(c.header, c.field); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	// Pages carry the token masked, differently each time.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: h.cookieName("csrf"), Value: token})
	a, b := h.csrfToken(httptest.NewRecorder(), r), h.csrfToken(httptest.NewRecorder(), r)
	if a == b || a == token {
		t.Fatal("the page token is not masked afresh")
	}
}
