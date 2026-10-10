package main

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // the websocket handshake is defined with SHA-1
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The mock node has to build without any dependency, so this is just enough of
// RFC 6455 for a JSON-RPC client: unfragmented text frames, ping and close.

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opText  = 0x1
	opClose = 0x8
	opPing  = 0x9
	opPong  = 0xA
)

type wsConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
}

func isWebsocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func acceptWebsocket(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	hijacker, ok := w.(http.Hijacker)
	if key == "" || !ok {
		http.Error(w, "not a websocket handshake", http.StatusBadRequest)
		return nil, errors.New("not a websocket handshake")
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	accept := sha1.Sum([]byte(key + websocketGUID)) //nolint:gosec // see above
	_, err = fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		base64.StdEncoding.EncodeToString(accept[:]))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &wsConn{conn: conn, reader: buffered.Reader}, nil
}

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	header := []byte{0x80 | opcode}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 0xFFFF:
		header = append(header, 126)
		header = binary.BigEndian.AppendUint16(header, uint16(len(payload)))
	default:
		header = append(header, 127)
		header = binary.BigEndian.AppendUint64(header, uint64(len(payload)))
	}
	if _, err := c.conn.Write(append(header, payload...)); err != nil {
		return err
	}
	return nil
}

func (c *wsConn) writeJSON(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return c.writeFrame(opText, payload)
}

// readMessage returns the next text message, answering pings on the way.
func (c *wsConn) readMessage() ([]byte, error) {
	for {
		var head [2]byte
		if _, err := io.ReadFull(c.reader, head[:]); err != nil {
			return nil, err
		}
		opcode, masked, length := head[0]&0x0F, head[1]&0x80 != 0, uint64(head[1]&0x7F)
		switch length {
		case 126:
			var extended [2]byte
			if _, err := io.ReadFull(c.reader, extended[:]); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(extended[:]))
		case 127:
			var extended [8]byte
			if _, err := io.ReadFull(c.reader, extended[:]); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(extended[:])
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.reader, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}

		switch opcode {
		case opText:
			return payload, nil
		case opPing:
			if err := c.writeFrame(opPong, payload); err != nil {
				return nil, err
			}
		case opClose:
			_ = c.writeFrame(opClose, nil)
			return nil, io.EOF
		}
	}
}

// serveWebsocket answers JSON-RPC over a websocket. eth_subscribe is confirmed
// and then fed with the node's head on every block until the connection ends.
func (n *node) serveWebsocket(w http.ResponseWriter, r *http.Request) {
	ws, err := acceptWebsocket(w, r)
	if err != nil {
		return
	}
	defer func() { _ = ws.conn.Close() }()
	closed := make(chan struct{})
	defer close(closed)

	subscriptions := 0
	for {
		message, err := ws.readMessage()
		if err != nil {
			return
		}
		var request rpcRequest
		if err := json.Unmarshal(message, &request); err != nil {
			return
		}
		switch request.Method {
		case "eth_subscribe":
			subscriptions++
			subscriptionId := fmt.Sprintf("0x%x", subscriptions)
			if err := ws.writeJSON(map[string]any{"jsonrpc": "2.0", "id": request.Id, "result": subscriptionId}); err != nil {
				return
			}
			go n.feedSubscription(ws, subscriptionId, closed)
		case "eth_unsubscribe":
			if err := ws.writeJSON(map[string]any{"jsonrpc": "2.0", "id": request.Id, "result": true}); err != nil {
				return
			}
		default:
			// off the read loop: a slow method must not hold the other requests
			go func() { _ = ws.writeJSON(n.reply(request)) }()
		}
	}
}

func (n *node) feedSubscription(ws *wsConn, subscriptionId string, closed <-chan struct{}) {
	ticker := time.NewTicker(n.blockTime)
	defer ticker.Stop()
	for {
		event := map[string]any{
			"jsonrpc": "2.0",
			"method":  "eth_subscription",
			"params":  map[string]any{"subscription": subscriptionId, "result": n.block()},
		}
		if err := ws.writeJSON(event); err != nil {
			return
		}
		select {
		case <-closed:
			return
		case <-ticker.C:
		}
	}
}
