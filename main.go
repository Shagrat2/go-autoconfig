package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/Shagrat2/go-autoconfig/config"
	"github.com/Shagrat2/go-autoconfig/handlers"
)

var path = flag.String(
	"config",
	"",
	"enter path to config file",
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	conf, err := config.NewConfig(*path)
	if err != nil {
		return err
	}
	if listen := os.Getenv("AUTOCONFIG_LISTEN"); listen != "" {
		conf.Listen = listen
	}
	if pprofListen := os.Getenv("AUTOCONFIG_PPROF"); pprofListen != "" {
		if pprofListen == "1" {
			pprofListen = "127.0.0.1:6060"
		}
		conf.PprofListen = pprofListen
	}
	if err := conf.Validate(); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              conf.Listen,
		Handler:           logging(recoverPanics(handlers.NewRouter(conf))),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	servers := []*http.Server{srv}
	if conf.PprofListen != "" {
		pprofSrv := newPprofServer(conf.PprofListen)
		servers = append(servers, pprofSrv)

		go func() {
			slog.Info("pprof started", "listen", conf.PprofListen)
			if err := pprofSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("pprof server failed", "error", err)
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server started", "listen", conf.Listen)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, s := range servers {
		if err := s.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown %s: %w", s.Addr, err)
		}
	}

	return nil
}

func newPprofServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	return r.ResponseWriter.Write(b)
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := requestID(r)
		r = r.WithContext(handlers.WithRequestID(r.Context(), id))
		w.Header().Set("X-Request-ID", id)

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}

		slog.Info("request",
			"request_id", id,
			"method", r.Method,
			"host", r.Host,
			"path", r.URL.Path,
			"status", status,
			"duration", time.Since(start),
		)
	})
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "error", rec, "path", r.URL.Path)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); requestIDPattern.MatchString(id) {
		return id
	}

	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}

	return hex.EncodeToString(b[:])
}
