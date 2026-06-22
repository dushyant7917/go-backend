package newsutils

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

func ComputeContentHash(title, source string) string {
	input := strings.ToLower(strings.TrimSpace(title)) + "|" + strings.ToLower(strings.TrimSpace(source))
	digest := sha256.Sum256([]byte(input))
	return hex.EncodeToString(digest[:])
}

func LanguageCodeToName(code string) string {
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

func IsRetryableGeminiError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "429") ||
		strings.Contains(s, "RESOURCE_EXHAUSTED") ||
		strings.Contains(s, "503") ||
		strings.Contains(s, "UNAVAILABLE")
}

func RetryGemini(ctx context.Context, fn func() error) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := range maxAttempts {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return lastErr
		}
		if !IsRetryableGeminiError(lastErr) {
			return lastErr
		}
		if attempt == maxAttempts-1 {
			break
		}
		base := time.Duration(1<<attempt) * time.Second
		jitter := time.Duration(rand.Int63n(int64(base)))
		backoff := base + jitter
		log.Printf("Gemini transient error, retrying in %v (attempt %d/%d): %v", backoff, attempt+1, maxAttempts, lastErr)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("gemini: all %d attempts failed: %w", maxAttempts, lastErr)
}

func NormalizeL2(vec []float32) {
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

// DedupStore tracks seen article links and content hashes to skip duplicates.
type DedupStore struct {
	links         sync.Map
	contentHashes sync.Map
}

func (d *DedupStore) Contains(link, contentHash string) bool {
	if _, ok := d.links.Load(link); ok {
		return true
	}
	if _, ok := d.contentHashes.Load(contentHash); ok {
		return true
	}
	return false
}

func (d *DedupStore) Add(link, contentHash string) {
	d.links.Store(link, struct{}{})
	d.contentHashes.Store(contentHash, struct{}{})
}

// LoadEntry adds a link and an optional content hash (may be nil when loading from DB).
func (d *DedupStore) LoadEntry(link string, hash *string) {
	d.links.Store(link, struct{}{})
	if hash != nil {
		d.contentHashes.Store(*hash, struct{}{})
	}
}

// CallGeminiAPI calls the given Gemini model with prompt and returns the raw text response.
func CallGeminiAPI(ctx context.Context, client *genai.Client, model, prompt string) (string, error) {
	contents := []*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{{Text: prompt}}},
	}
	config := &genai.GenerateContentConfig{ResponseMIMEType: "application/json"}

	var response *genai.GenerateContentResponse
	err := RetryGemini(ctx, func() error {
		var err error
		response, err = client.Models.GenerateContent(ctx, model, contents, config)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("Gemini API error: %w", err)
	}
	if len(response.Candidates) == 0 || response.Candidates[0].Content == nil || len(response.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in Gemini response")
	}
	return response.Candidates[0].Content.Parts[0].Text, nil
}

// CallGeminiAPIIntoMap calls Gemini and unmarshals the JSON response into a string map.
func CallGeminiAPIIntoMap(ctx context.Context, client *genai.Client, model, prompt string) (map[string]string, error) {
	raw, err := CallGeminiAPI(ctx, client, model, prompt)
	if err != nil {
		return nil, err
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini response: %w", err)
	}
	return result, nil
}
