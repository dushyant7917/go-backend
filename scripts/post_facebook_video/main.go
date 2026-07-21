package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
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
)

func main() {
	goEnv := os.Getenv("GO_ENV")
	if goEnv == "" {
		goEnv = "local"
	}
	_ = godotenv.Load(".env." + goEnv)
	_ = godotenv.Load()

	videoPath := flag.String("video-path", "", "path to the local video file to post (required)")
	pageID := flag.String("page-id", os.Getenv("META_PAGE_ID"), "Facebook Page ID (required)")
	description := flag.String("description", "", "reel caption/description text")
	scheduledPublishTime := flag.String("scheduled-publish-time", "", "optional Unix timestamp to schedule the post instead of publishing immediately")
	flag.Parse()

	if *videoPath == "" {
		log.Fatalf("flag -video-path is required")
	}
	if *pageID == "" {
		log.Fatalf("flag -page-id is required (or set META_PAGE_ID)")
	}

	token := mustEnv("FB_ACCESS_TOKEN")

	httpClient := &http.Client{Timeout: 10 * time.Minute}
	ctx := context.Background()

	log.Printf("starting reel upload session for page %s", *pageID)
	videoID, uploadURL, err := startReelUpload(ctx, httpClient, token, *pageID)
	if err != nil {
		log.Fatalf("start upload: %v", err)
	}
	log.Printf("video_id=%s, uploading file %s", videoID, *videoPath)

	if err := uploadReelVideo(ctx, httpClient, token, uploadURL, *videoPath); err != nil {
		log.Fatalf("upload video: %v", err)
	}
	log.Printf("upload complete, publishing reel")

	if err := finishReelUpload(ctx, httpClient, token, *pageID, videoID, *description, *scheduledPublishTime); err != nil {
		log.Fatalf("finish upload: %v", err)
	}

	if err := waitForVideo(ctx, httpClient, token, videoID); err != nil {
		log.Fatalf("video %s never became ready: %v", videoID, err)
	}

	fmt.Printf("\n=== Done ===\n")
	fmt.Printf("video_id: %s\n", videoID)
	fmt.Printf("url:      https://www.facebook.com/%s\n", videoID)
}

// startReelUpload initializes a resumable upload session for a Page Reel and returns
// the video_id along with the upload_url to send the video bytes to.
func startReelUpload(ctx context.Context, client *http.Client, token, pageID string) (videoID, uploadURL string, err error) {
	form := url.Values{}
	form.Set("upload_phase", "start")
	form.Set("access_token", token)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/%s/video_reels", graphBase, pageID), strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	body, err := doRequest(client, req)
	if err != nil {
		return "", "", err
	}
	var r struct {
		VideoID   string `json:"video_id"`
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", "", fmt.Errorf("parse start response: %w — body: %s", err, body)
	}
	if r.VideoID == "" || r.UploadURL == "" {
		return "", "", fmt.Errorf("missing video_id/upload_url in response: %s", body)
	}
	return r.VideoID, r.UploadURL, nil
}

// uploadReelVideo uploads the local video file's bytes to the resumable upload URL
// returned by startReelUpload.
func uploadReelVideo(ctx context.Context, client *http.Client, token, uploadURL, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Authorization", "OAuth "+token)
	req.Header.Set("offset", "0")
	req.Header.Set("file_size", strconv.FormatInt(info.Size(), 10))

	body, err := doRequest(client, req)
	if err != nil {
		return err
	}
	var r struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("parse upload response: %w — body: %s", err, body)
	}
	if !r.Success {
		return fmt.Errorf("upload not successful: %s", body)
	}
	return nil
}

// finishReelUpload publishes (or schedules) the uploaded video as a Page Reel.
func finishReelUpload(ctx context.Context, client *http.Client, token, pageID, videoID, description, scheduledPublishTime string) error {
	form := url.Values{}
	form.Set("access_token", token)
	form.Set("upload_phase", "finish")
	form.Set("video_id", videoID)
	if description != "" {
		form.Set("description", description)
	}
	if scheduledPublishTime != "" {
		form.Set("video_state", "SCHEDULED")
		form.Set("scheduled_publish_time", scheduledPublishTime)
	} else {
		form.Set("video_state", "PUBLISHED")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/%s/video_reels", graphBase, pageID), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	body, err := doRequest(client, req)
	if err != nil {
		return err
	}
	var r struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("parse finish response: %w — body: %s", err, body)
	}
	if !r.Success {
		return fmt.Errorf("finish not successful: %s", body)
	}
	return nil
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
