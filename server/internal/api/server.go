package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"dochub/internal/config"
	"dochub/internal/docparse"
	"dochub/internal/docs"
	"dochub/internal/secretbox"
	"dochub/internal/store"
)

const sessionCookie = "dochub_session"

// iframe 和 CSP 同时启用 sandbox：即使页面被单独打开，也不能取得 DocHub 同源权限。
const htmlSandboxPolicy = "sandbox allow-scripts allow-popups; " +
	"default-src 'none'; base-uri 'self'; object-src 'none'; frame-src 'none'; " +
	"form-action 'none'; frame-ancestors 'self'; " +
	"script-src 'self' 'unsafe-inline' https:; " +
	"style-src 'self' 'unsafe-inline' https:; " +
	"img-src 'self' data: blob: https:; " +
	"font-src 'self' data: https:; " +
	"media-src 'self' data: blob: https:; " +
	"connect-src 'self' https:"

const htmlResourceGrantTTL = time.Hour

// sandbox 中的子资源请求不会携带登录 Cookie，使用短期凭证绑定用户、分支和文档。
type htmlResourceGrant struct {
	ProjectID    string `json:"projectId"`
	BranchID     string `json:"branchId"`
	DocumentPath string `json:"documentPath"`
	UserID       string `json:"userId"`
	SessionID    string `json:"sessionId"`
	ExpiresAt    int64  `json:"expiresAt"`
}

type contextKey string

const (
	contextUser contextKey = "user"
	contextCSRF contextKey = "csrf"
	contextSID  contextKey = "session-id"
)

type Server struct {
	cfg     config.Config
	db      *store.Store
	docs    *docs.Service
	secrets *secretbox.Box
}

func New(cfg config.Config, db *store.Store, docsService *docs.Service) *Server {
	return &Server{cfg: cfg, db: db, docs: docsService, secrets: secretbox.New(cfg.StorageDir)}
}

func (s *Server) Router() *gin.Engine {
	router := gin.New()
	router.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		Skip: func(c *gin.Context) bool {
			return c.Request.URL.Path == "/api/auth/collie/callback" ||
				(strings.HasPrefix(c.Request.URL.Path, "/api/auth/oauth2/") &&
					strings.HasSuffix(c.Request.URL.Path, "/callback")) ||
				strings.Contains(c.Request.URL.Path, "/sandbox-assets/")
		},
	}), gin.Recovery(), securityHeaders())
	_ = router.SetTrustedProxies(nil)
	router.Use(limitRequestBody(1 << 20))

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := router.Group("/api")
	api.POST("/auth/login", s.login)
	api.GET("/auth/options", s.authOptions)
	api.GET("/auth/oauth2/:provider/start", s.oauthStart)
	api.GET("/auth/oauth2/:provider/callback", s.oauthCallback)
	api.GET("/auth/collie/start", func(c *gin.Context) {
		c.Params = append(c.Params, gin.Param{Key: "provider", Value: "collie"})
		s.oauthStart(c)
	})
	api.GET("/auth/collie/callback", func(c *gin.Context) {
		c.Params = append(c.Params, gin.Param{Key: "provider", Value: "collie"})
		s.oauthCallback(c)
	})
	api.GET("/projects/:id/sandbox-assets/:token/*assetPath", s.sandboxAsset)
	protected := api.Group("")
	protected.Use(s.authenticate())
	protected.GET("/auth/me", s.me)
	protected.PUT("/auth/password", s.changePassword)
	protected.POST("/auth/logout", s.logout)
	protected.GET("/account/tokens", s.listAccessTokens)
	protected.POST("/account/tokens", s.createAccessToken)
	protected.DELETE("/account/tokens/:tokenId", s.deleteAccessToken)

	protected.GET("/projects", s.listProjects)
	protected.POST("/projects", s.createProject)
	protected.GET("/projects/:id", s.getProject)
	protected.PATCH("/projects/:id", s.updateProject)
	protected.DELETE("/projects/:id", s.deleteProject)
	protected.POST("/projects/:id/branches", s.addBranch)
	protected.DELETE("/projects/:id/branches", s.deleteBranch)
	protected.POST("/projects/:id/sync", s.syncBranch)
	protected.GET("/projects/:id/tree", s.documentTree)
	protected.GET("/projects/:id/doc", s.document)
	protected.GET("/projects/:id/doc/download", s.downloadDocument)
	protected.GET("/projects/:id/html-preview/:branchID", s.htmlPreview)
	protected.GET("/projects/:id/assets/*assetPath", s.asset)
	protected.GET("/projects/:id/search", s.search)
	protected.GET("/jobs/:id", s.job)

	protected.GET("/users/directory", s.userDirectory)
	protected.GET("/users", s.listUsers)
	protected.POST("/users", s.createUser)
	protected.PATCH("/users/:id/role", s.updateUserRole)
	protected.PUT("/users/:id", s.updateUser)
	protected.DELETE("/users/:id", s.deleteUser)
	protected.GET("/organizations/directory", s.organizationDirectory)
	protected.GET("/admin/organizations", s.listOrganizations)
	protected.POST("/admin/organizations", s.createOrganization)
	protected.PUT("/admin/organizations/:id", s.updateOrganization)
	protected.DELETE("/admin/organizations/:id", s.deleteOrganization)
	protected.PUT("/admin/users/:id/organizations", s.setUserOrganizations)
	protected.GET("/admin/sso", s.listSSOSettings)
	protected.POST("/admin/sso", s.createSSOSettings)
	protected.GET("/admin/sso/:id", s.getSSOSettings)
	protected.PUT("/admin/sso/:id", s.updateSSOSettings)
	protected.DELETE("/admin/sso/:id", s.deleteSSOSettings)
	protected.GET("/projects/:id/access", s.projectAccess)
	protected.PUT("/projects/:id/access/project", s.setProjectAccess)
	protected.PUT("/projects/:id/access/document", s.setDocumentAccess)

	s.mountFrontend(router)
	return router
}

func (s *Server) login(c *gin.Context) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请输入用户名和密码", err.Error())
		return
	}
	user, hash, err := s.db.UserByUsername(c, input.Username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil {
		fail(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "用户名或密码错误", "")
		return
	}
	csrf, err := s.establishSession(c, user.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, "SESSION_FAILED", "登录失败", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"user": user, "csrfToken": csrf}})
}

func (s *Server) establishSession(c *gin.Context, userID string) (string, error) {
	token := randomToken(32)
	csrf := randomToken(24)
	expiresAt := time.Now().Add(s.cfg.SessionTTL)
	if err := s.db.CreateSession(c, userID, hashToken(token), csrf, expiresAt); err != nil {
		return "", err
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
		Expires: expiresAt, MaxAge: int(s.cfg.SessionTTL.Seconds()),
	})
	return csrf, nil
}

func (s *Server) me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"user":      currentUser(c),
		"csrfToken": c.GetString(string(contextCSRF)),
	}})
}

func (s *Server) logout(c *gin.Context) {
	sessionID := c.GetString(string(contextSID))
	_ = s.db.DeleteSession(c, sessionID)
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	c.Status(http.StatusNoContent)
}

func (s *Server) changePassword(c *gin.Context) {
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_PASSWORD", "密码参数无效", "")
		return
	}
	if len([]rune(input.NewPassword)) < 8 || len([]byte(input.NewPassword)) > 72 {
		fail(c, http.StatusBadRequest, "INVALID_PASSWORD", "新密码至少 8 位且不能超过 72 字节", "")
		return
	}
	user := currentUser(c)
	if user.HasPassword {
		hash, err := s.db.UserPasswordHash(c, user.ID)
		if err != nil {
			internal(c, err)
			return
		}
		if input.CurrentPassword == "" ||
			bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.CurrentPassword)) != nil {
			fail(c, http.StatusBadRequest, "INVALID_PASSWORD", "当前密码不正确", "")
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		internal(c, err)
		return
	}
	if err := s.db.UpdatePassword(c, user.ID, string(hash), c.GetString(string(contextSID))); err != nil {
		internal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		var user store.User
		var csrf string
		var sessionID string
		token, cookieErr := c.Cookie(sessionCookie)
		if cookieErr == nil && token != "" {
			var err error
			user, csrf, err = s.db.UserBySession(c, hashToken(token))
			if err == nil {
				sessionID = hashToken(token)
			}
		}
		if user.ID == "" && isSafeHTTPMethod(c.Request.Method) {
			if rawToken := bearerToken(c.GetHeader("Authorization")); rawToken != "" {
				var err error
				user, err = s.db.AccessTokenUser(c, hashToken(rawToken))
				if err != nil || !supportsAccessToken(c.Request.Method, c.Request.URL.Path) {
					fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Agent 凭证无效或已失效", "")
					c.Abort()
					return
				}
			}
		}
		if user.ID == "" && isSafeHTTPMethod(c.Request.Method) {
			if _, _, ok := c.Request.BasicAuth(); ok {
				var authenticated bool
				user, authenticated = s.authenticateBasic(c)
				if !authenticated {
					challengeBasicAuth(c)
					fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "用户名或密码错误", "")
					c.Abort()
					return
				}
			}
		}
		if user.ID == "" {
			if supportsBasicAuth(c.Request.Method, c.Request.URL.Path) {
				challengeBasicAuth(c)
			}
			fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "请先登录", "")
			c.Abort()
			return
		}
		if !isSafeHTTPMethod(c.Request.Method) && c.GetHeader("X-CSRF-Token") != csrf {
			fail(c, http.StatusForbidden, "INVALID_CSRF", "请求校验失败，请刷新页面后重试", "")
			c.Abort()
			return
		}
		c.Set(string(contextUser), user)
		c.Set(string(contextCSRF), csrf)
		c.Set(string(contextSID), sessionID)
		c.Next()
	}
}

func (s *Server) authenticateBasic(c *gin.Context) (store.User, bool) {
	username, password, ok := c.Request.BasicAuth()
	if !ok || password == "" || len(password) > 1024 {
		return store.User{}, false
	}
	user, hash, err := s.db.UserByUsername(c, username)
	if err != nil || hash == "" ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return store.User{}, false
	}
	return user, true
}

func (s *Server) listProjects(c *gin.Context) {
	user := currentUser(c)
	projects, err := s.db.ListProjects(c, user.ID, user.Role == "admin")
	if err != nil {
		internal(c, err)
		return
	}
	if projects == nil {
		projects = []store.Project{}
	}
	c.JSON(http.StatusOK, gin.H{"data": projects})
}

func (s *Server) createProject(c *gin.Context) {
	var input struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		RepoURL     string   `json:"repoUrl"`
		DocRoot     string   `json:"docRoot"`
		DocRoots    []string `json:"docRoots"`
		Branches    []string `json:"branches"`
		IsPublic    bool     `json:"isPublic"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "项目参数不完整", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.RepoURL = strings.TrimSpace(input.RepoURL)
	if len(input.DocRoots) == 0 && strings.TrimSpace(input.DocRoot) != "" {
		input.DocRoots = []string{input.DocRoot}
	}
	docRoots, docRootMessage := docs.NormalizeDocRoots(input.DocRoots)
	branches, message := normalizeBranches(input.Branches)
	if docRootMessage != nil {
		message = docRootMessage.Error()
	}
	if input.Name == "" || len(input.Name) > 100 {
		message = "项目名称长度应为 1-100 个字符"
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		message = "项目描述不能超过 500 个字符"
	}
	if message == "" {
		if err := firstError(
			docs.ValidateRepoURL(input.RepoURL, s.cfg.GitHostAllowlist),
		); err != nil {
			message = err.Error()
		}
	}
	if message != "" {
		fail(c, http.StatusBadRequest, "INVALID_PROJECT", message, "")
		return
	}
	user := currentUser(c)
	project, err := s.db.CreateProject(
		c, input.Name, input.Description, input.RepoURL, docRoots, user.ID, branches, input.IsPublic,
	)
	if err != nil {
		if errors.Is(err, store.ErrSlugExists) {
			slug, _ := store.ProjectSlug(input.RepoURL)
			fail(c, http.StatusConflict, "PROJECT_SLUG_EXISTS", "仓库名 "+slug+" 已被使用，请先调整项目路径或移除同名项目", "")
			return
		}
		fail(c, http.StatusConflict, "CREATE_FAILED", "新建项目失败，请检查参数后重试", err.Error())
		return
	}
	for _, branch := range branches {
		if _, err := s.enqueue(c, project.ID, branch); err != nil {
			fail(c, http.StatusInternalServerError, "SYNC_QUEUE_FAILED", "项目已创建，但同步任务提交失败", err.Error())
			return
		}
	}
	c.JSON(http.StatusCreated, gin.H{"data": project})
}

func (s *Server) getProject(c *gin.Context) {
	project, _, ok := s.requireProjectRead(c, c.Param("id"))
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": project})
}

func (s *Server) updateProject(c *gin.Context) {
	project, ok := s.manageableProject(c)
	if !ok {
		return
	}
	var input struct {
		Name        string   `json:"name"`
		Description *string  `json:"description"`
		RepoURL     string   `json:"repoUrl"`
		DocRoot     string   `json:"docRoot"`
		DocRoots    []string `json:"docRoots"`
		IsPublic    *bool    `json:"isPublic"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "项目参数不完整", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.RepoURL = strings.TrimSpace(input.RepoURL)
	description := project.Description
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	if len(input.DocRoots) == 0 && strings.TrimSpace(input.DocRoot) != "" {
		input.DocRoots = []string{input.DocRoot}
	}
	docRoots, docRootMessage := docs.NormalizeDocRoots(input.DocRoots)
	if docRootMessage != nil {
		fail(c, http.StatusBadRequest, "INVALID_PROJECT", docRootMessage.Error(), "")
		return
	}
	if input.Name == "" || len(input.Name) > 100 {
		fail(c, http.StatusBadRequest, "INVALID_PROJECT", "项目名称长度应为 1-100 个字符", "")
		return
	}
	if utf8.RuneCountInString(description) > 500 {
		fail(c, http.StatusBadRequest, "INVALID_PROJECT", "项目描述不能超过 500 个字符", "")
		return
	}
	if err := firstError(
		docs.ValidateRepoURL(input.RepoURL, s.cfg.GitHostAllowlist),
	); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_PROJECT", err.Error(), "")
		return
	}
	configChanged := input.RepoURL != project.RepoURL || !slices.Equal(docRoots, project.DocRoots)
	// 旧客户端未提交可见范围时不修改该字段；仅切换权限时无需重新同步仓库。
	isPublic := project.IsPublic
	if input.IsPublic != nil {
		isPublic = *input.IsPublic
	}
	if err := s.db.UpdateProject(c, project.ID, input.Name, description, input.RepoURL, docRoots, isPublic); err != nil {
		internal(c, err)
		return
	}
	if configChanged {
		s.docs.RemoveProject(project.ID)
		if err := s.db.InvalidateProject(c, project.ID); err != nil {
			internal(c, err)
			return
		}
		for _, branch := range project.Branches {
			if _, err := s.enqueue(c, project.ID, branch.Name); err != nil {
				fail(c, http.StatusInternalServerError, "SYNC_QUEUE_FAILED", "配置已更新，但同步任务提交失败", err.Error())
				return
			}
		}
	}
	updated, _ := s.db.Project(c, project.ID)
	c.JSON(http.StatusOK, gin.H{"data": updated})
}

func (s *Server) deleteProject(c *gin.Context) {
	project, ok := s.manageableProject(c)
	if !ok {
		return
	}
	_, err := s.db.DeleteProject(c, project.ID)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	s.docs.RemoveProject(project.ID)
	c.Status(http.StatusNoContent)
}

func (s *Server) addBranch(c *gin.Context) {
	project, ok := s.manageableProject(c)
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请输入分支名称", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if err := docs.ValidateBranch(input.Name); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_BRANCH", err.Error(), "")
		return
	}
	branch, err := s.db.AddBranch(c, project.ID, input.Name)
	if err != nil {
		fail(c, http.StatusConflict, "BRANCH_EXISTS", "分支已存在或无法添加", err.Error())
		return
	}
	job, err := s.enqueue(c, project.ID, branch.Name)
	if err != nil {
		fail(c, http.StatusInternalServerError, "SYNC_QUEUE_FAILED", "分支已添加，但同步任务提交失败", err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": gin.H{"branch": branch, "job": job}})
}

func (s *Server) deleteBranch(c *gin.Context) {
	project, ok := s.manageableProject(c)
	if !ok {
		return
	}
	branch := strings.TrimSpace(c.Query("branch"))
	if branch == "" {
		fail(c, http.StatusBadRequest, "INVALID_BRANCH", "缺少分支名称", "")
		return
	}
	snapshot, err := s.db.DeleteBranch(c, project.ID, branch)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	s.docs.RemoveSnapshot(snapshot)
	c.Status(http.StatusNoContent)
}

func (s *Server) syncBranch(c *gin.Context) {
	project, ok := s.manageableProject(c)
	if !ok {
		return
	}
	var input struct {
		Branch string `json:"branch"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Branch) == "" {
		fail(c, http.StatusBadRequest, "INVALID_BRANCH", "缺少分支名称", "")
		return
	}
	if !hasBranch(project, input.Branch) {
		fail(c, http.StatusNotFound, "BRANCH_NOT_FOUND", "分支未配置", "")
		return
	}
	job, err := s.enqueue(c, project.ID, input.Branch)
	if err != nil {
		fail(c, http.StatusInternalServerError, "SYNC_QUEUE_FAILED", "同步任务提交失败", err.Error())
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": job})
}

func (s *Server) documentTree(c *gin.Context) {
	branch := c.Query("branch")
	if branch == "" {
		fail(c, http.StatusBadRequest, "INVALID_BRANCH", "缺少分支名称", "")
		return
	}
	project, access, ok := s.requireProjectRead(c, c.Param("id"))
	if !ok {
		return
	}
	documents, err := s.db.ListDocuments(c, project.ID, branch, access.full, access.documents)
	if err != nil {
		internal(c, err)
		return
	}
	if documents == nil {
		documents = []store.Document{}
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"docRoots":  visibleDocRoots(project.DocRoots, documents, access.full),
		"documents": documents,
	}})
}

func (s *Server) document(c *gin.Context) {
	branch := c.Query("branch")
	docPath := strings.TrimSpace(c.Query("path"))
	if err := validateDocumentPath(docPath); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error(), "")
		return
	}
	project, access, ok := s.requireProjectRead(c, c.Param("id"))
	if !ok {
		return
	}
	_, documentAllowed := access.documentSet[docPath]
	if !access.full && !documentAllowed {
		fail(c, http.StatusForbidden, "DOCUMENT_FORBIDDEN", "没有该文档的读取权限", "")
		return
	}
	doc, err := s.db.Document(c, project.ID, branch, docPath)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": doc})
}

func (s *Server) downloadDocument(c *gin.Context) {
	branch := c.Query("branch")
	docPath := strings.TrimSpace(c.Query("path"))
	if err := validateDocumentPath(docPath); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error(), "")
		return
	}
	project, access, ok := s.requireProjectRead(c, c.Param("id"))
	if !ok {
		return
	}
	_, documentAllowed := access.documentSet[docPath]
	if !access.full && !documentAllowed {
		fail(c, http.StatusForbidden, "DOCUMENT_FORBIDDEN", "没有该文档的读取权限", "")
		return
	}
	doc, err := s.db.Document(c, project.ID, branch, docPath)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	contentType := "text/markdown; charset=utf-8"
	if doc.Format == store.DocumentFormatHTML {
		contentType = "text/html; charset=utf-8"
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", attachmentDisposition(doc.Path))
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, contentType, []byte(doc.Source()))
}

func (s *Server) asset(c *gin.Context) {
	branch := c.Query("branch")
	relative := strings.TrimPrefix(c.Param("assetPath"), "/")
	s.serveAsset(c, branch, relative)
}

func (s *Server) sandboxAsset(c *gin.Context) {
	grant, err := s.parseHTMLResourceGrant(c.Param("token"))
	if err != nil || grant.ProjectID != c.Param("id") || grant.ExpiresAt <= time.Now().Unix() {
		fail(c, http.StatusForbidden, "INVALID_RESOURCE_GRANT", "HTML 资源凭证无效或已过期", "")
		return
	}
	relative := strings.TrimPrefix(c.Param("assetPath"), "/")
	cleanAsset, err := cleanAssetPath(relative)
	if err != nil {
		fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error(), "")
		return
	}
	exists, err := s.db.DocumentHasAsset(
		c, grant.ProjectID, grant.BranchID, grant.DocumentPath, cleanAsset,
	)
	if err != nil {
		internal(c, err)
		return
	}
	if !exists {
		fail(c, http.StatusForbidden, "ASSET_FORBIDDEN", "该资源不属于当前 HTML 文档", "")
		return
	}
	project, err := s.db.Project(c, grant.ProjectID)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	user, _, err := s.db.UserBySession(c, grant.SessionID)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	if user.ID != grant.UserID {
		fail(c, http.StatusForbidden, "INVALID_RESOURCE_GRANT", "HTML 资源凭证归属无效", "")
		return
	}
	allowed := project.IsPublic || user.Role == "admin" || user.ID == project.OwnerID
	access, err := s.db.ProjectReadAccess(c, project.ID, user.ID)
	if err != nil {
		internal(c, err)
		return
	}
	if !allowed && access.Full {
		allowed = true
	}
	branch, err := s.db.BranchName(c, project.ID, grant.BranchID)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	if !allowed {
		allowed, err = s.db.CanReadDocumentAsset(
			c, project.ID, branch, user.ID, cleanAsset,
		)
		if err != nil {
			internal(c, err)
			return
		}
	}
	if !allowed {
		fail(c, http.StatusForbidden, "ASSET_FORBIDDEN", "没有该资源的读取权限", "")
		return
	}
	s.writeAsset(c, project.ID, branch, relative)
}

func (s *Server) serveAsset(c *gin.Context, branch, relative string) {
	project, access, ok := s.requireProjectRead(c, c.Param("id"))
	if !ok {
		return
	}
	if !access.full {
		cleanAsset, err := cleanAssetPath(relative)
		if err != nil {
			fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error(), "")
			return
		}
		allowed, err := s.db.CanReadDocumentAsset(c, project.ID, branch, currentUser(c).ID, cleanAsset)
		if err != nil {
			internal(c, err)
			return
		}
		if !allowed {
			fail(c, http.StatusForbidden, "ASSET_FORBIDDEN", "没有该资源的读取权限", "")
			return
		}
	}
	s.writeAsset(c, project.ID, branch, relative)
}

func (s *Server) writeAsset(c *gin.Context, projectID, branch, relative string) {
	resolved, err := s.docs.ResolveAsset(c, projectID, branch, relative)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(resolved))
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	if strings.EqualFold(filepath.Ext(resolved), ".html") ||
		strings.EqualFold(filepath.Ext(resolved), ".htm") {
		c.Header("Content-Security-Policy", htmlSandboxPolicy)
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "no-referrer")
	}
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.File(resolved)
}

func (s *Server) htmlPreview(c *gin.Context) {
	docPath := strings.TrimSpace(c.Query("path"))
	if err := validateDocumentPath(docPath); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error(), "")
		return
	}
	project, access, ok := s.requireProjectRead(c, c.Param("id"))
	if !ok {
		return
	}
	branch, ok := s.branchName(c, project.ID, c.Param("branchID"))
	if !ok {
		return
	}
	if _, allowed := access.documentSet[docPath]; !access.full && !allowed {
		fail(c, http.StatusForbidden, "DOCUMENT_FORBIDDEN", "没有该文档的读取权限", "")
		return
	}
	doc, err := s.db.Document(c, project.ID, branch, docPath)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	if doc.Format != store.DocumentFormatHTML {
		fail(c, http.StatusBadRequest, "NOT_HTML_DOCUMENT", "该文档不是 HTML 格式", "")
		return
	}
	resourceToken, err := s.createHTMLResourceGrant(htmlResourceGrant{
		ProjectID:    project.ID,
		BranchID:     c.Param("branchID"),
		DocumentPath: doc.Path,
		UserID:       currentUser(c).ID,
		SessionID:    c.GetString(string(contextSID)),
		ExpiresAt:    time.Now().Add(htmlResourceGrantTTL).Unix(),
	})
	if err != nil {
		internal(c, err)
		return
	}
	baseHref := sandboxAssetBase(project.ID, resourceToken, doc.Path)
	prepared, err := docparse.PrepareHTML(doc.Source(), baseHref)
	if err != nil {
		internal(c, err)
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Content-Disposition", "inline")
	c.Header("Content-Security-Policy", htmlSandboxPolicy)
	c.Header("X-Frame-Options", "SAMEORIGIN")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(prepared))
}

func (s *Server) search(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(http.StatusOK, gin.H{"data": []store.SearchResult{}})
		return
	}
	if len([]rune(query)) > 100 {
		fail(c, http.StatusBadRequest, "QUERY_TOO_LONG", "搜索关键词不能超过 100 个字符", "")
		return
	}
	project, access, ok := s.requireProjectRead(c, c.Param("id"))
	if !ok {
		return
	}
	results, err := s.db.SearchDocuments(
		c, project.ID, c.Query("branch"), query, 20, access.full, access.documents,
	)
	if err != nil {
		internal(c, err)
		return
	}
	if results == nil {
		results = []store.SearchResult{}
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}

func (s *Server) job(c *gin.Context) {
	job, err := s.db.Job(c, c.Param("id"))
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	if _, _, ok := s.requireProjectRead(c, job.ProjectID); !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": job})
}

func (s *Server) userDirectory(c *gin.Context) {
	users, err := s.db.ListUserSummaries(c)
	if err != nil {
		internal(c, err)
		return
	}
	if users == nil {
		users = []store.UserSummary{}
	}
	c.JSON(http.StatusOK, gin.H{"data": users})
}

func (s *Server) listUsers(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	users, err := s.db.ListUsers(c)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": users})
}

func (s *Server) createUser(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "用户参数不完整", err.Error())
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || len(input.Username) > 64 || len(input.Password) < 8 ||
		(input.Role != "admin" && input.Role != "member") {
		fail(c, http.StatusBadRequest, "INVALID_USER", "用户名、至少 8 位密码和有效角色为必填项", "")
		return
	}
	user, err := s.db.CreateUser(c, input.Username, input.Password, input.Role)
	if err != nil {
		fail(c, http.StatusConflict, "CREATE_USER_FAILED", "创建用户失败，用户名可能已存在", err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": user})
}

func (s *Server) updateUserRole(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&input); err != nil ||
		(input.Role != "admin" && input.Role != "member") {
		fail(c, http.StatusBadRequest, "INVALID_USER", "角色必须为管理员或成员", "")
		return
	}
	userID := c.Param("id")
	if userID == currentUser(c).ID {
		fail(c, http.StatusBadRequest, "SELF_ROLE_CHANGE", "不能修改自己的角色", "")
		return
	}
	user, err := s.db.UpdateUserRole(c, userID, input.Role)
	if errors.Is(err, store.ErrNotFound) {
		fail(c, http.StatusNotFound, "USER_NOT_FOUND", "用户不存在", "")
		return
	}
	if errors.Is(err, store.ErrLastAdmin) {
		fail(c, http.StatusConflict, "LAST_ADMIN", err.Error(), "")
		return
	}
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": user})
}

func (s *Server) projectAccess(c *gin.Context) {
	project, ok := s.manageableProject(c)
	if !ok {
		return
	}
	access, err := s.db.ProjectAccess(c, project.ID)
	if err != nil {
		internal(c, err)
		return
	}
	access = normalizeProjectAccess(access)
	c.JSON(http.StatusOK, gin.H{"data": access})
}

func (s *Server) setProjectAccess(c *gin.Context) {
	project, ok := s.manageableProject(c)
	if !ok {
		return
	}
	var input struct {
		UserIDs         []string `json:"userIds"`
		OrganizationIDs []string `json:"organizationIds"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "授权用户参数无效", err.Error())
		return
	}
	if err := s.db.SetProjectReadAccess(
		c,
		project.ID,
		input.UserIDs,
		input.OrganizationIDs,
		currentUser(c).ID,
	); err != nil {
		accessError(c, err)
		return
	}
	s.respondProjectAccess(c, project.ID)
}

func (s *Server) setDocumentAccess(c *gin.Context) {
	project, ok := s.manageableProject(c)
	if !ok {
		return
	}
	var input struct {
		Path            string   `json:"path"`
		UserIDs         []string `json:"userIds"`
		OrganizationIDs []string `json:"organizationIds"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "文档授权参数无效", err.Error())
		return
	}
	input.Path = strings.TrimSpace(input.Path)
	if err := validateDocumentPath(input.Path); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error(), "")
		return
	}
	if err := s.db.SetDocumentReadAccess(
		c,
		project.ID,
		input.Path,
		input.UserIDs,
		input.OrganizationIDs,
		currentUser(c).ID,
	); err != nil {
		accessError(c, err)
		return
	}
	s.respondProjectAccess(c, project.ID)
}

func (s *Server) respondProjectAccess(c *gin.Context, projectID string) {
	access, err := s.db.ProjectAccess(c, projectID)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": normalizeProjectAccess(access)})
}

func normalizeProjectAccess(access store.ProjectAccess) store.ProjectAccess {
	// JSON 接口统一返回空数组，避免前端把 nil 当成“数据尚未加载”。
	if access.ProjectUsers == nil {
		access.ProjectUsers = []store.UserSummary{}
	}
	if access.ProjectOrganizations == nil {
		access.ProjectOrganizations = []store.OrganizationSummary{}
	}
	if access.Documents == nil {
		access.Documents = []store.DocumentAccess{}
	}
	for index := range access.Documents {
		if access.Documents[index].Users == nil {
			access.Documents[index].Users = []store.UserSummary{}
		}
		if access.Documents[index].Organizations == nil {
			access.Documents[index].Organizations = []store.OrganizationSummary{}
		}
	}
	return access
}

func (s *Server) manageableProject(c *gin.Context) (store.Project, bool) {
	requestUser := currentUser(c)
	project, err := s.db.Project(c, c.Param("id"))
	if err != nil {
		respondRecord(c, nil, err)
		return store.Project{}, false
	}
	if requestUser.Role != "admin" && requestUser.ID != project.OwnerID {
		fail(c, http.StatusForbidden, "FORBIDDEN", "仅项目创建者或管理员可以执行此操作", "")
		return store.Project{}, false
	}
	return project, true
}

type readAccess struct {
	full        bool
	documents   []string
	documentSet map[string]struct{}
}

func (s *Server) requireProjectRead(
	c *gin.Context,
	identifier string,
) (store.Project, readAccess, bool) {
	project, err := s.db.Project(c, identifier)
	if err != nil {
		respondRecord(c, nil, err)
		return store.Project{}, readAccess{}, false
	}
	user := currentUser(c)
	// 创建人和管理员不受额外读取授权限制，其余成员先取项目级授权，
	// 没有项目级授权时再按仓库内完整文档路径限定可见范围。
	if project.IsPublic || user.Role == "admin" || user.ID == project.OwnerID {
		return project, readAccess{full: true}, true
	}
	access, err := s.db.ProjectReadAccess(c, project.ID, user.ID)
	if err != nil {
		internal(c, err)
		return store.Project{}, readAccess{}, false
	}
	if !access.Full && len(access.Documents) == 0 {
		fail(c, http.StatusForbidden, "PROJECT_FORBIDDEN", "没有该项目的读取权限", "")
		return store.Project{}, readAccess{}, false
	}
	result := readAccess{full: access.Full, documents: access.Documents}
	if !access.Full {
		result.documentSet = make(map[string]struct{}, len(access.Documents))
		for _, documentPath := range access.Documents {
			result.documentSet[documentPath] = struct{}{}
		}
	}
	return project, result, true
}

func visibleDocRoots(
	docRoots []string,
	documents []store.Document,
	fullAccess bool,
) []string {
	if fullAccess {
		return docRoots
	}
	var visible []string
	for _, docRoot := range docRoots {
		prefix := docRoot + "/"
		for _, document := range documents {
			if strings.HasPrefix(document.Path, prefix) {
				visible = append(visible, docRoot)
				break
			}
		}
	}
	if len(visible) == 0 && len(docRoots) == 1 && len(documents) > 0 {
		return docRoots
	}
	return visible
}

func cleanAssetPath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') ||
		strings.HasPrefix(value, "/") || path.Clean(value) != value ||
		value == ".." || strings.HasPrefix(value, "../") {
		return "", errors.New("资源路径无效")
	}
	return value, nil
}

func (s *Server) createHTMLResourceGrant(grant htmlResourceGrant) (string, error) {
	payload, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	return s.secrets.Encrypt(string(payload))
}

func (s *Server) parseHTMLResourceGrant(value string) (htmlResourceGrant, error) {
	payload, err := s.secrets.Decrypt(value)
	if err != nil {
		return htmlResourceGrant{}, err
	}
	var grant htmlResourceGrant
	if err := json.Unmarshal([]byte(payload), &grant); err != nil {
		return htmlResourceGrant{}, err
	}
	if grant.ProjectID == "" || grant.BranchID == "" || grant.DocumentPath == "" ||
		grant.UserID == "" || grant.SessionID == "" || grant.ExpiresAt == 0 {
		return htmlResourceGrant{}, errors.New("资源凭证内容不完整")
	}
	return grant, nil
}

func (s *Server) branchName(c *gin.Context, projectID, branchID string) (string, bool) {
	branch, err := s.db.BranchName(c, projectID, branchID)
	if err != nil {
		respondRecord(c, nil, err)
		return "", false
	}
	return branch, true
}

func sandboxAssetBase(projectID, resourceToken, docPath string) string {
	base := "/api/projects/" + url.PathEscape(projectID) +
		"/sandbox-assets/" + url.PathEscape(resourceToken) + "/"
	directory := path.Dir(docPath)
	if directory == "." {
		return base
	}
	parts := strings.Split(directory, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return base + strings.Join(parts, "/") + "/"
}

func accessError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotFound) {
		fail(c, http.StatusBadRequest, "INVALID_USER", "授权用户不存在", "")
		return
	}
	if errors.Is(err, store.ErrOrganizationNotFound) {
		fail(c, http.StatusBadRequest, "INVALID_ORGANIZATION", "授权组织机构不存在", "")
		return
	}
	internal(c, err)
}

func (s *Server) enqueue(c *gin.Context, projectID, branch string) (store.SyncJob, error) {
	job, err := s.db.CreateJob(c, projectID, branch)
	if err != nil {
		return store.SyncJob{}, err
	}
	s.docs.Enqueue(job)
	return job, nil
}

func (s *Server) mountFrontend(router *gin.Engine) {
	indexPath := filepath.Join(s.cfg.WebDir, "index.html")
	assetsPath := filepath.Join(s.cfg.WebDir, "assets")
	if info, err := os.Stat(assetsPath); err == nil && info.IsDir() {
		router.Static("/assets", assetsPath)
	}
	for _, name := range []string{"favicon.svg", "robots.txt"} {
		filePath := filepath.Join(s.cfg.WebDir, name)
		if _, err := os.Stat(filePath); err == nil {
			router.StaticFile("/"+name, filePath)
		}
	}
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			fail(c, http.StatusNotFound, "NOT_FOUND", "接口不存在", "")
			return
		}
		if _, err := os.Stat(indexPath); err != nil {
			fail(c, http.StatusNotFound, "FRONTEND_NOT_BUILT", "前端资源尚未构建", "")
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.File(indexPath)
	})
}

func normalizeBranches(values []string) ([]string, string) {
	seen := make(map[string]struct{})
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if err := docs.ValidateBranch(value); err != nil {
			return nil, "分支名称无效: " + value
		}
		if _, exists := seen[value]; exists {
			return nil, "分支名称不能重复: " + value
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, "至少配置一个分支"
	}
	if len(result) > 30 {
		return nil, "单个项目最多配置 30 个分支"
	}
	return result, ""
}

func validateDocumentPath(value string) error {
	if value == "" || strings.ContainsRune(value, '\x00') ||
		strings.HasPrefix(value, "/") || path.Clean(value) != value ||
		value == ".." || strings.HasPrefix(value, "../") {
		return errors.New("文档路径无效")
	}
	return nil
}

func attachmentDisposition(docPath string) string {
	filename := safeAttachmentFilename(path.Base(strings.ReplaceAll(docPath, "\\", "/")))
	fallbackRunes := make([]rune, 0, len(filename))
	for _, char := range filename {
		if char >= 0x20 && char <= 0x7e && char != '"' && char != '\\' {
			fallbackRunes = append(fallbackRunes, char)
		} else {
			fallbackRunes = append(fallbackRunes, '_')
		}
	}
	fallback := strings.TrimSpace(string(fallbackRunes))
	if fallback == "" {
		fallback = "document.md"
	}
	return fmt.Sprintf(
		`attachment; filename="%s"; filename*=UTF-8''%s`,
		fallback,
		url.PathEscape(filename),
	)
}

func safeAttachmentFilename(filename string) string {
	filename = strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f || char == '/' || char == '\\' {
			return '_'
		}
		return char
	}, filename)
	filename = strings.TrimSpace(filename)
	if filename == "" || filename == "." || filename == ".." {
		return "document.md"
	}
	return filename
}

func hasBranch(project store.Project, name string) bool {
	for _, branch := range project.Branches {
		if branch.Name == name {
			return true
		}
	}
	return false
}

func currentUser(c *gin.Context) store.User {
	value, _ := c.Get(string(contextUser))
	user, _ := value.(store.User)
	return user
}

func requireAdmin(c *gin.Context) bool {
	if currentUser(c).Role != "admin" {
		fail(c, http.StatusForbidden, "FORBIDDEN", "仅管理员可以执行此操作", "")
		return false
	}
	return true
}

func randomToken(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func firstError(errorsToCheck ...error) error {
	for _, err := range errorsToCheck {
		if err != nil {
			return err
		}
	}
	return nil
}

func respondRecord(c *gin.Context, value any, err error) {
	if errors.Is(err, store.ErrNotFound) {
		fail(c, http.StatusNotFound, "NOT_FOUND", "记录不存在或尚未同步", "")
		return
	}
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": value})
}

func internal(c *gin.Context, err error) {
	fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "服务暂时不可用", err.Error())
}

func fail(c *gin.Context, status int, code, message, details string) {
	errorBody := gin.H{"code": code, "message": message}
	if details != "" {
		errorBody["details"] = truncate(details, 800)
	}
	c.JSON(status, gin.H{"error": errorBody})
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Next()
	}
}

func limitRequestBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func isSafeHTTPMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func supportsBasicAuth(method, requestPath string) bool {
	return isSafeHTTPMethod(method) && strings.HasPrefix(requestPath, "/api/projects/") &&
		(strings.Contains(requestPath, "/assets/") || strings.HasSuffix(requestPath, "/doc/download"))
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func supportsAccessToken(method, requestPath string) bool {
	if !isSafeHTTPMethod(method) {
		return false
	}
	parts := strings.Split(strings.Trim(requestPath, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" || parts[1] != "projects" {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	if len(parts) == 3 {
		return true
	}
	switch parts[3] {
	case "tree", "doc", "download", "assets", "search":
		return true
	default:
		return false
	}
}

func challengeBasicAuth(c *gin.Context) {
	c.Header("WWW-Authenticate", `Basic realm="DocHub", charset="UTF-8"`)
}

func init() {
	gin.SetMode(gin.ReleaseMode)
}
