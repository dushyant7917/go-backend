package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"golang.org/x/time/rate"
	"google.golang.org/genai"
)

// ==================== Config ====================

const (
	veoModel = "veo-3.1-lite-generate-preview"

	pollInterval = 10 * time.Second
	maxRetries   = 5
)

var aspectRatioToSize = map[string]openai.ImageGenerateParamsSize{
	"1:1":  openai.ImageGenerateParamsSize1024x1024,
	"4:5":  openai.ImageGenerateParamsSize("1024x1280"),
	"9:16": openai.ImageGenerateParamsSize1024x1536,
}

type promptVariant struct {
	thumbnail string // format args: aspect-ratio, text
	video     string // format args: language, text
}

var variants = map[string]promptVariant{
	"reporter": {
		thumbnail: `Create a thumbnail of %s aspect ratio which has a beautiful indian female news reporter holding a mike in one hand and thumbnail text is "%s"`,
		video:     `The reporter in image speaks in %s "%s"`,
	},
	"male_kid_reporter": {
		thumbnail: `Create a thumbnail of %s aspect ratio which has a 10 year old indian male kid dressed as news reporter holding a mike in one hand and thumbnail text is "%s"`,
		video:     `The kid in image speaks in %s "%s"`,
	},
	"poster": {
		thumbnail: `Create an aesthetic poster of %s aspect ratio with the text "%s"`,
		video:     `Add a voice over in %s "%s"`,
	},
	"thumbnail": {
		thumbnail: `Create a catchy thumbnail of %s aspect ratio with the text "%s"`,
		video:     `Animate the image attached and add a voice over in %s for the text "%s"`,
	},
}

// ==================== Types ====================

// TextsMap: slug -> language -> text
type TextsMap map[string]map[string]string

type workItem struct {
	language  string
	text      string
	thumbPath string
	videoPath string
	index     int
}

type videoItem struct {
	workItem
	imageBytes []byte
}

// ==================== Helpers ====================

func ptr[T any](v T) *T { return &v }

func isRateLimit(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 429
	}
	s := err.Error()
	return strings.Contains(s, "429") || strings.Contains(s, "RESOURCE_EXHAUSTED")
}

// withRetry retries fn on rate-limit errors with exponential backoff + jitter.
func withRetry(ctx context.Context, fn func() error) error {
	var err error
	for attempt := range maxRetries {
		err = fn()
		if err == nil || !isRateLimit(err) {
			return err
		}
		if attempt == maxRetries-1 {
			break
		}
		base := time.Duration(1<<uint(attempt)) * time.Second
		jitter := time.Duration(rand.Int64N(int64(base / 2)))
		wait := base + jitter
		log.Printf("    rate limited, retrying in %s (attempt %d/%d)", wait, attempt+1, maxRetries)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return err
}

// ==================== Generators ====================

func generateThumbnail(ctx context.Context, client openai.Client, thumbnailTmpl, aspectRatio, text string, size openai.ImageGenerateParamsSize, limiter *rate.Limiter) ([]byte, error) {
	prompt := fmt.Sprintf(thumbnailTmpl, aspectRatio, text)
	var resp *openai.ImagesResponse
	err := withRetry(ctx, func() error {
		if e := limiter.Wait(ctx); e != nil {
			return e
		}
		var e error
		resp, e = client.Images.Generate(ctx, openai.ImageGenerateParams{
			Prompt:  prompt,
			Model:   "gpt-image-2",
			Size:    size,
			Quality: openai.ImageGenerateParamsQualityHigh,
		})
		return e
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("OpenAI returned no image data")
	}
	return base64.StdEncoding.DecodeString(resp.Data[0].B64JSON)
}

func generateVideo(ctx context.Context, client *genai.Client, imageBytes []byte, videoTmpl, language, text, aspectRatio string, duration int, limiter *rate.Limiter) ([]byte, error) {
	prompt := fmt.Sprintf(videoTmpl, language, text)

	var op *genai.GenerateVideosOperation
	err := withRetry(ctx, func() error {
		if e := limiter.Wait(ctx); e != nil {
			return e
		}
		var e error
		op, e = client.Models.GenerateVideos(ctx, veoModel, prompt, &genai.Image{
			ImageBytes: imageBytes,
			MIMEType:   "image/png",
		}, &genai.GenerateVideosConfig{
			AspectRatio:     aspectRatio,
			NumberOfVideos:  1,
			DurationSeconds: ptr(int32(duration)),
		})
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("GenerateVideos: %w", err)
	}

	log.Print("    polling Veo operation")
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for !op.Done {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		if err = withRetry(ctx, func() error {
			next, e := client.Operations.GetVideosOperation(ctx, op, nil)
			if e == nil {
				op = next
			}
			return e
		}); err != nil {
			return nil, fmt.Errorf("GetVideosOperation: %w", err)
		}
		log.Print("    ...")
	}

	if op.Response == nil || len(op.Response.GeneratedVideos) == 0 {
		return nil, fmt.Errorf("no videos in response")
	}

	var data []byte
	if err = withRetry(ctx, func() error {
		var e error
		data, e = client.Files.Download(ctx, genai.NewDownloadURIFromGeneratedVideo(op.Response.GeneratedVideos[0]), nil)
		return e
	}); err != nil {
		return nil, fmt.Errorf("download video: %w", err)
	}
	return data, nil
}

// ==================== Workers ====================

func processImage(
	ctx context.Context,
	item workItem,
	total int,
	videoCh chan<- videoItem,
	client openai.Client,
	limiter *rate.Limiter,
	thumbnailTmpl string,
	aspectRatio string,
	size openai.ImageGenerateParamsSize,
	skipVideo bool,
) {
	log.Printf("[%d/%d] [%s] %s", item.index, total, item.language, item.text)

	var imageBytes []byte

	if _, statErr := os.Stat(item.thumbPath); statErr == nil {
		log.Printf("  ✓ thumbnail exists: %s", item.thumbPath)
		if skipVideo {
			return
		}
		var err error
		imageBytes, err = os.ReadFile(item.thumbPath)
		if err != nil {
			log.Printf("  ✗ read cached thumbnail: %v — skipping", err)
			return
		}
	} else {
		log.Printf("  → generating thumbnail (gpt-image-2) [%s]", item.language)
		var err error
		imageBytes, err = generateThumbnail(ctx, client, thumbnailTmpl, aspectRatio, item.text, size, limiter)
		if err != nil {
			log.Printf("  ✗ thumbnail: %v — skipping", err)
			return
		}
		if err := os.WriteFile(item.thumbPath, imageBytes, 0o644); err != nil {
			log.Printf("  ✗ write thumbnail: %v — skipping", err)
			return
		}
		log.Printf("  ✓ thumbnail saved: %s", item.thumbPath)
	}

	if skipVideo {
		return
	}

	select {
	case <-ctx.Done():
	case videoCh <- videoItem{workItem: item, imageBytes: imageBytes}:
	}
}

func processVideo(
	ctx context.Context,
	item videoItem,
	client *genai.Client,
	limiter *rate.Limiter,
	videoTmpl string,
	aspectRatio string,
	duration int,
) {
	if _, statErr := os.Stat(item.videoPath); statErr == nil {
		log.Printf("  ✓ video exists: %s", item.videoPath)
		return
	}

	log.Printf("  → generating video (Veo 3.1 Lite Preview) [%s] %s", item.language, item.text)
	videoBytes, err := generateVideo(ctx, client, item.imageBytes, videoTmpl, item.language, item.text, aspectRatio, duration, limiter)
	if err != nil {
		log.Printf("  ✗ video: %v [%s]", err, item.language)
		return
	}
	if err := os.WriteFile(item.videoPath, videoBytes, 0o644); err != nil {
		log.Printf("  ✗ write video: %v [%s]", err, item.language)
		return
	}
	log.Printf("  ✓ video saved: %s", item.videoPath)
}

// ==================== Main ====================

func main() {
	textsFile := flag.String("texts", "texts.json", "path to texts JSON file")
	outputDir := flag.String("output", "output", "root output directory for thumbnails")
	videoBaseDir := flag.String("video-dir", "", "root directory for output videos; defaults to VIDEO_OUTPUT_PATH env var")
	variantName := flag.String("variant", "", "prompt variant to use: reporter, poster, thumbnail (required)")
	aspectRatioFlag := flag.String("aspect-ratio", "", "output aspect ratio: 1:1, 4:5, 9:16 (required)")
	videoDuration := flag.Int("duration", 0, "video duration in seconds: 4, 6, or 8 (required unless -skip-video)")
	skipVideo := flag.Bool("skip-video", false, "generate thumbnails only, skip video")
	imageWorkers := flag.Int("image-workers", 20, "concurrent thumbnail workers")
	videoWorkers := flag.Int("video-workers", 2, "concurrent video workers")
	imageRPM := flag.Int("image-rpm", 45, "max OpenAI image requests per minute")
	videoRPM := flag.Int("video-rpm", 4, "max Veo video requests per minute")
	flag.Parse()

	variant, ok := variants[*variantName]
	if !ok {
		names := make([]string, 0, len(variants))
		for k := range variants {
			names = append(names, k)
		}
		sort.Strings(names)
		log.Fatalf("-variant is required; choose one of: %s", strings.Join(names, ", "))
	}

	imgSize, ok := aspectRatioToSize[*aspectRatioFlag]
	if !ok {
		log.Fatalf("-aspect-ratio is required; choose one of: 1:1, 4:5, 9:16")
	}

	if !*skipVideo {
		if *videoDuration != 4 && *videoDuration != 6 && *videoDuration != 8 {
			log.Fatalf("-duration is required; choose one of: 4, 6, 8")
		}
	}

	if *imageWorkers <= 0 {
		log.Fatal("-image-workers must be a positive integer")
	}
	if *videoWorkers <= 0 {
		log.Fatal("-video-workers must be a positive integer")
	}
	if *imageRPM <= 0 {
		log.Fatal("-image-rpm must be a positive integer")
	}
	if *videoRPM <= 0 {
		log.Fatal("-video-rpm must be a positive integer")
	}

	goEnv := os.Getenv("GO_ENV")
	if goEnv == "" {
		goEnv = "local"
	}
	_ = godotenv.Load(".env." + goEnv)
	_ = godotenv.Load()

	openaiKey := os.Getenv("OPEN_AI_API_KEY")
	geminiKey := os.Getenv("GEMINI_API_KEY")
	if openaiKey == "" {
		log.Fatal("OPEN_AI_API_KEY is not set")
	}
	if geminiKey == "" && !*skipVideo {
		log.Fatal("GEMINI_API_KEY is not set (or pass -skip-video)")
	}

	if *videoBaseDir == "" {
		*videoBaseDir = os.Getenv("VIDEO_OUTPUT_PATH")
	}
	if *videoBaseDir == "" && !*skipVideo {
		log.Fatal("VIDEO_OUTPUT_PATH env var is not set (or pass -video-dir)")
	}

	raw, err := os.ReadFile(*textsFile)
	if err != nil {
		log.Fatalf("read texts file: %v", err)
	}
	var texts TextsMap
	if err := json.Unmarshal(raw, &texts); err != nil {
		log.Fatalf("parse texts file: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	openaiClient := openai.NewClient(option.WithAPIKey(openaiKey))

	var geminiClient *genai.Client
	if !*skipVideo {
		geminiClient, err = genai.NewClient(ctx, &genai.ClientConfig{APIKey: geminiKey})
		if err != nil {
			log.Fatalf("create Gemini client: %v", err)
		}
	}

	imageLimiter := rate.NewLimiter(rate.Every(time.Minute/time.Duration(*imageRPM)), 1)
	videoLimiter := rate.NewLimiter(rate.Every(time.Minute/time.Duration(*videoRPM)), 1)

	// Flatten all texts into work items and create output dirs up front.
	var items []workItem
	for slug, langMap := range texts {
		for language, text := range langMap {
			imgDir := filepath.Join(*outputDir, language, "images")
			vidDir := filepath.Join(*videoBaseDir, language)
			if err := os.MkdirAll(imgDir, 0o755); err != nil {
				log.Fatalf("mkdir %s: %v", imgDir, err)
			}
			if !*skipVideo {
				if err := os.MkdirAll(vidDir, 0o755); err != nil {
					log.Fatalf("mkdir %s: %v", vidDir, err)
				}
			}
			items = append(items, workItem{
				language:  language,
				text:      text,
				thumbPath: filepath.Join(imgDir, slug+".png"),
				videoPath: filepath.Join(vidDir, slug+".mp4"),
			})
		}
	}

	total := len(items)
	for i := range items {
		items[i].index = i + 1
	}

	inputCh := make(chan workItem, total)
	for _, item := range items {
		inputCh <- item
	}
	close(inputCh)

	// Stage 1: image workers.
	var imageWg sync.WaitGroup
	var videoCh chan videoItem
	if !*skipVideo {
		videoCh = make(chan videoItem, len(items))
	}

	for range *imageWorkers {
		imageWg.Add(1)
		go func() {
			defer imageWg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-inputCh:
					if !ok {
						return
					}
					processImage(ctx, item, total, videoCh, openaiClient, imageLimiter, variant.thumbnail, *aspectRatioFlag, imgSize, *skipVideo)
				}
			}
		}()
	}

	// Stage 2: video workers (only when not skipping video).
	var videoWg sync.WaitGroup
	if !*skipVideo {
		go func() {
			imageWg.Wait()
			close(videoCh)
		}()

		for range *videoWorkers {
			videoWg.Add(1)
			go func() {
				defer videoWg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case item, ok := <-videoCh:
						if !ok {
							return
						}
						processVideo(ctx, item, geminiClient, videoLimiter, variant.video, *aspectRatioFlag, *videoDuration)
					}
				}
			}()
		}
	}

	imageWg.Wait()
	videoWg.Wait()

	absOut, _ := filepath.Abs(*outputDir)
	log.Printf("done — thumbnails in %s | videos in %s", absOut, *videoBaseDir)
}
