package service

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

var (
	ErrEcosystemIdentityNotLinked = infraerrors.Forbidden(
		"ecosystem_identity_not_linked", "the Logto identity is not linked to a local user",
	)
	ErrEcosystemUserUnavailable = infraerrors.Forbidden(
		"ecosystem_user_unavailable", "the linked local user is unavailable",
	)
	ErrEcosystemDisabled = infraerrors.Forbidden(
		"ecosystem_disabled", "the ecosystem adapter is disabled",
	)
	ErrEcosystemGroupNotFound = infraerrors.NotFound(
		"ecosystem_group_not_found", "the ecosystem group does not exist or is not available",
	)
)

type EcosystemRepository interface {
	FindUserByLogtoIdentity(ctx context.Context, issuer, subject string) (*User, error)
	ListActiveKeys(ctx context.Context, userID int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error)
}

type EcosystemGroupProvider interface {
	GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error)
	GetUserGroupRates(ctx context.Context, userID int64) (map[int64]float64, error)
}

type EcosystemModelProvider interface {
	EffectiveModelIDs(ctx context.Context, group *Group) []string
}

type EcosystemService struct {
	repo              EcosystemRepository
	groups            EcosystemGroupProvider
	models            EcosystemModelProvider
	compositeResolver *CompositeRouteResolver
	cfg               *config.Config
}

func NewEcosystemService(
	repo EcosystemRepository,
	groups EcosystemGroupProvider,
	models EcosystemModelProvider,
	compositeResolver *CompositeRouteResolver,
	cfg *config.Config,
) *EcosystemService {
	return &EcosystemService{repo: repo, groups: groups, models: models, compositeResolver: compositeResolver, cfg: cfg}
}

func ProvideEcosystemGroupProvider(apiKeys *APIKeyService) EcosystemGroupProvider {
	return apiKeys
}

func ProvideEcosystemModelProvider(gateway *GatewayService) EcosystemModelProvider {
	return gateway
}

type EcosystemUser struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Username     string `json:"username"`
	LogtoSubject string `json:"logto_subject"`
}

type EcosystemGroup struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Platform         string  `json:"platform"`
	SubscriptionType string  `json:"subscription_type"`
	RateMultiplier   float64 `json:"rate_multiplier"`
	PeakRateEnabled  bool    `json:"peak_rate_enabled"`
	PeakMultiplier   float64 `json:"peak_multiplier"`
}

type EcosystemModel struct {
	GroupID  int64  `json:"group_id"`
	Platform string `json:"platform"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
	Endpoint string `json:"endpoint"`
}

type EcosystemAPIKey struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Key              string     `json:"key"`
	GroupID          *int64     `json:"group_id"`
	ExpiresAt        *time.Time `json:"expires_at"`
	IsExpired        bool       `json:"is_expired"`
	Quota            float64    `json:"quota"`
	QuotaUsed        float64    `json:"quota_used"`
	IsQuotaExhausted bool       `json:"is_quota_exhausted"`
}

func (s *EcosystemService) ResolveUser(ctx context.Context, identity EcosystemTokenIdentity) (*EcosystemUser, error) {
	if s == nil || s.repo == nil {
		return nil, ErrEcosystemUnavailable
	}
	user, err := s.repo.FindUserByLogtoIdentity(ctx, identity.Issuer, identity.Subject)
	if err != nil {
		if infraerrors.Code(err) == 404 {
			return nil, ErrEcosystemIdentityNotLinked
		}
		return nil, ErrEcosystemUnavailable
	}
	if user == nil {
		return nil, ErrEcosystemIdentityNotLinked
	}
	if user.DeletedAt != nil || !user.IsActive() {
		return nil, ErrEcosystemUserUnavailable
	}
	return &EcosystemUser{ID: user.ID, Email: user.Email, Username: user.Username, LogtoSubject: identity.Subject}, nil
}

func (s *EcosystemService) Groups(ctx context.Context, userID int64) ([]EcosystemGroup, error) {
	groups, rates, err := s.availableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	result := make([]EcosystemGroup, 0, len(groups))
	for i := range groups {
		rate := groups[i].RateMultiplier
		if override, ok := rates[groups[i].ID]; ok {
			rate = override
		}
		result = append(result, EcosystemGroup{
			ID: groups[i].ID, Name: groups[i].Name, Platform: groups[i].Platform,
			SubscriptionType: groups[i].SubscriptionType, RateMultiplier: rate,
			PeakRateEnabled: groups[i].PeakRateEnabled, PeakMultiplier: groups[i].PeakMultiplierAt(now),
		})
	}
	return result, nil
}

type ecosystemProtocol struct {
	name              string
	path              string
	compositeEndpoint string
}

var ecosystemGatewayProtocols = []ecosystemProtocol{
	{name: "openai_chat_completions", path: "/v1/chat/completions", compositeEndpoint: CompositeRouteEndpointChatCompletions},
	{name: "openai_responses", path: "/v1/responses", compositeEndpoint: CompositeRouteEndpointResponses},
	{name: "anthropic_messages", path: "/v1/messages", compositeEndpoint: CompositeRouteEndpointMessages},
}

func ecosystemProtocolsForPlatform(platform string) []ecosystemProtocol {
	switch platform {
	case PlatformAnthropic:
		return []ecosystemProtocol{ecosystemGatewayProtocols[2]}
	case PlatformGemini:
		return nil
	case PlatformAntigravity:
		return []ecosystemProtocol{ecosystemGatewayProtocols[2]}
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo:
		return []ecosystemProtocol{ecosystemGatewayProtocols[0], ecosystemGatewayProtocols[1]}
	default:
		return nil
	}
}

func (s *EcosystemService) Models(ctx context.Context, userID int64, groupID *int64) ([]EcosystemModel, error) {
	groups, _, err := s.availableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	if groupID != nil {
		filtered := groups[:0]
		for i := range groups {
			if groups[i].ID == *groupID {
				filtered = append(filtered, groups[i])
			}
		}
		groups = filtered
		if len(groups) == 0 {
			return nil, ErrEcosystemGroupNotFound
		}
	}
	baseURL := strings.TrimRight(s.ecosystemConfig().PublicGatewayURL, "/")
	result := make([]EcosystemModel, 0)
	for i := range groups {
		group := &groups[i]
		for _, model := range s.models.EffectiveModelIDs(ctx, group) {
			protocols := ecosystemGatewayProtocols
			if group.Platform != PlatformComposite {
				protocols = ecosystemProtocolsForPlatform(group.Platform)
			}
			for _, protocol := range protocols {
				if group.Platform == PlatformComposite && !s.compositeSupports(ctx, group.ID, model, protocol.compositeEndpoint) {
					continue
				}
				result = append(result, EcosystemModel{
					GroupID: group.ID, Platform: group.Platform, Model: model,
					Protocol: protocol.name, Endpoint: baseURL + protocol.path,
				})
			}
			if group.Platform == PlatformGemini ||
				(group.Platform == PlatformComposite && s.compositeSupports(ctx, group.ID, model, CompositeRouteEndpointGemini)) {
				result = append(result, EcosystemModel{
					GroupID: group.ID, Platform: group.Platform, Model: model,
					Protocol: "gemini_native", Endpoint: baseURL + "/v1beta/models/" + url.PathEscape(model) + ":generateContent",
				})
			}
			if group.Platform == PlatformAntigravity {
				result = append(result, EcosystemModel{
					GroupID: group.ID, Platform: group.Platform, Model: model,
					Protocol: "gemini_native", Endpoint: baseURL + "/antigravity/v1beta/models/" + url.PathEscape(model) + ":generateContent",
				})
			}
		}
	}
	return result, nil
}

func (s *EcosystemService) compositeSupports(ctx context.Context, groupID int64, model, endpoint string) bool {
	if s.compositeResolver == nil {
		return false
	}
	decision, err := s.compositeResolver.Resolve(ctx, groupID, model, endpoint)
	return err == nil && decision.Matched
}

func (s *EcosystemService) Keys(ctx context.Context, userID int64, params pagination.PaginationParams) ([]EcosystemAPIKey, *pagination.PaginationResult, error) {
	params.SortBy = "id"
	params.SortOrder = pagination.SortOrderAsc
	keys, page, err := s.repo.ListActiveKeys(ctx, userID, params)
	if err != nil {
		return nil, nil, ErrEcosystemUnavailable
	}
	result := make([]EcosystemAPIKey, 0, len(keys))
	for i := range keys {
		result = append(result, EcosystemAPIKey{
			ID: keys[i].ID, Name: keys[i].Name, Key: keys[i].Key, GroupID: keys[i].GroupID,
			ExpiresAt: keys[i].ExpiresAt, IsExpired: keys[i].IsExpired(), Quota: keys[i].Quota,
			QuotaUsed: keys[i].QuotaUsed, IsQuotaExhausted: keys[i].IsQuotaExhausted(),
		})
	}
	return result, page, nil
}

func (s *EcosystemService) availableGroups(ctx context.Context, userID int64) ([]Group, map[int64]float64, error) {
	if s == nil || s.groups == nil || s.models == nil {
		return nil, nil, ErrEcosystemUnavailable
	}
	groups, err := s.groups.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, nil, ErrEcosystemUnavailable
	}
	rates, err := s.groups.GetUserGroupRates(ctx, userID)
	if err != nil {
		return nil, nil, ErrEcosystemUnavailable
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	return groups, rates, nil
}

func (s *EcosystemService) ValidateEnabled() error {
	if s == nil || !s.ecosystemConfig().Enabled {
		return ErrEcosystemDisabled
	}
	return nil
}

func (s *EcosystemService) ecosystemConfig() config.EcosystemConfig {
	if s == nil || s.cfg == nil {
		return config.EcosystemConfig{}
	}
	return s.cfg.EcosystemSettings()
}
