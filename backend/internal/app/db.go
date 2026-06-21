package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type Store struct {
	DB *sqlx.DB
}

func OpenStore(ctx context.Context, cfg Config) (*Store, error) {
	db, err := sqlx.ConnectContext(ctx, "postgres", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting postgres: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(10 * time.Minute)
	return &Store{DB: db}, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) ApplyMigrations(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading migrations: %w", err)
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", file, err)
		}
		if _, err := s.DB.ExecContext(ctx, string(content)); err != nil {
			return fmt.Errorf("applying migration %s: %w", file, err)
		}
	}
	return nil
}

func (s *Store) SeedAdmin(ctx context.Context, cfg Config) error {
	var exists bool
	if err := s.DB.GetContext(ctx, &exists, "SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)", cfg.AdminEmail); err != nil {
		return fmt.Errorf("checking admin user: %w", err)
	}
	if exists {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing admin password: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO users(email, name, password_hash, role)
		VALUES ($1, $2, $3, 'admin')`,
		cfg.AdminEmail, "ASSOPS Admin", string(hash))
	if err != nil {
		return fmt.Errorf("creating admin user: %w", err)
	}
	return nil
}

type User struct {
	ID           string `db:"id" json:"id"`
	Email        string `db:"email" json:"email"`
	Name         string `db:"name" json:"name"`
	PasswordHash string `db:"password_hash" json:"-"`
	Role         string `db:"role" json:"role"`
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	err := s.DB.GetContext(ctx, &user, "SELECT id, email, name, password_hash, role FROM users WHERE email=$1", email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by email: %w", err)
	}
	return &user, nil
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	var user User
	err := s.DB.GetContext(ctx, &user, "SELECT id, email, name, password_hash, role FROM users WHERE id=$1", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by id: %w", err)
	}
	return &user, nil
}

func queryMaps(ctx context.Context, db sqlx.ExtContext, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		row := map[string]any{}
		if err := rows.MapScan(row); err != nil {
			return nil, err
		}
		normalizeRow(row)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func queryOne(ctx context.Context, db sqlx.ExtContext, query string, args ...any) (map[string]any, error) {
	items, err := queryMaps(ctx, db, query, args...)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items[0], nil
}

func normalizeRow(row map[string]any) {
	for key, value := range row {
		switch typed := value.(type) {
		case []byte:
			var decoded any
			if json.Unmarshal(typed, &decoded) == nil {
				row[key] = decoded
			} else {
				row[key] = string(typed)
			}
		case time.Time:
			row[key] = typed.Format(time.RFC3339)
		}
	}
}

func jsonParam(value any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encoding json parameter: %w", err)
	}
	return string(bytes), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() string {
	return uuid.NewString() + "." + uuid.NewString()
}
