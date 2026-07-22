package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	graphBase      = "https://graph.facebook.com/v25.0"
	graphVideoBase = "https://graph-video.facebook.com/v25.0"

	groupingShared = "shared"
	groupingUnique = "unique"
)

type videoAsset struct {
	videoID   string
	imageHash string
}

func main() {
	goEnv := os.Getenv("GO_ENV")
	if goEnv == "" {
		goEnv = "local"
	}
	_ = godotenv.Load(".env." + goEnv)
	_ = godotenv.Load()

	langCampaignIDs := map[string]string{
		"Hindi":    os.Getenv("META_CAMPAIGN_ID_HINDI"),
		"Tamil":    os.Getenv("META_CAMPAIGN_ID_TAMIL"),
		"Marathi":  os.Getenv("META_CAMPAIGN_ID_MARATHI"),
		"Gujarati": os.Getenv("META_CAMPAIGN_ID_GUJARATI"),
		"Bengali":  os.Getenv("META_CAMPAIGN_ID_BENGALI"),
		"Telugu":   os.Getenv("META_CAMPAIGN_ID_TELUGU"),
		"Kannada":  os.Getenv("META_CAMPAIGN_ID_KANNADA"),
	}

	// langStates maps ad copy language to the Indian state(s) it should be geo-targeted
	// to, instead of all of India.
	langStates := map[string][]string{
		"Hindi": {
			"Himachal Pradesh", "Uttarakhand", "Haryana", "Delhi", "Uttar Pradesh",
			"Bihar", "Jharkhand", "Rajasthan", "Madhya Pradesh", "Chhattisgarh",
		},
		"Tamil":    {"Tamil Nadu"},
		"Marathi":  {"Maharashtra"},
		"Gujarati": {"Gujarat"},
		"Bengali":  {"West Bengal"},
		"Telugu":   {"Telangana", "Andhra Pradesh"},
		"Kannada":  {"Karnataka"},
	}

	campaignID := flag.String("campaign-id", "", "existing Meta campaign ID (optional override; resolved from -lang if omitted; only valid together with -lang)")
	adAccountID := flag.String("ad-account-id", os.Getenv("META_AD_ACCOUNT_ID"), "ad account ID without act_ prefix (required)")
	pageID := flag.String("page-id", os.Getenv("META_PAGE_ID"), "Facebook page ID (required)")
	appID := flag.String("app-id", os.Getenv("META_APP_ID"), "Facebook app ID (required)")
	storeURL := flag.String("store-url", os.Getenv("META_STORE_URL"), "Play Store URL for the app (required)")
	conversionEvent := flag.String("conversion-event", "START_TRIAL", "app event name e.g. START_TRIAL, PURCHASE, COMPLETE_REGISTRATION (required)")
	linkURL := flag.String("link-url", "", "destination URL for CTA e.g. Play Store URL (optional, Meta uses app store URL from app config if omitted)")
	dailyBudget := flag.Int("daily-budget", 50000, "daily budget in paise e.g. 50000 = ₹500")
	startTime := flag.String("start-time", "", "ISO 8601 start time (default: 40 minutes from now, or 6am IST if run between 9pm and 5am IST)")
	textsFile := flag.String("texts-file", "", "path to texts JSON file (default: scripts/generate_reel_hooks/<lang-lowercase>.json)")
	textsLang := flag.String("lang", "", "language for ad copy: Hindi, Tamil, Marathi, Gujarati, Bengali, Telugu, Kannada (optional; omit to auto-process every language with a META_CAMPAIGN_ID_<LANG> set)")
	cta := flag.String("cta", "INSTALL_MOBILE_APP", "call-to-action button type")
	igUserID := flag.String("instagram-user-id", os.Getenv("META_IG_USER_ID"), "IG Business Account ID for Instagram delivery")
	androidOSMin := flag.String("android-os-min", "10.0", "minimum Android OS version")
	androidOSMax := flag.String("android-os-max", "", "maximum Android OS version (empty = no upper bound)")
	countries := flag.String("countries", "IN", "comma-separated target country codes")
	slugsFlag := flag.String("slugs", "", "comma-separated slugs to process (manual override, requires -lang; bypasses auto quota-based selection)")
	videoGrouping := flag.String("video-grouping", "", fmt.Sprintf("required: %q groups all videos sharing a slug prefix into one adset/ad (existing behavior), %q creates a separate adset/ad per video", groupingShared, groupingUnique))
	flag.Parse()

	if *videoGrouping != groupingShared && *videoGrouping != groupingUnique {
		log.Fatalf("flag -video-grouping is required and must be %q or %q", groupingShared, groupingUnique)
	}
	if *slugsFlag != "" && *textsLang == "" {
		log.Fatalf("-slugs requires -lang to also be set (manual slug selection only applies to a single language/campaign)")
	}
	if *textsLang != "" {
		if _, ok := langCampaignIDs[*textsLang]; !ok {
			log.Fatalf("flag -lang %q is not supported; valid values: Hindi, Tamil, Marathi, Gujarati, Bengali, Telugu, Kannada", *textsLang)
		}
	}

	if *startTime == "" {
		*startTime = defaultStartTime(time.Now())
	}

	for name, val := range map[string]string{
		"ad-account-id":    *adAccountID,
		"page-id":          *pageID,
		"app-id":           *appID,
		"conversion-event": *conversionEvent,
		"store-url":        *storeURL,
	} {
		if val == "" {
			log.Fatalf("flag -%s is required", name)
		}
	}

	token := mustEnv("META_ACCESS_TOKEN")
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	ctx := context.Background()

	ctaLink := *linkURL
	if ctaLink == "" {
		ctaLink = *storeURL
	}

	type langRun struct{ lang, campaignID string }
	var runs []langRun
	if *textsLang != "" {
		cid := *campaignID
		if cid == "" {
			cid = langCampaignIDs[*textsLang]
		}
		if cid == "" {
			log.Fatalf("no campaign ID resolved for -lang %q (set -campaign-id or META_CAMPAIGN_ID_%s)", *textsLang, strings.ToUpper(*textsLang))
		}
		runs = append(runs, langRun{*textsLang, cid})
	} else {
		for _, lang := range []string{"Hindi", "Tamil", "Marathi", "Gujarati", "Bengali", "Telugu", "Kannada"} {
			if cid := langCampaignIDs[lang]; cid != "" {
				runs = append(runs, langRun{lang, cid})
			}
		}
		if len(runs) == 0 {
			log.Fatalf("no -lang given and no META_CAMPAIGN_ID_<LANG> env vars are set")
		}
		log.Printf("no -lang given; processing %d language(s)", len(runs))
	}

	type result struct {
		lang       string
		slug       string
		adsetID    string
		creativeID string
		adID       string
		skipped    bool
		err        error
	}
	var results []result

	for _, run := range runs {
		lang, campID := run.lang, run.campaignID
		log.Printf("=== lang %s (campaign %s) ===", lang, campID)

		textsFilePath := *textsFile
		if textsFilePath == "" {
			textsFilePath = filepath.Join("scripts/generate_reel_hooks", strings.ToLower(lang)+".json")
		}
		videosPath := filepath.Join(mustEnv("META_ADS_VIDEOS_PATH"), lang)

		allTexts, orderedSlugs, err := loadTexts(textsFilePath)
		if err != nil {
			log.Printf("[%s] ERROR: %v", lang, err)
			continue
		}

		// Resolve state-level geo targeting for this language, if any (see langStates above).
		var regionKeys []string
		if states := langStates[lang]; len(states) > 0 {
			countryCode := strings.TrimSpace(strings.Split(*countries, ",")[0])
			for _, state := range states {
				key, resolveErr := resolveRegionKey(ctx, httpClient, token, countryCode, state)
				if resolveErr != nil {
					log.Fatalf("resolve region key for %s (%s): %v", state, countryCode, resolveErr)
				}
				regionKeys = append(regionKeys, key)
			}
			log.Printf("[%s] targeting states %v (region keys: %v) instead of country-level %q", lang, states, regionKeys, *countries)
		}

		adsets, err := fetchAdsets(ctx, httpClient, token,
			fmt.Sprintf("%s/%s/adsets?fields=id,name,effective_status&limit=500", graphBase, campID))
		if err != nil {
			log.Printf("[%s] ERROR fetching existing adsets for campaign %s: %v", lang, campID, err)
			continue
		}
		existingAdsets := map[string]string{}
		activeCount := 0
		for _, a := range adsets {
			existingAdsets[strings.ToLower(a.Name)] = a.ID
			if a.EffectiveStatus == "ACTIVE" {
				activeCount++
			}
		}
		log.Printf("[%s] found %d existing adset(s) in campaign %s, %d active", lang, len(adsets), campID, activeCount)

		var selectedSlugs []string
		if *slugsFlag != "" {
			allowedSlugs := map[string]bool{}
			for s := range strings.SplitSeq(*slugsFlag, ",") {
				allowedSlugs[strings.TrimSpace(s)] = true
			}
			for _, slug := range orderedSlugs {
				if allowedSlugs[slug] {
					selectedSlugs = append(selectedSlugs, slug)
				}
			}
		} else {
			selectedSlugs = selectSlugs(lang, orderedSlugs, activeCount, existingAdsets, videosPath, *videoGrouping)
			if len(selectedSlugs) == 0 {
				if activeCount >= 3 {
					log.Printf("[%s] campaign has %d active adset(s) (>=3), no action needed", lang, activeCount)
				} else {
					log.Printf("[%s] no not-yet-started slugs available to fill quota (active=%d)", lang, activeCount)
				}
				continue
			}
			log.Printf("[%s] auto-selected slug(s) (active=%d): %v", lang, activeCount, selectedSlugs)
		}

		for _, slug := range selectedSlugs {
			videoPaths := globVideos(videosPath, slug)
			if len(videoPaths) == 0 {
				log.Printf("[%s] no videos found in %s, skipping", slug, videosPath)
				continue
			}
			log.Printf("[%s] found %d video(s): %v", slug, len(videoPaths), videoPaths)

			text := allTexts[slug][lang]
			if text == "" {
				log.Printf("[%s] warn: no %q text, using slug as fallback", slug, lang)
				text = slug
			}

			process := func(name string, paths []string) {
				adsetName := name + " ad set"
				existingAdsetID := existingAdsets[strings.ToLower(adsetName)]

				if existingAdsetID != "" {
					existingAds, adsErr := fetchNamedEntities(ctx, httpClient, token,
						fmt.Sprintf("%s/%s/ads?fields=id,name&limit=500", graphBase, existingAdsetID))
					if adsErr != nil {
						log.Printf("[%s] ERROR checking existing ads in adset %s: %v", name, existingAdsetID, adsErr)
						results = append(results, result{lang: lang, slug: name, adsetID: existingAdsetID, err: adsErr})
						return
					}
					if _, adExists := existingAds[strings.ToLower(name)]; adExists {
						log.Printf("[%s] adset %q and its ad already exist, skipping", name, adsetName)
						results = append(results, result{lang: lang, slug: name, adsetID: existingAdsetID, skipped: true})
						return
					}
					log.Printf("[%s] adset %q exists but ad is missing, resuming creative/ad creation", name, adsetName)
				}

				res := result{lang: lang, slug: name}
				res.adsetID, res.creativeID, res.adID, res.err = createAdsForSlug(
					ctx, httpClient, token,
					*adAccountID, campID, *pageID,
					name, paths, text, text,
					*dailyBudget, *startTime,
					*appID, *storeURL, *conversionEvent,
					*androidOSMin, *androidOSMax, *countries, regionKeys, existingAdsetID,
					*cta, ctaLink, *igUserID,
				)
				if res.err != nil {
					log.Printf("[%s] ERROR: %v", name, res.err)
				} else {
					log.Printf("[%s] done — adset_id=%s  creative_id=%s  ad_id=%s", name, res.adsetID, res.creativeID, res.adID)
				}
				results = append(results, res)
			}

			if *videoGrouping == groupingShared {
				process(slug, videoPaths)
			} else {
				for _, vp := range videoPaths {
					base := strings.TrimSuffix(filepath.Base(vp), filepath.Ext(vp))
					process(base, []string{vp})
				}
			}
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	for _, r := range results {
		switch {
		case r.skipped:
			fmt.Printf("SKIP  [%s] %-40s  adset=%s and ad already exist\n", r.lang, r.slug, r.adsetID)
		case r.err != nil:
			fmt.Printf("FAIL  [%s] %-40s  %v\n", r.lang, r.slug, r.err)
		default:
			fmt.Printf("OK    [%s] %-40s  adset=%s  creative=%s  ad=%s\n", r.lang, r.slug, r.adsetID, r.creativeID, r.adID)
		}
	}
}

func createAdsForSlug(
	ctx context.Context, httpClient *http.Client, token,
	adAccountID, campaignID, pageID,
	slug string, videoPaths []string,
	message, headline string,
	dailyBudget int, startTime,
	appID, storeURL, conversionEvent,
	androidOSMin, androidOSMax, countries string, regionKeys []string, existingAdsetID string,
	cta, ctaLink, igUserID string,
) (adsetID, creativeID, adID string, err error) {
	adsetName := slug + " ad set"
	adName := slug

	// Upload each video + extract and upload its thumbnail
	assets := make([]videoAsset, 0, len(videoPaths))
	for i, vp := range videoPaths {
		log.Printf("[%s][%d/%d] uploading video: %s", slug, i+1, len(videoPaths), vp)
		vid, uploadErr := uploadVideo(ctx, httpClient, token, adAccountID, vp)
		if uploadErr != nil {
			return "", "", "", fmt.Errorf("upload video %s: %w", vp, uploadErr)
		}
		log.Printf("[%s][%d/%d] video_id=%s", slug, i+1, len(videoPaths), vid)

		log.Printf("[%s][%d/%d] waiting for video to finish processing", slug, i+1, len(videoPaths))
		if waitErr := waitForVideo(ctx, httpClient, token, vid); waitErr != nil {
			return "", "", "", fmt.Errorf("video %s never became ready: %w", vid, waitErr)
		}

		log.Printf("[%s][%d/%d] extracting thumbnail via ffmpeg", slug, i+1, len(videoPaths))
		thumbPath, thumbErr := extractThumbnail(vp)
		if thumbErr != nil {
			return "", "", "", fmt.Errorf("extract thumbnail for %s: %w", vp, thumbErr)
		}
		defer os.Remove(thumbPath) //nolint:gocritic

		log.Printf("[%s][%d/%d] uploading thumbnail", slug, i+1, len(videoPaths))
		imageHash, imgErr := uploadImage(ctx, httpClient, token, adAccountID, thumbPath)
		if imgErr != nil {
			return "", "", "", fmt.Errorf("upload thumbnail for %s: %w", vp, imgErr)
		}
		log.Printf("[%s][%d/%d] image_hash=%s", slug, i+1, len(videoPaths), imageHash)

		assets = append(assets, videoAsset{videoID: vid, imageHash: imageHash})
	}

	isDynamic := len(assets) > 1
	if existingAdsetID != "" {
		adsetID = existingAdsetID
		log.Printf("[%s] reusing existing adset_id=%s", slug, adsetID)
	} else {
		log.Printf("[%s] creating ad set (dynamic_creative=%v)", slug, isDynamic)
		adsetID, err = createAdset(ctx, httpClient, token,
			adAccountID, adsetName, campaignID,
			dailyBudget, startTime,
			appID, storeURL, conversionEvent,
			androidOSMin, androidOSMax, countries, regionKeys,
			isDynamic)
		if err != nil {
			return "", "", "", fmt.Errorf("create adset: %w", err)
		}
		log.Printf("[%s] adset_id=%s", slug, adsetID)
	}

	log.Printf("[%s] creating creative", slug)
	creativeID, err = createCreative(ctx, httpClient, token,
		adAccountID, slug, pageID,
		assets, message, headline, cta, ctaLink, igUserID)
	if err != nil {
		return "", "", "", fmt.Errorf("create creative: %w", err)
	}
	log.Printf("[%s] creative_id=%s", slug, creativeID)

	log.Printf("[%s] creating ad", slug)
	adID, err = createAd(ctx, httpClient, token, adAccountID, adName, adsetID, creativeID)
	if err != nil {
		return "", "", "", fmt.Errorf("create ad: %w", err)
	}
	log.Printf("[%s] ad_id=%s", slug, adID)

	return adsetID, creativeID, adID, nil
}

// uploadVideo uploads a local video file to the ad account and returns the video ID.
func uploadVideo(ctx context.Context, client *http.Client, token, adAccountID, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("source", filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", err
	}
	_ = w.WriteField("title", filepath.Base(path))
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/act_%s/advideos", graphVideoBase, adAccountID), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := doRequest(client, req)
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse response: %w — body: %s", err, body)
	}
	if r.ID == "" {
		return "", fmt.Errorf("empty video ID in response: %s", body)
	}
	return r.ID, nil
}

// extractThumbnail shells out to ffmpeg to extract the first frame of the video.
// waitForVideo polls the video status until it is ready or an error occurs.
func waitForVideo(ctx context.Context, client *http.Client, token, videoID string) error {
	for {
		endpoint := fmt.Sprintf("%s/%s?fields=status", graphBase, videoID)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		body, err := doRequest(client, req)
		if err != nil {
			return err
		}
		var r struct {
			Status struct {
				VideoStatus string `json:"video_status"`
			} `json:"status"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return fmt.Errorf("parse video status: %w — body: %s", err, body)
		}
		switch r.Status.VideoStatus {
		case "ready":
			return nil
		case "error":
			return fmt.Errorf("video processing failed: %s", body)
		default:
			log.Printf("  video status=%q, retrying in 5s…", r.Status.VideoStatus)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// resolveRegionKey looks up Meta's targeting region key for a state/region name within
// the given country via the Targeting Search API — used to build geo_locations.regions.
func resolveRegionKey(ctx context.Context, client *http.Client, token, countryCode, name string) (string, error) {
	q := url.Values{}
	q.Set("type", "adgeolocation")
	q.Set("location_types", `["region"]`)
	q.Set("q", name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/search?%s", graphBase, q.Encode()), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := doRequest(client, req)
	if err != nil {
		return "", err
	}
	var r struct {
		Data []struct {
			Key         string `json:"key"`
			Name        string `json:"name"`
			CountryCode string `json:"country_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse adgeolocation search response: %w — body: %s", err, body)
	}
	for _, d := range r.Data {
		if strings.EqualFold(d.CountryCode, countryCode) && strings.EqualFold(d.Name, name) {
			return d.Key, nil
		}
	}
	return "", fmt.Errorf("no region match for %q in %s — body: %s", name, countryCode, body)
}

// fetchNamedEntities follows a paginated Graph API list edge (e.g. a campaign's adsets,
// or an adset's ads) and returns a map of lowercased name -> id for every item found.
// Used to detect entities that already exist so reruns can skip or resume them.
func fetchNamedEntities(ctx context.Context, client *http.Client, token, endpoint string) (map[string]string, error) {
	entities := map[string]string{}
	for endpoint != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		body, err := doRequest(client, req)
		if err != nil {
			return nil, err
		}
		var r struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("parse list response: %w — body: %s", err, body)
		}
		for _, d := range r.Data {
			entities[strings.ToLower(d.Name)] = d.ID
		}
		endpoint = r.Paging.Next
	}
	return entities, nil
}

// defaultStartTime returns the default ad set start time given the time the script is
// run: 40 minutes from now, unless run between 9pm and 5am IST, in which case it
// returns 6am IST (same day if run before 5am, next day if run at/after 9pm) to
// avoid starting ad sets overnight.
func defaultStartTime(now time.Time) string {
	ist := time.FixedZone("IST", 19800) // UTC+5:30
	nowIST := now.In(ist)
	hour := nowIST.Hour()
	day := nowIST.Day()
	if hour >= 21 || hour < 5 {
		if hour >= 21 {
			day++
		}
		return time.Date(nowIST.Year(), nowIST.Month(), day, 6, 0, 0, 0, ist).Format(time.RFC3339)
	}
	return now.Add(40 * time.Minute).Format(time.RFC3339)
}

type adsetSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	EffectiveStatus string `json:"effective_status"`
}

// fetchAdsets follows a paginated adsets list edge and returns every adset's
// id/name/effective_status, so callers can derive both a name->id lookup and an
// active-adset count from one call.
func fetchAdsets(ctx context.Context, client *http.Client, token, endpoint string) ([]adsetSummary, error) {
	var out []adsetSummary
	for endpoint != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		body, err := doRequest(client, req)
		if err != nil {
			return nil, err
		}
		var r struct {
			Data   []adsetSummary `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("parse adsets response: %w — body: %s", err, body)
		}
		out = append(out, r.Data...)
		endpoint = r.Paging.Next
	}
	return out, nil
}

// loadTexts reads the texts JSON file (slug -> lang -> text) and returns both the
// parsed map and the slugs in the literal order they appear in the file — slug
// auto-selection must respect "first-listed slug = highest priority", which a plain
// map unmarshal can't preserve.
func loadTexts(path string) (map[string]map[string]string, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read texts file %s: %w", path, err)
	}

	var allTexts map[string]map[string]string
	if err := json.Unmarshal(raw, &allTexts); err != nil {
		return nil, nil, fmt.Errorf("parse texts file %s: %w", path, err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	var ordered []string
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, fmt.Errorf("parse texts file %s (order pass): %w", path, err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, nil, fmt.Errorf("texts file %s: expected top-level JSON object", path)
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("parse texts file %s (order pass): %w", path, err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("texts file %s: expected string key", path)
		}
		ordered = append(ordered, key)
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			return nil, nil, fmt.Errorf("parse texts file %s (order pass, value for %q): %w", path, key, err)
		}
	}

	return allTexts, ordered, nil
}

// globVideos returns every video file in videosPath whose base name starts with slug.
func globVideos(videosPath, slug string) []string {
	matches, err := filepath.Glob(filepath.Join(videosPath, slug+"*"))
	if err != nil {
		return nil
	}
	var videoPaths []string
	for _, m := range matches {
		ext := strings.ToLower(filepath.Ext(m))
		if ext == ".mp4" || ext == ".mov" || ext == ".avi" || ext == ".mkv" {
			videoPaths = append(videoPaths, m)
		}
	}
	return videoPaths
}

// unitNamesForSlug returns the adset-unit name(s) this slug would produce under the
// given grouping mode: the slug itself for "shared", or one entry per matching video's
// basename for "unique". Returns nil if no videos are found for the slug.
func unitNamesForSlug(slug, videosPath, videoGrouping string) []string {
	videoPaths := globVideos(videosPath, slug)
	if len(videoPaths) == 0 {
		return nil
	}
	if videoGrouping == groupingShared {
		return []string{slug}
	}
	names := make([]string, len(videoPaths))
	for i, vp := range videoPaths {
		names[i] = strings.TrimSuffix(filepath.Base(vp), filepath.Ext(vp))
	}
	return names
}

// slugNotStarted reports whether a slug has videos and none of its adset-unit-names
// have an existing adset in this campaign yet.
func slugNotStarted(slug string, existingAdsets map[string]string, videosPath, videoGrouping string) bool {
	unitNames := unitNamesForSlug(slug, videosPath, videoGrouping)
	if len(unitNames) == 0 {
		return false
	}
	for _, name := range unitNames {
		if _, exists := existingAdsets[strings.ToLower(name+" ad set")]; exists {
			return false
		}
	}
	return true
}

// selectSlugs walks orderedSlugs (in file order) and returns up to a quota-limited
// number of not-yet-started slugs, based on how many adsets are currently active in
// the campaign: 0 active -> up to 2 slugs, 1-2 active -> up to 1 slug, >=3 -> none.
// Slugs with no matching video files are logged and skipped over so the quota can
// still be filled from later slugs.
func selectSlugs(lang string, orderedSlugs []string, activeCount int, existingAdsets map[string]string, videosPath, videoGrouping string) []string {
	var quota int
	switch {
	case activeCount == 0:
		quota = 2
	case activeCount < 3:
		quota = 1
	default:
		quota = 0
	}
	if quota == 0 {
		return nil
	}

	var selected []string
	for _, slug := range orderedSlugs {
		if len(selected) >= quota {
			break
		}
		if len(globVideos(videosPath, slug)) == 0 {
			log.Printf("[%s] no videos found for slug %q, skipping", lang, slug)
			continue
		}
		if slugNotStarted(slug, existingAdsets, videosPath, videoGrouping) {
			selected = append(selected, slug)
		}
	}
	return selected
}

// Returns the path to a temp JPEG file; caller must delete it.
func extractThumbnail(videoPath string) (string, error) {
	out := filepath.Join(os.TempDir(), fmt.Sprintf("meta_thumb_%d.jpg", time.Now().UnixNano()))
	cmd := exec.Command("ffmpeg", "-i", videoPath, "-vframes", "1", "-ss", "0", "-y", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg error (is ffmpeg installed?): %w — output: %s", err, b)
	}
	return out, nil
}

// uploadImage uploads a local image file to the ad account and returns the image hash.
// Meta's adimages API requires the field name to match the filename.
func uploadImage(ctx context.Context, client *http.Client, token, adAccountID, imagePath string) (string, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}
	fileName := filepath.Base(imagePath)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(fileName, fileName)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/act_%s/adimages", graphBase, adAccountID), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := doRequest(client, req)
	if err != nil {
		return "", err
	}
	var r struct {
		Images map[string]struct {
			Hash string `json:"hash"`
		} `json:"images"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse adimages response: %w — body: %s", err, body)
	}
	for _, img := range r.Images {
		if img.Hash != "" {
			return img.Hash, nil
		}
	}
	return "", fmt.Errorf("no image hash in response: %s", body)
}

// createAdset creates an active ad set under the given campaign.
// When isDynamic is true, is_dynamic_creative is enabled (required for asset_feed_spec creatives).
func createAdset(
	ctx context.Context, client *http.Client, token,
	adAccountID, name, campaignID string,
	dailyBudget int, startTime,
	appID, storeURL, conversionEvent,
	androidOSMin, androidOSMax, countries string, regionKeys []string,
	isDynamic bool,
) (string, error) {
	countryList := strings.Split(countries, ",")
	for i := range countryList {
		countryList[i] = strings.TrimSpace(countryList[i])
	}

	var userOS string
	switch {
	case androidOSMin != "" && androidOSMax != "":
		userOS = fmt.Sprintf("Android_ver_%s_to_%s", androidOSMin, androidOSMax)
	case androidOSMin != "":
		userOS = fmt.Sprintf("Android_ver_%s_and_above", androidOSMin)
	default:
		userOS = "Android"
	}

	type regionSpec struct {
		Key string `json:"key"`
	}
	type geoLoc struct {
		Countries []string     `json:"countries,omitempty"`
		Regions   []regionSpec `json:"regions,omitempty"`
	}
	type targetingSpec struct {
		PublisherPlatforms []string `json:"publisher_platforms"`
		DevicePlatforms    []string `json:"device_platforms"`
		UserOS             []string `json:"user_os"`
		UserDevice         []string `json:"user_device"`
		GeoLocations       geoLoc   `json:"geo_locations"`
	}
	geo := geoLoc{Countries: countryList}
	if len(regionKeys) > 0 {
		regions := make([]regionSpec, len(regionKeys))
		for i, k := range regionKeys {
			regions[i] = regionSpec{Key: k}
		}
		geo = geoLoc{Regions: regions}
	}
	tgt := targetingSpec{
		PublisherPlatforms: []string{"facebook", "instagram"},
		DevicePlatforms:    []string{"mobile"},
		UserOS:             []string{userOS},
		UserDevice:         []string{"Android_Smartphone"},
		GeoLocations:       geo,
	}
	tgtJSON, err := json.Marshal(tgt)
	if err != nil {
		return "", err
	}

	poJSON, err := json.Marshal(map[string]string{
		"application_id":    appID,
		"object_store_url":  storeURL,
		"custom_event_type": conversionEvent,
	})
	if err != nil {
		return "", err
	}

	fields := url.Values{}
	fields.Set("name", name)
	fields.Set("campaign_id", campaignID)
	fields.Set("daily_budget", fmt.Sprintf("%d", dailyBudget))
	fields.Set("billing_event", "IMPRESSIONS")
	fields.Set("optimization_goal", "OFFSITE_CONVERSIONS")
	fields.Set("bid_strategy", "LOWEST_COST_WITHOUT_CAP")
	fields.Set("start_time", startTime)
	fields.Set("status", "ACTIVE")
	fields.Set("destination_type", "APP")
	fields.Set("promoted_object", string(poJSON))
	fields.Set("targeting", string(tgtJSON))
	if isDynamic {
		fields.Set("is_dynamic_creative", "true")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/act_%s/adsets", graphBase, adAccountID),
		strings.NewReader(fields.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := doRequest(client, req)
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse adsets response: %w — body: %s", err, body)
	}
	if r.ID == "" {
		return "", fmt.Errorf("empty adset ID in response: %s", body)
	}
	return r.ID, nil
}

// createCreative creates the ad creative.
// Single video uses object_story_spec; multiple videos use asset_feed_spec (Dynamic Creative).
func createCreative(
	ctx context.Context, client *http.Client, token,
	adAccountID, name, pageID string,
	assets []videoAsset,
	message, headline, cta, linkURL, igUserID string,
) (string, error) {
	ctaValue := map[string]string{}
	if linkURL != "" {
		ctaValue["link"] = linkURL
	}
	ctaSpec := map[string]any{
		"type":  cta,
		"value": ctaValue,
	}

	fields := url.Values{}
	fields.Set("name", name)

	if len(assets) == 1 {
		vd := map[string]any{
			"video_id":       assets[0].videoID,
			"image_hash":     assets[0].imageHash,
			"call_to_action": ctaSpec,
		}
		if message != "" {
			vd["message"] = message
		}
		if headline != "" {
			vd["title"] = headline
		}
		ossMap := map[string]any{
			"page_id":    pageID,
			"video_data": vd,
		}
		if igUserID != "" {
			ossMap["instagram_user_id"] = igUserID
		}
		oss, err := json.Marshal(ossMap)
		if err != nil {
			return "", err
		}
		fields.Set("object_story_spec", string(oss))
	} else {
		videos := make([]map[string]string, len(assets))
		for i, a := range assets {
			videos[i] = map[string]string{
				"video_id":       a.videoID,
				"thumbnail_hash": a.imageHash,
			}
		}
		afs := map[string]any{
			"videos":               videos,
			"ad_formats":           []string{"SINGLE_VIDEO"},
			"call_to_action_types": []string{cta},
		}
		if linkURL != "" {
			afs["link_urls"] = []map[string]string{{"website_url": linkURL}}
		}
		if headline != "" {
			afs["titles"] = []map[string]string{{"text": headline}}
		}
		if message != "" {
			afs["bodies"] = []map[string]string{{"text": message}}
		}
		afsJSON, err := json.Marshal(afs)
		if err != nil {
			return "", err
		}
		fields.Set("asset_feed_spec", string(afsJSON))

		ossMap := map[string]any{"page_id": pageID}
		if igUserID != "" {
			ossMap["instagram_user_id"] = igUserID
		}
		oss, err := json.Marshal(ossMap)
		if err != nil {
			return "", err
		}
		fields.Set("object_story_spec", string(oss))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/act_%s/adcreatives", graphBase, adAccountID),
		strings.NewReader(fields.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := doRequest(client, req)
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse adcreatives response: %w — body: %s", err, body)
	}
	if r.ID == "" {
		return "", fmt.Errorf("empty creative ID in response: %s", body)
	}
	return r.ID, nil
}

// createAd creates an active ad in the given ad set referencing the creative.
func createAd(ctx context.Context, client *http.Client, token, adAccountID, name, adsetID, creativeID string) (string, error) {
	creative, err := json.Marshal(map[string]string{"creative_id": creativeID})
	if err != nil {
		return "", err
	}

	fields := url.Values{}
	fields.Set("name", name)
	fields.Set("adset_id", adsetID)
	fields.Set("creative", string(creative))
	fields.Set("status", "ACTIVE")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/act_%s/ads", graphBase, adAccountID),
		strings.NewReader(fields.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := doRequest(client, req)
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse ads response: %w — body: %s", err, body)
	}
	if r.ID == "" {
		return "", fmt.Errorf("empty ad ID in response: %s", body)
	}
	return r.ID, nil
}

func doRequest(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return body, nil
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
