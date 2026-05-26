package function

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
)

var (
	repo              string
	discordWebhookURL string
	db                *sql.DB
)

func init() {
	repo = getEnv("watch_repo", "openfaas/faas")
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
}

type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
}

type DiscordEmbed struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Color       int    `json:"color"`
	Timestamp   string `json:"timestamp"`
	Footer      struct {
		Text string `json:"text"`
	} `json:"footer"`
}

type DiscordWebhook struct {
	Content string         `json:"content"`
	Embeds  []DiscordEmbed `json:"embeds"`
}

func Handle(w http.ResponseWriter, r *http.Request) {
	// Fetch latest release from GitHub
	release, err := fetchLatestRelease(repo)
	if err != nil {
		log.Printf("Error fetching release: %v", err)
		http.Error(w, fmt.Sprintf("Error fetching release: %v", err), http.StatusInternalServerError)
		return
	}

	// Get last seen tag from database
	lastTag, err := getLastTag(db, repo)
	if err != nil {
		log.Printf("Error getting last tag: %v", err)
		http.Error(w, fmt.Sprintf("Database query error: %v", err), http.StatusInternalServerError)
		return
	}

	if lastTag == release.TagName {
		msg := fmt.Sprintf("No new release for %s (current: %s)", repo, release.TagName)
		log.Println(msg)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(msg))
		return
	}

	// Post to Discord
	if err := postToDiscord(discordWebhookURL, repo, release); err != nil {
		log.Printf("Error posting to Discord: %v", err)
		http.Error(w, fmt.Sprintf("Discord webhook error: %v", err), http.StatusInternalServerError)
		return
	}

	// Update last seen tag
	if err := updateLastTag(db, repo, release.TagName); err != nil {
		log.Printf("Error updating last tag: %v", err)
		http.Error(w, fmt.Sprintf("Database update error: %v", err), http.StatusInternalServerError)
		return
	}

	msg := fmt.Sprintf("Posted new release %s for %s to Discord", release.TagName, repo)
	log.Println(msg)
	w.WriteHeader(http.StatusOK)
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

func fetchLatestRelease(repo string) (*GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
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

func postToDiscord(webhookURL, repo string, release *GitHubRelease) error {
	description := release.Body
	if description == "" {
		description = "_No release notes provided._"
	} else if len(description) > 300 {
		description = description[:300] + "…"
	}

	embed := DiscordEmbed{
		Title:       fmt.Sprintf("New release: %s %s", repo, release.TagName),
		Description: description,
		URL:         release.HTMLURL,
		Color:       0x3CB371, // MediumSeaGreen
		Timestamp:   release.PublishedAt.Format(time.RFC3339),
	}
	embed.Footer.Text = fmt.Sprintf("Released by %s", release.Author.Login)

	webhook := DiscordWebhook{
		Content: fmt.Sprintf("New release for **%s**: %s", repo, release.TagName),
		Embeds:  []DiscordEmbed{embed},
	}

	payload, err := json.Marshal(webhook)
	if err != nil {
		return err
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Discord webhook returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
