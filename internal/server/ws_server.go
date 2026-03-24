package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

var wsConnectionsMetric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: config.AppName,
	Subsystem: "request",
	Name:      "ws_connections",
	Help:      "The total number of active websocket connections",
}, []string{"chain"})

func init() {
	prometheus.MustRegister(wsConnectionsMetric)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type messageEvent struct {
	messageType int
	message     []byte
	err         error
}

func HandleWebsocket(
	ctx context.Context,
	conn *websocket.Conn,
	chain string,
	authPayload auth.AuthPayload,
	appCtx *ApplicationContext,
) {
	log := zerolog.Ctx(ctx)

	subCtx := flow.NewSubCtx()

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	wsConnectionsMetric.WithLabelValues(chain).Inc()

	defer func() {
		wsConnectionsMetric.WithLabelValues(chain).Dec()
	}()

	var wsLock sync.Mutex
	var wg sync.WaitGroup
	messageChan := make(chan *messageEvent, 100)
	closeChan := make(chan struct{}, 1)

	closeConn := func() {
		if err := conn.Close(); err != nil {
			log.Error().Err(err).Msg("couldn't close a client websocket connection")
		}
	}

	wg.Add(1)
	go readMessages(cancelCtx, conn, messageChan, &wg)

loop:
	for {
		select {
		case <-cancelCtx.Done():
			break loop
		case <-closeChan:
			break loop
		case message := <-messageChan:
			if message.err != nil {
				var closedErr *websocket.CloseError
				if ok := errors.As(message.err, &closedErr); ok {
					if closedErr.Code == websocket.CloseNormalClosure || closedErr.Code == websocket.CloseNoStatusReceived {
						log.Debug().Msg("closing ws connection")
					} else {
						log.Error().Err(message.err).Msg("couldn't receive a ws message")
					}
				}
				break loop
			}
			preRequest := &Request{
				Chain: chain,
			}
			requestHandler, err := NewJsonRpcHandler(preRequest, bytes.NewReader(message.message), true)
			if err != nil {
				log.Error().Err(err).Msg("couldn't create requestHandler")
				break loop
			}

			handleResp := handleRequest(cancelCtx, requestHandler, authPayload, appCtx, subCtx)

			wg.Add(1)
			go func(ctx context.Context) {
				defer wg.Done()
				closeFunc := func() {
					select {
					case <-ctx.Done():
					case closeChan <- struct{}{}:
					}
				}
				for {
					select {
					case <-ctx.Done():
						return
					case response, ok := <-handleResp.responseWrappers:
						if !ok {
							return
						}
						if replyErr, ok := response.Response.(*protocol.ReplyError); ok {
							// close the connection if there is only WsTotalFailure error
							// otherwise write a response to the connection
							if replyErr.ErrorKind == protocol.TotalFailure && replyErr.GetError().Code == protocol.WsTotalFailure {
								log.Warn().Msgf("got a ws total failure signal, the connection will be closed")
								closeFunc()
								return
							}
						}
						writeEvent := func() {
							wsLock.Lock()
							defer wsLock.Unlock()
							writer, err := conn.NextWriter(message.messageType)
							if err != nil {
								log.Error().Err(err).Msg("couldn't get writer to send a response")
							} else {
								resp := requestHandler.ResponseEncode(response.Response)
								if _, err = io.Copy(writer, resp.ResponseReader); err != nil {
									log.Error().Err(err).Msg("couldn't copy message")
								}
								if err = writer.Close(); err != nil {
									log.Error().Err(err).Msg("couldn't write message")
								}
							}
						}
						writeEvent()
					}
				}
			}(cancelCtx)
		}
	}

	cancel()
	closeConn()
	wg.Wait()
}

func readMessages(ctx context.Context, conn *websocket.Conn, messageChan chan *messageEvent, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		messageType, message, err := conn.ReadMessage()

		select {
		case <-ctx.Done():
			return
		case messageChan <- &messageEvent{messageType: messageType, message: message, err: err}:
		}
		if err != nil {
			break
		}
	}
}
