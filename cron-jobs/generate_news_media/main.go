package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	dsmodels "go-backend/internal/apps/dailystory/models"
	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

const (
	enqueueChunkSize = 64
	maxRetries       = 3
	retryBaseDelay   = 2 * time.Second

	// Modal credential sets are read from MODAL_*_1 .. MODAL_*_<modalAccountCount>.
	modalAccountCount = 4
)

// modalAccount is one Modal workspace's enqueue endpoint and proxy auth credentials.
type modalAccount struct {
	name   string
	url    string
	key    string
	secret string
}

type newsItem struct {
	ID          string `json:"id"`
	ImagePrompt string `json:"image_prompt"`
}

func main() {
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	accounts, err := loadModalAccounts()
	if err != nil {
		log.Fatalf("%v", err)
	}
	names := make([]string, len(accounts))
	for i, a := range accounts {
		names[i] = a.name
	}
	log.Printf("loaded %d Modal account(s): %v", len(accounts), names)

	dbConfig := database.Config{
		Host:     utils.GetEnv("DB_HOST", "localhost"),
		Port:     utils.GetEnv("DB_PORT", "5432"),
		User:     utils.GetEnv("DB_USER", "postgres"),
		Password: utils.GetEnv("DB_PASSWORD", ""),
		DBName:   utils.GetEnv("DB_NAME", "gobackend"),
		SSLMode:  utils.GetEnv("DB_SSL_MODE", "disable"),
	}

	db, err := database.NewCronConnection(dbConfig)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	log.Println("database connected")

	items, err := fetchNewsWithoutMedia(db)
	if err != nil {
		log.Fatalf("failed to fetch news: %v", err)
	}

	if len(items) == 0 {
		log.Println("no news items without media found, exiting")
		return
	}

	log.Printf("fetched %d news item(s) without media", len(items))

	var (
		mu     sync.Mutex
		failed int
		wg     sync.WaitGroup
	)

	// Chunks are assigned to Modal accounts round-robin: chunk 0 -> account 1,
	// chunk 1 -> account 2, ... wrapping back to account 1. The rotation restarts
	// at the first account on every run.
	for i := 0; i < len(items); i += enqueueChunkSize {
		chunk := items[i:min(i+enqueueChunkSize, len(items))]
		acct := accountForChunk(accounts, i/enqueueChunkSize)
		log.Printf("rows %d–%d -> Modal account %s", i, i+len(chunk)-1, acct.name)
		wg.Add(1)
		go func(c []newsItem, start int, a modalAccount) {
			defer wg.Done()
			if err := enqueue(a.url, a.key, a.secret, c); err != nil {
				log.Printf("chunk %d–%d (account %s) failed: %v", start, start+len(c)-1, a.name, err)
				mu.Lock()
				failed += len(c)
				mu.Unlock()
			}
		}(chunk, i, acct)
	}

	wg.Wait()
	log.Printf("done: submitted=%d failed=%d", len(items)-failed, failed)
}

// loadModalAccounts reads every configured Modal credential set (suffixes _1 ..
// _<modalAccountCount>). A fully unset set is skipped; a partially set one is a
// misconfiguration and returns an error; having no usable set at all is also an error.
func loadModalAccounts() ([]modalAccount, error) {
	var accounts []modalAccount
	for n := 1; n <= modalAccountCount; n++ {
		suffix := fmt.Sprintf("_%d", n)
		urlVar := "MODAL_ENQUEUE_URL" + suffix
		keyVar := "MODAL_PROXY_AUTH_TOKEN_ID" + suffix
		secretVar := "MODAL_PROXY_AUTH_TOKEN_SECRET" + suffix

		url := utils.GetEnv(urlVar, "")
		key := utils.GetEnv(keyVar, "")
		secret := utils.GetEnv(secretVar, "")

		if url == "" && key == "" && secret == "" {
			log.Printf("Modal account %d not configured, skipping", n)
			continue
		}
		if url == "" || key == "" || secret == "" {
			return nil, fmt.Errorf("Modal account %d is partially configured: %s, %s and %s must all be set",
				n, urlVar, keyVar, secretVar)
		}
		accounts = append(accounts, modalAccount{name: strconv.Itoa(n), url: url, key: key, secret: secret})
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no Modal account configured: set MODAL_ENQUEUE_URL_1, MODAL_PROXY_AUTH_TOKEN_ID_1 and MODAL_PROXY_AUTH_TOKEN_SECRET_1 (up to _%d)", modalAccountCount)
	}
	return accounts, nil
}

// accountForChunk returns the account for the given zero-based chunk index,
// cycling through accounts in order.
func accountForChunk(accounts []modalAccount, chunkIdx int) modalAccount {
	return accounts[chunkIdx%len(accounts)]
}

func fetchNewsWithoutMedia(db *gorm.DB) ([]newsItem, error) {
	var news []dsmodels.News
	since := time.Now().Add(-12 * time.Hour)
	err := db.
		Select("id", "image_prompt").
		Where("media_file_key IS NULL AND image_prompt IS NOT NULL AND created_at >= ?", since).
		Order("created_at DESC").
		Find(&news).Error
	if err != nil {
		return nil, err
	}

	items := make([]newsItem, 0, len(news))
	for _, n := range news {
		if n.ImagePrompt == nil {
			continue
		}
		items = append(items, newsItem{
			ID:          n.ID.String(),
			ImagePrompt: *n.ImagePrompt + " Purely photographic scene, visual elements only.",
		})
	}
	return items, nil
}

func enqueue(url, modalKey, modalSecret string, items []newsItem) error {
	body, err := json.Marshal(map[string]any{"payload": items})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			time.Sleep(retryBaseDelay * time.Duration(1<<(attempt-1)))
		}
		var status int
		if lastErr, status = doPost(url, modalKey, modalSecret, body); lastErr == nil {
			return nil
		}
		log.Printf("attempt %d/%d failed: %v", attempt+1, maxRetries, lastErr)
		if status >= 400 && status < 500 {
			break
		}
	}
	return lastErr
}

func doPost(url, modalKey, modalSecret string, body []byte) (error, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err), 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Modal-Key", modalKey)
	req.Header.Set("Modal-Secret", modalSecret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err), 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		preview := body
		if len(preview) > 500 {
			preview = preview[:500]
		}
		log.Printf("request body (first 500): %s", preview)
		return fmt.Errorf("status %d: %s", resp.StatusCode, respBody), resp.StatusCode
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		log.Printf("enqueue response: %v", result)
	}
	return nil, http.StatusOK
}
