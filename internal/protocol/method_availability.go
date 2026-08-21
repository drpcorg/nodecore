package protocol

import (
	"regexp"

	"google.golang.org/grpc/codes"
)

// MethodAvailability is what an upstream's error says about whether a method exists on
// that upstream. It is three-valued on purpose: "no opinion" has to be distinguishable
// from "definitely absent", so that a transient failure is never mistaken for a
// missing method.
type MethodAvailability int

const (
	// MethodAvailabilityUnknown - the error says nothing about the method's existence.
	MethodAvailabilityUnknown MethodAvailability = iota
	// MethodNotAvailable - the upstream does not have this method.
	MethodNotAvailable
	// MethodAvailable - the upstream has the method and rejected this particular call,
	// typically because the params were wrong.
	MethodAvailable
)

// methodNotAvailablePatterns are the client error messages that mean the method is
// absent. Kept identical to the set MethodBanHook used before this moved here, so ban
// behaviour is unchanged.
var methodNotAvailablePatterns = []*regexp.Regexp{
	regexp.MustCompile(`method ([A-Za-z0-9_]+) does not exist/is not available`),
	regexp.MustCompile(`([A-Za-z0-9_]+) found but the containing module is disabled`),
	regexp.MustCompile(`[Mm]ethod not found`),
	regexp.MustCompile(`[Mm]ethod is not available`),
	regexp.MustCompile(`The method ([A-Za-z0-9_]+) is not available`),
}

// methodAvailablePatterns are complaints about the call rather than about the method:
// the upstream had to have the method in order to reject its arguments.
var methodAvailablePatterns = []*regexp.Regexp{
	regexp.MustCompile(`missing value for required argument ([0-9]+)`),
	regexp.MustCompile(`Invalid params`),
}

// ClassifyMethodAvailability decides what an upstream error says about whether a method
// exists. Not-available patterns are checked before the params ones: an unavailable
// method is the answer that matters, and a params complaint only ever means the method
// is there.
func ClassifyMethodAvailability(respError *ResponseError) MethodAvailability {
	if respError == nil {
		return MethodAvailabilityUnknown
	}
	// gRPC statuses form a closed code model, so they classify on the code
	// alone: UNIMPLEMENTED is the wire's own "method absent". The message
	// patterns below are JSON-RPC vocabulary and must not run against them.
	if grpcStatus, ok := GrpcStatusFromError(respError); ok {
		switch grpcStatus.Code {
		case codes.Unimplemented:
			return MethodNotAvailable
		case codes.InvalidArgument:
			return MethodAvailable
		default:
			return MethodAvailabilityUnknown
		}
	}
	if respError.Code == NoSupportedMethod {
		return MethodNotAvailable
	}
	for _, pattern := range methodNotAvailablePatterns {
		if pattern.MatchString(respError.Message) {
			return MethodNotAvailable
		}
	}
	for _, pattern := range methodAvailablePatterns {
		if pattern.MatchString(respError.Message) {
			return MethodAvailable
		}
	}
	return MethodAvailabilityUnknown
}
