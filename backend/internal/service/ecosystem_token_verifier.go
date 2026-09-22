package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
)

var (
	ErrInvalidLogtoToken = infraerrors.Unauthorized(
		"invalid_logto_token", "invalid Logto access token",
	)
	ErrInsufficientEcosystemScope = infraerrors.Forbidden(
		"insufficient_scope", "the Logto access token does not grant the required scope",
	)
	ErrEcosystemClientNotAllowed = infraerrors.Forbidden(
		"ecosystem_client_not_allowed", "the Logto client is not allowed",
	)
	ErrEcosystemUnavailable = infraerrors.ServiceUnavailable(
		"ecosystem_unavailable", "the ecosystem adapter is temporarily unavailable",
	)
)

type EcosystemTokenIdentity struct {
	Issuer   string
	Subject  string
	ClientID string
	Scopes   map[string]struct{}
}

type ecosystemJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type ecosystemJWKS struct {
	Keys []ecosystemJWK `json:"keys"`
}

type EcosystemTokenVerifier struct {
	configSource func() config.EcosystemConfig
	client       *http.Client

	mu                 sync.RWMutex
	configFingerprint  [sha256.Size]byte
	keys               map[string]any
	expiresAt          time.Time
	lastRefresh        time.Time
	lastUnknownRefresh time.Time
	refresh            singleflight.Group
}

func NewEcosystemTokenVerifier(cfg config.EcosystemConfig, client *http.Client) *EcosystemTokenVerifier {
	if client == nil {
		client = http.DefaultClient
	}
	snapshot := cfg
	return &EcosystemTokenVerifier{
		configSource: func() config.EcosystemConfig { return snapshot },
		client:       client,
		keys:         make(map[string]any),
	}
}

func ProvideEcosystemTokenVerifier(cfg *config.Config) *EcosystemTokenVerifier {
	verifier := NewEcosystemTokenVerifier(cfg.EcosystemSettings(), http.DefaultClient)
	verifier.configSource = cfg.EcosystemSettings
	return verifier
}

func (v *EcosystemTokenVerifier) Verify(ctx context.Context, rawToken, requiredScope string) (*EcosystemTokenIdentity, error) {
	if v == nil || v.configSource == nil || strings.TrimSpace(rawToken) == "" {
		return nil, ErrInvalidLogtoToken
	}
	cfg := v.configSource()
	if !cfg.Enabled {
		return nil, ErrInvalidLogtoToken
	}
	fingerprint := ecosystemConfigFingerprint(cfg)
	v.ensureConfigFingerprint(fingerprint)
	parser := jwt.NewParser(
		jwt.WithValidMethods(cfg.AllowedSigningAlgs),
		jwt.WithIssuer(cfg.IssuerURL),
		jwt.WithAudience(cfg.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(time.Duration(cfg.ClockSkewSeconds)*time.Second),
	)
	claims := jwt.MapClaims{}
	_, err := parser.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) {
		if token.Header["typ"] != "at+jwt" {
			return nil, errors.New("token type is not an access token")
		}
		kid, ok := token.Header["kid"].(string)
		if !ok || strings.TrimSpace(kid) == "" {
			return nil, errors.New("token kid is missing")
		}
		return v.key(ctx, kid, cfg, fingerprint)
	})
	if err != nil {
		if errors.Is(err, errEcosystemJWKSUnavailable) {
			return nil, ErrEcosystemUnavailable
		}
		return nil, ErrInvalidLogtoToken
	}

	subject, err := claims.GetSubject()
	if err != nil || strings.TrimSpace(subject) == "" {
		return nil, ErrInvalidLogtoToken
	}
	if grantType, exists := claims["gty"]; exists {
		value, ok := grantType.(string)
		if !ok || strings.EqualFold(strings.TrimSpace(value), "client_credentials") {
			return nil, ErrInvalidLogtoToken
		}
	}
	clientID, ok := claims["client_id"].(string)
	clientID = strings.TrimSpace(clientID)
	if !ok || clientID == "" {
		return nil, ErrEcosystemClientNotAllowed
	}
	if !ecosystemClientAllowed(cfg.AllowedClientIDs, clientID) {
		return nil, ErrEcosystemClientNotAllowed
	}
	// Logto client-credentials tokens use the application as subject. Keep this
	// defense even when a customized token omits gty.
	if subject == clientID {
		return nil, ErrInvalidLogtoToken
	}
	scopeClaim, ok := claims["scope"].(string)
	if !ok {
		return nil, ErrInsufficientEcosystemScope
	}
	scopes := make(map[string]struct{})
	for _, scope := range strings.Fields(scopeClaim) {
		scopes[scope] = struct{}{}
	}
	if _, ok := scopes[requiredScope]; !ok {
		return nil, ErrInsufficientEcosystemScope
	}
	return &EcosystemTokenIdentity{
		Issuer: cfg.IssuerURL, Subject: subject, ClientID: clientID, Scopes: scopes,
	}, nil
}

func ecosystemClientAllowed(allowed []string, clientID string) bool {
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == clientID {
			return true
		}
	}
	return false
}

func ecosystemConfigFingerprint(cfg config.EcosystemConfig) [sha256.Size]byte {
	payload, _ := json.Marshal(cfg)
	return sha256.Sum256(payload)
}

func (v *EcosystemTokenVerifier) ensureConfigFingerprint(fingerprint [sha256.Size]byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.configFingerprint == fingerprint {
		return
	}
	v.configFingerprint = fingerprint
	v.keys = make(map[string]any)
	v.expiresAt = time.Time{}
	v.lastRefresh = time.Time{}
	v.lastUnknownRefresh = time.Time{}
}

var errEcosystemJWKSUnavailable = errors.New("ecosystem JWKS unavailable")

func (v *EcosystemTokenVerifier) key(ctx context.Context, kid string, cfg config.EcosystemConfig, fingerprint [sha256.Size]byte) (any, error) {
	now := time.Now()
	v.mu.RLock()
	key, found := v.keys[kid]
	fresh := now.Before(v.expiresAt)
	hasKeys := len(v.keys) > 0
	v.mu.RUnlock()
	if found && fresh {
		return key, nil
	}
	minInterval := time.Duration(cfg.JWKSRefreshMinIntervalSeconds) * time.Second
	if hasKeys && fresh {
		v.mu.Lock()
		if minInterval > 0 && now.Sub(v.lastUnknownRefresh) < minInterval {
			v.mu.Unlock()
			return nil, fmt.Errorf("unknown token kid")
		}
		v.lastUnknownRefresh = now
		v.mu.Unlock()
	}
	if err := v.refreshKeys(ctx, cfg, fingerprint); err != nil {
		return nil, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.configFingerprint != fingerprint {
		return nil, errEcosystemJWKSUnavailable
	}
	key, found = v.keys[kid]
	if !found {
		return nil, fmt.Errorf("unknown token kid")
	}
	return key, nil
}

func (v *EcosystemTokenVerifier) refreshKeys(ctx context.Context, cfg config.EcosystemConfig, fingerprint [sha256.Size]byte) error {
	result := v.refresh.DoChan(fmt.Sprintf("jwks:%x", fingerprint), func() (any, error) {
		return nil, v.fetchKeys(context.Background(), cfg, fingerprint)
	})
	select {
	case <-ctx.Done():
		return errEcosystemJWKSUnavailable
	case refreshed := <-result:
		return refreshed.Err
	}
}

func (v *EcosystemTokenVerifier) fetchKeys(parent context.Context, cfg config.EcosystemConfig, fingerprint [sha256.Size]byte) error {
	timeout := time.Duration(cfg.JWKSRequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.JWKSURL, nil)
	if err != nil {
		return fmt.Errorf("%w: create request", errEcosystemJWKSUnavailable)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: fetch keys", errEcosystemJWKSUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: unexpected status", errEcosystemJWKSUnavailable)
	}
	limit := cfg.JWKSMaxResponseBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return fmt.Errorf("%w: invalid response size", errEcosystemJWKSUnavailable)
	}
	var document ecosystemJWKS
	if err := json.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("%w: invalid response", errEcosystemJWKSUnavailable)
	}
	keys := make(map[string]any, len(document.Keys))
	for _, jwk := range document.Keys {
		if jwk.Kid == "" || (jwk.Use != "" && jwk.Use != "sig") {
			continue
		}
		key, err := parseEcosystemJWK(jwk)
		if err == nil {
			keys[jwk.Kid] = key
		}
	}
	if len(keys) == 0 {
		return fmt.Errorf("%w: no usable signing keys", errEcosystemJWKSUnavailable)
	}
	now := time.Now()
	ttl := time.Duration(cfg.JWKSCacheTTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	v.mu.Lock()
	if v.configFingerprint != fingerprint {
		v.mu.Unlock()
		return errEcosystemJWKSUnavailable
	}
	v.keys = keys
	v.lastRefresh = now
	v.expiresAt = now.Add(ttl)
	v.mu.Unlock()
	return nil
}

func parseEcosystemJWK(jwk ecosystemJWK) (any, error) {
	decode := func(value string) ([]byte, error) {
		return base64.RawURLEncoding.DecodeString(value)
	}
	switch jwk.Kty {
	case "RSA":
		nBytes, err := decode(jwk.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := decode(jwk.E)
		if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
			return nil, errors.New("invalid RSA exponent")
		}
		exponent := 0
		for _, b := range eBytes {
			exponent = exponent<<8 | int(b)
		}
		if exponent < 3 {
			return nil, errors.New("invalid RSA exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}, nil
	case "EC":
		var curve elliptic.Curve
		switch jwk.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, errors.New("unsupported EC curve")
		}
		xBytes, err := decode(jwk.X)
		if err != nil {
			return nil, err
		}
		yBytes, err := decode(jwk.Y)
		if err != nil {
			return nil, err
		}
		x, y := new(big.Int).SetBytes(xBytes), new(big.Int).SetBytes(yBytes)
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("invalid EC point")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	default:
		return nil, errors.New("unsupported key type")
	}
}
