package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Fuucodi0X/tailcat-preview/internal/gateway"
)

func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		listenAddr     = flag.String("listen", envOr("DEVPREVIEW_LISTEN", ":"+envOr("PORT", "8080")), "HTTP listen address")
		publicURL      = flag.String("public-url", envOr("DEVPREVIEW_PUBLIC_URL", os.Getenv("RENDER_EXTERNAL_URL")), "public HTTPS URL")
		controlToken   = flag.String("control-token", os.Getenv("DEVPREVIEW_CONTROL_TOKEN"), "shared laptop-to-gateway control token")
		insecureCookie = flag.Bool("insecure-cookie", envBool("DEVPREVIEW_INSECURE_COOKIE"), "allow the preview cookie over plain HTTP")
	)
	flag.Parse()

	server, err := gateway.NewServer(gateway.Config{
		ControlToken:   *controlToken,
		PublicURL:      *publicURL,
		InsecureCookie: *insecureCookie,
		Logger:         slog.Default(),
	})
	if err != nil {
		return err
	}
	defer server.Close()

	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	slog.Info("gateway listening", "address", *listenAddr, "public_url", *publicURL)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
