package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	posthogModels "go-backend/internal/apps/posthog/config/models"
	posthogRepository "go-backend/internal/apps/posthog/config/repository"
	"go-backend/internal/apps/r2/config/repository"
	"go-backend/internal/apps/r2/config/service"
	"go-backend/internal/common/constants"
	"go-backend/internal/common/database"
	"go-backend/pkg/analytics"
	"go-backend/pkg/storage"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/mmcdole/gofeed"
	"golang.org/x/time/rate"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

// News represents the news table
type News struct {
	ID           uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Link         string         `gorm:"type:varchar(2048);not null;unique"`
	MediaFileKey *string        `gorm:"type:varchar(512)"`
	Category     string         `gorm:"type:varchar(100);not null"`
	Status       string         `gorm:"type:varchar(50);not null;default:'published'"`
	PublishedAt  *time.Time     `gorm:"type:timestamp with time zone"`
	Metadata     utils.Metadata `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt    time.Time      `gorm:"autoCreateTime"`
	UpdatedAt    time.Time      `gorm:"autoUpdateTime"`
}

// NewsTranslation represents the news_translations table
type NewsTranslation struct {
	ID           uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	NewsID       uuid.UUID      `gorm:"type:uuid;not null"`
	Title        string         `gorm:"type:varchar(1000);not null"`
	LanguageCode string         `gorm:"type:varchar(10);not null"`
	Metadata     utils.Metadata `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt    time.Time      `gorm:"autoCreateTime"`
	UpdatedAt    time.Time      `gorm:"autoUpdateTime"`
}

// TableName specifies the table name for News
func (News) TableName() string {
	return "news"
}

// TableName specifies the table name for NewsTranslation
func (NewsTranslation) TableName() string {
	return "news_translations"
}

// TranslationResult holds translations for a title (source is Hindi)
// Map of language code -> translated text
type TranslationResult struct {
	// Converted Hindi headline (2-sentence poster format)
	HindiHeadline string
	// Translations of the Hindi headline to other languages
	Translations map[string]string
}

// GeminiConversionResponse for single language translation (convert + translate to one language)
type GeminiConversionResponse struct {
	HindiHeadline string `json:"hindi_headline"`
	Translation   string `json:"translation"`
}

// GeminiConversionMultiResponse for converting headline and translating to multiple languages
type GeminiConversionMultiResponse struct {
	HindiHeadline string `json:"hindi_headline"`
	Punjabi       string `json:"punjabi"`
	Gujarati      string `json:"gujarati"`
	Marathi       string `json:"marathi"`
	Bengali       string `json:"bengali"`
}

// Category-to-language mapping
// Each category maps to the language codes it should be translated to (in addition to Hindi)
// Empty slice means only Hindi (no additional translation needed)
var categoryLanguageMapping = map[string][]string{
	// national categories - all 4 regional languages
	"national":      {"pa", "gu", "mr", "bn"},
	"international": {"pa", "gu", "mr", "bn"},
	"sports":        {"pa", "gu", "mr", "bn"},
	"entertainment": {"pa", "gu", "mr", "bn"},
	// State categories - Hindi + state's language
	"punjab":      {"pa"},
	"west_bengal": {"bn"},
	"gujarat":     {"gu"},
	"maharashtra": {"mr"},
	// States where only Hindi is needed - empty slice
	"himachal_pradesh": {},
	"haryana":          {},
	"delhi":            {},
	"uttar_pradesh":    {},
	"bihar":            {},
	"rajasthan":        {},
	"madhya_pradesh":   {},
	"jharkhand":        {},
	"chhattisgarh":     {},
	"uttarakhand":      {},
}

// getLanguagesForCategory returns the languages to translate for a given category
func getLanguagesForCategory(category string) []string {
	if langs, ok := categoryLanguageMapping[category]; ok {
		return langs
	}
	// Default: no additional translations (only Hindi)
	return []string{}
}

const (
	geminiModel        = "gemini-2.5-flash-lite"
	rateLimitPerMinute = 1000

	// PostHog event names for news parsing
	PostHogEventNewsParsingFailed    = "NEWS_PARSING_FAILED"
	PostHogEventNewsParsingSucceeded = "NEWS_PARSING_SUCCEEDED"
)

// llmGuidelines contains the shared guidelines for LLM headline conversion
const llmGuidelines = `Important guidelines (निर्देश):
- Convert the content into a single factual headline that summarizes all key information
- The headline must not exceed 120 characters (अधिकतम 120 अक्षर)
- The headline should convey complete information (पूरी जानकारी) so users understand the news without reading the full article
- Avoid teaser-style text (चुभाने वाला टेक्स्ट) like "जानें क्या हुआ", "पढ़ें विवरण", "और जानें", "आप विश्वास नहीं करेंगे"
- Include the कौन (who), क्या (what), कब (when), कहाँ (where), and क्यों (why) if available in the original content
- The headline should be informative (जानकारीपूर्ण), not mysterious (रहस्यमय)
- Extract the most important facts from the content and present them in one clear headline`

// Shared HTTP client for connection reuse
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// Common date formats for RSS feeds
var dateFormats = []string{
	time.RFC1123,                 // "Mon, 02 Jan 2006 15:04:05 MST"
	time.RFC1123Z,                // "Mon, 02 Jan 2006 15:04:05 -0700"
	time.RFC822,                  // "02 Jan 06 15:04 MST"
	time.RFC822Z,                 // "02 Jan 06 15:04 -0700"
	"2006-01-02T15:04:05Z07:00",  // ISO 8601 with timezone
	"2006-01-02T15:04:05Z",       // ISO 8601 UTC
	"2006-01-02T15:04:05",        // ISO 8601 without timezone
	"2006-01-02 15:04:05 -0700",  // Custom with timezone offset
	"2006-01-02 15:04:05 MST",    // Custom with timezone name
	"2006-01-02 15:04:05",        // Custom without timezone
	"02 Jan 2006 15:04:05 MST",   // Common RSS format
	"02 Jan 2006 15:04:05 -0700", // Common RSS format with offset
	"02 Jan 2006 15:04:05",       // Common RSS format without timezone
	"Mon, 02 Jan 2006 15:04:05",  // RFC1123 without timezone
}

// RSSFeed represents an RSS feed source with its category
type RSSFeed struct {
	URL      string
	Category string
}

func main() {
	// Load environment variables from appropriate file
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		// Fallback to .env if environment-specific file not found
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s] Starting RSS news feed parser with translations\n", timestamp)

	// Get Gemini API key
	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	if geminiAPIKey == "" {
		log.Fatalf("[%s] ✗ GEMINI_API_KEY environment variable is required\n", timestamp)
	}

	// Initialize rate limiter (rateLimitPerMinute requests per minute, burst=1 for strict limiting)
	rateLimiter := rate.NewLimiter(rate.Every(time.Minute/time.Duration(rateLimitPerMinute)), 1)

	// Connect to database
	dbConfig := database.Config{
		Host:     utils.GetEnv("DB_HOST", "localhost"),
		Port:     utils.GetEnv("DB_PORT", "5432"),
		User:     utils.GetEnv("DB_USER", "postgres"),
		Password: utils.GetEnv("DB_PASSWORD", ""),
		DBName:   utils.GetEnv("DB_NAME", "gobackend"),
		SSLMode:  utils.GetEnv("DB_SSL_MODE", "disable"),
	}

	db, err := database.NewConnection(dbConfig)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to connect to database: %v\n", timestamp, err)
	}

	log.Printf("[%s] ✓ Database connected successfully\n", timestamp)

	// Initialize R2 client factory
	r2ConfigRepo := repository.NewR2ConfigRepository(db)
	r2ClientFactory := service.NewR2ClientFactory(r2ConfigRepo)

	// Get R2 client for DailyStoryApp
	r2Client, err := r2ClientFactory.GetClient(constants.AppNameDailyStory)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to get R2 client: %v\n", timestamp, err)
	}

	// Initialize PostHog client and fetch config once
	posthogConfigRepo := posthogRepository.NewPostHogConfigRepository(db)
	posthogClient := analytics.NewPostHogClient()
	posthogConfig := getPostHogConfig(posthogConfigRepo)
	if posthogConfig != nil {
		log.Printf("[%s] ✓ PostHog config loaded for app: %s\n", timestamp, constants.AppNameDailyStory)
	}

	// Get R2 bucket name from environment
	r2BucketName := os.Getenv("R2_DS_NEWS_BUCKET_NAME")
	if r2BucketName == "" {
		log.Fatalf("[%s] ✗ R2_DS_NEWS_BUCKET_NAME environment variable is required\n", timestamp)
	}

	log.Printf("[%s] ✓ R2 client initialized for bucket: %s\n", timestamp, r2BucketName)

	// Initialize Gemini client
	ctx := context.Background()
	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: geminiAPIKey,
	})
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to create Gemini client: %v\n", timestamp, err)
	}

	// List of RSS feeds with their categories
	rssFeeds := []RSSFeed{
		// States - Hindi only (Bhaskar)
		{URL: "https://www.bhaskar.com/rss-v1--category-12084.xml", Category: "himachal_pradesh"},
		{URL: "https://www.bhaskar.com/rss-v1--category-1742.xml", Category: "haryana"},
		{URL: "https://www.bhaskar.com/rss-v1--category-7140.xml", Category: "delhi"},
		{URL: "https://www.bhaskar.com/rss-v1--category-2052.xml", Category: "uttar_pradesh"},
		{URL: "https://www.bhaskar.com/rss-v1--category-3679.xml", Category: "bihar"},
		{URL: "https://www.bhaskar.com/rss-v1--category-1740.xml", Category: "rajasthan"},
		{URL: "https://www.bhaskar.com/rss-v1--category-1739.xml", Category: "madhya_pradesh"},
		{URL: "https://www.bhaskar.com/rss-v1--category-3682.xml", Category: "jharkhand"},
		{URL: "https://www.bhaskar.com/rss-v1--category-1741.xml", Category: "chhattisgarh"},
		// States - Hindi only (LiveHindustan)
		{URL: "https://api.livehindustan.com/feeds/rss/himachal-pradesh/rssfeed.xml", Category: "himachal_pradesh"},
		{URL: "https://api.livehindustan.com/feeds/rss/haryana/rssfeed.xml", Category: "haryana"},
		{URL: "https://api.livehindustan.com/feeds/rss/ncr/new-delhi/rssfeed.xml", Category: "delhi"},
		{URL: "https://api.livehindustan.com/feeds/rss/uttar-pradesh/rssfeed.xml", Category: "uttar_pradesh"},
		{URL: "https://api.livehindustan.com/feeds/rss/bihar/rssfeed.xml", Category: "bihar"},
		{URL: "https://api.livehindustan.com/feeds/rss/rajasthan/rssfeed.xml", Category: "rajasthan"},
		{URL: "https://api.livehindustan.com/feeds/rss/madhya-pradesh/rssfeed.xml", Category: "madhya_pradesh"},
		{URL: "https://api.livehindustan.com/feeds/rss/jharkhand/rssfeed.xml", Category: "jharkhand"},
		{URL: "https://api.livehindustan.com/feeds/rss/chhattisgarh/rssfeed.xml", Category: "chhattisgarh"},
		{URL: "https://api.livehindustan.com/feeds/rss/uttarakhand/rssfeed.xml", Category: "uttarakhand"},
		// States - Hindi + regional language (Bhaskar)
		{URL: "https://www.bhaskar.com/rss-v1--category-1743.xml", Category: "punjab"},
		{URL: "https://www.bhaskar.com/rss-v1--category-2314.xml", Category: "gujarat"},
		{URL: "https://www.bhaskar.com/rss-v1--category-2318.xml", Category: "maharashtra"},
		// States - Hindi + regional language (LiveHindustan)
		{URL: "https://api.livehindustan.com/feeds/rss/west-bengal/rssfeed.xml", Category: "west_bengal"},
		{URL: "https://api.livehindustan.com/feeds/rss/punjab/rssfeed.xml", Category: "punjab"},
		{URL: "https://api.livehindustan.com/feeds/rss/gujarat/rssfeed.xml", Category: "gujarat"},
		{URL: "https://api.livehindustan.com/feeds/rss/maharashtra/rssfeed.xml", Category: "maharashtra"},
		// national categories - all languages (Bhaskar)
		{URL: "https://www.bhaskar.com/rss-v1--category-1061.xml", Category: "national"},
		{URL: "https://www.bhaskar.com/rss-v1--category-1125.xml", Category: "international"},
		{URL: "https://www.bhaskar.com/rss-v1--category-1053.xml", Category: "sports"},
		{URL: "https://www.bhaskar.com/rss-v1--category-3998.xml", Category: "entertainment"},
		// national categories - all languages (LiveHindustan)
		{URL: "https://api.livehindustan.com/feeds/rss/national/rssfeed.xml", Category: "national"},
		{URL: "https://api.livehindustan.com/feeds/rss/international/rssfeed.xml", Category: "international"},
		{URL: "https://api.livehindustan.com/feeds/rss/sports/rssfeed.xml", Category: "sports"},
		{URL: "https://api.livehindustan.com/feeds/rss/entertainment/rssfeed.xml", Category: "entertainment"},
	}

	totalProcessed := 0
	totalSkipped := 0
	totalFailed := 0
	var countersMutex sync.Mutex

	// Track in-flight links to prevent duplicate processing across goroutines
	var inFlightLinks sync.Map

	// Worker pool for processing news items concurrently
	// Use a buffered channel as semaphore to limit concurrency
	maxConcurrentItems := 20
	itemSemaphore := make(chan struct{}, maxConcurrentItems)
	var itemWg sync.WaitGroup

	// Worker pool for fetching RSS feeds concurrently
	maxConcurrentFeeds := 5
	feedSemaphore := make(chan struct{}, maxConcurrentFeeds)
	var feedWg sync.WaitGroup

	// Process each RSS feed concurrently
	for _, rssFeed := range rssFeeds {
		feedWg.Add(1)
		feedSemaphore <- struct{}{} // Acquire semaphore

		go func(rssFeed RSSFeed) {
			defer feedWg.Done()
			defer func() { <-feedSemaphore }() // Release semaphore

			log.Printf("[%s] Parsing feed: %s (category: %s)\n", timestamp, rssFeed.URL, rssFeed.Category)

			// Create a new parser for each goroutine (gofeed.Parser is not thread-safe)
			fp := gofeed.NewParser()
			feed, err := fp.ParseURL(rssFeed.URL)
			if err != nil {
				log.Printf("[%s] ✗ Failed to parse feed %s: %v\n", timestamp, rssFeed.URL, err)
				return
			}

			log.Printf("[%s] ✓ Found %d items in feed\n", timestamp, len(feed.Items))

			for _, item := range feed.Items {
				// Trim title and description early
				item.Title = strings.TrimSpace(item.Title)
				item.Description = strings.TrimSpace(item.Description)

				// Skip items without title
				if item.Title == "" {
					countersMutex.Lock()
					totalSkipped++
					countersMutex.Unlock()
					continue
				}

				// Extract media link from extensions
				mediaLink := ""
				if media, ok := item.Extensions["media"]; ok {
					if content, ok := media["content"]; ok && len(content) > 0 {
						mediaLink = content[0].Attrs["url"]
					}
				}
				// Fallback to enclosure if no media extension
				if mediaLink == "" && len(item.Enclosures) > 0 {
					mediaLink = item.Enclosures[0].URL
				}

				// Skip items without media link
				if mediaLink == "" {
					countersMutex.Lock()
					totalSkipped++
					countersMutex.Unlock()
					continue
				}

				// Check if item is duplicate (by link or media file key)
				isDuplicate, err := checkDuplicateItem(db, item.Link, mediaLink)
				if err != nil {
					log.Printf("[%s] ✗ Database error checking duplicate: %v\n", timestamp, err)
					countersMutex.Lock()
					totalFailed++
					countersMutex.Unlock()
					continue
				}
				if isDuplicate {
					countersMutex.Lock()
					totalSkipped++
					countersMutex.Unlock()
					continue
				}

				// Check if this link is already being processed by another goroutine
				// LoadOrStore returns (value, loaded) - if loaded is true, the key already exists
				if _, alreadyProcessing := inFlightLinks.LoadOrStore(item.Link, true); alreadyProcessing {
					countersMutex.Lock()
					totalSkipped++
					countersMutex.Unlock()
					continue
				}

				// Parse published_at robustly
				publishedAt := parsePublishedAt(item)

				// Get target languages based on category
				targetLanguages := getLanguagesForCategory(rssFeed.Category)

				// Process news item concurrently
				itemWg.Add(1)
				itemSemaphore <- struct{}{} // Acquire semaphore

				go func(item *gofeed.Item, mediaLink string, publishedAt *time.Time, category string, targetLanguages []string) {
					defer itemWg.Done()
					defer func() { <-itemSemaphore }()    // Release semaphore
					defer inFlightLinks.Delete(item.Link) // Remove from in-flight tracking

					// Convert title to Hindi headline and translate based on category using Gemini
					// Use description if available, otherwise fall back to title
					result, err := translateWithGemini(context.Background(), genaiClient, item.Title, item.Description, targetLanguages, rateLimiter)
					if err != nil {
						log.Printf("[%s] ✗ Failed to convert/translate title '%s': %v\n", timestamp, item.Title, err)
						countersMutex.Lock()
						totalFailed++
						countersMutex.Unlock()

						// Emit PostHog event for news parsing failure
						go sendPostHogNewsParsingEvent(posthogClient, posthogConfig, item.Title, category, extractDomain(rssFeed.URL), PostHogEventNewsParsingFailed, err)

						return
					}

					// Upload media to R2 if present (before DB transaction)
					// If R2 upload fails, we don't insert in DB - maintaining atomicity
					var mediaFileKey *string
					if mediaLink != "" {
						fileKey, err := uploadMediaToR2(r2Client, r2BucketName, mediaLink, httpClient)
						if err != nil {
							log.Printf("[%s] ✗ Failed to upload media to R2: %v\n", timestamp, err)
							countersMutex.Lock()
							totalFailed++
							countersMutex.Unlock()
							return
						}
						mediaFileKey = &fileKey
					}

					// Store in database atomically (with R2 cleanup on failure)
					err = storeNewsWithTranslations(db, r2Client, r2BucketName, item.Link, mediaFileKey, publishedAt, category, result)
					if err != nil {
						log.Printf("[%s] ✗ Failed to store news item: %v\n", timestamp, err)
						countersMutex.Lock()
						totalFailed++
						countersMutex.Unlock()
						return
					}

					countersMutex.Lock()
					totalProcessed++
					countersMutex.Unlock()

					// Emit PostHog event for successful news parsing
					go sendPostHogNewsParsingEvent(posthogClient, posthogConfig, item.Title, category, extractDomain(rssFeed.URL), PostHogEventNewsParsingSucceeded, nil)
				}(item, mediaLink, publishedAt, rssFeed.Category, targetLanguages)
			}

			log.Printf("[%s] ✓ Finished processing feed: %s\n", timestamp, rssFeed.URL)
		}(rssFeed)
	}

	// Wait for all feeds to complete
	feedWg.Wait()

	// Wait for all news items to complete
	itemWg.Wait()

	log.Printf("[%s] ✓ RSS news feed parsing completed: %d processed, %d skipped, %d failed\n", timestamp, totalProcessed, totalSkipped, totalFailed)
	os.Exit(0)
}

// translateWithGemini converts the RSS title/description to a 2-sentence Hindi poster headline
// and translates it to target languages in a single LLM call
// Uses description if available and non-empty, otherwise falls back to title
func translateWithGemini(ctx context.Context, client *genai.Client, title, description string, targetLanguages []string, rateLimiter *rate.Limiter) (TranslationResult, error) {
	// Wait for rate limiter permission
	if err := rateLimiter.Wait(ctx); err != nil {
		return TranslationResult{}, fmt.Errorf("rate limiter wait failed: %w", err)
	}

	// Determine which source to use: description if available, otherwise title
	sourceText := title
	if description != "" {
		sourceText = description
	}

	// Determine if we need single or multi-language translation
	if len(targetLanguages) == 0 {
		// Only convert to Hindi headline, no translations needed
		return callGeminiConversionOnly(ctx, client, sourceText)
	}
	if len(targetLanguages) == 1 {
		return callGeminiSingleWithConversion(ctx, client, sourceText, targetLanguages[0])
	}
	return callGeminiMultiWithConversion(ctx, client, sourceText, targetLanguages)
}

// callGeminiConversionOnly converts RSS content to Hindi headline only (no translations)
func callGeminiConversionOnly(ctx context.Context, client *genai.Client, sourceText string) (TranslationResult, error) {
	prompt := fmt.Sprintf(`Convert the following Hindi news content into a single factual Hindi news poster headline.

%s

Respond ONLY with a JSON object in this exact format: {"hindi_headline": "converted headline here"}

Original Hindi news content: "%s"`, llmGuidelines, sourceText)

	result, err := callGeminiAPI(ctx, client, prompt)
	if err != nil {
		return TranslationResult{}, err
	}

	var response struct {
		HindiHeadline string `json:"hindi_headline"`
	}
	if err := json.Unmarshal([]byte(result), &response); err != nil {
		return TranslationResult{}, fmt.Errorf("failed to parse Gemini response: %w", err)
	}

	return TranslationResult{
		HindiHeadline: strings.TrimSpace(response.HindiHeadline),
		Translations:  make(map[string]string),
	}, nil
}

// callGeminiSingleWithConversion converts content to Hindi headline and translates to a single language
func callGeminiSingleWithConversion(ctx context.Context, client *genai.Client, sourceText string, langCode string) (TranslationResult, error) {
	langName := languageCodeToName(langCode)
	prompt := fmt.Sprintf(`Convert the following Hindi news content into a single factual Hindi news poster headline, then translate that converted headline to %s.

%s

Respond ONLY with a JSON object in this exact format:
{
  "hindi_headline": "converted Hindi headline here",
  "translation": "translated text in %s"
}

Original Hindi news content: "%s"`, langName, llmGuidelines, langName, sourceText)

	result, err := callGeminiAPI(ctx, client, prompt)
	if err != nil {
		return TranslationResult{}, err
	}

	var response GeminiConversionResponse
	if err := json.Unmarshal([]byte(result), &response); err != nil {
		return TranslationResult{}, fmt.Errorf("failed to parse Gemini response: %w", err)
	}

	return TranslationResult{
		HindiHeadline: strings.TrimSpace(response.HindiHeadline),
		Translations:  map[string]string{langCode: strings.TrimSpace(response.Translation)},
	}, nil
}

// callGeminiMultiWithConversion converts content to Hindi headline and translates to multiple languages
func callGeminiMultiWithConversion(ctx context.Context, client *genai.Client, sourceText string, targetLanguages []string) (TranslationResult, error) {
	// Build language list for prompt
	langNames := make([]string, len(targetLanguages))
	for i, langCode := range targetLanguages {
		langNames[i] = languageCodeToName(langCode)
	}

	prompt := fmt.Sprintf(`Convert the following Hindi news content into a single factual Hindi news poster headline, then translate that converted headline to these languages: %s.

%s

Respond ONLY with a JSON object in this exact format:
{
  "hindi_headline": "converted Hindi headline here",
  "punjabi": "translated text in Punjabi",
  "gujarati": "translated text in Gujarati",
  "marathi": "translated text in Marathi",
  "bengali": "translated text in Bengali"
}
Only include the languages requested from: Punjabi, Gujarati, Marathi, Bengali. Use the exact keys shown above.

Original Hindi news content: "%s"`, strings.Join(langNames, ", "), llmGuidelines, sourceText)

	result, err := callGeminiAPI(ctx, client, prompt)
	if err != nil {
		return TranslationResult{}, err
	}

	var response GeminiConversionMultiResponse
	if err := json.Unmarshal([]byte(result), &response); err != nil {
		return TranslationResult{}, fmt.Errorf("failed to parse Gemini response: %w", err)
	}

	// Map to TranslationResult based on which languages were requested
	translations := make(map[string]string)
	for _, langCode := range targetLanguages {
		switch langCode {
		case "pa":
			translations["pa"] = strings.TrimSpace(response.Punjabi)
		case "gu":
			translations["gu"] = strings.TrimSpace(response.Gujarati)
		case "mr":
			translations["mr"] = strings.TrimSpace(response.Marathi)
		case "bn":
			translations["bn"] = strings.TrimSpace(response.Bengali)
		}
	}

	return TranslationResult{
		HindiHeadline: strings.TrimSpace(response.HindiHeadline),
		Translations:  translations,
	}, nil
}

// languageCodeToName converts language code to full name
func languageCodeToName(code string) string {
	switch code {
	case "pa":
		return "Punjabi"
	case "gu":
		return "Gujarati"
	case "mr":
		return "Marathi"
	case "bn":
		return "Bengali"
	default:
		return code
	}
}

// callGeminiAPI makes a request to Gemini API using the SDK
func callGeminiAPI(ctx context.Context, client *genai.Client, prompt string) (string, error) {
	contents := []*genai.Content{
		{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: prompt}},
		},
	}

	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
	}

	response, err := client.Models.GenerateContent(ctx, geminiModel, contents, config)
	if err != nil {
		return "", fmt.Errorf("Gemini API error: %w", err)
	}

	if len(response.Candidates) == 0 || response.Candidates[0].Content == nil || len(response.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in Gemini response")
	}

	return response.Candidates[0].Content.Parts[0].Text, nil
}

// checkDuplicateItem checks if a news item is duplicate by link or media file key
// Returns true if either check finds a match (item is duplicate)
func checkDuplicateItem(db *gorm.DB, link, mediaFileKey string) (bool, error) {
	// Step 1: Check if link exists in news table
	var existingNews News
	result := db.Where("link = ?", link).First(&existingNews)
	if result.Error == nil {
		// Link already exists
		return true, nil
	}
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return false, fmt.Errorf("error checking link: %w", result.Error)
	}

	// Step 2: Check if media file key exists (if provided)
	if mediaFileKey != "" {
		var existingMedia News
		result = db.Where("media_file_key = ?", mediaFileKey).First(&existingMedia)
		if result.Error == nil {
			// Media file already exists
			return true, nil
		}
		if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
			return false, fmt.Errorf("error checking media: %w", result.Error)
		}
	}

	// Neither link nor media exists - not a duplicate
	return false, nil
}

// parsePublishedAt robustly parses the published date from an RSS item
// It tries multiple date formats and handles timezone presence/absence
func parsePublishedAt(item *gofeed.Item) *time.Time {
	// First, try the pre-parsed value from gofeed
	if item.PublishedParsed != nil {
		return item.PublishedParsed
	}

	// Try parsing the raw Published string with various formats
	if item.Published != "" {
		if t := tryParseDate(item.Published); t != nil {
			return t
		}
	}

	// Try parsing the raw Updated string as fallback
	if item.Updated != "" {
		if t := tryParseDate(item.Updated); t != nil {
			return t
		}
	}

	return nil
}

// tryParseDate attempts to parse a date string using multiple formats
func tryParseDate(dateStr string) *time.Time {
	// Clean up the date string
	dateStr = strings.TrimSpace(dateStr)

	for _, format := range dateFormats {
		if t, err := time.Parse(format, dateStr); err == nil {
			// If the parsed time has no timezone info, assume UTC
			if t.Location() == time.UTC {
				return &t
			}
			// Convert to UTC for consistent storage
			utcTime := t.UTC()
			return &utcTime
		}
	}

	// Try parsing without timezone and assume UTC
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"02 Jan 2006 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, dateStr, time.UTC); err == nil {
			return &t
		}
	}

	return nil
}

// storeNewsWithTranslations stores news and translations atomically
// If DB transaction fails and media was uploaded to R2, it cleans up the R2 file
// The hindiHeadline is the converted 2-sentence poster headline (stored for 'hi' language code)
func storeNewsWithTranslations(db *gorm.DB, r2Client *storage.R2Client, bucketName, link string, mediaFileKey *string, publishedAt *time.Time, category string, result TranslationResult) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		// Create news record
		news := News{
			Link:         link,
			Category:     category,
			Status:       "approved",
			PublishedAt:  publishedAt,
			MediaFileKey: mediaFileKey,
		}

		if err := tx.Create(&news).Error; err != nil {
			return fmt.Errorf("failed to create news: %w", err)
		}

		// Create translations (news.ID is populated after Create)
		// Store the converted Hindi headline (not the original RSS title) for 'hi' language code
		translationsToCreate := []NewsTranslation{
			{NewsID: news.ID, Title: result.HindiHeadline, LanguageCode: "hi"},
		}

		// Add translated titles
		for langCode, title := range result.Translations {
			translationsToCreate = append(translationsToCreate, NewsTranslation{
				NewsID:       news.ID,
				Title:        strings.TrimSpace(title),
				LanguageCode: langCode,
			})
		}

		if err := tx.Create(&translationsToCreate).Error; err != nil {
			return fmt.Errorf("failed to create translations: %w", err)
		}

		return nil
	})

	// If DB transaction failed and we uploaded media to R2, clean up the orphan file
	if err != nil && mediaFileKey != nil {
		cleanupErr := r2Client.DeleteFile(bucketName, *mediaFileKey)
		if cleanupErr != nil {
			log.Printf("Failed to cleanup R2 file after DB error: %v", cleanupErr)
		}
	}

	return err
}

// uploadMediaToR2 handles media upload to R2
// For GIFs, it converts to PNG (first frame) before uploading
func uploadMediaToR2(r2Client *storage.R2Client, bucketName, mediaURL string, httpClient *http.Client) (string, error) {
	// Check if it's a GIF
	if utils.IsGifURL(mediaURL) {
		// Download the GIF
		resp, err := httpClient.Get(mediaURL)
		if err != nil {
			return "", fmt.Errorf("failed to download GIF: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to download GIF: HTTP status %d", resp.StatusCode)
		}

		// Read the GIF data
		gifData, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read GIF data: %w", err)
		}

		// Convert GIF to PNG (first frame)
		pngData, err := utils.ConvertGifToPng(gifData)
		if err != nil {
			return "", fmt.Errorf("failed to convert GIF to PNG: %w", err)
		}

		// Generate file key with .png extension
		fileKey := fmt.Sprintf("media/%s.png", uuid.New().String())

		// Upload PNG to R2
		err = r2Client.UploadFile(bucketName, fileKey, bytes.NewReader(pngData), "image/png")
		if err != nil {
			return "", fmt.Errorf("failed to upload PNG to R2: %w", err)
		}

		return fileKey, nil
	}

	// For non-GIF media, upload directly
	fileKey := fmt.Sprintf("media/%s%s", uuid.New().String(), utils.GetFileExtensionFromURL(mediaURL))
	_, err := r2Client.UploadFileFromURL(bucketName, fileKey, mediaURL, httpClient)
	if err != nil {
		return "", fmt.Errorf("failed to upload media to R2: %w", err)
	}

	return fileKey, nil
}

// extractDomain extracts the domain name from a URL
// e.g., "https://www.bhaskar.com/rss-v1--category-1061.xml" -> "bhaskar.com"
func extractDomain(urlStr string) string {
	urlStr = strings.TrimSpace(urlStr)
	// Remove protocol
	if idx := strings.Index(urlStr, "://"); idx != -1 {
		urlStr = urlStr[idx+3:]
	}
	// Remove path
	if idx := strings.Index(urlStr, "/"); idx != -1 {
		urlStr = urlStr[:idx]
	}
	// Remove port if present
	if idx := strings.Index(urlStr, ":"); idx != -1 {
		urlStr = urlStr[:idx]
	}
	// Remove www. prefix
	urlStr = strings.TrimPrefix(urlStr, "www.")
	return urlStr
}

// ==================== PostHog Analytics Events ====================

// NewsParsingEventProperties contains properties for news parsing events
type NewsParsingEventProperties struct {
	Category     string
	RSSSource    string
	ErrorMessage string
}

// ToProperties converts NewsParsingEventProperties to a map
func (p NewsParsingEventProperties) ToProperties() map[string]interface{} {
	props := map[string]interface{}{
		"category":  p.Category,
		"rssSource": p.RSSSource,
	}

	if p.ErrorMessage != "" {
		props["error_message"] = p.ErrorMessage
	}

	return props
}

// getPostHogConfig fetches PostHog config once at startup
func getPostHogConfig(configRepo posthogRepository.PostHogConfigRepository) *posthogModels.PostHogConfig {
	env := utils.GetEnv("GO_ENV", "local")
	config, err := configRepo.FindByAppNameAndEnv(constants.AppNameDailyStory, env)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			log.Printf("[PostHog] No config found for app: %s, environment: %s. Events will be skipped.\n", constants.AppNameDailyStory, env)
		} else {
			log.Printf("[PostHog ERROR] Failed to get config: %v. Events will be skipped.\n", err)
		}
		return nil
	}

	if !config.IsActive {
		log.Printf("[PostHog] Config is inactive for app: %s. Events will be skipped.\n", constants.AppNameDailyStory)
		return nil
	}

	return config
}

// sendPostHogNewsParsingEvent sends NEWS_PARSING_FAILED or NEWS_PARSING_SUCCEEDED event to PostHog
func sendPostHogNewsParsingEvent(
	client *analytics.PostHogClient,
	config *posthogModels.PostHogConfig,
	title string,
	category string,
	rssSource string,
	eventName string,
	err error,
) {
	if config == nil {
		return
	}

	props := NewsParsingEventProperties{
		Category:  category,
		RSSSource: rssSource,
	}

	if err != nil {
		props.ErrorMessage = err.Error()
	}

	// Use a hash of the title as distinct_id since we don't have a user_id
	distinctID := uuid.NewMD5(uuid.NameSpaceDNS, []byte(title)).String()

	log.Printf("[PostHog] Processing %s event for title: %s\n", eventName, title)

	go func() {
		if sendErr := client.SendEvent(config.Host, config.APIKey, eventName, distinctID, props.ToProperties()); sendErr != nil {
			log.Printf("[PostHog ERROR] Failed to send %s event: %v\n", eventName, sendErr)
		}
	}()
}
