// Package giteaadmintest is an in-memory stand-in for the part of the Gitea
// API that giteaadmin calls, for tests of the client and of its callers.
package giteaadmintest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Hook is a webhook as the fake stores it.
type Hook struct {
	ID     int64
	URL    string
	Secret string
	Events []string
	Writes int // how many times it was created or rewritten
}

// Repo is a repository of an organization.
type Repo struct {
	Name        string
	Description string
}

// Server is the fake instance. Token is the only token it accepts.
type Server struct {
	*httptest.Server
	Token string
	Login string

	mu         sync.Mutex
	orgs       map[string]string // name -> full name
	teams      map[string][]string
	hooks      map[string][]*Hook
	repos      map[string][]Repo
	taken      map[string]bool // names an organization cannot use
	users      map[string]*account
	teamKeys   []string            // "org/team", index + 1 is the team's id
	members    map[string][]string // "org/team" -> logins
	nextHookID int64
	tokenSeq   int
}

// New starts a fake instance that is closed when the test ends.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{
		Token:   "service-token",
		Login:   "multica-service",
		orgs:    map[string]string{},
		teams:   map[string][]string{},
		hooks:   map[string][]*Hook{},
		repos:   map[string][]Repo{},
		taken:   map[string]bool{},
		users:   map[string]*account{},
		members: map[string][]string{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// SetToken replaces the token the instance accepts.
func (s *Server) SetToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Token = token
}

// TakeName makes name unavailable for organizations, as a reserved word or an
// existing user would.
func (s *Server) TakeName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taken[name] = true
}

// account is a user of the fake instance.
type account struct {
	fullName   string
	password   string
	prohibited bool
	tokens     map[string][]string // token -> scopes
	tokenIDs   map[int]string      // id -> token
}

// AddUser creates an account.
func (s *Server) AddUser(login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[login] = &account{tokens: map[string][]string{}}
}

// AddOrg creates an organization, as someone could on the instance's own
// pages or an earlier workspace could have left behind.
func (s *Server) AddOrg(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orgs[name] = name
	s.teams[name] = []string{"Owners"}
}

// HasUser reports whether the account exists.
func (s *Server) HasUser(login string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users[login] != nil
}

// TokenScopes returns the scopes of one of the account's tokens, and whether
// the token exists.
func (s *Server) TokenScopes(login, token string) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.users[login]
	if user == nil {
		return nil, false
	}
	scopes, ok := user.tokens[token]
	return scopes, ok
}

// RevokeTokens deletes every token of the account, as someone could on the
// instance's own pages.
func (s *Server) RevokeTokens(login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user := s.users[login]; user != nil {
		user.tokens = map[string][]string{}
	}
}

// LoginProhibited reports whether the account is blocked.
func (s *Server) LoginProhibited(login string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.users[login]
	return user != nil && user.prohibited
}

// TokenCount returns how many tokens the account has.
func (s *Server) TokenCount(login string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user := s.users[login]; user != nil {
		return len(user.tokens)
	}
	return 0
}

// TeamMembers returns the logins in an organization's team.
func (s *Server) TeamMembers(org, team string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.members[org+"/"+team]...)
}

// AddRepo adds a repository to an organization.
func (s *Server) AddRepo(org, name, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos[org] = append(s.repos[org], Repo{Name: name, Description: description})
}

// CloneURL is the clone URL the fake reports for a repository.
func (s *Server) CloneURL(org, name string) string {
	return s.URL + "/" + org + "/" + name + ".git"
}

// HasOrg reports whether the organization exists.
func (s *Server) HasOrg(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.orgs[name]
	return ok
}

// Teams returns the organization's team names.
func (s *Server) Teams(org string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.teams[org]...)
}

// Hooks returns copies of the organization's webhooks.
func (s *Server) Hooks(org string) []Hook {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Hook, 0, len(s.hooks[org]))
	for _, hook := range s.hooks[org] {
		out = append(out, *hook)
	}
	return out
}

type hookBody struct {
	Events []string `json:"events"`
	Config struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	} `json:"config"`
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Like Gitea, creating a token takes the account's own password and
	// nothing else.
	if len(parts) >= 3 && parts[0] == "users" && parts[2] == "tokens" {
		s.serveTokens(w, r, parts[1], parts[3:])
		return
	}
	if r.Header.Get("Authorization") != "token "+s.Token {
		// The only thing an account's own token is used for here is asking
		// who it belongs to.
		if path == "/user" && r.Method == http.MethodGet {
			s.serveOwnUser(w, strings.TrimPrefix(r.Header.Get("Authorization"), "token "))
			return
		}
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	switch {
	case path == "/admin/users" && r.Method == http.MethodPost:
		var body struct {
			Username string `json:"username"`
			FullName string `json:"full_name"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if s.users[body.Username] != nil || s.taken[body.Username] || s.orgs[body.Username] != "" {
			http.Error(w, `{"message":"user already exists"}`, http.StatusConflict)
			return
		}
		s.users[body.Username] = &account{fullName: body.FullName, password: body.Password, tokens: map[string][]string{}}
		writeJSON(w, http.StatusCreated, map[string]string{"login": body.Username})

	case len(parts) == 3 && parts[0] == "admin" && parts[1] == "users" && r.Method == http.MethodPatch:
		user := s.users[parts[2]]
		if user == nil {
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		var body struct {
			Password      string `json:"password"`
			ProhibitLogin *bool  `json:"prohibit_login"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Password != "" {
			user.password = body.Password
		}
		if body.ProhibitLogin != nil {
			user.prohibited = *body.ProhibitLogin
		}
		writeJSON(w, http.StatusOK, map[string]string{"login": parts[2]})

	case len(parts) == 3 && parts[0] == "admin" && parts[1] == "users" && r.Method == http.MethodDelete:
		if s.users[parts[2]] == nil {
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		// Like Gitea, an account that still belongs to an organization
		// cannot be deleted.
		for _, members := range s.members {
			for _, member := range members {
				if member == parts[2] {
					http.Error(w, `{"message":"user still has membership of organizations"}`, http.StatusUnprocessableEntity)
					return
				}
			}
		}
		delete(s.users, parts[2])
		w.WriteHeader(http.StatusNoContent)

	case path == "/user" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]string{"login": s.Login})

	case path == "/orgs" && r.Method == http.MethodPost:
		var body struct {
			Username string `json:"username"`
			FullName string `json:"full_name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, exists := s.orgs[body.Username]
		if s.taken[body.Username] || exists || s.users[body.Username] != nil {
			http.Error(w, `{"message":"name is reserved or already used"}`, http.StatusUnprocessableEntity)
			return
		}
		s.orgs[body.Username] = body.FullName
		// Like Gitea, every organization starts with an owners team.
		s.teams[body.Username] = []string{"Owners"}
		writeJSON(w, http.StatusCreated, map[string]string{"username": body.Username})

	case len(parts) == 4 && parts[0] == "teams" && parts[2] == "members":
		s.serveTeamMember(w, r, parts[1], parts[3])

	case len(parts) >= 2 && parts[0] == "orgs":
		org := parts[1]
		if _, ok := s.orgs[org]; !ok {
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		s.serveOrg(w, r, org, parts[2:])

	default:
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}
}

func (s *Server) serveOrg(w http.ResponseWriter, r *http.Request, org string, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]string{"username": org})

	case len(rest) == 1 && rest[0] == "teams" && r.Method == http.MethodGet:
		out := make([]map[string]any, 0, len(s.teams[org]))
		for _, name := range s.teams[org] {
			out = append(out, map[string]any{"id": s.teamID(org, name), "name": name})
		}
		writeJSON(w, http.StatusOK, out)

	case len(rest) == 1 && rest[0] == "teams" && r.Method == http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.teams[org] = append(s.teams[org], body.Name)
		writeJSON(w, http.StatusCreated, map[string]string{"name": body.Name})

	case len(rest) == 1 && rest[0] == "hooks" && r.Method == http.MethodGet:
		out := make([]map[string]any, 0, len(s.hooks[org]))
		for _, hook := range s.hooks[org] {
			out = append(out, map[string]any{"id": hook.ID, "config": map[string]string{"url": hook.URL}})
		}
		writeJSON(w, http.StatusOK, out)

	case len(rest) == 1 && rest[0] == "hooks" && r.Method == http.MethodPost:
		var body hookBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.nextHookID++
		s.hooks[org] = append(s.hooks[org], &Hook{
			ID: s.nextHookID, URL: body.Config.URL, Secret: body.Config.Secret, Events: body.Events, Writes: 1,
		})
		writeJSON(w, http.StatusCreated, map[string]any{"id": s.nextHookID})

	case len(rest) == 2 && rest[0] == "hooks" && r.Method == http.MethodPatch:
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		var body hookBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, hook := range s.hooks[org] {
			if hook.ID == id {
				hook.URL, hook.Secret, hook.Events = body.Config.URL, body.Config.Secret, body.Events
				hook.Writes++
				writeJSON(w, http.StatusOK, map[string]any{"id": id})
				return
			}
		}
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)

	case len(rest) == 2 && rest[0] == "hooks" && r.Method == http.MethodDelete:
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		kept := s.hooks[org][:0]
		for _, hook := range s.hooks[org] {
			if hook.ID != id {
				kept = append(kept, hook)
			}
		}
		s.hooks[org] = kept
		w.WriteHeader(http.StatusNoContent)

	case len(rest) == 1 && rest[0] == "repos" && r.Method == http.MethodGet:
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if page < 1 {
			page = 1
		}
		if limit < 1 {
			limit = 50
		}
		all := s.repos[org]
		start := min((page-1)*limit, len(all))
		end := min(start+limit, len(all))
		out := make([]map[string]string, 0, end-start)
		for _, repo := range all[start:end] {
			out = append(out, map[string]string{
				"full_name":   fmt.Sprintf("%s/%s", org, repo.Name),
				"clone_url":   s.URL + "/" + org + "/" + repo.Name + ".git",
				"description": repo.Description,
			})
		}
		writeJSON(w, http.StatusOK, out)

	default:
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}
}

// teamID gives each team a stable numeric id, as the member endpoints address
// teams by id.
func (s *Server) teamID(org, name string) int64 {
	key := org + "/" + name
	for i, known := range s.teamKeys {
		if known == key {
			return int64(i + 1)
		}
	}
	s.teamKeys = append(s.teamKeys, key)
	return int64(len(s.teamKeys))
}

func (s *Server) serveTeamMember(w http.ResponseWriter, r *http.Request, rawID, login string) {
	id, _ := strconv.ParseInt(rawID, 10, 64)
	if id < 1 || int(id) > len(s.teamKeys) || s.users[login] == nil {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		return
	}
	key := s.teamKeys[id-1]
	isMember := false
	for _, member := range s.members[key] {
		if member == login {
			isMember = true
		}
	}
	switch r.Method {
	case http.MethodGet:
		if !isMember {
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"login": login})
	case http.MethodPut:
		if !isMember {
			s.members[key] = append(s.members[key], login)
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		kept := s.members[key][:0]
		for _, member := range s.members[key] {
			if member != login {
				kept = append(kept, member)
			}
		}
		s.members[key] = kept
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}
}

func (s *Server) serveOwnUser(w http.ResponseWriter, token string) {
	for login, user := range s.users {
		if _, ok := user.tokens[token]; !ok {
			continue
		}
		if user.prohibited {
			http.Error(w, `{"message":"sign-in prohibited"}`, http.StatusForbidden)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"login": login})
		return
	}
	http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
}

// serveTokens lists, creates and deletes an account's tokens. Like Gitea, it
// takes the account's own password and nothing else.
func (s *Server) serveTokens(w http.ResponseWriter, r *http.Request, login string, rest []string) {
	user := s.users[login]
	name, password, ok := r.BasicAuth()
	if user == nil || user.prohibited || !ok || name != login || password == "" || password != user.password {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if user.tokenIDs == nil {
		user.tokenIDs = map[int]string{}
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		out := make([]map[string]any, 0, len(user.tokenIDs))
		for id, token := range user.tokenIDs {
			if _, live := user.tokens[token]; live {
				out = append(out, map[string]any{"id": id, "name": "multica-" + strconv.Itoa(id)})
			}
		}
		writeJSON(w, http.StatusOK, out)
	case len(rest) == 0 && r.Method == http.MethodPost:
		var body struct {
			Scopes []string `json:"scopes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.tokenSeq++
		token := fmt.Sprintf("token-%s-%d", login, s.tokenSeq)
		user.tokens[token] = body.Scopes
		user.tokenIDs[s.tokenSeq] = token
		writeJSON(w, http.StatusCreated, map[string]string{"sha1": token})
	case len(rest) == 1 && r.Method == http.MethodDelete:
		id, _ := strconv.Atoi(rest[0])
		delete(user.tokens, user.tokenIDs[id])
		delete(user.tokenIDs, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
