package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	hc "go-backend/internal/apps/sanskaar/hinducalendar"
	"go-backend/internal/apps/sanskaar/service"

	"github.com/gin-gonic/gin"
)

// HinduEventHandler serves the read-only Hindu calendar endpoints.
type HinduEventHandler struct {
	service service.HinduEventService
}

// NewHinduEventHandler creates a new instance of HinduEventHandler
func NewHinduEventHandler(service service.HinduEventService) *HinduEventHandler {
	return &HinduEventHandler{service: service}
}

// parseHinduLanguage reads the required language_code query param ("hi" or "en"). On failure it
// writes the 400 response itself and returns ok=false.
func parseHinduLanguage(c *gin.Context) (hc.Lang, bool) {
	raw := c.Query("language_code")
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "language_code is required"})
		return "", false
	}
	lang, ok := hc.ParseLang(raw)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported language_code, use hi or en"})
		return "", false
	}
	return lang, true
}

// parseHinduDate reads an optional YYYY-MM-DD query param. It writes the 400 itself on failure.
func parseHinduDate(c *gin.Context, name string) (value string, ok bool) {
	raw := c.Query(name)
	if raw == "" {
		return "", true
	}
	if _, err := time.Parse(hc.DateLayout, raw); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid %s, expected YYYY-MM-DD", name)})
		return "", false
	}
	return raw, true
}

// GetTodayOrUpcoming handles GET /api/v1/sanskaar/hindu-events/today-or-upcoming
func (h *HinduEventHandler) GetTodayOrUpcoming(c *gin.Context) {
	lang, ok := parseHinduLanguage(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, h.service.TodayOrUpcoming(lang))
}

// GetList handles GET /api/v1/sanskaar/hindu-events
func (h *HinduEventHandler) GetList(c *gin.Context) {
	lang, ok := parseHinduLanguage(c)
	if !ok {
		return
	}
	startDate, ok := parseHinduDate(c, "start_date")
	if !ok {
		return
	}
	endDate, ok := parseHinduDate(c, "end_date")
	if !ok {
		return
	}
	if startDate != "" && endDate != "" && endDate < startDate {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end_date must not be before start_date"})
		return
	}

	// category may be a comma list (?category=ekadashi,vrat), repeated (?category=ekadashi&category=vrat) or both.
	var categories []string
	for _, raw := range c.QueryArray("category") {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if !hc.IsCategory(part) {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown category %q", part)})
				return
			}
			categories = append(categories, part)
		}
	}

	page, pageSize := parsePageParams(c)
	c.JSON(http.StatusOK, h.service.List(service.HinduEventListParams{
		Lang:       lang,
		StartDate:  startDate,
		EndDate:    endDate,
		Categories: categories,
		Page:       page,
		PageSize:   pageSize,
	}))
}

// GetByID handles GET /api/v1/sanskaar/hindu-events/:id
func (h *HinduEventHandler) GetByID(c *gin.Context) {
	lang, ok := parseHinduLanguage(c)
	if !ok {
		return
	}
	resp, found := h.service.GetByID(c.Param("id"), lang)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "hindu event not found"})
		return
	}
	c.JSON(http.StatusOK, resp)
}
