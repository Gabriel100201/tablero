package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const linearAPI = "https://api.linear.app/graphql"

type linearProvider struct {
	name   string
	apiKey string
	client *http.Client
}

func NewLinear(name, apiKey string) Provider {
	return &linearProvider{
		name:   name,
		apiKey: apiKey,
		client: &http.Client{},
	}
}

func (l *linearProvider) Name() string { return l.name }
func (l *linearProvider) Type() string { return "linear" }

func (l *linearProvider) ListTasks(ctx context.Context, opts ListOpts) ([]Task, error) {
	filter := map[string]any{"state": buildStateFilter(opts.State)}

	if opts.Assigned {
		filter["assignee"] = map[string]any{"isMe": map[string]any{"eq": true}}
	}

	if opts.Project != "" {
		filter["or"] = []any{
			map[string]any{"project": map[string]any{"name": map[string]any{"eqIgnoreCase": opts.Project}}},
			map[string]any{"team": map[string]any{"key": map[string]any{"eqIgnoreCase": opts.Project}}},
			map[string]any{"team": map[string]any{"name": map[string]any{"eqIgnoreCase": opts.Project}}},
		}
	}

	query := `query($filter: IssueFilter, $after: String) {
		issues(filter: $filter, first: 250, after: $after) {
			pageInfo { hasNextPage endCursor }
			nodes {
				id identifier title
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate
				createdAt updatedAt
			}
		}
	}`

	var (
		tasks  []Task
		after  string
		pages  int
	)
	const maxPages = 10 // hard cap: 2500 issues per list call
	for {
		vars := map[string]any{"filter": filter}
		if after != "" {
			vars["after"] = after
		}

		var resp struct {
			Data struct {
				Issues struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []linearIssue `json:"nodes"`
				} `json:"issues"`
			} `json:"data"`
		}

		if err := l.graphql(ctx, query, vars, &resp); err != nil {
			return nil, err
		}

		for _, n := range resp.Data.Issues.Nodes {
			tasks = append(tasks, n.toTask(l.name))
		}

		pages++
		if !resp.Data.Issues.PageInfo.HasNextPage || pages >= maxPages {
			break
		}
		after = resp.Data.Issues.PageInfo.EndCursor
	}

	return tasks, nil
}

// buildStateFilter returns a map matching Linear's WorkflowStateFilter.type.nin.
func buildStateFilter(state string) map[string]any {
	nin := []string{"canceled", "completed"}
	switch state {
	case "active":
		nin = []string{"canceled", "completed", "backlog", "triage"}
	case "backlog":
		nin = []string{"canceled", "completed", "started"}
	}
	return map[string]any{"type": map[string]any{"nin": nin}}
}

func (l *linearProvider) GetTask(ctx context.Context, identifier string) (*TaskDetail, error) {
	query := `query($filter: IssueFilter) {
		issues(filter: $filter, first: 1) {
			nodes {
				id identifier title description
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate estimate
				cycle { name number }
				assignee { name }
				comments { nodes { body createdAt user { name } } }
				createdAt updatedAt
			}
		}
	}`

	// Parse identifier like "ABC-42" into team key and issue number
	parts := strings.SplitN(identifier, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid Linear identifier %q (expected format: TEAMKEY-NUMBER, e.g. ABC-42)", identifier)
	}

	vars := map[string]any{
		"filter": map[string]any{
			"team": map[string]any{
				"key": map[string]any{"eq": parts[0]},
			},
			"number": map[string]any{"eq": jsonNumber(parts[1])},
		},
	}

	var resp struct {
		Data struct {
			Issues struct {
				Nodes []linearIssueDetail `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, query, vars, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data.Issues.Nodes) == 0 {
		return nil, fmt.Errorf("issue %s not found in %s", identifier, l.name)
	}

	return resp.Data.Issues.Nodes[0].toTaskDetail(l.name), nil
}

func (l *linearProvider) CreateTask(ctx context.Context, input CreateInput) (*Task, error) {
	// First, resolve team ID from project name/key
	teamID, err := l.resolveTeamID(ctx, input.Project)
	if err != nil {
		return nil, fmt.Errorf("resolving team: %w", err)
	}

	mutation := `mutation($input: IssueCreateInput!) {
		issueCreate(input: $input) {
			success
			issue {
				id identifier title
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate createdAt updatedAt
			}
		}
	}`

	issueInput := map[string]any{
		"teamId": teamID,
		"title":  input.Title,
	}
	if input.Description != "" {
		issueInput["description"] = input.Description
	}
	if input.Priority != "" {
		issueInput["priority"] = priorityToInt(input.Priority)
	}
	if input.StateID != "" {
		issueInput["stateId"] = input.StateID
	}

	vars := map[string]any{"input": issueInput}

	var resp struct {
		Data struct {
			IssueCreate struct {
				Success bool        `json:"success"`
				Issue   linearIssue `json:"issue"`
			} `json:"issueCreate"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, vars, &resp); err != nil {
		return nil, err
	}

	if !resp.Data.IssueCreate.Success {
		return nil, fmt.Errorf("Linear issueCreate returned success=false")
	}

	t := resp.Data.IssueCreate.Issue.toTask(l.name)
	return &t, nil
}

func (l *linearProvider) UpdateTask(ctx context.Context, identifier string, input UpdateInput) (*Task, error) {
	// First get the issue ID
	issueID, err := l.resolveIssueID(ctx, identifier)
	if err != nil {
		return nil, err
	}

	mutation := `mutation($id: String!, $input: IssueUpdateInput!) {
		issueUpdate(id: $id, input: $input) {
			success
			issue {
				id identifier title
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate createdAt updatedAt
			}
		}
	}`

	updateInput := map[string]any{}
	if input.Title != nil {
		updateInput["title"] = *input.Title
	}
	if input.Description != nil {
		updateInput["description"] = *input.Description
	}
	if input.StateID != nil {
		updateInput["stateId"] = *input.StateID
	}
	if input.Priority != nil {
		updateInput["priority"] = priorityToInt(*input.Priority)
	}

	vars := map[string]any{"id": issueID, "input": updateInput}

	var resp struct {
		Data struct {
			IssueUpdate struct {
				Success bool        `json:"success"`
				Issue   linearIssue `json:"issue"`
			} `json:"issueUpdate"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, vars, &resp); err != nil {
		return nil, err
	}

	t := resp.Data.IssueUpdate.Issue.toTask(l.name)
	return &t, nil
}

func (l *linearProvider) SearchTasks(ctx context.Context, query string) ([]Task, error) {
	gql := `query($term: String!) {
		searchIssues(term: $term, first: 50, includeComments: true) {
			nodes {
				id identifier title
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate createdAt updatedAt
			}
		}
	}`

	vars := map[string]any{"term": query}

	var resp struct {
		Data struct {
			SearchIssues struct {
				Nodes []linearIssue `json:"nodes"`
			} `json:"searchIssues"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, gql, vars, &resp); err != nil {
		return nil, err
	}

	var tasks []Task
	for _, n := range resp.Data.SearchIssues.Nodes {
		tasks = append(tasks, n.toTask(l.name))
	}
	return tasks, nil
}

func (l *linearProvider) ListProjects(ctx context.Context) ([]Project, error) {
	query := `{
		teams(first: 100) {
			nodes {
				id name key
				projects(first: 100) {
					nodes { id name }
				}
			}
		}
	}`

	var resp struct {
		Data struct {
			Teams struct {
				Nodes []struct {
					ID       string `json:"id"`
					Name     string `json:"name"`
					Key      string `json:"key"`
					Projects struct {
						Nodes []struct {
							ID   string `json:"id"`
							Name string `json:"name"`
						} `json:"nodes"`
					} `json:"projects"`
				} `json:"nodes"`
			} `json:"teams"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, query, nil, &resp); err != nil {
		return nil, err
	}

	seenProjects := make(map[string]bool) // projects can appear under multiple teams
	var projects []Project
	for _, t := range resp.Data.Teams.Nodes {
		projects = append(projects, Project{
			Source:     l.name,
			SourceType: "linear",
			ID:         t.ID,
			Name:       t.Name,
			Key:        t.Key,
			Kind:       "team",
		})
		for _, p := range t.Projects.Nodes {
			if seenProjects[p.ID] {
				continue
			}
			seenProjects[p.ID] = true
			projects = append(projects, Project{
				Source:     l.name,
				SourceType: "linear",
				ID:         p.ID,
				Name:       p.Name,
				Key:        "",
				Kind:       "project",
				ParentTeam: t.Name,
			})
		}
	}
	return projects, nil
}

func (l *linearProvider) ListStates(ctx context.Context, projectKey string) ([]State, error) {
	query := `{ workflowStates { nodes { id name type team { name key } } } }`

	var resp struct {
		Data struct {
			WorkflowStates struct {
				Nodes []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Type string `json:"type"`
					Team struct {
						Name string `json:"name"`
						Key  string `json:"key"`
					} `json:"team"`
				} `json:"nodes"`
			} `json:"workflowStates"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, query, nil, &resp); err != nil {
		return nil, err
	}

	var states []State
	for _, s := range resp.Data.WorkflowStates.Nodes {
		if projectKey != "" && !strings.EqualFold(s.Team.Key, projectKey) && !strings.EqualFold(s.Team.Name, projectKey) {
			continue
		}
		states = append(states, State{
			ID:      s.ID,
			Name:    s.Name,
			Type:    s.Type,
			Project: s.Team.Name,
		})
	}
	return states, nil
}

// ─── Helpers ──────────────────��─────────────────────────────��────────────────

func (l *linearProvider) graphql(ctx context.Context, query string, variables map[string]any, target any) error {
	body := map[string]any{"query": query}
	if variables != nil {
		body["variables"] = variables
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", linearAPI, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", l.apiKey)

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("Linear API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("Linear API returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Check for GraphQL errors
	var errCheck struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &errCheck); err == nil && len(errCheck.Errors) > 0 {
		return fmt.Errorf("Linear GraphQL error: %s", errCheck.Errors[0].Message)
	}

	return json.Unmarshal(respBody, target)
}

// resolveTeamID accepts a team name, team key, or project name.
// If a project name is given, returns the parent team's ID.
func (l *linearProvider) resolveTeamID(ctx context.Context, nameOrKey string) (string, error) {
	projects, err := l.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	// Prefer exact team match (name or key) first.
	for _, p := range projects {
		if p.Kind != "team" {
			continue
		}
		if strings.EqualFold(p.Name, nameOrKey) || strings.EqualFold(p.Key, nameOrKey) {
			return p.ID, nil
		}
	}
	// Fall back to project name match → return parent team's ID.
	for _, p := range projects {
		if p.Kind != "project" {
			continue
		}
		if strings.EqualFold(p.Name, nameOrKey) {
			for _, t := range projects {
				if t.Kind == "team" && strings.EqualFold(t.Name, p.ParentTeam) {
					return t.ID, nil
				}
			}
		}
	}
	return "", fmt.Errorf("team or project %q not found in %s", nameOrKey, l.name)
}

func (l *linearProvider) resolveIssueID(ctx context.Context, identifier string) (string, error) {
	parts := strings.SplitN(identifier, "-", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid identifier %q", identifier)
	}

	query := `query($filter: IssueFilter) {
		issues(filter: $filter, first: 1) { nodes { id } }
	}`
	vars := map[string]any{
		"filter": map[string]any{
			"team":   map[string]any{"key": map[string]any{"eq": parts[0]}},
			"number": map[string]any{"eq": jsonNumber(parts[1])},
		},
	}

	var resp struct {
		Data struct {
			Issues struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, query, vars, &resp); err != nil {
		return "", err
	}
	if len(resp.Data.Issues.Nodes) == 0 {
		return "", fmt.Errorf("issue %s not found", identifier)
	}
	return resp.Data.Issues.Nodes[0].ID, nil
}

// ─── Types ────────────────────────────────────────────────────────���──────────

type linearIssue struct {
	ID             string `json:"id"`
	Identifier     string `json:"identifier"`
	Title          string `json:"title"`
	Priority       int    `json:"priority"`
	PriorityLabel  string `json:"priorityLabel"`
	State          struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Team struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	} `json:"team"`
	ProjectField *struct {
		Name string `json:"name"`
	} `json:"project"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	DueDate   *string `json:"dueDate"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

type linearIssueDetail struct {
	linearIssue
	Description string   `json:"description"`
	Estimate    *float64 `json:"estimate"`
	Cycle       *struct {
		Name   string `json:"name"`
		Number int    `json:"number"`
	} `json:"cycle"`
	Assignee *struct {
		Name string `json:"name"`
	} `json:"assignee"`
	Comments struct {
		Nodes []struct {
			Body      string `json:"body"`
			CreatedAt string `json:"createdAt"`
			User      struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"nodes"`
	} `json:"comments"`
}

func (n *linearIssue) projectName() string {
	if n.ProjectField != nil {
		return n.ProjectField.Name
	}
	return n.Team.Name
}

func (n *linearIssue) toTask(source string) Task {
	due := ""
	if n.DueDate != nil {
		due = *n.DueDate
	}
	project := n.projectName()
	url := fmt.Sprintf("https://linear.app/issue/%s", n.Identifier)
	return Task{
		Source:     source,
		SourceType: "linear",
		Identifier: n.Identifier,
		Title:      n.Title,
		Status:     n.State.Name,
		StatusType: n.State.Type,
		Priority:   n.PriorityLabel,
		Project:    project,
		DueDate:    due,
		URL:        url,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
	}
}

func (n *linearIssueDetail) toTaskDetail(source string) *TaskDetail {
	t := n.linearIssue.toTask(source)

	var comments []Comment
	for _, c := range n.Comments.Nodes {
		comments = append(comments, Comment{
			Author:    c.User.Name,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		})
	}

	var labels []string
	for _, l := range n.Labels.Nodes {
		labels = append(labels, l.Name)
	}

	assignee := ""
	if n.Assignee != nil {
		assignee = n.Assignee.Name
	}

	milestone := ""
	if n.Cycle != nil {
		milestone = fmt.Sprintf("%s (#%d)", n.Cycle.Name, n.Cycle.Number)
	}

	return &TaskDetail{
		Task:        t,
		Description: n.Description,
		Comments:    comments,
		Labels:      labels,
		Assignee:    assignee,
		Estimate:    n.Estimate,
		Milestone:   milestone,
	}
}

func priorityToInt(p string) int {
	switch strings.ToLower(p) {
	case "urgent":
		return 1
	case "high":
		return 2
	case "medium":
		return 3
	case "low":
		return 4
	default:
		return 0
	}
}

func jsonNumber(s string) json.Number {
	return json.Number(s)
}

// ─── Documents ────────────────────────────────────────────────────────────────

type linearDocument struct {
	ID      string `json:"id"`
	SlugID  string `json:"slugId"`
	Title   string `json:"title"`
	Icon    string `json:"icon"`
	Color   string `json:"color"`
	URL     string `json:"url"`
	Project *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Creator *struct {
		Name string `json:"name"`
	} `json:"creator"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type linearDocumentDetail struct {
	linearDocument
	Content string `json:"content"`
}

func (d *linearDocument) toDocument(source string) Document {
	projectName := ""
	if d.Project != nil {
		projectName = d.Project.Name
	}
	creator := ""
	if d.Creator != nil {
		creator = d.Creator.Name
	}
	return Document{
		Source:     source,
		SourceType: "linear",
		ID:         d.ID,
		SlugID:     d.SlugID,
		Title:      d.Title,
		Icon:       d.Icon,
		Color:      d.Color,
		Project:    projectName,
		Creator:    creator,
		URL:        d.URL,
		CreatedAt:  d.CreatedAt,
		UpdatedAt:  d.UpdatedAt,
	}
}

func (d *linearDocumentDetail) toDocumentDetail(source string) *DocumentDetail {
	base := d.linearDocument.toDocument(source)
	return &DocumentDetail{Document: base, Content: d.Content}
}

const documentFields = `id slugId title icon color url
	project { id name }
	creator { name }
	createdAt updatedAt`

// resolveDocProjectIDs accepts a team name/key, or a project name, and returns matching
// Linear Project IDs. Team match expands to all projects in that team. Empty input → nil.
func (l *linearProvider) resolveDocProjectIDs(ctx context.Context, nameOrKey string) ([]string, error) {
	if nameOrKey == "" {
		return nil, nil
	}
	projects, err := l.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	// Try project match first.
	for _, p := range projects {
		if p.Kind == "project" && strings.EqualFold(p.Name, nameOrKey) {
			return []string{p.ID}, nil
		}
	}
	// Fall back to team match → all projects in that team.
	var out []string
	for _, t := range projects {
		if t.Kind != "team" {
			continue
		}
		if !strings.EqualFold(t.Name, nameOrKey) && !strings.EqualFold(t.Key, nameOrKey) {
			continue
		}
		for _, p := range projects {
			if p.Kind == "project" && strings.EqualFold(p.ParentTeam, t.Name) {
				out = append(out, p.ID)
			}
		}
		break
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no team or project matches %q in %s", nameOrKey, l.name)
	}
	return out, nil
}

func (l *linearProvider) ListDocuments(ctx context.Context, opts DocumentListOpts) ([]Document, error) {
	filter := map[string]any{}
	if opts.Project != "" {
		ids, err := l.resolveDocProjectIDs(ctx, opts.Project)
		if err != nil {
			return nil, err
		}
		filter["project"] = map[string]any{"id": map[string]any{"in": ids}}
	}

	query := fmt.Sprintf(`query($filter: DocumentFilter, $after: String) {
		documents(filter: $filter, first: 100, after: $after) {
			pageInfo { hasNextPage endCursor }
			nodes { %s }
		}
	}`, documentFields)

	var (
		docs  []Document
		after string
		pages int
	)
	const maxPages = 10
	for {
		vars := map[string]any{}
		if len(filter) > 0 {
			vars["filter"] = filter
		}
		if after != "" {
			vars["after"] = after
		}

		var resp struct {
			Data struct {
				Documents struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []linearDocument `json:"nodes"`
				} `json:"documents"`
			} `json:"data"`
		}

		if err := l.graphql(ctx, query, vars, &resp); err != nil {
			return nil, err
		}

		for _, n := range resp.Data.Documents.Nodes {
			if opts.Query != "" && !strings.Contains(strings.ToLower(n.Title), strings.ToLower(opts.Query)) {
				continue
			}
			docs = append(docs, n.toDocument(l.name))
		}

		pages++
		if !resp.Data.Documents.PageInfo.HasNextPage || pages >= maxPages {
			break
		}
		after = resp.Data.Documents.PageInfo.EndCursor
	}

	return docs, nil
}

func (l *linearProvider) GetDocument(ctx context.Context, identifier string) (*DocumentDetail, error) {
	// Try native lookup first (works for both UUID and slugId).
	query := fmt.Sprintf(`query($id: String!) {
		document(id: $id) { %s content }
	}`, documentFields)

	var resp struct {
		Data struct {
			Document *linearDocumentDetail `json:"document"`
		} `json:"data"`
	}

	err := l.graphql(ctx, query, map[string]any{"id": identifier}, &resp)
	if err == nil && resp.Data.Document != nil {
		return resp.Data.Document.toDocumentDetail(l.name), nil
	}

	// Fallback: filter by slugId via documents list.
	fallbackQuery := fmt.Sprintf(`query($filter: DocumentFilter) {
		documents(filter: $filter, first: 1) { nodes { %s content } }
	}`, documentFields)
	vars := map[string]any{
		"filter": map[string]any{"slugId": map[string]any{"eq": identifier}},
	}

	var resp2 struct {
		Data struct {
			Documents struct {
				Nodes []linearDocumentDetail `json:"nodes"`
			} `json:"documents"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, fallbackQuery, vars, &resp2); err != nil {
		return nil, err
	}
	if len(resp2.Data.Documents.Nodes) == 0 {
		return nil, fmt.Errorf("document %q not found in %s", identifier, l.name)
	}
	return resp2.Data.Documents.Nodes[0].toDocumentDetail(l.name), nil
}

func (l *linearProvider) CreateDocument(ctx context.Context, input DocumentCreateInput) (*Document, error) {
	if input.Project == "" {
		return nil, fmt.Errorf("project is required for Linear documents")
	}
	if input.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	projectIDs, err := l.resolveDocProjectIDs(ctx, input.Project)
	if err != nil {
		return nil, err
	}
	if len(projectIDs) != 1 {
		return nil, fmt.Errorf("project %q must match exactly one Linear project (got %d matches); pass a project name, not a team", input.Project, len(projectIDs))
	}

	mutation := fmt.Sprintf(`mutation($input: DocumentCreateInput!) {
		documentCreate(input: $input) {
			success
			document { %s }
		}
	}`, documentFields)

	docInput := map[string]any{
		"title":     input.Title,
		"projectId": projectIDs[0],
	}
	if input.Content != "" {
		docInput["content"] = input.Content
	}
	if input.Icon != "" {
		docInput["icon"] = input.Icon
	}
	if input.Color != "" {
		docInput["color"] = input.Color
	}

	var resp struct {
		Data struct {
			DocumentCreate struct {
				Success  bool           `json:"success"`
				Document linearDocument `json:"document"`
			} `json:"documentCreate"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, map[string]any{"input": docInput}, &resp); err != nil {
		return nil, err
	}
	if !resp.Data.DocumentCreate.Success {
		return nil, fmt.Errorf("Linear documentCreate returned success=false")
	}
	d := resp.Data.DocumentCreate.Document.toDocument(l.name)
	return &d, nil
}

func (l *linearProvider) UpdateDocument(ctx context.Context, identifier string, input DocumentUpdateInput) (*Document, error) {
	docID, err := l.resolveDocID(ctx, identifier)
	if err != nil {
		return nil, err
	}

	mutation := fmt.Sprintf(`mutation($id: String!, $input: DocumentUpdateInput!) {
		documentUpdate(id: $id, input: $input) {
			success
			document { %s }
		}
	}`, documentFields)

	updateInput := map[string]any{}
	if input.Title != nil {
		updateInput["title"] = *input.Title
	}
	if input.Content != nil {
		updateInput["content"] = *input.Content
	}
	if input.Icon != nil {
		updateInput["icon"] = *input.Icon
	}
	if input.Color != nil {
		updateInput["color"] = *input.Color
	}

	var resp struct {
		Data struct {
			DocumentUpdate struct {
				Success  bool           `json:"success"`
				Document linearDocument `json:"document"`
			} `json:"documentUpdate"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, map[string]any{"id": docID, "input": updateInput}, &resp); err != nil {
		return nil, err
	}
	if !resp.Data.DocumentUpdate.Success {
		return nil, fmt.Errorf("Linear documentUpdate returned success=false")
	}
	d := resp.Data.DocumentUpdate.Document.toDocument(l.name)
	return &d, nil
}

func (l *linearProvider) DeleteDocument(ctx context.Context, identifier string) error {
	docID, err := l.resolveDocID(ctx, identifier)
	if err != nil {
		return err
	}

	mutation := `mutation($id: String!) {
		documentDelete(id: $id) { success }
	}`

	var resp struct {
		Data struct {
			DocumentDelete struct {
				Success bool `json:"success"`
			} `json:"documentDelete"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, map[string]any{"id": docID}, &resp); err != nil {
		return err
	}
	if !resp.Data.DocumentDelete.Success {
		return fmt.Errorf("Linear documentDelete returned success=false")
	}
	return nil
}

func (l *linearProvider) SearchDocuments(ctx context.Context, query string) ([]Document, error) {
	gql := fmt.Sprintf(`query($term: String!) {
		searchDocuments(term: $term, first: 50) { nodes { %s } }
	}`, documentFields)

	var resp struct {
		Data struct {
			SearchDocuments struct {
				Nodes []linearDocument `json:"nodes"`
			} `json:"searchDocuments"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, gql, map[string]any{"term": query}, &resp); err != nil {
		return nil, err
	}

	var docs []Document
	for _, n := range resp.Data.SearchDocuments.Nodes {
		docs = append(docs, n.toDocument(l.name))
	}
	return docs, nil
}

// resolveDocID accepts a UUID or slugId and returns the canonical UUID for mutations.
func (l *linearProvider) resolveDocID(ctx context.Context, identifier string) (string, error) {
	d, err := l.GetDocument(ctx, identifier)
	if err != nil {
		return "", err
	}
	return d.ID, nil
}
