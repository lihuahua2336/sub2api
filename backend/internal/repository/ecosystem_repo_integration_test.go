//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestEcosystemRepositoryCanonicalIdentityAndOwnedActiveKeys(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := NewEcosystemRepository(client)

	owner := mustCreateUser(t, client, &service.User{Email: "ecosystem-owner@example.com"})
	other := mustCreateUser(t, client, &service.User{Email: "ecosystem-other@example.com"})
	_, err := client.AuthIdentity.Create().
		SetUserID(owner.ID).
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example/oidc").
		SetProviderSubject("logto-user").
		Save(ctx)
	require.NoError(t, err)

	resolved, err := repo.FindUserByLogtoIdentity(ctx, "https://issuer.example/oidc", "logto-user")
	require.NoError(t, err)
	require.Equal(t, owner.ID, resolved.ID)
	_, err = repo.FindUserByLogtoIdentity(ctx, "https://issuer.example/oidc", "other-subject")
	require.ErrorIs(t, err, service.ErrUserNotFound)

	first := mustCreateApiKey(t, client, &service.APIKey{UserID: owner.ID, Key: "custom-first", Name: "first", Status: service.StatusActive})
	second := mustCreateApiKey(t, client, &service.APIKey{UserID: owner.ID, Key: "custom-second", Name: "second", Status: service.StatusActive})
	disabled := mustCreateApiKey(t, client, &service.APIKey{UserID: owner.ID, Key: "custom-disabled", Name: "disabled", Status: service.StatusDisabled})
	deleted := mustCreateApiKey(t, client, &service.APIKey{UserID: owner.ID, Key: "custom-deleted", Name: "deleted", Status: service.StatusActive})
	_ = mustCreateApiKey(t, client, &service.APIKey{UserID: other.ID, Key: "custom-other", Name: "other", Status: service.StatusActive})
	_, err = client.APIKey.UpdateOneID(deleted.ID).SetDeletedAt(time.Now()).Save(ctx)
	require.NoError(t, err)

	beforeUsers, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	beforeIdentities, err := client.AuthIdentity.Query().Count(ctx)
	require.NoError(t, err)
	beforeKeys, err := client.APIKey.Query().Count(ctx)
	require.NoError(t, err)

	pageOne, page, err := repo.ListActiveKeys(ctx, owner.ID, pagination.PaginationParams{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.Equal(t, []int64{first.ID}, []int64{pageOne[0].ID})
	require.Equal(t, int64(2), page.Total)
	require.Equal(t, 2, page.Pages)
	pageTwo, _, err := repo.ListActiveKeys(ctx, owner.ID, pagination.PaginationParams{Page: 2, PageSize: 1})
	require.NoError(t, err)
	require.Equal(t, []int64{second.ID}, []int64{pageTwo[0].ID})
	require.NotEqual(t, disabled.ID, pageTwo[0].ID)

	afterUsers, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	afterIdentities, err := client.AuthIdentity.Query().Count(ctx)
	require.NoError(t, err)
	afterKeys, err := client.APIKey.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeUsers, afterUsers)
	require.Equal(t, beforeIdentities, afterIdentities)
	require.Equal(t, beforeKeys, afterKeys)

	require.NoError(t, client.User.UpdateOneID(owner.ID).SetStatus(service.StatusDisabled).Exec(ctx))
	disabledUser, err := repo.FindUserByLogtoIdentity(ctx, "https://issuer.example/oidc", "logto-user")
	require.NoError(t, err)
	require.Equal(t, service.StatusDisabled, disabledUser.Status)
	require.NoError(t, client.User.UpdateOneID(owner.ID).SetDeletedAt(time.Now()).SetStatus(service.StatusActive).Exec(ctx))
	deletedUser, err := repo.FindUserByLogtoIdentity(ctx, "https://issuer.example/oidc", "logto-user")
	require.NoError(t, err)
	require.NotNil(t, deletedUser.DeletedAt)

	activeRows, err := client.APIKey.Query().Where(apikey.UserIDEQ(owner.ID), apikey.StatusEQ(service.StatusAPIKeyActive)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, activeRows)
	deletedRows, err := client.User.Query().Where(user.IDEQ(owner.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, deletedRows)
}
