package handler

import (
	"errors"
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

// validateStatusCategory checks category against statusCategoryPattern, returning an error
// describing the validation failure if it doesn't match.
func validateStatusCategory(category string) error {
	if !statusCategoryPattern.MatchString(category) {
		return errors.New("category must contain only letters and hyphens")
	}
	return nil
}

// sanskaarBucketEnvVar is the single R2 bucket shared by every Sanskaar resource
// (devotional music, tones, wallpapers). Resources are separated by key prefix, not by
// bucket, since none of them differ in access control, lifecycle, or ownership — see
// BuildDevotionalMusicFileKey / BuildAudioThumbnailFileKey / BuildWallpaperFileKey for the
// prefixes.
const sanskaarBucketEnvVar = "R2_SANSKAAR_BUCKET_NAME"

// validateAudioThumbnailFile validates filename against the declared purpose ("audio" or
// "thumbnail"), returning the extension, filename without extension, and resolved content
// type, or an error describing the validation failure.
func validateAudioThumbnailFile(filename, purpose string) (ext, filenameWithoutExt, contentType string, err error) {
	if strings.TrimSpace(filename) == "" {
		return "", "", "", errors.New("filename cannot be empty")
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
		return "", "", "", errors.New("only audio files are supported for purpose 'audio'")
	}
	if purpose == "thumbnail" && !strings.HasPrefix(contentType, "image/") {
		return "", "", "", errors.New("only image files are supported for purpose 'thumbnail'")
	}

	return ext, filenameWithoutExt, contentType, nil
}

// BuildAudioThumbnailFileKey computes the R2 object key and content type for a tone's audio or
// thumbnail file. Tone has "audio" and "thumbnail" file purposes but (unlike DevotionalMusic) no
// category to organize files by, so files are simply keyed under <resourcePrefix>/audio/ or
// <resourcePrefix>/thumbnails/ (resourcePrefix is "tones"). Exported so both the
// POST /api/v1/sanskaar/tones/upload-url handler and local upload scripts (which write directly
// to R2 instead of going through that endpoint) use the exact same key-construction rules.
func BuildAudioThumbnailFileKey(filename, purpose, resourcePrefix string) (fileKey, contentType string, err error) {
	ext, filenameWithoutExt, contentType, err := validateAudioThumbnailFile(filename, purpose)
	if err != nil {
		return "", "", err
	}

	folder := "audio"
	if purpose == "thumbnail" {
		folder = "thumbnails"
	}

	timestamp := time.Now().UTC().Unix()
	fileKey = fmt.Sprintf("%s/%s/%s_%d%s", resourcePrefix, folder, filenameWithoutExt, timestamp, ext)
	return fileKey, contentType, nil
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

	fileKey, contentType, err := BuildAudioThumbnailFileKey(req.Filename, req.Purpose, resourcePrefix)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

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

// BuildDevotionalMusicFileKey computes the R2 object key and content type for a devotional
// music track's audio or thumbnail file. DevotionalMusic organizes files by category (mantras,
// bhajans, aartis, chalisas) and, within each category, by purpose:
// devotional-music/<category>/audio/<filename> for the audio file and
// devotional-music/<category>/thumbnails/<filename> for its thumbnail. Exported so both the
// POST /api/v1/sanskaar/devotional-music/upload-url handler and local upload scripts (which
// write directly to R2 instead of going through that endpoint) use the exact same
// key-construction rules.
func BuildDevotionalMusicFileKey(filename, purpose, category string) (fileKey, contentType string, err error) {
	ext, filenameWithoutExt, contentType, err := validateAudioThumbnailFile(filename, purpose)
	if err != nil {
		return "", "", err
	}

	folder := "audio"
	if purpose == "thumbnail" {
		folder = "thumbnails"
	}
	categoryFolder := devotionalMusicCategoryFolders[category]

	timestamp := time.Now().UTC().Unix()
	fileKey = fmt.Sprintf("devotional-music/%s/%s/%s_%d%s", categoryFolder, folder, filenameWithoutExt, timestamp, ext)
	return fileKey, contentType, nil
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

	fileKey, contentType, err := BuildDevotionalMusicFileKey(req.Filename, req.Purpose, req.Category)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	uploadurl.Respond(c, r2ClientFactory, uploadurl.Request{
		AppName:           constants.AppNameSanskaar,
		AppLabel:          "sanskaar",
		BucketEnvVar:      sanskaarBucketEnvVar,
		FileKey:           fileKey,
		ContentType:       contentType,
		ExpirationMinutes: 5,
	})
}

// mediaFileKeyParts resolves the folder, filename-without-extension, extension, and content type
// shared by wallpapers' and statuses' media/thumbnail files: both key thumbnails (always images)
// under a "thumbnails" folder, and otherwise key images under imageFolder and videos under
// videoFolder. purpose defaults to "media" (anything other than "thumbnail") when empty, so
// existing clients that predate the thumbnail field keep working.
func mediaFileKeyParts(filename, typ, purpose, imageFolder, videoFolder string) (folder, filenameWithoutExt, ext, contentType string, err error) {
	if strings.TrimSpace(filename) == "" {
		return "", "", "", "", errors.New("filename cannot be empty")
	}

	var defaultExt, wantPrefix string
	if purpose == "thumbnail" {
		folder, defaultExt, wantPrefix = "thumbnails", ".png", "image/"
	} else {
		folder, defaultExt, wantPrefix = imageFolder, ".jpg", "image/"
		if typ == "video" {
			folder, defaultExt, wantPrefix = videoFolder, ".mp4", "video/"
		}
	}

	ext = filepath.Ext(filename)
	if ext == "" {
		ext = defaultExt
	}
	filenameWithoutExt = strings.TrimSuffix(filename, ext)
	contentType = utils.GetContentTypeFromExtension(ext)

	if !strings.HasPrefix(contentType, wantPrefix) {
		errLabel := typ
		if purpose == "thumbnail" {
			errLabel = "thumbnail"
		}
		return "", "", "", "", fmt.Errorf("only %s files are supported for '%s'", strings.TrimSuffix(wantPrefix, "/"), errLabel)
	}

	return folder, filenameWithoutExt, ext, contentType, nil
}

// BuildWallpaperFileKey computes the R2 object key and content type for a wallpaper's media or
// thumbnail file. Wallpapers organize files by purpose, then (for purpose "media") by type:
// video wallpapers ("live") under wallpapers/live/, image wallpapers ("static") under
// wallpapers/static/, thumbnails (always images, only applicable to video wallpapers) under
// wallpapers/thumbnails/. Exported so both the POST /api/v1/sanskaar/wallpapers/upload-url
// handler and local upload scripts (which write directly to R2 instead of going through that
// endpoint) use the exact same key-construction rules.
func BuildWallpaperFileKey(filename, typ, purpose string) (fileKey, contentType string, err error) {
	folder, filenameWithoutExt, ext, contentType, err := mediaFileKeyParts(filename, typ, purpose, "static", "live")
	if err != nil {
		return "", "", err
	}

	timestamp := time.Now().UTC().Unix()
	fileKey = fmt.Sprintf("wallpapers/%s/%s_%d%s", folder, filenameWithoutExt, timestamp, ext)
	return fileKey, contentType, nil
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

	fileKey, contentType, err := BuildWallpaperFileKey(req.Filename, req.Type, req.Purpose)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	uploadurl.Respond(c, r2ClientFactory, uploadurl.Request{
		AppName:           constants.AppNameSanskaar,
		AppLabel:          "sanskaar",
		BucketEnvVar:      sanskaarBucketEnvVar,
		FileKey:           fileKey,
		ContentType:       contentType,
		ExpirationMinutes: 5,
	})
}

// BuildStatusFileKey computes the R2 object key and content type for a status's media or
// thumbnail file. Statuses organize files by category, then purpose: statuses/<category>/images/
// for image statuses, statuses/<category>/videos/ for video statuses,
// statuses/<category>/thumbnails/ for thumbnails (always images, only applicable to video
// statuses). category is stored in the key exactly as given (validated against
// statusCategoryPattern, no folder-name mapping like DevotionalMusic's fixed categories).
// Exported so both the POST /api/v1/sanskaar/statuses/upload-url handler and local upload
// scripts (which write directly to R2 instead of going through that endpoint) use the exact same
// key-construction rules.
func BuildStatusFileKey(filename, typ, category, purpose string) (fileKey, contentType string, err error) {
	// Checked here (ahead of mediaFileKeyParts' own filename check) to preserve the original
	// validation order: filename, then category, then folder/content-type.
	if strings.TrimSpace(filename) == "" {
		return "", "", errors.New("filename cannot be empty")
	}
	if err := validateStatusCategory(category); err != nil {
		return "", "", err
	}

	folder, filenameWithoutExt, ext, contentType, err := mediaFileKeyParts(filename, typ, purpose, "images", "videos")
	if err != nil {
		return "", "", err
	}

	timestamp := time.Now().UTC().Unix()
	fileKey = fmt.Sprintf("statuses/%s/%s/%s_%d%s", category, folder, filenameWithoutExt, timestamp, ext)
	return fileKey, contentType, nil
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

	fileKey, contentType, err := BuildStatusFileKey(req.Filename, req.Type, req.Category, req.Purpose)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	uploadurl.Respond(c, r2ClientFactory, uploadurl.Request{
		AppName:           constants.AppNameSanskaar,
		AppLabel:          "sanskaar",
		BucketEnvVar:      sanskaarBucketEnvVar,
		FileKey:           fileKey,
		ContentType:       contentType,
		ExpirationMinutes: 5,
	})
}
