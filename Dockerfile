FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /geth-triage .
RUN CGO_ENABLED=0 go build -o /batch-init ./cmd/batch

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /geth-triage /usr/local/bin/geth-triage
COPY --from=builder /batch-init /usr/local/bin/batch-init

RUN mkdir -p /data
VOLUME /data

EXPOSE 8443

ENTRYPOINT ["geth-triage"]
