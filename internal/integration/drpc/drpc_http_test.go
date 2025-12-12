package drpc_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
)

func TestDrpcHttpConnectorOwnerNoError(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusOK, nil)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	err := connector.OwnerExists("id", "token")
	assert.NoError(t, err)

	var expectedErr *protocol.ClientRetryableError
	assert.NotErrorAs(t, err, &expectedErr)
}

func TestDrpcHttpConnectorOwnerNotExistThenError(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusNotFound, nil)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	err := connector.OwnerExists("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.NotErrorAs(t, err, &expectedErr)

	assert.ErrorContains(t, err, "owner 'id' not found")
}

func TestDrpcHttpConnectorOwnerForbiddenThenError(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	responseBody := []byte(`{"message":"wrong api token", "code":403}`)

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusForbidden, responseBody)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	err := connector.OwnerExists("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.NotErrorAs(t, err, &expectedErr)

	assert.ErrorContains(t, err, "forbidden, owner - 'id', message: wrong api token")
}

func TestDrpcHttpConnectorOwnerTooManyRequestsThenError(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusTooManyRequests, nil)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	err := connector.OwnerExists("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.ErrorAs(t, err, &expectedErr)

	assert.ErrorContains(t, err, "too many requests, please try again later")
}

func TestDrpcHttpConnectorOwnerForbiddenCantParseBodyThenError(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	responseBody := []byte(`{message":wrong api token}`)

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusForbidden, responseBody)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	err := connector.OwnerExists("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.NotErrorAs(t, err, &expectedErr)

	assert.ErrorContains(t, err, "couldn't parse owner response: invalid character 'm' looking for beginning of object key string")
}

func TestDrpcHttpConnectorOwnerInternalServerErrorThenRetryableErr(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	responseBody := []byte(`{"message":"internal error!", "code":500}`)

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusInternalServerError, responseBody)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	err := connector.OwnerExists("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.ErrorAs(t, err, &expectedErr)

	assert.ErrorContains(t, err, `internal server error while getting owner 'id', {"message":"internal error!", "code":500}`)
}

func TestDrpcHttpConnectorOwnerAnyUnexpectedCodeThenErr(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	responseBody := []byte(`{"message":"bad gateway!"}`)

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusBadGateway, responseBody)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	err := connector.OwnerExists("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.NotErrorAs(t, err, &expectedErr)

	assert.ErrorContains(t, err, `unexpected response while getting owner 'id', code and body - 502, {"message":"bad gateway!"}`)
}

func TestDrpcHttpConnectorOwnerRequestErrorThenRetryableErr(t *testing.T) {
	tests := []struct {
		name      string
		responder func()
		err       string
	}{
		{
			name: "ctx cancelled owner",
			responder: func() {
				httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
					time.Sleep(50 * time.Millisecond)
					resp := httpmock.NewBytesResponse(http.StatusOK, nil)
					return resp, nil
				})
			},
			err: `couldn't get owner: couldn't execute request GET http://localhost:8080/nodecore/owners/id: Get "http://localhost:8080/nodecore/owners/id": context deadline exceeded`,
		},
		{
			name: "some other error owner",
			responder: func() {
				httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
					return nil, errors.New("some err")
				})
			},
			err: `couldn't get owner: couldn't execute request GET http://localhost:8080/nodecore/owners/id: Get "http://localhost:8080/nodecore/owners/id": some err`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			httpmock.Activate(te)
			defer httpmock.DeactivateAndReset()

			test.responder()

			connector := getDrpcHttpConnectorWithTimeout(1 * time.Millisecond)
			err := connector.OwnerExists("id", "token")

			var expectedErr *protocol.ClientRetryableError
			assert.ErrorAs(te, err, &expectedErr)

			assert.ErrorContains(te, err, test.err)
		})
	}
}

func TestDrpcHttpConnectorLoadKeysRequestErrorThenRetryableErr(t *testing.T) {
	tests := []struct {
		name      string
		responder func()
		err       string
	}{
		{
			name: "ctx cancelled keys",
			responder: func() {
				httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id/keys", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
					time.Sleep(50 * time.Millisecond)
					resp := httpmock.NewBytesResponse(http.StatusOK, nil)
					return resp, nil
				})
			},
			err: `couldn't load keys: couldn't execute request GET http://localhost:8080/nodecore/owners/id/keys: Get "http://localhost:8080/nodecore/owners/id/keys": context deadline exceeded`,
		},
		{
			name: "some other error keys",
			responder: func() {
				httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id/keys", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
					return nil, errors.New("some err")
				})
			},
			err: `couldn't load keys: couldn't execute request GET http://localhost:8080/nodecore/owners/id/keys: Get "http://localhost:8080/nodecore/owners/id/keys": some err`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			httpmock.Activate(te)
			defer httpmock.DeactivateAndReset()

			test.responder()

			connector := getDrpcHttpConnectorWithTimeout(1 * time.Millisecond)
			keys, err := connector.LoadOwnerKeys("id", "token")

			var expectedErr *protocol.ClientRetryableError
			assert.ErrorAs(te, err, &expectedErr)

			assert.Empty(te, keys)
			assert.ErrorContains(te, err, test.err)
		})
	}
}

func TestDrpcHttpConnectorLoadKeysNoErrors(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	responseBody := []byte(`
		[
			{
				"key_id": "id",
				"ip_whitelist": ["1.1.2", "3.3.3.3"],
				"methods_blacklist": ["method", "method2"],
				"methods_whitelist": ["test", "test1"],
				"contract_whitelist": ["contract1", "contract2"],
				"cors_origins": ["http://localhost:8080"],
				"api_key": "api-key"
			},
			{
				"key_id": "id1",
				"ip_whitelist": ["5.5.5", "6.6.6.6"],
				"methods_blacklist": ["method6", "method26"],
				"methods_whitelist": ["test5", "test51"],
				"contract_whitelist": ["contract221", "contract32"],
				"cors_origins": ["http://test:8080"],
				"api_key": "api-key123"
			}
		]`,
	)

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id/keys", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusOK, responseBody)
		return resp, nil
	})

	connector := getDrpcHttpConnector()

	keys, err := connector.LoadOwnerKeys("id", "token")
	assert.NoError(t, err)

	expected := []*drpc.DrpcKey{
		{
			KeyId:             "id",
			IpWhitelist:       []string{"1.1.2", "3.3.3.3"},
			MethodsBlacklist:  []string{"method", "method2"},
			MethodsWhitelist:  []string{"test", "test1"},
			ContractWhitelist: []string{"contract1", "contract2"},
			CorsOrigins:       []string{"http://localhost:8080"},
			ApiKey:            "api-key",
		},
		{
			KeyId:             "id1",
			IpWhitelist:       []string{"5.5.5", "6.6.6.6"},
			MethodsBlacklist:  []string{"method6", "method26"},
			MethodsWhitelist:  []string{"test5", "test51"},
			ContractWhitelist: []string{"contract221", "contract32"},
			CorsOrigins:       []string{"http://test:8080"},
			ApiKey:            "api-key123",
		},
	}
	assert.Equal(t, expected, keys)
}

func TestDrpcHttpConnectorLoadKeysOwnerNotFoundThenErr(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id/keys", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusNotFound, nil)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	keys, err := connector.LoadOwnerKeys("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.NotErrorAs(t, err, &expectedErr)

	assert.Empty(t, keys)
	assert.ErrorContains(t, err, "owner 'id' not found")
}

func TestDrpcHttpConnectorLoadKeysForbiddenThenError(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	responseBody := []byte(`{"message":"wrong api token", "code":403}`)

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id/keys", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusForbidden, responseBody)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	keys, err := connector.LoadOwnerKeys("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.NotErrorAs(t, err, &expectedErr)

	assert.Empty(t, keys)
	assert.ErrorContains(t, err, "forbidden, owner - 'id', message: wrong api token")
}

func TestDrpcHttpConnectorLoadKeysForbiddenCantParseBodyThenError(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	responseBody := []byte(`{message":wrong api token}`)

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id/keys", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusForbidden, responseBody)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	keys, err := connector.LoadOwnerKeys("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.NotErrorAs(t, err, &expectedErr)

	assert.Empty(t, keys)
	assert.ErrorContains(t, err, "couldn't parse keys response: invalid character 'm' looking for beginning of object key string")
}

func TestDrpcHttpConnectorLoadKeysInternalServerErrorThenRetryableErr(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	responseBody := []byte(`{"message":"internal error!", "code":500}`)

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id/keys", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusInternalServerError, responseBody)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	keys, err := connector.LoadOwnerKeys("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.ErrorAs(t, err, &expectedErr)

	assert.Empty(t, keys)
	assert.ErrorContains(t, err, `internal server error while loading keys of owner 'id', {"message":"internal error!", "code":500}`)
}

func TestDrpcHttpConnectorLoadKeysTooManyRequestsThenError(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id/keys", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusTooManyRequests, nil)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	keys, err := connector.LoadOwnerKeys("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.ErrorAs(t, err, &expectedErr)

	assert.Empty(t, keys)
	assert.ErrorContains(t, err, "too many requests, please try again later")
}

func TestDrpcHttpConnectorLoadKeysAnyUnexpectedCodeThenErr(t *testing.T) {
	httpmock.Activate(t)
	defer httpmock.Deactivate()

	responseBody := []byte(`{"message":"bad gateway!"}`)

	httpmock.RegisterMatcherResponder("GET", "/nodecore/owners/id/keys", httpmock.HeaderIs(drpc.ApiToken, "token"), func(request *http.Request) (*http.Response, error) {
		resp := httpmock.NewBytesResponse(http.StatusBadGateway, responseBody)
		return resp, nil
	})

	connector := getDrpcHttpConnector()
	keys, err := connector.LoadOwnerKeys("id", "token")

	var expectedErr *protocol.ClientRetryableError
	assert.NotErrorAs(t, err, &expectedErr)

	assert.Empty(t, keys)
	assert.ErrorContains(t, err, `unexpected response while loading keys of owner 'id', code and body - 502, {"message":"bad gateway!"}`)
}

func getDrpcHttpConnector() *drpc.SimpleDrpcHttpConnector {
	return drpc.NewSimpleDrpcHttpConnectorWithDefaultClient(
		&config.DrpcIntegrationConfig{
			Url:            "http://localhost:8080",
			RequestTimeout: 10 * time.Second,
		},
	)
}

func getDrpcHttpConnectorWithTimeout(timeout time.Duration) *drpc.SimpleDrpcHttpConnector {
	return drpc.NewSimpleDrpcHttpConnectorWithDefaultClient(
		&config.DrpcIntegrationConfig{
			Url:            "http://localhost:8080",
			RequestTimeout: timeout,
		},
	)
}
