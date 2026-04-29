package repository

import (
	"time"

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
	FindAllPaginated(appName string, page, pageSize int) ([]models.User, int64, error)
	UpdateWithTransaction(fn func(txRepo UserRepository) error) error
	FindByAppWithPushToken(appName string) ([]models.User, error)
	GetUserCountByDay(appName string, days, page, pageSize int) ([]models.UserDailyCountResponse, int64, error)
	FindByApp(appName string) ([]models.User, error)
	FindNewUsersWithoutActiveSubscription(appName string, days int) ([]models.User, error)
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

// FindAllPaginated retrieves users with pagination and optional app_name filter
func (r *userRepository) FindAllPaginated(appName string, page, pageSize int) ([]models.User, int64, error) {
	var users []models.User
	var total int64

	query := r.db.Model(&models.User{})

	// Apply app_name filter if provided
	if appName != "" {
		query = query.Where("app_name = ?", appName)
	}

	// Get total count
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Calculate offset
	offset := (page - 1) * pageSize

	// Get paginated results
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&users).Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// UpdateWithTransaction executes a function within a database transaction
func (r *userRepository) UpdateWithTransaction(fn func(txRepo UserRepository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		txRepo := &userRepository{db: tx}
		return fn(txRepo)
	})
}

// FindByAppWithPushToken retrieves users by app_name who have push_notification_token in metadata
func (r *userRepository) FindByAppWithPushToken(appName string) ([]models.User, error) {
	var users []models.User
	// Query users with app_name match and check if metadata contains push_notification_token
	if err := r.db.Where("app_name = ? AND metadata ? 'push_notification_token'", appName).Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// GetUserCountByDay retrieves user count grouped by day for the last n days with pagination
func (r *userRepository) GetUserCountByDay(appName string, days, page, pageSize int) ([]models.UserDailyCountResponse, int64, error) {
	results := []models.UserDailyCountResponse{}
	total := int64(days)

	// Query to get user counts for each day with pagination including days with zero signups
	offset := (page - 1) * pageSize
	err := r.db.Table("(SELECT generate_series(CURRENT_DATE - (INTERVAL '1 day' * (? - 1)), CURRENT_DATE, '1 day'::interval)::date AS date) AS ds", days).
		Select("ds.date::text as date, COUNT(u.id) as user_count").
		Joins("LEFT JOIN users u ON DATE(u.created_at) = ds.date AND u.app_name = ? AND u.deleted_at IS NULL", appName).
		Group("ds.date").
		Order("ds.date DESC").
		Limit(pageSize).
		Offset(offset).
		Scan(&results).Error

	if err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// FindByApp retrieves all users by app name
func (r *userRepository) FindByApp(appName string) ([]models.User, error) {
	var users []models.User
	if err := r.db.Where("app_name = ?", appName).Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// FindNewUsersWithoutActiveSubscription retrieves users created within the last N days
// who have push notification tokens but do NOT have an active subscription or recurring payment.
// Uses NOT EXISTS subqueries to match the combined status logic from GetCombinedStatus:
//   - No subscription with status IN ('active', 'authenticated') (matched by phone)
//   - No recurring payment with status = 'active' (matched by user_id)
func (r *userRepository) FindNewUsersWithoutActiveSubscription(appName string, days int) ([]models.User, error) {
	var users []models.User
	since := time.Now().AddDate(0, 0, -days)

	err := r.db.Where(
		"app_name = ? AND created_at >= ? AND metadata ? 'push_notification_token'",
		appName, since,
	).
		Where(
			"NOT EXISTS (SELECT 1 FROM subscriptions WHERE subscriptions.phone = users.phone AND subscriptions.app_name = ? AND subscriptions.status IN ('active', 'authenticated') AND subscriptions.deleted_at IS NULL)",
			appName,
		).
		Where(
			"NOT EXISTS (SELECT 1 FROM recurring_payments WHERE recurring_payments.user_id = users.id AND recurring_payments.app_name = ? AND recurring_payments.status = 'active' AND recurring_payments.deleted_at IS NULL)",
			appName,
		).
		Find(&users).Error

	if err != nil {
		return nil, err
	}
	return users, nil
}
