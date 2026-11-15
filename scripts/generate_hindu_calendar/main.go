// generate_hindu_calendar turns the Hindu calendar CSV into the single embedded data file
// internal/apps/sanskaar/hinducalendar/data/hindu_events.json and mirrors the event icons into
// the Sanskaar R2 bucket.
//
// Usage, from the repo root:
//
//	go run ./scripts/generate_hindu_calendar -input <csv> [-mode merge|replace] [-dry-run] [-skip-icons] [-icons-only] [-yes]
//
// -mode merge (the default) replaces the CSV's date range [min, max] and keeps every row outside
// it, so adding a new year appends it and re-running a corrected CSV replaces just that range.
// -mode replace makes the file exactly the CSV's rows.
//
// -dry-run validates and prints the report without writing or uploading anything. -skip-icons
// writes the JSON only; -icons-only uploads icons only and leaves the JSON alone.
//
// The JSON does not depend on the environment, but icons must exist in each environment's R2
// bucket, so run the icon step once per environment: GO_ENV=local ... -icons-only, then
// GO_ENV=prod ... -icons-only (GO_ENV selects .env.$GO_ENV like make migrate-up; prod asks for
// confirmation unless -yes).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	r2ConfigRepository "go-backend/internal/apps/r2/config/repository"
	r2ConfigService "go-backend/internal/apps/r2/config/service"
	hc "go-backend/internal/apps/sanskaar/hinducalendar"
	"go-backend/internal/common/constants"
	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
)

const defaultOut = "internal/apps/sanskaar/hinducalendar/data/hindu_events.json"

func main() {
	input := flag.String("input", "", "path to the source CSV (required)")
	mode := flag.String("mode", "merge", "merge (replace the CSV's date range, keep the rest) or replace (file = CSV only)")
	out := flag.String("out", defaultOut, "data file to write (leave the default except in tests)")
	dryRun := flag.Bool("dry-run", false, "validate and print the report, write nothing")
	skipIcons := flag.Bool("skip-icons", false, "write the JSON only, do not upload icons")
	iconsOnly := flag.Bool("icons-only", false, "upload icons only, do not touch the JSON")
	yes := flag.Bool("yes", false, "skip the confirmation prompt when GO_ENV=prod")
	flag.Parse()

	if *input == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *skipIcons && *iconsOnly {
		log.Fatal("-skip-icons and -icons-only cannot be combined")
	}

	f, err := os.Open(*input)
	if err != nil {
		log.Fatalf("open input: %v", err)
	}
	rows, warnings, err := ReadCSV(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	for _, w := range warnings {
		fmt.Println("warning:", w)
	}

	events, report, err := BuildEvents(rows)
	if err != nil {
		log.Fatalf("CSV is not valid:\n  %v", err)
	}
	printReport(report)

	if !*iconsOnly {
		var existing []hc.Event
		if *mode == "merge" {
			existing, err = readExisting(*out)
			if err != nil {
				log.Fatal(err)
			}
		}
		merged, err := Merge(existing, events, *mode)
		if err != nil {
			log.Fatalf("merge: %v", err)
		}
		data, err := hc.Marshal(merged)
		if err != nil {
			log.Fatal(err)
		}
		// Make sure the server would accept exactly what we are about to write.
		if _, err := hc.Load(data); err != nil {
			log.Fatalf("generated file would not load: %v", err)
		}
		fmt.Printf("%s: %d events (%s..%s)\n", *out, len(merged), merged[0].Date, merged[len(merged)-1].Date)

		if *dryRun {
			fmt.Println("dry-run: nothing written")
			return
		}
		if old, err := os.ReadFile(*out); err == nil && bytes.Equal(old, data) {
			fmt.Println("data file already up to date")
		} else {
			if err := os.WriteFile(*out, data, 0o644); err != nil {
				log.Fatalf("write %s: %v", *out, err)
			}
			fmt.Println("data file written")
		}
	} else if *dryRun {
		fmt.Println("dry-run: nothing uploaded")
		return
	}

	if *skipIcons || *dryRun {
		return
	}
	if err := uploadIcons(report.Icons, *yes); err != nil {
		log.Fatalf("icons: %v", err)
	}
}

func readExisting(path string) ([]hc.Event, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []hc.Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("existing %s is not valid JSON: %w", path, err)
	}
	return events, nil
}

func printReport(r *Report) {
	fmt.Printf("CSV rows: %d, events: %d, rows without tithi: %d, distinct icons: %d\n", r.Rows, r.Events, r.WithoutTithi, len(r.Icons))
	for _, w := range r.Warnings {
		fmt.Println("warning:", w)
	}
}

// uploadIcons mirrors every icon into the Sanskaar R2 bucket of the environment selected by
// GO_ENV. Keys are deterministic (hindu-events/icons/<file>), so re-running skips existing files.
func uploadIcons(icons map[string]string, yes bool) error {
	env := utils.GetEnv("GO_ENV", "local")
	if err := godotenv.Load(".env." + env); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No .env.%s or .env file found, using environment variables", env)
		}
	}
	bucket := utils.GetEnv("R2_SANSKAAR_BUCKET_NAME", "")
	if bucket == "" {
		return errors.New("R2_SANSKAAR_BUCKET_NAME environment variable is required")
	}

	if env == "prod" && !yes {
		fmt.Printf("About to upload %d icons to R2 bucket %q (GO_ENV=prod, DB %s/%s).\nType \"prod\" to continue: ",
			len(icons), bucket, utils.GetEnv("DB_HOST", "localhost"), utils.GetEnv("DB_NAME", "gobackend"))
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(answer) != "prod" {
			return errors.New("aborted")
		}
	}

	// The DB is only used to read the per-app R2 credentials.
	db, err := database.NewCronConnection(database.Config{
		Host:     utils.GetEnv("DB_HOST", "localhost"),
		Port:     utils.GetEnv("DB_PORT", "5432"),
		User:     utils.GetEnv("DB_USER", "postgres"),
		Password: utils.GetEnv("DB_PASSWORD", ""),
		DBName:   utils.GetEnv("DB_NAME", "gobackend"),
		SSLMode:  utils.GetEnv("DB_SSL_MODE", "disable"),
	})
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	factory := r2ConfigService.NewR2ClientFactory(r2ConfigRepository.NewR2ConfigRepository(db))
	client, err := factory.GetClient(constants.AppNameSanskaar)
	if err != nil {
		return fmt.Errorf("initialize R2 client: %w", err)
	}

	keys := make([]string, 0, len(icons))
	for k := range icons {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	httpClient := &http.Client{Timeout: 60 * time.Second}
	uploaded, skipped := 0, 0
	for _, key := range keys {
		exists, err := client.FileExists(bucket, key)
		if err != nil {
			return fmt.Errorf("check %s: %w", key, err)
		}
		if exists {
			skipped++
			continue
		}
		if _, err := client.UploadFileFromURL(bucket, key, icons[key], httpClient); err != nil {
			return fmt.Errorf("upload %s from %s: %w", key, icons[key], err)
		}
		uploaded++
		fmt.Println("uploaded", key)
	}
	fmt.Printf("icons: %d uploaded, %d already present in %q\n", uploaded, skipped, bucket)
	return nil
}
