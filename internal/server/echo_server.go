package server

import (
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/labstack/echo/v4"
)

func StartEcho(e *echo.Echo, addr string, tlsCfg *config.TlsConfig) error {
	if tlsCfg != nil && tlsCfg.Enabled {
		return e.StartTLS(addr, tlsCfg.Certificate, tlsCfg.Key)
	}
	return e.Start(addr)
}
