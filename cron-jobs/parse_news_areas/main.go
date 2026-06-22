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
	posthogModels "go-backend/internal/apps/posthog/config/models"
	posthogRepository "go-backend/internal/apps/posthog/config/repository"
	"go-backend/internal/common/constants"
	"go-backend/internal/common/database"
	"go-backend/internal/cron/newsutils"
	"go-backend/pkg/analytics"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/pgvector/pgvector-go"
	"golang.org/x/time/rate"
	"google.golang.org/genai"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ==================== State / Area Config ====================

type stateInfo struct {
	Name      string
	StateKey  string   // lowercase_underscore form used as category
	LangCode  string   // primary language code for Serper hl param
	Languages []string // translation targets stored in news_translations (first = base)
	Areas     []string
}

// areaKey converts an area name to its lowercase_underscore key (sub_category value).
func areaKey(area string) string {
	return strings.ToLower(strings.ReplaceAll(area, " ", "_"))
}

func getAreaCapLimit() int {
	ist := time.FixedZone("IST", 19800) // UTC+5:30 (5*3600 + 30*60); FixedZone avoids system tzdata dependency
	hour := time.Now().In(ist).Hour()
	switch {
	case hour >= 6 && hour < 9:
		return 6
	case hour >= 9 && hour < 12:
		return 12
	default:
		return 20
	}
}

// 18 states, 573 areas total.
var states = []stateInfo{
	{
		Name: "Himachal Pradesh", StateKey: "himachal_pradesh", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Bilaspur", "Chamba", "Hamirpur", "Kangra", "Kinnaur", "Kullu",
			"Lahaul", "Spiti", "Mandi", "Shimla", "Sirmaur", "Solan", "Una",
		},
	},
	{
		Name: "Haryana", StateKey: "haryana", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Ambala", "Bhiwani", "Charkhi Dadri", "Faridabad", "Fatehabad",
			"Gurugram", "Hisar", "Jhajjar", "Jind", "Kaithal", "Karnal",
			"Kurukshetra", "Mahendragarh", "Nuh", "Palwal", "Panchkula",
			"Panipat", "Rewari", "Rohtak", "Sirsa", "Sonipat", "Yamunanagar",
		},
	},
	{
		Name: "Delhi", StateKey: "delhi", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Delhi",
		},
	},
	{
		Name: "Uttar Pradesh", StateKey: "uttar_pradesh", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Agra", "Aligarh", "Ambedkar Nagar", "Amethi", "Amroha", "Auraiya",
			"Ayodhya", "Azamgarh", "Baghpat", "Bahraich", "Ballia", "Balrampur",
			"Banda", "Barabanki", "Bareilly", "Basti", "Bhadohi", "Bijnor",
			"Budaun", "Bulandshahr", "Chandauli", "Chitrakoot", "Deoria", "Etah",
			"Etawah", "Farrukhabad", "Fatehpur", "Firozabad", "Gautam Buddha Nagar",
			"Ghaziabad", "Ghazipur", "Gonda", "Gorakhpur", "Hamirpur", "Hapur",
			"Hardoi", "Hathras", "Jalaun", "Jaunpur", "Jhansi", "Kannauj",
			"Kanpur Dehat", "Kanpur Nagar", "Kasganj", "Kaushambi", "Kushinagar",
			"Lakhimpur Kheri", "Lalitpur", "Lucknow", "Maharajganj", "Mahoba",
			"Mainpuri", "Mathura", "Mau", "Meerut", "Mirzapur", "Moradabad",
			"Muzaffarnagar", "Pilibhit", "Pratapgarh", "Prayagraj", "Rae Bareli",
			"Rampur", "Saharanpur", "Sambhal", "Sant Kabir Nagar", "Shahjahanpur",
			"Shamli", "Shravasti", "Siddharthnagar", "Sitapur", "Sonbhadra",
			"Sultanpur", "Unnao", "Varanasi",
		},
	},
	{
		Name: "Bihar", StateKey: "bihar", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Araria", "Arwal", "Aurangabad", "Banka", "Begusarai", "Bhagalpur",
			"Bhojpur", "Buxar", "Darbhanga", "East Champaran", "Gaya", "Gopalganj",
			"Jamui", "Jehanabad", "Kaimur", "Katihar", "Khagaria", "Kishanganj",
			"Lakhisarai", "Madhepura", "Madhubani", "Munger", "Muzaffarpur",
			"Nalanda", "Nawada", "Patna", "Purnia", "Rohtas", "Saharsa",
			"Samastipur", "Saran", "Sheikhpura", "Sheohar", "Sitamarhi",
			"Siwan", "Supaul", "Vaishali", "West Champaran",
		},
	},
	{
		Name: "Rajasthan", StateKey: "rajasthan", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Ajmer", "Alwar", "Anupgarh", "Balotra", "Banswara", "Baran",
			"Barmer", "Beawar", "Bharatpur", "Bhilwara", "Bikaner", "Bundi",
			"Chittorgarh", "Churu", "Dausa", "Deeg", "Didwana", "Kuchaman",
			"Dholpur", "Dudu", "Dungarpur", "Gangapurcity", "Ganganagar",
			"Hanumangarh", "Jaipur", "Jaipur Rural", "Jaisalmer", "Jalore",
			"Jhalawar", "Jhunjhunu", "Jodhpur", "Jodhpur Rural", "Karauli",
			"Kekri", "Khairthal", "Tijara", "Kota", "Kotputli", "Behror", "Nagaur",
			"Neem Ka Thana", "Pali", "Phalodi", "Pratapgarh", "Rajsamand",
			"Salumbar", "Sanchore", "Sawai Madhopur", "Shahpura", "Sikar",
			"Sirohi", "Tonk", "Udaipur",
		},
	},
	{
		Name: "Madhya Pradesh", StateKey: "madhya_pradesh", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Agar Malwa", "Alirajpur", "Anuppur", "Ashoknagar", "Balaghat",
			"Barwani", "Betul", "Bhind", "Bhopal", "Burhanpur", "Chhatarpur",
			"Chhindwara", "Damoh", "Datia", "Dewas", "Dhar", "Dindori", "Guna",
			"Gwalior", "Harda", "Indore", "Jabalpur", "Jhabua", "Katni",
			"Khandwa", "Khargone", "Maihar", "Mandla", "Mandsaur", "Mauganj",
			"Morena", "Narsinghpur", "Narmadapuram", "Neemuch", "Niwari",
			"Panna", "Pandhurna", "Raisen", "Rajgarh", "Ratlam", "Rewa", "Sagar",
			"Satna", "Sehore", "Seoni", "Shahdol", "Shajapur", "Sheopur",
			"Shivpuri", "Sidhi", "Singrauli", "Tikamgarh", "Ujjain", "Umaria",
			"Vidisha",
		},
	},
	{
		Name: "Jharkhand", StateKey: "jharkhand", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Bokaro", "Chatra", "Deoghar", "Dhanbad", "Dumka", "East Singhbhum",
			"Garhwa", "Giridih", "Godda", "Gumla", "Hazaribagh", "Jamtara",
			"Khunti", "Koderma", "Latehar", "Lohardaga", "Pakur", "Palamu",
			"Ramgarh", "Ranchi", "Sahebganj", "Seraikela", "Kharsawan", "Simdega",
			"West Singhbhum",
		},
	},
	{
		Name: "Chhattisgarh", StateKey: "chhattisgarh", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Balod", "Baloda Bazar", "Balrampur", "Bastar", "Bemetara", "Bijapur",
			"Bilaspur", "Dantewada", "Dhamtari", "Durg", "Gariyaband",
			"Gaurela", "Pendra", "Marwahi", "Janjgir", "Champa", "Jashpur", "Kabirdham",
			"Kanker", "Khairagarh", "Chhuikhadan", "Gandai", "Kondagaon", "Korba",
			"Koriya", "Mahasamund", "Manendragarh", "Chirmiri", "Bharatpur",
			"Mohla", "Manpur", "Mungeli", "Narayanpur", "Raigarh", "Raipur",
			"Rajnandgaon", "Sakti", "Sarangarh", "Bilaigarh", "Sukma", "Surajpur", "Surguja",
		},
	},
	{
		Name: "Uttarakhand", StateKey: "uttarakhand", LangCode: "hi", Languages: []string{"hi"},
		Areas: []string{
			"Almora", "Bageshwar", "Chamoli", "Champawat", "Dehradun",
			"Haridwar", "Nainital", "Pauri Garhwal", "Pithoragarh",
			"Rudraprayag", "Tehri Garhwal", "Uttarkashi",
		},
	},
	{
		Name: "Punjab", StateKey: "punjab", LangCode: "pa", Languages: []string{"pa", "hi"},
		Areas: []string{
			"Amritsar", "Barnala", "Bathinda", "Faridkot", "Fatehgarh Sahib",
			"Fazilka", "Ferozepur", "Gurdaspur", "Hoshiarpur", "Jalandhar",
			"Kapurthala", "Ludhiana", "Malerkotla", "Mansa", "Moga",
			"Mohali", "Muktsar", "Pathankot", "Patiala", "Rupnagar",
			"Sangrur", "Tarn Taran",
		},
	},
	{
		Name: "West Bengal", StateKey: "west_bengal", LangCode: "bn", Languages: []string{"bn", "hi"},
		Areas: []string{
			"Alipurduar", "Bankura", "Birbhum", "Cooch Behar", "Dakshin Dinajpur",
			"Darjeeling", "Hooghly", "Howrah", "Jalpaiguri", "Jhargram",
			"Kalimpong", "Kolkata", "Malda", "Murshidabad", "Nadia",
			"North 24 Parganas", "Paschim Bardhaman", "Paschim Medinipur",
			"Purba Bardhaman", "Purba Medinipur", "Purulia",
			"South 24 Parganas", "Uttar Dinajpur",
		},
	},
	{
		Name: "Gujarat", StateKey: "gujarat", LangCode: "gu", Languages: []string{"gu", "hi"},
		Areas: []string{
			"Ahmedabad", "Amreli", "Anand", "Aravalli", "Banaskantha", "Bharuch",
			"Bhavnagar", "Botad", "Chhota Udaipur", "Dahod", "Dang",
			"Devbhoomi Dwarka", "Gandhinagar", "Gir", "Somnath", "Jamnagar",
			"Junagadh", "Kheda", "Kutch", "Mahisagar", "Mehsana", "Morbi",
			"Narmada", "Navsari", "Panchmahal", "Patan", "Porbandar", "Rajkot",
			"Sabarkantha", "Surat", "Surendranagar", "Tapi", "Vadodara", "Valsad",
		},
	},
	{
		Name: "Maharashtra", StateKey: "maharashtra", LangCode: "mr", Languages: []string{"mr", "hi"},
		Areas: []string{
			"Ahmednagar", "Akola", "Amravati", "Chhatrapati Sambhajinagar",
			"Beed", "Bhandara", "Buldhana", "Chandrapur", "Dhule", "Gadchiroli",
			"Gondia", "Hingoli", "Jalgaon", "Jalna", "Kolhapur", "Latur",
			"Mumbai", "Mumbai", "Nagpur", "Nanded", "Nandurbar",
			"Nashik", "Dharashiv", "Palghar", "Parbhani", "Pune", "Raigad",
			"Ratnagiri", "Sangli", "Satara", "Sindhudurg", "Solapur", "Thane",
			"Wardha", "Washim", "Yavatmal",
		},
	},
	{
		Name: "Telangana", StateKey: "telangana", LangCode: "te", Languages: []string{"te", "en"},
		Areas: []string{
			"Adilabad", "Bhadradri", "Kothagudem", "Hanumakonda", "Hyderabad",
			"Jagtial", "Jangaon", "Jayashankar", "Bhupalpally", "Jogulamba", "Gadwal",
			"Kamareddy", "Karimnagar", "Khammam", "Kumuram Bheem", "Asifabad",
			"Mahabubabad", "Mahabubnagar", "Mancherial", "Medak",
			"Medchal", "Malkajgiri", "Mulugu", "Nagarkurnool", "Nalgonda",
			"Narayanpet", "Nirmal", "Nizamabad", "Peddapalli", "Rajanna", "Sircilla",
			"Rangareddy", "Sangareddy", "Siddipet", "Suryapet", "Vikarabad",
			"Wanaparthy", "Warangal", "Yadadri", "Bhuvanagiri",
		},
	},
	{
		Name: "Tamil Nadu", StateKey: "tamil_nadu", LangCode: "ta", Languages: []string{"ta", "en"},
		Areas: []string{
			"Ariyalur", "Chengalpattu", "Chennai", "Coimbatore", "Cuddalore",
			"Dharmapuri", "Dindigul", "Erode", "Kallakurichi", "Kancheepuram",
			"Kanyakumari", "Karur", "Krishnagiri", "Madurai", "Mayiladuthurai",
			"Nagapattinam", "Namakkal", "Nilgiris", "Perambalur", "Pudukkottai",
			"Ramanathapuram", "Ranipet", "Salem", "Sivaganga", "Tenkasi",
			"Thanjavur", "Theni", "Thoothukudi", "Tiruchirappalli", "Tirunelveli",
			"Tirupathur", "Tiruppur", "Tiruvallur", "Tiruvannamalai", "Tiruvarur",
			"Vellore", "Villupuram", "Virudhunagar",
		},
	},
	{
		Name: "Kerala", StateKey: "kerala", LangCode: "ml", Languages: []string{"ml", "en"},
		Areas: []string{
			"Alappuzha", "Ernakulam", "Idukki", "Kannur", "Kasaragod", "Kollam",
			"Kottayam", "Kozhikode", "Malappuram", "Palakkad", "Pathanamthitta",
			"Thiruvananthapuram", "Thrissur", "Wayanad",
		},
	},
	{
		Name: "Karnataka", StateKey: "karnataka", LangCode: "kn", Languages: []string{"kn", "en"},
		Areas: []string{
			"Bagalkot", "Ballari", "Belagavi", "Bengaluru",
			"Bidar", "Chamarajanagar", "Chikkaballapur", "Chikkamagaluru",
			"Chitradurga", "Dakshina Kannada", "Davanagere", "Dharwad", "Gadag",
			"Hassan", "Haveri", "Kalaburagi", "Kodagu", "Kolar", "Koppal",
			"Mandya", "Mysuru", "Raichur", "Ramanagara", "Shivamogga", "Tumakuru",
			"Udupi", "Uttara Kannada", "Vijayapura", "Vijayanagara", "Yadgir",
		},
	},
}

// ==================== Serper API ====================

const (
	serperEndpoint = "https://google.serper.dev/news"
	serperBatch    = 100
)

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
	llmRateLimitPerMinute       = 8000 // API limit 10K RPM; 8K gives 20% headroom; 8K×1000tok=8M<10M TPM
	embeddingRateLimitPerMinute = 2000 // TPM-constrained: 2000×1800tok=3.6M<5M TPM (API RPM limit is 5K)

	PostHogEventNewsParsingFailed    = "NEWS_PARSING_FAILED"
	PostHogEventNewsParsingSucceeded = "NEWS_PARSING_SUCCEEDED"

	areaCapWindow      = 12 * time.Hour
	embeddingBatchSize = 20
	dbBatchSize        = 50

	// Worker pool sizes — derived from rate limits and DB connection pool (max 11).
	// LLM workers: 50 goroutines × ~2s/call ≈ 1500 actual RPM, well under 8000 RPM limiter.
	// Embedding workers: 5 goroutines; limiter caps at 2000 RPM batches (~1800 tok each) = 3.6M TPM.
	// DB workers: 9 keeps concurrent queries under the 11-connection cron pool limit.
	// Fetch workers: ceil(573 areas / 100 per batch) = 6 max Serper batches in parallel.
	llmWorkers       = 50
	embeddingWorkers = 5
	dbWorkers        = 9
	fetchWorkers     = 6
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

// ==================== Area entry ====================

type areaEntry struct {
	state    stateInfo
	areaKey  string
	areaName string
}

func fullAreaKey(stateKey, dKey string) string {
	return stateKey + ":" + dKey
}

// ==================== Pipeline types ====================

type rawNewsItem struct {
	item        serperNewsItem
	entry       areaEntry
	contentHash string
	done        func() // deletes both link and contentHash from inFlightLinks
}

type translatedNewsItem struct {
	rawNewsItem
	result translationResult
}

type embeddedNewsItem struct {
	translatedNewsItem
	embedding []float32 // nil when semanticDedupEnabled=false
}

// ==================== Per-area mutex map ====================

type areaLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (a *areaLocks) get(key string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	if m, ok := a.locks[key]; ok {
		return m
	}
	m := &sync.Mutex{}
	a.locks[key] = m
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
	log.Printf("[%s] Starting Serper area news parser\n", timestamp)

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

	posthogConfigRepo := posthogRepository.NewPostHogConfigRepository(db)
	posthogClient := analytics.NewPostHogClient()
	posthogConfig := getPostHogConfig(posthogConfigRepo, timestamp)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: geminiAPIKey})
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to create Gemini client: %v\n", timestamp, err)
	}

	// Build flat area list
	var allAreas []areaEntry
	for _, s := range states {
		for _, d := range s.Areas {
			allAreas = append(allAreas, areaEntry{
				state:    s,
				areaKey:  areaKey(d),
				areaName: d,
			})
		}
	}
	log.Printf("[%s] Total areas: %d\n", timestamp, len(allAreas))

	// Phase 0: pre-load area caps and dedup store before any goroutines start
	fullAreas := loadFullAreas(db, timestamp)
	var skippedAreas int
	var remaining []areaEntry
	for _, e := range allAreas {
		if _, ok := fullAreas[fullAreaKey(e.state.StateKey, e.areaKey)]; ok {
			skippedAreas++
		} else {
			remaining = append(remaining, e)
		}
	}
	log.Printf("[%s] Areas skipped (cap reached): %d, remaining: %d\n", timestamp, skippedAreas, len(remaining))

	store := loadDedupStore(db, timestamp)

	var totalProcessed, totalSkipped, totalFailed int64
	var inFlightLinks sync.Map
	areaLk := &areaLocks{locks: make(map[string]*sync.Mutex)}

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
				processBatch(db, batch, store, areaLk, timestamp, &totalProcessed, &totalSkipped, &totalFailed)
			}
		}()
	}

	// ── Phase 4b: DB batcher ─────────────────────────────────────────
	var dbBatcherWg sync.WaitGroup
	dbBatcherWg.Add(1)
	go func() {
		defer dbBatcherWg.Done()
		defer close(dbBatchesCh) // always close so DB workers exit cleanly on ctx.Done early return
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
		// Embedding workers
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

		// Embedding batcher (1)
		embBatcherWg.Add(1)
		go func() {
			defer embBatcherWg.Done()
			defer close(newsItemBatchesCh) // always close so embedding workers exit cleanly on ctx.Done early return
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
		// Semantic dedup disabled: pass items through with nil embedding directly
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

				result, err := callGeminiTranslate(ctx, genaiClient, raw.item.Title, raw.item.Snippet, raw.entry.state, llmRateLimiter)
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

	// ── Phase 1: Serper fetch ────────────────────────────────────────
	fetchSemaphore := make(chan struct{}, fetchWorkers)
	var fetchWg sync.WaitGroup

	for batchStart := 0; batchStart < len(remaining); batchStart += serperBatch {
		end := batchStart + serperBatch
		if end > len(remaining) {
			end = len(remaining)
		}
		batch := remaining[batchStart:end]

		fetchWg.Add(1)
		fetchSemaphore <- struct{}{}

		go func(batch []areaEntry, batchStart, end int) {
			defer fetchWg.Done()
			defer func() { <-fetchSemaphore }()

			if ctx.Err() != nil {
				return
			}

			localRng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(batchStart)))
			log.Printf("[%s] Fetching Serper batch %d-%d\n", timestamp, batchStart+1, end)

			results, err := fetchSerperBatch(ctx, batch, serperAPIKey, localRng, timestamp)
			if err != nil {
				log.Printf("[%s] ✗ Serper batch error (areas %d-%d): %v\n", timestamp, batchStart+1, end, err)
				return
			}

			queryToEntry := make(map[string]areaEntry, len(batch))
			for _, e := range batch {
				queryToEntry[e.areaName+", "+e.state.Name] = e
			}

			for _, result := range results {
				entry, ok := queryToEntry[result.SearchParameters.Q]
				if !ok {
					log.Printf("[%s] ✗ No matching area for Serper result q=%q\n", timestamp, result.SearchParameters.Q)
					continue
				}

				for _, item := range result.News {
					if ctx.Err() != nil {
						return
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
						return
					}
				}
			}
		}(batch, batchStart, end)
	}

	// ── Teardown: drain in dependency order ──────────────────────────
	fetchWg.Wait()
	close(rawNewsItemsCh)

	llmWg.Wait()
	close(translatedNewsItemsCh)

	embBatcherWg.Wait() // embedding batcher closes newsItemBatchesCh and exits
	embedWg.Wait()      // embedding workers drain newsItemBatchesCh and exit
	close(embeddedNewsItemsCh)

	dbBatcherWg.Wait() // DB batcher closes dbBatchesCh and exits
	dbWg.Wait()        // DB workers drain dbBatchesCh and exit

	elapsed := time.Since(startTime)
	if ctx.Err() != nil {
		log.Printf("[%s] ⚠ Run interrupted after %dm %02ds: %d processed, %d skipped, %d failed\n",
			timestamp, int(elapsed.Minutes()), int(elapsed.Seconds())%60, totalProcessed, totalSkipped, totalFailed)
	} else {
		log.Printf("[%s] ✓ Completed in %dm %02ds: %d processed, %d skipped, %d failed\n",
			timestamp, int(elapsed.Minutes()), int(elapsed.Seconds())%60, totalProcessed, totalSkipped, totalFailed)
	}

	if ctx.Err() == nil {
		sendPostHogEvent(posthogClient, posthogConfig, PostHogEventNewsParsingSucceeded, int(totalProcessed))
	}
	sendPostHogEvent(posthogClient, posthogConfig, PostHogEventNewsParsingFailed, int(totalFailed))
}

// ==================== Serper batch fetch ====================

func fetchSerperBatch(ctx context.Context, entries []areaEntry, apiKey string, rng *rand.Rand, timestamp string) ([]serperBatchResult, error) {
	queries := make([]serperQuery, len(entries))
	for i, e := range entries {
		queries[i] = serperQuery{
			Q:   e.areaName + ", " + e.state.Name,
			Gl:  "in",
			Hl:  e.state.LangCode,
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

// ==================== Area cap ====================

func loadFullAreas(db *gorm.DB, timestamp string) map[string]struct{} {
	since := time.Now().Add(-areaCapWindow)
	var rows []struct {
		Category    string
		SubCategory string
		Count       int64
	}
	err := db.Raw(`
		SELECT category, sub_category, COUNT(*) AS count
		FROM news
		WHERE sub_category IS NOT NULL AND created_at >= ?
		GROUP BY category, sub_category
		HAVING COUNT(*) >= ?`, since, getAreaCapLimit()).Scan(&rows).Error
	if err != nil {
		log.Printf("[%s] ✗ Failed to load area caps: %v\n", timestamp, err)
		return map[string]struct{}{}
	}
	full := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		full[fullAreaKey(r.Category, r.SubCategory)] = struct{}{}
	}
	return full
}

func areaCount(db *gorm.DB, stateKey, dKey string) (int64, error) {
	since := time.Now().Add(-areaCapWindow)
	var count int64
	err := db.Model(&dsmodels.News{}).
		Where("category = ? AND sub_category = ? AND created_at >= ?", stateKey, dKey, since).
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

func callGeminiTranslate(ctx context.Context, client *genai.Client, title, snippet string, state stateInfo, rateLimiter *rate.Limiter) (translationResult, error) {
	if err := rateLimiter.Wait(ctx); err != nil {
		return translationResult{}, fmt.Errorf("rate limiter: %w", err)
	}
	if len(state.Languages) == 0 {
		return translationResult{}, fmt.Errorf("state %q has no languages configured", state.StateKey)
	}

	baseLang := state.Languages[0]
	additionalLangs := state.Languages[1:]
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
- Include geographic context: %s, India; add culturally relevant visual elements with authentic, grounded detail
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
		state.Name,
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
	areaLk *areaLocks,
	timestamp string,
	totalProcessed, totalSkipped, totalFailed *int64,
) {
	// Guarantee done() is called for every item regardless of exit path.
	// done() deletes both link and contentHash from inFlightLinks.
	defer func() {
		for _, item := range batch {
			item.done()
		}
	}()

	// Group batch by area, preserving insertion order for deterministic processing.
	type areaGroup struct {
		stateKey string
		dKey     string
		items    []embeddedNewsItem
	}
	var areaOrder []string
	groups := make(map[string]*areaGroup)
	for _, item := range batch {
		sk := item.entry.state.StateKey
		dk := item.entry.areaKey
		key := fullAreaKey(sk, dk)
		if _, ok := groups[key]; !ok {
			areaOrder = append(areaOrder, key)
			groups[key] = &areaGroup{stateKey: sk, dKey: dk}
		}
		groups[key].items = append(groups[key].items, item)
	}

	since := time.Now().Add(-semanticDedupWindow)

	for _, key := range areaOrder {
		grp := groups[key]

		// Acquire only this area's mutex; release before moving to the next area.
		mu := areaLk.get(key)
		mu.Lock()

		count, err := areaCount(db, grp.stateKey, grp.dKey)
		if err != nil {
			log.Printf("[%s] ✗ areaCount failed for %s: %v\n", timestamp, key, err)
			mu.Unlock()
			for range grp.items {
				atomic.AddInt64(totalFailed, 1)
			}
			continue
		}

		remaining := int64(getAreaCapLimit()) - count
		if remaining <= 0 {
			log.Printf("[%s] ⊘ [%s] Area cap reached\n", timestamp, key)
			mu.Unlock()
			for range grp.items {
				atomic.AddInt64(totalSkipped, 1)
			}
			continue
		}

		candidates := grp.items
		if int64(len(candidates)) > remaining {
			for _, item := range candidates[remaining:] {
				log.Printf("[%s] ⊘ [%s] Area cap trim | '%s'\n", timestamp, key, item.item.Title)
				atomic.AddInt64(totalSkipped, 1)
			}
			candidates = candidates[:remaining]
		}

		// Semantic dedup: one CROSS JOIN LATERAL query per area group (skip if embeddings are nil).
		var toInsert []embeddedNewsItem
		if semanticDedupEnabled && len(candidates) > 0 {
			dupMap, err := bulkSemanticDedup(db, candidates, grp.stateKey, grp.dKey, since)
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
					recordSimilarNews(db, info.CanonicalID, item.item.Link, item.contentHash, grp.stateKey, grp.dKey, info.Similarity)
					store.Add(item.item.Link, item.contentHash)
					atomic.AddInt64(totalSkipped, 1)
				} else {
					toInsert = append(toInsert, item)
				}
			}
		} else {
			toInsert = candidates
		}

		// Intra-batch semantic dedup: drop items similar to an earlier
		// candidate in the same batch (not yet in DB, so bulkSemanticDedup
		// cannot catch them).
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
			if err := bulkInsertNewsWithTranslations(db, toInsert, grp.stateKey, grp.dKey); err != nil {
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

func bulkSemanticDedup(db *gorm.DB, items []embeddedNewsItem, stateKey, dKey string, since time.Time) (map[int]semanticDupInfo, error) {
	valueParts := make([]string, len(items))
	args := make([]any, 0, len(items)+4)
	for i, item := range items {
		valueParts[i] = fmt.Sprintf("(%d, ?::vector)", i)
		args = append(args, pgvector.NewVector(item.embedding))
	}
	args = append(args, stateKey, dKey, since, semanticDedupThreshold)

	query := fmt.Sprintf(`
		WITH incoming(idx, embedding) AS (
			VALUES %s
		)
		SELECT i.idx, n.id, 1 - (n.embedding <=> i.embedding) AS similarity
		FROM incoming i
		CROSS JOIN LATERAL (
			SELECT id, embedding
			FROM news
			WHERE category = ? AND sub_category = ? AND embedding IS NOT NULL AND created_at >= ?
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

func bulkInsertNewsWithTranslations(db *gorm.DB, items []embeddedNewsItem, stateKey, dKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		publishedAt := time.Now().UTC()
		sub := dKey

		newsItems := make([]dsmodels.News, len(items))
		for i, item := range items {
			n := dsmodels.News{
				Link:        item.item.Link,
				ContentHash: &item.contentHash,
				Category:    stateKey,
				SubCategory: &sub,
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

		// GORM populates newsItems[i].ID via RETURNING after bulk create.
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

// dotProduct computes the dot product of two L2-normalised vectors.
// For normalised vectors dot product == cosine similarity.
func dotProduct(a, b []float32) float32 {
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

// intraBatchSemanticDedup removes items that are semantically similar to an
// earlier item in the same slice. Items must have L2-normalised embeddings.
// Returns accepted items and the ones that were dropped (with their similarity
// score against the closest accepted item).
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

func recordSimilarNews(db *gorm.DB, canonicalID uuid.UUID, link, contentHash, stateKey, dKey string, similarity float32) {
	sub := dKey
	rec := dsmodels.SimilarNews{
		NewsID:          canonicalID,
		Link:            link,
		ContentHash:     &contentHash,
		Category:        stateKey,
		SubCategory:     &sub,
		SimilarityScore: &similarity,
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rec).Error; err != nil {
		log.Printf("Failed to record similar_news for %s: %v", link, err)
	}
}

// ==================== PostHog ====================

type newsParsingEventProps struct{ Count int }

func (p newsParsingEventProps) ToProperties() map[string]any {
	return map[string]any{"count": p.Count}
}

func getPostHogConfig(repo posthogRepository.PostHogConfigRepository, timestamp string) *posthogModels.PostHogConfig {
	env := utils.GetEnv("GO_ENV", "local")
	config, err := repo.FindByAppNameAndEnv(constants.AppNameDailyStory, env)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			log.Printf("[%s] [PostHog] No config found for %s/%s\n", timestamp, constants.AppNameDailyStory, env)
		} else {
			log.Printf("[%s] [PostHog] Error: %v\n", timestamp, err)
		}
		return nil
	}
	if !config.IsActive {
		log.Printf("[%s] [PostHog] Config inactive for %s\n", timestamp, constants.AppNameDailyStory)
		return nil
	}
	return config
}

func sendPostHogEvent(client *analytics.PostHogClient, config *posthogModels.PostHogConfig, eventName string, count int) {
	if config == nil {
		return
	}
	props := newsParsingEventProps{Count: count}
	if err := client.SendEvent(config.Host, config.APIKey, eventName, constants.AppNameDailyStory, props.ToProperties()); err != nil {
		log.Printf("[PostHog ERROR] Failed to send %s: %v\n", eventName, err)
	}
}
