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
	"github.com/drpcorg/nodecore/pkg/chains"
	_ "github.com/drpcorg/nodecore/pkg/errors_config"
	_ "github.com/drpcorg/nodecore/pkg/logger"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/rs/zerolog/log"
	_ "go.uber.org/automaxprocs"
)

const (
	// envExtraChainsPath points at an additional chain-registry YAML (same
	// schema as drpcorg/public chains.yaml) that gets merged into the
	// embedded registry at startup. Empty/unset = embedded only.
	envExtraChainsPath = "NODECORE_EXTRA_CHAINS_PATH"
	// specPathVar points at a directory of JSON method specs that extend the
	// embedded ones shipped by github.com/drpcorg/public.
	specPathVar = "NODECORE_SPECS_PATH"
)

func main() {
	flag.Parse()

	// SIGHUP reloads the upstream list. A process that has not taken the signal
	// over is terminated by it, so it is taken over before anything else and
	// never given back: a SIGHUP sent while nodecore is starting waits in the
	// channel, one sent while it is shutting down is ignored.
	reloadSignals := make(chan os.Signal, 1)
	signal.Notify(reloadSignals, syscall.SIGHUP)

	if path := os.Getenv(envExtraChainsPath); path != "" {
		extra, err := os.ReadFile(path)
		if err != nil {
			log.Panic().Err(err).Str("path", path).Msg("unable to read extra chains file")
		}
		if err := chains.LoadExtraChains(extra); err != nil {
			log.Panic().Err(err).Str("path", path).Msg("unable to merge extra chains")
		}
		log.Info().Str("path", path).Msg("loaded extra chain definitions")
	}

	// Specs load before the config: connector validation checks each connector
	// against the connectors its chain's method spec declares.
	specLoader := specs.NewMethodSpecLoader()
	if path := os.Getenv(specPathVar); path != "" {
		specLoader = specs.NewMethodSpecLoaderWithExtraFs(os.DirFS(path))
		log.Info().Str("path", path).Msg("extending method specs with external directory")
	}
	if err := specLoader.Load(); err != nil {
		log.Panic().Err(err).Msg("unable to load method specs")
	}

	appConfig, err := config.NewAppConfig()
	if err != nil {
		log.Panic().Err(err).Msg("unable to parse the config file")
	}

	mainCtx, mainCtxCancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Info().Msgf("got signal %v", sig)
		mainCtxCancel()
	}()

	nodeCoreApp, err := app.NewApp(mainCtx, appConfig, reloadSignals)
	if err != nil {
		log.Panic().Err(err).Msg("unable to create the app")
	}
	nodeCoreApp.Start()
}
