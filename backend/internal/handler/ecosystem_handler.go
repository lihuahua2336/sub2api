package handler

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ecosystemRateWindow struct {
	started time.Time
	count   int
}

type EcosystemHandler struct {
	verifier *service.EcosystemTokenVerifier
	service  *service.EcosystemService
	cfg      *config.Config
	rateMu   sync.Mutex
	rates    map[string]ecosystemRateWindow
}

func NewEcosystemHandler(verifier *service.EcosystemTokenVerifier, resources *service.EcosystemService, cfg *config.Config) *EcosystemHandler {
	return &EcosystemHandler{verifier: verifier, service: resources, cfg: cfg, rates: make(map[string]ecosystemRateWindow)}
}

func (h *EcosystemHandler) Me(c *gin.Context) {
	_, user, ok := h.authorize(c, "ecosystem:me", "me")
	if ok {
		response.Success(c, user)
	}
}

func (h *EcosystemHandler) Groups(c *gin.Context) {
	_, user, ok := h.authorize(c, "ecosystem:groups:read", "groups")
	if !ok {
		return
	}
	groups, err := h.service.Groups(c.Request.Context(), user.ID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, groups)
	}
}

func (h *EcosystemHandler) Models(c *gin.Context) {
	_, user, ok := h.authorize(c, "ecosystem:models:read", "models")
	if !ok {
		return
	}
	var groupID *int64
	if value := strings.TrimSpace(c.Query("group_id")); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			response.BadRequest(c, "group_id must be a positive integer")
			return
		}
		groupID = &parsed
	}
	models, err := h.service.Models(c.Request.Context(), user.ID, groupID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, models)
	}
}

func (h *EcosystemHandler) Keys(c *gin.Context) {
	_, user, ok := h.authorize(c, "ecosystem:tokens:read", "keys")
	if !ok {
		return
	}
	c.Header("Cache-Control", "no-store")
	page, pageSize := response.ParsePagination(c)
	keys, result, err := h.service.Keys(c.Request.Context(), user.ID, pagination.PaginationParams{
		Page: page, PageSize: pageSize, SortBy: "id", SortOrder: pagination.SortOrderAsc,
	})
	if response.ErrorFrom(c, err) {
		return
	}
	response.PaginatedWithResult(c, keys, &response.PaginationResult{
		Total: result.Total, Page: result.Page, PageSize: result.PageSize, Pages: result.Pages,
	})
}

func (h *EcosystemHandler) authorize(c *gin.Context, requiredScope, resource string) (*service.EcosystemTokenIdentity, *service.EcosystemUser, bool) {
	if err := h.service.ValidateEnabled(); response.ErrorFrom(c, err) {
		return nil, nil, false
	}
	raw, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		response.ErrorFrom(c, service.ErrInvalidLogtoToken)
		return nil, nil, false
	}
	identity, err := h.verifier.Verify(c.Request.Context(), raw, requiredScope)
	if response.ErrorFrom(c, err) {
		return nil, nil, false
	}
	user, err := h.service.ResolveUser(c.Request.Context(), *identity)
	if response.ErrorFrom(c, err) {
		return nil, nil, false
	}
	if !h.allow(user.ID, identity.ClientID) {
		response.ErrorWithDetails(c, http.StatusTooManyRequests, "too many requests", "RATE_LIMITED", nil)
		return nil, nil, false
	}
	servermiddleware.SetAuditActor(c, user.ID, user.Email)
	servermiddleware.SetAuditAction(c, "ecosystem."+resource+".read")
	servermiddleware.SetAuditExtra(c, map[string]any{
		"ecosystem_client_id": identity.ClientID,
		"ecosystem_resource":  resource,
	})
	return identity, user, true
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	token = strings.TrimSpace(token)
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

func (h *EcosystemHandler) allow(userID int64, clientID string) bool {
	limit := 60
	if h.cfg != nil && h.cfg.Ecosystem.RateLimitPerMinute > 0 {
		limit = h.cfg.Ecosystem.RateLimitPerMinute
	}
	now := time.Now()
	key := strconv.FormatInt(userID, 10) + ":" + clientID
	h.rateMu.Lock()
	defer h.rateMu.Unlock()
	if len(h.rates) >= 10000 {
		for existingKey, existing := range h.rates {
			if now.Sub(existing.started) >= time.Minute {
				delete(h.rates, existingKey)
			}
		}
	}
	window := h.rates[key]
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		h.rates[key] = ecosystemRateWindow{started: now, count: 1}
		return true
	}
	if window.count >= limit {
		return false
	}
	window.count++
	h.rates[key] = window
	return true
}
