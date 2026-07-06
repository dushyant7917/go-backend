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

	campaignID := flag.String("campaign-id", "", "existing Meta campaign ID (optional override; resolved from -lang if omitted)")
	adAccountID := flag.String("ad-account-id", os.Getenv("META_AD_ACCOUNT_ID"), "ad account ID without act_ prefix (required)")
	pageID := flag.String("page-id", os.Getenv("META_PAGE_ID"), "Facebook page ID (required)")
	appID := flag.String("app-id", os.Getenv("META_APP_ID"), "Facebook app ID (required)")
	storeURL := flag.String("store-url", os.Getenv("META_STORE_URL"), "Play Store URL for the app (required)")
	conversionEvent := flag.String("conversion-event", "START_TRIAL", "app event name e.g. START_TRIAL, PURCHASE, COMPLETE_REGISTRATION (required)")
	linkURL := flag.String("link-url", "", "destination URL for CTA e.g. Play Store URL (optional, Meta uses app store URL from app config if omitted)")
	dailyBudget := flag.Int("daily-budget", 37500, "daily budget in paise e.g. 37500 = ₹375")
	startTime := flag.String("start-time", "", "ISO 8601 start time (default: 1 hour from now)")
	textsFile := flag.String("texts-file", "scripts/generate_reel_hooks/texts.json", "path to texts JSON file")
	textsLang := flag.String("lang", "", "language for ad copy: Hindi, Tamil, Marathi, Gujarati, Bengali, Telugu, Kannada (required)")
	cta := flag.String("cta", "INSTALL_MOBILE_APP", "call-to-action button type")
	igUserID := flag.String("instagram-user-id", os.Getenv("META_IG_USER_ID"), "IG Business Account ID for Instagram delivery")
	androidOSMin := flag.String("android-os-min", "10.0", "minimum Android OS version")
	androidOSMax := flag.String("android-os-max", "", "maximum Android OS version (empty = no upper bound)")
	countries := flag.String("countries", "IN", "comma-separated target country codes")
	slugsFlag := flag.String("slugs", "", "comma-separated slugs to process (default: all slugs in texts file)")
	flag.Parse()

	if *textsLang == "" {
		log.Fatalf("flag -lang is required (Hindi, Tamil, Marathi, Gujarati, Bengali, Telugu, Kannada)")
	}
	if _, ok := langCampaignIDs[*textsLang]; !ok {
		log.Fatalf("flag -lang %q is not supported; valid values: Hindi, Tamil, Marathi, Gujarati, Bengali, Telugu, Kannada", *textsLang)
	}
	if *campaignID == "" {
		*campaignID = langCampaignIDs[*textsLang]
	}

	if *startTime == "" {
		*startTime = time.Now().Add(time.Hour).Format(time.RFC3339)
	}

	for name, val := range map[string]string{
		"campaign-id":      *campaignID,
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
	videosPath := filepath.Join(mustEnv("META_ADS_VIDEOS_PATH"), *textsLang)

	// Load texts.json — maps slug → lang → text
	textsRaw, err := os.ReadFile(*textsFile)
	if err != nil {
		log.Fatalf("read texts file %s: %v", *textsFile, err)
	}
	var allTexts map[string]map[string]string
	if err := json.Unmarshal(textsRaw, &allTexts); err != nil {
		log.Fatalf("parse texts file %s: %v", *textsFile, err)
	}

	httpClient := &http.Client{Timeout: 5 * time.Minute}
	ctx := context.Background()

	ctaLink := *linkURL
	if ctaLink == "" {
		ctaLink = *storeURL
	}

	type result struct {
		slug      string
		adsetID   string
		creativeID string
		adID      string
		err       error
	}
	var results []result

	allowedSlugs := map[string]bool{}
	if *slugsFlag != "" {
		for s := range strings.SplitSeq(*slugsFlag, ",") {
			allowedSlugs[strings.TrimSpace(s)] = true
		}
	}

	for slug := range allTexts {
		if len(allowedSlugs) > 0 && !allowedSlugs[slug] {
			continue
		}
		// Find all video files whose base name starts with the slug
		pattern := filepath.Join(videosPath, slug+"*")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			log.Printf("[%s] glob error: %v", slug, err)
			continue
		}
		// Filter to only video files
		var videoPaths []string
		for _, m := range matches {
			ext := strings.ToLower(filepath.Ext(m))
			if ext == ".mp4" || ext == ".mov" || ext == ".avi" || ext == ".mkv" {
				videoPaths = append(videoPaths, m)
			}
		}
		if len(videoPaths) == 0 {
			log.Printf("[%s] no videos found in %s, skipping", slug, videosPath)
			continue
		}
		log.Printf("[%s] found %d video(s): %v", slug, len(videoPaths), videoPaths)

		text := allTexts[slug][*textsLang]
		if text == "" {
			log.Printf("[%s] warn: no %q text, using slug as fallback", slug, *textsLang)
			text = slug
		}

		res := result{slug: slug}
		res.adsetID, res.creativeID, res.adID, res.err = createAdsForSlug(
			ctx, httpClient, token,
			*adAccountID, *campaignID, *pageID,
			slug, videoPaths, text, text,
			*dailyBudget, *startTime,
			*appID, *storeURL, *conversionEvent,
			*androidOSMin, *androidOSMax, *countries,
			*cta, ctaLink, *igUserID,
		)
		if res.err != nil {
			log.Printf("[%s] ERROR: %v", slug, res.err)
		} else {
			log.Printf("[%s] done — adset_id=%s  creative_id=%s  ad_id=%s", slug, res.adsetID, res.creativeID, res.adID)
		}
		results = append(results, res)
	}

	fmt.Printf("\n=== Summary ===\n")
	for _, r := range results {
		if r.err != nil {
			fmt.Printf("FAIL  %-40s  %v\n", r.slug, r.err)
		} else {
			fmt.Printf("OK    %-40s  adset=%s  creative=%s  ad=%s\n", r.slug, r.adsetID, r.creativeID, r.adID)
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
	androidOSMin, androidOSMax, countries,
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
	log.Printf("[%s] creating ad set (dynamic_creative=%v)", slug, isDynamic)
	adsetID, err = createAdset(ctx, httpClient, token,
		adAccountID, adsetName, campaignID,
		dailyBudget, startTime,
		appID, storeURL, conversionEvent,
		androidOSMin, androidOSMax, countries,
		isDynamic)
	if err != nil {
		return "", "", "", fmt.Errorf("create adset: %w", err)
	}
	log.Printf("[%s] adset_id=%s", slug, adsetID)

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
	androidOSMin, androidOSMax, countries string,
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

	type geoLoc struct {
		Countries []string `json:"countries"`
	}
	type targetingSpec struct {
		PublisherPlatforms []string `json:"publisher_platforms"`
		DevicePlatforms    []string `json:"device_platforms"`
		UserOS             []string `json:"user_os"`
		UserDevice         []string `json:"user_device"`
		GeoLocations       geoLoc   `json:"geo_locations"`
	}
	tgt := targetingSpec{
		PublisherPlatforms: []string{"facebook", "instagram"},
		DevicePlatforms:    []string{"mobile"},
		UserOS:             []string{userOS},
		UserDevice:         []string{"Android_Smartphone"},
		GeoLocations:       geoLoc{Countries: countryList},
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
