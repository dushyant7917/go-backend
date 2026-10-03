// Package uploadurl centralizes the "generate a presigned R2 upload URL and respond" tail
// shared by every app's upload-url endpoint (DailyStory's image templates, profile pictures,
// image posters; Sanskaar's devotional music, tones, wallpapers). Each caller still owns its
// own filename/content-type validation and file-key construction, since those genuinely differ
// per resource — only the R2 resolution + presigned URL generation + JSON response, which was
// byte-identical across every existing call site, lives here.
package uploadurl

import (
	"fmt"
	"net/http"
	"os"

	r2ConfigService "go-backend/internal/apps/r2/config/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
)

// Request describes one presigned-upload-url call. Grouped into a struct (rather than several
// positional string parameters of the same type) so call sites are self-documenting and a typo
// can't silently swap two fields, e.g. FileKey for ContentType.
type Request struct {
	// AppName is the constants.AppNameX value used to resolve the R2 client via the factory.
	AppName string
	// AppLabel is a short human-readable app name used only in the "R2 configuration not
	// found" error message text (kept separate from AppName so existing error wording is
	// preserved exactly regardless of the underlying constant's value).
	AppLabel string
	// BucketEnvVar is the name of the environment variable holding the target bucket name.
	BucketEnvVar string
	// FileKey is the object key the presigned URL will be issued for.
	FileKey string
	// ContentType is the MIME type the presigned URL is signed for; the client must send it
	// as the Content-Type header on the actual upload.
	ContentType string
	// ExpirationMinutes is how long the presigned URL stays valid.
	ExpirationMinutes int
}

// Respond resolves an R2 client and bucket for req, generates a presigned PUT URL, and writes
// it as the standard JSON response (presigned_url, file_key, upload_headers, instructions). On
// any failure it writes the appropriate error response itself — callers should return
// immediately after calling this.
func Respond(c *gin.Context, factory *r2ConfigService.R2ClientFactory, req Request) {
	bucketName := os.Getenv(req.BucketEnvVar)
	if bucketName == "" {
		commonResponse.Error(c, http.StatusInternalServerError, nil, "R2 bucket configuration missing")
		return
	}

	r2Client, err := factory.GetClient(req.AppName)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, fmt.Sprintf("R2 configuration not found for %s app", req.AppLabel))
		return
	}

	presignedURL, err := r2Client.GetPresignedUploadURL(bucketName, req.FileKey, req.ContentType, req.ExpirationMinutes)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, "Failed to generate upload URL")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"presigned_url": presignedURL,
		"file_key":      req.FileKey,
		"upload_headers": gin.H{
			"Content-Type": req.ContentType,
		},
		"instructions": fmt.Sprintf("MUST send Content-Type: %s header when uploading. The presigned URL signature requires this exact header.", req.ContentType),
	})
}
