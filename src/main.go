package main

import (
	"flag"
	_ "github.com/drpcorg/dshaltie/src/chains"
	"github.com/drpcorg/dshaltie/src/config"
	_ "github.com/drpcorg/dshaltie/src/logger"
	"github.com/drpcorg/dshaltie/src/upstreams"
	"github.com/rs/zerolog/log"
	"time"
)

func main() {
	flag.Parse()

	appConfig, err := config.NewAppConfig()
	if err != nil {
		log.Panic().Err(err).Msg("unable to parse the config file")
	}

	upstreamSupervisor := upstreams.NewUpstreamSupervisor()
	for _, project := range appConfig.Projects {
		go upstreamSupervisor.StartUpstreams(project.Upstreams)
	}

	time.Sleep(50000 * time.Second)
}
