package app

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

func (w *ControlWorker) executeArgoTokenCreate(ctx context.Context, opID string, result map[string]any) (map[string]any, error) {
	if w == nil || w.store == nil || w.store.Gorm == nil {
		return result, fmt.Errorf("database is not configured")
	}
	var op GormOperationRun
	if err := w.store.Gorm.WithContext(ctx).First(&op, &GormOperationRun{GormBase: GormBase{ID: opID}}).Error; err != nil {
		return result, err
	}
	input := mapFromAny(op.Input.Data)
	envID := cleanOptionalID(stringFromMap(input, "kubernetes_environment_id"))
	if envID == "" {
		return result, fmt.Errorf("operation is missing kubernetes_environment_id")
	}
	var env GormKubernetesEnvironment
	if err := w.store.Gorm.WithContext(ctx).First(&env, &GormKubernetesEnvironment{GormBase: GormBase{ID: envID}}).Error; err != nil {
		return result, err
	}
	server := &Server{store: w.store, cfg: w.cfg}
	existing, err := server.existingAutoArgoCredentialForEnvironment(ctx, env)
	if err != nil {
		return result, err
	}
	if existing != nil {
		return argoTokenCreateResult(result, env, *existing, "Argo token credential reused"), nil
	}
	credential, err := server.argoCredentialFromKubernetesPod(ctx, env, stringFromMap(input, "connection_name"))
	if err != nil {
		return result, err
	}
	if err := w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Create(&credential).Error
	}); err != nil {
		return result, err
	}
	return argoTokenCreateResult(result, env, credential, "Argo token credential created"), nil
}

func argoTokenCreateResult(result map[string]any, env GormKubernetesEnvironment, credential GormConnectionCredential, message string) map[string]any {
	metadata := mapFromAny(credential.Metadata.Data)
	result["message"] = message
	result["credential_id"] = credential.ID
	result["credential_name"] = credential.Name
	result["credential_kind"] = credential.Kind
	result["kubernetes_environment_id"] = env.ID
	result["source_pod"] = map[string]any{
		"namespace":      metadataString(metadata["namespace"]),
		"pod_name":       metadataString(metadata["pod_name"]),
		"container_name": metadataString(metadata["container_name"]),
	}
	result["secret_included"] = false
	result["stdout_included"] = false
	result["stderr_included"] = false
	return result
}
