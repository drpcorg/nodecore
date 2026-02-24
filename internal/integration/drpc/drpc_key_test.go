package drpc_test

import (
	"testing"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/stretchr/testify/assert"
)

func TestMarshallDrpcKey(t *testing.T) {
	key := drpc.DrpcKey{
		KeyId:             "drpc-key-id",
		IpWhitelist:       []string{"192.168.1.2"},
		MethodsWhitelist:  []string{"eth_call"},
		ContractWhitelist: []string{"0xdef"},
		CorsOrigins:       []string{"http://localhost:8080"},
		MethodsBlacklist:  []string{"eth_getBalance"},
		ApiKey:            "api-key",
	}
	expectedKey := `{"key_id":"drpc-key-id","ip_whitelist":["192.168.1.2"],"methods_blacklist":["eth_getBalance"],"methods_whitelist":["eth_call"],"contract_whitelist":["0xdef"],"cors_origins":["http://localhost:8080"],"api_key":"api-key"}`

	keyBytes, err := sonic.Marshal(key)
	assert.NoError(t, err)
	assert.Equal(t, expectedKey, string(keyBytes))
}

func TestUnmarshallDrpcKey(t *testing.T) {
	key := `{"key_id":"drpc-key-id","ip_whitelist":["192.168.1.2"],"methods_blacklist":["eth_getBalance"],"methods_whitelist":["eth_call"],"contract_whitelist":["0xdef"],"cors_origins":["http://localhost:8080"],"api_key":"api-key"}`
	expectedKey := drpc.DrpcKey{
		KeyId:             "drpc-key-id",
		IpWhitelist:       []string{"192.168.1.2"},
		MethodsWhitelist:  []string{"eth_call"},
		ContractWhitelist: []string{"0xdef"},
		CorsOrigins:       []string{"http://localhost:8080"},
		MethodsBlacklist:  []string{"eth_getBalance"},
		ApiKey:            "api-key",
	}

	var keyData drpc.DrpcKey
	err := sonic.Unmarshal([]byte(key), &keyData)
	assert.NoError(t, err)
	assert.Equal(t, expectedKey, keyData)
}

func TestDrpcKeyGetValues(t *testing.T) {
	key := drpc.DrpcKey{
		KeyId:             "drpc-key-id",
		IpWhitelist:       []string{"192.168.1.2"},
		MethodsWhitelist:  []string{"eth_call"},
		ContractWhitelist: []string{"0xdef"},
		CorsOrigins:       []string{"http://localhost:8080"},
		MethodsBlacklist:  []string{"eth_getBalance"},
		ApiKey:            "api-key",
	}

	assert.Equal(t, "drpc-key-id", key.Id())
	assert.Equal(t, "api-key", key.GetKeyValue())
}
