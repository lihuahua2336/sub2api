package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type testJWKS struct {
	keys  atomic.Value
	calls atomic.Int64
	delay atomic.Int64
}

func newTestJWKS(t *testing.T) (*testJWKS, *httptest.Server) {
	t.Helper()
	fixture := &testJWKS{}
	fixture.keys.Store([]map[string]any{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fixture.calls.Add(1)
		if delay := fixture.delay.Load(); delay > 0 {
			time.Sleep(time.Duration(delay))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": fixture.keys.Load()})
	}))
	t.Cleanup(server.Close)
	return fixture, server
}

func (j *testJWKS) setRSA(kid string, key *rsa.PublicKey) {
	j.keys.Store([]map[string]any{{
		"kty": "RSA",
		"kid": kid,
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}})
}

func ecosystemVerifierConfig(jwksURL string) config.EcosystemConfig {
	return config.EcosystemConfig{
		Enabled:                       true,
		IssuerURL:                     "https://issuer.example/oidc",
		Audience:                      "https://sub2api.example/ecosystem",
		JWKSURL:                       jwksURL,
		AllowedClientIDs:              []string{"trusted-bff", "second-bff"},
		AllowedSigningAlgs:            []string{"RS256"},
		ClockSkewSeconds:              1,
		JWKSRequestTimeoutSeconds:     1,
		JWKSMaxResponseBytes:          1 << 20,
		JWKSCacheTTLSeconds:           300,
		JWKSRefreshMinIntervalSeconds: 1,
	}
}

func signEcosystemToken(t *testing.T, key *rsa.PrivateKey, kid string, mutate func(jwt.MapClaims)) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":       "https://issuer.example/oidc",
		"aud":       "https://sub2api.example/ecosystem",
		"sub":       "logto-user-1",
		"client_id": "trusted-bff",
		"scope":     "ecosystem:me ecosystem:groups:read ecosystem:models:read ecosystem:tokens:read",
		"iat":       now.Unix(),
		"exp":       now.Add(time.Minute).Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	token.Header["typ"] = "at+jwt"
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func TestEcosystemTokenVerifierAcceptsLogtoUserResourceToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks, server := newTestJWKS(t)
	jwks.setRSA("key-1", &privateKey.PublicKey)
	verifier := NewEcosystemTokenVerifier(ecosystemVerifierConfig(server.URL), server.Client())

	identity, err := verifier.Verify(context.Background(), signEcosystemToken(t, privateKey, "key-1", nil), "ecosystem:tokens:read")
	require.NoError(t, err)
	require.Equal(t, "logto-user-1", identity.Subject)
	require.Equal(t, "trusted-bff", identity.ClientID)
	require.Equal(t, int64(1), jwks.calls.Load())
}

func TestEcosystemTokenVerifierRejectsInvalidClaimsAndAuthorization(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks, server := newTestJWKS(t)
	jwks.setRSA("key-1", &privateKey.PublicKey)
	verifier := NewEcosystemTokenVerifier(ecosystemVerifierConfig(server.URL), server.Client())

	tests := []struct {
		name    string
		scope   string
		mutate  func(jwt.MapClaims)
		wantErr error
	}{
		{name: "issuer", mutate: func(c jwt.MapClaims) { c["iss"] = "https://evil.example" }, wantErr: ErrInvalidLogtoToken},
		{name: "audience", mutate: func(c jwt.MapClaims) { c["aud"] = "other-api" }, wantErr: ErrInvalidLogtoToken},
		{name: "missing expiration", mutate: func(c jwt.MapClaims) { delete(c, "exp") }, wantErr: ErrInvalidLogtoToken},
		{name: "future not before", mutate: func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Minute).Unix() }, wantErr: ErrInvalidLogtoToken},
		{name: "empty subject", mutate: func(c jwt.MapClaims) { c["sub"] = "" }, wantErr: ErrInvalidLogtoToken},
		{name: "scope uses exact values", scope: "ecosystem:me", mutate: func(c jwt.MapClaims) { c["scope"] = "prefix-ecosystem:me-suffix" }, wantErr: ErrInsufficientEcosystemScope},
		{name: "missing client", mutate: func(c jwt.MapClaims) { delete(c, "client_id") }, wantErr: ErrEcosystemClientNotAllowed},
		{name: "untrusted client", mutate: func(c jwt.MapClaims) { c["client_id"] = "unknown" }, wantErr: ErrEcosystemClientNotAllowed},
		{name: "azp is not an alias", mutate: func(c jwt.MapClaims) { delete(c, "client_id"); c["azp"] = "trusted-bff" }, wantErr: ErrEcosystemClientNotAllowed},
		{name: "machine token", mutate: func(c jwt.MapClaims) { c["gty"] = "client_credentials"; c["sub"] = "trusted-bff" }, wantErr: ErrInvalidLogtoToken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := tt.scope
			if scope == "" {
				scope = "ecosystem:me"
			}
			_, err := verifier.Verify(context.Background(), signEcosystemToken(t, privateKey, "key-1", tt.mutate), scope)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}

	idToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://issuer.example/oidc", "aud": "https://sub2api.example/ecosystem",
		"sub": "logto-user-1", "client_id": "trusted-bff", "scope": "ecosystem:me",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
	})
	idToken.Header["kid"] = "key-1"
	idToken.Header["typ"] = "JWT"
	idTokenRaw, err := idToken.SignedString(privateKey)
	require.NoError(t, err)
	_, err = verifier.Verify(context.Background(), idTokenRaw, "ecosystem:me")
	require.ErrorIs(t, err, ErrInvalidLogtoToken)
}

func TestEcosystemTokenVerifierRefreshesRotatedKidAndBoundsUnknownKidRefresh(t *testing.T) {
	oldKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks, server := newTestJWKS(t)
	jwks.setRSA("old", &oldKey.PublicKey)
	cfg := ecosystemVerifierConfig(server.URL)
	verifier := NewEcosystemTokenVerifier(cfg, server.Client())

	_, err = verifier.Verify(context.Background(), signEcosystemToken(t, oldKey, "old", nil), "ecosystem:me")
	require.NoError(t, err)
	jwks.setRSA("new", &newKey.PublicKey)
	_, err = verifier.Verify(context.Background(), signEcosystemToken(t, newKey, "new", nil), "ecosystem:me")
	require.NoError(t, err)
	require.Equal(t, int64(2), jwks.calls.Load())

	for range 5 {
		_, err = verifier.Verify(context.Background(), signEcosystemToken(t, newKey, "missing", nil), "ecosystem:me")
		require.ErrorIs(t, err, ErrInvalidLogtoToken)
	}
	require.LessOrEqual(t, jwks.calls.Load(), int64(3))
}

func TestEcosystemTokenVerifierReportsJWKSDependencyFailure(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks, server := newTestJWKS(t)
	jwks.setRSA("key-1", &privateKey.PublicKey)
	jwks.delay.Store(int64(100 * time.Millisecond))
	cfg := ecosystemVerifierConfig(server.URL)
	cfg.JWKSRequestTimeoutSeconds = 0
	verifier := NewEcosystemTokenVerifier(cfg, &http.Client{Timeout: 20 * time.Millisecond})

	_, err = verifier.Verify(context.Background(), signEcosystemToken(t, privateKey, "key-1", nil), "ecosystem:me")
	require.ErrorIs(t, err, ErrEcosystemUnavailable)
}

func TestEcosystemTokenVerifierUsesRuntimeConfigAndInvalidatesJWKSCache(t *testing.T) {
	oldKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks, server := newTestJWKS(t)
	jwks.setRSA("shared-kid", &oldKey.PublicKey)

	runtimeConfig := &config.Config{Ecosystem: ecosystemVerifierConfig(server.URL)}
	verifier := ProvideEcosystemTokenVerifier(runtimeConfig)
	verifier.client = server.Client()
	oldToken := signEcosystemToken(t, oldKey, "shared-kid", nil)
	_, err = verifier.Verify(context.Background(), oldToken, "ecosystem:me")
	require.NoError(t, err)
	require.Equal(t, int64(1), jwks.calls.Load())

	updated := ecosystemVerifierConfig(server.URL)
	updated.IssuerURL = "https://issuer.example/updated"
	updated.AllowedClientIDs = []string{"updated-bff"}
	runtimeConfig.SetEcosystemSettings(updated)
	jwks.setRSA("shared-kid", &newKey.PublicKey)
	newToken := signEcosystemToken(t, newKey, "shared-kid", func(claims jwt.MapClaims) {
		claims["iss"] = updated.IssuerURL
		claims["client_id"] = "updated-bff"
	})

	identity, err := verifier.Verify(context.Background(), newToken, "ecosystem:me")
	require.NoError(t, err)
	require.Equal(t, updated.IssuerURL, identity.Issuer)
	require.Equal(t, "updated-bff", identity.ClientID)
	require.Equal(t, int64(2), jwks.calls.Load())

	_, err = verifier.Verify(context.Background(), oldToken, "ecosystem:me")
	require.ErrorIs(t, err, ErrInvalidLogtoToken)
}
