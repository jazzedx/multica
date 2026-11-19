// Package giteaadmin is the small part of the Gitea / Forgejo REST API the
// server needs to set a bundled instance up for a workspace: an organization,
// a team, a webhook, and the organization's repository list. Reading pull
// requests and verifying webhooks stays in the vcs package.
package giteaadmin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound is returned when the instance answers 404.
var ErrNotFound = errors.New("giteaadmin: not found")

// ErrNameUnavailable is returned when a name cannot be created: it is
// reserved, malformed for Gitea, or already taken by a user.
var ErrNameUnavailable = errors.New("giteaadmin: name unavailable")

// Client talks to one instance as one account.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a client for the instance at baseURL (no trailing slash needed).
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Repository is one repository of an organization.
type Repository struct {
	FullName    string `json:"full_name"`
	CloneURL    string `json:"clone_url"`
	Description string `json:"description"`
}

// Hook is the definition of an organization webhook.
type Hook struct {
	URL    string
	Secret string
	Events []string
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("giteaadmin: unexpected status %d: %s", e.status, e.body)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	return c.doAs(ctx, func(req *http.Request) {
		req.Header.Set("Authorization", "token "+c.token)
	}, method, path, in, out)
}

func (c *Client) doAs(ctx context.Context, authorize func(*http.Request), method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/v1"+path, body)
	if err != nil {
		return err
	}
	authorize(req)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		snippet := string(raw)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return &apiError{status: resp.StatusCode, body: snippet}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func statusOf(err error) int {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.status
	}
	return 0
}

// TokenRejected reports whether the instance refuses this client's token: it
// was deleted, or its account may not sign in. Any other failure (the
// instance is unreachable, say) is returned as an error, so a caller does not
// replace a token that may be fine. The token needs the read:user scope.
func (c *Client) TokenRejected(ctx context.Context) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/user", nil, nil)
	if status := statusOf(err); status == http.StatusUnauthorized || status == http.StatusForbidden {
		return true, nil
	}
	return false, err
}

// SetLoginProhibited blocks or unblocks an account. A blocked account's
// tokens stop working for the API and for git, and start working again when
// it is unblocked.
func (c *Client) SetLoginProhibited(ctx context.Context, username string, prohibited bool) error {
	return c.do(ctx, http.MethodPatch, "/admin/users/"+url.PathEscape(username), map[string]any{
		"login_name":     username,
		"source_id":      0,
		"prohibit_login": prohibited,
	}, nil)
}

// CurrentLogin returns the login of the account the token belongs to.
func (c *Client) CurrentLogin(ctx context.Context) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := c.do(ctx, http.MethodGet, "/user", nil, &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

// EnsureOrg makes sure an organization called name exists. An organization
// that is already there is left as it is.
func (c *Client) EnsureOrg(ctx context.Context, name, fullName string) error {
	err := c.do(ctx, http.MethodGet, "/orgs/"+url.PathEscape(name), nil, nil)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return err
	}
	return c.CreateOrg(ctx, name, fullName)
}

// CreateOrg creates an organization called name. It returns
// ErrNameUnavailable when the name is reserved or already belongs to a user
// or to another organization, so the caller never ends up holding an
// organization it did not create.
func (c *Client) CreateOrg(ctx context.Context, name, fullName string) error {
	err := c.do(ctx, http.MethodPost, "/orgs", map[string]any{
		"username":  name,
		"full_name": fullName,
		// Visible to signed-in accounts only, like everything else on an
		// instance that requires sign-in.
		"visibility": "limited",
	}, nil)
	if statusOf(err) == http.StatusUnprocessableEntity {
		return fmt.Errorf("%w: %q", ErrNameUnavailable, name)
	}
	return err
}

// Team permissions on every repository of the organization.
const (
	PermissionRead  = "read"
	PermissionWrite = "write"
)

// EnsureTeam makes sure the organization has a team called name whose members
// have permission on every repository of the organization. An existing team
// keeps its settings.
func (c *Client) EnsureTeam(ctx context.Context, org, name, permission string) error {
	var teams []struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/orgs/"+url.PathEscape(org)+"/teams?limit=50", nil, &teams); err != nil {
		return err
	}
	for _, team := range teams {
		if strings.EqualFold(team.Name, name) {
			return nil
		}
	}
	return c.do(ctx, http.MethodPost, "/orgs/"+url.PathEscape(org)+"/teams", map[string]any{
		"name":                      name,
		"permission":                permission,
		"includes_all_repositories": true,
		"units": []string{
			"repo.code", "repo.pulls", "repo.issues", "repo.releases", "repo.actions",
		},
	}, nil)
}

// teamMemberPath is the API path of user's membership in the organization's
// team called team, or ErrNotFound when there is no such team.
func (c *Client) teamMemberPath(ctx context.Context, org, team, user string) (string, error) {
	var teams []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/orgs/"+url.PathEscape(org)+"/teams?limit=50", nil, &teams); err != nil {
		return "", err
	}
	for _, candidate := range teams {
		if strings.EqualFold(candidate.Name, team) {
			return fmt.Sprintf("/teams/%d/members/%s", candidate.ID, url.PathEscape(user)), nil
		}
	}
	return "", ErrNotFound
}

// RemoveTeamMember makes sure user does not belong to the organization's team
// called team. A user who is not a member is not an error.
func (c *Client) RemoveTeamMember(ctx context.Context, org, team, user string) error {
	member, err := c.teamMemberPath(ctx, org, team, user)
	if err != nil {
		return err
	}
	if err := c.do(ctx, http.MethodDelete, member, nil, nil); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// CreateUser creates an account that signs in with a password nobody knows;
// IssueToken is how it gets credentials. It returns ErrNameUnavailable when
// the name is taken or reserved.
func (c *Client) CreateUser(ctx context.Context, username, fullName, email string) error {
	password, err := randomPassword()
	if err != nil {
		return err
	}
	err = c.do(ctx, http.MethodPost, "/admin/users", map[string]any{
		"username":             username,
		"full_name":            fullName,
		"email":                email,
		"password":             password,
		"must_change_password": false,
		"visibility":           "limited",
	}, nil)
	if status := statusOf(err); status == http.StatusConflict || status == http.StatusUnprocessableEntity {
		return fmt.Errorf("%w: %q", ErrNameUnavailable, username)
	}
	return err
}

// tokenNamePrefix marks the tokens IssueToken creates.
const tokenNamePrefix = "multica-"

// IssueToken returns a new access token of username with scopes, and deletes
// the ones it issued to that account before, so the account has one. Gitea
// only lets an account manage its own tokens, and only with its password, so
// the account's password is replaced by a random one first; it must therefore
// be an account nobody signs in to with a password.
func (c *Client) IssueToken(ctx context.Context, username string, scopes []string) (string, error) {
	password, err := randomPassword()
	if err != nil {
		return "", err
	}
	if err := c.do(ctx, http.MethodPatch, "/admin/users/"+url.PathEscape(username), map[string]any{
		"login_name":           username,
		"source_id":            0,
		"password":             password,
		"must_change_password": false,
	}, nil); err != nil {
		return "", err
	}
	asAccount := func(req *http.Request) { req.SetBasicAuth(username, password) }
	tokens := "/users/" + url.PathEscape(username) + "/tokens"

	var existing []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := c.doAs(ctx, asAccount, http.MethodGet, tokens+"?limit=50", nil, &existing); err != nil {
		return "", err
	}
	for _, old := range existing {
		if !strings.HasPrefix(old.Name, tokenNamePrefix) {
			continue
		}
		if err := c.doAs(ctx, asAccount, http.MethodDelete, fmt.Sprintf("%s/%d", tokens, old.ID), nil, nil); err != nil && !errors.Is(err, ErrNotFound) {
			return "", err
		}
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	var created struct {
		Token string `json:"sha1"`
	}
	err = c.doAs(ctx, asAccount, http.MethodPost, tokens, map[string]any{
		// Token names are unique per account.
		"name":   tokenNamePrefix + hex.EncodeToString(suffix),
		"scopes": scopes,
	}, &created)
	if err != nil {
		return "", err
	}
	if created.Token == "" {
		return "", errors.New("giteaadmin: the instance returned no token")
	}
	return created.Token, nil
}

// randomPassword satisfies Gitea's default complexity rules (upper, lower,
// digit, symbol).
func randomPassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "Aa1-" + hex.EncodeToString(raw), nil
}

// EnsureTeamMember makes sure user belongs to the organization's team called
// team. It returns ErrNotFound when the team or the user does not exist.
func (c *Client) EnsureTeamMember(ctx context.Context, org, team, user string) error {
	member, err := c.teamMemberPath(ctx, org, team, user)
	if err != nil {
		return err
	}
	err = c.do(ctx, http.MethodGet, member, nil, nil)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return err
	}
	return c.do(ctx, http.MethodPut, member, nil, nil)
}

// DeleteUser removes an account. One that does not exist is not an error.
// What the account wrote on the instance stays, attributed to nobody. The
// instance refuses while the account still belongs to an organization.
func (c *Client) DeleteUser(ctx context.Context, username string) error {
	err := c.do(ctx, http.MethodDelete, "/admin/users/"+url.PathEscape(username), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// DeleteOrgHook removes the organization's webhooks that deliver to hookURL.
// An organization or a webhook that is not there is not an error.
func (c *Client) DeleteOrgHook(ctx context.Context, org, hookURL string) error {
	base := "/orgs/" + url.PathEscape(org) + "/hooks"
	var hooks []struct {
		ID     int64 `json:"id"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := c.do(ctx, http.MethodGet, base+"?limit=50", nil, &hooks); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	for _, hook := range hooks {
		if hook.Config.URL != hookURL {
			continue
		}
		if err := c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/%d", base, hook.ID), nil, nil); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}

// EnsureOrgHook makes sure the organization delivers hook.Events to hook.URL.
// A hook for that URL that already exists is rewritten only when refresh is
// set, because the stored secret cannot be read back to compare.
func (c *Client) EnsureOrgHook(ctx context.Context, org string, hook Hook, refresh bool) error {
	base := "/orgs/" + url.PathEscape(org) + "/hooks"
	var hooks []struct {
		ID     int64 `json:"id"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := c.do(ctx, http.MethodGet, base+"?limit=50", nil, &hooks); err != nil {
		return err
	}
	config := map[string]string{
		"url":          hook.URL,
		"content_type": "json",
		"secret":       hook.Secret,
	}
	for _, existing := range hooks {
		if existing.Config.URL != hook.URL {
			continue
		}
		if !refresh {
			return nil
		}
		return c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/%d", base, existing.ID), map[string]any{
			"active": true,
			"events": hook.Events,
			"config": config,
		}, nil)
	}
	return c.do(ctx, http.MethodPost, base, map[string]any{
		"type":   "gitea",
		"active": true,
		"events": hook.Events,
		"config": config,
	}, nil)
}

// maxOrgRepositories bounds how many repositories ListOrgRepositories returns.
const maxOrgRepositories = 500

// ListOrgRepositories returns the repositories of an organization.
func (c *Client) ListOrgRepositories(ctx context.Context, org string) ([]Repository, error) {
	const pageSize = 50
	var all []Repository
	for page := 1; len(all) < maxOrgRepositories; page++ {
		var batch []Repository
		path := fmt.Sprintf("/orgs/%s/repos?page=%d&limit=%d", url.PathEscape(org), page, pageSize)
		if err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < pageSize {
			break
		}
	}
	return all, nil
}
