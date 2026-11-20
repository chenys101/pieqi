package api

import (
	"net/http"
	"strings"

	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

func (s *Server) getRequirementPlan(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not configured"})
		return
	}
	draft, pin, revs, blockers, err := s.requirements.GetPlanView(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"draft": draft, "execution_pin": pin, "revisions": revs, "publish_blocked": blockers,
	})
}

func (s *Server) putRequirementPlanDraft(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not configured"})
		return
	}
	var body model.PlanDraft
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := s.requirements.SavePlanDraft(c.Param("id"), &body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_, _, _, pubBlocked, _ := s.requirements.GetPlanView(c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"draft": out, "publish_blocked": pubBlocked})
}

func (s *Server) publishRequirementPlan(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not configured"})
		return
	}
	var body struct {
		Actor       string `json:"actor"`
		Materialize bool   `json:"materialize"`
		Force       bool   `json:"force"`
	}
	_ = c.ShouldBindJSON(&body)
	if q := strings.TrimSpace(c.Query("materialize")); q == "1" || strings.EqualFold(q, "true") {
		body.Materialize = true
	}
	if q := strings.TrimSpace(c.Query("force")); q == "1" || strings.EqualFold(q, "true") {
		body.Force = true
	}
	rid := c.Param("id")
	if body.Materialize {
		rev, mat, err := s.requirements.PublishAndMaterialize(rid, body.Actor, body.Force)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "force") {
				status = http.StatusConflict
			}
			_, _, _, blockers, _ := s.requirements.GetPlanView(rid)
			c.JSON(status, gin.H{"error": err.Error(), "publish_blocked": blockers})
			return
		}
		c.JSON(http.StatusOK, gin.H{"revision": rev, "materialize": mat})
		return
	}
	rev, err := s.requirements.PublishPlan(rid, body.Actor)
	if err != nil {
		_, _, _, blockers, _ := s.requirements.GetPlanView(rid)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "publish_blocked": blockers})
		return
	}
	c.JSON(http.StatusOK, gin.H{"revision": rev})
}

func (s *Server) listRequirementPlanRevisions(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not configured"})
		return
	}
	r, ok := s.requirements.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "requirement not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"revisions": r.PlanRevisions, "execution_pin": r.ExecutionPin})
}

func (s *Server) pinRequirementPlan(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not configured"})
		return
	}
	var body struct {
		RevisionID string `json:"revision_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	pin, err := s.requirements.PinPlanRevision(c.Param("id"), body.RevisionID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"execution_pin": pin})
}

func (s *Server) materializeRequirementPlan(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not configured"})
		return
	}
	var body struct {
		Force bool `json:"force"`
	}
	_ = c.ShouldBindJSON(&body)
	if q := strings.TrimSpace(c.Query("force")); q == "1" || strings.EqualFold(q, "true") {
		body.Force = true
	}
	out, err := s.requirements.MaterializePinnedPlan(c.Param("id"), body.Force)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "force") {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) addRequirementPlanChange(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not configured"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
		Actor  string `json:"actor"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ch, err := s.requirements.AddPlanChange(c.Param("id"), body.Reason, body.Actor)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"change": ch})
}
