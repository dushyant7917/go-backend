package main

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

var dateFormats = []string{
	time.RFC1123,
	time.RFC1123Z,
	time.RFC822,
	time.RFC822Z,
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05 MST",
	"2006-01-02 15:04:05",
	"02 Jan 2006 15:04:05 MST",
	"02 Jan 2006 15:04:05 -0700",
	"02 Jan 2006 15:04:05",
	"Mon, 02 Jan 2006 15:04:05",
}

var imgSrcRegex = regexp.MustCompile(`(?i)<img[^>]*\ssrc=["']([^"']+)["']`)

type browserUserAgentTransport struct{ http.RoundTripper }

func (t *browserUserAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; RSS reader)")
	return t.RoundTripper.RoundTrip(req)
}

func extractImageFromContent(content string) string {
	if matches := imgSrcRegex.FindStringSubmatch(content); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func tryParseDate(dateStr string) *time.Time {
	dateStr = strings.TrimSpace(dateStr)
	for _, format := range dateFormats {
		if t, err := time.Parse(format, dateStr); err == nil {
			utcTime := t.UTC()
			return &utcTime
		}
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"02 Jan 2006 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, dateStr, time.UTC); err == nil {
			return &t
		}
	}
	return nil
}

func parsePublishedAt(item *gofeed.Item) *time.Time {
	if item.PublishedParsed != nil {
		return item.PublishedParsed
	}
	if item.Published != "" {
		if t := tryParseDate(item.Published); t != nil {
			return t
		}
	}
	if item.Updated != "" {
		if t := tryParseDate(item.Updated); t != nil {
			return t
		}
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: debug <rss-url>")
		os.Exit(1)
	}
	feedURL := os.Args[1]

	fp := gofeed.NewParser()
	fp.Client = &http.Client{
		Timeout:   30 * time.Second,
		Transport: &browserUserAgentTransport{http.DefaultTransport},
	}

	feed, err := fp.ParseURL(feedURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse feed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Feed title: %s\n", feed.Title)
	fmt.Printf("Items: %d\n\n", len(feed.Items))

	for i, item := range feed.Items[:min(5, len(feed.Items))] {
		mediaLink := ""
		if media, ok := item.Extensions["media"]; ok {
			if content, ok := media["content"]; ok && len(content) > 0 {
				mediaLink = content[0].Attrs["url"]
			}
		}
		if mediaLink == "" && len(item.Enclosures) > 0 {
			mediaLink = item.Enclosures[0].URL
		}
		if mediaLink == "" && item.Content != "" {
			mediaLink = extractImageFromContent(item.Content)
		}

		publishedAt := parsePublishedAt(item)
		publishedStr := "<nil>"
		if publishedAt != nil {
			publishedStr = publishedAt.Format(time.RFC3339)
		}

		fmt.Printf("--- Item %d ---\n", i+1)
		fmt.Printf("  Title:       %s\n", strings.TrimSpace(item.Title))
		fmt.Printf("  Description: %s\n", strings.TrimSpace(item.Description))
		fmt.Printf("  Link:        %s\n", item.Link)
		fmt.Printf("  Media:       %s\n", mediaLink)
		fmt.Printf("  PublishedAt: %s\n", publishedStr)
		fmt.Println()
	}
}
