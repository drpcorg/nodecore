package grpc_ingress

import (
	"google.golang.org/grpc/encoding"
	_ "google.golang.org/grpc/encoding/proto" // registers the default proto codec
	"google.golang.org/grpc/mem"
)

// rawFrame is the chain ingress's private message type: a gRPC message kept
// as raw bytes, never parsed. The delegating codec dispatches on it - the
// value's type says with certainty which world a message belongs to, because
// generated service code always passes proto structs to RecvMsg/SendMsg
// while the catch-all always passes *rawFrame.
type rawFrame struct {
	data []byte
}

// delegatingCodec exists because a grpc.Server has exactly one server-wide
// codec, and the chain-ingress server hosts two worlds: the reflection
// service (generated code that needs the normal proto codec) and the
// catch-all (which needs raw bytes). The codec wraps the registered proto
// CodecV2 instance grpc-go uses by default, so the generated path - internal
// buffer pooling included - is preserved unchanged behind a single type
// assertion. The dshackle server never sees this codec: it runs as a
// separate grpc.Server on its own port.
//
// Known semantic edge: forcing a server codec disables grpc-go's
// per-content-subtype codec lookup - every call on this server is treated as
// proto unless it is our raw type. gzip compression is unaffected
// (compressors are a separate registry from codecs).
type delegatingCodec struct {
	protoCodec encoding.CodecV2
}

func newDelegatingCodec() delegatingCodec {
	return delegatingCodec{protoCodec: encoding.GetCodecV2("proto")}
}

func (c delegatingCodec) Marshal(v any) (mem.BufferSlice, error) {
	if frame, ok := v.(*rawFrame); ok {
		return mem.BufferSlice{mem.SliceBuffer(frame.data)}, nil
	}
	return c.protoCodec.Marshal(v)
}

func (c delegatingCodec) Unmarshal(data mem.BufferSlice, v any) error {
	if frame, ok := v.(*rawFrame); ok {
		// Materialize copies out of the transport's ref-counted buffers, so the
		// frame stays valid after the transport recycles them.
		frame.data = data.Materialize()
		return nil
	}
	return c.protoCodec.Unmarshal(data, v)
}

func (c delegatingCodec) Name() string {
	return c.protoCodec.Name()
}

var _ encoding.CodecV2 = delegatingCodec{}
