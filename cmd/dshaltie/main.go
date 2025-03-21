package main

import (
	"context"
	"flag"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/upstreams"
	_ "github.com/drpcorg/dshaltie/pkg/chains"
	_ "github.com/drpcorg/dshaltie/pkg/logger"
	"github.com/rs/zerolog/log"
	"time"
)

func main() {
	flag.Parse()

	appConfig, err := config.NewAppConfig()
	if err != nil {
		log.Panic().Err(err).Msg("unable to parse the config file")
	}

	appCtx := context.Background()

	upstreamSupervisor := upstreams.NewUpstreamSupervisor(appCtx)
	go upstreamSupervisor.StartUpstreams(appConfig.UpstreamConfig)

	time.Sleep(50000 * time.Second)
}
