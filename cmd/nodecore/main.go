package main

import (
	"context"
	"flag"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/drpcorg/nodecore/internal/app"
	"github.com/drpcorg/nodecore/internal/config"
	_ "github.com/drpcorg/nodecore/pkg/chains"
	_ "github.com/drpcorg/nodecore/pkg/errors_config"
	_ "github.com/drpcorg/nodecore/pkg/logger"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog/log"
	_ "go.uber.org/automaxprocs"
)

func main() {
	flag.Parse()

	appConfig, err := config.NewAppConfig()
	if err != nil {
		log.Panic().Err(err).Msg("unable to parse the config file")
	}
	err = specs.NewMethodSpecLoader().Load()
	if err != nil {
		log.Panic().Err(err).Msg("unable to load method specs")
	}

	mainCtx, mainCtxCancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Info().Msgf("got signal %v", sig)
		mainCtxCancel()
	}()

	nodeCoreApp, err := app.NewApp(mainCtx, appConfig)
	if err != nil {
		log.Panic().Err(err).Msg("unable to create the app")
	}
	nodeCoreApp.Start()
}
