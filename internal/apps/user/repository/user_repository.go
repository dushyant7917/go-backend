package repository

import (
	"go-backend/internal/apps/user/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserRepository defines the interface for user data operations
type UserRepository interface {
	Create(user *models.User) error
	FindByID(id uuid.UUID) (*models.User, error)
	FindByAppAndContact(appName, countryCode, phone string) (*models.User, error)
	FindByAppAndEmail(appName, email string) (*models.User, error)
	Update(user *models.User) error
}

// userRepository implements UserRepository
type userRepository struct {
	db *gorm.DB
}

// NewUserRepository creates a new instance of UserRepository
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

// Create creates a new user in the database
func (r *userRepository) Create(user *models.User) error {
	return r.db.Create(user).Error
}

// FindByID retrieves a user by its ID
func (r *userRepository) FindByID(id uuid.UUID) (*models.User, error) {
	var user models.User
	if err := r.db.First(&user, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByAppAndContact retrieves a user by app name, country code and phone
func (r *userRepository) FindByAppAndContact(appName, countryCode, phone string) (*models.User, error) {
	var user models.User
	if err := r.db.Where("app_name = ? AND country_code = ? AND phone = ?", appName, countryCode, phone).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByAppAndEmail retrieves a user by app name and email
func (r *userRepository) FindByAppAndEmail(appName, email string) (*models.User, error) {
	var user models.User
	if err := r.db.Where("app_name = ? AND email = ?", appName, email).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// Update updates an existing user
func (r *userRepository) Update(user *models.User) error {
	return r.db.Save(user).Error
}
