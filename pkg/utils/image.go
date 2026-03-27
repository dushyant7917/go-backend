package utils

import (
	"bytes"
	"fmt"
	"image/gif"
	"image/png"
	"strings"
)

// IsGifURL checks if the URL points to a GIF file
func IsGifURL(url string) bool {
	lowerURL := strings.ToLower(url)
	// Check extension
	if strings.HasSuffix(lowerURL, ".gif") {
		return true
	}
	// Check for .gif? (with query params)
	if idx := strings.Index(lowerURL, ".gif?"); idx != -1 {
		return true
	}
	return false
}

// ConvertGifToPng converts a GIF to PNG using the first frame
func ConvertGifToPng(gifData []byte) ([]byte, error) {
	// Decode the GIF
	gifImg, err := gif.DecodeAll(bytes.NewReader(gifData))
	if err != nil {
		return nil, fmt.Errorf("failed to decode GIF: %w", err)
	}

	// Get the first frame
	if len(gifImg.Image) == 0 {
		return nil, fmt.Errorf("GIF has no frames")
	}

	firstFrame := gifImg.Image[0]

	// Encode as PNG
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, firstFrame); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}

	return pngBuf.Bytes(), nil
}
