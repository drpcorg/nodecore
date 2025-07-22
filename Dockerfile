FROM golang:1.24.1-bullseye AS base
WORKDIR /go/src/github.com/drpcorg/dsheltie
COPY go.* .
RUN go mod download -x
COPY pkg ./pkg
COPY cmd ./cmd
COPY internal ./internal
COPY Makefile .
RUN GOOS=linux make build

FROM debian:bullseye
COPY --from=base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=base /go/src/github.com/drpcorg/dsheltie/dsheltie /dsheltie
CMD [ "/dsheltie" ]
