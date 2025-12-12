package drpc

import (
	"context"
	"encoding/json"
	"errors"
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
	response, closeBodyFunc, err := s.makeRequest(http.MethodGet, apiToken, s.baseUrl+fmt.Sprintf("/nodecore/owners/%s", ownerId), nil)
	if err != nil {
		return fmt.Errorf("couldn't get owner: %w", err)
	}
	defer closeBodyFunc()

	switch response.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("owner '%s' not found", ownerId)
	case http.StatusTooManyRequests:
		return protocol.NewClientRetryableError(errors.New("too many requests, please try again later"))
	case http.StatusForbidden:
		var jsonErr jsonError
		err = json.NewDecoder(response.Body).Decode(&jsonErr)
		if err != nil {
			return fmt.Errorf("couldn't parse owner response: %w", err)
		}
		return fmt.Errorf("forbidden, owner - '%s', message: %s", ownerId, jsonErr.Message)
	default:
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return fmt.Errorf("couldn't read owner response: %w", err)
		}
		if response.StatusCode == http.StatusInternalServerError {
			return protocol.NewClientRetryableError(fmt.Errorf("internal server error while getting owner '%s', %s", ownerId, string(body)))
		} else {
			return fmt.Errorf("unexpected response while getting owner '%s', code and body - %d, %s", ownerId, response.StatusCode, string(body))
		}

	}
}

func (s *SimpleDrpcHttpConnector) LoadOwnerKeys(ownerId, apiToken string) ([]*DrpcKey, error) {
	response, closeBodyFunc, err := s.makeRequest(http.MethodGet, apiToken, s.baseUrl+fmt.Sprintf("/nodecore/owners/%s/keys", ownerId), nil)
	if err != nil {
		return nil, fmt.Errorf("couldn't load keys: %w", err)
	}
	defer closeBodyFunc()

	switch response.StatusCode {
	case http.StatusNotFound:
		return nil, fmt.Errorf("owner '%s' not found", ownerId)
	case http.StatusTooManyRequests:
		return nil, protocol.NewClientRetryableError(errors.New("too many requests, please try again later"))
	case http.StatusForbidden:
		var jsonErr jsonError
		err = json.NewDecoder(response.Body).Decode(&jsonErr)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse keys response: %w", err)
		}
		return nil, fmt.Errorf("forbidden, owner - '%s', message: %s", ownerId, jsonErr.Message)
	case http.StatusOK:
		var keys []*DrpcKey
		err = json.NewDecoder(response.Body).Decode(&keys)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse keys response: %w", err)
		}
		return keys, nil
	default:
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, fmt.Errorf("couldn't read keys response: %w", err)
		}
		if response.StatusCode == http.StatusInternalServerError {
			return nil, protocol.NewClientRetryableError(fmt.Errorf("internal server error while loading keys of owner '%s', %s", ownerId, string(body)))
		} else {
			return nil, fmt.Errorf("unexpected response while loading keys of owner '%s', code and body - %d, %s", ownerId, response.StatusCode, string(body))
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
