FROM golang:1.27.1-alpine AS builder
WORKDIR /src
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /mocknode main.go websocket.go

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /mocknode /mocknode
EXPOSE 8545
ENTRYPOINT ["/mocknode"]
