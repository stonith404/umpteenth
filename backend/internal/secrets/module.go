// Package secrets stores encrypted workspace secrets and maps them to job environment variables
package secrets

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/secrets/secretsdb"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// JobChecker verifies a job belongs to the workspace
type JobChecker interface {
	JobExists(ctx context.Context, workspaceID, jobID string) error
}

type Dependencies struct {
	DB            *database.DB
	EncryptionKey []byte
	Jobs          JobChecker
}

type Module struct {
	deps    Dependencies
	db      *database.DB
	queries *secretsdb.Queries
	key     []byte
}

func New(deps Dependencies) (*Module, error) {
	key, err := crypto.DeriveKey(deps.EncryptionKey, "secrets")
	if err != nil {
		return nil, fmt.Errorf("failed to derive secrets key: %w", err)
	}
	return &Module{deps: deps, db: deps.DB, queries: secretsdb.New(deps.DB), key: key}, nil
}

// SetJobs wires the job checker, which is built after this module
func (m *Module) SetJobs(j JobChecker) { m.deps.Jobs = j }

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("list-secrets", http.MethodGet, "/api/secrets", "Secrets"), auth, m.list)
	httpserver.Register(api, httpserver.Operation("create-secret", http.MethodPost, "/api/secrets", "Secrets"), auth, m.create)
	httpserver.Register(api, httpserver.Operation("update-secret", http.MethodPut, "/api/secrets/{id}", "Secrets"), auth, m.update)
	httpserver.Register(api, httpserver.Operation("delete-secret", http.MethodDelete, "/api/secrets/{id}", "Secrets"), auth, m.delete)
	httpserver.Register(api, httpserver.Operation("get-job-secrets", http.MethodGet, "/api/jobs/{id}/secrets", "Secrets"), auth, m.getJobSecrets)
	httpserver.Register(api, httpserver.Operation("set-job-secrets", http.MethodPut, "/api/jobs/{id}/secrets", "Secrets"), auth, m.setJobSecrets)
}

// Resolve returns the plaintext of a secret by name
func (m *Module) Resolve(ctx context.Context, workspaceID, name string) (string, error) {
	row, err := m.queries.GetSecretByName(ctx, secretsdb.GetSecretByNameParams{WorkspaceID: workspaceID, Name: name})
	if database.IsNotFound(err) {
		return "", apperror.NotFound("Secret " + name)
	} else if err != nil {
		return "", fmt.Errorf("failed to load secret %s: %w", name, err)
	}
	plain, err := crypto.Decrypt(m.key, row.ValueEnc)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt secret %s: %w", name, err)
	}
	return string(plain), nil
}

var templatePattern = regexp.MustCompile(`\{\{\s*secret:([A-Za-z0-9_.-]+)\s*\}\}`)

// Expand replaces {{secret:NAME}} references in a value with the secret's plaintext
// It stops at the first reference that can't be resolved, and never returns a partly expanded value
func (m *Module) Expand(ctx context.Context, workspaceID, value string) (string, error) {
	var firstErr error
	out := templatePattern.ReplaceAllStringFunc(value, func(ref string) string {
		if firstErr != nil {
			return ""
		}
		plain, err := m.Resolve(ctx, workspaceID, templatePattern.FindStringSubmatch(ref)[1])
		firstErr = err
		return plain
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// EnvForJob returns the job's secrets as environment variables, implementing jobs.SecretEnv
// SecretNames lists the workspace's secret names, which the compile step matches to the credentials a job needs
func (m *Module) SecretNames(ctx context.Context, workspaceID string) ([]string, error) {
	return m.queries.ListSecretNames(ctx, workspaceID)
}

func (m *Module) EnvForJob(ctx context.Context, workspaceID, jobID string) (map[string]string, error) {
	rows, err := m.queries.ListJobSecrets(ctx, secretsdb.ListJobSecretsParams{WorkspaceID: workspaceID, JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("failed to load job secrets: %w", err)
	}
	env := make(map[string]string, len(rows))
	for _, r := range rows {
		plain, err := crypto.Decrypt(m.key, r.ValueEnc)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt secret %s: %w", r.Name, err)
		}
		env[r.EnvName] = string(plain)
	}
	return env, nil
}

type secretDto struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

var listSpec = &listquery.Spec{
	Select:      "SELECT id, name, created_at, updated_at FROM secrets",
	From:        "FROM secrets",
	Sorts:       map[string]string{"name": "name", "createdAt": "created_at", "updatedAt": "updated_at"},
	DefaultSort: "name",
	Search:      []string{"name"},
	TieBreaker:  "id",
}

type listInput struct {
	httpserver.ListParams
}

// list never returns values: secrets are write-only through the API
func (m *Module) list(ctx context.Context, in *listInput) (*httpserver.PaginatedOutput[secretDto], error) {
	q := listquery.New(listSpec).WhereEq("workspace_id", principal.WorkspaceID(ctx))
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (secretDto, error) {
		var d secretDto
		err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.UpdatedAt)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

type createInput struct {
	Body struct {
		Name  string `json:"name" minLength:"1" maxLength:"100"`
		Value string `json:"value" maxLength:"65536"`
	}
}

type idOutput struct {
	Body struct {
		ID string `json:"id"`
	}
}

func (m *Module) create(ctx context.Context, in *createInput) (*idOutput, error) {
	name := strings.TrimSpace(in.Body.Name)
	if !namePattern.MatchString(name) {
		return nil, apperror.InvalidField("name", "invalid", "may only contain letters, digits, dots, dashes and underscores")
	}
	enc, err := crypto.Encrypt(m.key, []byte(in.Body.Value))
	if err != nil {
		return nil, err
	}
	id := database.NewID()
	err = m.queries.CreateSecret(ctx, secretsdb.CreateSecretParams{ID: id, WorkspaceID: principal.WorkspaceID(ctx), Name: name, ValueEnc: enc, KeyID: crypto.KeyIDV1, Now: database.Now()})
	if database.IsUniqueViolation(err) {
		return nil, apperror.AlreadyInUse("Secret name")
	} else if err != nil {
		return nil, fmt.Errorf("failed to create secret: %w", err)
	}
	out := &idOutput{}
	out.Body.ID = id
	return out, nil
}

type updateInput struct {
	ID   string `path:"id"`
	Body struct {
		Value string `json:"value" maxLength:"65536"`
	}
}

func (m *Module) update(ctx context.Context, in *updateInput) (*struct{}, error) {
	enc, err := crypto.Encrypt(m.key, []byte(in.Body.Value))
	if err != nil {
		return nil, err
	}
	n, err := m.queries.UpdateSecretValue(ctx, secretsdb.UpdateSecretValueParams{ValueEnc: enc, KeyID: crypto.KeyIDV1, UpdatedAt: database.Now(), WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to update secret: %w", err)
	}
	if n == 0 {
		return nil, apperror.NotFound("Secret")
	}
	return nil, nil
}

type idInput struct {
	ID string `path:"id"`
}

func (m *Module) delete(ctx context.Context, in *idInput) (*struct{}, error) {
	n, err := m.queries.DeleteSecret(ctx, secretsdb.DeleteSecretParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to delete secret: %w", err)
	}
	if n == 0 {
		return nil, apperror.NotFound("Secret")
	}
	return nil, nil
}

// JobSecret maps a secret to an environment variable of a job's sandbox
type JobSecret struct {
	SecretID   string `json:"secretId"`
	SecretName string `json:"secretName,omitempty" readOnly:"true"`
	EnvName    string `json:"envName" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" maxLength:"100"`
}

type jobSecretsOutput struct {
	Body []JobSecret
}

func (m *Module) getJobSecrets(ctx context.Context, in *idInput) (*jobSecretsOutput, error) {
	wid := principal.WorkspaceID(ctx)
	if err := m.deps.Jobs.JobExists(ctx, wid, in.ID); err != nil {
		return nil, err
	}
	rows, err := m.queries.ListJobSecrets(ctx, secretsdb.ListJobSecretsParams{WorkspaceID: wid, JobID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to load job secrets: %w", err)
	}
	out := &jobSecretsOutput{Body: []JobSecret{}}
	for _, r := range rows {
		out.Body = append(out.Body, JobSecret{SecretID: r.ID, SecretName: r.Name, EnvName: r.EnvName})
	}
	return out, nil
}

type setJobSecretsInput struct {
	ID   string `path:"id"`
	Body []JobSecret
}

func (m *Module) setJobSecrets(ctx context.Context, in *setJobSecretsInput) (*jobSecretsOutput, error) {
	wid := principal.WorkspaceID(ctx)
	if err := m.deps.Jobs.JobExists(ctx, wid, in.ID); err != nil {
		return nil, err
	}

	err := m.db.InTx(ctx, func(tx *database.Tx) error {
		q := secretsdb.New(tx)
		err := q.ClearJobSecrets(ctx, in.ID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, s := range in.Body {
			if seen[s.EnvName] {
				return apperror.InvalidField("envName", "duplicate", s.EnvName+" is mapped twice")
			}
			seen[s.EnvName] = true
			// Secrets from other workspaces must never be attachable
			count, err := q.SecretExists(ctx, secretsdb.SecretExistsParams{WorkspaceID: wid, ID: s.SecretID})
			if err != nil {
				return err
			}
			if count == 0 {
				return apperror.NotFound("Secret")
			}
			err = q.AddJobSecret(ctx, secretsdb.AddJobSecretParams{JobID: in.ID, SecretID: s.SecretID, EnvName: s.EnvName})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m.getJobSecrets(ctx, &idInput{ID: in.ID})
}
