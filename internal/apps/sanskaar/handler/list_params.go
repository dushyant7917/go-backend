package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// parsePageParams reads page/page_size query params, defaulting to page 1, page_size 10.
// Final clamping (e.g. max page_size) happens in the service layer via pagination.NormalizeParams.
func parsePageParams(c *gin.Context) (page, pageSize int) {
	page, pageSize = 1, 10
	if pageStr := c.Query("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 {
			pageSize = ps
		}
	}
	return page, pageSize
}

// parseEligibilityParams reads the optional user_created_at (RFC3339), first_day_unlock_count,
// and unlock_batch_size query params shared by every Sanskaar list endpoint.
// unlock_batch_size is required whenever user_created_at is provided: it's how many rows
// become eligible on each day after the user joined. first_day_unlock_count is how many rows
// are eligible on the day the user joined itself; it's optional and defaults to
// unlock_batch_size (preserving the pre-existing behavior) when omitted.
// On validation failure it writes the 400 response itself and returns ok=false; callers should
// return immediately in that case.
func parseEligibilityParams(c *gin.Context) (userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, ok bool) {
	userCreatedAtStr := c.Query("user_created_at")
	if userCreatedAtStr == "" {
		return nil, 0, 0, true
	}

	parsed, err := time.Parse(time.RFC3339, userCreatedAtStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_created_at, expected RFC3339 timestamp"})
		return nil, 0, 0, false
	}

	unlockBatchSizeStr := c.Query("unlock_batch_size")
	if unlockBatchSizeStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unlock_batch_size is required when user_created_at is provided"})
		return nil, 0, 0, false
	}
	parsedBatchSize, err := strconv.Atoi(unlockBatchSizeStr)
	if err != nil || parsedBatchSize < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unlock_batch_size must be a positive integer"})
		return nil, 0, 0, false
	}

	parsedFirstDayUnlockCount := parsedBatchSize
	if firstDayUnlockCountStr := c.Query("first_day_unlock_count"); firstDayUnlockCountStr != "" {
		parsedFirstDayUnlockCount, err = strconv.Atoi(firstDayUnlockCountStr)
		if err != nil || parsedFirstDayUnlockCount < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "first_day_unlock_count must be a positive integer"})
			return nil, 0, 0, false
		}
	}

	return &parsed, parsedFirstDayUnlockCount, parsedBatchSize, true
}
