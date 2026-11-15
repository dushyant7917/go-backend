package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Opt-in DB test (needs ALL migrations applied, e.g. a scratch database migrated with goose):
//
//	AUTOPAY_FULL_IT_DSN="host=localhost port=5432 user=postgres password=postgres dbname=autopay_full_it sslmode=disable" go test go-backend/cron-jobs/send_subscription_activity_report
func TestCancelledQueryCoversAllGateways(t *testing.T) {
	dsn := os.Getenv("AUTOPAY_FULL_IT_DSN")
	if dsn == "" {
		t.Skip("AUTOPAY_FULL_IT_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}

	const app = "ReportItestApp"
	var users []uuid.UUID
	configs := map[string]uuid.UUID{}
	for _, gw := range []string{"cashfree", "phonepe"} {
		id := uuid.New()
		if err := db.Exec(`INSERT INTO payment_gateway_configs (id, gateway, app_name, environment) VALUES (?, ?, ?, ?)`, id, gw, app, "it"+id.String()[:8]).Error; err != nil {
			t.Fatal(err)
		}
		configs[gw] = id
	}
	mkUser := func() (uuid.UUID, string) {
		id := uuid.New()
		phone := "9" + strings.ReplaceAll(id.String(), "-", "")[:9]
		if err := db.Exec(`INSERT INTO users (id, app_name, email, phone, country_code) VALUES (?, ?, ?, ?, '+91')`,
			id, app, id.String()+"@example.com", phone).Error; err != nil {
			t.Fatal(err)
		}
		users = append(users, id)
		return id, phone
	}
	t.Cleanup(func() {
		for _, u := range users {
			db.Exec(`DELETE FROM autopay_mandates WHERE user_id = ?`, u)
			db.Exec(`DELETE FROM recurring_payments WHERE user_id = ?`, u)
			db.Exec(`DELETE FROM users WHERE id = ?`, u)
		}
		for _, c := range configs {
			db.Exec(`DELETE FROM payment_gateway_configs WHERE id = ?`, c)
		}
	})

	razorpay := func(user uuid.UUID, status string, updated time.Duration, paidCycle1 bool) {
		rp := uuid.New()
		if err := db.Exec(`INSERT INTO recurring_payments (id, user_id, app_name, status, max_amount, frequency, updated_at) VALUES (?, ?, ?, ?, 9900, 'monthly', now() - ?::interval)`,
			rp, user, app, status, updated.String()).Error; err != nil {
			t.Fatal(err)
		}
		if paidCycle1 {
			if err := db.Exec(`INSERT INTO billing_cycles (recurring_payment_id, cycle_number, start_at, amount, status) VALUES (?, 1, now(), 9900, 'paid')`, rp).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	autopay := func(gateway string, user uuid.UUID, status, paymentType string, updated time.Duration, paidCycle1 bool, deleted bool) {
		m := uuid.New()
		if err := db.Exec(`INSERT INTO autopay_mandates (id, user_id, app_name, gateway_config_id, payment_type, merchant_autopay_id, status, max_debit_amount, billing_frequency, updated_at, deleted_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 9900, 'monthly', now() - ?::interval, CASE WHEN ? THEN now() ELSE NULL END)`,
			m, user, app, configs[gateway], paymentType, "ap_"+m.String(), status, updated.String(), deleted).Error; err != nil {
			t.Fatal(err)
		}
		if paidCycle1 {
			if err := db.Exec(`INSERT INTO autopay_billing_cycles (autopay_mandate_id, cycle_number, start_at, amount, status) VALUES (?, 1, now(), 9900, 'paid')`, m).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	rzpUser, rzpPhone := mkUser()
	cfPaidUser, cfPaidPhone := mkUser()
	cfUnpaidUser, cfUnpaidPhone := mkUser()
	staleUser, stalePhone := mkUser()
	activeUser, activePhone := mkUser()
	deletedUser, deletedPhone := mkUser()
	oneTimeUser, oneTimePhone := mkUser()
	ppPaidUser, ppPaidPhone := mkUser()
	ppUnpaidUser, ppUnpaidPhone := mkUser()

	razorpay(rzpUser, "cancelled", time.Hour, true)
	autopay("cashfree", cfPaidUser, "cancelled", "recurring", 2*time.Hour, true, false)
	autopay("cashfree", cfUnpaidUser, "cancelled", "recurring", 3*time.Hour, false, false)
	autopay("cashfree", staleUser, "cancelled", "recurring", 30*time.Hour, true, false) // outside the 20h window
	autopay("cashfree", activeUser, "active", "recurring", time.Hour, true, false)      // not cancelled
	autopay("cashfree", deletedUser, "cancelled", "recurring", time.Hour, true, true)   // soft deleted
	autopay("cashfree", oneTimeUser, "cancelled", "one_time", time.Hour, true, false)   // one-time payments are not mandates
	autopay("phonepe", ppPaidUser, "cancelled", "recurring", 4*time.Hour, true, false)
	autopay("phonepe", ppUnpaidUser, "cancelled", "recurring", 5*time.Hour, false, false)

	var rows []cancelledRow
	if err := db.Raw(cancelledQuery, app, app).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}

	type got struct {
		gateway string
		paid    bool
	}
	byPhone := map[string]got{}
	for _, r := range rows {
		byPhone[deref(r.Phone)] = got{r.Gateway, r.PaidFirstCycle}
	}

	want := map[string]got{
		rzpPhone:      {"Razorpay", true},
		cfPaidPhone:   {"Cashfree", true},
		cfUnpaidPhone: {"Cashfree", false},
		ppPaidPhone:   {"PhonePe", true},
		ppUnpaidPhone: {"PhonePe", false},
	}
	for phone, w := range want {
		if g, ok := byPhone[phone]; !ok || g != w {
			t.Errorf("phone %s: got %+v (present=%v), want %+v", phone, g, ok, w)
		}
	}
	for _, phone := range []string{stalePhone, activePhone, deletedPhone, oneTimePhone} {
		if _, ok := byPhone[phone]; ok {
			t.Errorf("phone %s must not be reported", phone)
		}
	}
	if len(rows) != len(want) {
		t.Errorf("got %d rows, want %d", len(rows), len(want))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].UpdatedAt.After(rows[i-1].UpdatedAt) {
			t.Error("rows must be ordered by updated_at DESC")
		}
	}

	body := buildCancelledEmail(rows, time.UTC)
	for _, s := range []string{"Gateway", "Razorpay: 1, Cashfree: 2, PhonePe: 2", "Total cancellations: 5"} {
		if !strings.Contains(body, s) {
			t.Errorf("email is missing %q", s)
		}
	}
}
