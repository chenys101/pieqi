package api

import (
	"net/http"
	"strings"

	"pieqi/internal/core"
	"pieqi/internal/workflow"

	"github.com/gin-gonic/gin"
)

// SetWorkflowRegistry wires workflow packs（设置页 / 新建需求选用）。
func (s *Server) SetWorkflowRegistry(r *workflow.Registry) {
	s.workflows = r
}

// listWorkflows GET /api/workflows
func (s *Server) listWorkflows(c *gin.Context) {
	if s.workflows == nil {
		c.JSON(http.StatusOK, gin.H{"workflows": []any{}, "default_id": "", "user_dir": ""})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"workflows":  s.workflows.List(),
		"default_id": s.workflows.DefaultID(),
		"user_dir":   s.workflows.UserDir(),
	})
}

// getWorkflow GET /api/workflows/:id
func (s *Server) getWorkflow(c *gin.Context) {
	if s.workflows == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workflows not wired"})
		return
	}
	d, err := s.workflows.Detail(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

// reloadWorkflows POST /api/workflows/reload
func (s *Server) reloadWorkflows(c *gin.Context) {
	if s.workflows == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workflows not wired"})
		return
	}
	if err := s.workflows.Reload(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s.settings != nil {
		id := strings.TrimSpace(s.settings.Get().DefaultWorkflowID)
		if id != "" {
			_ = s.workflows.SetDefaultID(id)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"workflows":  s.workflows.List(),
		"default_id": s.workflows.DefaultID(),
		"user_dir":   s.workflows.UserDir(),
	})
}

// setDefaultWorkflow POST /api/workflows/default  body: { "id": "s1-plan-serial" }
func (s *Server) setDefaultWorkflow(c *gin.Context) {
	if s.workflows == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workflows not wired"})
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := strings.TrimSpace(body.ID)
	if err := s.workflows.SetDefaultID(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	def := s.workflows.DefaultID()
	if s.settings != nil {
		if _, err := s.settings.Patch(core.SettingsPatch{DefaultWorkflowID: &def}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"default_id": def})
}
