package database

import (
	"fmt"
	"log"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Config holds database configuration
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// NewConnection creates a new database connection
func NewConnection(config Config) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		config.Host,
		config.Port,
		config.User,
		config.Password,
		config.DBName,
		config.SSLMode,
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Pool configuration to prevent connection exhaustion and hangs
	// MaxOpenConns: limits total connections to prevent overwhelming PostgreSQL
	sqlDB.SetMaxOpenConns(25)
	// MaxIdleConns: keep enough idle connections to avoid reconnect churn
	sqlDB.SetMaxIdleConns(10)
	// ConnMaxLifetime: recycle connections to prevent stale TCP connections
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	// ConnMaxIdleTime: close idle connections to free resources
	sqlDB.SetConnMaxIdleTime(1 * time.Minute)

	log.Println("Database connection established successfully")
	return db, nil
}
