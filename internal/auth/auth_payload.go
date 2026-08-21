package auth

import (
	"net/http"

	"google.golang.org/grpc/metadata"
)

type AuthPayload interface {
	payload()
}

type HttpAuthPayload struct {
	httpRequest *http.Request
}

func NewHttpAuthPayload(httpRequest *http.Request) *HttpAuthPayload {
	return &HttpAuthPayload{
		httpRequest: httpRequest,
	}
}

func (h *HttpAuthPayload) payload() {}

type GrpcAuthPayload struct {
	md metadata.MD
}

func NewGrpcAuthPayload(md metadata.MD) *GrpcAuthPayload {
	return &GrpcAuthPayload{
		md: md,
	}
}

func (h *GrpcAuthPayload) payload() {}
