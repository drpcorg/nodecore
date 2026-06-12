package health_server

import (
	"net/http"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/labstack/echo/v4"
)

type chainStatus struct {
	Chain  string `json:"chain"`
	Status string `json:"status"`
}

type statusResponse struct {
	Ready  bool          `json:"ready"`
	Chains []chainStatus `json:"chains"`
}

func NewHealthServer(supervisor upstreams.UpstreamSupervisor) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.GET("/health", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
	e.GET("/ready", func(c echo.Context) error {
		if isReady(supervisor) {
			return c.NoContent(http.StatusOK)
		}
		return c.NoContent(http.StatusServiceUnavailable)
	})
	e.GET("/status", func(c echo.Context) error { return c.JSON(http.StatusOK, buildStatus(supervisor)) })
	return e
}

func isReady(supervisor upstreams.UpstreamSupervisor) bool {
	if supervisor == nil {
		return false
	}
	for _, chain := range supervisor.GetChainSupervisors() {
		if chain.GetChainState().Status == protocol.Available {
			return true
		}
	}
	return false
}

func buildStatus(supervisor upstreams.UpstreamSupervisor) statusResponse {
	resp := statusResponse{Ready: isReady(supervisor)}
	if supervisor == nil {
		return resp
	}
	for _, chain := range supervisor.GetChainSupervisors() {
		state := chain.GetChainState()
		resp.Chains = append(resp.Chains, chainStatus{Chain: chain.GetChain().String(), Status: state.Status.String()})
	}
	return resp
}
