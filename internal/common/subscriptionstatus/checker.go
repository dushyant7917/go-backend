// Package subscriptionstatus answers "is this user subscribed / have they used the free trial"
// across every recurring payment system (the old Razorpay stack and the unified autopay system, which covers every gateway), so each app's status
// endpoint doesn't re-implement the same any-gateway-wins logic.
package subscriptionstatus

import (
	"log"
	"time"

	"github.com/google/uuid"
)

// GatewayRepo is the subset of a gateway's recurring payment repository the checker needs.
// The old Razorpay repository and the unified autopay repository both satisfy it.
type GatewayRepo interface {
	IsSubscriptionActive(userID uuid.UUID, appName string, now time.Time) (bool, error)
	HasCompletedAuthorizationPayment(userID uuid.UUID, appName string) (bool, error)
}

// Gateway pairs a gateway's repository with a name used in log lines.
type Gateway struct {
	Name string
	Repo GatewayRepo
}

// Status is the merged result across all gateways.
type Status struct {
	ActiveSubscription bool // Active on at least one gateway
	UsedFreeTrial      bool // Completed an authorization payment on at least one gateway
}

// Checker merges subscription status across gateways.
type Checker struct {
	gateways []Gateway
}

// NewChecker creates a Checker. Supporting another gateway is just another Gateway argument here.
func NewChecker(gateways ...Gateway) *Checker {
	return &Checker{gateways: gateways}
}

// Check reports the user's merged status for an app. A user counts as subscribed (or as having used
// the trial) if any gateway says so. A repository error is logged and treated as "not active / trial
// not used" for that gateway, so one failing gateway doesn't hide another's positive answer.
func (c *Checker) Check(userID uuid.UUID, appName string) Status {
	var result Status
	now := time.Now().UTC()

	for _, g := range c.gateways {
		if isActive, err := g.Repo.IsSubscriptionActive(userID, appName, now); err != nil {
			log.Printf("[subscriptionstatus] %s IsSubscriptionActive failed: %v", g.Name, err)
		} else if isActive {
			result.ActiveSubscription = true
		}

		if hasCompleted, err := g.Repo.HasCompletedAuthorizationPayment(userID, appName); err != nil {
			log.Printf("[subscriptionstatus] %s HasCompletedAuthorizationPayment failed: %v", g.Name, err)
		} else if hasCompleted {
			result.UsedFreeTrial = true
		}
	}

	return result
}
