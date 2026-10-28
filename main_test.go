package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Shagrat2/go-autoconfig/config"
	"github.com/Shagrat2/go-autoconfig/handlers"
)

func TestPprofServerServesProfiles(t *testing.T) {
	srv := newPprofServer("127.0.0.1:0")

	for _, path := range []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/goroutine?debug=1",
		"/debug/pprof/heap?debug=1",
	} {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want %d", path, rec.Code, http.StatusOK)
		}
	}
}

func TestPublicRouterDoesNotExposePprof(t *testing.T) {
	cfg := &config.Config{
		Domains: map[string]config.Domain{
			"example.com": {
				IMAP: config.Server{Host: "imap.example.com", Port: 993},
				SMTP: config.Server{Host: "smtp.example.com", Port: 465},
			},
		},
	}

	r := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	r.Host = "example.com"
	rec := httptest.NewRecorder()
	handlers.NewRouter(cfg).ServeHTTP(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRequestIDValidation(t *testing.T) {
	for _, id := range []string{"abc", "abc-123.X_y", strings.Repeat("a", 64)} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Request-ID", id)

		if got := requestID(r); got != id {
			t.Errorf("requestID(%q) = %q, want %q", id, got, id)
		}
	}

	for _, id := range []string{"bad id", "bad\nid", strings.Repeat("a", 65)} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Request-ID", id)

		got := requestID(r)
		if got == "" || got == id || !requestIDPattern.MatchString(got) {
			t.Errorf("requestID(%q) = %q, want generated id", id, got)
		}
	}
}

func TestLoggingPropagatesRequestID(t *testing.T) {
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	var got string
	h := logging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = handlers.RequestID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	r := httptest.NewRequest(http.MethodGet, "/mail/config-v1.1.xml", nil)
	r.Header.Set("X-Request-ID", "abc-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if got != "abc-123" {
		t.Errorf("context request id = %q, want %q", got, "abc-123")
	}
	if header := rec.Header().Get("X-Request-ID"); header != "abc-123" {
		t.Errorf("response X-Request-ID = %q, want %q", header, "abc-123")
	}
}
