package store

import (
	"context"

	"dochub/internal/docparse"
)

func (s *Store) ListUserSummaries(ctx context.Context) ([]UserSummary, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, username, role FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []UserSummary
	for rows.Next() {
		var user UserSummary
		if err := rows.Scan(&user.ID, &user.Username, &user.Role); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) ProjectReadAccess(ctx context.Context, projectID, userID string) (ReadAccess, error) {
	// 项目级授权覆盖整个项目，命中后无需再加载文档级路径。
	var full bool
	if err := s.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM project_read_permissions
			WHERE project_id = $1 AND user_id = $2
			UNION ALL
			SELECT 1
			FROM project_read_organizations pro
			JOIN organization_members om ON om.organization_id = pro.organization_id
			WHERE pro.project_id = $1 AND om.user_id = $2
		)`, projectID, userID,
	).Scan(&full); err != nil {
		return ReadAccess{}, err
	}
	if full {
		return ReadAccess{Full: true}, nil
	}
	rows, err := s.DB.Query(ctx, `
		SELECT document_path
		FROM (
			SELECT document_path
			FROM document_read_permissions
			WHERE project_id = $1 AND user_id = $2
			UNION
			SELECT dro.document_path
			FROM document_read_organizations dro
			JOIN organization_members om ON om.organization_id = dro.organization_id
			WHERE dro.project_id = $1 AND om.user_id = $2
		) authorized_documents
		ORDER BY document_path`, projectID, userID)
	if err != nil {
		return ReadAccess{}, err
	}
	defer rows.Close()
	access := ReadAccess{}
	for rows.Next() {
		var documentPath string
		if err := rows.Scan(&documentPath); err != nil {
			return ReadAccess{}, err
		}
		access.Documents = append(access.Documents, documentPath)
	}
	return access, rows.Err()
}

func (s *Store) ProjectAccess(ctx context.Context, projectID string) (ProjectAccess, error) {
	var result ProjectAccess
	projectRows, err := s.DB.Query(ctx, `
		SELECT u.id, u.username, u.role
		FROM project_read_permissions prp
		JOIN users u ON u.id = prp.user_id
		WHERE prp.project_id = $1
		ORDER BY u.username`, projectID)
	if err != nil {
		return result, err
	}
	for projectRows.Next() {
		var user UserSummary
		if err := projectRows.Scan(&user.ID, &user.Username, &user.Role); err != nil {
			projectRows.Close()
			return result, err
		}
		result.ProjectUsers = append(result.ProjectUsers, user)
	}
	if err := projectRows.Err(); err != nil {
		projectRows.Close()
		return result, err
	}
	projectRows.Close()

	projectOrganizationRows, err := s.DB.Query(ctx, `
		SELECT o.id, o.name
		FROM project_read_organizations pro
		JOIN organizations o ON o.id = pro.organization_id
		WHERE pro.project_id = $1
		ORDER BY LOWER(o.name), o.id`, projectID)
	if err != nil {
		return result, err
	}
	for projectOrganizationRows.Next() {
		var organization OrganizationSummary
		if err := projectOrganizationRows.Scan(&organization.ID, &organization.Name); err != nil {
			projectOrganizationRows.Close()
			return result, err
		}
		result.ProjectOrganizations = append(result.ProjectOrganizations, organization)
	}
	if err := projectOrganizationRows.Err(); err != nil {
		projectOrganizationRows.Close()
		return result, err
	}
	projectOrganizationRows.Close()

	documentRows, err := s.DB.Query(ctx, `
		SELECT drp.document_path, u.id, u.username, u.role
		FROM document_read_permissions drp
		JOIN users u ON u.id = drp.user_id
		WHERE drp.project_id = $1
		ORDER BY drp.document_path, u.username`, projectID)
	if err != nil {
		return result, err
	}
	defer documentRows.Close()
	documentIndex := make(map[string]int)
	for documentRows.Next() {
		var documentPath string
		var user UserSummary
		if err := documentRows.Scan(&documentPath, &user.ID, &user.Username, &user.Role); err != nil {
			return result, err
		}
		index, exists := documentIndex[documentPath]
		if !exists {
			index = len(result.Documents)
			documentIndex[documentPath] = index
			result.Documents = append(result.Documents, DocumentAccess{Path: documentPath})
		}
		result.Documents[index].Users = append(result.Documents[index].Users, user)
	}
	if err := documentRows.Err(); err != nil {
		return result, err
	}
	documentRows.Close()

	documentOrganizationRows, err := s.DB.Query(ctx, `
		SELECT dro.document_path, o.id, o.name
		FROM document_read_organizations dro
		JOIN organizations o ON o.id = dro.organization_id
		WHERE dro.project_id = $1
		ORDER BY dro.document_path, LOWER(o.name), o.id`, projectID)
	if err != nil {
		return result, err
	}
	defer documentOrganizationRows.Close()
	for documentOrganizationRows.Next() {
		var documentPath string
		var organization OrganizationSummary
		if err := documentOrganizationRows.Scan(
			&documentPath,
			&organization.ID,
			&organization.Name,
		); err != nil {
			return result, err
		}
		index, exists := documentIndex[documentPath]
		if !exists {
			index = len(result.Documents)
			documentIndex[documentPath] = index
			result.Documents = append(result.Documents, DocumentAccess{Path: documentPath})
		}
		result.Documents[index].Organizations = append(
			result.Documents[index].Organizations,
			organization,
		)
	}
	return result, documentOrganizationRows.Err()
}

func (s *Store) SetProjectReadAccess(
	ctx context.Context,
	projectID string,
	userIDs []string,
	organizationIDs []string,
	createdBy string,
) error {
	userIDs = uniqueStrings(userIDs)
	organizationIDs = uniqueStrings(organizationIDs)
	if err := s.ensureUsersExist(ctx, userIDs); err != nil {
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
	if _, err = tx.Exec(ctx, `DELETE FROM project_read_permissions WHERE project_id = $1`, projectID); err != nil {
		return err
	}
	for _, userID := range userIDs {
		if _, err = tx.Exec(ctx, `
			INSERT INTO project_read_permissions (project_id, user_id, created_by)
			VALUES ($1, $2, $3)`, projectID, userID, createdBy,
		); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM project_read_organizations WHERE project_id = $1`, projectID); err != nil {
		return err
	}
	for _, organizationID := range organizationIDs {
		if _, err = tx.Exec(ctx, `
			INSERT INTO project_read_organizations (project_id, organization_id, created_by)
			VALUES ($1, $2, $3)`, projectID, organizationID, createdBy,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SetDocumentReadAccess(
	ctx context.Context,
	projectID, documentPath string,
	userIDs []string,
	organizationIDs []string,
	createdBy string,
) error {
	userIDs = uniqueStrings(userIDs)
	organizationIDs = uniqueStrings(organizationIDs)
	if err := s.ensureUsersExist(ctx, userIDs); err != nil {
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
	if _, err = tx.Exec(ctx, `
		DELETE FROM document_read_permissions
		WHERE project_id = $1 AND document_path = $2`, projectID, documentPath,
	); err != nil {
		return err
	}
	for _, userID := range userIDs {
		if _, err = tx.Exec(ctx, `
			INSERT INTO document_read_permissions
				(project_id, document_path, user_id, created_by)
			VALUES ($1, $2, $3, $4)`,
			projectID, documentPath, userID, createdBy,
		); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM document_read_organizations
		WHERE project_id = $1 AND document_path = $2`,
		projectID,
		documentPath,
	); err != nil {
		return err
	}
	for _, organizationID := range organizationIDs {
		if _, err = tx.Exec(ctx, `
			INSERT INTO document_read_organizations
				(project_id, document_path, organization_id, created_by)
			VALUES ($1, $2, $3, $4)`,
			projectID,
			documentPath,
			organizationID,
			createdBy,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) CanReadDocumentAsset(
	ctx context.Context,
	projectID, branch, userID, assetPath string,
) (bool, error) {
	// 资源只能通过“已授权文档实际引用它”这一关系读取，
	// 避免拿到文档级权限后直接探测仓库里的其他文件。
	var allowed bool
	err := s.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM document_read_permissions drp
			JOIN project_branches pb
			  ON pb.project_id = drp.project_id AND pb.name = $2
			JOIN document_assets da
			  ON da.branch_id = pb.id
			 AND da.document_path = drp.document_path
			 AND da.asset_path = $4
			WHERE drp.project_id = $1 AND drp.user_id = $3
			UNION ALL
			SELECT 1
			FROM document_read_organizations dro
			JOIN organization_members om ON om.organization_id = dro.organization_id
			JOIN project_branches pb
			  ON pb.project_id = dro.project_id AND pb.name = $2
			JOIN document_assets da
			  ON da.branch_id = pb.id
			 AND da.document_path = dro.document_path
			 AND da.asset_path = $4
			WHERE dro.project_id = $1 AND om.user_id = $3
		)`, projectID, branch, userID, assetPath,
	).Scan(&allowed)
	return allowed, err
}

func (s *Store) backfillDocumentAssets(ctx context.Context) error {
	// 升级已有数据时补建引用关系，新同步由 FinishBranchSync 原子写入。
	rows, err := s.DB.Query(ctx, `
		SELECT d.branch_id, d.path, d.markdown
		FROM documents d
		WHERE NOT EXISTS (
			SELECT 1 FROM document_assets da
			WHERE da.branch_id = d.branch_id AND da.document_path = d.path
		)`)
	if err != nil {
		return err
	}
	type documentRecord struct {
		branchID string
		path     string
		markdown string
	}
	var records []documentRecord
	for rows.Next() {
		var record documentRecord
		if err := rows.Scan(&record.branchID, &record.path, &record.markdown); err != nil {
			rows.Close()
			return err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, record := range records {
		for _, assetPath := range docparse.ExtractAssets(record.markdown, record.path) {
			if _, err := s.DB.Exec(ctx, `
				INSERT INTO document_assets (branch_id, document_path, asset_path)
				VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`,
				record.branchID, record.path, assetPath,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ensureUsersExist(ctx context.Context, userIDs []string) error {
	if len(userIDs) == 0 {
		return nil
	}
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE id = ANY($1)`, userIDs).Scan(&count); err != nil {
		return err
	}
	if count != len(userIDs) {
		return ErrNotFound
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
