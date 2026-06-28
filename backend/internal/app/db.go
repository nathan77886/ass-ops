package app

import (
	"context"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store struct {
	Gorm *gorm.DB
}

func OpenStore(ctx context.Context, cfg Config) (*Store, error) {
	gormDB, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("opening gorm postgres: %w", err)
	}
	gormSQL, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("opening gorm sql db: %w", err)
	}
	gormSQL.SetMaxOpenConns(20)
	gormSQL.SetMaxIdleConns(5)
	gormSQL.SetConnMaxLifetime(10 * time.Minute)
	return &Store{Gorm: gormDB}, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	if s.Gorm != nil {
		gormSQL, err := s.Gorm.DB()
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil {
			if err := gormSQL.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *Store) AutoMigrate(ctx context.Context) error {
	if s == nil || s.Gorm == nil {
		return fmt.Errorf("gorm store is not initialized")
	}
	models, err := gormSchemaModelsForAutoMigrate(s.Gorm.WithContext(ctx))
	if err != nil {
		return err
	}
	if err := s.Gorm.WithContext(ctx).AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto migrating gorm schema: %w", err)
	}
	return nil
}
