package flow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
)

// methodTranslator rewrites a client method into the request an upstream
// actually understands and reshapes the upstream response back into the shape
// the client expects. Translators are looked up by (spec name, client method)
// in sendUnaryRequest right before the request hits an api connector, so the
// translated request's spec method drives the connector choice.
type methodTranslator interface {
	TranslateRequest(ctx context.Context, request protocol.RequestHolder) (protocol.RequestHolder, error)
	TranslateResponse(request, upstreamRequest protocol.RequestHolder, upstreamHeadHeight uint64, response protocol.ResponseHolder) protocol.ResponseHolder
}

var methodTranslators = map[string]map[string]methodTranslator{
	"bitcoin": {
		"getblocknumber": &jsonRpcMethodAlias{specName: "bitcoin", target: "getblockcount"},
		"listunspent":    &bitcoinListUnspentTranslator{specName: "bitcoin"},
	},
}

func getMethodTranslator(specName, method string) methodTranslator {
	return methodTranslators[specName][method]
}

// jsonRpcMethodAlias resends the request under another JSON-RPC method name,
// keeping the id and params intact. The response passes through untouched.
type jsonRpcMethodAlias struct {
	specName string
	target   string
}

func (a *jsonRpcMethodAlias) TranslateRequest(_ context.Context, request protocol.RequestHolder) (protocol.RequestHolder, error) {
	body, err := jsonRpcBodyOf(request)
	if err != nil {
		return nil, err
	}
	body.Method = a.target
	return protocol.NewUpstreamJsonRpcRequest(request.Id(), *body, false, a.specName, request.Selectors()...), nil
}

func (a *jsonRpcMethodAlias) TranslateResponse(_, _ protocol.RequestHolder, _ uint64, response protocol.ResponseHolder) protocol.ResponseHolder {
	return response
}

const esploraAddressUtxoMethod = "GET#/address/*/utxo"

// bitcoinListUnspentTranslator serves bitcoind's listunspent from an esplora
// upstream: GET /address/{addr}/utxo, then reshapes the utxo list into the
// bitcoind response shape.
type bitcoinListUnspentTranslator struct {
	specName string
}

func (t *bitcoinListUnspentTranslator) TranslateRequest(_ context.Context, request protocol.RequestHolder) (protocol.RequestHolder, error) {
	body, err := jsonRpcBodyOf(request)
	if err != nil {
		return nil, err
	}
	address, err := listUnspentAddress(body.Params)
	if err != nil {
		return nil, err
	}
	params := &protocol.RequestParams{PathParams: []string{address}}
	return protocol.NewUpstreamRestRequest(request.Id(), esploraAddressUtxoMethod, params, nil, t.specName, request.Selectors()...), nil
}

func (t *bitcoinListUnspentTranslator) TranslateResponse(request, upstreamRequest protocol.RequestHolder, upstreamHeadHeight uint64, response protocol.ResponseHolder) protocol.ResponseHolder {
	if response.HasError() {
		return response
	}
	var utxos []esploraUtxo
	if err := sonic.Unmarshal(response.ResponseResult(), &utxos); err != nil {
		return protocol.NewTotalFailureFromErr(request.Id(), protocol.IncorrectResponseBodyError(err), protocol.JsonRpc)
	}
	address := ""
	if params := upstreamRequest.RequestParams(); params != nil && len(params.PathParams) > 0 {
		address = params.PathParams[0]
	}
	unspent := make([]listUnspentUtxo, 0, len(utxos))
	for _, utxo := range utxos {
		unspent = append(unspent, listUnspentUtxo{
			Txid:          utxo.Txid,
			Vout:          utxo.Vout,
			Address:       address,
			Amount:        satsToBtc(utxo.Value),
			Confirmations: utxoConfirmations(utxo.Status, upstreamHeadHeight),
		})
	}
	result, err := sonic.Marshal(unspent)
	if err != nil {
		return protocol.NewTotalFailureFromErr(request.Id(), protocol.IncorrectResponseBodyError(err), protocol.JsonRpc)
	}
	return protocol.NewSimpleHttpUpstreamResponse(request.Id(), result, protocol.JsonRpc)
}

type esploraUtxo struct {
	Txid   string            `json:"txid"`
	Vout   uint32            `json:"vout"`
	Status esploraUtxoStatus `json:"status"`
	Value  uint64            `json:"value"`
}

type esploraUtxoStatus struct {
	Confirmed   bool   `json:"confirmed"`
	BlockHeight uint64 `json:"block_height"`
}

type listUnspentUtxo struct {
	Txid          string          `json:"txid"`
	Vout          uint32          `json:"vout"`
	Address       string          `json:"address"`
	Amount        json.RawMessage `json:"amount"`
	Confirmations uint64          `json:"confirmations"`
}

func jsonRpcBodyOf(request protocol.RequestHolder) (*protocol.JsonRpcRequestBody, error) {
	rawBody, err := request.Body()
	if err != nil {
		return nil, protocol.ClientError(err)
	}
	var body protocol.JsonRpcRequestBody
	if err := sonic.Unmarshal(rawBody, &body); err != nil {
		return nil, protocol.ClientError(err)
	}
	return &body, nil
}

// listUnspentAddress extracts the single queried address from bitcoind-style
// listunspent params - [minconf, maxconf, [addresses]] - accepting a bare
// address string in place of the array defensively.
func listUnspentAddress(rawParams json.RawMessage) (string, error) {
	var params []any
	if len(rawParams) > 0 {
		if err := sonic.Unmarshal(rawParams, &params); err != nil {
			return "", protocol.InvalidParamsError("listunspent params must be an array")
		}
	}
	for _, param := range params {
		switch value := param.(type) {
		case string:
			return value, nil
		case []any:
			if len(value) != 1 {
				return "", protocol.InvalidParamsError("listunspent supports exactly one address")
			}
			address, ok := value[0].(string)
			if !ok {
				return "", protocol.InvalidParamsError("listunspent address must be a string")
			}
			return address, nil
		}
	}
	return "", protocol.InvalidParamsError("listunspent requires an address")
}

const satsPerBtc = 100_000_000

// satsToBtc renders a satoshi amount as a fixed 8-decimal BTC JSON number,
// avoiding float64 artifacts.
func satsToBtc(sats uint64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf("%d.%08d", sats/satsPerBtc, sats%satsPerBtc))
}

func utxoConfirmations(status esploraUtxoStatus, headHeight uint64) uint64 {
	if !status.Confirmed || status.BlockHeight == 0 || headHeight < status.BlockHeight {
		return 0
	}
	return headHeight - status.BlockHeight + 1
}
