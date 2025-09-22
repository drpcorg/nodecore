package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/labstack/echo/v4"
)

func StartEcho(e *echo.Echo, addr string, tlsCfg *config.TlsConfig) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	if tlsCfg != nil && tlsCfg.Enabled {
		tc := &tls.Config{
			NextProtos: []string{"h2"},
		}
		cert, err := tls.LoadX509KeyPair(tlsCfg.Certificate, tlsCfg.Key)
		if err != nil {
			return err
		}
		tc.Certificates = []tls.Certificate{cert}

		if tlsCfg.Ca != "" {
			pem, err := os.ReadFile(tlsCfg.Ca)
			if err != nil {
				return err
			}
			caPool := x509.NewCertPool()
			if !caPool.AppendCertsFromPEM(pem) {
				return fmt.Errorf("failed to append client CA")
			}
			tc.ClientCAs = caPool
			tc.ClientAuth = tls.RequireAndVerifyClientCert
		}

		ln = tls.NewListener(ln, tc)
	}

	s := e.Server
	s.Addr = addr
	s.Handler = e
	return s.Serve(ln)
}
