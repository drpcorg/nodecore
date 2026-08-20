package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// newGrpcKeyProcessor builds a basic auth processor with one local key and no
// request strategy, the shape the grpc ingress runs with when only key auth
// is configured.
func newGrpcKeyProcessor(t *testing.T, methods *config.AuthMethods) auth.AuthProcessor {
	t.Helper()
	appCfg := &config.AuthConfig{
		Enabled: true,
		KeyConfigs: []*config.KeyConfig{
			{
				Id:   "k1",
				Type: config.Local,
				LocalKeyConfig: &config.LocalKeyConfig{
					Key: "secret-key",
					KeySettingsConfig: &config.KeySettingsConfig{
						Methods: methods,
					},
				},
			},
		},
	}

	processor, err := auth.NewAuthProcessor(context.Background(), appCfg, integration.NewIntegrationResolver(nil))
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	return processor
}

func TestGrpcAuthPayloadResolvesKeyFromMetadata(t *testing.T) {
	processor := newGrpcKeyProcessor(t, nil)
	payload := auth.NewGrpcAuthPayload(metadata.Pairs(auth.XNodecoreKey, "secret-key"))

	assert.NoError(t, processor.Authenticate(context.Background(), payload))
	_, err := processor.PreKeyValidate(context.Background(), payload)
	assert.NoError(t, err)
	assert.Equal(t, "secret-key", processor.GetKeyValue(payload))
}

// metadata keys are case-insensitive: any client casing lands lowercase on
// the wire and must still resolve the key
func TestGrpcAuthPayloadKeyIsCaseInsensitive(t *testing.T) {
	processor := newGrpcKeyProcessor(t, nil)
	payload := auth.NewGrpcAuthPayload(metadata.New(map[string]string{"X-NODECORE-KEY": "secret-key"}))

	_, err := processor.PreKeyValidate(context.Background(), payload)
	assert.NoError(t, err)
	assert.Equal(t, "secret-key", processor.GetKeyValue(payload))
}

func TestGrpcAuthPayloadWithoutKey(t *testing.T) {
	processor := newGrpcKeyProcessor(t, nil)
	payload := auth.NewGrpcAuthPayload(metadata.MD{})

	_, err := processor.PreKeyValidate(context.Background(), payload)
	assert.ErrorContains(t, err, "api-key must be provided")
	assert.Empty(t, processor.GetKeyValue(payload))
}

func TestGrpcAuthPayloadUnknownKey(t *testing.T) {
	processor := newGrpcKeyProcessor(t, nil)
	payload := auth.NewGrpcAuthPayload(metadata.Pairs(auth.XNodecoreKey, "nope"))

	_, err := processor.PreKeyValidate(context.Background(), payload)
	assert.ErrorContains(t, err, "specified api-key not found")
}

// key method scoping must apply to gRPC methods like to any other
func TestGrpcAuthPayloadMethodScoping(t *testing.T) {
	processor := newGrpcKeyProcessor(t, &config.AuthMethods{
		Allowed: []string{"/sui.rpc.v2.LedgerService/GetServiceInfo"},
	})
	payload := auth.NewGrpcAuthPayload(metadata.Pairs(auth.XNodecoreKey, "secret-key"))

	allowed := protocol.NewUpstreamGrpcRequest("1", "/sui.rpc.v2.LedgerService/GetServiceInfo", nil, nil, "")
	assert.NoError(t, processor.PostKeyValidate(context.Background(), payload, allowed))

	forbidden := protocol.NewUpstreamGrpcRequest("1", "/sui.rpc.v2.LedgerService/GetObject", nil, nil, "")
	err := processor.PostKeyValidate(context.Background(), payload, forbidden)
	assert.ErrorContains(t, err, "method '/sui.rpc.v2.LedgerService/GetObject' is not allowed")
}
