package repository

import (
	"context"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type ecosystemRepository struct {
	client *dbent.Client
}

func NewEcosystemRepository(client *dbent.Client) service.EcosystemRepository {
	return &ecosystemRepository{client: client}
}

func (r *ecosystemRepository) FindUserByLogtoIdentity(ctx context.Context, issuer, subject string) (*service.User, error) {
	identity, err := r.client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ(strings.TrimSpace(issuer)),
			authidentity.ProviderSubjectEQ(strings.TrimSpace(subject)),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrUserNotFound
		}
		return nil, err
	}
	entity, err := r.client.User.Query().
		Where(user.IDEQ(identity.UserID)).
		Only(mixins.SkipSoftDelete(ctx))
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrUserNotFound
		}
		return nil, err
	}
	return userEntityToService(entity), nil
}

func (r *ecosystemRepository) ListActiveKeys(
	ctx context.Context,
	userID int64,
	params pagination.PaginationParams,
) ([]service.APIKey, *pagination.PaginationResult, error) {
	query := r.client.APIKey.Query().Where(
		apikey.UserIDEQ(userID),
		apikey.StatusEQ(service.StatusAPIKeyActive),
		apikey.DeletedAtIsNil(),
	)
	total, err := query.Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	entities, err := query.WithGroup().
		Order(dbent.Asc(apikey.FieldID)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]service.APIKey, 0, len(entities))
	for _, entity := range entities {
		keys = append(keys, *apiKeyEntityToService(entity))
	}
	return keys, paginationResultFromTotal(int64(total), params), nil
}
