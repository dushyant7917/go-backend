package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	dsmodels "go-backend/internal/apps/dailystory/models"
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

// computeContentHash returns a sha256 hex digest over normalized title + UTC date + source host.
// The date is truncated to day precision (UTC) so minor timezone differences don't split the same article.
// sourceHost is the hostname of the RSS feed URL (e.g. "www.bhaskar.com").
func computeContentHash(title string, publishedAt *time.Time, sourceHost string) string {
	normalizedTitle := strings.ToLower(strings.TrimSpace(title))
	normalizedDate := ""
	if publishedAt != nil {
		normalizedDate = publishedAt.UTC().Format("2006-01-02")
	}
	input := normalizedTitle + "|" + normalizedDate + "|" + sourceHost
	digest := sha256.Sum256([]byte(input))
	return hex.EncodeToString(digest[:])
}

// TranslationPair holds both headline and summary translations for a language
type TranslationPair struct {
	Headline string
	Summary  string
}

// TranslationResult holds translations for a news item
type TranslationResult struct {
	// Base language code (e.g. "hi" for Hindi-source feeds, "te" for Telugu-source feeds)
	BaseLanguageCode string
	// Headline in the base language (poster format, 4 words)
	BaseHeadline string
	// Short summary in the base language (poster format, 25-30 words)
	BaseSummary string
	// Translations of the headline and summary to additional languages
	Translations map[string]TranslationPair
	// SkipItem is true when LLM determined the article should not be stored
	SkipItem bool
	// SkipReason is the brief reason returned by LLM when SkipItem is true
	SkipReason string
}

// categoryLanguageMapping maps each category to the full set of language codes that should be stored.
// "hi" is listed explicitly so the set is self-documenting and the routing logic never needs to
// assume a default base language.
// Hindi-source categories always include "hi". Regional-source categories (te/ta/ml/kn) do NOT
// include "hi" — their base language is the regional language itself.
var categoryLanguageMapping = map[string][]string{
	// National categories — Hindi base + English + all 8 regional languages
	"national":      {"hi", "en", "pa", "gu", "mr", "bn", "te", "ta", "ml", "kn"},
	"international": {"hi", "en", "pa", "gu", "mr", "bn", "te", "ta", "ml", "kn"},
	"sports":        {"hi", "en", "pa", "gu", "mr", "bn", "te", "ta", "ml", "kn"},
	"entertainment": {"hi", "en", "pa", "gu", "mr", "bn", "te", "ta", "ml", "kn"},
	// Hindi-source states — Hindi base + state's regional language
	"punjab":      {"hi", "pa"},
	"west_bengal": {"hi", "bn"},
	"gujarat":     {"hi", "gu"},
	"maharashtra": {"hi", "mr"},
	// Hindi-source states — Hindi only
	"himachal_pradesh": {"hi"},
	"haryana":          {"hi"},
	"delhi":            {"hi"},
	"uttar_pradesh":    {"hi"},
	"bihar":            {"hi"},
	"rajasthan":        {"hi"},
	"madhya_pradesh":   {"hi"},
	"jharkhand":        {"hi"},
	"chhattisgarh":     {"hi"},
	"uttarakhand":      {"hi"},
	// Regional-source states — regional language + English (no Hindi)
	"telangana":  {"te", "en"},
	"tamil_nadu": {"ta", "en"},
	"kerala":     {"ml", "en"},
	"karnataka":  {"kn", "en"},
}

// getLanguagesForCategory returns the full set of language codes to store for a given category.
// Falls back to Hindi-only for any unmapped category.
func getLanguagesForCategory(category string) []string {
	if langs, ok := categoryLanguageMapping[category]; ok {
		return langs
	}
	return []string{"hi"}
}

// isStateCategory returns true for state-level categories (not national/international/sports/entertainment).
func isStateCategory(category string) bool {
	switch category {
	case "national", "international", "sports", "entertainment":
		return false
	}
	return true
}

// categoryToStateName converts a category key to a human-readable state name.
func categoryToStateName(category string) string {
	names := map[string]string{
		"himachal_pradesh": "Himachal Pradesh",
		"haryana":          "Haryana",
		"delhi":            "Delhi",
		"uttar_pradesh":    "Uttar Pradesh",
		"bihar":            "Bihar",
		"rajasthan":        "Rajasthan",
		"madhya_pradesh":   "Madhya Pradesh",
		"jharkhand":        "Jharkhand",
		"chhattisgarh":     "Chhattisgarh",
		"uttarakhand":      "Uttarakhand",
		"punjab":           "Punjab",
		"west_bengal":      "West Bengal",
		"gujarat":          "Gujarat",
		"maharashtra":      "Maharashtra",
		"telangana":        "Telangana",
		"tamil_nadu":       "Tamil Nadu",
		"kerala":           "Kerala",
		"karnataka":        "Karnataka",
	}
	if name, ok := names[category]; ok {
		return name
	}
	return category
}

// buildFilterPreamble returns the Gemini prompt preamble that instructs the model to classify
// (and potentially skip) the article. It starts with a base filter common to all categories,
// then appends category-specific additional rules.
func buildFilterPreamble(category string) string {
	base := `Before translating, classify this article. Skip it if ANY of these apply:
- It is about astrology, horoscopes, zodiac signs, or numerology.
- It is a food or cooking recipe, or a step-by-step cooking guide.
- It is a commercial advertisement or sponsored promotional content.`

	var additional string
	switch {
	case isStateCategory(category):
		stateName := categoryToStateName(category)
		additional = fmt.Sprintf(`- The article is primarily about national or central government affairs (e.g. Parliament, Prime Minister, central ministers, national policies) with no direct %s angle.
- The article is primarily about another Indian state and only mentions %s incidentally or not at all.
- The article covers international news or events abroad with no direct %s connection.
- The article is about a person, place, or event with no clear tie to %s (e.g. a national celebrity, a pan-India company, a court ruling from another state).
- It is a sports or entertainment article that does not feature players, artists, teams, or events specifically from or representing %s.
When in doubt about whether the article is truly %s-specific versus national or other-state coverage, SKIP it.`, stateName, stateName, stateName, stateName, stateName, stateName)
	case category == "national":
		additional = `- The article is about a state government decision, state CM/minister, state budget, or state-level political appointment with no nationwide impact.
- The article covers a local crime, state police operation, or law-and-order incident confined to one state.
- The article is about a city- or state-level civic/municipal issue (roads, electricity, water supply in a specific city or state).
- The article is about a state-level infrastructure or development project with no pan-India significance.
- The article is about regional politics (state assembly, state election results for a single constituency, intra-party state-level factionalism) without broader national implications.
- It is international news with no direct India angle.
Truly national news involves: central government policies, Parliament, PM/President, Supreme Court rulings with nationwide impact, national elections, RBI/SEBI/ISRO/armed forces, or events affecting multiple states simultaneously.
When in doubt about whether the article has genuine national significance versus being a single-state story, SKIP it.`
	case category == "international":
		additional = `- It covers only India's domestic affairs with no international angle (purely internal Indian news that does not involve other countries or world events).`
	case category == "sports":
		additional = `- It is not about sports, athletics, or competitive games.`
	case category == "entertainment":
		additional = `- It is not about entertainment (unrelated to films, music, TV, celebrities, or pop culture).`
	}

	preamble := base
	if additional != "" {
		preamble += "\n" + additional
	}
	preamble += `

If the article should be skipped, respond ONLY with: {"skip": "true", "skip_reason": "brief reason"}
If the article should NOT be skipped, include "skip": "false" as the first field in your JSON response.`

	return preamble
}

const (
	geminiModel        = "gemini-2.5-flash-lite"
	rateLimitPerMinute = 1000

	// PostHog event names for news parsing
	PostHogEventNewsParsingFailed    = "NEWS_PARSING_FAILED"
	PostHogEventNewsParsingSucceeded = "NEWS_PARSING_SUCCEEDED"
)

// llmHeadlineGuidelines contains the shared guidelines for LLM headline creation
const llmHeadlineGuidelines = `Headline guidelines:
- Create a concise news headline that captures the essence of the news
- Important: The headline must be 4 words long only`

// llmSummaryGuidelines contains the shared guidelines for LLM short summary conversion
const llmSummaryGuidelines = `Summary guidelines:
- Convert the content into a single factual short summary that summarizes key information
- Important: The summary must be 25-30 words long only`

// browserUserAgentTransport wraps an http.RoundTripper and injects a browser-like
// User-Agent so that feeds which block the default gofeed agent (e.g. manatelangana.news)
// respond with 200 instead of 403.
type browserUserAgentTransport struct{ http.RoundTripper }

func (t *browserUserAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; RSS reader)")
	return t.RoundTripper.RoundTrip(req)
}

// Shared HTTP client for connection reuse
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// imgSrcRegex extracts the src attribute of the first <img> tag in HTML content.
// Used as a fallback for feeds that embed images in content:encoded rather than
// using <media:content> or <enclosure> elements (e.g. manatelangana.news, ntvtelugu.com).
var imgSrcRegex = regexp.MustCompile(`(?i)<img[^>]*\ssrc=["']([^"']+)["']`)

// extractImageFromContent returns the first <img src> URL found in HTML content, or "".
func extractImageFromContent(content string) string {
	if matches := imgSrcRegex.FindStringSubmatch(content); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
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

	db, err := database.NewCronConnection(dbConfig)
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
		{URL: "https://api.livehindustan.com/feeds/rss/rajasthan/rssfeed.xml", Category: "rajasthan"},
		{URL: "https://api.livehindustan.com/feeds/rss/madhya-pradesh/rssfeed.xml", Category: "madhya_pradesh"},
		{URL: "https://api.livehindustan.com/feeds/rss/jharkhand/rssfeed.xml", Category: "jharkhand"},
		{URL: "https://api.livehindustan.com/feeds/rss/chhattisgarh/rssfeed.xml", Category: "chhattisgarh"},
		{URL: "https://api.livehindustan.com/feeds/rss/uttarakhand/rssfeed.xml", Category: "uttarakhand"},
		// States - Hindi only (OneIndia)
		{URL: "https://hindi.oneindia.com/rss/feeds/hindi-bihar-fb.xml", Category: "bihar"},
		// States - Hindi + regional language (Bhaskar)
		{URL: "https://www.bhaskar.com/rss-v1--category-1743.xml", Category: "punjab"},
		{URL: "https://www.bhaskar.com/rss-v1--category-2314.xml", Category: "gujarat"},
		{URL: "https://www.bhaskar.com/rss-v1--category-2318.xml", Category: "maharashtra"},
		// States - Hindi + regional language (LiveHindustan)
		{URL: "https://api.livehindustan.com/feeds/rss/west-bengal/rssfeed.xml", Category: "west_bengal"},
		{URL: "https://api.livehindustan.com/feeds/rss/punjab/rssfeed.xml", Category: "punjab"},
		{URL: "https://api.livehindustan.com/feeds/rss/gujarat/rssfeed.xml", Category: "gujarat"},
		{URL: "https://api.livehindustan.com/feeds/rss/maharashtra/rssfeed.xml", Category: "maharashtra"},
		// national categories - Hindi source, all 8 regional languages (Bhaskar)
		{URL: "https://www.bhaskar.com/rss-v1--category-1061.xml", Category: "national"},
		{URL: "https://www.bhaskar.com/rss-v1--category-1125.xml", Category: "international"},
		{URL: "https://www.bhaskar.com/rss-v1--category-1053.xml", Category: "sports"},
		{URL: "https://www.bhaskar.com/rss-v1--category-3998.xml", Category: "entertainment"},
		// national categories - Hindi source, all 8 regional languages (LiveHindustan)
		{URL: "https://api.livehindustan.com/feeds/rss/national/rssfeed.xml", Category: "national"},
		{URL: "https://api.livehindustan.com/feeds/rss/international/rssfeed.xml", Category: "international"},
		{URL: "https://api.livehindustan.com/feeds/rss/sports/rssfeed.xml", Category: "sports"},
		{URL: "https://api.livehindustan.com/feeds/rss/entertainment/rssfeed.xml", Category: "entertainment"},
		// Telangana - Telugu source
		{URL: "https://www.manatelangana.news/feed", Category: "telangana"},
		{URL: "https://ntvtelugu.com/feed", Category: "telangana"},
		// Tamil Nadu - Tamil source
		{URL: "https://tamil.oneindia.com/rss/feeds/oneindia-tamil-fb.xml", Category: "tamil_nadu"},
		// Kerala - Malayalam source
		{URL: "https://malayalam.oneindia.com/rss/feeds/oneindia-malayalam-fb.xml", Category: "kerala"},
		{URL: "https://www.onmanorama.com/kerala.feeds.onmrss.xml", Category: "kerala"},
		// Karnataka - Kannada source
		{URL: "https://kannada.oneindia.com/rss/feeds/oneindia-kannada-fb.xml", Category: "karnataka"},
	}

	totalProcessed := 0
	totalSkipped := 0
	totalFailed := 0
	var countersMutex sync.Mutex

	// Track in-flight links to prevent duplicate processing across goroutines
	var inFlightLinks sync.Map

	// Worker pool for processing news items concurrently
	// Use a buffered channel as semaphore to limit concurrency.
	// Peak concurrency is capped at maxConcurrentFeeds + maxConcurrentItems = 10.
	maxConcurrentItems := 8
	itemSemaphore := make(chan struct{}, maxConcurrentItems)
	var itemWg sync.WaitGroup

	// Worker pool for fetching RSS feeds concurrently
	maxConcurrentFeeds := 2
	feedSemaphore := make(chan struct{}, maxConcurrentFeeds)
	var feedWg sync.WaitGroup

	// Process each RSS feed concurrently
	for _, rssFeed := range rssFeeds {
		feedWg.Add(1)
		feedSemaphore <- struct{}{} // Acquire semaphore

		go func(rssFeed RSSFeed) {
			defer feedWg.Done()
			defer func() { <-feedSemaphore }() // Release semaphore
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[%s] PANIC in feed goroutine: %v\n", timestamp, r)
				}
			}()

			log.Printf("[%s] Parsing feed: %s (category: %s)\n", timestamp, rssFeed.URL, rssFeed.Category)

			// Create a new parser for each goroutine (gofeed.Parser is not thread-safe).
			// Some feeds (e.g. manatelangana.news) block the default gofeed User-Agent with 403,
			// so we use a browser-like User-Agent.
			fp := gofeed.NewParser()
			fp.Client = &http.Client{
				Timeout:   30 * time.Second,
				Transport: &browserUserAgentTransport{http.DefaultTransport},
			}
			feed, err := fp.ParseURL(rssFeed.URL)
			if err != nil {
				log.Printf("[%s] ✗ Failed to parse feed %s: %v\n", timestamp, rssFeed.URL, err)
				return
			}

			log.Printf("[%s] ✓ Found %d items in feed\n", timestamp, len(feed.Items))

			// Extract source hostname once per feed for content hashing
			sourceHost := rssFeed.URL
			if parsed, err := url.Parse(rssFeed.URL); err == nil {
				sourceHost = parsed.Hostname()
			}

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
				// Fallback: extract first <img src> from content:encoded
				// Handles feeds like manatelangana.news and ntvtelugu.com that
				// embed images in the HTML body instead of using standard media tags.
				if mediaLink == "" && item.Content != "" {
					mediaLink = extractImageFromContent(item.Content)
				}

				// Skip items without media link
				if mediaLink == "" {
					countersMutex.Lock()
					totalSkipped++
					countersMutex.Unlock()
					continue
				}

				// Parse published_at before hashing so the date is available
				publishedAt := parsePublishedAt(item)
				contentHash := computeContentHash(item.Title, publishedAt, sourceHost)

				// Check DB for duplicates: skip if link OR content hash already exists
				isDuplicate, err := checkDuplicateItem(db, item.Link, contentHash)
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

				// Claim link in-flight; skip if another goroutine is already processing it
				if _, alreadyProcessing := inFlightLinks.LoadOrStore(item.Link, true); alreadyProcessing {
					countersMutex.Lock()
					totalSkipped++
					countersMutex.Unlock()
					continue
				}
				// Claim hash in-flight; release link claim if the same content is already in-flight
				if _, alreadyProcessing := inFlightLinks.LoadOrStore(contentHash, true); alreadyProcessing {
					inFlightLinks.Delete(item.Link)
					countersMutex.Lock()
					totalSkipped++
					countersMutex.Unlock()
					continue
				}

				// Get target languages based on category
				targetLanguages := getLanguagesForCategory(rssFeed.Category)

				// Process news item concurrently
				itemWg.Add(1)
				itemSemaphore <- struct{}{} // Acquire semaphore

				go func(item *gofeed.Item, mediaLink string, publishedAt *time.Time, contentHash string, category string, targetLanguages []string) {
					defer itemWg.Done()
					defer func() { <-itemSemaphore }()
					defer inFlightLinks.Delete(item.Link)
					defer inFlightLinks.Delete(contentHash)
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[%s] PANIC in item goroutine: %v\n", timestamp, r)
						}
					}()

					geminiCtx, geminiCancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer geminiCancel()
					result, err := translateWithGemini(geminiCtx, genaiClient, item.Title, item.Description, targetLanguages, rateLimiter, category)
					if err != nil {
						log.Printf("[%s] ✗ Failed to convert/translate title '%s': %v\n", timestamp, item.Title, err)
						countersMutex.Lock()
						totalFailed++
						countersMutex.Unlock()
						return
					}
					if result.SkipItem {
						log.Printf("[%s] ⊘ [%s] Skipped: %s | title: %s\n", timestamp, category, result.SkipReason, item.Title)
						countersMutex.Lock()
						totalSkipped++
						countersMutex.Unlock()
						return
					}
					if result.BaseHeadline == "" || result.BaseSummary == "" {
						log.Printf("[%s] ✗ Gemini returned empty base headline or summary for '%s'\n", timestamp, item.Title)
						countersMutex.Lock()
						totalFailed++
						countersMutex.Unlock()
						return
					}
					for langCode, pair := range result.Translations {
						if pair.Headline == "" || pair.Summary == "" {
							log.Printf("[%s] ✗ Gemini returned empty %s translation for '%s'\n", timestamp, langCode, item.Title)
							countersMutex.Lock()
							totalFailed++
							countersMutex.Unlock()
							return
						}
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
					err = storeNewsWithTranslations(db, r2Client, r2BucketName, item.Link, contentHash, mediaFileKey, publishedAt, category, result)
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

				}(item, mediaLink, publishedAt, contentHash, rssFeed.Category, targetLanguages)
			}

			log.Printf("[%s] ✓ Finished processing feed: %s\n", timestamp, rssFeed.URL)
		}(rssFeed)
	}

	// Wait for all feeds to complete
	feedWg.Wait()

	// Wait for all news items to complete
	itemWg.Wait()

	log.Printf("[%s] ✓ RSS news feed parsing completed: %d processed, %d skipped, %d failed\n", timestamp, totalProcessed, totalSkipped, totalFailed)

	// Emit PostHog events with final counts
	sendPostHogNewsParsingEvent(posthogClient, posthogConfig, PostHogEventNewsParsingSucceeded, totalProcessed)
	sendPostHogNewsParsingEvent(posthogClient, posthogConfig, PostHogEventNewsParsingFailed, totalFailed)

	os.Exit(0)
}

// translateWithGemini determines the base language and additional translation targets from
// targetLanguages, then delegates to callGeminiTranslate.
//
// If "hi" is in targetLanguages it is the base language (Hindi-source feeds).
// Otherwise the first language is the base (regional-source feeds, e.g. "te").
// Uses description if available and non-empty, otherwise falls back to title.
func translateWithGemini(ctx context.Context, client *genai.Client, title, description string, targetLanguages []string, rateLimiter *rate.Limiter, category string) (TranslationResult, error) {
	if err := rateLimiter.Wait(ctx); err != nil {
		return TranslationResult{}, fmt.Errorf("rate limiter wait failed: %w", err)
	}
	if len(targetLanguages) == 0 {
		return TranslationResult{}, fmt.Errorf("no target languages specified for category")
	}

	sourceText := title
	if description != "" {
		sourceText = description
	}

	// Determine base language and additional translations
	var baseLang string
	var additionalLangs []string
	if slices.Contains(targetLanguages, "hi") {
		baseLang = "hi"
		for _, lang := range targetLanguages {
			if lang != "hi" {
				additionalLangs = append(additionalLangs, lang)
			}
		}
	} else {
		baseLang = targetLanguages[0]
		additionalLangs = targetLanguages[1:]
	}

	return callGeminiTranslate(ctx, client, sourceText, baseLang, additionalLangs, category)
}

// callGeminiTranslate converts source content into a headline and short summary in baseLang,
// then translates both to each language in additionalLangs.
//
// To add a new language: add its code→name entry to languageCodeToName, then add the code to
// the relevant categories in categoryLanguageMapping. No changes needed here.
func callGeminiTranslate(ctx context.Context, client *genai.Client, sourceText, baseLang string, additionalLangs []string, category string) (TranslationResult, error) {
	baseLangName := languageCodeToName(baseLang)

	// Build the optional translation clause and the corresponding JSON fields dynamically.
	// Adding a new language requires no change here — only languageCodeToName + categoryLanguageMapping.
	translateClause := ""
	translationFields := ""
	if len(additionalLangs) > 0 {
		names := make([]string, len(additionalLangs))
		for i, code := range additionalLangs {
			names[i] = languageCodeToName(code)
		}
		translateClause = fmt.Sprintf(", then translate both to: %s", strings.Join(names, ", "))
		var sb strings.Builder
		for _, name := range names {
			lower := strings.ToLower(name)
			fmt.Fprintf(&sb, ",\n  \"%s_headline\": \"headline translated to %s\",\n  \"%s_summary\": \"summary translated to %s\"", lower, name, lower, name)
		}
		translationFields = sb.String()
	}

	filterPreamble := buildFilterPreamble(category)

	prompt := fmt.Sprintf(`%s

Convert the following %s news content into a %s news headline and a %s news poster short summary%s.

%s

%s

Respond ONLY with a JSON object in this exact format:
{
  "skip": "false",
  "base_headline": "%s headline here",
  "base_summary": "%s short summary here"%s
}

Original %s news content: "%s"`,
		filterPreamble,
		baseLangName, baseLangName, baseLangName, translateClause,
		llmHeadlineGuidelines, llmSummaryGuidelines,
		baseLangName, baseLangName, translationFields,
		baseLangName, sourceText)

	raw, err := callGeminiAPIIntoMap(ctx, client, prompt)
	if err != nil {
		return TranslationResult{}, err
	}

	if raw["skip"] == "true" {
		return TranslationResult{SkipItem: true, SkipReason: strings.TrimSpace(raw["skip_reason"])}, nil
	}

	translations := make(map[string]TranslationPair, len(additionalLangs))
	for _, code := range additionalLangs {
		lower := strings.ToLower(languageCodeToName(code))
		translations[code] = TranslationPair{
			Headline: strings.TrimSpace(raw[lower+"_headline"]),
			Summary:  strings.TrimSpace(raw[lower+"_summary"]),
		}
	}

	return TranslationResult{
		BaseLanguageCode: baseLang,
		BaseHeadline:     strings.TrimSpace(raw["base_headline"]),
		BaseSummary:      strings.TrimSpace(raw["base_summary"]),
		Translations:     translations,
	}, nil
}

// languageCodeToName converts language code to full name
func languageCodeToName(code string) string {
	switch code {
	case "hi":
		return "Hindi"
	case "en":
		return "English"
	case "pa":
		return "Punjabi"
	case "gu":
		return "Gujarati"
	case "mr":
		return "Marathi"
	case "bn":
		return "Bengali"
	case "te":
		return "Telugu"
	case "ta":
		return "Tamil"
	case "ml":
		return "Malayalam"
	case "kn":
		return "Kannada"
	default:
		return code
	}
}

// callGeminiAPIIntoMap calls the Gemini API and unmarshals the JSON response into a flat string map.
func callGeminiAPIIntoMap(ctx context.Context, client *genai.Client, prompt string) (map[string]string, error) {
	raw, err := callGeminiAPI(ctx, client, prompt)
	if err != nil {
		return nil, err
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini response: %w", err)
	}
	return result, nil
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

// checkDuplicateItem returns true if a news record already exists matching either the article
// link or the content hash. Both checks together catch the same article re-appearing under a
// different URL in a later run.
func checkDuplicateItem(db *gorm.DB, link, contentHash string) (bool, error) {
	var existing dsmodels.News
	result := db.Where("link = ? OR content_hash = ?", link, contentHash).First(&existing)
	if result.Error == nil {
		return true, nil
	}
	if result.Error != gorm.ErrRecordNotFound {
		return false, fmt.Errorf("error checking duplicate: %w", result.Error)
	}
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

// storeNewsWithTranslations stores news and translations atomically.
// If the DB transaction fails and media was already uploaded to R2, it cleans up the orphaned file.
func storeNewsWithTranslations(db *gorm.DB, r2Client *storage.R2Client, bucketName, link, contentHash string, mediaFileKey *string, publishedAt *time.Time, category string, result TranslationResult) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Create news record
		news := dsmodels.News{
			Link:         link,
			ContentHash:  &contentHash,
			Category:     category,
			Status:       "approved",
			PublishedAt:  publishedAt,
			MediaFileKey: mediaFileKey,
		}

		if err := tx.Create(&news).Error; err != nil {
			return fmt.Errorf("failed to create news: %w", err)
		}

		// Create translations (news.ID is populated after Create)
		translationsToCreate := []dsmodels.NewsTranslation{
			{NewsID: news.ID, Title: result.BaseHeadline, Summary: result.BaseSummary, LanguageCode: result.BaseLanguageCode},
		}

		// Add translated headlines and summaries
		for langCode, pair := range result.Translations {
			translationsToCreate = append(translationsToCreate, dsmodels.NewsTranslation{
				NewsID:       news.ID,
				Title:        strings.TrimSpace(pair.Headline),
				Summary:      strings.TrimSpace(pair.Summary),
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

// ==================== PostHog Analytics Events ====================

// NewsParsingEventProperties contains properties for news parsing events
type NewsParsingEventProperties struct {
	Count int
}

// ToProperties converts NewsParsingEventProperties to a map
func (p NewsParsingEventProperties) ToProperties() map[string]any {
	return map[string]any{
		"count": p.Count,
	}
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
	eventName string,
	count int,
) {
	if config == nil {
		return
	}

	props := NewsParsingEventProperties{
		Count: count,
	}

	// Use the app name as distinct_id for cron job events
	distinctID := constants.AppNameDailyStory

	log.Printf("[PostHog] Processing %s event with count: %d\n", eventName, count)

	if sendErr := client.SendEvent(config.Host, config.APIKey, eventName, distinctID, props.ToProperties()); sendErr != nil {
		log.Printf("[PostHog ERROR] Failed to send %s event: %v\n", eventName, sendErr)
	}
}
