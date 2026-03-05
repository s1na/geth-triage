#!/bin/bash
# .env is loaded automatically by the binary
export DB_PATH=./data/geth-triage.db
export LISTEN_ADDR=:8443
export HTTP_LISTEN_ADDR=:8080
export TLS_CERT=./data/tls/cert.pem
export TLS_KEY=./data/tls/key.pem
exec ./geth-triage
