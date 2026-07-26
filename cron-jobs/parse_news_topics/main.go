package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	dsmodels "go-backend/internal/apps/dailystory/models"
	"go-backend/internal/common/database"
	"go-backend/internal/cron/newsutils"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/pgvector/pgvector-go"
	"golang.org/x/time/rate"
	"google.golang.org/genai"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ==================== Topic Config ====================

type topicInfo struct {
	Key          string   // DB category value
	SearchQuery  string   // Serper query string
	LangCode     string   // Serper hl param
	Languages    []string // translation targets stored in news_translations (first = base)
	ImageContext string   // scene/geographic context for the image generation prompt
}

var allLanguages = []string{"en", "hi", "pa", "gu", "mr", "bn", "te", "ta", "ml", "kn"}

var topics = []topicInfo{
	{
		Key: "india", SearchQuery: "india",
		LangCode:     "en",
		Languages:    allLanguages,
		ImageContext: "India",
	},
	{
		Key: "world", SearchQuery: "world news",
		LangCode:     "en",
		Languages:    allLanguages,
		ImageContext: "Global / international",
	},
	{
		Key: "sports", SearchQuery: "sports",
		LangCode:     "hi",
		Languages:    allLanguages,
		ImageContext: "Sports / athletics",
	},
	{
		Key: "entertainment", SearchQuery: "entertainment",
		LangCode:     "hi",
		Languages:    allLanguages,
		ImageContext: "Entertainment",
	},
}

func getCategoryCapLimit() int {
	ist := time.FixedZone("IST", 19800)
	hour := time.Now().In(ist).Hour()
	switch {
	case hour >= 6 && hour < 9:
		return 6
	case hour >= 9 && hour < 12:
		return 15
	default:
		return 30
	}
}

// ==================== Serper API ====================

const serperEndpoint = "https://google.serper.dev/news"

type serperQuery struct {
	Q   string `json:"q"`
	Gl  string `json:"gl"`
	Hl  string `json:"hl"`
	Tbs string `json:"tbs"`
}

type serperNewsItem struct {
	Title    string `json:"title"`
	Link     string `json:"link"`
	Snippet  string `json:"snippet"`
	Date     string `json:"date"`
	Source   string `json:"source"`
	ImageURL string `json:"imageUrl"`
}

type serperBatchResult struct {
	SearchParameters struct {
		Q string `json:"q"`
	} `json:"searchParameters"`
	News []serperNewsItem `json:"news"`
}

// ==================== Translation ====================

type translationPair struct {
	Headline string
	Summary  string
}

type translationResult struct {
	BaseLanguageCode string
	BaseHeadline     string
	BaseSummary      string
	Translations     map[string]translationPair
	ImagePrompt      string
}

// ==================== Gemini constants ====================

const (
	geminiModel                 = "gemini-2.5-flash-lite"
	embeddingModel              = "gemini-embedding-001"
	embeddingDimensions         = 768
	llmRateLimitPerMinute       = 8000
	embeddingRateLimitPerMinute = 2000

	categoryCapWindow  = 12 * time.Hour
	embeddingBatchSize = 20
	dbBatchSize        = 50

	llmWorkers       = 50
	embeddingWorkers = 5
	dbWorkers        = 9
)

const llmHeadlineGuidelines = `Headline guidelines:
- Create a concise news headline that captures the essence of the news
- Important: The headline must be at max 6 words long only`

const llmSummaryGuidelines = `Summary guidelines:
- Convert the content into a single factual short summary that summarizes key information
- Important: The summary must be at max 3-4 sentences long only`

// ==================== Semantic dedup config ====================

var (
	semanticDedupEnabled   bool
	semanticDedupThreshold float64
	semanticDedupWindow    time.Duration
)

// ==================== Shared HTTP client ====================

var httpClient = &http.Client{Timeout: 30 * time.Second}

// ==================== Pipeline types ====================

type rawNewsItem struct {
	item        serperNewsItem
	entry       topicInfo
	contentHash string
	done        func()
}

type translatedNewsItem struct {
	rawNewsItem
	result translationResult
}

type embeddedNewsItem struct {
	translatedNewsItem
	embedding []float32
}

// ==================== Per-category mutex map ====================

type categoryLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (c *categoryLocks) get(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.locks[key]; ok {
		return m
	}
	m := &sync.Mutex{}
	c.locks[key] = m
	return m
}

// ==================== main ====================

func main() {
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	startTime := time.Now()
	timestamp := startTime.Format("2006-01-02 15:04:05")
	log.Printf("[%s] Starting topic news parser\n", timestamp)

	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	if geminiAPIKey == "" {
		log.Fatalf("[%s] ✗ GEMINI_API_KEY is required\n", timestamp)
	}

	serperAPIKey := os.Getenv("SERPER_API_KEY")
	if serperAPIKey == "" {
		log.Fatalf("[%s] ✗ SERPER_API_KEY is required\n", timestamp)
	}

	llmRateLimiter := rate.NewLimiter(rate.Every(time.Minute/time.Duration(llmRateLimitPerMinute)), 1)
	embeddingRateLimiter := rate.NewLimiter(rate.Every(time.Minute/time.Duration(embeddingRateLimitPerMinute)), 1)

	semanticDedupEnabled = utils.GetEnv("SEMANTIC_DEDUP_ENABLED", "true") != "false"
	semanticDedupThreshold = 0.90
	if t, err := strconv.ParseFloat(utils.GetEnv("SEMANTIC_DEDUP_THRESHOLD", "0.90"), 64); err == nil {
		semanticDedupThreshold = t
	}
	semanticDedupWindow = 48 * time.Hour
	if h, err := strconv.Atoi(utils.GetEnv("SEMANTIC_DEDUP_WINDOW_HOURS", "48")); err == nil && h > 0 {
		semanticDedupWindow = time.Duration(h) * time.Hour
	}
	log.Printf("[%s] Semantic dedup: enabled=%t threshold=%.2f window=%s\n", timestamp, semanticDedupEnabled, semanticDedupThreshold, semanticDedupWindow)

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
	log.Printf("[%s] ✓ Database connected\n", timestamp)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: geminiAPIKey})
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to create Gemini client: %v\n", timestamp, err)
	}

	log.Printf("[%s] Total topics: %d\n", timestamp, len(topics))

	// Phase 0: pre-load category caps and dedup store
	fullCategories := loadFullCategories(db, timestamp)
	var skippedTopics int
	var remaining []topicInfo
	for _, t := range topics {
		if _, ok := fullCategories[t.Key]; ok {
			skippedTopics++
		} else {
			remaining = append(remaining, t)
		}
	}
	log.Printf("[%s] Topics skipped (cap reached): %d, remaining: %d\n", timestamp, skippedTopics, len(remaining))

	store := loadDedupStore(db, timestamp)

	var totalProcessed, totalSkipped, totalFailed int64
	var inFlightLinks sync.Map
	catLk := &categoryLocks{locks: make(map[string]*sync.Mutex)}

	// Channels
	rawNewsItemsCh := make(chan rawNewsItem, 500)
	translatedNewsItemsCh := make(chan translatedNewsItem, 500)
	embeddedNewsItemsCh := make(chan embeddedNewsItem, 50)
	newsItemBatchesCh := make(chan []translatedNewsItem, 10)
	dbBatchesCh := make(chan []embeddedNewsItem, 10)

	// ── Phase 4a: DB workers ─────────────────────────────────────────
	var dbWg sync.WaitGroup
	for range dbWorkers {
		dbWg.Add(1)
		go func() {
			defer dbWg.Done()
			for batch := range dbBatchesCh {
				processBatch(db, batch, store, catLk, timestamp, &totalProcessed, &totalSkipped, &totalFailed)
			}
		}()
	}

	// ── Phase 4b: DB batcher ─────────────────────────────────────────
	var dbBatcherWg sync.WaitGroup
	dbBatcherWg.Add(1)
	go func() {
		defer dbBatcherWg.Done()
		defer close(dbBatchesCh)
		batch := make([]embeddedNewsItem, 0, dbBatchSize)
		for item := range embeddedNewsItemsCh {
			batch = append(batch, item)
			if len(batch) == dbBatchSize {
				select {
				case dbBatchesCh <- batch:
					batch = make([]embeddedNewsItem, 0, dbBatchSize)
				case <-ctx.Done():
					for _, it := range batch {
						it.done()
						atomic.AddInt64(&totalSkipped, 1)
					}
					return
				}
			}
		}
		if len(batch) > 0 {
			select {
			case dbBatchesCh <- batch:
			case <-ctx.Done():
				for _, it := range batch {
					it.done()
					atomic.AddInt64(&totalSkipped, 1)
				}
			}
		}
	}()

	// ── Phase 3: Embedding ───────────────────────────────────────────
	var embedWg sync.WaitGroup
	var embBatcherWg sync.WaitGroup

	if semanticDedupEnabled {
		for range embeddingWorkers {
			embedWg.Add(1)
			go func() {
				defer embedWg.Done()
				for batch := range newsItemBatchesCh {
					embeddings, err := generateEmbeddingBatch(ctx, genaiClient, batch, embeddingRateLimiter)
					if err != nil {
						outcome := &totalFailed
						if ctx.Err() != nil {
							outcome = &totalSkipped
						}
						for _, item := range batch {
							item.done()
							atomic.AddInt64(outcome, 1)
						}
						continue
					}
					for i, item := range batch {
						embedded := embeddedNewsItem{translatedNewsItem: item, embedding: embeddings[i]}
						select {
						case embeddedNewsItemsCh <- embedded:
						case <-ctx.Done():
							item.done()
							atomic.AddInt64(&totalSkipped, 1)
						}
					}
				}
			}()
		}

		embBatcherWg.Add(1)
		go func() {
			defer embBatcherWg.Done()
			defer close(newsItemBatchesCh)
			batch := make([]translatedNewsItem, 0, embeddingBatchSize)
			for item := range translatedNewsItemsCh {
				batch = append(batch, item)
				if len(batch) == embeddingBatchSize {
					select {
					case newsItemBatchesCh <- batch:
						batch = make([]translatedNewsItem, 0, embeddingBatchSize)
					case <-ctx.Done():
						for _, it := range batch {
							it.done()
							atomic.AddInt64(&totalSkipped, 1)
						}
						return
					}
				}
			}
			if len(batch) > 0 {
				select {
				case newsItemBatchesCh <- batch:
				case <-ctx.Done():
					for _, it := range batch {
						it.done()
						atomic.AddInt64(&totalSkipped, 1)
					}
				}
			}
		}()
	} else {
		close(newsItemBatchesCh)
		embBatcherWg.Add(1)
		go func() {
			defer embBatcherWg.Done()
			for item := range translatedNewsItemsCh {
				embedded := embeddedNewsItem{translatedNewsItem: item, embedding: nil}
				select {
				case embeddedNewsItemsCh <- embedded:
				case <-ctx.Done():
					item.done()
					atomic.AddInt64(&totalSkipped, 1)
				}
			}
		}()
	}

	// ── Phase 2: LLM workers ────────────────────────────────────────
	var llmWg sync.WaitGroup
	for range llmWorkers {
		llmWg.Add(1)
		go func() {
			defer llmWg.Done()
			for raw := range rawNewsItemsCh {
				if ctx.Err() != nil {
					raw.done()
					atomic.AddInt64(&totalSkipped, 1)
					continue
				}

				result, err := callGeminiTranslate(ctx, genaiClient, raw.item.Title, raw.item.Snippet, raw.entry, llmRateLimiter)
				if err != nil {
					log.Printf("[%s] ✗ Gemini failed for '%s': %v\n", timestamp, raw.item.Title, err)
					raw.done()
					if ctx.Err() != nil {
						atomic.AddInt64(&totalSkipped, 1)
					} else {
						atomic.AddInt64(&totalFailed, 1)
					}
					continue
				}

				if result.BaseHeadline == "" || result.BaseSummary == "" || result.ImagePrompt == "" {
					log.Printf("[%s] ✗ Empty headline/summary/prompt for '%s'\n", timestamp, raw.item.Title)
					raw.done()
					atomic.AddInt64(&totalFailed, 1)
					continue
				}
				valid := true
				for langCode, pair := range result.Translations {
					if pair.Headline == "" || pair.Summary == "" {
						log.Printf("[%s] ✗ Empty %s translation for '%s'\n", timestamp, langCode, raw.item.Title)
						valid = false
						break
					}
				}
				if !valid {
					raw.done()
					atomic.AddInt64(&totalFailed, 1)
					continue
				}

				translated := translatedNewsItem{rawNewsItem: raw, result: result}
				select {
				case translatedNewsItemsCh <- translated:
				case <-ctx.Done():
					raw.done()
					atomic.AddInt64(&totalSkipped, 1)
				}
			}
		}()
	}

	// ── Phase 1: Serper fetch (synchronous — all topics fit in one batch) ──
	if len(remaining) > 0 && ctx.Err() == nil {
		localRng := rand.New(rand.NewSource(time.Now().UnixNano()))
		log.Printf("[%s] Fetching Serper batch (1-%d)\n", timestamp, len(remaining))

		results, err := fetchSerperBatch(ctx, remaining, serperAPIKey, localRng, timestamp)
		if err != nil {
			log.Printf("[%s] ✗ Serper fetch error: %v\n", timestamp, err)
		} else {
			// Key on SearchQuery (lowercased) so the lookup matches whatever
			// Serper echoes back in searchParameters.q regardless of case.
			queryToEntry := make(map[string]topicInfo, len(remaining))
			for _, e := range remaining {
				queryToEntry[strings.ToLower(e.SearchQuery)] = e
			}

		outer:
			for _, result := range results {
				entry, ok := queryToEntry[strings.ToLower(result.SearchParameters.Q)]
				if !ok {
					log.Printf("[%s] ✗ No matching topic for Serper result q=%q\n", timestamp, result.SearchParameters.Q)
					continue
				}

				for _, item := range result.News {
					if ctx.Err() != nil {
						break outer
					}

					item.Title = strings.TrimSpace(item.Title)
					item.Snippet = strings.TrimSpace(item.Snippet)

					if item.Title == "" || item.Link == "" {
						atomic.AddInt64(&totalSkipped, 1)
						continue
					}

					contentHash := newsutils.ComputeContentHash(item.Title, item.Source)

					if store.Contains(item.Link, contentHash) {
						atomic.AddInt64(&totalSkipped, 1)
						continue
					}

					if _, alreadyProcessing := inFlightLinks.LoadOrStore(item.Link, struct{}{}); alreadyProcessing {
						atomic.AddInt64(&totalSkipped, 1)
						continue
					}
					if _, alreadyProcessing := inFlightLinks.LoadOrStore(contentHash, struct{}{}); alreadyProcessing {
						inFlightLinks.Delete(item.Link)
						atomic.AddInt64(&totalSkipped, 1)
						continue
					}

					link := item.Link
					hash := contentHash
					raw := rawNewsItem{
						item:        item,
						entry:       entry,
						contentHash: contentHash,
						done: func() {
							inFlightLinks.Delete(link)
							inFlightLinks.Delete(hash)
						},
					}

					select {
					case rawNewsItemsCh <- raw:
					case <-ctx.Done():
						raw.done()
						atomic.AddInt64(&totalSkipped, 1)
						break outer
					}
				}
			}
		}
	}
	close(rawNewsItemsCh)

	// ── Teardown: drain in dependency order ──────────────────────────
	llmWg.Wait()
	close(translatedNewsItemsCh)

	embBatcherWg.Wait()
	embedWg.Wait()
	close(embeddedNewsItemsCh)

	dbBatcherWg.Wait()
	dbWg.Wait()

	elapsed := time.Since(startTime)
	if ctx.Err() != nil {
		log.Printf("[%s] ⚠ Run interrupted after %dm %02ds: %d processed, %d skipped, %d failed\n",
			timestamp, int(elapsed.Minutes()), int(elapsed.Seconds())%60, totalProcessed, totalSkipped, totalFailed)
	} else {
		log.Printf("[%s] ✓ Completed in %dm %02ds: %d processed, %d skipped, %d failed\n",
			timestamp, int(elapsed.Minutes()), int(elapsed.Seconds())%60, totalProcessed, totalSkipped, totalFailed)
	}
}

// ==================== Serper batch fetch ====================

func fetchSerperBatch(ctx context.Context, entries []topicInfo, apiKey string, rng *rand.Rand, timestamp string) ([]serperBatchResult, error) {
	queries := make([]serperQuery, len(entries))
	for i, e := range entries {
		queries[i] = serperQuery{
			Q:   e.SearchQuery,
			Gl:  "in",
			Hl:  e.LangCode,
			Tbs: "qdr:d",
		}
	}

	body, err := json.Marshal(queries)
	if err != nil {
		return nil, err
	}

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			jitter := time.Duration(2*attempt)*time.Second + time.Duration(rng.Intn(1000))*time.Millisecond
			log.Printf("[%s] Serper retry %d/%d in %v\n", timestamp, attempt, maxAttempts, jitter.Round(time.Millisecond))
			select {
			case <-time.After(jitter):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequest(http.MethodPost, serperEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-API-KEY", apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("serper returned %d: %s", resp.StatusCode, string(respBody))
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return nil, lastErr
			}
			continue
		}

		var results []serperBatchResult
		if err := json.Unmarshal(respBody, &results); err != nil {
			return nil, fmt.Errorf("failed to parse serper response: %w", err)
		}
		return results, nil
	}
	return nil, fmt.Errorf("all %d attempts failed: %w", maxAttempts, lastErr)
}

// ==================== Category cap ====================

func loadFullCategories(db *gorm.DB, timestamp string) map[string]struct{} {
	since := time.Now().Add(-categoryCapWindow)
	var rows []struct {
		Category string
		Count    int64
	}
	err := db.Raw(`
		SELECT category, COUNT(*) AS count
		FROM news
		WHERE sub_category IS NULL AND created_at >= ?
		GROUP BY category
		HAVING COUNT(*) >= ?`, since, getCategoryCapLimit()).Scan(&rows).Error
	if err != nil {
		log.Printf("[%s] ✗ Failed to load category caps: %v\n", timestamp, err)
		return map[string]struct{}{}
	}
	full := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		full[r.Category] = struct{}{}
	}
	return full
}

func categoryCount(db *gorm.DB, topicKey string) (int64, error) {
	since := time.Now().Add(-categoryCapWindow)
	var count int64
	err := db.Model(&dsmodels.News{}).
		Where("category = ? AND sub_category IS NULL AND created_at >= ?", topicKey, since).
		Count(&count).Error
	return count, err
}

// ==================== Dedup store ====================

func loadDedupStore(db *gorm.DB, timestamp string) *newsutils.DedupStore {
	store := &newsutils.DedupStore{}
	since := time.Now().Add(-semanticDedupWindow)

	var newsRows []struct {
		Link        string
		ContentHash *string
	}
	if err := db.Model(&dsmodels.News{}).
		Select("link, content_hash").
		Where("created_at >= ?", since).
		Find(&newsRows).Error; err != nil {
		log.Printf("[%s] ✗ Failed to load news dedup store: %v\n", timestamp, err)
	} else {
		for _, r := range newsRows {
			store.LoadEntry(r.Link, r.ContentHash)
		}
	}

	var simRows []struct {
		Link        string
		ContentHash *string
	}
	if err := db.Model(&dsmodels.SimilarNews{}).
		Select("link, content_hash").
		Where("created_at >= ?", since).
		Find(&simRows).Error; err != nil {
		log.Printf("[%s] ✗ Failed to load similar_news dedup store: %v\n", timestamp, err)
	} else {
		for _, r := range simRows {
			store.LoadEntry(r.Link, r.ContentHash)
		}
	}

	log.Printf("[%s] ✓ Dedup store loaded\n", timestamp)
	return store
}

// ==================== Gemini ====================

func callGeminiTranslate(ctx context.Context, client *genai.Client, title, snippet string, topic topicInfo, rateLimiter *rate.Limiter) (translationResult, error) {
	if err := rateLimiter.Wait(ctx); err != nil {
		return translationResult{}, fmt.Errorf("rate limiter: %w", err)
	}
	if len(topic.Languages) == 0 {
		return translationResult{}, fmt.Errorf("topic %q has no languages configured", topic.Key)
	}

	baseLang := topic.Languages[0]
	additionalLangs := topic.Languages[1:]
	baseLangName := newsutils.LanguageCodeToName(baseLang)

	// Build names and lowercase keys in a single pass; reuse lowers during extraction.
	names := make([]string, len(additionalLangs))
	lowers := make([]string, len(additionalLangs))
	for i, code := range additionalLangs {
		names[i] = newsutils.LanguageCodeToName(code)
		lowers[i] = strings.ToLower(names[i])
	}

	translateClause := ""
	translationFields := ""
	if len(names) > 0 {
		var sb strings.Builder
		for i, name := range names {
			fmt.Fprintf(&sb, ",\n  \"%s_headline\": \"headline translated to %s\",\n  \"%s_summary\": \"summary translated to %s\"", lowers[i], name, lowers[i], name)
		}
		translateClause = fmt.Sprintf(", then translate both to: %s", strings.Join(names, ", "))
		translationFields = sb.String()
	}

	sourceText := title
	if snippet != "" {
		sourceText = title + ". " + snippet
	}

	prompt := fmt.Sprintf(`Convert the following %s news content into a %s news headline and a %s news poster short summary%s.
Then generate an English image generation prompt for this news article.

%s

%s

Image prompt guidelines:
- Write a cinematic, photorealistic scene in English for Flux Klein 4B; Klein renders exactly what you write so be specific and descriptive
- Style: cinematic photorealism, like a still frame from a high-budget film — realistic textures, natural anatomy, filmic depth of field, subtle grain
- Include geographic context: %s; add culturally relevant visual elements with authentic, grounded detail
- Lighting: use cinematic lighting — golden hour or blue hour tones, volumetric light, dramatic shadows, lens flare, rim lighting on subjects
- Composition: describe the layout clearly — wide establishing shot, medium two-shot, or close-up — with strong foreground/background depth separation and a clear focal point
- Characters: realistic human figures with natural proportions, expressive but believable faces and body language; avoid cartoonish or illustrated rendering
- Colors: cinematic color grading — rich contrast, filmic tones, warm/cool color separation to add depth
- If text must appear in the scene, describe it explicitly: specify the exact words in quotes (in English), placement, font style, and surface it appears on (e.g. a sign reading "VOTE 2024", bold rounded sans-serif, center frame)
- Purely visual scene; no typographic content anywhere in the frame unless text is explicitly requested above

Respond ONLY with a JSON object in this exact format:
{
  "base_headline": "%s headline here",
  "base_summary": "%s short summary here"%s,
  "image_prompt": "English image generation prompt here"
}

Original %s news content: "%s"`,
		baseLangName, baseLangName, baseLangName, translateClause,
		llmHeadlineGuidelines, llmSummaryGuidelines,
		topic.ImageContext,
		baseLangName, baseLangName, translationFields,
		baseLangName, sourceText)

	translateCtx, translateCancel := context.WithTimeout(ctx, 60*time.Second)
	defer translateCancel()

	raw, err := newsutils.CallGeminiAPIIntoMap(translateCtx, client, geminiModel, prompt)
	if err != nil {
		return translationResult{}, err
	}

	translations := make(map[string]translationPair, len(additionalLangs))
	for i, code := range additionalLangs {
		translations[code] = translationPair{
			Headline: strings.TrimSpace(raw[lowers[i]+"_headline"]),
			Summary:  strings.TrimSpace(raw[lowers[i]+"_summary"]),
		}
	}

	return translationResult{
		BaseLanguageCode: baseLang,
		BaseHeadline:     strings.TrimSpace(raw["base_headline"]),
		BaseSummary:      strings.TrimSpace(raw["base_summary"]),
		Translations:     translations,
		ImagePrompt:      strings.TrimSpace(raw["image_prompt"]),
	}, nil
}

// ==================== Embedding ====================

func generateEmbeddingBatch(ctx context.Context, client *genai.Client, items []translatedNewsItem, rateLimiter *rate.Limiter) ([][]float32, error) {
	if err := rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	dim := int32(embeddingDimensions)
	contents := make([]*genai.Content, len(items))
	for i, item := range items {
		embText := item.result.BaseHeadline + "\n" + item.result.BaseSummary
		contents[i] = &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: embText}},
		}
	}
	config := &genai.EmbedContentConfig{
		TaskType:             "SEMANTIC_SIMILARITY",
		OutputDimensionality: &dim,
	}

	var response *genai.EmbedContentResponse
	err := newsutils.RetryGemini(ctx, func() error {
		var err error
		response, err = client.Models.EmbedContent(ctx, embeddingModel, contents, config)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("embedding batch failed: %w", err)
	}
	if len(response.Embeddings) != len(items) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(items), len(response.Embeddings))
	}

	embeddings := make([][]float32, len(items))
	for i, emb := range response.Embeddings {
		vec := emb.Values
		newsutils.NormalizeL2(vec)
		embeddings[i] = vec
	}
	return embeddings, nil
}

// ==================== Phase 4: DB write ====================

type semanticDupInfo struct {
	CanonicalID uuid.UUID
	Similarity  float32
}

type intraDupInfo struct {
	item       embeddedNewsItem
	similarity float32
}

func processBatch(
	db *gorm.DB,
	batch []embeddedNewsItem,
	store *newsutils.DedupStore,
	catLk *categoryLocks,
	timestamp string,
	totalProcessed, totalSkipped, totalFailed *int64,
) {
	defer func() {
		for _, item := range batch {
			item.done()
		}
	}()

	type categoryGroup struct {
		topicKey string
		items    []embeddedNewsItem
	}
	var catOrder []string
	groups := make(map[string]*categoryGroup)
	for _, item := range batch {
		key := item.entry.Key
		if _, ok := groups[key]; !ok {
			catOrder = append(catOrder, key)
			groups[key] = &categoryGroup{topicKey: key}
		}
		groups[key].items = append(groups[key].items, item)
	}

	since := time.Now().Add(-semanticDedupWindow)

	for _, key := range catOrder {
		grp := groups[key]

		mu := catLk.get(key)
		mu.Lock()

		count, err := categoryCount(db, grp.topicKey)
		if err != nil {
			log.Printf("[%s] ✗ categoryCount failed for %s: %v\n", timestamp, key, err)
			mu.Unlock()
			for range grp.items {
				atomic.AddInt64(totalFailed, 1)
			}
			continue
		}

		remaining := int64(getCategoryCapLimit()) - count
		if remaining <= 0 {
			log.Printf("[%s] ⊘ [%s] Category cap reached\n", timestamp, key)
			mu.Unlock()
			for range grp.items {
				atomic.AddInt64(totalSkipped, 1)
			}
			continue
		}

		candidates := grp.items
		if int64(len(candidates)) > remaining {
			for _, item := range candidates[remaining:] {
				log.Printf("[%s] ⊘ [%s] Category cap trim | '%s'\n", timestamp, key, item.item.Title)
				atomic.AddInt64(totalSkipped, 1)
			}
			candidates = candidates[:remaining]
		}

		var toInsert []embeddedNewsItem
		if semanticDedupEnabled && len(candidates) > 0 {
			dupMap, err := bulkSemanticDedup(db, candidates, grp.topicKey, since)
			if err != nil {
				log.Printf("[%s] ✗ Semantic dedup failed for %s: %v\n", timestamp, key, err)
				mu.Unlock()
				for range candidates {
					atomic.AddInt64(totalFailed, 1)
				}
				continue
			}
			for i, item := range candidates {
				if info, isDup := dupMap[i]; isDup {
					log.Printf("[%s] ⊘ [%s] Semantic dup (%.3f) of %s | '%s'\n", timestamp, key, info.Similarity, info.CanonicalID, item.item.Title)
					recordSimilarNews(db, info.CanonicalID, item.item.Link, item.contentHash, grp.topicKey, info.Similarity)
					store.Add(item.item.Link, item.contentHash)
					atomic.AddInt64(totalSkipped, 1)
				} else {
					toInsert = append(toInsert, item)
				}
			}
		} else {
			toInsert = candidates
		}

		if semanticDedupEnabled && len(toInsert) > 1 {
			var intraDups []intraDupInfo
			toInsert, intraDups = intraBatchSemanticDedup(toInsert)
			for _, d := range intraDups {
				log.Printf("[%s] ⊘ [%s] Intra-batch semantic dup (%.3f) | '%s'\n", timestamp, key, d.similarity, d.item.item.Title)
				store.Add(d.item.item.Link, d.item.contentHash)
				atomic.AddInt64(totalSkipped, 1)
			}
		}

		if len(toInsert) > 0 {
			if err := bulkInsertNewsWithTranslations(db, toInsert, grp.topicKey); err != nil {
				log.Printf("[%s] ✗ Bulk insert failed for %s: %v\n", timestamp, key, err)
				mu.Unlock()
				for range toInsert {
					atomic.AddInt64(totalFailed, 1)
				}
				continue
			}
			for _, item := range toInsert {
				store.Add(item.item.Link, item.contentHash)
				atomic.AddInt64(totalProcessed, 1)
			}
		}

		mu.Unlock()
	}
}

func bulkSemanticDedup(db *gorm.DB, items []embeddedNewsItem, topicKey string, since time.Time) (map[int]semanticDupInfo, error) {
	valueParts := make([]string, len(items))
	args := make([]any, 0, len(items)+3)
	for i, item := range items {
		valueParts[i] = fmt.Sprintf("(%d, ?::vector)", i)
		args = append(args, pgvector.NewVector(item.embedding))
	}
	args = append(args, topicKey, since, semanticDedupThreshold)

	query := fmt.Sprintf(`
		WITH incoming(idx, embedding) AS (
			VALUES %s
		)
		SELECT i.idx, n.id, 1 - (n.embedding <=> i.embedding) AS similarity
		FROM incoming i
		CROSS JOIN LATERAL (
			SELECT id, embedding
			FROM news
			WHERE category = ? AND sub_category IS NULL AND embedding IS NOT NULL AND created_at >= ?
			ORDER BY embedding <=> i.embedding
			LIMIT 1
		) n
		WHERE 1 - (n.embedding <=> i.embedding) >= ?`,
		strings.Join(valueParts, ", "))

	var results []struct {
		Idx        int
		ID         uuid.UUID
		Similarity float64
	}
	if err := db.Raw(query, args...).Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("bulk semantic dedup query failed: %w", err)
	}

	dupMap := make(map[int]semanticDupInfo, len(results))
	for _, r := range results {
		dupMap[r.Idx] = semanticDupInfo{
			CanonicalID: r.ID,
			Similarity:  float32(r.Similarity),
		}
	}
	return dupMap, nil
}

func bulkInsertNewsWithTranslations(db *gorm.DB, items []embeddedNewsItem, topicKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		publishedAt := time.Now().UTC()

		newsItems := make([]dsmodels.News, len(items))
		for i, item := range items {
			n := dsmodels.News{
				Link:        item.item.Link,
				ContentHash: &item.contentHash,
				Category:    topicKey,
				SubCategory: nil,
				Status:      "approved",
				PublishedAt: &publishedAt,
			}
			if item.result.ImagePrompt != "" {
				n.ImagePrompt = &item.result.ImagePrompt
			}
			if len(item.embedding) > 0 {
				v := pgvector.NewVector(item.embedding)
				n.Embedding = &v
			}
			newsItems[i] = n
		}

		if err := tx.Create(&newsItems).Error; err != nil {
			return fmt.Errorf("failed to bulk create news: %w", err)
		}

		var translations []dsmodels.NewsTranslation
		for i, item := range items {
			newsID := newsItems[i].ID
			translations = append(translations, dsmodels.NewsTranslation{
				NewsID:       newsID,
				Title:        item.result.BaseHeadline,
				Summary:      item.result.BaseSummary,
				LanguageCode: item.result.BaseLanguageCode,
			})
			for langCode, pair := range item.result.Translations {
				translations = append(translations, dsmodels.NewsTranslation{
					NewsID:       newsID,
					Title:        pair.Headline,
					Summary:      pair.Summary,
					LanguageCode: langCode,
				})
			}
		}

		if err := tx.Create(&translations).Error; err != nil {
			return fmt.Errorf("failed to bulk create translations: %w", err)
		}
		return nil
	})
}

// ==================== Intra-batch semantic dedup ====================

func dotProduct(a, b []float32) float32 {
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

func intraBatchSemanticDedup(items []embeddedNewsItem) (accepted []embeddedNewsItem, dups []intraDupInfo) {
	for _, item := range items {
		if len(item.embedding) == 0 {
			accepted = append(accepted, item)
			continue
		}
		var maxSim float32
		for _, acc := range accepted {
			if len(acc.embedding) == 0 {
				continue
			}
			if sim := dotProduct(item.embedding, acc.embedding); sim > maxSim {
				maxSim = sim
			}
		}
		if maxSim >= float32(semanticDedupThreshold) {
			dups = append(dups, intraDupInfo{item: item, similarity: maxSim})
		} else {
			accepted = append(accepted, item)
		}
	}
	return
}

// ==================== Semantic dedup ====================

func recordSimilarNews(db *gorm.DB, canonicalID uuid.UUID, link, contentHash, topicKey string, similarity float32) {
	rec := dsmodels.SimilarNews{
		NewsID:          canonicalID,
		Link:            link,
		ContentHash:     &contentHash,
		Category:        topicKey,
		SubCategory:     nil,
		SimilarityScore: &similarity,
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rec).Error; err != nil {
		log.Printf("Failed to record similar_news for %s: %v", link, err)
	}
}
