package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestServeListenerWaitsForRequestsAndBoundsShutdown(t *testing.T) {
	for _, expire := range []bool{false, true} {
		name := "graceful"
		if expire {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			shutdownStarted := make(chan struct{})
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				close(started)
				<-release
				w.WriteHeader(http.StatusNoContent)
			})}
			server.RegisterOnShutdown(func() { close(shutdownStarted) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := time.Second
			if expire {
				timeout = 30 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() { done <- serveListener(ctx, server, listener, timeout, zap.NewNop()) }()
			requestDone := make(chan struct{})
			go func() {
				defer close(requestDone)
				client := &http.Client{Timeout: 2 * time.Second}
				if response, err := client.Get("http://" + listener.Addr().String()); err == nil {
					response.Body.Close()
				}
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				close(release)
				t.Fatal("request did not start")
			}
			cancel()
			<-shutdownStarted
			if !expire {
				select {
				case err := <-done:
					close(release)
					t.Fatalf("shutdown returned before request finished: %v", err)
				default:
				}
				close(release)
			}
			select {
			case err := <-done:
				if expire {
					close(release)
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("shutdown error = %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				if expire {
					close(release)
				}
				t.Fatal("shutdown did not complete")
			}
			<-finished
			<-requestDone
		})
	}
}

func TestServeReportsListenFailureWithoutSuccessLog(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	core, logs := observer.New(zapcore.InfoLevel)
	err = serve(context.Background(), &http.Server{Addr: listener.Addr().String()}, time.Second, zap.New(core))
	if err == nil || logs.FilterMessage("HTTP server listening").Len() != 0 {
		t.Fatalf("listener failure = %v, logs = %v", err, logs.All())
	}
}
