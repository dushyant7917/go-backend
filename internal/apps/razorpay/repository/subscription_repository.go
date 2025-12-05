package repository

import (
	"go-backend/internal/apps/razorpay/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SubscriptionRepository defines the interface for subscription data operations
type SubscriptionRepository interface {
	Create(subscription *models.Subscription) error
	FindByID(id uuid.UUID) (*models.Subscription, error)
	FindByRazorpaySubscriptionID(razorpaySubID string) (*models.Subscription, error)
	FindByUserIDAndAppName(userID uuid.UUID, appName string) (*models.Subscription, error)
	FindActiveByUserIDAndAppName(userID uuid.UUID, appName string) (*models.Subscription, error)
	FindByPhoneAndAppName(phone string, appName string) (*models.Subscription, error)
	Update(subscription *models.Subscription) error
	UpdateStatus(id uuid.UUID, status models.SubscriptionStatus) error
	FindAll(limit, offset int) ([]models.Subscription, int64, error)
	FindByAppName(appName string, limit, offset int) ([]models.Subscription, int64, error)
	HasAuthenticatedSubscriptionByPhone(phone string) (bool, error)
}

// subscriptionRepository implements SubscriptionRepository interface
type subscriptionRepository struct {
	db *gorm.DB
}

// NewSubscriptionRepository creates a new instance of SubscriptionRepository
func NewSubscriptionRepository(db *gorm.DB) SubscriptionRepository {
	return &subscriptionRepository{db: db}
}

// Create creates a new subscription in the database
func (r *subscriptionRepository) Create(subscription *models.Subscription) error {
	return r.db.Create(subscription).Error
}

// FindByID retrieves a subscription by its ID
func (r *subscriptionRepository) FindByID(id uuid.UUID) (*models.Subscription, error) {
	var subscription models.Subscription
	err := r.db.First(&subscription, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &subscription, nil
}

// FindByRazorpaySubscriptionID retrieves a subscription by Razorpay subscription ID
func (r *subscriptionRepository) FindByRazorpaySubscriptionID(razorpaySubID string) (*models.Subscription, error) {
	var subscription models.Subscription
	err := r.db.Where("razorpay_subscription_id = ?", razorpaySubID).First(&subscription).Error
	if err != nil {
		return nil, err
	}
	return &subscription, nil
}

// FindByUserIDAndAppName retrieves the latest subscription for a user and app
func (r *subscriptionRepository) FindByUserIDAndAppName(userID uuid.UUID, appName string) (*models.Subscription, error) {
	var subscription models.Subscription
	err := r.db.Where("user_id = ? AND app_name = ?", userID, appName).
		Order("created_at DESC").
		First(&subscription).Error
	if err != nil {
		return nil, err
	}
	return &subscription, nil
}

// FindActiveByUserIDAndAppName retrieves active subscription for a user and app
func (r *subscriptionRepository) FindActiveByUserIDAndAppName(userID uuid.UUID, appName string) (*models.Subscription, error) {
	var subscription models.Subscription
	err := r.db.Where("user_id = ? AND app_name = ? AND status = ?",
		userID, appName, models.SubscriptionStatusActive).
		First(&subscription).Error
	if err != nil {
		return nil, err
	}
	return &subscription, nil
}

// FindByPhoneAndAppName retrieves the latest subscription for a phone number and app
func (r *subscriptionRepository) FindByPhoneAndAppName(phone string, appName string) (*models.Subscription, error) {
	var subscription models.Subscription
	err := r.db.Where("phone = ? AND app_name = ?", phone, appName).
		Order("created_at DESC").
		First(&subscription).Error
	if err != nil {
		return nil, err
	}
	return &subscription, nil
}

// Update updates an existing subscription
func (r *subscriptionRepository) Update(subscription *models.Subscription) error {
	return r.db.Save(subscription).Error
}

// UpdateStatus updates only the status of a subscription
func (r *subscriptionRepository) UpdateStatus(id uuid.UUID, status models.SubscriptionStatus) error {
	return r.db.Model(&models.Subscription{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// FindAll retrieves all subscriptions with pagination
func (r *subscriptionRepository) FindAll(limit, offset int) ([]models.Subscription, int64, error) {
	var subscriptions []models.Subscription
	var total int64

	// Get total count
	if err := r.db.Model(&models.Subscription{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Get paginated results
	err := r.db.Limit(limit).Offset(offset).Order("created_at DESC").Find(&subscriptions).Error
	if err != nil {
		return nil, 0, err
	}

	return subscriptions, total, nil
}

// FindByAppName retrieves subscriptions by app name with pagination
func (r *subscriptionRepository) FindByAppName(appName string, limit, offset int) ([]models.Subscription, int64, error) {
	var subscriptions []models.Subscription
	var total int64

	// Get total count
	if err := r.db.Model(&models.Subscription{}).
		Where("app_name = ?", appName).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Get paginated results
	err := r.db.Where("app_name = ?", appName).
		Limit(limit).Offset(offset).
		Order("created_at DESC").
		Find(&subscriptions).Error
	if err != nil {
		return nil, 0, err
	}

	return subscriptions, total, nil
}

// HasAuthenticatedSubscriptionByPhone checks if a phone number has ever had an authenticated subscription
func (r *subscriptionRepository) HasAuthenticatedSubscriptionByPhone(phone string) (bool, error) {
	var count int64
	err := r.db.Model(&models.Subscription{}).
		Where("phone = ? AND metadata::jsonb @> '{\"authenticated\": true}'", phone).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
