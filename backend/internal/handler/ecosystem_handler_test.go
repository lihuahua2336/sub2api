package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type ecosystemHandlerRepo struct{ user *service.User }

func (r ecosystemHandlerRepo) FindUserByLogtoIdentity(context.Context, string, string) (*service.User, error) {
	return r.user, nil
}
func (r ecosystemHandlerRepo) ListActiveKeys(context.Context, int64, pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	return []service.APIKey{{ID: 1, UserID: 9, Key: "raw-key", Status: service.StatusActive}}, &pagination.PaginationResult{Total: 1, Page: 1, PageSize: 20, Pages: 1}, nil
}

type ecosystemHandlerGroups struct{}

func (ecosystemHandlerGroups) GetAvailableGroups(context.Context, int64) ([]service.Group, error) {
	return []service.Group{}, nil
}
func (ecosystemHandlerGroups) GetUserGroupRates(context.Context, int64) (map[int64]float64, error) {
	return map[int64]float64{}, nil
}

type ecosystemHandlerModels struct{}

func (ecosystemHandlerModels) EffectiveModelIDs(context.Context, *service.Group) []string { return nil }

func TestEcosystemHandlerAuthenticatesRoutesAndProtectsKeyResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.E)).Bytes()),
		}}})
	}))
	t.Cleanup(jwks.Close)
	cfg := &config.Config{Ecosystem: config.EcosystemConfig{
		Enabled: true, IssuerURL: "https://issuer.example", Audience: "sub2api-resource", JWKSURL: jwks.URL,
		AllowedClientIDs: []string{"trusted-bff"}, PublicGatewayURL: "https://gateway.example",
		AllowedSigningAlgs: []string{"RS256"}, JWKSRequestTimeoutSeconds: 1, JWKSMaxResponseBytes: 1 << 20,
		JWKSCacheTTLSeconds: 300, JWKSRefreshMinIntervalSeconds: 30, RateLimitPerMinute: 60,
	}}
	verifier := service.NewEcosystemTokenVerifier(cfg.Ecosystem, jwks.Client())
	resources := service.NewEcosystemService(
		ecosystemHandlerRepo{user: &service.User{ID: 9, Email: "user@example.com", Username: "reader", Status: service.StatusActive}},
		ecosystemHandlerGroups{}, ecosystemHandlerModels{}, nil, cfg,
	)
	h := NewEcosystemHandler(verifier, resources, cfg)
	router := gin.New()
	router.GET("/api/v1/ecosystem/me", h.Me)
	router.GET("/api/v1/ecosystem/groups", h.Groups)
	router.GET("/api/v1/ecosystem/models", h.Models)
	router.GET("/api/v1/ecosystem/keys", h.Keys)

	sign := func(scope string) string {
		claims := jwt.MapClaims{
			"iss": "https://issuer.example", "aud": "sub2api-resource", "sub": "logto-user",
			"client_id": "trusted-bff", "scope": scope, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["typ"] = "at+jwt"
		token.Header["kid"] = "test"
		raw, signErr := token.SignedString(privateKey)
		require.NoError(t, signErr)
		return raw
	}

	me := httptest.NewRecorder()
	meRequest := httptest.NewRequest(http.MethodGet, "/api/v1/ecosystem/me", nil)
	meRequest.Header.Set("Authorization", "Bearer "+sign("ecosystem:me"))
	router.ServeHTTP(me, meRequest)
	require.Equal(t, http.StatusOK, me.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"id":9,"email":"user@example.com","username":"reader","logto_subject":"logto-user"}}`, me.Body.String())

	groups := httptest.NewRecorder()
	groupsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/ecosystem/groups", nil)
	groupsRequest.Header.Set("Authorization", "Bearer "+sign("ecosystem:groups:read"))
	router.ServeHTTP(groups, groupsRequest)
	require.Equal(t, http.StatusOK, groups.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":[]}`, groups.Body.String())

	models := httptest.NewRecorder()
	modelsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/ecosystem/models", nil)
	modelsRequest.Header.Set("Authorization", "Bearer "+sign("ecosystem:models:read"))
	router.ServeHTTP(models, modelsRequest)
	require.Equal(t, http.StatusOK, models.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":[]}`, models.Body.String())

	keys := httptest.NewRecorder()
	keysRequest := httptest.NewRequest(http.MethodGet, "/api/v1/ecosystem/keys", nil)
	keysRequest.Header.Set("Authorization", "Bearer "+sign("ecosystem:tokens:read"))
	router.ServeHTTP(keys, keysRequest)
	require.Equal(t, http.StatusOK, keys.Code)
	require.Equal(t, "no-store", keys.Header().Get("Cache-Control"))
	require.NotContains(t, keys.Body.String(), "sk-")
	require.Contains(t, keys.Body.String(), "raw-key")

	denied := httptest.NewRecorder()
	deniedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/ecosystem/me", nil)
	deniedRequest.Header.Set("Authorization", "Bearer "+sign("ecosystem:me-extra"))
	router.ServeHTTP(denied, deniedRequest)
	require.Equal(t, http.StatusForbidden, denied.Code)
	require.Contains(t, denied.Body.String(), `"reason":"insufficient_scope"`)
}
