package protocol

import (
	"context"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
)

type UpstreamRestRequest struct {
}

func NewUpstreamRestRequest() *UpstreamRestRequest {
	return &UpstreamRestRequest{}
}

func (u *UpstreamRestRequest) ModifyParams(ctx context.Context, newValue any) {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) SpecMethod() *specs.Method {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) Id() string {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) Method() string {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) Headers() map[string]string {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) Body() ([]byte, error) {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) ParseParams(ctx context.Context) specs.MethodParam {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) IsStream() bool {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) IsSubscribe() bool {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) RequestType() RequestType {
	//TODO implement me
	panic("implement me")
}

func (u *UpstreamRestRequest) RequestHash() string {
	//TODO implement me
	panic("implement me")
}

var _ RequestHolder = (*UpstreamRestRequest)(nil)
