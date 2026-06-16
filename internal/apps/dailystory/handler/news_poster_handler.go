package handler

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/service"
	r2ConfigService "go-backend/internal/apps/r2/config/service"
	recurringPaymentRepo "go-backend/internal/apps/razorpay/recurring_payment/repository"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// NewsPosterHandler handles HTTP requests for news poster operations
type NewsPosterHandler struct {
	service              service.NewsPosterService
	recurringPaymentRepo recurringPaymentRepo.RecurringPaymentRepository
	r2ClientFactory      *r2ConfigService.R2ClientFactory
}

// NewNewsPosterHandler creates a new instance of NewsPosterHandler
func NewNewsPosterHandler(service service.NewsPosterService, recurringPaymentRepo recurringPaymentRepo.RecurringPaymentRepository, r2ClientFactory *r2ConfigService.R2ClientFactory) *NewsPosterHandler {
	return &NewsPosterHandler{service: service, recurringPaymentRepo: recurringPaymentRepo, r2ClientFactory: r2ClientFactory}
}

// CreateNewsPoster handles POST /api/v1/dailystory/news-posters
func (h *NewsPosterHandler) CreateNewsPoster(c *gin.Context) {
	var req models.CreateNewsPosterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate UUIDs
	if req.NewsID == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "news_id is required"})
		return
	}
	if req.UserID == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	// Validate required string fields
	if req.UserName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_name is required"})
		return
	}
	if req.UserStateID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_state_id is required"})
		return
	}
	if req.LanguageCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "language_code is required"})
		return
	}

	newsPoster, err := h.service.CreateNewsPoster(&req)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": newsPoster})
}

// GetNewsPosterPrefillData handles GET /api/v1/dailystory/news-posters/prefill-data
func (h *NewsPosterHandler) GetNewsPosterPrefillData(c *gin.Context) {
	newsID, err := uuid.Parse(c.Query("news_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid news_id"})
		return
	}

	userID, err := uuid.Parse(c.Query("user_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	var (
		isActive bool
		subErr   error
		result   *models.NewsPosterPrefillResponse
		dataErr  error
	)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		isActive, subErr = h.recurringPaymentRepo.IsSubscriptionActive(userID, "DailyStoryApp", time.Now().UTC())
	}()

	go func() {
		defer wg.Done()
		result, dataErr = h.service.GetNewsPosterPrefillData(newsID, userID)
	}()

	wg.Wait()

	// dataErr (user/news/translation not found) takes priority over the subscription check.
	if dataErr != nil {
		switch {
		case errors.Is(dataErr, service.ErrNewsPosterPrefillUserNotFound),
			errors.Is(dataErr, service.ErrNewsPosterPrefillNewsNotFound),
			errors.Is(dataErr, service.ErrNewsPosterPrefillTranslationNotFound):
			commonResponse.Error(c, http.StatusNotFound, dataErr, dataErr.Error())
		default:
			commonResponse.Error(c, http.StatusInternalServerError, dataErr, dataErr.Error())
		}
		return
	}

	if subErr != nil {
		commonResponse.Error(c, http.StatusInternalServerError, subErr, subErr.Error())
		return
	}
	if !isActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "active subscription required"})
		return
	}

	if result.User.ProfilePictureKey != nil && *result.User.ProfilePictureKey != "" {
		if link, err := h.profilePictureViewLink(*result.User.ProfilePictureKey); err == nil {
			result.User.ProfilePictureLink = &link
		}
	}

	commonResponse.Success(c, result)
}

// profilePictureViewLink generates a presigned URL (valid for 60 minutes) for a stored profile picture key.
func (h *NewsPosterHandler) profilePictureViewLink(fileKey string) (string, error) {
	r2Client, bucketName, err := dailyStoryUsersR2Client(h.r2ClientFactory)
	if err != nil {
		return "", err
	}

	return r2Client.GetPresignedViewURL(bucketName, fileKey, 60)
}
