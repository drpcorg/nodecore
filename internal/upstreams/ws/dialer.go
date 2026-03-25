package ws

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"
)

type DialFunc func() (*websocket.Conn, error)

type DialWsService interface {
	NewConnectFunc(ctx context.Context) (DialFunc, error)
}

const (
	wsReadBuffer  = 1024
	wsWriteBuffer = 1024
)

var wsBufferPool = new(sync.Pool)

type DefaultDialWsService struct {
	connectorConfig *config.ApiConnectorConfig
	torProxyUrl     string
}

func (d *DefaultDialWsService) NewConnectFunc(ctx context.Context) (DialFunc, error) {
	endpoint := d.connectorConfig.Url

	socksDialer, err := newSocksDialer(endpoint, d.torProxyUrl)
	if err != nil {
		return nil, err
	}

	tlsCfg, err := newTLSConfig(d.connectorConfig.Ca)
	if err != nil {
		return nil, err
	}

	dialer := &websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		ReadBufferSize:   wsReadBuffer,
		WriteBufferSize:  wsWriteBuffer,
		WriteBufferPool:  wsBufferPool,
		Proxy:            http.ProxyFromEnvironment,
		NetDial:          socksDialer,
		HandshakeTimeout: 45 * time.Second,
	}

	header := make(http.Header, len(d.connectorConfig.Headers))
	for key, val := range d.connectorConfig.Headers {
		header.Add(key, val)
	}

	connectFunc := func() (*websocket.Conn, error) {
		conn, _, err := dialer.DialContext(ctx, endpoint, header)
		if err != nil {
			return nil, err
		}
		return conn, nil
	}

	return connectFunc, nil
}

func NewDefaultDialWsService(connectorConfig *config.ApiConnectorConfig, torProxyUrl string) *DefaultDialWsService {
	return &DefaultDialWsService{
		connectorConfig: connectorConfig,
		torProxyUrl:     torProxyUrl,
	}
}

func newSocksDialer(endpoint, torProxyURL string) (func(network, addr string) (net.Conn, error), error) {
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("error parsing the endpoint: %v", err)
	}

	if !strings.HasSuffix(parsedEndpoint.Hostname(), ".onion") {
		return nil, nil
	}
	if torProxyURL == "" {
		return nil, errors.New("tor proxy url is required for onion endpoints")
	}

	netDialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	dial, err := proxy.SOCKS5("tcp", torProxyURL, nil, netDialer)
	if err != nil {
		return nil, err
	}
	return dial.Dial, nil
}

func newTLSConfig(ca string) (*tls.Config, error) {
	customCA, err := utils.GetCustomCAPool(ca)
	if err != nil {
		return nil, err
	}
	if customCA == nil {
		return nil, nil
	}
	return &tls.Config{RootCAs: customCA}, nil
}

var _ DialWsService = (*DefaultDialWsService)(nil)
