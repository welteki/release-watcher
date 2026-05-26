# release-watcher

OpenFaaS function that monitors GitHub releases and posts notifications to Discord when new versions are published.

## Features

- Polls GitHub API for the latest release of a configured repository
- Stores state in PostgreSQL to avoid duplicate notifications
- Posts rich Discord embeds with release details
- Runs on a schedule via OpenFaaS cron-connector (default: 3 times per day)

## Architecture

```
cron-connector (3x per day)
       │
       ▼
release-watcher (Python)
       │
       ├─► GitHub API  →  fetch latest release
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

# 2. PostgreSQL connection string
faas-cli secret create pg-conn \
  --from-literal "postgresql://USER:PASSWORD@host:5432/dbname"
```

### Environment Variables

The function uses environment variable substitution. Set these when deploying:

| Variable | Default | Description |
|----------|---------|-------------|
| `WATCH_REPO` | `openfaas/faas` | GitHub repository to watch (format: `owner/repo`) |
| `CRON_SCHEDULE` | `0 */8 * * *` | Cron schedule (every 8 hours: midnight, 8am, 4pm UTC) |
| `REGISTRY` | `ttl.sh` | Container registry |
| `OWNER` | `openfaas-fn` | Registry namespace/owner |

Override at deploy time:

```bash
WATCH_REPO=openfaas/faas REGISTRY=ghcr.io OWNER=youruser \
  faas-cli up -f stack.yaml --tag=digest
```

Use [crontab.guru](https://crontab.guru) to validate cron expressions.

## Testing

Invoke the function manually to test:

```bash
curl -s http://gateway:8080/function/release-watcher | jq
```

The response shows whether a notification was sent:

```json
{
  "repo": "openfaas/faas",
  "latest_tag": "0.27.13",
  "previous_tag": "0.27.13",
  "notified": false
}
```

- `notified: true` — new release detected, Discord notification sent
- `notified: false` — no new release since last check

## Troubleshooting

Check the function logs:

```bash
faas-cli logs release-watcher
```

If the function returns an error, the response body will contain details:

```json
{
  "error": "Postgres error: ..."
}
```

Common issues:
- **Postgres connection errors**: Verify the `pg-conn` secret is correct
- **Discord errors**: Verify the `discord-webhook-url` secret is correct

## License

MIT
