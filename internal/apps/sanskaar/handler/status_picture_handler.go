package handler

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	r2ConfigService "go-backend/internal/apps/r2/config/service"
	"go-backend/internal/common/constants"
	"go-backend/internal/common/uploadurl"
	"go-backend/pkg/utils"

	"github.com/gin-gonic/gin"
)

// statusPictureKeyPrefix is the R2 key prefix for Sanskaar user status pictures, stored in the
// same shared Sanskaar bucket as wallpapers/statuses/tones/devotional-music (see
// sanskaarBucketEnvVar) but under their own prefix.
const statusPictureKeyPrefix = "status-pictures/"

// StatusPictureHandler handles the Sanskaar user status-picture upload/view flow. Unlike
// Wallpaper/Status/Tone/DevotionalMusic, the picture key itself isn't stored in a Sanskaar
// table — the client writes it onto metadata["status_data"]["picture_key"] via the existing
// PUT /api/v1/users/:id endpoint (see internal/apps/user/service/user_service.go), so this
// handler only needs to hand out the presigned upload URL and resolve the public view URL.
type StatusPictureHandler struct {
	r2ClientFactory *r2ConfigService.R2ClientFactory
	publicURLBase   string
}

// NewStatusPictureHandler creates a new instance of StatusPictureHandler.
func NewStatusPictureHandler(r2ClientFactory *r2ConfigService.R2ClientFactory, publicURLBase string) *StatusPictureHandler {
	return &StatusPictureHandler{r2ClientFactory: r2ClientFactory, publicURLBase: publicURLBase}
}

// GetUploadURL handles POST /api/v1/sanskaar/status-picture/upload-url
func (h *StatusPictureHandler) GetUploadURL(c *gin.Context) {
	var req struct {
		Filename string `json:"filename" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if strings.TrimSpace(req.Filename) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename cannot be empty"})
		return
	}

	ext := filepath.Ext(req.Filename)
	if ext == "" {
		ext = ".png"
	}
	filenameWithoutExt := strings.TrimSuffix(req.Filename, ext)
	contentType := utils.GetContentTypeFromExtension(ext)

	if !strings.HasPrefix(contentType, "image/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only image files are supported"})
		return
	}

	timestamp := time.Now().UTC().Unix()
	fileKey := fmt.Sprintf("%s%s_%d%s", statusPictureKeyPrefix, filenameWithoutExt, timestamp, ext)

	uploadurl.Respond(c, h.r2ClientFactory, uploadurl.Request{
		AppName:           constants.AppNameSanskaar,
		AppLabel:          "sanskaar",
		BucketEnvVar:      sanskaarBucketEnvVar,
		FileKey:           fileKey,
		ContentType:       contentType,
		ExpirationMinutes: 5,
	})
}

// GetViewURL handles GET /api/v1/sanskaar/status-picture/view-url. Sanskaar's bucket is public
// (same as wallpapers/statuses/tones/devotional-music), so this just resolves the public URL —
// no presigning needed.
func (h *StatusPictureHandler) GetViewURL(c *gin.Context) {
	fileKey := c.Query("file_key")
	if fileKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_key query parameter is required"})
		return
	}
	if !strings.HasPrefix(fileKey, statusPictureKeyPrefix) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file_key: must be a status picture"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"view_url": h.publicURLBase + "/" + fileKey,
		"file_key": fileKey,
	})
}
