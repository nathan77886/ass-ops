package app

import (
	"context"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm/clause"
	"net"
	"net/http"
	"strings"
	"time"
)

func (l *webhookRateLimiter) allow(key string, now time.Time) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.buckets[key]
	if bucket.resetAt.IsZero() || now.After(bucket.resetAt) {
		bucket = webhookRateBucket{resetAt: now.Add(l.window)}
	}
	bucket.count++
	bucket.updatedAt = now
	l.buckets[key] = bucket
	for bucketKey, stale := range l.buckets {
		if now.Sub(stale.updatedAt) > 2*l.window {
			delete(l.buckets, bucketKey)
		}
	}
	return bucket.count <= l.limit
}

func webhookRateLimitKey(r *http.Request, connectionID string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		host = "unknown"
	}
	return host + ":" + connectionID
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := s.store.UserByEmail(r.Context(), strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	token, err := s.signJWT(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not sign token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": currentUser(r)})
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenText := bearerTokenFromRequest(r)
		if tokenText == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		token, err := jwt.Parse(tokenText, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(s.cfg.JWTSecret), nil
		})
		if err != nil || !token.Valid {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		sub, err := token.Claims.GetSubject()
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token subject")
			return
		}
		user, err := s.store.UserByID(r.Context(), sub)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "user not found")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, user)))
	})
}

func bearerTokenFromRequest(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}
	if strings.HasSuffix(r.URL.Path, "/logs/stream") {
		return strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return ""
}

func (s *Server) signJWT(userID string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   userID,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	})
	return token.SignedString([]byte(s.cfg.JWTSecret))
}

type userContextKey struct{}

func currentUser(r *http.Request) *User {
	user, _ := r.Context().Value(userContextKey{}).(*User)
	return user
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project"}, "create") {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}
	project := GormProject{Name: req.Name, Slug: req.Slug, Description: req.Description}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&project).Error; err != nil {
		writeError(w, http.StatusBadRequest, "could not create project")
		return
	}
	member := GormProjectMember{ProjectID: project.ID, UserID: currentUser(r).ID, Role: "owner"}
	if err := s.store.Gorm.WithContext(r.Context()).Where(map[string]any{"project_id": member.ProjectID, "user_id": member.UserID}).FirstOrCreate(&member).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not create project membership")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync project asset")
		return
	}
	writeJSON(w, http.StatusCreated, projectMap(project))
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project"}, "read") {
		return
	}
	user := currentUser(r)
	var projects []GormProject
	query := s.store.Gorm.WithContext(r.Context()).Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true})
	if !userCanReadAllProjects(user) {
		var memberships []GormProjectMember
		if err := s.store.Gorm.WithContext(r.Context()).Where(map[string]any{"user_id": userIDOrNil(user)}).Find(&memberships).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		projectIDs := make([]string, 0, len(memberships))
		for _, membership := range memberships {
			projectIDs = append(projectIDs, membership.ProjectID)
		}
		if len(projectIDs) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"items": []map[string]any{}})
			return
		}
		query = query.Find(&projects, projectIDs)
	} else {
		query = query.Find(&projects)
	}
	if query.Error != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": projectMaps(projects)})
}

func (s *Server) recordDemoReadinessSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID   string `json:"project_id"`
		ProjectSlug string `json:"project_slug"`
		DryRun      bool   `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	opts := DemoReadinessSnapshotOptions{ProjectID: req.ProjectID, ProjectSlug: req.ProjectSlug, DryRun: req.DryRun}
	projectID, err := ResolveDemoReadinessSnapshotProjectID(r.Context(), s.store, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "resolve demo readiness project failed")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project", ID: projectID, ProjectID: projectID}, "update") {
		return
	}
	opts.ProjectID = projectID
	opts.ProjectSlug = ""
	result, err := RecordDemoReadinessSnapshot(r.Context(), s.store, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "record demo readiness snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) ensureDemoReadinessData(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project"}, "create") {
		return
	}
	var req struct {
		ProjectName    string `json:"project_name"`
		ProjectSlug    string `json:"project_slug"`
		RepositoryName string `json:"repository_name"`
		RepositoryKey  string `json:"repository_key"`
		DryRun         bool   `json:"dry_run"`
		RecordSnapshot *bool  `json:"record_snapshot"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	recordSnapshot := true
	if req.RecordSnapshot != nil {
		recordSnapshot = *req.RecordSnapshot
	}
	actorUserID := ""
	if user := currentUser(r); user != nil {
		actorUserID = user.ID
	}
	result, err := EnsureDemoReadinessData(r.Context(), s.store, DemoReadinessDataOptions{
		ProjectName:    req.ProjectName,
		ProjectSlug:    req.ProjectSlug,
		RepositoryName: req.RepositoryName,
		RepositoryKey:  req.RepositoryKey,
		ActorUserID:    actorUserID,
		DryRun:         req.DryRun,
		RecordSnapshot: recordSnapshot,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not ensure demo readiness data")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listProjectTemplates(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "read") {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	var templates []GormProjectTemplate
	query := s.store.Gorm.WithContext(r.Context()).Order(gormOrderDesc("updated_at")).Order(gormOrderAsc("name")).Limit(100)
	if status != "" {
		query = query.Where(&GormProjectTemplate{Status: status})
	}
	if err := query.Find(&templates).Error; err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	items := make([]map[string]any, 0, len(templates))
	for _, template := range templates {
		items = append(items, projectTemplateMap(template))
	}
	writeQueryResult(w, items, nil)
}
