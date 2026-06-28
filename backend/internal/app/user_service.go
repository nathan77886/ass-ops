package app

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func (s *Store) SeedAdmin(ctx context.Context, cfg Config) error {
	if s == nil || s.Gorm == nil {
		return fmt.Errorf("gorm store is not initialized")
	}
	var existing GormUser
	err := s.Gorm.WithContext(ctx).Where(gormField("email", cfg.AdminEmail)).Take(&existing).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("checking admin user: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing admin password: %w", err)
	}
	admin := GormUser{
		Email:        cfg.AdminEmail,
		Name:         "ASSOPS Admin",
		PasswordHash: string(hash),
		Role:         "admin",
	}
	if err := s.Gorm.WithContext(ctx).Create(&admin).Error; err != nil {
		return fmt.Errorf("creating admin user: %w", err)
	}
	return nil
}

type User struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	var model GormUser
	err := s.Gorm.WithContext(ctx).Where(map[string]any{"email": email}).First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by email: %w", err)
	}
	return userFromGorm(model), nil
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	var model GormUser
	err := s.Gorm.WithContext(ctx).First(&model, &GormUser{GormBase: GormBase{ID: id}}).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by id: %w", err)
	}
	return userFromGorm(model), nil
}

func userFromGorm(model GormUser) *User {
	return &User{
		ID:           model.ID,
		Email:        model.Email,
		Name:         model.Name,
		PasswordHash: model.PasswordHash,
		Role:         model.Role,
	}
}
