package store

import "time"

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

type Project struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	RepoURL   string    `json:"repoUrl"`
	DocRoots  []string  `json:"docRoots"`
	OwnerID   string    `json:"ownerId"`
	OwnerName string    `json:"ownerName"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Branches  []Branch  `json:"branches"`
}

type Branch struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"projectId"`
	Name         string     `json:"name"`
	CommitSHA    string     `json:"commitSha"`
	Status       string     `json:"status"`
	LastError    string     `json:"lastError"`
	LastSyncedAt *time.Time `json:"lastSyncedAt"`
}

type Document struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

type SearchResult struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
}

type SyncJob struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"projectId"`
	Branch     string     `json:"branch"`
	Status     string     `json:"status"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

type DocFile struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	Markdown  string `json:"markdown"`
	PlainText string `json:"plainText"`
}
