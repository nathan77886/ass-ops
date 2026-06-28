package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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

func jsonPatchParam(value any) (string, error) {
	if value == nil {
		return "null", nil
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encoding json patch parameter: %w", err)
	}
	return string(bytes), nil
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() string {
	return uuid.NewString() + "." + uuid.NewString()
}

func validNullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: strings.TrimSpace(value) != ""}
}

func validNullTime(value time.Time) sql.NullTime {
	return sql.NullTime{Time: value, Valid: !value.IsZero()}
}

func gormField(column string, value any) map[string]any {
	return map[string]any{column: value}
}

func gormOrderAsc(column string) clause.OrderBy {
	return clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: column}}}}
}

func gormOrderDesc(column string) clause.OrderBy {
	return clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: column}, Desc: true}}}
}

func gormOrder(columns ...clause.OrderByColumn) clause.OrderBy {
	return clause.OrderBy{Columns: columns}
}

func gormOrderColumn(column string, desc bool) clause.OrderByColumn {
	return clause.OrderByColumn{Column: clause.Column{Name: column}, Desc: desc}
}

func whereFieldGTE(db *gorm.DB, column string, value any) *gorm.DB {
	return db.Where(clause.Gte{Column: clause.Column{Name: column}, Value: value})
}

func whereFieldLTE(db *gorm.DB, column string, value any) *gorm.DB {
	return db.Where(clause.Lte{Column: clause.Column{Name: column}, Value: value})
}

func whereAnyFieldEquals(db *gorm.DB, value any, columns ...string) *gorm.DB {
	if len(columns) == 0 {
		return db
	}
	condition := db.Session(&gorm.Session{NewDB: true}).Where(gormField(columns[0], value))
	for _, column := range columns[1:] {
		condition = condition.Or(gormField(column, value))
	}
	return db.Where(condition)
}
