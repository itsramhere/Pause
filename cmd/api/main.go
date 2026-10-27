package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/ramst/pause/internal/auth"
	"github.com/ramst/pause/internal/bootstrap"
	"github.com/ramst/pause/internal/httpapi"
)

func main() {
	log := bootstrap.Logger()
	ctx, stop := bootstrap.Context()
	defer stop()
	a, e := bootstrap.Load(ctx)
	if e != nil {
		log.Error("configuration_failed", "reason", e.Error())
		os.Exit(1)
	}
	defer a.DB.Pool.Close()
	authentication, e := auth.New(a.DB, a.Clock, a.Config.SessionTTL)
	if e != nil {
		log.Error("authentication_initialization_failed")
		os.Exit(1)
	}
	server := &http.Server{Addr: a.Config.Listen, Handler: (&httpapi.Server{App: a, Auth: authentication, Log: log}).Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Info("api_starting", "address", a.Config.Listen, "environment", a.Config.Environment)
	if e = server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		log.Error("api_stopped", "reason_code", "LISTEN_FAILED")
		os.Exit(1)
	}
}
