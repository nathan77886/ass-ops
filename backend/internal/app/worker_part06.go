package app

import (
	"fmt"
	"gorm.io/gorm"
)

func workerGormTx(rawTx any) (*gorm.DB, error) {
	if tx, ok := rawTx.(*gorm.DB); ok && tx != nil {
		return tx, nil
	}
	return nil, fmt.Errorf("gorm transaction is required")
}
