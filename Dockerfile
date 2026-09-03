# Build stage
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS builder
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
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/nodecore .
COPY nodecore-default.yml nodecore.yml

EXPOSE 8080
CMD ["/app/nodecore"]
