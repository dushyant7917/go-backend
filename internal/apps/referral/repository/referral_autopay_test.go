package repository

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Opt-in DB test (needs ALL migrations applied, e.g. a scratch database migrated with goose):
//
//	AUTOPAY_FULL_IT_DSN="host=localhost port=5432 user=postgres password=postgres dbname=autopay_full_it sslmode=disable" go test go-backend/internal/apps/referral/repository
func TestPendingReferralBonusCountsAutopayAndRazorpayOncePerUser(t *testing.T) {
	dsn := os.Getenv("AUTOPAY_FULL_IT_DSN")
	if dsn == "" {
		t.Skip("AUTOPAY_FULL_IT_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}

	const app = "RefItestApp"
	code := "REF-" + uuid.NewString()[:8]
	cfg := uuid.New()
	if err := db.Exec(`INSERT INTO payment_gateway_configs (id, gateway, app_name, environment) VALUES (?, 'cashfree', ?, ?)`, cfg, app, "it"+cfg.String()[:8]).Error; err != nil {
		t.Fatal(err)
	}
	mkUser := func() uuid.UUID {
		id := uuid.New()
		if err := db.Exec(`INSERT INTO users (id, app_name, email, metadata) VALUES (?, ?, ?, jsonb_build_object('referral_code', ?::text))`,
			id, app, id.String()+"@example.com", code).Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	autopayCycle := func(user uuid.UUID, cycleNumber int, amount int64, status string) {
		m := uuid.New()
		if err := db.Exec(`INSERT INTO autopay_mandates (id, user_id, app_name, gateway_config_id, payment_type, merchant_autopay_id, status, max_debit_amount, billing_frequency)
			VALUES (?, ?, ?, ?, 'recurring', ?, 'active', ?, 'monthly')`, m, user, app, cfg, "ap_"+m.String(), amount).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO autopay_billing_cycles (autopay_mandate_id, cycle_number, start_at, amount, status) VALUES (?, ?, now(), ?, ?)`,
			m, cycleNumber, amount, status).Error; err != nil {
			t.Fatal(err)
		}
	}
	razorpayCycle := func(user uuid.UUID, cycleNumber int, amount int64, status string) {
		rp := uuid.New()
		if err := db.Exec(`INSERT INTO recurring_payments (id, user_id, app_name, status, max_amount, frequency) VALUES (?, ?, ?, 'active', ?, 'monthly')`,
			rp, user, app, amount).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO billing_cycles (recurring_payment_id, cycle_number, start_at, amount, status) VALUES (?, ?, now(), ?, ?)`,
			rp, cycleNumber, amount, status).Error; err != nil {
			t.Fatal(err)
		}
	}

	apHigh := mkUser()   // autopay, cycle 1 paid, >= 699  -> 100
	apLow := mkUser()    // autopay, cycle 0 paid, Rs 99    -> 20
	both := mkUser()     // paid in both systems            -> counted once
	unpaid := mkUser()   // autopay, cycle 1 still awaiting -> not counted
	tooSmall := mkUser() // autopay, cycle 0 of Rs 1        -> not counted
	rzpOnly := mkUser()  // old Razorpay only (existing behaviour) -> 20

	autopayCycle(apHigh, 1, 69900, "paid")
	autopayCycle(apLow, 0, 9900, "paid")
	autopayCycle(both, 1, 9900, "paid")
	razorpayCycle(both, 1, 9900, "paid")
	autopayCycle(unpaid, 1, 9900, "awaiting_payment")
	autopayCycle(tooSmall, 0, 100, "paid")
	razorpayCycle(rzpOnly, 1, 9900, "paid")

	t.Cleanup(func() {
		for _, u := range []uuid.UUID{apHigh, apLow, both, unpaid, tooSmall, rzpOnly} {
			db.Exec(`DELETE FROM autopay_mandates WHERE user_id = ?`, u)
			db.Exec(`DELETE FROM recurring_payments WHERE user_id = ?`, u)
			db.Exec(`DELETE FROM users WHERE id = ?`, u)
		}
		db.Exec(`DELETE FROM payment_gateway_configs WHERE id = ?`, cfg)
	})

	total, count, err := NewReferralRepository(db).GetPendingReferralBonus(app, code)
	if err != nil {
		t.Fatal(err)
	}
	// apHigh 100 + apLow 20 + both 20 + rzpOnly 20
	if count != 4 || total != 160 {
		t.Fatalf("count=%d total=%d, want count=4 total=160", count, total)
	}
}
