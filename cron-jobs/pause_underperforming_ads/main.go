package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	graphBase = "https://graph.facebook.com/v25.0"

	createdTimeLayout = "2006-01-02T15:04:05-0700"
)

// campaignIDsFromEnv parses the comma-separated META_CAMPAIGN_IDS env var.
func campaignIDsFromEnv() []string {
	raw := os.Getenv("META_CAMPAIGN_IDS")
	var ids []string
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

type promotedObject struct {
	CustomEventType string `json:"custom_event_type"`
}

type adsetInfo struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	EffectiveStatus string         `json:"effective_status"`
	PromotedObject  promotedObject `json:"promoted_object"`
}

type adInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	EffectiveStatus string `json:"effective_status"`
	CreatedTime     string `json:"created_time"`
}

type actionValue struct {
	ActionType string `json:"action_type"`
	Value      string `json:"value"`
}

type insightsRow struct {
	Spend       string        `json:"spend"`
	Actions     []actionValue `json:"actions"`
	Conversions []actionValue `json:"conversions"`
}

func main() {
	goEnv := os.Getenv("GO_ENV")
	if goEnv == "" {
		goEnv = "local"
	}
	_ = godotenv.Load(".env." + goEnv)
	_ = godotenv.Load()

	campaignIDs := flag.String("campaign-ids", "", "comma-separated campaign IDs to check (default: META_CAMPAIGN_IDS env var)")
	adAccountID := flag.String("ad-account-id", os.Getenv("META_AD_ACCOUNT_ID"), "ad account ID without act_ prefix (required)")
	resultActionType := flag.String("result-action-type", "", `Meta insights action_type to count as 'Results' (e.g. "start_trial_total" for a START_TRIAL-optimized adset — confirmed via -metric-field=conversions discovery mode). Omit to run in discovery mode, which prints the candidate conversions sums per ad instead of making pause decisions.`)
	metricField := flag.String("metric-field", "conversions", `insights field to sum -result-action-type from: "conversions" (Meta's curated per-objective conversion count — confirmed to match Ads Manager's "Results" column) or "actions" (raw event count, does not match "Results")`)
	apply := flag.Bool("apply", false, "actually pause flagged ads (requires -result-action-type). Without this flag the script only logs what it would do.")
	flag.Parse()

	if *adAccountID == "" {
		log.Fatalf("flag -ad-account-id is required")
	}
	if *apply && *resultActionType == "" {
		log.Fatalf("-apply requires -result-action-type to be set — run in discovery mode first to confirm the correct action_type against Ads Manager")
	}
	if *metricField != "actions" && *metricField != "conversions" {
		log.Fatalf("flag -metric-field must be %q or %q", "actions", "conversions")
	}

	token := mustEnv("META_ACCESS_TOKEN")
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	ctx := context.Background()

	var campaigns []string
	if *campaignIDs != "" {
		for _, id := range strings.Split(*campaignIDs, ",") {
			if id = strings.TrimSpace(id); id != "" {
				campaigns = append(campaigns, id)
			}
		}
	} else {
		campaigns = campaignIDsFromEnv()
	}
	if len(campaigns) == 0 {
		log.Fatalf("no -campaign-ids given and META_CAMPAIGN_IDS env var is not set")
	}

	type outcome struct {
		campaignID string
		adsetName  string
		adID       string
		name       string
		spend      float64
		results    int
		cpr        float64
		pause      bool
		reason     string
		paused     bool
		pauseErr   error
	}
	var outcomes []outcome

	discovery := *resultActionType == ""

	for _, campaignID := range campaigns {
		log.Printf("=== campaign %s ===", campaignID)
		adsets, err := fetchActiveAdsets(ctx, httpClient, token, campaignID)
		if err != nil {
			log.Printf("[%s] ERROR fetching adsets: %v", campaignID, err)
			continue
		}
		log.Printf("[%s] %d active adset(s)", campaignID, len(adsets))

		for _, as := range adsets {
			ads, err := fetchActiveAds(ctx, httpClient, token, as.ID)
			if err != nil {
				log.Printf("[%s] adset %s (%s): ERROR fetching ads: %v", campaignID, as.ID, as.Name, err)
				continue
			}
			log.Printf("[%s] adset %s (%s): %d active ad(s)", campaignID, as.ID, as.Name, len(ads))

			for _, ad := range ads {
				createdTime, timeErr := parseCreatedTime(ad.CreatedTime)
				if timeErr != nil {
					log.Printf("[%s] adset %s: ad %s (%s): ERROR parsing created_time %q: %v", campaignID, as.Name, ad.ID, ad.Name, ad.CreatedTime, timeErr)
					continue
				}
				now := time.Now()
				if createdTime.After(now) {
					log.Printf("[%s] adset %s: ad %s (%s): created %s, skipping (not yet delivering)", campaignID, as.Name, ad.ID, ad.Name, createdTime.Format(time.RFC3339))
					continue
				}
				since := createdTime.Format("2006-01-02")
				until := now.Format("2006-01-02")

				row, insErr := fetchInsights(ctx, httpClient, token, ad.ID, since, until)
				if insErr != nil {
					log.Printf("[%s] adset %s: ad %s (%s): ERROR fetching insights: %v", campaignID, as.Name, ad.ID, ad.Name, insErr)
					continue
				}
				spend, _ := strconv.ParseFloat(row.Spend, 64)

				event := strings.ToLower(as.PromotedObject.CustomEventType)

				if discovery {
					totalType := event + "_total"
					results := sumAction(row.Conversions, totalType)
					log.Printf("[%s] adset %s: %s: spend=%.2f results=%d", campaignID, as.Name, ad.Name, spend, results)
					continue
				}

				actionSource := row.Actions
				if *metricField == "conversions" {
					actionSource = row.Conversions
				}
				results := sumAction(actionSource, *resultActionType)
				var cpr float64
				if results > 0 {
					cpr = spend / float64(results)
				}
				pause, reason := evaluate(results, spend, cpr)
				log.Printf("[%s] adset %s: %s: spend=%.2f results=%d", campaignID, as.Name, ad.Name, spend, results)

				outcomes = append(outcomes, outcome{
					campaignID: campaignID,
					adsetName:  as.Name,
					adID:       ad.ID,
					name:       ad.Name,
					spend:      spend,
					results:    results,
					cpr:        cpr,
					pause:      pause,
					reason:     reason,
				})
			}
		}
	}

	if discovery {
		fmt.Println("\n=== Discovery mode: no pause decisions made. Compare the sums above against Ads Manager's Results column, then re-run with -result-action-type set. ===")
		return
	}

	if *apply {
		for i, o := range outcomes {
			if !o.pause {
				continue
			}
			err := pauseAd(ctx, httpClient, token, o.adID)
			outcomes[i].paused = err == nil
			outcomes[i].pauseErr = err
			if err != nil {
				log.Printf("[%s] ad %s: ERROR pausing: %v", o.campaignID, o.adID, err)
			} else {
				log.Printf("[%s] ad %s: paused", o.campaignID, o.adID)
			}
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	for _, o := range outcomes {
		switch {
		case !o.pause:
			cprStr := "NA"
			if o.results > 0 {
				cprStr = fmt.Sprintf("%.2f", o.cpr)
			}
			fmt.Printf("OK    [%s] adset=%-25s ad=%-30s  results=%d cpr=%s spend=%.2f\n", o.campaignID, o.adsetName, o.name, o.results, cprStr, o.spend)
		case o.pause && !*apply:
			fmt.Printf("DRY   [%s] adset=%-25s ad=%-30s  would pause: %s\n", o.campaignID, o.adsetName, o.name, o.reason)
		case o.pauseErr != nil:
			fmt.Printf("FAIL  [%s] adset=%-25s ad=%-30s  pause error: %v\n", o.campaignID, o.adsetName, o.name, o.pauseErr)
		default:
			fmt.Printf("PAUSE [%s] adset=%-25s ad=%-30s  %s\n", o.campaignID, o.adsetName, o.name, o.reason)
		}
	}
}

// evaluate applies the CPR kill-switch thresholds. Bands are checked from the highest
// results lower-bound down, so a matching band's threshold is the only one considered.
func evaluate(results int, spend, cpr float64) (pause bool, reason string) {
	switch {
	case results >= 20:
		if cpr > 100 {
			return true, fmt.Sprintf("results=%d (>=20) cpr=%.2f > 100", results, cpr)
		}
	case results >= 10:
		if cpr > 110 {
			return true, fmt.Sprintf("results=%d (10-19) cpr=%.2f > 110", results, cpr)
		}
	case results >= 5:
		if cpr > 120 {
			return true, fmt.Sprintf("results=%d (5-9) cpr=%.2f > 120", results, cpr)
		}
	case results >= 2:
		if cpr > 150 {
			return true, fmt.Sprintf("results=%d (2-4) cpr=%.2f > 150", results, cpr)
		}
	case results == 1:
		if cpr > 170 {
			return true, fmt.Sprintf("results=1 cpr=%.2f > 170", cpr)
		}
	case results == 0:
		if spend > 125 {
			return true, fmt.Sprintf("results=0 spend=%.2f > 125", spend)
		}
	}
	return false, ""
}

// parseCreatedTime parses a Graph API created_time timestamp.
func parseCreatedTime(s string) (time.Time, error) {
	t, err := time.Parse(createdTimeLayout, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, err
		}
	}
	return t, nil
}

// sumAction sums the (string-encoded) values of every actions[] entry matching actionType.
func sumAction(actions []actionValue, actionType string) int {
	total := 0.0
	for _, a := range actions {
		if a.ActionType != actionType {
			continue
		}
		v, err := strconv.ParseFloat(a.Value, 64)
		if err != nil {
			continue
		}
		total += v
	}
	return int(math.Round(total))
}

// fetchActiveAdsets returns every adset in campaignID whose effective_status is ACTIVE.
func fetchActiveAdsets(ctx context.Context, client *http.Client, token, campaignID string) ([]adsetInfo, error) {
	var out []adsetInfo
	endpoint := fmt.Sprintf("%s/%s/adsets?fields=id,name,effective_status,promoted_object&limit=500", graphBase, campaignID)
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
			Data   []adsetInfo `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("parse adsets response: %w — body: %s", err, body)
		}
		for _, as := range r.Data {
			if as.EffectiveStatus == "ACTIVE" {
				out = append(out, as)
			}
		}
		endpoint = r.Paging.Next
	}
	return out, nil
}

// fetchActiveAds returns every ad in adsetID whose effective_status is ACTIVE.
func fetchActiveAds(ctx context.Context, client *http.Client, token, adsetID string) ([]adInfo, error) {
	var out []adInfo
	endpoint := fmt.Sprintf("%s/%s/ads?fields=id,name,effective_status,created_time&limit=500", graphBase, adsetID)
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
			Data   []adInfo `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("parse ads response: %w — body: %s", err, body)
		}
		for _, ad := range r.Data {
			if ad.EffectiveStatus == "ACTIVE" {
				out = append(out, ad)
			}
		}
		endpoint = r.Paging.Next
	}
	return out, nil
}

// fetchInsights fetches lifetime spend + actions for a single ad over [since, until].
func fetchInsights(ctx context.Context, client *http.Client, token, adID, since, until string) (insightsRow, error) {
	timeRange := fmt.Sprintf(`{"since":"%s","until":"%s"}`, since, until)
	endpoint := fmt.Sprintf("%s/%s/insights?fields=spend,actions,conversions&time_range=%s", graphBase, adID, url.QueryEscape(timeRange))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return insightsRow{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := doRequest(ctx, client, req)
	if err != nil {
		return insightsRow{}, err
	}
	var r struct {
		Data []insightsRow `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return insightsRow{}, fmt.Errorf("parse insights response: %w — body: %s", err, body)
	}
	if len(r.Data) == 0 {
		return insightsRow{}, nil
	}
	return r.Data[0], nil
}

// pauseAd sets an ad's status to PAUSED.
func pauseAd(ctx context.Context, client *http.Client, token, adID string) error {
	fields := url.Values{}
	fields.Set("status", "PAUSED")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/%s", graphBase, adID), strings.NewReader(fields.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)

	_, err = doRequest(ctx, client, req)
	return err
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

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
