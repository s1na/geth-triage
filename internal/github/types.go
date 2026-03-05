package github

import "time"

type PRData struct {
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	Author        string    `json:"author"`
	State         string    `json:"state"`
	Labels        []string  `json:"labels"`
	HeadSHA       string    `json:"head_sha"`
	Additions     int       `json:"additions"`
	Deletions     int       `json:"deletions"`
	CommentsCount int       `json:"comments_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Diff          string    `json:"diff"`
	Comments      []Comment `json:"comments"`
}

type Comment struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}
