package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"dochub/internal/store"
)

func (s *Server) organizationDirectory(c *gin.Context) {
	organizations, err := s.db.ListOrganizationSummaries(c)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": organizations})
}

func (s *Server) listOrganizations(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	organizations, err := s.db.ListOrganizations(c)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": organizations})
}

func (s *Server) createOrganization(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	var input organizationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "组织机构参数不完整", err.Error())
		return
	}
	if message := normalizeOrganizationInput(&input); message != "" {
		fail(c, http.StatusBadRequest, "INVALID_ORGANIZATION", message, "")
		return
	}
	organization, err := s.db.CreateOrganization(c, input.Name, input.UserIDs)
	if err != nil {
		organizationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": organization})
}

func (s *Server) updateOrganization(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	var input organizationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "组织机构参数不完整", err.Error())
		return
	}
	if message := normalizeOrganizationInput(&input); message != "" {
		fail(c, http.StatusBadRequest, "INVALID_ORGANIZATION", message, "")
		return
	}
	organization, err := s.db.UpdateOrganization(c, c.Param("id"), input.Name, input.UserIDs)
	if err != nil {
		organizationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": organization})
}

func (s *Server) deleteOrganization(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	if err := s.db.DeleteOrganization(c, c.Param("id")); err != nil {
		organizationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) setUserOrganizations(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	var input struct {
		OrganizationIDs []string `json:"organizationIds"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "组织归属参数无效", err.Error())
		return
	}
	if err := s.db.SetUserOrganizations(c, c.Param("id"), input.OrganizationIDs); err != nil {
		organizationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type organizationInput struct {
	Name    string   `json:"name"`
	UserIDs []string `json:"userIds"`
}

func normalizeOrganizationInput(input *organizationInput) string {
	input.Name = strings.TrimSpace(input.Name)
	length := utf8.RuneCountInString(input.Name)
	if length == 0 || length > 100 {
		return "组织机构名称长度应为 1-100 个字符"
	}
	return ""
}

func organizationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrOrganizationNameExists):
		fail(c, http.StatusConflict, "ORGANIZATION_NAME_EXISTS", err.Error(), "")
	case errors.Is(err, store.ErrOrganizationNotFound):
		fail(c, http.StatusNotFound, "ORGANIZATION_NOT_FOUND", err.Error(), "")
	case errors.Is(err, store.ErrNotFound):
		fail(c, http.StatusBadRequest, "INVALID_USER", "组织成员不存在", "")
	default:
		internal(c, err)
	}
}
