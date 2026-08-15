# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder
WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY pkg/ ./pkg/
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY Makefile ./

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GIT_SHA=
# Generate code on the build platform, then cross-compile for the target
# platform. This avoids slow QEMU-emulated Go builds for multi-arch images.
RUN go run cmd/chains/init_chains.go && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-$(go env GOARCH)} \
    go build -trimpath -ldflags "-s -w -X github.com/drpcorg/nodecore/internal/buildinfo.Version=${VERSION} -X github.com/drpcorg/nodecore/internal/buildinfo.GitSHA=${GIT_SHA}" \
    -o /app/nodecore cmd/nodecore/main.go

# Final stage
# distroless/static ships CA certificates, tzdata and a built-in nonroot user (uid 65532)
FROM gcr.io/distroless/static-debian12:nonroot@sha256:1b7b9f0f0e0a1d2155f531db587cc48ec26aaf97ab64364225f5bf18a054e66a
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/nodecore .
COPY nodecore-default.yml nodecore.yml

EXPOSE 8080
CMD ["/app/nodecore"]
