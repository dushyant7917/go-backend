package service

import (
	"errors"
	"os"
	"strings"

	crushRepository "go-backend/internal/apps/crush/repository"
	"go-backend/internal/apps/user/models"
	"go-backend/internal/apps/user/repository"
	"go-backend/pkg/storage"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserService defines the interface for user business logic
type UserService interface {
	CreateUser(req models.CreateUserRequest) (*models.UserResponse, error)
	UpdateUser(id uuid.UUID, req models.UpdateUserRequest) (*models.UserResponse, error)
	GetUserByID(id uuid.UUID) (*models.UserResponse, error)
	GetUserByAppAndContact(appName, countryCode, phone string) (*models.UserResponse, error)
	GetUserByAppAndEmail(appName, email string) (*models.UserResponse, error)
	ListAllUsersPaginated(appName string, page, pageSize int) (*models.PaginatedUsersWithCountResponse, error)
	GetUserCountByDay(appName string, days, page, pageSize int) (*models.PaginatedUserDailyCountResponse, error)
}

// userService implements UserService
type userService struct {
	repo      repository.UserRepository
	crushRepo crushRepository.CrushRepository
	r2Client  *storage.R2Client
}

// NewUserService creates a new instance of UserService
func NewUserService(repo repository.UserRepository, crushRepo crushRepository.CrushRepository, r2Client *storage.R2Client) UserService {
	return &userService{
		repo:      repo,
		crushRepo: crushRepo,
		r2Client:  r2Client,
	}
}

// validateContactRule ensures either email or (country_code + phone) is present
func validateContactRule(countryCode, phone, email *string) error {
	emailPresent := email != nil && strings.TrimSpace(*email) != ""
	phonePresent := phone != nil && strings.TrimSpace(*phone) != ""
	ccPresent := countryCode != nil && strings.TrimSpace(*countryCode) != ""

	if !emailPresent && !(phonePresent && ccPresent) {
		return errors.New("either email or (country_code + phone) is required")
	}
	return nil
}

// CreateUser creates a new user
func (s *userService) CreateUser(req models.CreateUserRequest) (*models.UserResponse, error) {
	if err := validateContactRule(req.CountryCode, req.Phone, req.Email); err != nil {
		return nil, err
	}

	// Build model
	user := &models.User{
		Name:        req.Name,
		CountryCode: req.CountryCode,
		Phone:       req.Phone,
		Email:       req.Email,
		AppName:     req.AppName,
		Metadata:    req.Metadata,
	}

	if err := s.repo.Create(user); err != nil {
		return nil, err
	}
	resp := user.ToResponse()
	return &resp, nil
}

// GetUserByID retrieves a user by ID
func (s *userService) GetUserByID(id uuid.UUID) (*models.UserResponse, error) {
	user, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	resp := user.ToResponse()
	return &resp, nil
}

// GetUserByAppAndContact retrieves a user by app name, country code and phone
func (s *userService) GetUserByAppAndContact(appName, countryCode, phone string) (*models.UserResponse, error) {
	user, err := s.repo.FindByAppAndContact(appName, countryCode, phone)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	resp := user.ToResponse()
	return &resp, nil
}

// GetUserByAppAndEmail retrieves a user by app name and email
func (s *userService) GetUserByAppAndEmail(appName, email string) (*models.UserResponse, error) {
	user, err := s.repo.FindByAppAndEmail(appName, email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	resp := user.ToResponse()
	return &resp, nil
}

// UpdateUser updates an existing user
func (s *userService) UpdateUser(id uuid.UUID, req models.UpdateUserRequest) (*models.UserResponse, error) {
	user, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}

	// Check if we need to delete old profile picture
	var oldProfilePictureKey string
	var needsFileDeletion bool
	if req.Metadata != nil {
		if newKey, exists := req.Metadata["profile_picture_key"]; exists {
			// Extract old profile picture key from current metadata
			if user.Metadata != nil {
				if oldKey, ok := user.Metadata["profile_picture_key"].(string); ok && oldKey != "" {
					// Only delete if keys are different
					if newKey != oldKey {
						oldProfilePictureKey = oldKey
						needsFileDeletion = true
					}
				}
			}
		}
	}

	// If profile picture deletion is needed, use atomic transaction
	if needsFileDeletion {
		// Get bucket name based on app_name
		bucketName := s.getBucketNameForApp(user.AppName)
		if bucketName == "" {
			return nil, errors.New("bucket configuration not found for app: " + user.AppName)
		}

		// Perform atomic transaction: update DB first, then delete file
		err = s.repo.UpdateWithTransaction(func(txRepo repository.UserRepository) error {
			// Apply all updates to user object
			if err := applyUserUpdates(user, req); err != nil {
				return err
			}

			// Validate rule after updates
			if err := validateContactRule(user.CountryCode, user.Phone, user.Email); err != nil {
				return err
			}

			// Step 1: Update user in DB within transaction
			if err := txRepo.Update(user); err != nil {
				return err // DB update failed, transaction will auto-rollback
			}

			// Step 2: DB update succeeded, now delete old file from R2
			if err := s.r2Client.DeleteFile(bucketName, oldProfilePictureKey); err != nil {
				// R2 deletion failed, return error to trigger transaction rollback
				return errors.New("failed to delete old profile picture: " + err.Error())
			}

			// Both operations succeeded, commit transaction
			return nil
		})

		if err != nil {
			return nil, err
		}

		resp := user.ToResponse()
		return &resp, nil
	}

	// Normal update without profile picture deletion
	if err := applyUserUpdates(user, req); err != nil {
		return nil, err
	}

	// Validate rule after updates
	if err := validateContactRule(user.CountryCode, user.Phone, user.Email); err != nil {
		return nil, err
	}

	if err := s.repo.Update(user); err != nil {
		return nil, err
	}
	resp := user.ToResponse()
	return &resp, nil
}

// getBucketNameForApp returns the appropriate R2 bucket name based on app_name
func (s *userService) getBucketNameForApp(appName string) string {
	// Normalize app name to lowercase for comparison
	normalizedAppName := strings.ToLower(appName)

	switch normalizedAppName {
	case "dailystory", "dailystoryapp":
		// Return the existing users bucket for DailyStory app
		return os.Getenv("R2_DS_USERS_BUCKET_NAME")
	case "crushconnect", "crushconnectapp":
		// Return the bucket name for CrushConnect app if needed
		return os.Getenv("R2_CC_USERS_BUCKET_NAME")
	default:
		// Return empty string for unknown apps (no deletion will occur)
		return ""
	}
}

// applyUserUpdates applies the update request fields to the user model
func applyUserUpdates(user *models.User, req models.UpdateUserRequest) error {
	// Apply updates if provided
	if req.Name != nil {
		user.Name = req.Name
	}
	if req.CountryCode != nil {
		user.CountryCode = req.CountryCode
	}
	if req.Phone != nil {
		user.Phone = req.Phone
	}
	if req.Email != nil {
		user.Email = req.Email
	}
	if req.AppName != nil {
		trimmed := strings.TrimSpace(*req.AppName)
		if trimmed == "" {
			return errors.New("app_name cannot be empty")
		}
		user.AppName = trimmed
	}
	// Merge metadata if provided (partial update)
	if req.Metadata != nil && len(req.Metadata) > 0 {
		if user.Metadata == nil {
			user.Metadata = make(models.Metadata)
		}
		for key, value := range req.Metadata {
			user.Metadata[key] = value
		}
	}
	return nil
}

// ListAllUsersPaginated retrieves all users with pagination and optional app_name filter
func (s *userService) ListAllUsersPaginated(appName string, page, pageSize int) (*models.PaginatedUsersWithCountResponse, error) {
	// Validate page and pageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10 // default page size
	}
	if pageSize > 100 {
		pageSize = 100 // max page size
	}

	users, total, err := s.repo.FindAllPaginated(appName, page, pageSize)
	if err != nil {
		return nil, err
	}

	// Build response with crushes count
	responses := make([]models.UserWithCountResponse, len(users))
	for i, user := range users {
		// Get crushes count for this user
		crushesCount, err := s.crushRepo.CountByUserID(user.ID)
		if err != nil {
			// If error counting crushes, set count to 0
			crushesCount = 0
		}

		responses[i] = models.UserWithCountResponse{
			ID:           user.ID,
			Name:         user.Name,
			CountryCode:  user.CountryCode,
			Phone:        user.Phone,
			Email:        user.Email,
			AppName:      user.AppName,
			Metadata:     user.Metadata,
			CrushesCount: crushesCount,
			CreatedAt:    user.CreatedAt,
			UpdatedAt:    user.UpdatedAt,
		}
	}

	// Calculate total pages
	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

	// Calculate next and previous pages
	var nextPage, prevPage *int
	if page > 1 {
		prev := page - 1
		prevPage = &prev
	}
	if page < totalPages {
		next := page + 1
		nextPage = &next
	}

	return &models.PaginatedUsersWithCountResponse{
		Data:       responses,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}

// GetUserCountByDay retrieves user count grouped by day for the last n days with pagination
func (s *userService) GetUserCountByDay(appName string, days, page, pageSize int) (*models.PaginatedUserDailyCountResponse, error) {
	if strings.TrimSpace(appName) == "" {
		return nil, errors.New("app_name is required")
	}
	if days < 1 {
		return nil, errors.New("days must be at least 1")
	}

	// Validate page and pageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10 // default page size
	}
	if pageSize > 100 {
		pageSize = 100 // max page size
	}

	results, total, err := s.repo.GetUserCountByDay(appName, days, page, pageSize)
	if err != nil {
		return nil, err
	}

	// Calculate pagination metadata
	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

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

	return &models.PaginatedUserDailyCountResponse{
		Data:       results,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}
