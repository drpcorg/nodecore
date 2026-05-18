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
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog/log"
	_ "go.uber.org/automaxprocs"
)

const (
	// envExtraChainsPath points at an additional chain-registry YAML (same
	// schema as drpcorg/public chains.yaml) that gets merged into the
	// embedded registry at startup. Empty/unset = embedded only.
	envExtraChainsPath = "NODECORE_EXTRA_CHAINS_PATH"

	// envExtraSpecsPath points at a directory of JSON method-spec files that
	// extend or override the embedded specs. Empty/unset = embedded only.
	envExtraSpecsPath = "NODECORE_EXTRA_SPECS_PATH"
)

func main() {
	flag.Parse()

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

	appConfig, err := config.NewAppConfig()
	if err != nil {
		log.Panic().Err(err).Msg("unable to parse the config file")
	}

	specLoader := specs.NewMethodSpecLoader()
	if path := os.Getenv(envExtraSpecsPath); path != "" {
		specLoader = specs.NewMethodSpecLoaderWithFs(os.DirFS(path))
		log.Info().Str("path", path).Msg("loading method specs from external directory")
	}
	err = specLoader.Load()
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
