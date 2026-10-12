// Command mocknode is a minimal EVM JSON-RPC node for e2e tests that are about
// nodecore itself and need no chain data: it reports a chain id, a head that
// keeps growing, and can answer one method slowly. The same port speaks
// JSON-RPC over HTTP and over a websocket, with eth_subscribe on the latter.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type rpcRequest struct {
	Id     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

type node struct {
	chainId    uint64
	started    time.Time
	blockTime  time.Duration
	slowMethod string
	slowDelay  time.Duration
}

func (n *node) block() map[string]any {
	number := 20_000_000 + uint64(time.Since(n.started)/n.blockTime)
	zeroHash := "0x" + strings.Repeat("00", 32)
	return map[string]any{
		"number":           fmt.Sprintf("0x%x", number),
		"hash":             fmt.Sprintf("0x%064x", number),
		"parentHash":       fmt.Sprintf("0x%064x", number-1),
		"timestamp":        fmt.Sprintf("0x%x", time.Now().Unix()),
		"transactions":     []any{},
		"uncles":           []any{},
		"difficulty":       "0x0",
		"totalDifficulty":  "0x0",
		"gasLimit":         "0x1c9c380",
		"gasUsed":          "0x0",
		"miner":            "0x" + strings.Repeat("00", 20),
		"nonce":            "0x0000000000000000",
		"size":             "0x200",
		"extraData":        "0x",
		"baseFeePerGas":    "0x1",
		"logsBloom":        "0x" + strings.Repeat("00", 256),
		"stateRoot":        zeroHash,
		"receiptsRoot":     zeroHash,
		"transactionsRoot": zeroHash,
		"sha3Uncles":       zeroHash,
		"mixHash":          zeroHash,
	}
}

func (n *node) answer(method string) any {
	if method == n.slowMethod {
		time.Sleep(n.slowDelay)
	}
	switch method {
	case "eth_chainId":
		return fmt.Sprintf("0x%x", n.chainId)
	case "net_version":
		return strconv.FormatUint(n.chainId, 10)
	case "eth_syncing":
		return false
	case "net_peerCount":
		return "0x20"
	case "web3_clientVersion":
		return "Geth/v1.16.0/linux-amd64/go1.24"
	case "eth_blockNumber":
		return n.block()["number"]
	case "eth_getBlockByNumber", "eth_getBlockByHash":
		return n.block()
	case "eth_gasPrice":
		return "0x3b9aca00"
	case "eth_getBalance":
		return fmt.Sprintf("0x%x", n.chainId)
	}
	return nil
}

func (n *node) reply(request rpcRequest) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": request.Id, "result": n.answer(request.Method)}
}

func (n *node) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isWebsocketUpgrade(r) {
		n.serveWebsocket(w, r)
		return
	}
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var response any
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		var batch []rpcRequest
		if err := json.Unmarshal(raw, &batch); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		replies := make([]map[string]any, 0, len(batch))
		for _, request := range batch {
			replies = append(replies, n.reply(request))
		}
		response = replies
	} else {
		var request rpcRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response = n.reply(request)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("couldn't write a response: %v", err)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	addr := flag.String("addr", envOr("MOCKNODE_ADDR", ":8545"), "listen address")
	chainId := flag.Uint64("chain-id", 0, "chain id reported by eth_chainId and net_version, MOCKNODE_CHAIN_ID or 1 by default")
	blockTime := flag.Duration("block-time", 0, "how often the head grows, MOCKNODE_BLOCK_TIME or 1s by default")
	slowMethod := flag.String("slow-method", envOr("MOCKNODE_SLOW_METHOD", ""), "a method that is answered after slow-delay")
	slowDelay := flag.Duration("slow-delay", 0, "the delay of slow-method, MOCKNODE_SLOW_DELAY or 0 by default")
	flag.Parse()

	if *chainId == 0 {
		parsed, err := strconv.ParseUint(envOr("MOCKNODE_CHAIN_ID", "1"), 10, 64)
		if err != nil {
			log.Fatalf("invalid MOCKNODE_CHAIN_ID: %v", err)
		}
		*chainId = parsed
	}
	if *blockTime == 0 {
		parsed, err := time.ParseDuration(envOr("MOCKNODE_BLOCK_TIME", "1s"))
		if err != nil {
			log.Fatalf("invalid MOCKNODE_BLOCK_TIME: %v", err)
		}
		*blockTime = parsed
	}
	if *slowDelay == 0 {
		parsed, err := time.ParseDuration(envOr("MOCKNODE_SLOW_DELAY", "0s"))
		if err != nil {
			log.Fatalf("invalid MOCKNODE_SLOW_DELAY: %v", err)
		}
		*slowDelay = parsed
	}

	server := &http.Server{
		Addr: *addr,
		Handler: &node{
			chainId:    *chainId,
			started:    time.Now(),
			blockTime:  *blockTime,
			slowMethod: *slowMethod,
			slowDelay:  *slowDelay,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("mocknode of chain %d is listening on %s", *chainId, *addr)
	log.Fatal(server.ListenAndServe())
}
