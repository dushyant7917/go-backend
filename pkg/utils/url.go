package utils

import "strings"

// GetFileExtensionFromURL extracts the file extension from a URL
// Returns the extension with dot prefix (e.g., ".jpg", ".png")
// Falls back to ".jpg" if no valid extension is found
func GetFileExtensionFromURL(url string) string {
	// Parse URL path
	idx := strings.LastIndex(url, "/")
	if idx == -1 {
		return ".jpg" // default extension
	}

	filename := url[idx+1:]

	// Find extension
	extIdx := strings.LastIndex(filename, ".")
	if extIdx == -1 {
		return ".jpg" // default extension
	}

	// Get extension and clean query parameters
	ext := filename[extIdx:]
	if queryIdx := strings.Index(ext, "?"); queryIdx != -1 {
		ext = ext[:queryIdx]
	}

	// Validate extension (common image formats)
	validExts := map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".gif":  true,
		".webp": true,
		".bmp":  true,
	}

	lowerExt := strings.ToLower(ext)
	if validExts[lowerExt] {
		return lowerExt
	}

	return ".jpg" // default extension
}
