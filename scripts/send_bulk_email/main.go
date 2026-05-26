package main

import (
	"crypto/tls"
	"encoding/csv"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

const maxEmails = 450

// Edit the subject and body below to set your email content.
// {{name}} is replaced with each recipient's name.
const emailSubject = "हर न्यूज़ रिपोर्टर अब Daily Story app इस्तेमाल कर रहा है!"

const emailBody = `
<!DOCTYPE html>
<html lang="hi">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Daily Story App</title>
</head>

<body style="margin:0; padding:0; background:#111111; font-family:Arial, sans-serif;">

<table width="100%" cellpadding="0" cellspacing="0" style="background:#111111; padding:40px 0;">
<tr>
<td align="center">

<table width="600" cellpadding="0" cellspacing="0" style="background:#1a1a1a; border-radius:14px; overflow:hidden; border:1px solid #FFD700;">

<!-- HEADER -->
<tr>
<td style="background:#000000; padding:25px; text-align:center; border-bottom:2px solid #FFD700;">
<h1 style="color:#FFD700; margin:0; font-size:30px; letter-spacing:1px;">
🚨 Daily Story
</h1>
</td>
</tr>

<!-- CONTENT -->
<tr>
<td style="padding:40px; color:#f5f5f5; font-size:18px; line-height:1.8;">

<p style="margin-top:0; color:#FFD700;">
नमस्कार {{name}} जी,
</p>

<p style="font-size:24px; font-weight:bold; color:#ffffff;">
हर न्यूज़ रिपोर्टर अब Daily Story app इस्तेमाल कर रहा है!
</p>

<p>
Daily Story app से मिनटों में बनाइए 
<strong style="color:#FFD700;">Professional</strong> समाचार वाले पोस्टर, 
आपके खुद के फोटो और नाम के साथ 🔥
</p>

<p>
14 राज्यों के रिपोर्टर रोज़ इसी ऐप से वायरल पोस्ट डाल रहे हैं…<br>
अगर आप अभी भी पुराने तरीके से news share कर रहे हैं, 
तो आप पीछे छूट सकते हैं!
</p>

<!-- FEATURES BOX -->
<div style="
background:#0f0f0f;
border:1px solid #FFD700;
border-radius:10px;
padding:22px;
margin:30px 0;
">

<p style="margin:10px 0; color:#ffffff;">✅ Daily ready-made posters</p>
<p style="margin:10px 0; color:#ffffff;">✅ अपना खुद का पोस्टर बनाएं</p>
<p style="margin:10px 0; color:#ffffff;">✅ Breaking News style design</p>
<p style="margin:10px 0; color:#ffffff;">✅ सिर्फ मोबाइल से</p>

</div>

<!-- BUTTON -->
<div style="text-align:center; margin:40px 0;">

<a href="https://www.dailystory.in"
style="
background:#000000;
color:#ffffff;
border:2px solid #ffffff;
padding:16px 34px;
text-decoration:none;
border-radius:8px;
font-size:18px;
font-weight:bold;
display:inline-block;
">
<span style="color:#ffffff !important; -webkit-text-fill-color:#ffffff;">TRY NOW</span>
</a>

</div>

<p style="margin-bottom:0;">
धन्यवाद,<br><br>

<strong style="color:#FFD700;">Devesh</strong><br>
Daily Story App
</p>

</td>
</tr>

</table>

</td>
</tr>
</table>

</body>
</html>
`

type UserRecord struct {
	Name      string
	Email     string
	EmailSent bool
	rowIndex  int // original row position for ordered CSV output
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/send_bulk_email/main.go <input.csv> <output.csv>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Required env vars:")
		fmt.Fprintln(os.Stderr, "  GMAIL_FROM        sender email address")
		fmt.Fprintln(os.Stderr, "  GMAIL_APP_PASSWORD Gmail app password (not your account password)")
		fmt.Fprintln(os.Stderr, "  GMAIL_DISPLAY_NAME (optional) sender display name")
		fmt.Fprintln(os.Stderr, "  GMAIL_REPLY_TO    (optional) reply-to email address")
		os.Exit(1)
	}

	inputCSV := os.Args[1]
	outputCSV := os.Args[2]

	gmailFrom := mustEnv("GMAIL_FROM")
	gmailAppPassword := mustEnv("GMAIL_APP_PASSWORD")
	displayName := os.Getenv("GMAIL_DISPLAY_NAME")
	if displayName == "" {
		displayName = gmailFrom
	}
	replyTo := os.Getenv("GMAIL_REPLY_TO")

	log.Printf("GMAIL_FROM=%s", gmailFrom)
	log.Printf("GMAIL_APP_PASSWORD=%s", gmailAppPassword)
	log.Printf("GMAIL_DISPLAY_NAME=%s", displayName)
	log.Printf("GMAIL_REPLY_TO=%s", replyTo)
	log.Printf("input=%s output=%s", inputCSV, outputCSV)

	// Read CSV and build map (all rows stored; only unsent ones are targeted)
	allRows, userMap, err := loadCSV(inputCSV)
	if err != nil {
		log.Fatalf("failed to read CSV: %v", err)
	}
	log.Printf("loaded %d total rows, %d pending sends", len(allRows)-1, len(userMap))

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	sent := 0
	for _, record := range userMap {
		if sent >= maxEmails {
			log.Printf("reached %d email limit, stopping", maxEmails)
			break
		}

		subject := strings.ReplaceAll(emailSubject, "{{name}}", record.Name)
		body := strings.ReplaceAll(emailBody, "{{name}}", record.Name)

		err := sendEmail(gmailFrom, displayName, gmailAppPassword, replyTo, record.Email, subject, body)
		if err != nil {
			log.Printf("FAILED  [%s] <%s>: %v", record.Name, record.Email, err)
		} else {
			record.EmailSent = true
			sent++
			log.Printf("SENT %d [%s] <%s>", sent, record.Name, record.Email)
		}

		// Random delay 10–20 seconds between sends
		if sent < maxEmails {
			delay := time.Duration(10+rng.Intn(11)) * time.Second
			log.Printf("waiting %s before next send...", delay)
			time.Sleep(delay)
		}
	}

	log.Printf("done — sent %d emails", sent)

	if err := writeCSV(outputCSV, allRows, userMap); err != nil {
		log.Fatalf("failed to write output CSV: %v", err)
	}
	log.Printf("results written to %s", outputCSV)
}

// loadCSV reads the CSV file. Returns all raw rows (for output) and a map of
// email → *UserRecord for rows where Email_Sent is blank or "false".
func loadCSV(path string) ([][]string, map[string]*UserRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	allRows, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(allRows) < 2 {
		return allRows, map[string]*UserRecord{}, nil
	}

	// Normalise header names → column indices
	header := allRows[0]
	colIdx := map[string]int{}
	for i, h := range header {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	nameCol, okN := colIdx["name"]
	emailCol, okE := colIdx["email"]
	sentCol, okS := colIdx["email_sent"]
	if !okN || !okE || !okS {
		return nil, nil, fmt.Errorf("CSV must have Name, Email, Email_Sent columns (got: %v)", header)
	}

	userMap := make(map[string]*UserRecord, len(allRows)-1)
	for i, row := range allRows[1:] {
		if len(row) <= emailCol {
			continue
		}
		sentVal := strings.TrimSpace(strings.ToLower(row[sentCol]))
		if sentVal != "" && sentVal != "false" {
			continue // already sent — skip
		}
		email := strings.TrimSpace(row[emailCol])
		if email == "" {
			continue
		}
		name := strings.TrimSpace(row[nameCol])
		if name == "" {
			name = email
		}
		userMap[email] = &UserRecord{
			Name:      name,
			Email:     email,
			EmailSent: false,
			rowIndex:  i + 1, // 1-based offset into allRows (skipping header)
		}
	}
	return allRows, userMap, nil
}

// writeCSV writes all original rows to outputPath, updating Email_Sent for
// records present in userMap.
func writeCSV(path string, allRows [][]string, userMap map[string]*UserRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Build a lookup: rowIndex → record (so we can patch the right rows)
	byRow := make(map[int]*UserRecord, len(userMap))
	for _, rec := range userMap {
		byRow[rec.rowIndex] = rec
	}

	header := allRows[0]
	colIdx := map[string]int{}
	for i, h := range header {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	sentCol := colIdx["email_sent"]

	w := csv.NewWriter(f)
	_ = w.Write(header)

	for i, row := range allRows[1:] {
		rowNum := i + 1
		if rec, ok := byRow[rowNum]; ok && rec.EmailSent {
			// Patch Email_Sent column in a copy of the row
			patched := make([]string, len(row))
			copy(patched, row)
			if len(patched) > sentCol {
				patched[sentCol] = "true"
			}
			_ = w.Write(patched)
		} else {
			_ = w.Write(row)
		}
	}

	w.Flush()
	return w.Error()
}

// sendEmail sends an HTML email via Gmail SMTP using an App Password.
func sendEmail(from, displayName, appPassword, replyTo, to, subject, htmlBody string) error {
	fromHeader := fmt.Sprintf("%s <%s>", displayName, from)
	msg := buildMIMEMessage(fromHeader, to, replyTo, subject, htmlBody)
	return smtpSendTLS(from, appPassword, to, msg)
}

// smtpSendTLS dials smtp.gmail.com:465 (implicit TLS) and sends the message.
// Using port 465 avoids the STARTTLS upgrade step and is slightly more robust.
func smtpSendTLS(from, appPassword, to string, msg []byte) error {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         "smtp.gmail.com",
	}

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

	auth := smtp.PlainAuth("", from, appPassword, "smtp.gmail.com")
	if err = c.Auth(auth); err != nil {
		return err
	}
	if err = c.Mail(from); err != nil {
		return err
	}
	if err = c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	_, err = wc.Write(msg)
	if err != nil {
		return err
	}
	return wc.Close()
}

// smtpSendSTARTTLS sends via port 587 with STARTTLS upgrade.
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

	tlsCfg := &tls.Config{ServerName: host}
	if err = c.StartTLS(tlsCfg); err != nil {
		return err
	}

	auth := smtp.PlainAuth("", from, appPassword, host)
	if err = c.Auth(auth); err != nil {
		return err
	}
	if err = c.Mail(from); err != nil {
		return err
	}
	if err = c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	_, err = wc.Write(msg)
	if err != nil {
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
