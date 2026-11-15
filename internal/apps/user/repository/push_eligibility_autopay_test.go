package repository

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Opt-in DB test (needs ALL migrations applied): set AUTOPAY_FULL_IT_DSN as in the referral test.
func TestPushEligibilityTreatsAutopayLikeRazorpay(t *testing.T) {
	dsn := os.Getenv("AUTOPAY_FULL_IT_DSN")
	if dsn == "" {
		t.Skip("AUTOPAY_FULL_IT_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}

	const app = "PushItestApp"
	cfg := uuid.New()
	if err := db.Exec(`INSERT INTO payment_gateway_configs (id, gateway, app_name, environment) VALUES (?, 'phonepe', ?, ?)`, cfg, app, "it"+cfg.String()[:8]).Error; err != nil {
		t.Fatal(err)
	}
	newUser := func(backdate bool) uuid.UUID {
		id := uuid.New()
		created := "now()"
		if backdate {
			created = "now() - interval '90 days'"
		}
		if err := db.Exec(`INSERT INTO users (id, app_name, email, metadata, created_at) VALUES (?, ?, ?, '{"push_notification_token":"ExponentPushToken[x]"}'::jsonb, `+created+`)`,
			id, app, id.String()+"@example.com").Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	mandate := func(user uuid.UUID, status string) {
		m := uuid.New()
		if err := db.Exec(`INSERT INTO autopay_mandates (id, user_id, app_name, gateway_config_id, payment_type, merchant_autopay_id, status, max_debit_amount, billing_frequency)
			VALUES (?, ?, ?, ?, 'recurring', ?, ?, 9900, 'monthly')`, m, user, app, cfg, "ap_"+m.String(), status).Error; err != nil {
			t.Fatal(err)
		}
	}

	freshNoSub := newUser(false)  // new, nothing -> eligible
	freshActive := newUser(false) // new, active autopay mandate -> not eligible
	oldCancelled := newUser(true) // old, autopay mandate cancelled just now -> eligible (lapsed)
	oldActive := newUser(true)    // old, active autopay mandate -> not eligible
	oldNothing := newUser(true)   // old, nothing -> not eligible
	mandate(freshActive, "active")
	mandate(oldCancelled, "cancelled")
	mandate(oldActive, "active")

	all := []uuid.UUID{freshNoSub, freshActive, oldCancelled, oldActive, oldNothing}
	t.Cleanup(func() {
		for _, u := range all {
			db.Exec(`DELETE FROM autopay_mandates WHERE user_id = ?`, u)
			db.Exec(`DELETE FROM users WHERE id = ?`, u)
		}
		db.Exec(`DELETE FROM payment_gateway_configs WHERE id = ?`, cfg)
	})

	users, err := NewUserRepository(db).FindUsersEligibleForPushNotification(app, 7, 7)
	if err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]bool{}
	for _, u := range users {
		got[u.ID] = true
	}
	want := map[uuid.UUID]bool{freshNoSub: true, oldCancelled: true}
	for _, u := range all {
		if got[u] != want[u] {
			t.Errorf("user %s: eligible=%v, want %v", u, got[u], want[u])
		}
	}
}
