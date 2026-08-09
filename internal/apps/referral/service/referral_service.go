package service

import (
	"go-backend/internal/apps/referral/repository"
)

// ReferralBonusResponse is the response payload for the pending referral bonus query
type ReferralBonusResponse struct {
	ReferralCode  string `json:"referral_code"`
	AppName       string `json:"app_name"`
	ReferralCount int    `json:"referral_count"`
	BonusAmount   int64  `json:"bonus_amount"`
}

// ReferralService defines the interface for referral business logic
type ReferralService interface {
	GetPendingReferralBonus(appName, referralCode string) (*ReferralBonusResponse, error)
}

// referralService implements ReferralService interface
type referralService struct {
	repo repository.ReferralRepository
}

// NewReferralService creates a new instance of ReferralService
func NewReferralService(repo repository.ReferralRepository) ReferralService {
	return &referralService{repo: repo}
}

// GetPendingReferralBonus returns the pending referral bonus for a referral code, scoped to an app
func (s *referralService) GetPendingReferralBonus(appName, referralCode string) (*ReferralBonusResponse, error) {
	totalAmount, referralCount, err := s.repo.GetPendingReferralBonus(appName, referralCode)
	if err != nil {
		return nil, err
	}

	return &ReferralBonusResponse{
		ReferralCode:  referralCode,
		AppName:       appName,
		ReferralCount: referralCount,
		BonusAmount:   totalAmount,
	}, nil
}
