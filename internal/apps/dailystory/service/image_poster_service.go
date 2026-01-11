package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/repository"
	userModels "go-backend/internal/apps/user/models"
	userRepository "go-backend/internal/apps/user/repository"
	"go-backend/pkg/storage"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"github.com/google/uuid"
	"golang.org/x/image/font/gofont/goregular"
	"gorm.io/gorm"
)

// Shared HTTP client with connection pooling for optimal performance
var sharedHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	},
}

// ImagePosterService defines the interface for image poster business logic
type ImagePosterService interface {
	GeneratePoster(templateID, userID uuid.UUID) (*models.GeneratePosterResponse, error)
	GetUserPosterStatsByAppName(appName, sortBy string, page, pageSize int) (*models.PaginatedUserPosterStatsResponse, error)
}

// imagePosterService implements ImagePosterService
type imagePosterService struct {
	posterRepo   repository.ImagePosterRepository
	templateRepo repository.ImageTemplateRepository
	userRepo     userRepository.UserRepository
	r2Client     *storage.R2Client
}

// NewImagePosterService creates a new instance of ImagePosterService
func NewImagePosterService(
	posterRepo repository.ImagePosterRepository,
	templateRepo repository.ImageTemplateRepository,
	userRepo userRepository.UserRepository,
	r2Client *storage.R2Client,
) ImagePosterService {
	return &imagePosterService{
		posterRepo:   posterRepo,
		templateRepo: templateRepo,
		userRepo:     userRepo,
		r2Client:     r2Client,
	}
}

// GeneratePoster generates a poster for the given template and user
func (s *imagePosterService) GeneratePoster(templateID, userID uuid.UUID) (*models.GeneratePosterResponse, error) {
	// Step 1: Fetch user and template in parallel
	var user *userModels.User
	var template *models.ImageTemplate
	var userErr, templateErr error
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		user, userErr = s.userRepo.FindByID(userID)
	}()

	go func() {
		defer wg.Done()
		template, templateErr = s.templateRepo.FindByID(templateID)
	}()

	wg.Wait()

	// Check user fetch result
	if userErr != nil {
		if userErr == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to fetch user: %w", userErr)
	}

	// Check template fetch result
	if templateErr != nil {
		if templateErr == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("template not found")
		}
		return nil, fmt.Errorf("failed to fetch template: %w", templateErr)
	}

	// Extract name from user
	userName := ""
	if user.Name != nil {
		userName = *user.Name
	} else {
		userName = "User" // Default name if not set
	}

	// Extract profile picture key from user metadata
	profilePictureKey := ""
	if ppKey, ok := user.Metadata["profile_picture_key"].(string); ok && ppKey != "" {
		profilePictureKey = ppKey
	} else {
		return nil, fmt.Errorf("user does not have a profile picture")
	}

	// Step 2: Check if a poster already exists for this combo
	existingPoster, err := s.posterRepo.FindByCombo(userID, templateID, userName, profilePictureKey)
	if err == nil && existingPoster != nil {
		// Poster exists, return its URL
		bucketName := os.Getenv("R2_DS_POSTERS_BUCKET_NAME")
		if bucketName == "" {
			return nil, fmt.Errorf("R2_DS_POSTERS_BUCKET_NAME not configured")
		}

		publicURL, err := s.r2Client.GetPublicFileURL(bucketName, existingPoster.FileKey)
		if err != nil {
			return nil, fmt.Errorf("failed to generate public URL for existing poster: %w", err)
		}

		return &models.GeneratePosterResponse{
			PosterURL: publicURL,
			Cached:    true,
		}, nil
	}

	// Step 3: Validate template has config
	if template.Config == nil || template.Config.Face == nil || template.Config.Name == nil {
		return nil, fmt.Errorf("template does not have complete configuration")
	}

	// Step 4: Fetch template image URL and user profile picture URL
	templateBucketName := os.Getenv("R2_DS_TEMPLATES_BUCKET_NAME")
	if templateBucketName == "" {
		return nil, fmt.Errorf("R2_DS_TEMPLATES_BUCKET_NAME not configured")
	}

	templateURL, err := s.r2Client.GetPublicFileURL(templateBucketName, template.FileKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get template URL: %w", err)
	}

	userBucketName := os.Getenv("R2_DS_USERS_BUCKET_NAME")
	if userBucketName == "" {
		return nil, fmt.Errorf("R2_DS_USERS_BUCKET_NAME not configured")
	}

	profilePictureURL, err := s.r2Client.GetPresignedViewURL(userBucketName, profilePictureKey, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to get profile picture URL: %w", err)
	}

	// Step 5: Create the poster image
	startMemStats := &runtime.MemStats{}
	runtime.ReadMemStats(startMemStats)
	startAlloc := startMemStats.Alloc

	posterImageData, contentType, err := s.createPosterImage(templateURL, profilePictureURL, userName, template.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create poster image: %w", err)
	}

	endMemStats := &runtime.MemStats{}
	runtime.ReadMemStats(endMemStats)
	endAlloc := endMemStats.Alloc
	memUsedBytes := int64(endAlloc) - int64(startAlloc)
	memUsedMB := float64(memUsedBytes) / (1024 * 1024)
	log.Printf("Image processing completed. RAM used: %.2f MB", memUsedMB)

	// Step 6: Get presigned upload URL for the new poster
	posterBucketName := os.Getenv("R2_DS_POSTERS_BUCKET_NAME")
	if posterBucketName == "" {
		return nil, fmt.Errorf("R2_DS_POSTERS_BUCKET_NAME not configured")
	}

	// Generate file key: images/<random_slug>_<timestamp>.<ext>
	fileExt := ".png"
	if contentType == "image/jpeg" {
		fileExt = ".jpg"
	}
	randomSlug := generateRandomSlug(8)
	timestamp := time.Now().UTC().Unix()
	fileKey := fmt.Sprintf("images/%s_%d%s", randomSlug, timestamp, fileExt)

	uploadURL, err := s.r2Client.GetPresignedUploadURL(posterBucketName, fileKey, contentType, 5)
	if err != nil {
		return nil, fmt.Errorf("failed to get upload URL: %w", err)
	}

	// Step 7: Begin database transaction and create poster record first
	tx := s.posterRepo.GetDB().Begin()
	if tx.Error != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}

	newPoster := &models.ImagePoster{
		UserID:                userID,
		TemplateID:            templateID,
		NameUsed:              userName,
		ProfilePictureKeyUsed: profilePictureKey,
		FileKey:               fileKey,
	}

	if err := s.posterRepo.CreateWithTx(tx, newPoster); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to create poster record: %w", err)
	}

	// Step 8: Upload the poster image
	err = uploadImageToR2(uploadURL, posterImageData, contentType)
	if err != nil {
		// Upload failed - rollback database transaction
		tx.Rollback()
		return nil, fmt.Errorf("failed to upload poster: %w", err)
	}

	// Step 9: Commit transaction (both DB and upload succeeded)
	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Step 10: Return the static URL
	publicURL, err := s.r2Client.GetPublicFileURL(posterBucketName, fileKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate public URL: %w", err)
	}

	return &models.GeneratePosterResponse{
		PosterURL: publicURL,
		Cached:    false,
	}, nil
}

// GetUserPosterStatsByAppName retrieves paginated user poster statistics filtered by app_name with sorting
// Supported sortBy values:
// - "most_active": Most recent activity first, with highest engagement as tiebreaker (find currently active power users)
// - "least_active": Least recent activity first, with lowest engagement as tiebreaker (find low-usage inactive users to contact for feedback)
// - "power_users": Highest poster count first, with recent activity as tiebreaker (find top content creators)
// - "new_engaged": Newest users first, with highest engagement as tiebreaker (find highly engaged new users)
func (s *imagePosterService) GetUserPosterStatsByAppName(appName, sortBy string, page, pageSize int) (*models.PaginatedUserPosterStatsResponse, error) {
	// Validate sortBy parameter
	validSortOptions := map[string]bool{
		"most_active":  true,
		"least_active": true,
		"power_users":  true,
		"new_engaged":  true,
	}

	if !validSortOptions[sortBy] {
		return nil, fmt.Errorf("invalid sort_by value: must be one of [most_active, least_active, power_users, new_engaged]")
	}

	// Validate pagination parameters
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// Get stats from repository
	stats, total, err := s.posterRepo.GetUserPosterStatsByAppName(appName, sortBy, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to get user poster stats: %w", err)
	}

	// Calculate pagination metadata
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	var nextPage *int
	var prevPage *int

	if page < totalPages {
		next := page + 1
		nextPage = &next
	}

	if page > 1 {
		prev := page - 1
		prevPage = &prev
	}

	return &models.PaginatedUserPosterStatsResponse{
		Data:       stats,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}

// createPosterImage creates a poster by compositing template, profile picture, and name
func (s *imagePosterService) createPosterImage(
	templateURL, profilePictureURL, userName string,
	config *models.TemplateConfig,
) ([]byte, string, error) {
	// Download template and profile picture in parallel
	var templateImg, profileImg image.Image
	var templateErr, profileErr error
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		templateImg, templateErr = downloadImage(templateURL)
	}()

	go func() {
		defer wg.Done()
		profileImg, profileErr = downloadImage(profilePictureURL)
	}()

	wg.Wait()

	// Check download results
	if templateErr != nil {
		return nil, "", fmt.Errorf("failed to download template: %w", templateErr)
	}
	if profileErr != nil {
		return nil, "", fmt.Errorf("failed to download profile picture: %w", profileErr)
	}

	// Get template dimensions
	templateBounds := templateImg.Bounds()
	templateWidth := float64(templateBounds.Dx())
	templateHeight := float64(templateBounds.Dy())

	// Create drawing context with template as base
	dc := gg.NewContextForImage(templateImg)

	// Draw profile picture (circular) if config exists
	if config.Face != nil {
		centerX := config.Face.CenterX * templateWidth / 100.0
		centerY := config.Face.CenterY * templateHeight / 100.0
		radius := config.Face.Radius * templateHeight / 100.0

		// Calculate circle diameter - this is the target size for the profile picture
		diameter := int(radius * 2)

		// Scale profile image to exactly match circle diameter
		// This ensures the image fits perfectly in the circle
		scaledProfileImg := resizeImageToSquare(profileImg, diameter)

		// Create circular clipping mask
		dc.DrawCircle(centerX, centerY, radius)
		dc.Clip()

		// Draw scaled profile image anchored at center
		// The image is now exactly diameter x diameter, so it fits perfectly in the circle
		dc.DrawImageAnchored(scaledProfileImg, int(centerX), int(centerY), 0.5, 0.5)

		// Reset clipping
		dc.ResetClip()

		// Draw black border around the circle
		// Border width scales with image size (0.5% of radius for better visibility)
		borderWidth := radius * 0.01 // 1% of radius
		if borderWidth < 3 {
			borderWidth = 3 // Minimum 3px for small images
		}
		dc.SetRGB(0, 0, 0) // Black color
		dc.SetLineWidth(borderWidth)
		dc.DrawCircle(centerX, centerY, radius)
		dc.Stroke()
	}

	// Draw name text with black background if config exists
	if config.Name != nil {
		topLeftX := config.Name.TopLeftX * templateWidth / 100.0
		topLeftY := config.Name.TopLeftY * templateHeight / 100.0
		width := config.Name.Width * templateWidth / 100.0
		height := config.Name.Height * templateHeight / 100.0

		// Draw black rectangle
		dc.SetRGB(0, 0, 0)
		dc.DrawRectangle(topLeftX, topLeftY, width, height)
		dc.Fill()

		// Load font
		font, err := truetype.Parse(goregular.TTF)
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse font: %w", err)
		}

		// Calculate optimal font size that fits the text within the rectangle
		// Start with 40% of rectangle height as initial size
		fontSize := calculateOptimalFontSize(dc, font, userName, width, height)

		face := truetype.NewFace(font, &truetype.Options{
			Size: fontSize,
			DPI:  72,
		})
		dc.SetFontFace(face)

		// Draw white centered text
		// For proper vertical centering, we need to account for font metrics
		dc.SetRGB(1, 1, 1)
		textX := topLeftX + width/2
		textY := topLeftY + height/2
		// Use anchor 0.5, 0.5 for horizontal and vertical centering
		dc.DrawStringAnchored(userName, textX, textY, 0.5, 0.5)
	}

	// Encode to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, dc.Image()); err != nil {
		return nil, "", fmt.Errorf("failed to encode image: %w", err)
	}

	return buf.Bytes(), "image/png", nil
}

// downloadImage downloads an image from a URL and returns it as image.Image
func downloadImage(url string) (image.Image, error) {
	resp, err := sharedHTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download image: status %d", resp.StatusCode)
	}

	// Decode image
	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, err
	}

	return img, nil
}

// resizeImageToSquare resizes an image to a square of the specified size
// It scales and crops from center to fit the square dimensions
func resizeImageToSquare(img image.Image, size int) image.Image {
	bounds := img.Bounds()
	imgWidth := bounds.Dx()
	imgHeight := bounds.Dy()

	// Calculate scale factor to cover the square
	// Use the larger scale to ensure the image covers the entire area
	scaleX := float64(size) / float64(imgWidth)
	scaleY := float64(size) / float64(imgHeight)

	scale := scaleX
	if scaleY > scaleX {
		scale = scaleY
	}

	// Calculate new dimensions after scaling
	scaledWidth := float64(imgWidth) * scale
	scaledHeight := float64(imgHeight) * scale

	// Create a new context with exact target size
	dc := gg.NewContext(size, size)

	// Calculate offset to center the scaled image
	offsetX := (float64(size) - scaledWidth) / 2.0
	offsetY := (float64(size) - scaledHeight) / 2.0

	// Apply scaling and draw image centered
	dc.Scale(scale, scale)
	dc.DrawImage(img, int(offsetX/scale), int(offsetY/scale))

	return dc.Image()
}

// calculateOptimalFontSize calculates the optimal font size to fit text within rectangle
// It ensures the text fits both horizontally and vertically with some padding
func calculateOptimalFontSize(dc *gg.Context, font *truetype.Font, text string, rectWidth, rectHeight float64) float64 {
	// Start with 40% of rectangle height as initial size
	maxFontSize := rectHeight * 0.4
	minFontSize := rectHeight * 0.1 // Minimum readable size

	// Add horizontal padding (5% on each side, so 90% usable width)
	usableWidth := rectWidth * 0.9
	// Add vertical padding (5% on top and bottom, so 90% usable height)
	usableHeight := rectHeight * 0.9

	// Binary search for optimal font size
	fontSize := maxFontSize

	for fontSize >= minFontSize {
		face := truetype.NewFace(font, &truetype.Options{
			Size: fontSize,
			DPI:  72,
		})
		dc.SetFontFace(face)

		// Measure text dimensions
		textWidth, textHeight := dc.MeasureString(text)

		// Check if text fits within usable dimensions
		if textWidth <= usableWidth && textHeight <= usableHeight {
			return fontSize
		}

		// Reduce font size by 5% and try again
		fontSize *= 0.95
	}

	// Return minimum font size if nothing fits
	return minFontSize
}

// uploadImageToR2 uploads image data to R2 using presigned URL
func uploadImageToR2(presignedURL string, imageData []byte, contentType string) error {
	req, err := http.NewRequest(http.MethodPut, presignedURL, bytes.NewReader(imageData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", contentType)

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// generateRandomSlug generates a random slug of specified length
func generateRandomSlug(length int) string {
	bytes := make([]byte, length/2)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
