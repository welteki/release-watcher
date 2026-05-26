import os
import json
import requests
import psycopg


def read_secret(name):
    try:
        with open(f"/var/openfaas/secrets/{name}") as f:
            lines = [l for l in f.read().splitlines() if l.strip() and not l.strip().startswith("#")]
            return lines[0].strip() if lines else ""
    except FileNotFoundError:
        return ""


def get_db_conn(dsn):
    return psycopg.connect(dsn)


def ensure_table(conn):
    with conn.cursor() as cur:
        cur.execute("""
            CREATE TABLE IF NOT EXISTS release_state (
                repo       TEXT PRIMARY KEY,
                last_tag   TEXT NOT NULL,
                updated_at TIMESTAMPTZ DEFAULT now()
            )
        """)
    conn.commit()


def get_last_tag(conn, repo):
    with conn.cursor() as cur:
        cur.execute("SELECT last_tag FROM release_state WHERE repo = %s", (repo,))
        row = cur.fetchone()
        return row[0] if row else None


def upsert_last_tag(conn, repo, tag):
    with conn.cursor() as cur:
        cur.execute("""
            INSERT INTO release_state (repo, last_tag, updated_at)
            VALUES (%s, %s, now())
            ON CONFLICT (repo) DO UPDATE
                SET last_tag   = EXCLUDED.last_tag,
                    updated_at = now()
        """, (repo, tag))
    conn.commit()


def fetch_latest_release(repo):
    url = f"https://api.github.com/repos/{repo}/releases/latest"
    headers = {"Accept": "application/vnd.github+json"}
    resp = requests.get(url, headers=headers, timeout=10)
    resp.raise_for_status()
    return resp.json()


def post_discord(webhook_url, repo, release):
    tag = release.get("tag_name", "unknown")
    html_url = release.get("html_url", "")
    body = release.get("body") or ""
    description = body[:300] + ("…" if len(body) > 300 else "")

    payload = {
        "embeds": [
            {
                "title": f"New release: {repo} {tag}",
                "description": description or "_No release notes provided._",
                "url": html_url,
                "color": 0x3CB371,
                "footer": {"text": "release-watcher · OpenFaaS"},
            }
        ]
    }
    resp = requests.post(webhook_url, json=payload, timeout=10)
    resp.raise_for_status()


def handle(event, context):
    repo = os.getenv("WATCH_REPO", "openfaas/faas")
    discord_webhook_url = read_secret("discord-webhook-url")
    pg_conn_string = read_secret("pg-conn")

    release = fetch_latest_release(repo)
    except requests.HTTPError as e:
        return {
            "statusCode": 502,
            "body": json.dumps({"error": f"GitHub API error: {e}"}),
            "headers": {"Content-Type": "application/json"},
        }

    latest_tag = release.get("tag_name")
    if not latest_tag:
        return {
            "statusCode": 502,
            "body": json.dumps({"error": "GitHub response missing tag_name"}),
            "headers": {"Content-Type": "application/json"},
        }

    # Compare against stored state
    try:
        conn = get_db_conn(pg_conn_str)
        ensure_table(conn)
        last_tag = get_last_tag(conn, repo)
    except Exception as e:
        return {
            "statusCode": 500,
            "body": json.dumps({"error": f"Postgres error: {e}"}),
            "headers": {"Content-Type": "application/json"},
        }

    notified = False
    if latest_tag != last_tag:
        try:
            post_discord(discord_webhook_url, repo, release)
        except requests.HTTPError as e:
            conn.close()
            return {
                "statusCode": 502,
                "body": json.dumps({"error": f"Discord webhook error: {e}"}),
                "headers": {"Content-Type": "application/json"},
            }
        upsert_last_tag(conn, repo, latest_tag)
        notified = True

    conn.close()

    return {
        "statusCode": 200,
        "body": json.dumps({
            "repo": repo,
            "latest_tag": latest_tag,
            "previous_tag": last_tag,
            "notified": notified,
        }),
        "headers": {"Content-Type": "application/json"},
    }
