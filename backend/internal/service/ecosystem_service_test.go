package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type ecosystemRepoStub struct {
	user *User
	keys []APIKey
}

func (s ecosystemRepoStub) FindUserByLogtoIdentity(context.Context, string, string) (*User, error) {
	return s.user, nil
}

func (s ecosystemRepoStub) ListActiveKeys(_ context.Context, _ int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	start := params.Offset()
	if start > len(s.keys) {
		start = len(s.keys)
	}
	end := min(start+params.Limit(), len(s.keys))
	return s.keys[start:end], &pagination.PaginationResult{
		Total: int64(len(s.keys)), Page: params.Page, PageSize: params.Limit(), Pages: (len(s.keys) + params.Limit() - 1) / params.Limit(),
	}, nil
}

type ecosystemGroupsStub struct{ groups []Group }

func (s ecosystemGroupsStub) GetAvailableGroups(context.Context, int64) ([]Group, error) {
	return s.groups, nil
}

func (s ecosystemGroupsStub) GetUserGroupRates(context.Context, int64) (map[int64]float64, error) {
	return map[int64]float64{2: 0.8}, nil
}

type ecosystemModelsStub struct{ models map[int64][]string }

func (s ecosystemModelsStub) EffectiveModelIDs(_ context.Context, group *Group) []string {
	return s.models[group.ID]
}

func TestEcosystemServiceReturnsWhitelistedResourcesWithoutMutatingKeys(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	groupID := int64(2)
	repo := ecosystemRepoStub{
		user: &User{ID: 7, Email: "user@example.com", Username: "reader", Status: StatusActive},
		keys: []APIKey{
			{ID: 10, UserID: 7, Name: "custom", Key: "raw.custom-key", GroupID: &groupID, Status: StatusActive, ExpiresAt: &past, Quota: 5, QuotaUsed: 5},
			{ID: 11, UserID: 7, Name: "ungrouped", Key: "another-raw-key", Status: StatusActive},
		},
	}
	groups := ecosystemGroupsStub{groups: []Group{
		{ID: 1, Name: "Claude", Platform: PlatformAnthropic, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 1.2},
		{ID: 2, Name: "OpenAI", Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription, RateMultiplier: 1.1},
	}}
	models := ecosystemModelsStub{models: map[int64][]string{1: {"shared-model"}, 2: {"shared-model", "gpt-5"}}}
	svc := NewEcosystemService(repo, groups, models, nil, &config.Config{Ecosystem: config.EcosystemConfig{PublicGatewayURL: "https://gateway.example.com"}})

	identity, err := svc.ResolveUser(context.Background(), EcosystemTokenIdentity{Issuer: "https://issuer", Subject: "subject-1"})
	require.NoError(t, err)
	require.Equal(t, int64(7), identity.ID)
	require.Equal(t, "subject-1", identity.LogtoSubject)

	visibleGroups, err := svc.Groups(context.Background(), identity.ID)
	require.NoError(t, err)
	require.Len(t, visibleGroups, 2)
	require.Equal(t, 0.8, visibleGroups[1].RateMultiplier)

	catalog, err := svc.Models(context.Background(), identity.ID, nil)
	require.NoError(t, err)
	require.NotEmpty(t, catalog)
	require.Equal(t, "shared-model", catalog[0].Model)
	require.NotEqual(t, catalog[0].GroupID, catalog[3].GroupID, "same model names must retain group ownership")

	keys, page, err := svc.Keys(context.Background(), identity.ID, pagination.PaginationParams{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
	require.Equal(t, "raw.custom-key", keys[0].Key)
	require.True(t, keys[0].IsExpired)
	require.True(t, keys[0].IsQuotaExhausted)
	require.Nil(t, keys[1].GroupID)
	require.Equal(t, "raw.custom-key", repo.keys[0].Key, "read path must not rewrite credentials")
}

func TestEcosystemServiceRejectsUnavailableAndUnboundUsersAndUnauthorizedGroupFilter(t *testing.T) {
	groups := ecosystemGroupsStub{groups: []Group{{ID: 1, Platform: PlatformAnthropic, Status: StatusActive}}}
	models := ecosystemModelsStub{models: map[int64][]string{1: {"claude"}}}

	for _, user := range []*User{nil, {ID: 1, Status: StatusDisabled}, {ID: 2, Status: StatusActive, DeletedAt: ecosystemTimePtr(time.Now())}} {
		svc := NewEcosystemService(ecosystemRepoStub{user: user}, groups, models, nil, &config.Config{})
		_, err := svc.ResolveUser(context.Background(), EcosystemTokenIdentity{Issuer: "issuer", Subject: "sub"})
		if user == nil {
			require.ErrorIs(t, err, ErrEcosystemIdentityNotLinked)
		} else {
			require.ErrorIs(t, err, ErrEcosystemUserUnavailable)
		}
	}

	svc := NewEcosystemService(ecosystemRepoStub{user: &User{ID: 1, Status: StatusActive}}, groups, models, nil, &config.Config{})
	missing := int64(999)
	_, err := svc.Models(context.Background(), 1, &missing)
	require.ErrorIs(t, err, ErrEcosystemGroupNotFound)
}

func TestDefaultModelIDsForPlatformPreservesClaudeCompatibleProviderFallback(t *testing.T) {
	want := DefaultModelIDsForPlatform(PlatformAnthropic)
	for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax} {
		require.Equal(t, want, DefaultModelIDsForPlatform(platform), "platform=%s", platform)
	}
}

func TestEcosystemServiceCompositeCatalogOnlyExposesExplicitProtocolRoutes(t *testing.T) {
	const groupID int64 = 9
	const model = "shared/model"
	groups := ecosystemGroupsStub{groups: []Group{{ID: groupID, Platform: PlatformComposite, Status: StatusActive}}}
	models := ecosystemModelsStub{models: map[int64][]string{groupID: {model}}}
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{routes: []CompositeModelRoute{
		{ID: 1, GroupID: groupID, PublicModel: model, MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, Endpoint: CompositeRouteEndpointResponses, Enabled: true},
		{ID: 2, GroupID: groupID, PublicModel: model, MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformGemini, Endpoint: CompositeRouteEndpointGemini, Enabled: true},
	}})
	svc := NewEcosystemService(ecosystemRepoStub{}, groups, models, resolver, &config.Config{Ecosystem: config.EcosystemConfig{PublicGatewayURL: "https://gateway.example.com"}})

	catalog, err := svc.Models(context.Background(), 1, nil)
	require.NoError(t, err)
	require.Equal(t, []EcosystemModel{
		{GroupID: groupID, Platform: PlatformComposite, Model: model, Protocol: "openai_responses", Endpoint: "https://gateway.example.com/v1/responses"},
		{GroupID: groupID, Platform: PlatformComposite, Model: model, Protocol: "gemini_native", Endpoint: "https://gateway.example.com/v1beta/models/shared%2Fmodel:generateContent"},
	}, catalog)
}

func ecosystemTimePtr(value time.Time) *time.Time { return &value }
