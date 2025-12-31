package utils

import "strings"

// GetContentTypeFromExtension returns the MIME type based on file extension
// Supports common image formats and can be extended for other file types
func GetContentTypeFromExtension(filename string) string {
	// Extract extension (convert to lowercase for case-insensitive matching)
	ext := strings.ToLower(strings.TrimPrefix(strings.ToLower(filename), "."))
	if idx := strings.LastIndex(ext, "."); idx != -1 {
		ext = ext[idx+1:]
	}

	// Map extensions to MIME types
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "svg":
		return "image/svg+xml"
	case "bmp":
		return "image/bmp"
	case "ico":
		return "image/x-icon"
	case "tiff", "tif":
		return "image/tiff"
	case "pdf":
		return "application/pdf"
	case "json":
		return "application/json"
	case "xml":
		return "application/xml"
	case "txt":
		return "text/plain"
	case "html", "htm":
		return "text/html"
	case "css":
		return "text/css"
	case "js":
		return "application/javascript"
	case "mp4":
		return "video/mp4"
	case "mp3":
		return "audio/mpeg"
	case "zip":
		return "application/zip"
	default:
		// Default to binary stream for unknown types
		return "application/octet-stream"
	}
}
