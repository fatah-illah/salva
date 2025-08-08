package handler

import (
	"net/http"
	"strconv"

	"multi-tenant-messaging/internal/domain"
	"multi-tenant-messaging/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MessageHandler handles message related requests
type MessageHandler struct {
	db *repository.Database
}

// NewMessageHandler creates a new MessageHandler
func NewMessageHandler(db *repository.Database) *MessageHandler {
	return &MessageHandler{db: db}
}

// ListMessages godoc
// @Summary List messages with cursor pagination
// @Description Get a list of messages with cursor-based pagination
// @Tags messages
// @Accept  json
// @Produce  json
// @Param cursor query string false "Cursor for pagination"
// @Param limit query int false "Limit of messages per page (default 10)"
// @Success 200 {object} object{data=[]domain.Message,next_cursor=string}
// @Failure 400 {object} object "Invalid cursor or limit"
// @Failure 500 {object} object "Internal server error"
// @Router /messages [get]
func (h *MessageHandler) ListMessages(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit parameter"})
		return
	}

	cursor := c.Query("cursor")

	var query string
	var args []interface{}
	var orderClause = "ORDER BY created_at DESC, id DESC"

	if cursor == "" {
		query = `
			SELECT id, tenant_id, payload, created_at 
			FROM messages 
			` + orderClause + `
			LIMIT $1
		`
		args = []interface{}{limit}
	} else {
		// Validasi cursor sebagai UUID
		if _, err := uuid.Parse(cursor); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor format"})
			return
		}

		query = `
			SELECT id, tenant_id, payload, created_at 
			FROM messages 
			WHERE (created_at, id) < (
				SELECT created_at, id FROM messages WHERE id = $1
			)
			` + orderClause + `
			LIMIT $2
		`
		args = []interface{}{cursor, limit}
	}

	rows, err := h.db.DB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	messages := make([]domain.Message, 0)
	var lastID string

	for rows.Next() {
		var msg domain.Message
		if err := rows.Scan(&msg.ID, &msg.TenantID, &msg.Payload, &msg.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		messages = append(messages, msg)
		lastID = msg.ID
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	nextCursor := ""
	if len(messages) > 0 && len(messages) == limit {
		nextCursor = lastID
	}

	c.JSON(http.StatusOK, gin.H{
		"data":        messages,
		"next_cursor": nextCursor,
	})
}
