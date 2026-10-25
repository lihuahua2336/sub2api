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

// Docs returns the public integration guide for the Logto ecosystem API.
// It intentionally does not require a bearer token so a newly integrating
// application can discover the resource identifier, scopes, and call format.
func (h *EcosystemHandler) Docs(c *gin.Context) {
	cfg := config.EcosystemConfig{}
	if h != nil && h.cfg != nil {
		cfg = h.cfg.EcosystemSettings()
	}

	baseURL := strings.TrimRight(cfg.PublicGatewayURL, "/")
	if baseURL == "" {
		baseURL = "https://your-sub2api.example.com"
	}
	audience := strings.TrimSpace(cfg.Audience)
	if audience == "" {
		audience = baseURL + "/api"
	}
	issuer := strings.TrimSpace(cfg.IssuerURL)
	if issuer == "" {
		issuer = "https://your-logto.example.com/oidc"
	}

	response.Success(c, gin.H{
		"name":        "Sub2API Logto Ecosystem API",
		"version":     "v1",
		"description": "面向 Logto 用户的只读生态接口。第三方项目使用自己的 Traditional Web Application 获取用户 Access Token 后调用。",
		"authentication": gin.H{
			"type":              "Logto user access token",
			"header":            "Authorization: Bearer <access_token>",
			"resource":          audience,
			"issuer":            issuer,
			"token_type":        "at+jwt",
			"machine_token":     false,
			"required_identity": "用户必须已经使用相同 Logto issuer 绑定到 sub2api",
		},
		"apis": []gin.H{
			{"method": "GET", "path": "/api/v1/ecosystem/me", "scope": "ecosystem:me", "description": "获取当前已绑定的 sub2api 用户", "response_fields": []string{"id", "email", "username", "logto_subject"}},
			{"method": "GET", "path": "/api/v1/ecosystem/groups", "scope": "ecosystem:groups:read", "description": "获取用户可用的分组", "response_fields": []string{"id", "name", "platform", "subscription_type", "rate_multiplier", "peak_rate_enabled", "peak_multiplier"}},
			{"method": "GET", "path": "/api/v1/ecosystem/models", "scope": "ecosystem:models:read", "description": "获取用户可用模型；可选查询参数 group_id", "response_fields": []string{"group_id", "platform", "model", "protocol", "endpoint"}},
			{"method": "GET", "path": "/api/v1/ecosystem/keys", "scope": "ecosystem:tokens:read", "description": "获取用户自己的有效 API Key；支持 page 和 page_size，响应禁止缓存", "response_fields": []string{"id", "name", "key", "group_id", "expires_at", "is_expired", "quota", "quota_used", "is_quota_exhausted"}},
		},
		"logto_setup": gin.H{
			"application": "创建 Traditional Web Application，必须使用你的应用，不要创建或复用第三方应用",
			"api_resource": gin.H{
				"identifier": audience,
				"scopes": []string{"ecosystem:me", "ecosystem:groups:read", "ecosystem:models:read", "ecosystem:tokens:read"},
			},
			"role":       "创建 eggai 角色，将上述 API Resource scopes 添加到角色，并默认分配给 Logto 自建应用的用户",
			"client_id":  "将应用 Client ID 加入 sub2api 的 ecosystem.allowed_client_ids",
			"steps": []string{
				"在 Logto 创建 Traditional Web Application，并配置回调地址",
				"创建 API Resource，Identifier 必须与 sub2api audience 完全一致",
				"为 API Resource 创建四个 ecosystem scopes",
				"创建 eggai 角色并加入四个 scopes，再将角色分配给用户",
				"将应用 Client ID 加入 sub2api Allowed Client IDs",
				"第三方服务端使用 resource 获取 Access Token，再调用生态接口",
			},
		},
		"sub2api_config": gin.H{
			"ecosystem.enabled":                            cfg.Enabled,
			"ecosystem.issuer_url":                         issuer,
			"ecosystem.audience":                           audience,
			"ecosystem.jwks_url":                           cfg.JWKSURL,
			"ecosystem.public_gateway_url":                 baseURL,
			"ecosystem.allowed_signing_algs":               cfg.AllowedSigningAlgs,
			"ecosystem.clock_skew_seconds":                 cfg.ClockSkewSeconds,
			"ecosystem.jwks_request_timeout_seconds":       cfg.JWKSRequestTimeoutSeconds,
			"ecosystem.jwks_max_response_bytes":            cfg.JWKSMaxResponseBytes,
			"ecosystem.jwks_cache_ttl_seconds":             cfg.JWKSCacheTTLSeconds,
			"ecosystem.jwks_refresh_min_interval_seconds":  cfg.JWKSRefreshMinIntervalSeconds,
			"ecosystem.rate_limit_per_minute": cfg.RateLimitPerMinute,
		},
		"third_party_env": gin.H{
			"LOGTO_ISSUER":             issuer,
			"LOGTO_INTERNAL_ISSUER":    "",
			"LOGTO_CLIENT_ID":          "<YOUR_LOGTO_CLIENT_ID>",
			"LOGTO_CLIENT_SECRET":      "<YOUR_LOGTO_CLIENT_SECRET>",
			"LOGTO_SCOPE":              "openid profile email",
			"EGGAI_BASE_URL":           baseURL,
			"EGGAI_PUBLIC_URL":          baseURL,
			"EGGAI_API_DISPLAY_NAME":   "EggAi",
			"EGGAI_API_LOGTO_AUDIENCE": audience,
			"EGGAI_API_LOGTO_SCOPE":    "ecosystem:me ecosystem:models:read ecosystem:tokens:read ecosystem:groups:read",
		},
		"usage": gin.H{
			"steps": []string{
				"在 Logto 登录并申请 resource 为 api_resource.identifier 的 Access Token",
				"将 Access Token 放在 Authorization Bearer 请求头中",
				"第三方项目服务端调用 /api/v1/ecosystem/*，不要把 Token 或 API Key 暴露给浏览器",
			},
			"example": "curl -H 'Authorization: Bearer <access_token>' " + baseURL + "/api/v1/ecosystem/me",
		},
	})
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
	if h.cfg != nil {
		if configured := h.cfg.EcosystemSettings().RateLimitPerMinute; configured > 0 {
			limit = configured
		}
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
