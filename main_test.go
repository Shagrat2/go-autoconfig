package main

import (
	"net/http"
	"net/http/httptest"
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
