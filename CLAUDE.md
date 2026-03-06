# Geth Triage

AI-powered PR triage service for the go-ethereum repository.

## Running the Service

Use the systemd user service, not `run.sh` or direct binary execution:

```bash
# Rebuild and restart
go build -o geth-triage . && systemctl --user restart geth-triage

# Logs
journalctl --user -u geth-triage -f

# Status
systemctl --user status geth-triage
```

Service unit: `~/.config/systemd/user/geth-triage.service`

After editing the unit file: `systemctl --user daemon-reload`

## Frontend

Frontend code is at `~/geth-review-roulette`. It runs on Vercel — just `git push` and it deploys as serverless.

## Build & Test

```bash
go build ./...    # build all packages
go vet ./...      # static analysis
go run ./cmd/test <pr-number>  # test single PR analysis
```
