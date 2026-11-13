package api

import (
	"net/http"
	"strings"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

type createRequirementReq struct {
	Title       string                 `json:"title"`
	Body        string                 `json:"body"`
	ProjectPath string                 `json:"project_path" binding:"required"`
	ProjectID   string                 `json:"project_id"`
	Acceptance  []model.AcceptanceItem `json:"acceptance"`
	Path        model.ExecutionPath    `json:"path"`
	WorkflowID  string                 `json:"workflow_id"`
}

type patchRequirementReq struct {
	Title      *string                 `json:"title"`
	Body       *string                 `json:"body"`
	Status     *model.RequirementStatus `json:"status"`
	Acceptance *[]model.AcceptanceItem `json:"acceptance"`
	WorkflowID *string                 `json:"workflow_id"`
	Path       *model.ExecutionPath    `json:"path"`
}

// listRequirements GET /api/requirements
func (s *Server) listRequirements(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusOK, gin.H{"requirements": []any{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"requirements": s.requirements.List()})
}

// createRequirement POST /api/requirements
func (s *Server) createRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req createRequirementReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r, err := s.requirements.Create(&model.Requirement{
		Title:       strings.TrimSpace(req.Title),
		Body:        req.Body,
		ProjectPath: strings.TrimSpace(req.ProjectPath),
		ProjectID:   req.ProjectID,
		Acceptance:  req.Acceptance,
		Path:        req.Path,
		WorkflowID:  strings.TrimSpace(req.WorkflowID),
	})
	if err != nil {
		if dup, ok := err.(*core.ErrDuplicateRequirement); ok && dup.Existing != nil {
			c.JSON(http.StatusConflict, gin.H{
				"error":               err.Error(),
				"code":                "duplicate_requirement",
				"existing_id":         dup.Existing.ID,
				"existing_title":      dup.Existing.Title,
				"existing_status":     dup.Existing.Status,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, r)
}

// getRequirement GET /api/requirements/:id
func (s *Server) getRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	r, ok := s.requirements.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, r)
}

// patchRequirement PATCH /api/requirements/:id
func (s *Server) patchRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req patchRequirementReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rid := c.Param("id")
	if req.WorkflowID != nil {
		allow, _, reason := s.requirements.CanChangeWorkflow(rid)
		if !allow {
			c.JSON(http.StatusConflict, gin.H{"error": reason, "code": "workflow_locked"})
			return
		}
		wid := strings.TrimSpace(*req.WorkflowID)
		if wid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "workflow_id required"})
			return
		}
		if s.workflows != nil {
			p, err := s.workflows.Get(wid)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			wid = p.ID
			ver := p.EffectiveVersion()
			req.WorkflowID = &wid
			// stash version via closure
			updated, err := s.requirements.Update(rid, func(r *model.Requirement) bool {
				r.WorkflowID = wid
				r.WorkflowVersion = ver
				if req.Path != nil {
					r.Path = *req.Path
				}
				if req.Title != nil {
					r.Title = strings.TrimSpace(*req.Title)
				}
				if req.Body != nil {
					r.Body = *req.Body
				}
				if req.Status != nil {
					r.Status = *req.Status
				}
				if req.Acceptance != nil {
					r.Acceptance = *req.Acceptance
				}
				return true
			})
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, updated)
			return
		}
	}
	updated, err := s.requirements.Update(rid, func(r *model.Requirement) bool {
		changed := false
		if req.Title != nil {
			r.Title = strings.TrimSpace(*req.Title)
			changed = true
		}
		if req.Body != nil {
			r.Body = *req.Body
			changed = true
		}
		if req.Status != nil {
			r.Status = *req.Status
			changed = true
		}
		if req.Acceptance != nil {
			r.Acceptance = *req.Acceptance
			changed = true
		}
		if req.Path != nil {
			r.Path = *req.Path
			changed = true
		}
		return changed
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// getReviewCapacity GET /api/requirements/capacity
func (s *Server) getReviewCapacity(c *gin.Context) {
	n := 0
	if s.store != nil {
		n = core.CountHighStakesApprovals(s.store)
	}
	c.JSON(http.StatusOK, gin.H{
		"high_stakes_pending": n,
		"blocked":             n >= 2,
	})
}

// deleteRequirement DELETE /api/requirements/:id
func (s *Server) deleteRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	if err := s.requirements.Delete(c.Param("id")); err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
