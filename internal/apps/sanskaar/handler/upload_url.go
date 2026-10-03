package handler

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	r2ConfigService "go-backend/internal/apps/r2/config/service"
	"go-backend/internal/common/constants"
	"go-backend/internal/common/uploadurl"
	"go-backend/pkg/utils"

	"github.com/gin-gonic/gin"
)

// statusCategoryPattern restricts Status.Category to alphabetic words separated by single
// hyphens (e.g. "festive", "good-morning"). Unlike DevotionalMusic, Status categories aren't a
// fixed enum — new categories can be added without a migration — so this format check is the
// only validation, and the value is stored/keyed in R2 exactly as the client sends it.
var statusCategoryPattern = regexp.MustCompile(`^[a-zA-Z]+(-[a-zA-Z]+)*$`)

// validateStatusCategory checks category against statusCategoryPattern. On validation failure
// it writes the 400 response itself and returns false; callers should return immediately.
func validateStatusCategory(c *gin.Context, category string) bool {
	if !statusCategoryPattern.MatchString(category) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "category must contain only letters and hyphens"})
		return false
	}
	return true
}

// sanskaarBucketEnvVar is the single R2 bucket shared by every Sanskaar resource
// (devotional music, tones, wallpapers). Resources are separated by key prefix, not by
// bucket, since none of them differ in access control, lifecycle, or ownership — see
// devotionalMusicUploadURL / audioThumbnailUploadURL / wallpaperUploadURL for the prefixes.
const sanskaarBucketEnvVar = "R2_SANSKAAR_BUCKET_NAME"

// validateAudioThumbnailFile validates filename against the declared purpose ("audio" or
// "thumbnail"), returning the extension, filename without extension, and resolved content
// type. On validation failure it writes the 400 response itself and returns ok=false;
// callers should return immediately in that case.
func validateAudioThumbnailFile(c *gin.Context, filename, purpose string) (ext, filenameWithoutExt, contentType string, ok bool) {
	if strings.TrimSpace(filename) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename cannot be empty"})
		return "", "", "", false
	}

	ext = filepath.Ext(filename)
	if ext == "" {
		if purpose == "audio" {
			ext = ".mp3"
		} else {
			ext = ".png"
		}
	}
	filenameWithoutExt = strings.TrimSuffix(filename, ext)
	contentType = utils.GetContentTypeFromExtension(ext)

	if purpose == "audio" && !strings.HasPrefix(contentType, "audio/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only audio files are supported for purpose 'audio'"})
		return "", "", "", false
	}
	if purpose == "thumbnail" && !strings.HasPrefix(contentType, "image/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only image files are supported for purpose 'thumbnail'"})
		return "", "", "", false
	}

	return ext, filenameWithoutExt, contentType, true
}

// audioThumbnailUploadURL handles POST /api/v1/sanskaar/tones/upload-url. Tone has "audio" and
// "thumbnail" file purposes but (unlike DevotionalMusic) no category to organize files by, so
// files are simply keyed under tones/audio/ or tones/thumbnails/.
func audioThumbnailUploadURL(c *gin.Context, r2ClientFactory *r2ConfigService.R2ClientFactory, resourcePrefix string) {
	var req struct {
		Filename string `json:"filename" binding:"required"`
		Purpose  string `json:"purpose" binding:"required,oneof=audio thumbnail"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ext, filenameWithoutExt, contentType, ok := validateAudioThumbnailFile(c, req.Filename, req.Purpose)
	if !ok {
		return
	}

	folder := "audio"
	if req.Purpose == "thumbnail" {
		folder = "thumbnails"
	}

	timestamp := time.Now().UTC().Unix()
	fileKey := fmt.Sprintf("%s/%s/%s_%d%s", resourcePrefix, folder, filenameWithoutExt, timestamp, ext)

	uploadurl.Respond(c, r2ClientFactory, uploadurl.Request{
		AppName:           constants.AppNameSanskaar,
		AppLabel:          "sanskaar",
		BucketEnvVar:      sanskaarBucketEnvVar,
		FileKey:           fileKey,
		ContentType:       contentType,
		ExpirationMinutes: 5,
	})
}

// devotionalMusicCategoryFolders maps the singular category values clients send — and the DB
// stores, per the devotional_music.category CHECK constraint — to the plural folder name used
// in the R2 key (e.g. "mantra" -> "mantras"). Client requests and the database stay singular;
// only the R2 path is pluralized.
var devotionalMusicCategoryFolders = map[string]string{
	"mantra":  "mantras",
	"bhajan":  "bhajans",
	"aarti":   "aartis",
	"chalisa": "chalisas",
}

// devotionalMusicUploadURL handles POST /api/v1/sanskaar/devotional-music/upload-url.
// DevotionalMusic organizes files by category (mantras, bhajans, aartis, chalisas) and, within
// each category, by purpose: devotional-music/<category>/audio/<filename> for the audio file and
// devotional-music/<category>/thumbnails/<filename> for its thumbnail.
func devotionalMusicUploadURL(c *gin.Context, r2ClientFactory *r2ConfigService.R2ClientFactory) {
	var req struct {
		Filename string `json:"filename" binding:"required"`
		Purpose  string `json:"purpose" binding:"required,oneof=audio thumbnail"`
		Category string `json:"category" binding:"required,oneof=mantra bhajan aarti chalisa"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ext, filenameWithoutExt, contentType, ok := validateAudioThumbnailFile(c, req.Filename, req.Purpose)
	if !ok {
		return
	}

	folder := "audio"
	if req.Purpose == "thumbnail" {
		folder = "thumbnails"
	}
	categoryFolder := devotionalMusicCategoryFolders[req.Category]

	timestamp := time.Now().UTC().Unix()
	fileKey := fmt.Sprintf("devotional-music/%s/%s/%s_%d%s", categoryFolder, folder, filenameWithoutExt, timestamp, ext)

	uploadurl.Respond(c, r2ClientFactory, uploadurl.Request{
		AppName:           constants.AppNameSanskaar,
		AppLabel:          "sanskaar",
		BucketEnvVar:      sanskaarBucketEnvVar,
		FileKey:           fileKey,
		ContentType:       contentType,
		ExpirationMinutes: 5,
	})
}

// wallpaperUploadURL handles POST /api/v1/sanskaar/wallpapers/upload-url. Wallpapers organize
// files by purpose, then (for purpose "media") by type: video wallpapers ("live") under
// wallpapers/live/, image wallpapers ("static") under wallpapers/static/, thumbnails (always
// images, only applicable to video wallpapers) under wallpapers/thumbnails/. purpose defaults to
// "media" when omitted, so existing clients that predate the thumbnail field keep working.
func wallpaperUploadURL(c *gin.Context, r2ClientFactory *r2ConfigService.R2ClientFactory) {
	var req struct {
		Filename string `json:"filename" binding:"required"`
		Type     string `json:"type" binding:"required,oneof=image video"`
		Purpose  string `json:"purpose,omitempty" binding:"omitempty,oneof=media thumbnail"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if strings.TrimSpace(req.Filename) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename cannot be empty"})
		return
	}

	var folder, defaultExt, wantPrefix string
	if req.Purpose == "thumbnail" {
		folder, defaultExt, wantPrefix = "thumbnails", ".png", "image/"
	} else {
		folder, defaultExt, wantPrefix = "static", ".jpg", "image/"
		if req.Type == "video" {
			folder, defaultExt, wantPrefix = "live", ".mp4", "video/"
		}
	}

	ext := filepath.Ext(req.Filename)
	if ext == "" {
		ext = defaultExt
	}
	filenameWithoutExt := strings.TrimSuffix(req.Filename, ext)
	contentType := utils.GetContentTypeFromExtension(ext)

	if !strings.HasPrefix(contentType, wantPrefix) {
		errLabel := req.Type
		if req.Purpose == "thumbnail" {
			errLabel = "thumbnail"
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("only %s files are supported for '%s'", strings.TrimSuffix(wantPrefix, "/"), errLabel)})
		return
	}

	timestamp := time.Now().UTC().Unix()
	fileKey := fmt.Sprintf("wallpapers/%s/%s_%d%s", folder, filenameWithoutExt, timestamp, ext)

	uploadurl.Respond(c, r2ClientFactory, uploadurl.Request{
		AppName:           constants.AppNameSanskaar,
		AppLabel:          "sanskaar",
		BucketEnvVar:      sanskaarBucketEnvVar,
		FileKey:           fileKey,
		ContentType:       contentType,
		ExpirationMinutes: 5,
	})
}

// statusUploadURL handles POST /api/v1/sanskaar/statuses/upload-url. Statuses organize files by
// category, then purpose: statuses/<category>/images/ for image statuses,
// statuses/<category>/videos/ for video statuses, statuses/<category>/thumbnails/ for thumbnails
// (always images, only applicable to video statuses). category is stored in the key exactly as
// sent (validated against statusCategoryPattern, no folder-name mapping like DevotionalMusic's
// fixed categories). purpose defaults to "media" when omitted, so existing clients that predate
// the thumbnail field keep working.
func statusUploadURL(c *gin.Context, r2ClientFactory *r2ConfigService.R2ClientFactory) {
	var req struct {
		Filename string `json:"filename" binding:"required"`
		Type     string `json:"type" binding:"required,oneof=image video"`
		Category string `json:"category" binding:"required"`
		Purpose  string `json:"purpose,omitempty" binding:"omitempty,oneof=media thumbnail"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if strings.TrimSpace(req.Filename) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename cannot be empty"})
		return
	}

	if !validateStatusCategory(c, req.Category) {
		return
	}

	var folder, defaultExt, wantPrefix string
	if req.Purpose == "thumbnail" {
		folder, defaultExt, wantPrefix = "thumbnails", ".png", "image/"
	} else {
		folder, defaultExt, wantPrefix = "images", ".jpg", "image/"
		if req.Type == "video" {
			folder, defaultExt, wantPrefix = "videos", ".mp4", "video/"
		}
	}

	ext := filepath.Ext(req.Filename)
	if ext == "" {
		ext = defaultExt
	}
	filenameWithoutExt := strings.TrimSuffix(req.Filename, ext)
	contentType := utils.GetContentTypeFromExtension(ext)

	if !strings.HasPrefix(contentType, wantPrefix) {
		errLabel := req.Type
		if req.Purpose == "thumbnail" {
			errLabel = "thumbnail"
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("only %s files are supported for '%s'", strings.TrimSuffix(wantPrefix, "/"), errLabel)})
		return
	}

	timestamp := time.Now().UTC().Unix()
	fileKey := fmt.Sprintf("statuses/%s/%s/%s_%d%s", req.Category, folder, filenameWithoutExt, timestamp, ext)

	uploadurl.Respond(c, r2ClientFactory, uploadurl.Request{
		AppName:           constants.AppNameSanskaar,
		AppLabel:          "sanskaar",
		BucketEnvVar:      sanskaarBucketEnvVar,
		FileKey:           fileKey,
		ContentType:       contentType,
		ExpirationMinutes: 5,
	})
}
