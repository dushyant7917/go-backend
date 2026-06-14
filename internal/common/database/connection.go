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
//   - MaxOpenConns covers peak concurrent queries with headroom for spikes
//     (current traffic uses only ~2-3 at a time)
//   - MaxIdleConns keeps a few connections warm for fast API response without
//     pinning idle backend slots on PostgreSQL
var ServerPoolProfile = PoolProfile{
	MaxOpenConns:    20,
	MaxIdleConns:    20,
	ConnMaxLifetime: 15 * time.Minute,
	ConnMaxIdleTime: 10 * time.Minute,
}

// CronPoolProfile is optimized for cron jobs:
//   - MaxOpenConns matches max goroutines (11) to prevent pool exhaustion
//   - MaxIdleConns is low (2) because the process exits after the job finishes
//   - ConnMaxIdleTime is short to free PostgreSQL resources quickly
var CronPoolProfile = PoolProfile{
	MaxOpenConns:    11,
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
	return newConnectionWithProfile(config, ServerPoolProfile, logger.Info)
}

// NewCronConnection creates a connection for cron jobs (CronPoolProfile).
// Uses Warn log level to suppress verbose SQL logs (e.g. vector embeddings in queries).
func NewCronConnection(config Config) (*gorm.DB, error) {
	return newConnectionWithProfile(config, CronPoolProfile, logger.Warn)
}

func newConnectionWithProfile(config Config, profile PoolProfile, logLevel logger.LogLevel) (*gorm.DB, error) {
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
		Logger: logger.Default.LogMode(logLevel),
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
