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
)

var languages = []string{"Hindi", "Tamil", "Marathi", "Gujarati", "Bengali", "Telugu", "Kannada"}

func main() {
	goEnv := os.Getenv("GO_ENV")
	if goEnv == "" {
		goEnv = "local"
	}
	_ = godotenv.Load(".env." + goEnv)
	_ = godotenv.Load()

	campaignID := flag.String("campaign-id", "", "existing Meta campaign ID that already contains one adset per language (required)")
	adAccountID := flag.String("ad-account-id", os.Getenv("META_AD_ACCOUNT_ID"), "ad account ID without act_ prefix (required)")
	pageID := flag.String("page-id", os.Getenv("META_PAGE_ID"), "Facebook page ID (required)")
	appID := flag.String("app-id", os.Getenv("META_APP_ID"), "Facebook app ID (required)")
	storeURL := flag.String("store-url", os.Getenv("META_STORE_URL"), "Play Store URL for the app (required)")
	conversionEvent := flag.String("conversion-event", "START_TRIAL", "app event name e.g. START_TRIAL, PURCHASE, COMPLETE_REGISTRATION (required)")
	linkURL := flag.String("link-url", "", "destination URL for CTA e.g. Play Store URL (optional, defaults to -store-url)")
	textsFile := flag.String("texts-file", "", "path to texts JSON file (default: scripts/generate_reel_hooks/<lang-lowercase>.json)")
	langFlag := flag.String("lang", "", "language adset to process: Hindi, Tamil, Marathi, Gujarati, Bengali, Telugu, Kannada (optional; omit to process every language adset in the campaign)")
	cta := flag.String("cta", "INSTALL_MOBILE_APP", "call-to-action button type")
	igUserID := flag.String("instagram-user-id", os.Getenv("META_IG_USER_ID"), "IG Business Account ID for Instagram delivery")
	minActive := flag.Int("min-active", 4, "target number of active ads per language adset")
	flag.Parse()

	if *campaignID == "" {
		log.Fatalf("flag -campaign-id is required")
	}
	if *langFlag != "" && !containsFold(languages, *langFlag) {
		log.Fatalf("flag -lang %q is not supported; valid values: Hindi, Tamil, Marathi, Gujarati, Bengali, Telugu, Kannada", *langFlag)
	}
	if *minActive <= 0 {
		log.Fatalf("flag -min-active must be > 0")
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
	videosBasePath := mustEnv("META_ADS_VIDEOS_PATH")
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	ctx := context.Background()

	ctaLink := *linkURL
	if ctaLink == "" {
		ctaLink = *storeURL
	}

	langs := languages
	if *langFlag != "" {
		langs = []string{*langFlag}
	}

	adsets, err := fetchNamedList(ctx, httpClient, token,
		fmt.Sprintf("%s/%s/adsets?fields=id,name,effective_status&limit=500", graphBase, *campaignID))
	if err != nil {
		log.Fatalf("fetch adsets for campaign %s: %v", *campaignID, err)
	}
	adsetsByLang := map[string]string{}
	for _, a := range adsets {
		adsetsByLang[strings.ToLower(a.Name)] = a.ID
	}
	log.Printf("found %d adset(s) in campaign %s", len(adsets), *campaignID)

	type result struct {
		lang string
		slug string
		adID string
		err  error
	}
	var results []result

	for _, lang := range langs {
		adsetID, ok := adsetsByLang[strings.ToLower(lang)]
		if !ok {
			log.Printf("[%s] ERROR: no adset named %q found in campaign %s, skipping", lang, lang, *campaignID)
			continue
		}

		ads, err := fetchNamedList(ctx, httpClient, token,
			fmt.Sprintf("%s/%s/ads?fields=id,name,effective_status&limit=500", graphBase, adsetID))
		if err != nil {
			log.Printf("[%s] ERROR fetching existing ads for adset %s: %v", lang, adsetID, err)
			continue
		}
		activeCount := 0
		existingAdNames := map[string]bool{}
		for _, a := range ads {
			existingAdNames[strings.ToLower(a.Name)] = true
			if isActiveOrPending(a.EffectiveStatus) {
				activeCount++
			}
		}
		log.Printf("[%s] adset %s has %d ad(s), %d active", lang, adsetID, len(ads), activeCount)

		if activeCount >= *minActive {
			log.Printf("[%s] already has %d active ad(s) (>= %d), no action needed", lang, activeCount, *minActive)
			continue
		}
		needed := *minActive - activeCount

		textsFilePath := *textsFile
		if textsFilePath == "" {
			textsFilePath = filepath.Join("scripts/generate_reel_hooks", strings.ToLower(lang)+".json")
		}
		allTexts, orderedSlugs, err := loadTexts(textsFilePath)
		if err != nil {
			log.Printf("[%s] ERROR: %v", lang, err)
			continue
		}
		videosPath := filepath.Join(videosBasePath, lang)

		created := 0
		for _, slug := range orderedSlugs {
			if created >= needed {
				break
			}
			if existingAdNames[strings.ToLower(slug)] {
				continue
			}
			videoPaths := globVideos(videosPath, slug)
			if len(videoPaths) == 0 {
				log.Printf("[%s] no video found for slug %q in %s, skipping", lang, slug, videosPath)
				continue
			}
			if len(videoPaths) > 1 {
				log.Printf("[%s] multiple videos found for slug %q, using first: %s", lang, slug, videoPaths[0])
			}
			videoPath := videoPaths[0]

			text := allTexts[slug][lang]
			if text == "" {
				log.Printf("[%s] warn: no %q text for slug %q, using slug as fallback", lang, lang, slug)
				text = slug
			}

			log.Printf("[%s] creating ad for slug %q (%d/%d needed)", lang, slug, created+1, needed)
			adID, adErr := createAdForSlug(ctx, httpClient, token,
				*adAccountID, *pageID, slug, videoPath, text, text,
				adsetID, *cta, ctaLink, *igUserID)
			if adErr != nil {
				log.Printf("[%s] ERROR creating ad for slug %q: %v", lang, slug, adErr)
				results = append(results, result{lang: lang, slug: slug, err: adErr})
				continue
			}
			log.Printf("[%s] slug %q done — ad_id=%s", lang, slug, adID)
			results = append(results, result{lang: lang, slug: slug, adID: adID})
			existingAdNames[strings.ToLower(slug)] = true
			created++
		}

		if created < needed {
			log.Printf("[%s] warn: only created %d/%d needed ad(s) — ran out of not-yet-used slugs with videos", lang, created, needed)
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	for _, r := range results {
		switch {
		case r.err != nil:
			fmt.Printf("FAIL  [%s] %-40s  %v\n", r.lang, r.slug, r.err)
		default:
			fmt.Printf("OK    [%s] %-40s  ad=%s\n", r.lang, r.slug, r.adID)
		}
	}
}

// createAdForSlug uploads a single video, extracts+uploads its thumbnail, creates a
// single-video creative, and creates an active ad for it in the given (already
// existing) adset.
func createAdForSlug(
	ctx context.Context, httpClient *http.Client, token,
	adAccountID, pageID, slug, videoPath, message, headline, adsetID string,
	cta, ctaLink, igUserID string,
) (adID string, err error) {
	log.Printf("[%s] uploading video: %s", slug, videoPath)
	videoID, err := uploadVideo(ctx, httpClient, token, adAccountID, videoPath)
	if err != nil {
		return "", fmt.Errorf("upload video %s: %w", videoPath, err)
	}
	log.Printf("[%s] video_id=%s", slug, videoID)

	log.Printf("[%s] waiting for video to finish processing", slug)
	if err := waitForVideo(ctx, httpClient, token, videoID); err != nil {
		return "", fmt.Errorf("video %s never became ready: %w", videoID, err)
	}

	log.Printf("[%s] extracting thumbnail via ffmpeg", slug)
	thumbPath, err := extractThumbnail(videoPath)
	if err != nil {
		return "", fmt.Errorf("extract thumbnail for %s: %w", videoPath, err)
	}
	defer os.Remove(thumbPath)

	log.Printf("[%s] uploading thumbnail", slug)
	imageHash, err := uploadImage(ctx, httpClient, token, adAccountID, thumbPath)
	if err != nil {
		return "", fmt.Errorf("upload thumbnail for %s: %w", videoPath, err)
	}
	log.Printf("[%s] image_hash=%s", slug, imageHash)

	log.Printf("[%s] creating creative", slug)
	creativeID, err := createCreative(ctx, httpClient, token,
		adAccountID, slug, pageID, videoID, imageHash, message, headline, cta, ctaLink, igUserID)
	if err != nil {
		return "", fmt.Errorf("create creative: %w", err)
	}
	log.Printf("[%s] creative_id=%s", slug, creativeID)

	log.Printf("[%s] creating ad", slug)
	adID, err = createAd(ctx, httpClient, token, adAccountID, slug, adsetID, creativeID)
	if err != nil {
		return "", fmt.Errorf("create ad: %w", err)
	}
	log.Printf("[%s] ad_id=%s", slug, adID)

	return adID, nil
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

	body, err := doRequest(ctx, client, req)
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

// waitForVideo polls the video status until it is ready or an error occurs.
func waitForVideo(ctx context.Context, client *http.Client, token, videoID string) error {
	for {
		endpoint := fmt.Sprintf("%s/%s?fields=status", graphBase, videoID)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		body, err := doRequest(ctx, client, req)
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

// listItem is a Graph API list entry with the fields needed to resolve adsets/ads by
// name and to count how many are currently active.
type listItem struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	EffectiveStatus string `json:"effective_status"`
}

// fetchNamedList follows a paginated Graph API list edge (e.g. a campaign's adsets, or
// an adset's ads) and returns every item's id/name/effective_status.
func fetchNamedList(ctx context.Context, client *http.Client, token, endpoint string) ([]listItem, error) {
	var out []listItem
	for endpoint != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		body, err := doRequest(ctx, client, req)
		if err != nil {
			return nil, err
		}
		var r struct {
			Data   []listItem `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("parse list response: %w — body: %s", err, body)
		}
		out = append(out, r.Data...)
		endpoint = r.Paging.Next
	}
	return out, nil
}

// loadTexts reads the texts JSON file (slug -> lang -> text) and returns both the
// parsed map and the slugs in the literal order they appear in the file — slug
// selection must respect "first-listed slug = highest priority", which a plain map
// unmarshal can't preserve.
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

	body, err := doRequest(ctx, client, req)
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

// createCreative creates a single-video ad creative via object_story_spec.
func createCreative(
	ctx context.Context, client *http.Client, token,
	adAccountID, name, pageID, videoID, imageHash, message, headline, cta, linkURL, igUserID string,
) (string, error) {
	ctaValue := map[string]string{}
	if linkURL != "" {
		ctaValue["link"] = linkURL
	}
	ctaSpec := map[string]any{
		"type":  cta,
		"value": ctaValue,
	}

	vd := map[string]any{
		"video_id":       videoID,
		"image_hash":     imageHash,
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

	fields := url.Values{}
	fields.Set("name", name)
	fields.Set("object_story_spec", string(oss))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/act_%s/adcreatives", graphBase, adAccountID),
		strings.NewReader(fields.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := doRequest(ctx, client, req)
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

	body, err := doRequest(ctx, client, req)
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

// doRequest executes req, retrying with backoff when the Graph API responds with a
// transient rate-limit error (codes 4/17/32/613 — app, account, page, or custom
// throttling). req.Body must be re-derivable via req.GetBody (true for nil, GET, or
// bodies created from a []byte/*bytes.Buffer/*strings.Reader, which covers every
// caller in this file).
func doRequest(ctx context.Context, client *http.Client, req *http.Request) ([]byte, error) {
	const maxAttempts = 3
	for attempt := 1; ; attempt++ {
		if attempt > 1 && req.GetBody != nil {
			b, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("rebuild request body for retry: %w", err)
			}
			req.Body = b
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, nil
		}
		if !isRateLimitError(body) || attempt >= maxAttempts {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
		}

		wait := time.Duration(attempt) * 30 * time.Second
		log.Printf("rate limited (attempt %d/%d): %s — waiting %s before retry", attempt, maxAttempts, body, wait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// isRateLimitError reports whether a Graph API error response body indicates a
// transient app/account/page throttling condition worth retrying (as opposed to a
// permanent error like bad params or auth failure).
func isRateLimitError(body []byte) bool {
	var r struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return false
	}
	switch r.Error.Code {
	case 4, 17, 32, 613:
		return true
	default:
		return false
	}
}

// isActiveOrPending reports whether a Graph API effective_status counts as "active" for
// quota purposes: delivering ads (ACTIVE) plus ads still in Meta's review pipeline
// (IN_PROCESS, PENDING_REVIEW) that will likely become active shortly, so they
// shouldn't be double-counted against by creating redundant new ads.
func isActiveOrPending(status string) bool {
	switch status {
	case "ACTIVE", "IN_PROCESS", "PENDING_REVIEW":
		return true
	default:
		return false
	}
}

func containsFold(list []string, val string) bool {
	for _, v := range list {
		if strings.EqualFold(v, val) {
			return true
		}
	}
	return false
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
