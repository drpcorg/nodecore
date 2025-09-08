package utils

import (
	"context"
	"net"
	"net/http"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
)

type contextKey string

const (
	ipKey contextKey = "ip"
)

func ContextWithIps(ctx context.Context, request *http.Request) context.Context {
	ips := request.Header.Get("X-Forwarded-For")
	ipValues := mapset.NewThreadUnsafeSet[string]()

	if ips != "" {
		for _, ip := range strings.Split(ips, ",") {
			ipValues.Add(ip)
		}
	}
	if ipValues.IsEmpty() {
		remoteIP, _, err := net.SplitHostPort(request.RemoteAddr)
		if err != nil {
			ipValues.Add("127.0.0.1")
		} else {
			ipValues.Add(remoteIP)
		}
	}

	ctx = context.WithValue(ctx, ipKey, ipValues)

	return ctx
}

func IpsFromContext(ctx context.Context) mapset.Set[string] {
	ips, ok := ctx.Value(ipKey).(mapset.Set[string])
	if !ok {
		return nil
	}
	return ips
}
