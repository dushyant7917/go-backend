package service

import (
	"errors"
	"strings"

	"go-backend/internal/apps/user/models"
	"go-backend/internal/apps/user/repository"

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
}

// userService implements UserService
type userService struct {
	repo repository.UserRepository
}

// NewUserService creates a new instance of UserService
func NewUserService(repo repository.UserRepository) UserService {
	return &userService{repo: repo}
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
			return nil, errors.New("app_name cannot be empty")
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
