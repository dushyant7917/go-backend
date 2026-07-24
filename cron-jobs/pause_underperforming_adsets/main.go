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

	startTimeLayout = "2006-01-02T15:04:05-0700"
	dateLayout      = "2006-01-02"
)

// langCampaignIDs mirrors the same env vars scripts/create_meta_ad uses to create these
// campaigns in the first place, so this script can default to checking all of them.
func langCampaignIDs() []struct{ Lang, ID string } {
	order := []string{"Hindi", "Tamil", "Marathi", "Gujarati", "Bengali", "Telugu", "Kannada"}
	envKey := map[string]string{
		"Hindi":    "META_CAMPAIGN_ID_HINDI",
		"Tamil":    "META_CAMPAIGN_ID_TAMIL",
		"Marathi":  "META_CAMPAIGN_ID_MARATHI",
		"Gujarati": "META_CAMPAIGN_ID_GUJARATI",
		"Bengali":  "META_CAMPAIGN_ID_BENGALI",
		"Telugu":   "META_CAMPAIGN_ID_TELUGU",
		"Kannada":  "META_CAMPAIGN_ID_KANNADA",
	}
	var out []struct{ Lang, ID string }
	for _, lang := range order {
		if id := os.Getenv(envKey[lang]); id != "" {
			out = append(out, struct{ Lang, ID string }{lang, id})
		}
	}
	return out
}

type promotedObject struct {
	CustomEventType string `json:"custom_event_type"`
}

type adsetInfo struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	EffectiveStatus string         `json:"effective_status"`
	StartTime       string         `json:"start_time"`
	PromotedObject  promotedObject `json:"promoted_object"`
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

	campaignID := flag.String("campaign-id", "", "check only this campaign ID (default: loop over all configured META_CAMPAIGN_ID_<LANG> env vars)")
	adAccountID := flag.String("ad-account-id", os.Getenv("META_AD_ACCOUNT_ID"), "ad account ID without act_ prefix (required)")
	resultActionType := flag.String("result-action-type", "", `Meta insights action_type to count as 'Results' (e.g. "start_trial_total" for a START_TRIAL-optimized adset — confirmed via -metric-field=conversions discovery mode). Omit to run in discovery mode, which prints the candidate conversions sums per adset instead of making pause decisions.`)
	metricField := flag.String("metric-field", "conversions", `insights field to sum -result-action-type from: "conversions" (Meta's curated per-objective conversion count — confirmed to match Ads Manager's "Results" column) or "actions" (raw event count, does not match "Results")`)
	apply := flag.Bool("apply", false, "actually pause flagged ad sets (requires -result-action-type). Without this flag the script only logs what it would do.")
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

	type campaign struct{ Label, ID string }
	var campaigns []campaign
	if *campaignID != "" {
		campaigns = append(campaigns, campaign{Label: *campaignID, ID: *campaignID})
	} else {
		for _, lc := range langCampaignIDs() {
			campaigns = append(campaigns, campaign{Label: lc.Lang, ID: lc.ID})
		}
		if len(campaigns) == 0 {
			log.Fatalf("no -campaign-id given and no META_CAMPAIGN_ID_<LANG> env vars are set")
		}
	}

	type outcome struct {
		campaignLabel string
		adsetID       string
		name          string
		spend         float64
		results       int
		cpr           float64
		pause         bool
		reason        string
		paused        bool
		pauseErr      error
	}
	var outcomes []outcome

	discovery := *resultActionType == ""

	for _, c := range campaigns {
		log.Printf("=== campaign %s (%s) ===", c.Label, c.ID)
		adsets, err := fetchActiveAdsets(ctx, httpClient, token, c.ID)
		if err != nil {
			log.Printf("[%s] ERROR fetching adsets: %v", c.Label, err)
			continue
		}
		log.Printf("[%s] %d active adset(s)", c.Label, len(adsets))

		for _, as := range adsets {
			since, sinceErr := parseStartDate(as.StartTime)
			if sinceErr != nil {
				log.Printf("[%s] adset %s (%s): ERROR parsing start_time %q: %v", c.Label, as.ID, as.Name, as.StartTime, sinceErr)
				continue
			}
			until := time.Now().Format(dateLayout)

			row, insErr := fetchInsights(ctx, httpClient, token, as.ID, since, until)
			if insErr != nil {
				log.Printf("[%s] adset %s (%s): ERROR fetching insights: %v", c.Label, as.ID, as.Name, insErr)
				continue
			}
			spend, _ := strconv.ParseFloat(row.Spend, 64)

			event := strings.ToLower(as.PromotedObject.CustomEventType)

			if discovery {
				totalType := event + "_total"
				results := sumAction(row.Conversions, totalType)
				log.Printf("[%s] %s: spend=%.2f results=%d", c.Label, as.Name, spend, results)
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
			log.Printf("[%s] %s: spend=%.2f results=%d", c.Label, as.Name, spend, results)

			outcomes = append(outcomes, outcome{
				campaignLabel: c.Label,
				adsetID:       as.ID,
				name:          as.Name,
				spend:         spend,
				results:       results,
				cpr:           cpr,
				pause:         pause,
				reason:        reason,
			})
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
			err := pauseAdset(ctx, httpClient, token, o.adsetID)
			outcomes[i].paused = err == nil
			outcomes[i].pauseErr = err
			if err != nil {
				log.Printf("[%s] adset %s: ERROR pausing: %v", o.campaignLabel, o.adsetID, err)
			} else {
				log.Printf("[%s] adset %s: paused", o.campaignLabel, o.adsetID)
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
			fmt.Printf("OK    [%s] %-30s  results=%d cpr=%s spend=%.2f\n", o.campaignLabel, o.name, o.results, cprStr, o.spend)
		case o.pause && !*apply:
			fmt.Printf("DRY   [%s] %-30s  would pause: %s\n", o.campaignLabel, o.name, o.reason)
		case o.pauseErr != nil:
			fmt.Printf("FAIL  [%s] %-30s  pause error: %v\n", o.campaignLabel, o.name, o.pauseErr)
		default:
			fmt.Printf("PAUSE [%s] %-30s  %s\n", o.campaignLabel, o.name, o.reason)
		}
	}
}

// evaluate applies the CPR kill-switch thresholds. Bands are checked from the highest
// results lower-bound down, so a matching band's threshold is the only one considered.
func evaluate(results int, spend, cpr float64) (pause bool, reason string) {
	switch {
	case results >= 20:
		if cpr > 110 {
			return true, fmt.Sprintf("results=%d (>=20) cpr=%.2f > 110", results, cpr)
		}
	case results >= 10:
		if cpr > 120 {
			return true, fmt.Sprintf("results=%d (10-19) cpr=%.2f > 120", results, cpr)
		}
	case results >= 5:
		if cpr > 150 {
			return true, fmt.Sprintf("results=%d (5-9) cpr=%.2f > 150", results, cpr)
		}
	case results >= 2:
		if cpr > 200 {
			return true, fmt.Sprintf("results=%d (2-4) cpr=%.2f > 200", results, cpr)
		}
	case results == 1:
		if cpr > 250 {
			return true, fmt.Sprintf("results=1 cpr=%.2f > 250", cpr)
		}
	case results == 0:
		if spend > 225 {
			return true, fmt.Sprintf("results=0 spend=%.2f > 225", spend)
		}
	}
	return false, ""
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

// parseStartDate parses a Graph API start_time timestamp and returns just the date portion,
// suitable for use as the "since" bound of an insights time_range.
func parseStartDate(s string) (string, error) {
	t, err := time.Parse(startTimeLayout, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return "", err
		}
	}
	return t.Format(dateLayout), nil
}

// fetchActiveAdsets returns every adset in campaignID whose effective_status is ACTIVE.
func fetchActiveAdsets(ctx context.Context, client *http.Client, token, campaignID string) ([]adsetInfo, error) {
	var out []adsetInfo
	endpoint := fmt.Sprintf("%s/%s/adsets?fields=id,name,effective_status,start_time,promoted_object&limit=500", graphBase, campaignID)
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

// fetchInsights fetches lifetime spend + actions for a single adset over [since, until].
func fetchInsights(ctx context.Context, client *http.Client, token, adsetID, since, until string) (insightsRow, error) {
	timeRange := fmt.Sprintf(`{"since":"%s","until":"%s"}`, since, until)
	endpoint := fmt.Sprintf("%s/%s/insights?fields=spend,actions,conversions&time_range=%s", graphBase, adsetID, url.QueryEscape(timeRange))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return insightsRow{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := doRequest(client, req)
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

// pauseAdset sets an adset's status to PAUSED.
func pauseAdset(ctx context.Context, client *http.Client, token, adsetID string) error {
	fields := url.Values{}
	fields.Set("status", "PAUSED")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/%s", graphBase, adsetID), strings.NewReader(fields.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)

	_, err = doRequest(client, req)
	return err
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
