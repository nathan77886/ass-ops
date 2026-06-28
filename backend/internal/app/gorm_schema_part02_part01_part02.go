package app

import (
	"database/sql"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"time"
)

func (m *GormRollbackPoint) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormAsset struct {
	GormBase
	ProjectID   sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	AssetType   string         `gorm:"not null;index:assets_source_key,unique" json:"asset_type"`
	SourceTable string         `gorm:"not null;default:'';index:assets_source_key,unique" json:"source_table"`
	SourceID    sql.NullString `gorm:"type:uuid;index:assets_source_key,unique" json:"source_id"`
	Name        string         `gorm:"not null" json:"name"`
	DisplayName string         `gorm:"not null;default:''" json:"display_name"`
	Description string         `gorm:"not null;default:''" json:"description"`
	Source      string         `gorm:"not null;default:'local'" json:"source"`
	ExternalID  string         `gorm:"not null;default:''" json:"external_id"`
	Status      string         `gorm:"not null;default:'unknown';index" json:"status"`
	RiskLevel   string         `gorm:"not null;default:'normal';index" json:"risk_level"`
	OwnerUserID sql.NullString `gorm:"type:uuid;index" json:"owner_user_id"`
	Metadata    JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormAsset) TableName() string { return "assets" }

type GormAssetRelation struct {
	ID           string         `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID    sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	FromAssetID  string         `gorm:"type:uuid;not null;index:asset_relations_unique_relation,unique" json:"from_asset_id"`
	ToAssetID    string         `gorm:"type:uuid;not null;index:asset_relations_unique_relation,unique" json:"to_asset_id"`
	RelationType string         `gorm:"not null;index:asset_relations_unique_relation,unique" json:"relation_type"`
	Metadata     JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	CreatedAt    time.Time      `json:"created_at"`
}

func (GormAssetRelation) TableName() string { return "asset_relations" }

func (m *GormAssetRelation) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}
