package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) ListOrganizations(ctx context.Context) ([]Organization, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, name, created_at, updated_at
		FROM organizations
		ORDER BY LOWER(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	organizations := make([]Organization, 0)
	for rows.Next() {
		var organization Organization
		if err := rows.Scan(
			&organization.ID,
			&organization.Name,
			&organization.CreatedAt,
			&organization.UpdatedAt,
		); err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range organizations {
		organizations[index].Members, err = s.listOrganizationMembers(ctx, organizations[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return organizations, nil
}

func (s *Store) ListOrganizationSummaries(ctx context.Context) ([]OrganizationSummary, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, name
		FROM organizations
		ORDER BY LOWER(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	organizations := make([]OrganizationSummary, 0)
	for rows.Next() {
		var organization OrganizationSummary
		if err := rows.Scan(&organization.ID, &organization.Name); err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	return organizations, rows.Err()
}

func (s *Store) CreateOrganization(
	ctx context.Context,
	name string,
	userIDs []string,
) (Organization, error) {
	userIDs = uniqueStrings(userIDs)
	if err := s.ensureUsersExist(ctx, userIDs); err != nil {
		return Organization{}, err
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Organization{}, err
	}
	defer tx.Rollback(ctx)

	organization := Organization{ID: newID(), Name: strings.TrimSpace(name)}
	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (id, name)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
		RETURNING created_at, updated_at`,
		organization.ID,
		organization.Name,
	).Scan(&organization.CreatedAt, &organization.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrOrganizationNameExists
	}
	if err != nil {
		return Organization{}, err
	}
	if err := replaceOrganizationMembers(ctx, tx, organization.ID, userIDs); err != nil {
		return Organization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, err
	}
	organization.Members, err = s.listOrganizationMembers(ctx, organization.ID)
	return organization, err
}

func (s *Store) UpdateOrganization(
	ctx context.Context,
	organizationID, name string,
	userIDs []string,
) (Organization, error) {
	userIDs = uniqueStrings(userIDs)
	if err := s.ensureUsersExist(ctx, userIDs); err != nil {
		return Organization{}, err
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Organization{}, err
	}
	defer tx.Rollback(ctx)

	var organization Organization
	err = tx.QueryRow(ctx, `
		UPDATE organizations
		SET name = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, created_at, updated_at`,
		organizationID,
		strings.TrimSpace(name),
	).Scan(
		&organization.ID,
		&organization.Name,
		&organization.CreatedAt,
		&organization.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrOrganizationNotFound
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Organization{}, ErrOrganizationNameExists
		}
		return Organization{}, err
	}
	if err := replaceOrganizationMembers(ctx, tx, organization.ID, userIDs); err != nil {
		return Organization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, err
	}
	organization.Members, err = s.listOrganizationMembers(ctx, organization.ID)
	return organization, err
}

func (s *Store) DeleteOrganization(ctx context.Context, organizationID string) error {
	result, err := s.DB.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, organizationID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrOrganizationNotFound
	}
	return nil
}

func (s *Store) SetUserOrganizations(
	ctx context.Context,
	userID string,
	organizationIDs []string,
) error {
	organizationIDs = uniqueStrings(organizationIDs)
	if err := s.ensureUsersExist(ctx, []string{userID}); err != nil {
		return err
	}
	if err := s.ensureOrganizationsExist(ctx, organizationIDs); err != nil {
		return err
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	oldRows, err := tx.Query(ctx, `
		SELECT organization_id
		FROM organization_members
		WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		return err
	}
	touchedOrganizationIDs := make([]string, 0, len(organizationIDs))
	for oldRows.Next() {
		var organizationID string
		if err := oldRows.Scan(&organizationID); err != nil {
			oldRows.Close()
			return err
		}
		touchedOrganizationIDs = append(touchedOrganizationIDs, organizationID)
	}
	if err := oldRows.Err(); err != nil {
		oldRows.Close()
		return err
	}
	oldRows.Close()
	touchedOrganizationIDs = uniqueStrings(append(touchedOrganizationIDs, organizationIDs...))

	if _, err = tx.Exec(ctx, `DELETE FROM organization_members WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, organizationID := range organizationIDs {
		if _, err = tx.Exec(ctx, `
			INSERT INTO organization_members (organization_id, user_id)
			VALUES ($1, $2)`,
			organizationID,
			userID,
		); err != nil {
			return err
		}
	}
	if len(touchedOrganizationIDs) > 0 {
		if _, err = tx.Exec(ctx, `
			UPDATE organizations
			SET updated_at = NOW()
			WHERE id = ANY($1)`,
			touchedOrganizationIDs,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) listOrganizationMembers(
	ctx context.Context,
	organizationID string,
) ([]UserSummary, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT u.id, u.username, u.role
		FROM organization_members om
		JOIN users u ON u.id = om.user_id
		WHERE om.organization_id = $1
		ORDER BY u.username`,
		organizationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]UserSummary, 0)
	for rows.Next() {
		var member UserSummary
		if err := rows.Scan(&member.ID, &member.Username, &member.Role); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func replaceOrganizationMembers(
	ctx context.Context,
	tx pgx.Tx,
	organizationID string,
	userIDs []string,
) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM organization_members
		WHERE organization_id = $1`,
		organizationID,
	); err != nil {
		return err
	}
	for _, userID := range userIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO organization_members (organization_id, user_id)
			VALUES ($1, $2)`,
			organizationID,
			userID,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureOrganizationsExist(ctx context.Context, organizationIDs []string) error {
	if len(organizationIDs) == 0 {
		return nil
	}
	var count int
	if err := s.DB.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM organizations
		WHERE id = ANY($1)`,
		organizationIDs,
	).Scan(&count); err != nil {
		return err
	}
	if count != len(organizationIDs) {
		return ErrOrganizationNotFound
	}
	return nil
}
