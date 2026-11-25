package shopbrain

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handlers exposes the brain to bcaccount. Every route checks, through the
// main API and the caller's own token, that the caller owns the shop.
type Handlers struct {
	Svc    *Service
	Store  FeedbackStore // nil → feedback routes answer 503
	Tokens MCPTokenStore // nil → MCP and its token routes answer 503
}

func bearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if t, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

// authorize writes the error response itself and returns the shopid on success.
func (h *Handlers) authorize(c *gin.Context) (string, bool) {
	shopID := strings.TrimSpace(c.Query("shopid"))
	if shopID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "shopid is required"})
		return "", false
	}
	err := h.Svc.Verifier.Verify(c.Request.Context(), bearer(c), shopID)
	switch {
	case err == nil:
		return shopID, true
	case errors.Is(err, ErrUnauthorized):
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized"})
	case errors.Is(err, ErrNotOwner):
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "owner_only", "message": "เฉพาะเจ้าของร้านเท่านั้น"})
	case errors.Is(err, ErrNotConfigured):
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "not_configured", "message": "ยังไม่ได้ตั้งค่าการตรวจสิทธิ์ (MAIN_API_URL)"})
	default:
		log.Printf("[shop-brain] verify owner failed for shop %s: %v", shopID, err)
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": "verify_failed", "message": "ตรวจสิทธิ์กับระบบหลักไม่สำเร็จ ลองใหม่อีกครั้ง"})
	}
	return "", false
}

func (h *Handlers) brain(c *gin.Context, shopID string) (*Brain, bool) {
	b, err := h.Svc.Brain(c.Request.Context(), shopID, c.Query("refresh") == "1")
	if err != nil {
		log.Printf("[shop-brain] build failed for shop %s: %v", shopID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "build_failed", "message": "สร้างสมองร้านไม่สำเร็จ"})
		return nil, false
	}
	return b, true
}

// GetShopBrain handles GET /shop-brain?shopid=&refresh=1 — the page view.
// The per-journal hash index (large, only needed for diffs) is left out.
func (h *Handlers) GetShopBrain(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok {
		return
	}
	b, ok := h.brain(c, shopID)
	if !ok {
		return
	}
	view := *b
	view.Index = nil
	view.Diff = nil
	c.JSON(http.StatusOK, gin.H{"success": true, "data": &view})
}

// ExportShopBrain handles GET /shop-brain/export?shopid=&format=json|vault.
func (h *Handlers) ExportShopBrain(c *gin.Context) {
	shopID, ok := h.authorize(c)
	if !ok {
		return
	}
	b, ok := h.brain(c, shopID)
	if !ok {
		return
	}
	stamp := b.GeneratedAt.Format("20060102-1504")
	switch c.DefaultQuery("format", "json") {
	case "json":
		data, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		c.Header("Content-Disposition", `attachment; filename="shop-brain-`+shopID+"-"+stamp+`.json"`)
		c.Data(http.StatusOK, "application/json; charset=utf-8", data)
	case "vault":
		data, err := vaultZip(b)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		c.Header("Content-Disposition", `attachment; filename="shop-brain-`+shopID+"-"+stamp+`.zip"`)
		c.Data(http.StatusOK, "application/zip", data)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "format must be json or vault"})
	}
}

func vaultZip(b *Brain) ([]byte, error) {
	files := renderVault(b)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create("shop-brain/" + strings.ReplaceAll(n, "\\", "/"))
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(files[n])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
