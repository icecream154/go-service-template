package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// serve owns the listener and HTTP shutdown; the caller closes application resources afterwards.
func serve(ctx context.Context, server *http.Server, shutdownTimeout time.Duration, logger *zap.Logger) error {
	if ctx == nil || server == nil || logger == nil || shutdownTimeout <= 0 {
		return errors.New("HTTP server requires context, server, logger and a positive shutdown timeout")
	}
	if ctx.Err() != nil {
		return nil
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", server.Addr)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return nil
		}
		return fmt.Errorf("listen for HTTP requests: %w", err)
	}
	return serveListener(ctx, server, listener, shutdownTimeout, logger)
}

func serveListener(ctx context.Context, server *http.Server, listener net.Listener, shutdownTimeout time.Duration, logger *zap.Logger) error {
	defer listener.Close()
	exited := make(chan error, 1)
	go func() { exited <- server.Serve(listener) }()
	logger.Info("HTTP server listening", zap.String("address", listener.Addr().String()))
	var serveErr error
	finished := false
	select {
	case err := <-exited:
		finished = true
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	case <-ctx.Done():
	}
	logger.Info("stopping HTTP server")
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownContext)
	var closeErr error
	if shutdownErr != nil {
		closeErr = server.Close()
	}
	if !finished {
		if err := <-exited; !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	}
	return errors.Join(serveErr, shutdownErr, closeErr)
}
