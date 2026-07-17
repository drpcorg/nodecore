# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder
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
    go build -ldflags "-X github.com/drpcorg/nodecore/internal/buildinfo.Version=${VERSION} -X github.com/drpcorg/nodecore/internal/buildinfo.GitSHA=${GIT_SHA}" \
    -o /app/nodecore cmd/nodecore/main.go

# Final stage
FROM alpine:3.24
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/nodecore .
COPY nodecore-default.yml nodecore.yml

# Create non-root user
RUN addgroup -g 1001 -S nodecore && \
    adduser -S nodecore -u 1001 -G nodecore

USER nodecore

EXPOSE 8080
CMD ["./nodecore"]
