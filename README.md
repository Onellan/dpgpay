# DPG Pay

DPG Pay is a self-hosted, internal ledger-based payment processor. It accepts payer confirmations for payment requests, simulates EFT settlement, and records every movement through an immutable double-entry ledger.

## What DPG Pay is and is not

- Is: an internal operator-focused ledger + wallet engine with payer-facing confirmation pages.
- Is: simulation-ready today, with a clean transfer interface for real banking integration later.
- Is not: a third-party payment gateway integration.
- Is not: card acquiring, token vaulting, or PCI payment processing.

## Stack

- Backend: Go + Chi
- Frontend: HTMX + html/template
- Styling: Tailwind (CDN)
- Database: SQLite via modernc.org/sqlite (pure Go, CGO disabled)
- Runtime: Docker + Docker Compose
- Target: Raspberry Pi ARM64
- Port: 18231

## Project structure

```
/dpg-pay
  /cmd/server/main.go
  /internal
    /handlers
    /ledger
    /wallet
    /transfer
    /models
    /templates
    /middleware
    /notify
  /static
  /migrations
  Dockerfile
  docker-compose.yml
  .env.example
  README.md
  go.mod
  go.sum
```

## Setup

1. Create an env file:

```bash
cp .env.example .env
```

2. Fill required values in `.env`:
- `ADMIN_USERNAME`
- `ADMIN_PASSWORD_BCRYPT`
- `BASE_URL`
- `DB_PATH` (in containers: `/data/dpgpay.db`)
- `EFT_ACCOUNT_NAME`
- `EFT_BANK_NAME`
- `EFT_ACCOUNT_NUMBER`
- `EFT_BRANCH_CODE` (optional)
- `WEBHOOK_ENDPOINT_URL` (optional, enables outbound webhook delivery)
- `WEBHOOK_SIGNING_SECRET` (optional but recommended)

3. Build and run:

```bash
docker compose up -d --build
```

4. Access DPG Pay:

- `http://<pi-ip>:18231`

## DNS setup

To expose via your domain:

1. Create/Update an `A` record pointing to your Raspberry Pi public/static IP.
2. Forward external traffic to host port `18231`.
3. Set `BASE_URL` to your domain URL.

## Generate bcrypt hash for `ADMIN_PASSWORD_BCRYPT`

Use Python:

```bash
python - <<'PY'
import bcrypt
print(bcrypt.hashpw(b"your-strong-password", bcrypt.gensalt()).decode())
PY
```

Or with htpasswd (Apache tools):

```bash
htpasswd -bnBC 12 "" "your-strong-password" | tr -d ':\n' | sed 's/$/\n/'
```

## Simulation mode and real EFT migration

- Keep `EFT_SIMULATION_MODE=true` for simulated transfers.
- When integrating real rails, implement a new `transfer.Rail` adapter in `internal/transfer` and wire it in `cmd/server/main.go`.
- Set `EFT_SIMULATION_MODE=false` once real rail processing is active.

## Backup and restore (SQLite Docker volume)

Backup named volume:

```bash
docker run --rm -v dpgpay_dpgpay_data:/from -v $(pwd):/to alpine sh -c "cp /from/dpgpay.db /to/dpgpay-backup-$(date +%Y%m%d%H%M%S).db"
```

Restore example:

```bash
docker compose down
docker run --rm -v dpgpay_dpgpay_data:/to -v $(pwd):/from alpine sh -c "cp /from/dpgpay-backup.db /to/dpgpay.db"
docker compose up -d
```

Automated backup script (with retention cleanup):

```bash
chmod +x ./scripts/backup_dpgpay.sh
./scripts/backup_dpgpay.sh
```

Suggested cron on Raspberry Pi (every 6 hours):

```bash
0 */6 * * * cd /opt/dpgpay && ./scripts/backup_dpgpay.sh >> /var/log/dpgpay-backup.log 2>&1
```

## Webhook outbox delivery

Set `WEBHOOK_ENDPOINT_URL` to enable outbound event delivery from the built-in outbox dispatcher.

- Delivery is asynchronous and retry-safe.
- Events are HMAC signed in `X-DPGPay-Signature` (`sha256=<hex>`), when `WEBHOOK_SIGNING_SECRET` is set.
- Admin monitoring page: `/admin/webhooks`
- Event headers:
  - `X-DPGPay-Event`
  - `X-DPGPay-Reference`
  - `X-DPGPay-Signature`

## Endpoints

- Admin:
  - `/admin/login`
  - `/admin`
  - `/admin/wallet/{OPERATING|ESCROW|FEE}`
  - `/admin/settlements`
  - `/admin/audit`
- Payer:
  - `/pay/{uuid}`
  - `/pay/{uuid}/status`
  - `/pay/{uuid}/success`
  - `/pay/{uuid}/failed`

## Security controls included

- Session auth for all `/admin/*` routes
- Rate limiting:
  - `/admin/login` (5 attempts/min/IP)
  - `/pay/*` routes
- CSRF protection on POST forms
- Security headers middleware
- Immutable ledger design (no update/delete logic in query layer)

## Future integration point

Real banking/EFT APIs plug into `internal/transfer` by replacing the simulation rail implementation with a real rail adapter while keeping handlers and ledger logic unchanged.
