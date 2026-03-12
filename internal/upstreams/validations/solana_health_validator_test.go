package validations_test

import (
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func matchSolanaGetHealthRequest(request protocol.RequestHolder) bool {
	body, err := request.Body()
	if err != nil {
		return false
	}

	return request.Method() == "getHealth" &&
		request.Id() == "1" &&
		request.RequestType() == protocol.JsonRpc &&
		strings.Contains(string(body), `"method":"getHealth"`) &&
		strings.Contains(string(body), `"params":null`)
}

func TestSolanaHealthValidatorValidateReturnsAvailableWhenHealthIsOk(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSolanaGetHealthRequest)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"ok"`), protocol.JsonRpc)).
		Once()

	validator := validations.NewSolanaHealthValidator("id", connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Available, status)
	connector.AssertExpectations(t)
}

func TestSolanaHealthValidatorValidateReturnsUnavailableWhenConnectorReturnsError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSolanaGetHealthRequest)).
		Return(protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	validator := validations.NewSolanaHealthValidator("id", connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Unavailable, status)
	connector.AssertExpectations(t)
}

func TestSolanaHealthValidatorValidateReturnsUnavailableWhenResponseIsNotString(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSolanaGetHealthRequest)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"status":"ok"}`), protocol.JsonRpc)).
		Once()

	validator := validations.NewSolanaHealthValidator("id", connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Unavailable, status)
	connector.AssertExpectations(t)
}

func TestSolanaHealthValidatorValidateReturnsUnavailableWhenHealthIsNotOk(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSolanaGetHealthRequest)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"behind"`), protocol.JsonRpc)).
		Once()

	validator := validations.NewSolanaHealthValidator("id", connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Unavailable, status)
	connector.AssertExpectations(t)
}

func TestSolanaHealthValidatorValidateReturnsUnavailableWhenHealthCaseDoesNotMatch(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSolanaGetHealthRequest)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"OK"`), protocol.JsonRpc)).
		Once()

	validator := validations.NewSolanaHealthValidator("id", connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Unavailable, status)
	connector.AssertExpectations(t)
}
