# Build stage
FROM golang:1.26.0-alpine AS builder
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

# Build the application
RUN make build

# Final stage
FROM alpine:3.18
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
