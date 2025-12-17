package drpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
)

const ApiToken = "X-Api-Token"

type jsonError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type DrpcHttpConnector interface {
	OwnerExists(ownerId, apiToken string) error
	LoadOwnerKeys(ownerId, apiToken string) ([]*DrpcKey, error)
}

type SimpleDrpcHttpConnector struct {
	client         *http.Client
	baseUrl        string
	requestTimeout time.Duration
}

func NewSimpleDrpcHttpConnectorWithDefaultClient(drpcIntegration *config.DrpcIntegrationConfig) *SimpleDrpcHttpConnector {
	return &SimpleDrpcHttpConnector{
		client:         http.DefaultClient,
		baseUrl:        drpcIntegration.Url,
		requestTimeout: drpcIntegration.RequestTimeout,
	}
}

func NewSimpleDrpcHttpConnector(drpcIntegration *config.DrpcIntegrationConfig) *SimpleDrpcHttpConnector {
	httpClient := &http.Client{
		Transport: utils.DefaultHttpTransport(),
		Timeout:   60 * time.Second,
	}

	return &SimpleDrpcHttpConnector{
		client:         httpClient,
		baseUrl:        drpcIntegration.Url,
		requestTimeout: drpcIntegration.RequestTimeout,
	}
}

func (s *SimpleDrpcHttpConnector) OwnerExists(ownerId, apiToken string) error {
	path := fmt.Sprintf("/nodecore/owners/%s", ownerId)
	response, closeBodyFunc, err := s.makeRequest(http.MethodGet, apiToken, s.baseUrl+path, nil)
	if err != nil {
		return fmt.Errorf("couldn't get owner: %w", err)
	}
	defer closeBodyFunc()

	if response.StatusCode == http.StatusOK {
		return nil
	} else {
		return handleStatusCodes(response.StatusCode, ownerId, path, response.Body)
	}
}

func (s *SimpleDrpcHttpConnector) LoadOwnerKeys(ownerId, apiToken string) ([]*DrpcKey, error) {
	path := fmt.Sprintf("/nodecore/owners/%s/keys", ownerId)
	response, closeBodyFunc, err := s.makeRequest(http.MethodGet, apiToken, s.baseUrl+path, nil)
	if err != nil {
		return nil, fmt.Errorf("couldn't load keys: %w", err)
	}
	defer closeBodyFunc()

	if response.StatusCode == http.StatusOK {
		var keys []*DrpcKey
		err = json.NewDecoder(response.Body).Decode(&keys)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse keys response: %w", err)
		}
		return keys, nil
	} else {
		return nil, handleStatusCodes(response.StatusCode, ownerId, path, response.Body)
	}
}

func handleStatusCodes(statusCode int, ownerId, path string, responseBody io.ReadCloser) error {
	switch statusCode {
	case http.StatusNotFound:
		return fmt.Errorf("owner '%s' not found", ownerId)
	case http.StatusTooManyRequests:
		return protocol.NewClientRetryableError(fmt.Errorf("%s, too many requests, please try again later", path))
	case http.StatusForbidden:
		var jsonErr jsonError
		err := json.NewDecoder(responseBody).Decode(&jsonErr)
		if err != nil {
			return fmt.Errorf("%s, couldn't parse forbidden response: %w", path, err)
		}
		return fmt.Errorf("%s forbidden, owner - '%s', message: %s", path, ownerId, jsonErr.Message)
	default:
		body, err := io.ReadAll(responseBody)
		if err != nil {
			return fmt.Errorf("%s, couldn't read a response: %w", path, err)
		}
		if statusCode == http.StatusInternalServerError {
			return protocol.NewClientRetryableError(fmt.Errorf("%s, internal server, owner '%s', body - %s", path, ownerId, string(body)))
		} else {
			return fmt.Errorf("%s, unexpected response, owner '%s', code and body - %d, %s", path, ownerId, statusCode, string(body))
		}
	}
}

func (s *SimpleDrpcHttpConnector) makeRequest(method, apiToken, url string, body io.Reader) (*http.Response, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, nil, fmt.Errorf("error making request %s %s: %w", method, url, err)
	}
	request.Header.Set(ApiToken, apiToken)

	response, err := s.client.Do(request)
	if err != nil {
		return nil, nil, protocol.NewClientRetryableError(fmt.Errorf("couldn't execute request %s %s: %w", method, url, err))
	}
	return response, func() { utils.CloseBodyReader(ctx, response.Body) }, nil
}
