# release-watcher

OpenFaaS function that monitors GitHub releases and posts notifications to Discord when new versions are published.

## Features

- Polls GitHub API for the latest release of a configured repository
- Stores state in PostgreSQL to avoid duplicate notifications
- Posts rich Discord embeds with release details
- Runs on a schedule via OpenFaaS cron-connector (default: daily at midnight UTC)
- Supports GitHub authentication to avoid rate-limiting

## Architecture

```
cron-connector (daily at midnight UTC)
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
- (Optional) GitHub Personal Access Token for higher rate limits

## Deploy

The function is configured via environment variables and secrets. No need to edit `stack.yaml` directly.

### Secrets

Create the following secrets before deploying:

```bash
# 1. Discord webhook URL
faas-cli secret create discord-webhook-url \
  --from-literal "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_WEBHOOK_TOKEN"

# 2. GitHub token (optional but recommended)
faas-cli secret create github-token \
  --from-literal "ghp_YOUR_GITHUB_TOKEN"

# 3. PostgreSQL connection string (URL-encoded password)
faas-cli secret create pg-conn \
  --from-literal "postgresql://USER:PASSWORD@host:5432/dbname"
```

### Environment Variables

The function uses environment variable substitution. Set these when deploying:

| Variable | Default | Description |
|----------|---------|-------------|
| `WATCH_REPO` | `openfaas/faas` | GitHub repository to watch (format: `owner/repo`) |
| `CRON_SCHEDULE` | `0 0 * * *` | Cron schedule (daily at midnight UTC) |
| `REGISTRY` | `ttl.sh` | Container registry |
| `OWNER` | `openfaas-fn` | Registry namespace/owner |

Override at deploy time:

```bash
WATCH_REPO=openfaas/faas REGISTRY=ghcr.io OWNER=youruser faas-cli up -f stack.yaml --tag=digest
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
- **GitHub rate-limiting**: Add a `github-token` secret (see [Deploy](#deploy))
- **Discord errors**: Verify the `discord-webhook-url` secret is correct

## License

MIT
