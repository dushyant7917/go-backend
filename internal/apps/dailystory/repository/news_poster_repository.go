package repository

import (
	"go-backend/internal/apps/dailystory/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NewsPosterRepository defines the interface for news poster data operations
type NewsPosterRepository interface {
	Create(newsPoster *models.NewsPoster) error
	FindByID(id uuid.UUID) (*models.NewsPoster, error)
	FindByNewsIDAndUserID(newsID, userID uuid.UUID) (*models.NewsPoster, error)
	FindPrefillData(newsID, userID uuid.UUID) (*models.NewsPosterPrefillRow, error)
}

// newsPosterRepository implements NewsPosterRepository
type newsPosterRepository struct {
	db *gorm.DB
}

// NewNewsPosterRepository creates a new instance of NewsPosterRepository
func NewNewsPosterRepository(db *gorm.DB) NewsPosterRepository {
	return &newsPosterRepository{db: db}
}

// Create creates a new news poster in the database
func (r *newsPosterRepository) Create(newsPoster *models.NewsPoster) error {
	return r.db.Create(newsPoster).Error
}

// FindByID retrieves a news poster by ID
func (r *newsPosterRepository) FindByID(id uuid.UUID) (*models.NewsPoster, error) {
	var newsPoster models.NewsPoster
	if err := r.db.Where("id = ?", id).First(&newsPoster).Error; err != nil {
		return nil, err
	}
	return &newsPoster, nil
}

// FindByNewsIDAndUserID retrieves a news poster by news_id and user_id
func (r *newsPosterRepository) FindByNewsIDAndUserID(newsID, userID uuid.UUID) (*models.NewsPoster, error) {
	var newsPoster models.NewsPoster
	if err := r.db.Where("news_id = ? AND user_id = ?", newsID, userID).First(&newsPoster).Error; err != nil {
		return nil, err
	}
	return &newsPoster, nil
}

// FindPrefillData fetches user info and news translation in a single JOIN query for poster prefill.
func (r *newsPosterRepository) FindPrefillData(newsID, userID uuid.UUID) (*models.NewsPosterPrefillRow, error) {
	const query = `
		SELECT
			u.name                                  AS user_name,
			u.phone                                 AS user_phone,
			u.metadata->>'detail'                   AS user_detail,
			(u.metadata->>'display_phone')::boolean AS display_phone,
			u.metadata->>'profile_picture_key'      AS profile_picture_key,
			u.metadata->>'language_code'            AS user_language_code,
			(n.id IS NOT NULL)                      AS news_exists,
			nt.title                                AS news_title,
			nt.summary                              AS news_summary,
			n.media_file_key                        AS media_file_key
		FROM users u
		LEFT JOIN news_translations nt
			ON  nt.news_id       = @news_id
			AND nt.language_code = u.metadata->>'language_code'
		LEFT JOIN news n ON n.id = @news_id
		WHERE u.id       = @user_id
		  AND u.app_name = 'DailyStoryApp'
		LIMIT 1`

	var row models.NewsPosterPrefillRow
	tx := r.db.Raw(query, map[string]interface{}{
		"news_id": newsID,
		"user_id": userID,
	}).Scan(&row)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}
