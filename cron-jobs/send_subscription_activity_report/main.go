package main

import (
	"crypto/tls"
	"fmt"
	"html"
	"log"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"

	"go-backend/internal/common/constants"
	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
)

// reportRecipient is the inbox that receives both daily report emails.
const reportRecipient = "devesh.product.launchpad@gmail.com"

// cancelledRow is a single row of the "cancelled subscriptions" report.
type cancelledRow struct {
	Phone          *string
	CountryCode    *string
	PaidFirstCycle bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// orphanOTPRow is a single row of the "OTP without signup" report.
type orphanOTPRow struct {
	Phone       string
	CountryCode string
}

func main() {
	// 1. Load environment variables (.env.<GO_ENV> then .env fallback)
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	ts := func() string { return time.Now().Format("2006-01-02 15:04:05") }
	log.Printf("[%s] Starting subscription activity report (app=%s)\n", ts(), constants.AppNameDailyStory)

	// Validate email credentials up front so we fail fast before doing DB work.
	gmailFrom := mustEnv("GMAIL_FROM")
	gmailAppPassword := mustEnv("GMAIL_APP_PASSWORD")
	displayName := utils.GetEnv("GMAIL_DISPLAY_NAME", "DailyStory Reports")
	replyTo := utils.GetEnv("GMAIL_REPLY_TO", gmailFrom)

	// 2. Connect to the database with the cron pool profile
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
		log.Fatalf("[%s] ✗ Failed to connect to database: %v\n", ts(), err)
	}
	log.Printf("[%s] ✓ Database connected\n", ts())

	// IST for human-readable timestamps in the emails.
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.UTC
	}

	// 3. Query 1 — subscriptions cancelled in the last 20 hours
	var cancelled []cancelledRow
	cancelledQuery := `
		SELECT
			u.phone        AS phone,
			u.country_code AS country_code,
			EXISTS (
				SELECT 1 FROM billing_cycles bc
				WHERE bc.recurring_payment_id = rp.id
				  AND bc.cycle_number = 1
				  AND bc.status = 'paid'
			)              AS paid_first_cycle,
			rp.created_at  AS created_at,
			rp.updated_at  AS updated_at
		FROM recurring_payments rp
		JOIN users u ON u.id = rp.user_id
		WHERE rp.app_name = ?
		  AND rp.status   = 'cancelled'
		  AND rp.updated_at >= NOW() - INTERVAL '20 hours'
		  AND rp.deleted_at IS NULL
		ORDER BY rp.updated_at DESC`
	if err := db.Raw(cancelledQuery, constants.AppNameDailyStory).Scan(&cancelled).Error; err != nil {
		log.Fatalf("[%s] ✗ Failed to query cancelled subscriptions: %v\n", ts(), err)
	}
	log.Printf("[%s] ✓ Cancelled subscriptions found: %d\n", ts(), len(cancelled))

	// 4. Query 2 — OTP requests (last 20h → last 1h) with no matching user
	var orphans []orphanOTPRow
	orphanQuery := `
		SELECT po.phone AS phone, po.country_code AS country_code
		FROM phone_otp po
		LEFT JOIN users u
		       ON u.phone       = po.phone
		      AND u.country_code = po.country_code
		      AND u.app_name     = po.app_name
		      AND u.deleted_at  IS NULL
		WHERE po.app_name = ?
		  AND po.created_at >= NOW() - INTERVAL '20 hours'
		  AND po.created_at <= NOW() - INTERVAL '1 hour'
		  AND u.id IS NULL
		ORDER BY po.created_at DESC`
	if err := db.Raw(orphanQuery, constants.AppNameDailyStory).Scan(&orphans).Error; err != nil {
		log.Fatalf("[%s] ✗ Failed to query orphan OTP requests: %v\n", ts(), err)
	}
	log.Printf("[%s] ✓ OTP requests without signup found: %d\n", ts(), len(orphans))

	// 5. Build and send both emails. Attempt both, then fail if either errored.
	dateLabel := time.Now().In(loc).Format("2006-01-02")
	var sendErr bool

	// send delivers one report email and records the outcome.
	send := func(label, subject, body string, rows int) {
		if err := sendEmail(gmailFrom, displayName, gmailAppPassword, replyTo, reportRecipient, subject, body); err != nil {
			log.Printf("[%s] ✗ Failed to send %s email: %v\n", ts(), label, err)
			sendErr = true
			return
		}
		log.Printf("[%s] ✓ Sent %s email (%d rows)\n", ts(), label, rows)
	}

	send("cancellations",
		fmt.Sprintf("DailyStory - Cancellations (last 20h) - %s", dateLabel),
		buildCancelledEmail(cancelled, loc), len(cancelled))
	send("orphan-OTP",
		fmt.Sprintf("DailyStory - OTP requests without signup - %s", dateLabel),
		buildOrphanEmail(orphans), len(orphans))

	if sendErr {
		log.Fatalf("[%s] ✗ Completed with email send failures\n", ts())
	}
	log.Printf("[%s] ✓ Completed successfully\n", ts())
}

// ==================== Email body builders ====================

func buildCancelledEmail(rows []cancelledRow, loc *time.Location) string {
	if len(rows) == 0 {
		return plainEmail("Subscriptions cancelled in the last 20 hours",
			"No subscriptions were cancelled in the last 20 hours.")
	}

	headers := []string{
		"User Phone", "Paid 1st Cycle",
		"Subscription Created", "Cancelled", "Days Active",
	}

	var paidCount, unpaidCount, within3Count, after3Count int
	cells := make([][]cell, 0, len(rows))
	for _, r := range rows {
		lifetime := r.UpdatedAt.Sub(r.CreatedAt)
		days := int(lifetime.Hours() / 24)

		paid := "No"
		if r.PaidFirstCycle {
			paid = "Yes"
			paidCount++
		} else {
			unpaidCount++
		}
		if lifetime <= 3*24*time.Hour {
			within3Count++
		} else {
			after3Count++
		}

		cells = append(cells, []cell{
			phoneCell(deref(r.CountryCode), deref(r.Phone)),
			textCell(paid),
			textCell(r.CreatedAt.In(loc).Format("2006-01-02 15:04")),
			textCell(r.UpdatedAt.In(loc).Format("2006-01-02 15:04")),
			textCell(fmt.Sprintf("%d", days)),
		})
	}

	summary := []string{
		fmt.Sprintf("Total cancellations: %d", len(rows)),
		fmt.Sprintf("Paid 1st cycle: %d", paidCount),
		fmt.Sprintf("Unpaid 1st cycle: %d", unpaidCount),
		fmt.Sprintf("Cancelled within 3 days: %d", within3Count),
		fmt.Sprintf("Cancelled after 3 days: %d", after3Count),
	}

	return htmlDocument(
		"Subscriptions cancelled in the last 20 hours",
		summary,
		headers, cells,
	)
}

func buildOrphanEmail(rows []orphanOTPRow) string {
	if len(rows) == 0 {
		return plainEmail("OTP requests without signup (20h–1h ago)",
			"No OTP requests without signup were found in this window.")
	}

	headers := []string{"Phone"}
	cells := make([][]cell, 0, len(rows))
	for _, r := range rows {
		cells = append(cells, []cell{phoneCell(r.CountryCode, r.Phone)})
	}

	return htmlDocument(
		"OTP requests without signup (20h–1h ago)",
		[]string{fmt.Sprintf("%d phone number(s)", len(rows))},
		headers, cells,
	)
}

// ==================== HTML rendering ====================

// htmlPage wraps inner HTML in a styled document with a heading.
func htmlPage(heading, inner string) string {
	return `<!DOCTYPE html><html><head><meta charset="UTF-8"></head>` +
		`<body style="margin:0;padding:24px;background:#f4f4f5;font-family:Arial,sans-serif;color:#1a1a1a;">` +
		`<h2 style="margin:0 0 8px;">` + html.EscapeString(heading) + `</h2>` +
		inner +
		`</body></html>`
}

// cell is a single table cell. html holds the already-safe HTML to render.
type cell struct {
	html string
}

// textCell renders an escaped plain-text cell.
func textCell(s string) cell {
	return cell{html: html.EscapeString(s)}
}

// phoneCell renders a phone number as a tel: link (easy to copy/tap) plus a
// WhatsApp link. countryCode is combined with the number for the wa.me/tel
// targets; the visible text stays the original phone value.
func phoneCell(countryCode, phone string) cell {
	if phone == "" {
		return textCell("")
	}
	intl := onlyDigits(countryCode) + onlyDigits(phone)
	return cell{html: `<a href="tel:+` + intl +
		`" style="color:#1a1a1a;text-decoration:none;font-weight:bold;">` + html.EscapeString(phone) + `</a>` +
		`<br><a href="https://wa.me/` + intl +
		`" style="display:inline-block;margin-top:14px;padding:5px 12px;background:#25D366;color:#ffffff;` +
		`text-decoration:none;font-size:13px;font-weight:bold;border-radius:6px;">WhatsApp</a>`}
}

// onlyDigits strips everything except 0-9 from s.
func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// htmlDocument renders a minimal HTML email with a heading and a table.
// Each entry in summary is rendered on its own line above the table.
func htmlDocument(heading string, summary []string, headers []string, rows [][]cell) string {
	var b strings.Builder
	b.WriteString(`<div style="margin:0 0 16px;color:#555;">`)
	for _, line := range summary {
		b.WriteString(`<div>` + html.EscapeString(line) + `</div>`)
	}
	b.WriteString(`</div>`)

	b.WriteString(`<table cellpadding="8" cellspacing="0" style="border-collapse:collapse;background:#fff;border:1px solid #ddd;">`)
	b.WriteString(`<tr style="background:#111;color:#FFD700;text-align:left;">`)
	for _, h := range headers {
		b.WriteString(`<th style="border:1px solid #333;padding:8px;">` + html.EscapeString(h) + `</th>`)
	}
	b.WriteString(`</tr>`)
	for i, row := range rows {
		bg := "#ffffff"
		if i%2 == 1 {
			bg = "#f9f9f9"
		}
		b.WriteString(`<tr style="background:` + bg + `;">`)
		for _, c := range row {
			b.WriteString(`<td style="border:1px solid #ddd;padding:8px;white-space:nowrap;">` + c.html + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table>`)

	return htmlPage(heading, b.String())
}

// plainEmail renders a simple message body (no table) for empty result sets.
func plainEmail(heading, message string) string {
	return htmlPage(heading, `<p style="margin:0;color:#555;">`+html.EscapeString(message)+`</p>`)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ==================== Email sending (Gmail SMTP) ====================
// Mirrors scripts/send_bulk_email/main.go: port 465 implicit TLS with a
// 587 STARTTLS fallback, sending an HTML body.

func sendEmail(from, displayName, appPassword, replyTo, to, subject, htmlBody string) error {
	fromHeader := fmt.Sprintf("%s <%s>", displayName, from)
	msg := buildMIMEMessage(fromHeader, to, replyTo, subject, htmlBody)
	return smtpSendTLS(from, appPassword, to, msg)
}

func smtpSendTLS(from, appPassword, to string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: "smtp.gmail.com"}

	conn, err := tls.Dial("tcp", "smtp.gmail.com:465", tlsCfg)
	if err != nil {
		// Fallback to port 587 STARTTLS
		return smtpSendSTARTTLS(from, appPassword, to, msg)
	}

	c, err := smtp.NewClient(conn, "smtp.gmail.com")
	if err != nil {
		return err
	}
	defer c.Quit()

	return deliver(c, "smtp.gmail.com", from, appPassword, to, msg)
}

func smtpSendSTARTTLS(from, appPassword, to string, msg []byte) error {
	host := "smtp.gmail.com"
	port := "587"

	conn, err := net.Dial("tcp", host+":"+port)
	if err != nil {
		return err
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Quit()

	if err = c.StartTLS(&tls.Config{ServerName: host}); err != nil {
		return err
	}

	return deliver(c, host, from, appPassword, to, msg)
}

// deliver authenticates and writes a single message over an established client.
func deliver(c *smtp.Client, host, from, appPassword, to string, msg []byte) error {
	if err := c.Auth(smtp.PlainAuth("", from, appPassword, host)); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		return err
	}
	return wc.Close()
}

func buildMIMEMessage(from, to, replyTo, subject, htmlBody string) []byte {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	if replyTo != "" {
		sb.WriteString("Reply-To: " + replyTo + "\r\n")
	}
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(htmlBody)
	return []byte(sb.String())
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
