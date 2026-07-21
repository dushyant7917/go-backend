package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"

	"go-backend/internal/apps/dailystory/repository"
	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
)

func main() {
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	category := flag.String("category", "", "news category to look up (required)")
	subCategory := flag.String("sub-category", "", "news sub-category filter (optional)")
	languageCode := flag.String("language-code", "hi", "language code for title/summary translation")
	outputDir := flag.String("output-dir", "", "directory to write the rendered video into (optional, render script default is used if omitted)")
	outputFile := flag.String("output-file", "", "filename for the rendered video (default: <category>_<news-id>.mp4)")
	flag.Parse()

	if *category == "" {
		log.Fatalf("flag -category is required")
	}

	renderScript := mustEnv("NEWS_REEL_RENDER_SCRIPT")
	publicURLBase := mustEnv("R2_DS_NEWS_PUBLIC_URL")

	dbConfig := database.Config{
		Host:     utils.GetEnv("DB_HOST", "localhost"),
		Port:     utils.GetEnv("DB_PORT", "5432"),
		User:     utils.GetEnv("DB_USER", "postgres"),
		Password: utils.GetEnv("DB_PASSWORD", ""),
		DBName:   utils.GetEnv("DB_NAME", "gobackend"),
		SSLMode:  utils.GetEnv("DB_SSL_MODE", "disable"),
	}
	db, err := database.NewCronConnection(dbConfig)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	newsRepo := repository.NewNewsRepository(db)
	results, _, err := newsRepo.FindAllPaginated(*category, *subCategory, *languageCode, "", nil, 1, 1)
	if err != nil {
		log.Fatalf("query latest news for category %q: %v", *category, err)
	}
	if len(results) == 0 {
		log.Fatalf("no news found for category=%q sub_category=%q language_code=%q", *category, *subCategory, *languageCode)
	}
	news := results[0]

	if news.Title == "" || news.Summary == "" {
		log.Fatalf("news %s has no %q translation (title/summary empty)", news.ID, *languageCode)
	}
	if news.MediaFileKey == nil || *news.MediaFileKey == "" {
		log.Fatalf("news %s has no media_file_key", news.ID)
	}
	mediaLink := publicURLBase + "/" + *news.MediaFileKey

	if *outputFile == "" {
		*outputFile = fmt.Sprintf("%s_%s.mp4", *category, news.ID)
	}

	log.Printf("news_id=%s title=%q media=%s", news.ID, news.Title, mediaLink)

	args := []string{
		"--title", news.Title,
		"--summary", news.Summary,
		"--image", mediaLink,
		"--output-file", *outputFile,
	}
	if *outputDir != "" {
		args = append(args, "--output-dir", *outputDir)
	}

	cmd := exec.Command(renderScript, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("render script failed: %v", err)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
