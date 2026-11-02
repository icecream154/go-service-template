package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"go-service-template/bootstrap"
	"go-service-template/routers"

	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() (result error) {
	flags := flag.NewFlagSet("web", flag.ContinueOnError)
	configPath := flags.String("config", "config/app.yml", "application configuration file")
	address := flags.String("addr", "", "override the HTTP listen address")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := bootstrap.Initialize(*configPath)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, app.Close()) }()
	if *address != "" {
		app.Config.HTTP.Address = *address
		if err := app.Config.Validate(); err != nil {
			return err
		}
	}
	if app.Config.AppDebug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	router, err := routers.InitWebRouter(app.Runtime, app.Config.HTTP, app.Logger)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              app.Config.HTTP.Address,
		Handler:           router,
		ReadHeaderTimeout: app.Config.HTTP.ReadHeaderTimeout,
		WriteTimeout:      app.Config.HTTP.WriteTimeout,
		IdleTimeout:       app.Config.HTTP.IdleTimeout,
	}
	return serve(ctx, server, app.Config.HTTP.ShutdownTimeout, app.Logger)
}
