package wallet

import (
	"context"
	"fmt"

	"dpg-pay/internal/models"
)

type Service struct {
	store *models.Store
}

func NewService(store *models.Store) *Service {
	return &Service{store: store}
}

func (s *Service) ByType(ctx context.Context, walletType string) (models.Wallet, error) {
	return s.store.GetWalletByType(ctx, walletType)
}

func (s *Service) Details(ctx context.Context, walletType string, page, pageSize int) (models.Wallet, []models.LedgerEntry, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	w, err := s.store.GetWalletByType(ctx, walletType)
	if err != nil {
		return models.Wallet{}, nil, err
	}
	offset := (page - 1) * pageSize
	entries, err := s.store.ListLedgerEntriesByWallet(ctx, w.ID, pageSize, offset)
	if err != nil {
		return models.Wallet{}, nil, err
	}
	return w, entries, nil
}

func FormatCents(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%s %d.%02d", sign, currency, cents/100, cents%100)
}
