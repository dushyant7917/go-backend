package database

import (
	"fmt"
	"log"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// PoolProfile defines connection pool settings for different workload patterns.
type PoolProfile struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// ServerPoolProfile is optimized for the web server:
//   - Many short-lived requests, low latency requirements
//   - Keep idle connections warm for fast API response
//   - MaxOpenConns should cover peak concurrent API requests
//   - MaxIdleConns should match MaxOpenConns to avoid cold-start latency
var ServerPoolProfile = PoolProfile{
	MaxOpenConns:    20,
	MaxIdleConns:    20,
	ConnMaxLifetime: 10 * time.Minute,
	ConnMaxIdleTime: 10 * time.Minute,
}

// CronPoolProfile is optimized for cron jobs:
//   - MaxOpenConns matches max goroutines (20) to prevent pool exhaustion
//   - MaxIdleConns is low (2) because the process exits after the job finishes
//   - ConnMaxIdleTime is short to free PostgreSQL resources quickly
var CronPoolProfile = PoolProfile{
	MaxOpenConns:    15,
	MaxIdleConns:    2,
	ConnMaxLifetime: 10 * time.Minute,
	ConnMaxIdleTime: 1 * time.Minute,
}

// Config holds database configuration
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// NewConnection creates a connection for the web server (ServerPoolProfile).
func NewConnection(config Config) (*gorm.DB, error) {
	return newConnectionWithProfile(config, ServerPoolProfile)
}

// NewCronConnection creates a connection for cron jobs (CronPoolProfile).
func NewCronConnection(config Config) (*gorm.DB, error) {
	return newConnectionWithProfile(config, CronPoolProfile)
}

func newConnectionWithProfile(config Config, profile PoolProfile) (*gorm.DB, error) {
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

	sqlDB.SetMaxOpenConns(profile.MaxOpenConns)
	sqlDB.SetMaxIdleConns(profile.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(profile.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(profile.ConnMaxIdleTime)

	log.Printf("Database connection established (pool: open=%d idle=%d lifetime=%s idleTime=%s)",
		profile.MaxOpenConns, profile.MaxIdleConns, profile.ConnMaxLifetime, profile.ConnMaxIdleTime)
	return db, nil
}
