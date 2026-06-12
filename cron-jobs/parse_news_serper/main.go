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
	"math"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dsmodels "go-backend/internal/apps/dailystory/models"
	posthogModels "go-backend/internal/apps/posthog/config/models"
	posthogRepository "go-backend/internal/apps/posthog/config/repository"
	"go-backend/internal/common/constants"
	"go-backend/internal/common/database"
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
	llmRateLimitPerMinute       = 5000
	embeddingRateLimitPerMinute = 1000

	PostHogEventNewsParsingFailed    = "NEWS_PARSING_FAILED"
	PostHogEventNewsParsingSucceeded = "NEWS_PARSING_SUCCEEDED"

	areaCapWindow = 12 * time.Hour
	areaCapLimit  = 20
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
	semanticStoreMutex     sync.Mutex
)

// ==================== Shared HTTP client ====================

var httpClient = &http.Client{Timeout: 30 * time.Second}

// ==================== Helpers ====================

func computeContentHash(title, source string) string {
	input := strings.ToLower(strings.TrimSpace(title)) + "|" + strings.ToLower(strings.TrimSpace(source))
	digest := sha256.Sum256([]byte(input))
	return hex.EncodeToString(digest[:])
}

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

// fullAreaKey returns the composite key used to identify an area bucket.
func fullAreaKey(stateKey, dKey string) string {
	return stateKey + ":" + dKey
}

// ==================== Area entry ====================

type areaEntry struct {
	state    stateInfo
	areaKey  string
	areaName string
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

	ctx := context.Background()
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

	// Pre-check area cap: single query to find areas already at limit
	fullAreas := loadFullAreas(db, timestamp)
	var skippedAreas int
	var remaining []areaEntry
	for _, e := range allAreas {
		if fullAreas[fullAreaKey(e.state.StateKey, e.areaKey)] {
			skippedAreas++
		} else {
			remaining = append(remaining, e)
		}
	}
	log.Printf("[%s] Areas skipped (cap reached): %d, remaining: %d\n", timestamp, skippedAreas, len(remaining))

	var totalProcessed, totalSkipped, totalFailed int64

	processor := &newsProcessor{
		db:                   db,
		genai:                genaiClient,
		llmRateLimiter:       llmRateLimiter,
		embeddingRateLimiter: embeddingRateLimiter,
		timestamp:            timestamp,
	}

	var inFlightLinks sync.Map

	maxConcurrentItems := 8
	itemSemaphore := make(chan struct{}, maxConcurrentItems)
	var itemWg sync.WaitGroup

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Process remaining areas in Serper batches
	for batchStart := 0; batchStart < len(remaining); batchStart += serperBatch {
		end := batchStart + serperBatch
		if end > len(remaining) {
			end = len(remaining)
		}
		batch := remaining[batchStart:end]

		log.Printf("[%s] Fetching Serper batch %d-%d\n", timestamp, batchStart+1, end)

		results, err := fetchSerperBatch(batch, serperAPIKey, rng, timestamp)
		if err != nil {
			log.Printf("[%s] ✗ Serper batch error (areas %d-%d): %v\n", timestamp, batchStart+1, end, err)
			continue
		}

		// Build a lookup from the query string to its areaEntry so that
		// results can be matched by searchParameters.q rather than by index.
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
			if len(result.News) == 0 {
				continue
			}

			for _, item := range result.News {
				item.Title = strings.TrimSpace(item.Title)
				item.Snippet = strings.TrimSpace(item.Snippet)

				if item.Title == "" || item.Link == "" {
					atomic.AddInt64(&totalSkipped, 1)
					continue
				}

				contentHash := computeContentHash(item.Title, item.Source)

				isDuplicate, err := checkDuplicateItem(db, item.Link, contentHash)
				if err != nil {
					log.Printf("[%s] ✗ DB error checking duplicate: %v\n", timestamp, err)
					atomic.AddInt64(&totalFailed, 1)
					continue
				}
				if isDuplicate {
					atomic.AddInt64(&totalSkipped, 1)
					continue
				}

				if _, alreadyProcessing := inFlightLinks.LoadOrStore(item.Link, true); alreadyProcessing {
					atomic.AddInt64(&totalSkipped, 1)
					continue
				}
				if _, alreadyProcessing := inFlightLinks.LoadOrStore(contentHash, true); alreadyProcessing {
					inFlightLinks.Delete(item.Link)
					atomic.AddInt64(&totalSkipped, 1)
					continue
				}

				itemWg.Add(1)
				itemSemaphore <- struct{}{}

				go func(item serperNewsItem, entry areaEntry, contentHash string) {
					defer itemWg.Done()
					defer func() { <-itemSemaphore }()
					defer inFlightLinks.Delete(item.Link)
					defer inFlightLinks.Delete(contentHash)
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[%s] PANIC in item goroutine: %v\n", timestamp, r)
						}
					}()

					switch processor.processItem(item, entry.state, entry.areaKey, contentHash) {
					case outcomeProcessed:
						atomic.AddInt64(&totalProcessed, 1)
					case outcomeSkipped:
						atomic.AddInt64(&totalSkipped, 1)
					case outcomeFailed:
						atomic.AddInt64(&totalFailed, 1)
					}
				}(item, entry, contentHash)
			}
		}
	}

	itemWg.Wait()

	elapsed := time.Since(startTime)
	log.Printf("[%s] ✓ Completed in %dm %02ds: %d processed, %d skipped, %d failed\n",
		timestamp, int(elapsed.Minutes()), int(elapsed.Seconds())%60, totalProcessed, totalSkipped, totalFailed)

	sendPostHogEvent(posthogClient, posthogConfig, PostHogEventNewsParsingSucceeded, int(totalProcessed))
	sendPostHogEvent(posthogClient, posthogConfig, PostHogEventNewsParsingFailed, int(totalFailed))

	os.Exit(0)
}

// ==================== Serper batch fetch ====================

func fetchSerperBatch(entries []areaEntry, apiKey string, rng *rand.Rand, timestamp string) ([]serperBatchResult, error) {
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
			time.Sleep(jitter)
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

// loadFullAreas returns a set of "stateKey:areaKey" strings for areas
// that already have >= areaCapLimit news items in the last areaCapWindow.
func loadFullAreas(db *gorm.DB, timestamp string) map[string]bool {
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
		HAVING COUNT(*) >= ?`, since, areaCapLimit).Scan(&rows).Error
	if err != nil {
		log.Printf("[%s] ✗ Failed to load area caps: %v\n", timestamp, err)
		return map[string]bool{}
	}
	full := make(map[string]bool, len(rows))
	for _, r := range rows {
		full[fullAreaKey(r.Category, r.SubCategory)] = true
	}
	return full
}

// areaCount returns the current count of news items for the given area in the cap window.
func areaCount(db *gorm.DB, stateKey, dKey string) (int64, error) {
	since := time.Now().Add(-areaCapWindow)
	var count int64
	err := db.Model(&dsmodels.News{}).
		Where("category = ? AND sub_category = ? AND created_at >= ?", stateKey, dKey, since).
		Count(&count).Error
	return count, err
}

// ==================== Item processor ====================

type itemOutcome int

const (
	outcomeProcessed itemOutcome = iota
	outcomeSkipped
	outcomeFailed
)

type newsProcessor struct {
	db                   *gorm.DB
	genai                *genai.Client
	llmRateLimiter       *rate.Limiter
	embeddingRateLimiter *rate.Limiter
	timestamp            string
}

func (p *newsProcessor) processItem(item serperNewsItem, state stateInfo, dKey, contentHash string) itemOutcome {
	translateCtx, translateCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer translateCancel()

	result, err := callGeminiTranslate(translateCtx, p.genai, item.Title, item.Snippet, state, p.llmRateLimiter)
	if err != nil {
		log.Printf("[%s] ✗ Gemini failed for '%s': %v\n", p.timestamp, item.Title, err)
		return outcomeFailed
	}
	if result.BaseHeadline == "" || result.BaseSummary == "" {
		log.Printf("[%s] ✗ Empty headline/summary for '%s'\n", p.timestamp, item.Title)
		return outcomeFailed
	}
	for langCode, pair := range result.Translations {
		if pair.Headline == "" || pair.Summary == "" {
			log.Printf("[%s] ✗ Empty %s translation for '%s'\n", p.timestamp, langCode, item.Title)
			return outcomeFailed
		}
	}

	var embedding []float32
	if semanticDedupEnabled {
		embText := result.BaseHeadline + "\n" + result.BaseSummary
		embedCtx, embedCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer embedCancel()
		var err error
		embedding, err = generateEmbedding(embedCtx, p.genai, embText, p.embeddingRateLimiter)
		if err != nil {
			log.Printf("[%s] ✗ Embedding failed for '%s': %v\n", p.timestamp, item.Title, err)
			return outcomeFailed
		}

		// Phase A: optimistic dedup check before entering the critical section.
		isDup, canonicalID, similarity, err := findSemanticDuplicate(p.db, embedding, state.StateKey, dKey)
		if err != nil {
			log.Printf("[%s] ✗ Semantic dedup search failed for '%s': %v\n", p.timestamp, item.Title, err)
			return outcomeFailed
		}
		if isDup {
			log.Printf("[%s] ⊘ [%s/%s] Semantic dup (%.3f) of %s | '%s'\n", p.timestamp, state.StateKey, dKey, similarity, canonicalID, item.Title)
			recordSimilarNews(p.db, canonicalID, item.Link, contentHash, state.StateKey, dKey, similarity)
			return outcomeSkipped
		}
	}

	// Phase B: mutex-protected cap check + optional dedup re-check + store.
	// Passing nil embedding skips the dedup re-check (semantic dedup disabled).
	return p.storeWithDedup(item, state.StateKey, dKey, contentHash, result, embedding)
}

func (p *newsProcessor) storeWithDedup(item serperNewsItem, stateKey, dKey, contentHash string, result translationResult, embedding []float32) itemOutcome {
	isDup, canonicalID, similarity, err := func() (bool, uuid.UUID, float32, error) {
		semanticStoreMutex.Lock()
		defer semanticStoreMutex.Unlock()

		count, cerr := areaCount(p.db, stateKey, dKey)
		if cerr != nil {
			return false, uuid.Nil, 0, fmt.Errorf("cap re-check failed: %w", cerr)
		}
		if count >= areaCapLimit {
			return true, uuid.Nil, 0, nil
		}

		if len(embedding) > 0 {
			dup, cid, sim, ferr := findSemanticDuplicate(p.db, embedding, stateKey, dKey)
			if ferr != nil {
				return false, uuid.Nil, 0, ferr
			}
			if dup {
				return true, cid, sim, nil
			}
		}
		return false, uuid.Nil, 0, storeNewsWithTranslations(p.db, item.Link, contentHash, stateKey, dKey, result, embedding)
	}()

	if err != nil {
		log.Printf("[%s] ✗ Store/dedup failed for '%s': %v\n", p.timestamp, item.Title, err)
		return outcomeFailed
	}
	if isDup {
		if canonicalID != uuid.Nil {
			log.Printf("[%s] ⊘ [%s/%s] Semantic dup on re-check (%.3f) of %s | '%s'\n", p.timestamp, stateKey, dKey, similarity, canonicalID, item.Title)
			recordSimilarNews(p.db, canonicalID, item.Link, contentHash, stateKey, dKey, similarity)
		} else {
			log.Printf("[%s] ⊘ [%s/%s] Area cap reached on re-check | '%s'\n", p.timestamp, stateKey, dKey, item.Title)
		}
		return outcomeSkipped
	}
	return outcomeProcessed
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
	baseLangName := languageCodeToName(baseLang)

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

	sourceText := title
	if snippet != "" {
		sourceText = title + ". " + snippet
	}

	prompt := fmt.Sprintf(`Convert the following %s news content into a %s news headline and a %s news poster short summary%s.
Then generate an English image generation prompt for this news article.

%s

%s

Image prompt guidelines:
- Write a hyper-realistic photographic description in English for Flux Klein 4B image generation
- Include geographic context: %s, India
- Focus on the key subject/event; include culturally relevant visual elements
- If humans are present: show at most one person, clearly defined hands tucked close to the body or holding a single object, full body or upper body only (no cropped limbs), neutral symmetrical pose
- Use negative framing for faces: sharp facial features, natural skin texture, anatomically correct proportions
- Do not include any text, letters, alphabets, or numbers in the image
- Keep it under 100 words

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

	raw, err := callGeminiAPIIntoMap(ctx, client, prompt)
	if err != nil {
		return translationResult{}, err
	}

	translations := make(map[string]translationPair, len(additionalLangs))
	for _, code := range additionalLangs {
		lower := strings.ToLower(languageCodeToName(code))
		translations[code] = translationPair{
			Headline: strings.TrimSpace(raw[lower+"_headline"]),
			Summary:  strings.TrimSpace(raw[lower+"_summary"]),
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

func callGeminiAPI(ctx context.Context, client *genai.Client, prompt string) (string, error) {
	contents := []*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{{Text: prompt}}},
	}
	config := &genai.GenerateContentConfig{ResponseMIMEType: "application/json"}

	response, err := client.Models.GenerateContent(ctx, geminiModel, contents, config)
	if err != nil {
		return "", fmt.Errorf("Gemini API error: %w", err)
	}
	if len(response.Candidates) == 0 || response.Candidates[0].Content == nil || len(response.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in Gemini response")
	}
	return response.Candidates[0].Content.Parts[0].Text, nil
}

// ==================== Embedding ====================

func generateEmbedding(ctx context.Context, client *genai.Client, text string, rateLimiter *rate.Limiter) ([]float32, error) {
	if err := rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	dim := int32(embeddingDimensions)
	contents := []*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{{Text: text}}},
	}
	config := &genai.EmbedContentConfig{
		TaskType:             "SEMANTIC_SIMILARITY",
		OutputDimensionality: &dim,
	}
	response, err := client.Models.EmbedContent(ctx, embeddingModel, contents, config)
	if err != nil {
		return nil, fmt.Errorf("Gemini embedding error: %w", err)
	}
	if len(response.Embeddings) == 0 || len(response.Embeddings[0].Values) == 0 {
		return nil, fmt.Errorf("no embedding in Gemini response")
	}
	vec := response.Embeddings[0].Values
	normalizeL2(vec)
	return vec, nil
}

func normalizeL2(vec []float32) {
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	if sumSq == 0 {
		return
	}
	norm := math.Sqrt(sumSq)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}
}

// ==================== Semantic dedup ====================

func findSemanticDuplicate(db *gorm.DB, embedding []float32, stateKey, dKey string) (bool, uuid.UUID, float32, error) {
	since := time.Now().Add(-semanticDedupWindow)
	vec := pgvector.NewVector(embedding)

	var match struct {
		ID         uuid.UUID
		Similarity float64
	}
	err := db.Raw(`
		SELECT id, 1 - (embedding <=> ?::vector) AS similarity
		FROM news
		WHERE category = ? AND sub_category = ? AND embedding IS NOT NULL AND created_at >= ?
		ORDER BY embedding <=> ?::vector
		LIMIT 1`, vec, stateKey, dKey, since, vec).Scan(&match).Error
	if err != nil {
		return false, uuid.Nil, 0, fmt.Errorf("semantic search failed: %w", err)
	}
	if match.ID == uuid.Nil {
		return false, uuid.Nil, 0, nil
	}
	return match.Similarity >= semanticDedupThreshold, match.ID, float32(match.Similarity), nil
}

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

// ==================== Dedup check ====================

func checkDuplicateItem(db *gorm.DB, link, contentHash string) (bool, error) {
	for _, model := range []any{&dsmodels.News{}, &dsmodels.SimilarNews{}} {
		var count int64
		if err := db.Model(model).
			Where("link = ? OR content_hash = ?", link, contentHash).
			Count(&count).Error; err != nil {
			return false, fmt.Errorf("error checking duplicate in %T: %w", model, err)
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

// ==================== Store ====================

func storeNewsWithTranslations(db *gorm.DB, link, contentHash, stateKey, dKey string, result translationResult, embedding []float32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	publishedAt := time.Now().UTC().Truncate(24 * time.Hour)
	sub := dKey

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		news := dsmodels.News{
			Link:        link,
			ContentHash: &contentHash,
			Category:    stateKey,
			SubCategory: &sub,
			Status:      "approved",
			PublishedAt: &publishedAt,
		}

		if result.ImagePrompt != "" {
			news.ImagePrompt = &result.ImagePrompt
		}

		if len(embedding) > 0 {
			v := pgvector.NewVector(embedding)
			news.Embedding = &v
		}

		if err := tx.Create(&news).Error; err != nil {
			return fmt.Errorf("failed to create news: %w", err)
		}

		translationsToCreate := []dsmodels.NewsTranslation{
			{NewsID: news.ID, Title: result.BaseHeadline, Summary: result.BaseSummary, LanguageCode: result.BaseLanguageCode},
		}
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
