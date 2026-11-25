package shopbrain

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Owner-only token management for the MCP endpoint (bcaccount "เชื่อมต่อ AI").

type mcpTokenView struct {
	MCPToken
	Status string `json:"status"`
}

func (h *Handlers) tokenStoreReady(c *gin.Context) bool {
	if h.Tokens == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "mcp_unavailable", "message": "ระบบเก็บรหัสเชื่อมต่อยังไม่พร้อม"})
		return false
	}
	return true
}

// ListMCPTokens handles GET /shop-brain/mcp-tokens?shopid=
func (h *Handlers) ListMCPTokens(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok || !h.tokenStoreReady(c) {
		return
	}
	list, err := h.Tokens.ListTokens(c.Request.Context(), shopID)
	if err != nil {
		h.feedbackUnavailable(c, err)
		return
	}
	now := time.Now()
	out := make([]mcpTokenView, 0, len(list))
	for _, t := range list {
		out = append(out, mcpTokenView{MCPToken: t, Status: t.Status(now)})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out, "ttl_days": int(MCPTokenTTL.Hours() / 24)})
}

// CreateMCPToken handles POST /shop-brain/mcp-tokens?shopid= {name}. The
// plain token is in this response only.
func (h *Handlers) CreateMCPToken(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok || !h.tokenStoreReady(c) {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&in)
	plain, rec, err := NewMCPToken(shopID, in.Name, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "token_generation_failed"})
		return
	}
	if err := h.Tokens.CreateToken(c.Request.Context(), rec); err != nil {
		if errors.Is(err, ErrTokenLimit) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "error": "too_many_tokens", "message": "รหัสที่ใช้อยู่ครบ 20 รหัสแล้ว ยกเลิกรหัสเก่าก่อน"})
			return
		}
		h.feedbackUnavailable(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "token": plain, "data": mcpTokenView{MCPToken: rec, Status: "active"}})
}

// RevokeMCPToken handles POST /shop-brain/mcp-tokens/:id/revoke?shopid=
func (h *Handlers) RevokeMCPToken(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok || !h.tokenStoreReady(c) {
		return
	}
	err := h.Tokens.RevokeToken(c.Request.Context(), shopID, c.Param("id"), time.Now())
	switch {
	case errors.Is(err, ErrTokenInvalid):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "token_not_found"})
	case err != nil:
		h.feedbackUnavailable(c, err)
	default:
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}
