package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/dshackle"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	NodecoreHTTPPort   = "8080/tcp"
	NodecoreGRPCPort   = "9090/tcp"
	NodecoreHealthPort = "9091/tcp"
	HardhatPort        = "8545/tcp"
	ContainerConfig    = "/tmp/nodecore-e2e.yml"
)

var (
	nodecoreImageOnce sync.Once
	nodecoreImageErr  error
	hardhatImageOnce  sync.Once
	hardhatImageErr   error
)

type RPCNode struct {
	Alias     string
	Container tc.Container
	Endpoint  string
	NonceHex  string
}

type HardhatNode = RPCNode

func StartHardhatNode(t *testing.T, ctx context.Context, networkName, alias string, seedTxs int) *HardhatNode {
	t.Helper()
	return startScriptedHardhatNode(t, ctx, networkName, alias, seedTxs, "")
}

type HardhatNodeSpec struct {
	Alias   string
	SeedTxs int
	Rules   string
}

func StartHardhatNodes(t *testing.T, ctx context.Context, networkName string, specs ...HardhatNodeSpec) []*HardhatNode {
	t.Helper()
	nodes := make([]*HardhatNode, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nodes[i] = startScriptedHardhatNode(t, ctx, networkName, spec.Alias, spec.SeedTxs, spec.Rules)
		}()
	}
	wg.Wait()
	for i, node := range nodes {
		if node == nil {
			t.Fatalf("hardhat node %s did not start", specs[i].Alias)
		}
	}
	return nodes
}

func startScriptedHardhatNode(t *testing.T, ctx context.Context, networkName, alias string, seedTxs int, rulesJSON string) *HardhatNode {
	t.Helper()
	ensureHardhatImage(t)
	env := hardhatEnv(t)
	c, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:          "nodecore-e2e-hardhat:latest",
			ExposedPorts:   []string{HardhatPort},
			Env:            env,
			Networks:       []string{networkName},
			NetworkAliases: map[string][]string{networkName: {alias}},
			WaitingFor:     wait.ForListeningPort(HardhatPort).WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start hardhat node %s: %v", alias, err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("hardhat node host: %v", err)
	}
	port, err := c.MappedPort(ctx, HardhatPort)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("hardhat node mapped port: %v", err)
	}
	hardhatURL := fmt.Sprintf("http://%s:%s", host, port.Port())
	waitHardhatReady(t, ctx, hardhatURL)
	nonceHex := setHardhatNonce(t, ctx, hardhatURL, seedTxs)
	if strings.TrimSpace(rulesJSON) != "" {
		setHardhatRules(t, ctx, hardhatURL, rulesJSON)
	}
	return &RPCNode{Alias: alias, Container: c, Endpoint: hardhatURL, NonceHex: nonceHex}
}

func setHardhatRules(t *testing.T, ctx context.Context, hardhatURL, rulesJSON string) {
	t.Helper()
	var rules []any
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		t.Fatalf("parse hardhat e2e rules: %v", err)
	}
	resp := HardhatRPC(t, ctx, hardhatURL, "hardhat_setE2ERules", []any{rules})
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("set hardhat e2e rules: %+v", resp)
	}
}

func hardhatEnv(t *testing.T) map[string]string {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("NODECORE_E2E_DRPC_KEY"))
	if key == "" {
		t.Fatal("NODECORE_E2E_DRPC_KEY is required for forked Hardhat e2e tests")
	}

	env := map[string]string{
		"HARDHAT_FORK_URL": fmt.Sprintf("https://lb.drpc.live/ethereum/%s", url.PathEscape(key)),
	}
	if block := strings.TrimSpace(os.Getenv("NODECORE_E2E_HARDHAT_FORK_BLOCK")); block != "" {
		env["HARDHAT_FORK_BLOCK"] = block
	}
	return env
}

func waitHardhatReady(t *testing.T, ctx context.Context, hardhatURL string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = hardhatRPCNoFatal(ctx, hardhatURL, "eth_chainId", []any{})
		if _, hasErr := last["error"]; !hasErr && last["result"] != nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("hardhat did not become JSON-RPC ready before timeout: last=%+v", last)
}

func setHardhatNonce(t *testing.T, ctx context.Context, hardhatURL string, seedTxs int) string {
	t.Helper()
	const account = "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"

	currentResp := HardhatRPC(t, ctx, hardhatURL, "eth_getTransactionCount", []any{account, "latest"})
	currentHex := ResultString(t, currentResp)
	current, err := parseHexQuantity(currentHex)
	if err != nil {
		t.Fatalf("parse current hardhat nonce %q: %v", currentHex, err)
	}
	target := current + uint64(seedTxs)
	targetHex := fmt.Sprintf("0x%x", target)
	if seedTxs == 0 {
		return targetHex
	}

	params := []any{account, targetHex}
	deadline := time.Now().Add(30 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = HardhatRPC(t, ctx, hardhatURL, "hardhat_setNonce", params)
		if _, hasErr := last["error"]; !hasErr {
			return targetHex
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("hardhat_setNonce did not succeed before timeout: last=%+v", last)
	return ""
}

func parseHexQuantity(value string) (uint64, error) {
	value = strings.TrimPrefix(value, "0x")
	if value == "" {
		return 0, nil
	}
	var out uint64
	for _, ch := range value {
		out *= 16
		switch {
		case ch >= '0' && ch <= '9':
			out += uint64(ch - '0')
		case ch >= 'a' && ch <= 'f':
			out += uint64(ch-'a') + 10
		case ch >= 'A' && ch <= 'F':
			out += uint64(ch-'A') + 10
		default:
			return 0, fmt.Errorf("invalid hex digit %q", ch)
		}
	}
	return out, nil
}

func HardhatRPC(t *testing.T, ctx context.Context, hardhatURL, method string, params any) map[string]any {
	t.Helper()
	return hardhatRPCNoFatal(ctx, hardhatURL, method, params)
}

func ClearHardhatRequests(t *testing.T, ctx context.Context, node *RPCNode) {
	t.Helper()
	resp := HardhatRPC(t, ctx, node.Endpoint, "hardhat_clearE2ERequests", []any{})
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("clear hardhat requests on %s: %+v", node.Alias, resp)
	}
}

func CountHardhatRequests(t *testing.T, ctx context.Context, node *RPCNode, method string) int {
	t.Helper()
	resp := HardhatRPC(t, ctx, node.Endpoint, "hardhat_getE2ERequests", []any{})
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("get hardhat requests on %s: %+v", node.Alias, resp)
	}
	items, ok := resp["result"].([]any)
	if !ok {
		t.Fatalf("unexpected hardhat request log response from %s: %+v", node.Alias, resp)
	}
	count := 0
	for _, item := range items {
		m, ok := item.(map[string]any)
		if ok && m["method"] == method {
			count++
		}
	}
	return count
}
func hardhatRPCNoFatal(ctx context.Context, hardhatURL, method string, params any) map[string]any {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hardhatURL, bytes.NewReader(body))
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return out
}

func (m *RPCNode) InternalURL() string { return fmt.Sprintf("http://%s:8545", m.Alias) }

func (m *RPCNode) Terminate(ctx context.Context) {
	_ = m.Container.Terminate(ctx, tc.StopTimeout(time.Second))
}

type Nodecore struct {
	Container tc.Container
	HTTPURL   string
	GRPCAddr  string
	HealthURL string
	LogRedact []string
}

func GRPCClient(t *testing.T, nodecore *Nodecore) (*grpc.ClientConn, dshackle.BlockchainClient) {
	t.Helper()
	conn, err := grpc.NewClient(nodecore.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("create grpc client: %v", err)
	}
	waitGRPCReady(t, nodecore, conn, 30*time.Second)
	return conn, dshackle.NewBlockchainClient(conn)
}

func waitGRPCReady(t *testing.T, nodecore *Nodecore, conn *grpc.ClientConn, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return
		}
		if !conn.WaitForStateChange(ctx, state) {
			t.Fatalf("gRPC server at %s did not become ready before timeout; last state=%s\nlogs:\n%s", nodecore.GRPCAddr, state, nodecore.Logs(context.Background()))
		}
		conn.Connect()
	}
}

func GRPCSessionContext(ctx context.Context, sessionID string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("sessionid", sessionID))
}

type NativeCall struct {
	Chain     dshackle.ChainRef
	ID        uint32
	Method    string
	Payload   string
	Selectors []*dshackle.Selector
}

func NativeCallOnce(ctx context.Context, client dshackle.BlockchainClient, call NativeCall, timeout time.Duration) (*dshackle.NativeCallReplyItem, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := client.NativeCall(callCtx, &dshackle.NativeCallRequest{
		Chain: call.Chain,
		Items: []*dshackle.NativeCallItem{{
			Id:        call.ID,
			Method:    call.Method,
			Selectors: call.Selectors,
			Data:      &dshackle.NativeCallItem_Payload{Payload: []byte(call.Payload)},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("NativeCall open stream failed: %w", err)
	}
	item, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("NativeCall recv failed: %w", err)
	}
	return item, nil
}

func NativeCallUntilSuccess(t *testing.T, ctx context.Context, client dshackle.BlockchainClient, nodecore *Nodecore, call NativeCall, timeout time.Duration) *dshackle.NativeCallReplyItem {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *dshackle.NativeCallReplyItem
	var lastErr error
	for time.Now().Before(deadline) {
		item, err := NativeCallOnce(ctx, client, call, 5*time.Second)
		if err == nil {
			last = item
		}
		if err == nil && item.GetSucceed() {
			return item
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("gRPC NativeCall %s did not succeed before timeout: lastErr=%v last=%+v\nlogs:\n%s", call.Method, lastErr, last, nodecore.Logs(ctx))
	return nil
}

func WaitForFullChainStatus(t *testing.T, ctx context.Context, client dshackle.BlockchainClient, nodecore *Nodecore, chain dshackle.ChainRef, timeout time.Duration) *dshackle.SubscribeChainStatusResponse {
	t.Helper()
	statusCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := client.SubscribeChainStatus(statusCtx, &dshackle.SubscribeChainStatusRequest{})
	if err != nil {
		t.Fatalf("SubscribeChainStatus open stream failed: %v\nlogs:\n%s", err, nodecore.Logs(ctx))
	}

	for {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("SubscribeChainStatus recv failed before full response: %v\nlogs:\n%s", err, nodecore.Logs(ctx))
		}
		desc := resp.GetChainDescription()
		if desc == nil || desc.GetChain() != chain || !resp.GetFullResponse() {
			continue
		}
		return resp
	}
}

func AssertFullChainStatus(t *testing.T, resp *dshackle.SubscribeChainStatusResponse) {
	t.Helper()
	desc := resp.GetChainDescription()
	buildInfo := resp.GetBuildInfo()
	if buildInfo == nil || !strings.HasPrefix(buildInfo.GetVersion(), "nodecore/") {
		t.Fatalf("SubscribeChainStatus missing build version metadata: buildInfo=%+v resp=%+v", buildInfo, resp)
	}

	if desc == nil || len(desc.GetChainEvent()) == 0 {
		t.Fatalf("SubscribeChainStatus full response has no chain events: %+v", resp)
	}
	var hasStatus, hasMethods, hasHead bool
	for _, event := range desc.GetChainEvent() {
		if event.GetStatus() != nil {
			hasStatus = true
		}
		if methods := event.GetSupportedMethodsEvent(); methods != nil && len(methods.GetMethods()) > 0 {
			hasMethods = true
		}
		if head := event.GetHead(); head != nil && head.GetHeight() > 0 && head.GetBlockId() != "" {
			hasHead = true
		}
	}
	if !hasStatus || !hasMethods || !hasHead {
		t.Fatalf("SubscribeChainStatus missing expected events: status=%v methods=%v head=%v resp=%+v", hasStatus, hasMethods, hasHead, resp)
	}
}

func WaitForChainStatusBuildVersion(t *testing.T, ctx context.Context, client dshackle.BlockchainClient, nodecore *Nodecore, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		stream, err := client.SubscribeChainStatus(callCtx, &dshackle.SubscribeChainStatusRequest{})
		if err == nil {
			resp, recvErr := stream.Recv()
			if recvErr == nil && resp.GetBuildInfo().GetVersion() != "" {
				cancel()
				return resp.GetBuildInfo().GetVersion()
			}
			lastErr = recvErr
		} else {
			lastErr = err
		}
		cancel()
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("chain status build info did not arrive before timeout: lastErr=%v\nlogs:\n%s", lastErr, nodecore.Logs(ctx))
	return ""
}

func StartNodecore(t *testing.T, ctx context.Context, networkName, config string, redactions ...string) *Nodecore {
	t.Helper()
	return StartNodecoreWithFiles(t, ctx, networkName, config, nil, redactions...)
}

func ensureHardhatImage(t *testing.T) {
	t.Helper()
	hardhatImageOnce.Do(func() {
		hardhatImageErr = dockerBuild(RepoRoot(t), "test/e2e/internal/hardhat", "nodecore-e2e-hardhat:latest")
	})
	if hardhatImageErr != nil {
		t.Fatalf("build hardhat e2e image: %v", hardhatImageErr)
	}
}

func ensureNodecoreImage(t *testing.T) {
	t.Helper()
	nodecoreImageOnce.Do(func() {
		nodecoreImageErr = dockerBuild(RepoRoot(t), ".", "nodecore-e2e:latest")
	})
	if nodecoreImageErr != nil {
		t.Fatalf("build nodecore e2e image: %v", nodecoreImageErr)
	}
}

func BuildNodecoreImageWithBuildInfo(t *testing.T, tag, version, gitSHA string) {
	t.Helper()
	cmd := exec.Command(
		"docker", "build",
		"-t", tag,
		"--build-arg", "VERSION="+version,
		"--build-arg", "GIT_SHA="+gitSHA,
		RepoRoot(t),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build nodecore image %s: %v: %s", tag, err, string(out))
	}
}

func dockerBuild(repoRoot, contextRel, tag string) error {
	if strings.ToLower(strings.TrimSpace(os.Getenv("NODECORE_E2E_REBUILD_IMAGES"))) != "true" {
		if err := exec.Command("docker", "image", "inspect", tag).Run(); err == nil {
			return nil
		}
	}
	cmd := exec.Command("docker", "build", "-t", tag, filepath.Join(repoRoot, contextRel))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

func StartNodecoreWithFiles(t *testing.T, ctx context.Context, networkName, config string, extraFiles map[string]string, redactions ...string) *Nodecore {
	t.Helper()
	ensureNodecoreImage(t)
	return StartNodecoreWithImageAndFiles(t, ctx, networkName, "nodecore-e2e:latest", config, extraFiles, redactions...)
}

func StartNodecoreWithImageAndFiles(t *testing.T, ctx context.Context, networkName, image, config string, extraFiles map[string]string, redactions ...string) *Nodecore {
	t.Helper()
	files := []tc.ContainerFile{{Reader: strings.NewReader(config), ContainerFilePath: ContainerConfig, FileMode: 0o644}}
	for path, content := range extraFiles {
		files = append(files, tc.ContainerFile{Reader: strings.NewReader(content), ContainerFilePath: path, FileMode: 0o644})
	}
	c, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:        image,
			ExposedPorts: []string{NodecoreHTTPPort, NodecoreGRPCPort, NodecoreHealthPort},
			Env: map[string]string{
				"NODECORE_CONFIG_PATH": ContainerConfig,
				"LOG_FORMAT":           "console",
				"LOG_LEVEL":            "info",
			},
			Files:      files,
			Networks:   []string{networkName},
			WaitingFor: wait.ForListeningPort(NodecoreHTTPPort).WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start nodecore: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("nodecore host: %v", err)
	}
	httpPort, err := c.MappedPort(ctx, NodecoreHTTPPort)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("nodecore mapped http port: %v", err)
	}
	grpcPort, err := c.MappedPort(ctx, NodecoreGRPCPort)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("nodecore mapped grpc port: %v", err)
	}
	healthPort, err := c.MappedPort(ctx, NodecoreHealthPort)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("nodecore mapped health port: %v", err)
	}
	nodecore := &Nodecore{Container: c, HTTPURL: fmt.Sprintf("http://%s:%s", host, httpPort.Port()), GRPCAddr: fmt.Sprintf("%s:%s", host, grpcPort.Port()), HealthURL: fmt.Sprintf("http://%s:%s", host, healthPort.Port()), LogRedact: redactions}
	waitNodecoreHealth(t, ctx, nodecore, 30*time.Second)
	return nodecore
}

func waitNodecoreHealth(t *testing.T, ctx context.Context, nodecore *Nodecore, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	var lastStatus int
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nodecore.HealthURL+"/health", nil)
		if err != nil {
			t.Fatalf("create nodecore health request: %v", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if lastStatus == http.StatusOK {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("nodecore health endpoint did not become ready before timeout; status=%d err=%v\nlogs:\n%s", lastStatus, lastErr, nodecore.Logs(context.Background()))
}

func (n *Nodecore) Terminate(ctx context.Context) {
	_ = n.Container.Terminate(ctx, tc.StopTimeout(time.Second))
}

func (n *Nodecore) Logs(ctx context.Context) string {
	reader, err := n.Container.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("<failed to read nodecore logs: %v>", err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Sprintf("<failed to read nodecore logs: %v>", err)
	}
	logs := string(data)
	for _, secret := range n.LogRedact {
		if secret != "" {
			logs = strings.ReplaceAll(logs, secret, "<redacted>")
		}
	}
	const max = 32 * 1024
	if len(logs) > max {
		logs = logs[len(logs)-max:]
	}
	if strings.TrimSpace(logs) == "" {
		return "<empty>"
	}
	return logs
}

func NewNetwork(t *testing.T, ctx context.Context) (name string, cleanup func()) {
	t.Helper()
	net, err := network.New(ctx, network.WithDriver("bridge"), network.WithAttachable())
	if err != nil {
		t.Fatalf("create docker network: %v", err)
	}
	return net.Name, func() { _ = net.Remove(context.Background()) }
}

type HTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func JSONRPC(t *testing.T, ctx context.Context, baseURL, chain, method string, params any) map[string]any {
	t.Helper()
	return JSONRPCWithHeaders(t, ctx, baseURL, chain, method, params, nil)
}

func JSONRPCWithHeaders(t *testing.T, ctx context.Context, baseURL, chain, method string, params any, headers map[string]string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatalf("marshal json-rpc request: %v", err)
	}
	raw := JSONRPCRaw(t, ctx, baseURL, chain, body, headers)
	var out map[string]any
	if err := json.Unmarshal(raw.Body, &out); err != nil {
		t.Fatalf("decode json-rpc response status=%d body=%s: %v", raw.StatusCode, string(raw.Body), err)
	}
	return out
}

func JSONRPCBatch(t *testing.T, ctx context.Context, baseURL, chain string, batch any, headers map[string]string) []map[string]any {
	t.Helper()
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal json-rpc batch request: %v", err)
	}
	raw := JSONRPCRaw(t, ctx, baseURL, chain, body, headers)
	var out []map[string]any
	if err := json.Unmarshal(raw.Body, &out); err != nil {
		t.Fatalf("decode json-rpc batch response status=%d body=%s: %v", raw.StatusCode, string(raw.Body), err)
	}
	return out
}

func JSONRPCExpectError(t *testing.T, ctx context.Context, baseURL, chain, method string, params any, headers map[string]string) map[string]any {
	t.Helper()
	resp := JSONRPCWithHeaders(t, ctx, baseURL, chain, method, params, headers)
	if errVal, ok := resp["error"]; !ok || errVal == nil {
		t.Fatalf("json-rpc %s did not return an error: %+v", method, resp)
	}
	return resp
}

func JSONRPCRaw(t *testing.T, ctx context.Context, baseURL, chain string, body []byte, headers map[string]string) HTTPResponse {
	t.Helper()
	url := fmt.Sprintf("%s/queries/%s", strings.TrimRight(baseURL, "/"), strings.TrimLeft(chain, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create json-rpc request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("json-rpc raw request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read json-rpc response: %v", err)
	}
	return HTTPResponse{StatusCode: resp.StatusCode, Headers: resp.Header.Clone(), Body: respBody}
}

func RepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test helper path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
}

func ResultString(t *testing.T, resp map[string]any) string {
	t.Helper()
	if errVal, ok := resp["error"]; ok && errVal != nil {
		t.Fatalf("json-rpc returned error: %+v", errVal)
	}
	result, ok := resp["result"].(string)
	if !ok {
		t.Fatalf("json-rpc result is not string: %#v", resp["result"])
	}
	return result
}
