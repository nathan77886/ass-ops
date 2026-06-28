package app

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
)

func demoReadinessRemoteSpecs() []demoReadinessRemoteSpec {
	return []demoReadinessRemoteSpec{
		{
			Name:         "Demo Gitea Source",
			Kind:         "gitea",
			RemoteKey:    "gitea",
			ProviderType: "gitea",
			RemoteRole:   "source",
			IsPrimary:    true,
		},
		{
			Name:         "Demo GitHub Mirror",
			Kind:         "github",
			RemoteKey:    "github",
			ProviderType: "github",
			RemoteRole:   "mirror",
			IsPrimary:    false,
		},
	}
}

func ensureDemoReadinessRemote(ctx context.Context, tx *gorm.DB, repositoryID string, spec demoReadinessRemoteSpec) (string, bool, error) {
	var remote GormGitRemote
	if err := tx.WithContext(ctx).Where(&GormGitRemote{ProjectGitRepositoryID: repositoryID, RemoteKey: spec.RemoteKey}).First(&remote).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, fmt.Errorf("loading demo readiness remote %q: %w", spec.RemoteKey, err)
		}
		remote = demoReadinessRemoteModel(repositoryID, spec)
		if err := tx.WithContext(ctx).Create(&remote).Error; err != nil {
			return "", false, fmt.Errorf("creating demo readiness remote %q: %w", spec.RemoteKey, err)
		}
		return remote.ID, true, nil
	}
	updated := demoReadinessRemoteModel(repositoryID, spec)
	updated.ID = remote.ID
	updated.CreatedAt = remote.CreatedAt
	if err := tx.WithContext(ctx).Save(&updated).Error; err != nil {
		return "", false, fmt.Errorf("updating demo readiness remote %q: %w", spec.RemoteKey, err)
	}
	return remote.ID, false, nil
}

func demoReadinessRemoteModel(repositoryID string, spec demoReadinessRemoteSpec) GormGitRemote {
	return GormGitRemote{
		ProjectGitRepositoryID: repositoryID,
		Name:                   spec.Name,
		Kind:                   spec.Kind,
		RemoteKey:              spec.RemoteKey,
		ProviderType:           spec.ProviderType,
		RemoteRole:             spec.RemoteRole,
		IsPrimary:              spec.IsPrimary,
		SyncEnabled:            true,
		Protected:              false,
		LatestSHA:              "",
		LastSyncStatus:         "never",
		URLs:                   JSONValue{Data: []string{}},
		DefaultBranch:          "main",
		Metadata:               JSONValue{Data: map[string]any{"source": "demo_readiness_data", "url_intentionally_omitted": true}},
	}
}
