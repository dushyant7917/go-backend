// Package fonts provides font loading utilities for multilingual text rendering.
// It includes embedded fonts with support for English and Hindi/Devanagari scripts.
package fonts

import (
	_ "embed"
	"fmt"
	"log"

	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font/gofont/goregular"
)

// Embed Poppins font that supports both English and Hindi/Devanagari
//
//go:embed Poppins-Regular.ttf
var poppinsRegular []byte

// LoadMultilingualFont loads a font that supports both English and Hindi characters
// Falls back to goregular if embedded font is not available
func LoadMultilingualFont() (*truetype.Font, error) {
	// Try to load embedded Poppins font (supports Hindi/Devanagari)
	if len(poppinsRegular) > 0 {
		font, err := truetype.Parse(poppinsRegular)
		if err == nil {
			log.Printf("Successfully loaded Poppins font with Devanagari support")
			return font, nil
		}
		log.Printf("Warning: failed to parse Poppins font, falling back to goregular: %v", err)
	}

	// Fallback to goregular (English only)
	font, err := truetype.Parse(goregular.TTF)
	if err != nil {
		return nil, fmt.Errorf("failed to parse fallback font: %w", err)
	}

	return font, nil
}
