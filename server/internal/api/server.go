package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"dochub/internal/config"
	"dochub/internal/docs"
	"dochub/internal/store"
)

const sessionCookie = "dochub_session"

type contextKey string

const (
	contextUser contextKey = "user"
	contextCSRF contextKey = "csrf"
	contextSID  contextKey = "session-id"
)

type Server struct {
	cfg  config.Config
	db   *store.Store
	docs *docs.Service
}

func New(cfg config.Config, db *store.Store, docsService *docs.Service) *Server {
	return &Server{cfg: cfg, db: db, docs: docsService}
}

func (s *Server) Router() *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery(), securityHeaders())
	_ = router.SetTrustedProxies(nil)
	router.Use(limitRequestBody(1 << 20))

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := router.Group("/api")
	api.POST("/auth/login", s.login)
	protected := api.Group("")
	protected.Use(s.authenticate())
	protected.GET("/auth/me", s.me)
	protected.POST("/auth/logout", s.logout)

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
	protected.GET("/projects/:id/assets/*assetPath", s.asset)
	protected.GET("/projects/:id/search", s.search)
	protected.GET("/jobs/:id", s.job)

	protected.GET("/users", s.listUsers)
	protected.POST("/users", s.createUser)

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
	token := randomToken(32)
	csrf := randomToken(24)
	expiresAt := time.Now().Add(s.cfg.SessionTTL)
	if err := s.db.CreateSession(c, user.ID, hashToken(token), csrf, expiresAt); err != nil {
		fail(c, http.StatusInternalServerError, "SESSION_FAILED", "登录失败", err.Error())
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"user": user, "csrfToken": csrf}})
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

func (s *Server) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookie)
		if err != nil || token == "" {
			fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "请先登录", "")
			c.Abort()
			return
		}
		user, csrf, err := s.db.UserBySession(c, hashToken(token))
		if err != nil {
			fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "登录已过期，请重新登录", "")
			c.Abort()
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead &&
			c.GetHeader("X-CSRF-Token") != csrf {
			fail(c, http.StatusForbidden, "INVALID_CSRF", "请求校验失败，请刷新页面后重试", "")
			c.Abort()
			return
		}
		c.Set(string(contextUser), user)
		c.Set(string(contextCSRF), csrf)
		c.Set(string(contextSID), hashToken(token))
		c.Next()
	}
}

func (s *Server) listProjects(c *gin.Context) {
	projects, err := s.db.ListProjects(c)
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
		Name     string   `json:"name"`
		RepoURL  string   `json:"repoUrl"`
		DocRoot  string   `json:"docRoot"`
		DocRoots []string `json:"docRoots"`
		Branches []string `json:"branches"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "项目参数不完整", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
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
	project, err := s.db.CreateProject(c, input.Name, input.RepoURL, docRoots, user.ID, branches)
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
	project, err := s.db.Project(c, c.Param("id"))
	respondRecord(c, project, err)
}

func (s *Server) updateProject(c *gin.Context) {
	project, ok := s.manageableProject(c)
	if !ok {
		return
	}
	var input struct {
		Name     string   `json:"name"`
		RepoURL  string   `json:"repoUrl"`
		DocRoot  string   `json:"docRoot"`
		DocRoots []string `json:"docRoots"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "项目参数不完整", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.RepoURL = strings.TrimSpace(input.RepoURL)
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
	if err := firstError(
		docs.ValidateRepoURL(input.RepoURL, s.cfg.GitHostAllowlist),
	); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_PROJECT", err.Error(), "")
		return
	}
	configChanged := input.RepoURL != project.RepoURL || !slices.Equal(docRoots, project.DocRoots)
	if err := s.db.UpdateProject(c, project.ID, input.Name, input.RepoURL, docRoots); err != nil {
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
	project, err := s.db.Project(c, c.Param("id"))
	if err != nil {
		respondRecord(c, nil, err)
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
	project, err := s.db.Project(c, c.Param("id"))
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	documents, err := s.db.ListDocuments(c, project.ID, branch)
	if err != nil {
		internal(c, err)
		return
	}
	if documents == nil {
		documents = []store.Document{}
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"docRoots": project.DocRoots, "documents": documents}})
}

func (s *Server) document(c *gin.Context) {
	branch := c.Query("branch")
	docPath := strings.TrimSpace(c.Query("path"))
	if err := validateDocumentPath(docPath); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error(), "")
		return
	}
	doc, err := s.db.Document(c, c.Param("id"), branch, docPath)
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
	doc, err := s.db.Document(c, c.Param("id"), branch, docPath)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	c.Header("Content-Type", "text/markdown; charset=utf-8")
	c.Header("Content-Disposition", attachmentDisposition(doc.Path))
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(doc.Markdown))
}

func (s *Server) asset(c *gin.Context) {
	branch := c.Query("branch")
	relative := strings.TrimPrefix(c.Param("assetPath"), "/")
	resolved, err := s.docs.ResolveAsset(c, c.Param("id"), branch, relative)
	if err != nil {
		respondRecord(c, nil, err)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(resolved))
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.File(resolved)
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
	results, err := s.db.SearchDocuments(c, c.Param("id"), c.Query("branch"), query, 20)
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
	respondRecord(c, job, err)
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

func init() {
	gin.SetMode(gin.ReleaseMode)
}
