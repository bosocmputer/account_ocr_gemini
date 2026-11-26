package shopbrain

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func (h *Handlers) currentKnowledge(ctx context.Context, shopID string) ([]KnowledgeItem, error) {
	if h.Knowledge == nil {
		return nil, nil
	}
	return h.Knowledge.ListKnowledge(ctx, shopID)
}

func (h *Handlers) auditUsername(ctx context.Context, token string) string {
	if verifier, ok := h.Svc.Verifier.(interface {
		username(context.Context, string) string
	}); ok {
		return verifier.username(ctx, token)
	}
	return "unknown"
}

func (h *Handlers) authorizeKnowledge(c *gin.Context) (string, bool) {
	shopID, ok := h.authorize(c)
	if ok && h.Knowledge == nil {
		knowledgeError(c, http.StatusServiceUnavailable, "ระบบเก็บความรู้ของร้านยังไม่พร้อม")
		return "", false
	}
	return shopID, ok
}

func knowledgeError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"success": false, "message": message})
}

func knowledgeStoreError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errKnowledgeNotFound):
		knowledgeError(c, http.StatusNotFound, "ไม่พบความรู้รายการนี้")
	case errors.Is(err, errKnowledgeConflict):
		knowledgeError(c, http.StatusConflict, "รายการถูกแก้ไขแล้ว กรุณาโหลดข้อมูลล่าสุดก่อนแก้ไข")
	default:
		log.Printf("shopbrain: knowledge store request failed: %T", err)
		knowledgeError(c, http.StatusServiceUnavailable, "ระบบเก็บความรู้ของร้านไม่พร้อม กรุณาลองใหม่")
	}
}

func (h *Handlers) ListShopKnowledge(c *gin.Context) {
	shopID, ok := h.authorizeKnowledge(c)
	if !ok {
		return
	}
	items, err := h.currentKnowledge(c.Request.Context(), shopID)
	if err != nil {
		knowledgeStoreError(c, err)
		return
	}
	b, ok := h.brain(c, shopID)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "profile": profileOf(b.Master.Shop, knowledgeAliases(items)), "warnings": b.knowledgeWarnings(items, nil)}})
}

type knowledgeInput struct {
	Kind     string        `json:"kind"`
	Data     KnowledgeData `json:"data"`
	Note     string        `json:"note"`
	Active   *bool         `json:"active"`
	Revision int64         `json:"revision"`
}

func (h *Handlers) SaveShopKnowledge(c *gin.Context) {
	shopID, ok := h.authorizeKnowledge(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var in knowledgeInput
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(&in); err != nil {
		knowledgeError(c, http.StatusBadRequest, "ข้อมูลความรู้ไม่ถูกต้องหรือมีขนาดเกินที่กำหนด")
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(in.Note) > 1000 {
		knowledgeError(c, http.StatusBadRequest, "หมายเหตุต้องไม่เกิน 1,000 ตัวอักษร")
		return
	}
	now := time.Now().UTC()
	row := KnowledgeItem{ID: primitive.NewObjectID().Hex(), ShopID: shopID, Active: true, Kind: in.Kind, Data: in.Data, Note: in.Note, CreatedAt: now, UpdatedAt: now, Revision: 1}
	action := "create"
	var before *KnowledgeItem
	if id := c.Param("id"); id != "" {
		var err error
		before, err = h.Knowledge.GetKnowledge(ctx, shopID, id)
		if err != nil {
			knowledgeStoreError(c, err)
			return
		}
		if before.Revision != in.Revision {
			knowledgeStoreError(c, errKnowledgeConflict)
			return
		}
		if before.Kind != in.Kind {
			knowledgeError(c, http.StatusBadRequest, "ไม่สามารถเปลี่ยนประเภทของความรู้เดิม")
			return
		}
		row.ID, row.CreatedAt, row.CreatedBy, row.Revision = before.ID, before.CreatedAt, before.CreatedBy, before.Revision+1
		row.Active = before.Active
		action = "update"
	}
	if in.Active != nil {
		row.Active = *in.Active
	}
	if before != nil && row.Active != before.Active {
		if row.Active {
			action = "enable"
		} else {
			action = "disable"
		}
	}
	if action == "disable" {
		row.Data, row.Note = before.Data, before.Note
	} else {
		var chart map[string]Account
		if in.Kind == "rule" || in.Kind == "remap" {
			b, ok := h.brain(c, shopID)
			if !ok {
				return
			}
			chart = b.chart
		}
		if err := validateKnowledge(in.Kind, &row.Data, chart); err != nil {
			knowledgeError(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	row.UpdatedBy = h.auditUsername(ctx, bearer(c))
	if before == nil {
		row.CreatedBy = row.UpdatedBy
	}
	if err := h.Knowledge.SaveKnowledge(ctx, before, row, action); err != nil {
		knowledgeStoreError(c, err)
		return
	}
	status := http.StatusOK
	if before == nil {
		status = http.StatusCreated
	}
	c.JSON(status, gin.H{"success": true, "data": row})
}

func (h *Handlers) DeleteShopKnowledge(c *gin.Context) {
	shopID, ok := h.authorizeKnowledge(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	before, err := h.Knowledge.GetKnowledge(ctx, shopID, c.Param("id"))
	if err != nil {
		knowledgeStoreError(c, err)
		return
	}
	if before.Active {
		row := *before
		row.Active, row.UpdatedAt, row.UpdatedBy = false, time.Now().UTC(), h.auditUsername(ctx, bearer(c))
		row.Revision++
		if err := h.Knowledge.SaveKnowledge(ctx, before, row, "delete"); err != nil {
			knowledgeStoreError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handlers) ShopKnowledgeHistory(c *gin.Context) {
	shopID, ok := h.authorizeKnowledge(c)
	if !ok {
		return
	}
	id := c.Query("id")
	if id == "" {
		knowledgeError(c, http.StatusBadRequest, "กรุณาระบุรายการความรู้")
		return
	}
	data, err := h.Knowledge.KnowledgeHistory(c.Request.Context(), shopID, id)
	if err != nil {
		knowledgeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func (h *Handlers) ShopBrainPrompt(c *gin.Context) {
	shopID, ok := h.authorizeKnowledge(c)
	if !ok {
		return
	}
	b, ok := h.brain(c, shopID)
	if !ok {
		return
	}
	items, err := h.currentKnowledge(c.Request.Context(), shopID)
	if err != nil {
		knowledgeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"instructions": buildInstructions(b, items)}})
}
