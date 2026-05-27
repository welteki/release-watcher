# release-watcher

OpenFaaS function that monitors GitHub releases and posts notifications to Discord when new versions are published.

## Features

- Polls GitHub for the latest release of a configured repository
- Stores state in PostgreSQL to avoid duplicate notifications
- Posts latest release url to Discord
- Runs on a schedule via OpenFaaS cron-connector (default: 3 times per day)

## Architecture

```
cron-connector (3x per day)
       │
       ▼
release-watcher
       │
       ├─► GitHub  →  fetch latest release url
       │
       ├─► Postgres    →  read/write last-seen release tag
       │
       └─► Discord Webhook  →  POST notification (only on new release)
```

## Prerequisites

- OpenFaaS (faasd or Kubernetes)
- PostgreSQL database
- Discord webhook URL

## Deploy

The function is configured via environment variables and secrets. No need to edit `stack.yaml` directly.

### Secrets

Create the following secrets before deploying:

```bash
# 1. Discord webhook URL
faas-cli secret create discord-webhook-url \
  --from-literal "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_WEBHOOK_TOKEN"

# 2. PostgreSQL password
faas-cli secret create postgres-passwd \
  --from-literal "your-postgres-password"
```

### Environment Variables

The function uses environment variable substitution. Set these when deploying:

| Variable | Default | Description |
|----------|---------|-------------|
| `WATCH_REPO` | `openfaas/faas` | GitHub repository to watch (format: `owner/repo`) |
| `CRON_SCHEDULE` | `0 */8 * * *` | Cron schedule (every 8 hours: midnight, 8am, 4pm UTC) |
| `REGISTRY` | `ttl.sh` | Container registry |
| `OWNER` | `openfaas-fn` | Registry namespace/owner |
| `POSTGRES_HOST` | `postgresql` | PostgreSQL host |
| `POSTGRES_PORT` | `5432` | PostgreSQL port |
| `POSTGRES_USER` | `postgres` | PostgreSQL user |
| `POSTGRES_DB` | `postgres` | PostgreSQL database name |

Override at deploy time:

```bash
WATCH_REPO=kubernetes/kubernetes REGISTRY=ghcr.io OWNER=youruser \
  faas-cli up -f stack.yaml --tag=digest
```

Use [crontab.guru](https://crontab.guru) to validate cron expressions.

## Testing

Invoke the function manually to test:

```bash
curl http://gateway:8080/function/release-watcher
```

The function will check for new releases and post to Discord if one is found.

## Troubleshooting

Check the function logs:

```bash
faas-cli logs release-watcher
```

Common issues:
- **Postgres connection errors**: Verify the `postgres-passwd` secret and environment variables (`POSTGRES_HOST`, `POSTGRES_USER`, etc.) are correct
- **Discord errors**: Verify the `discord-webhook-url` secret is correct

## License

MIT
