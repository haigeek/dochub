package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dochub/internal/config"
	"dochub/internal/docs"
	"dochub/internal/store"
)

func TestOrganizationAccessAndManagement(t *testing.T) {
	db := oauthTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := db.CreateUser(ctx, "org-admin", "organization-test-password", "admin")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := db.CreateUser(ctx, "org-owner", "organization-test-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := db.CreateUser(ctx, "org-reader", "organization-test-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.CreateProject(ctx, "Organization", "git@git.test:team/organization.git",
		[]string{"docs"}, owner.ID, []string{"main"}, false)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		StorageDir:       t.TempDir(),
		SessionTTL:       time.Hour,
		GitHostAllowlist: []string{"git.test"},
	}
	service, err := docs.NewService(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := t.TempDir()
	snapshot, err = filepath.EvalSymlinks(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(snapshot, "docs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(snapshot, "docs", "asset.txt"),
		[]byte("organization asset"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishBranchSync(ctx, project.ID, "main", "organization-commit", snapshot, []store.DocFile{
		{
			Path:      "docs/guide.md",
			Title:     "Organization Guide",
			Markdown:  "# Organization Guide\n[asset](asset.txt)",
			PlainText: "Organization Guide shared",
			Assets:    []string{"docs/asset.txt"},
		},
		{
			Path:      "docs/secret.md",
			Title:     "Organization Secret",
			Markdown:  "# Organization Secret",
			PlainText: "private secret",
		},
	}); err != nil {
		t.Fatal(err)
	}

	router := New(cfg, db, service).Router()
	adminCookie, adminCSRF := loginForTest(t, router, admin.Username, "organization-test-password")
	ownerCookie, ownerCSRF := loginForTest(t, router, owner.Username, "organization-test-password")
	readerCookie, _ := loginForTest(t, router, reader.Username, "organization-test-password")
	base := "/api/projects/" + project.ID
	request := func(
		method, path string,
		body any,
		cookie *http.Cookie,
		csrf string,
	) *httptest.ResponseRecorder {
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	decodeData := func(response *httptest.ResponseRecorder, target any) {
		t.Helper()
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			t.Fatal(err)
		}
	}

	createPayload := map[string]any{
		"name":    "Infrastructure",
		"userIds": []string{reader.ID},
	}
	if response := request(
		http.MethodPost,
		"/api/admin/organizations",
		createPayload,
		readerCookie,
		"",
	); response.Code != http.StatusForbidden {
		t.Fatalf("普通成员不应创建组织: %d %s", response.Code, response.Body.String())
	}
	response := request(
		http.MethodPost,
		"/api/admin/organizations",
		createPayload,
		adminCookie,
		adminCSRF,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("创建组织失败: %d %s", response.Code, response.Body.String())
	}
	var organization store.Organization
	decodeData(response, &organization)
	if organization.Name != "Infrastructure" || len(organization.Members) != 1 ||
		organization.Members[0].ID != reader.ID {
		t.Fatalf("创建组织返回错误: %#v", organization)
	}
	response = request(
		http.MethodPut,
		"/api/admin/organizations/"+organization.ID,
		map[string]any{
			"name":    "Infrastructure Platform",
			"userIds": []string{reader.ID},
		},
		adminCookie,
		adminCSRF,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("更新组织失败: %d %s", response.Code, response.Body.String())
	}
	decodeData(response, &organization)
	if organization.Name != "Infrastructure Platform" || len(organization.Members) != 1 {
		t.Fatalf("更新组织返回错误: %#v", organization)
	}
	if response := request(
		http.MethodPost,
		"/api/admin/organizations",
		map[string]any{"name": "infrastructure platform", "userIds": []string{}},
		adminCookie,
		adminCSRF,
	); response.Code != http.StatusConflict {
		t.Fatalf("组织名称应按忽略大小写判重: %d %s", response.Code, response.Body.String())
	}

	setOrganizationDocumentAccess := func(userIDs, organizationIDs []string) *httptest.ResponseRecorder {
		return request(
			http.MethodPut,
			base+"/access/document",
			map[string]any{
				"path":            "docs/guide.md",
				"userIds":         userIDs,
				"organizationIds": organizationIDs,
			},
			ownerCookie,
			ownerCSRF,
		)
	}
	if response := setOrganizationDocumentAccess(nil, []string{organization.ID}); response.Code != http.StatusOK {
		t.Fatalf("设置文档组织授权失败: %d %s", response.Code, response.Body.String())
	}
	accessResponse := request(http.MethodGet, base+"/access", nil, ownerCookie, "")
	if accessResponse.Code != http.StatusOK {
		t.Fatalf("读取项目授权失败: %d %s", accessResponse.Code, accessResponse.Body.String())
	}
	var projectAccess store.ProjectAccess
	decodeData(accessResponse, &projectAccess)
	if len(projectAccess.Documents) != 1 ||
		projectAccess.Documents[0].Path != "docs/guide.md" ||
		len(projectAccess.Documents[0].Organizations) != 1 ||
		projectAccess.Documents[0].Organizations[0].ID != organization.ID {
		t.Fatalf("文档组织授权响应错误: %#v", projectAccess)
	}

	assertDocumentAccess := func(wantGuide, wantSecret int) {
		t.Helper()
		for path, want := range map[string]int{
			base + "/doc?branch=main&path=docs/guide.md":          wantGuide,
			base + "/doc?branch=main&path=docs/secret.md":         wantSecret,
			base + "/doc/download?branch=main&path=docs/guide.md": wantGuide,
		} {
			if response := request(http.MethodGet, path, nil, readerCookie, ""); response.Code != want {
				t.Fatalf("%s: want %d, got %d: %s", path, want, response.Code, response.Body.String())
			}
		}
		if response := request(
			http.MethodGet,
			base+"/assets/docs/asset.txt?branch=main",
			nil,
			readerCookie,
			"",
		); response.Code != wantGuide {
			t.Fatalf("组织文档资源权限错误: %d %s", response.Code, response.Body.String())
		}
	}
	assertDocumentAccess(http.StatusOK, http.StatusForbidden)
	basicRequest := httptest.NewRequest(
		http.MethodGet,
		base+"/doc?branch=main&path=docs/guide.md",
		nil,
	)
	basicRequest.SetBasicAuth(reader.Username, "organization-test-password")
	basicResponse := httptest.NewRecorder()
	router.ServeHTTP(basicResponse, basicRequest)
	if basicResponse.Code != http.StatusOK {
		t.Fatalf("Basic Auth 应遵循组织文档授权: %d %s", basicResponse.Code, basicResponse.Body.String())
	}

	treeResponse := request(http.MethodGet, base+"/tree?branch=main", nil, readerCookie, "")
	if treeResponse.Code != http.StatusOK ||
		!strings.Contains(treeResponse.Body.String(), "docs/guide.md") ||
		strings.Contains(treeResponse.Body.String(), "docs/secret.md") {
		t.Fatalf("组织文档授权的目录树错误: %d %s", treeResponse.Code, treeResponse.Body.String())
	}
	searchResponse := request(
		http.MethodGet,
		base+"/search?branch=main&q=Organization",
		nil,
		readerCookie,
		"",
	)
	if searchResponse.Code != http.StatusOK ||
		!strings.Contains(searchResponse.Body.String(), "docs/guide.md") ||
		strings.Contains(searchResponse.Body.String(), "docs/secret.md") {
		t.Fatalf("组织文档授权的搜索错误: %d %s", searchResponse.Code, searchResponse.Body.String())
	}

	if response := request(
		http.MethodPut,
		"/api/admin/users/"+reader.ID+"/organizations",
		map[string]any{"organizationIds": []string{}},
		adminCookie,
		adminCSRF,
	); response.Code != http.StatusNoContent {
		t.Fatalf("清空用户组织失败: %d %s", response.Code, response.Body.String())
	}
	assertDocumentAccess(http.StatusForbidden, http.StatusForbidden)

	if response := setOrganizationDocumentAccess([]string{reader.ID}, nil); response.Code != http.StatusOK {
		t.Fatalf("设置个人文档授权失败: %d %s", response.Code, response.Body.String())
	}
	assertDocumentAccess(http.StatusOK, http.StatusForbidden)

	if response := request(
		http.MethodPut,
		"/api/admin/users/"+reader.ID+"/organizations",
		map[string]any{"organizationIds": []string{organization.ID}},
		adminCookie,
		adminCSRF,
	); response.Code != http.StatusNoContent {
		t.Fatalf("恢复用户组织失败: %d %s", response.Code, response.Body.String())
	}
	if response := setOrganizationDocumentAccess(nil, nil); response.Code != http.StatusOK {
		t.Fatalf("清空文档授权失败: %d %s", response.Code, response.Body.String())
	}
	if response := request(
		http.MethodPut,
		base+"/access/project",
		map[string]any{
			"userIds":         []string{},
			"organizationIds": []string{organization.ID},
		},
		ownerCookie,
		ownerCSRF,
	); response.Code != http.StatusOK {
		t.Fatalf("设置项目组织授权失败: %d %s", response.Code, response.Body.String())
	}
	accessResponse = request(http.MethodGet, base+"/access", nil, ownerCookie, "")
	decodeData(accessResponse, &projectAccess)
	if len(projectAccess.ProjectOrganizations) != 1 ||
		projectAccess.ProjectOrganizations[0].ID != organization.ID {
		t.Fatalf("项目组织授权响应错误: %#v", projectAccess)
	}
	if response := request(
		http.MethodGet,
		base+"/doc?branch=main&path=docs/secret.md",
		nil,
		readerCookie,
		"",
	); response.Code != http.StatusOK {
		t.Fatalf("项目组织授权未覆盖全项目: %d %s", response.Code, response.Body.String())
	}
	projectsResponse := request(http.MethodGet, "/api/projects", nil, readerCookie, "")
	if !strings.Contains(projectsResponse.Body.String(), project.ID) {
		t.Fatalf("组织项目授权后项目列表不可见: %s", projectsResponse.Body.String())
	}

	if response := request(
		http.MethodDelete,
		"/api/admin/organizations/"+organization.ID,
		nil,
		adminCookie,
		adminCSRF,
	); response.Code != http.StatusNoContent {
		t.Fatalf("删除组织失败: %d %s", response.Code, response.Body.String())
	}
	if response := request(
		http.MethodGet,
		base+"/doc?branch=main&path=docs/guide.md",
		nil,
		readerCookie,
		"",
	); response.Code != http.StatusForbidden {
		t.Fatalf("删除组织后授权应级联失效: %d %s", response.Code, response.Body.String())
	}
}
