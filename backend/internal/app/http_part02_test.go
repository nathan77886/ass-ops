package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdateAndDeleteSSHMachineHandlers(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormUser{}, &GormProject{}, &GormConnectionCredential{}, &GormSSHMachine{}, &GormSSHCommandRun{}, &GormAsset{}, &GormAssetRelation{}, &sqliteAssetStatusSnapshotFixture{})
	server := &Server{store: store}
	admin := &User{ID: "user-admin", Email: "admin@example.test", Role: "admin"}
	project := GormProject{Name: "Demo", Slug: "demo"}
	if err := store.Gorm.Create(&project).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	credential := GormConnectionCredential{ProjectID: validNullString(project.ID), Name: "ssh-key", Kind: "ssh_key", SecretCiphertext: "encrypted", Metadata: JSONValue{Data: map[string]any{}}}
	if err := store.Gorm.Create(&credential).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	machine := GormSSHMachine{ProjectID: project.ID, Name: "old", Host: "old.example.com", Port: 22, Username: "deploy", AuthType: "key", CredentialID: validNullString(credential.ID), Metadata: JSONValue{Data: map[string]any{"known_hosts_path": "/etc/assops/ssh/known_hosts/demo", "host_key_path": "/etc/assops/ssh/keys/demo.pub"}}}
	if err := store.Gorm.Create(&machine).Error; err != nil {
		t.Fatalf("create ssh machine: %v", err)
	}
	if _, err := store.SyncCanonicalAssets(t.Context()); err != nil {
		t.Fatalf("sync assets: %v", err)
	}
	updateBody := strings.NewReader(fmt.Sprintf(`{"name":"new","host":"new.example.com","port":2222,"username":"ops","auth_type":"key","credential_id":%q,"metadata":{"team":"platform"}}`, credential.ID))
	updateReq := withRouteParam(httptest.NewRequest(http.MethodPatch, "/api/ssh-machines/"+machine.ID, updateBody), "id", machine.ID)
	updateReq = updateReq.WithContext(context.WithValue(updateReq.Context(), userContextKey{}, admin))
	updateRR := httptest.NewRecorder()
	server.updateSSHMachine(updateRR, updateReq)
	if updateRR.Code != http.StatusOK {
		t.Fatalf("update status = %d, body: %s", updateRR.Code, updateRR.Body.String())
	}
	var updated GormSSHMachine
	if err := store.Gorm.First(&updated, &GormSSHMachine{GormBase: GormBase{ID: machine.ID}}).Error; err != nil {
		t.Fatalf("load updated ssh machine: %v", err)
	}
	if updated.Name != "new" || updated.Host != "new.example.com" || updated.Port != 2222 || updated.Username != "ops" {
		t.Fatalf("unexpected updated ssh machine: %#v", updated)
	}
	updatedMetadata := mapFromAny(updated.Metadata.Data)
	if updatedMetadata["known_hosts_path"] != "/etc/assops/ssh/known_hosts/demo" || updatedMetadata["host_key_path"] != "/etc/assops/ssh/keys/demo.pub" {
		t.Fatalf("ssh metadata was not preserved: %#v", updatedMetadata)
	}
	run := GormSSHCommandRun{SSHMachineID: validNullString(machine.ID), ProjectID: validNullString(project.ID), Command: "true", Status: "completed"}
	if err := store.Gorm.Create(&run).Error; err != nil {
		t.Fatalf("create ssh run: %v", err)
	}
	deleteReq := withRouteParam(httptest.NewRequest(http.MethodDelete, "/api/ssh-machines/"+machine.ID, nil), "id", machine.ID)
	deleteReq = deleteReq.WithContext(context.WithValue(deleteReq.Context(), userContextKey{}, admin))
	deleteRR := httptest.NewRecorder()
	server.deleteSSHMachine(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body: %s", deleteRR.Code, deleteRR.Body.String())
	}
	var machineCount int64
	if err := store.Gorm.Model(&GormSSHMachine{}).Count(&machineCount).Error; err != nil {
		t.Fatalf("count machines: %v", err)
	}
	if machineCount != 0 {
		t.Fatalf("machine count = %d, want 0", machineCount)
	}
	var deletedAssetCount int64
	if err := store.Gorm.Model(&GormAsset{}).Where(&GormAsset{AssetType: "ssh_machine", SourceTable: "ssh_machines", SourceID: validNullString(machine.ID)}).Count(&deletedAssetCount).Error; err != nil {
		t.Fatalf("count deleted ssh asset: %v", err)
	}
	if deletedAssetCount != 0 {
		t.Fatalf("deleted ssh asset count = %d, want 0", deletedAssetCount)
	}
	var preserved GormSSHCommandRun
	if err := store.Gorm.First(&preserved, &GormSSHCommandRun{ID: run.ID}).Error; err != nil {
		t.Fatalf("load preserved run: %v", err)
	}
	if preserved.SSHMachineID.Valid {
		t.Fatalf("ssh run machine id still linked: %#v", preserved.SSHMachineID)
	}
}

func TestCreateGitRemoteAcceptsHTTPSCredential(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormProject{}, &GormProjectGitRepository{}, &GormConnectionCredential{}, &GormGitRemote{}, &GormAsset{}, &GormAssetRelation{}, &sqliteAssetStatusSnapshotFixture{})
	server := &Server{store: store}
	admin := &User{ID: "user-admin", Email: "admin@example.test", Role: "admin"}
	project := GormProject{Name: "Demo", Slug: "demo"}
	if err := store.Gorm.Create(&project).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	repo := GormProjectGitRepository{ProjectID: project.ID, Name: "service", RepoKey: "service", DisplayName: "Service", RepoRole: "service"}
	if err := store.Gorm.Create(&repo).Error; err != nil {
		t.Fatalf("create repo: %v", err)
	}
	gitCredential := GormConnectionCredential{ProjectID: validNullString(project.ID), Name: "gitea token", Kind: "git_https_token", SecretCiphertext: "encrypted", PublicValue: "deploy-bot", Metadata: JSONValue{Data: map[string]any{}}}
	if err := store.Gorm.Create(&gitCredential).Error; err != nil {
		t.Fatalf("create git credential: %v", err)
	}
	argoCredential := GormConnectionCredential{ProjectID: validNullString(project.ID), Name: "argo token", Kind: "argo_token", SecretCiphertext: "encrypted", Metadata: JSONValue{Data: map[string]any{}}}
	if err := store.Gorm.Create(&argoCredential).Error; err != nil {
		t.Fatalf("create argo credential: %v", err)
	}
	body := strings.NewReader(fmt.Sprintf(`{"name":"gitea","remote_key":"gitea","provider_type":"gitea","remote_url":"https://gitea.example.test/acme/service.git","credential_id":%q}`, gitCredential.ID))
	req := withRouteParam(httptest.NewRequest(http.MethodPost, "/api/git-repositories/"+repo.ID+"/remotes", body), "id", repo.ID)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, admin))
	rr := httptest.NewRecorder()
	server.createGitRemote(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", rr.Code, rr.Body.String())
	}
	var remote GormGitRemote
	if err := store.Gorm.First(&remote, &GormGitRemote{Name: "gitea"}).Error; err != nil {
		t.Fatalf("load remote: %v", err)
	}
	if remote.CredentialID.String != gitCredential.ID {
		t.Fatalf("credential id = %q, want %q", remote.CredentialID.String, gitCredential.ID)
	}

	badBody := strings.NewReader(fmt.Sprintf(`{"name":"bad","remote_key":"bad","provider_type":"gitea","remote_url":"https://gitea.example.test/acme/bad.git","credential_id":%q}`, argoCredential.ID))
	badReq := withRouteParam(httptest.NewRequest(http.MethodPost, "/api/git-repositories/"+repo.ID+"/remotes", badBody), "id", repo.ID)
	badReq = badReq.WithContext(context.WithValue(badReq.Context(), userContextKey{}, admin))
	badRR := httptest.NewRecorder()
	server.createGitRemote(badRR, badReq)
	if badRR.Code != http.StatusBadRequest {
		t.Fatalf("bad credential status = %d, body: %s", badRR.Code, badRR.Body.String())
	}
}

func TestCreateConnectionCredentialValidatesGitHTTPSCredential(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormProject{}, &GormConnectionCredential{})
	server := &Server{store: store}
	admin := &User{ID: "user-admin", Email: "admin@example.test", Role: "admin"}
	project := GormProject{Name: "Demo", Slug: "demo"}
	if err := store.Gorm.Create(&project).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}

	body := strings.NewReader(`{"name":"gitea token","kind":"git_https_token","public_value":"deploy-bot","secret_value":"token-value"}`)
	req := withRouteParam(httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/connection-credentials", body), "id", project.ID)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, admin))
	rr := httptest.NewRecorder()
	server.createConnectionCredential(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", rr.Code, rr.Body.String())
	}

	badBody := strings.NewReader(`{"name":"bad","kind":"git_https_token","public_value":"deploy-bot","secret_value":"-----BEGIN PRIVATE KEY-----"}`)
	badReq := withRouteParam(httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/connection-credentials", badBody), "id", project.ID)
	badReq = badReq.WithContext(context.WithValue(badReq.Context(), userContextKey{}, admin))
	badRR := httptest.NewRecorder()
	server.createConnectionCredential(badRR, badReq)
	if badRR.Code != http.StatusBadRequest {
		t.Fatalf("private key status = %d, body: %s", badRR.Code, badRR.Body.String())
	}

	missingUserBody := strings.NewReader(`{"name":"missing-user","kind":"git_https_password","secret_value":"password-value"}`)
	missingUserReq := withRouteParam(httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/connection-credentials", missingUserBody), "id", project.ID)
	missingUserReq = missingUserReq.WithContext(context.WithValue(missingUserReq.Context(), userContextKey{}, admin))
	missingUserRR := httptest.NewRecorder()
	server.createConnectionCredential(missingUserRR, missingUserReq)
	if missingUserRR.Code != http.StatusBadRequest {
		t.Fatalf("missing username status = %d, body: %s", missingUserRR.Code, missingUserRR.Body.String())
	}
}
