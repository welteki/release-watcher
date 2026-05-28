package function

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

const userAgent = "release-monitor"

var (
	repo              string
	discordWebhookURL string
	db                *sql.DB
	githubClient      *http.Client
)

func init() {
	repo = getEnv("watch_repo", "openfaas/faas")
	log.Printf("Watching repository: %s", repo)
	discordWebhookURL = readSecret("discord-webhook-url")
	postgresPassword := readSecret("postgres-passwd")

	// Build Postgres connection string from environment variables
	pgHost := getEnv("postgres_host", "postgresql")
	pgPort := getEnv("postgres_port", "5432")
	pgUser := getEnv("postgres_user", "postgres")
	pgDB := getEnv("postgres_db", "postgres")
	pgConnStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		pgHost, pgPort, pgUser, postgresPassword, pgDB)

	// Initialize connection pool
	var err error
	db, err = sql.Open("postgres", pgConnStr)
	if err != nil {
		log.Fatalf("Error connecting to database during init: %v", err)
	}

	// Set connection pool limits to prevent connection exhaustion attacks
	db.SetMaxOpenConns(2)            // Max 2 concurrent connections
	db.SetMaxIdleConns(1)            // Keep 1 idle connection ready
	db.SetConnMaxLifetime(time.Hour) // Recycle connections after 1 hour

	// Ensure table exists on startup
	if err := ensureTable(db); err != nil {
		log.Fatalf("Error creating table during init: %v", err)
	}

	// Initialize HTTP client for GitHub (no redirect following)
	githubClient = &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type DiscordWebhook struct {
	Content string `json:"content"`
}

func Handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Fetch latest releaseURL url from GitHub
	releaseURL, err := fetchLatestReleaseURL(ctx, repo)
	if err != nil {
		log.Printf("Error fetching release: %v", err)
		http.Error(w, fmt.Sprintf("Error fetching release: %v", err), http.StatusInternalServerError)
		return
	}

	tagName := releaseURL[strings.LastIndex(releaseURL, "/")+1:]

	// Get last seen tag from database
	lastTag, err := getLastTag(db, repo)
	if err != nil {
		log.Printf("Error getting last tag: %v", err)
		http.Error(w, fmt.Sprintf("Database query error: %v", err), http.StatusInternalServerError)
		return
	}

	if lastTag == tagName {
		msg := fmt.Sprintf("No new release for %s (current: %s)", repo, tagName)
		log.Println(msg)
		w.Write([]byte(msg))
		return
	}

	// Post to Discord
	if err := postToDiscord(ctx, discordWebhookURL, releaseURL); err != nil {
		log.Printf("Error posting to Discord: %v", err)
		http.Error(w, fmt.Sprintf("Discord webhook error: %v", err), http.StatusInternalServerError)
		return
	}

	// Update last seen tag
	if err := updateLastTag(db, repo, tagName); err != nil {
		log.Printf("Error updating last tag: %v", err)
		http.Error(w, fmt.Sprintf("Database update error: %v", err), http.StatusInternalServerError)
		return
	}

	msg := fmt.Sprintf("Posted new release %s for %s to Discord", tagName, repo)
	log.Println(msg)
	w.Write([]byte(msg))
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func readSecret(name string) string {
	data, err := os.ReadFile(fmt.Sprintf("/var/openfaas/secrets/%s", name))
	if err != nil {
		log.Printf("Warning: could not read secret %s: %v", name, err)
		return ""
	}
	return string(bytes.TrimSpace(data))
}

func fetchLatestReleaseURL(ctx context.Context, repo string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	res, err := githubClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusFound {
		io.Copy(io.Discard, res.Body)
		return "", fmt.Errorf("GitHub returned uunexpected status: %d", res.StatusCode)
	}

	releaseURL := res.Header.Get("Location")
	if len(releaseURL) == 0 {
		return "", fmt.Errorf("unable to determine latest release")
	}

	return releaseURL, nil
}

func ensureTable(db *sql.DB) error {
	query := `
		CREATE TABLE IF NOT EXISTS release_state (
			repo TEXT PRIMARY KEY,
			last_tag TEXT NOT NULL,
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)
	`
	_, err := db.Exec(query)
	return err
}

func getLastTag(db *sql.DB, repo string) (string, error) {
	var lastTag string
	err := db.QueryRow("SELECT last_tag FROM release_state WHERE repo = $1", repo).Scan(&lastTag)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return lastTag, err
}

func updateLastTag(db *sql.DB, repo, tag string) error {
	query := `
		INSERT INTO release_state (repo, last_tag, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (repo) DO UPDATE SET last_tag = $2, updated_at = NOW()
	`
	_, err := db.Exec(query, repo, tag)
	return err
}

func postToDiscord(ctx context.Context, webhookURL string, release string) error {
	webhook := DiscordWebhook{
		Content: release,
	}

	payload, err := json.Marshal(webhook)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("Discord webhook returned %d: %s", res.StatusCode, string(body))
	}

	return nil
}
