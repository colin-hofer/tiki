// Package tiki owns the task model, validation, and transactional SQLite store.
// It is independent of HTTP and CLI concerns. Callers are trusted; network
// authentication and authorization are enforced by internal/httpapi.
package tiki

import (
	"encoding/json"
	"strconv"
)

const (
	DefaultPageSize     = 50
	MaxPageSize         = 200
	MaxDescriptionBytes = 256 << 10
)

// ID uses strings on the wire to preserve int64 precision in JavaScript.
type ID int64

func (id ID) String() string { return strconv.FormatInt(int64(id), 10) }
func (id ID) MarshalJSON() ([]byte, error) {
	return append(strconv.AppendInt([]byte{'"'}, int64(id), 10), '"'), nil
}
func (id *ID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return invalid("ID must be a decimal string")
	}
	v, err := ParseID(s)
	if err != nil {
		return err
	}
	*id = v
	return nil
}
func ParseID(s string) (ID, error) {
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, invalid("ID must be a positive decimal integer")
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, invalid("ID must be a positive decimal integer")
	}
	return ID(n), nil
}

type Status string
type Type string

const (
	StatusBacklog    Status = "backlog"
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusCodeReview Status = "code_review"
	StatusBlocked    Status = "blocked"
	StatusComplete   Status = "complete"
	StatusVoid       Status = "void"
)
const (
	ItemTypeBug     Type = "bug"
	ItemTypeFeature Type = "feature"
	ItemTypeTask    Type = "task"
)

type User struct {
	ID        ID     `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email,omitempty"`
	Role      string `json:"role"`
	RemovedAt int64  `json:"removed_at,omitempty"`
}

type UserPage struct {
	Users     []User `json:"users"`
	NextAfter ID     `json:"next_after,omitempty"`
}

type TagPage struct {
	Tags      []string         `json:"tags"`
	Usage     map[string]int64 `json:"usage,omitempty"`
	NextAfter string           `json:"next_after,omitempty"`
}

type Session struct {
	User      User   `json:"user"`
	Token     string `json:"session_token"`
	ExpiresAt int64  `json:"expires_at"`
}

type Item struct {
	ID          ID       `json:"id"`
	Type        Type     `json:"type"`
	Status      Status   `json:"status"`
	Priority    float64  `json:"priority"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	CreatedBy   ID       `json:"created_by"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	Version     int64    `json:"version"`
	Assignees   []ID     `json:"assignees"`
	Tags        []string `json:"tags"`
}

type CreateItem struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Type        Type     `json:"type"`
	Status      Status   `json:"status"`
	Priority    *float64 `json:"priority,omitempty"`
	Assignees   []ID     `json:"assignees"`
	Tags        []string `json:"tags"`
}

type UpdateItem struct {
	Version         int64    `json:"version"`
	Title           *string  `json:"title,omitempty"`
	Description     *string  `json:"description,omitempty"`
	Type            *Type    `json:"type,omitempty"`
	Status          *Status  `json:"status,omitempty"`
	Priority        *float64 `json:"priority,omitempty"`
	AddAssignees    []ID     `json:"add_assignees,omitempty"`
	RemoveAssignees []ID     `json:"remove_assignees,omitempty"`
	AddTags         []string `json:"add_tags,omitempty"`
	RemoveTags      []string `json:"remove_tags,omitempty"`
}

type MoveItem struct {
	Version int64   `json:"version"`
	Before  ID      `json:"before,omitempty"`
	After   ID      `json:"after,omitempty"`
	Status  *Status `json:"status,omitempty"`
}

type Filter struct {
	Tags       []string
	Assignee   ID
	Unassigned bool
	Status     Status
	Limit      int
	Cursor     string
}

type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type Activity struct {
	ID        ID              `json:"id"`
	ItemID    *ID             `json:"item_id,omitempty"`
	ActorID   ID              `json:"actor_id"`
	Kind      string          `json:"kind"`
	CreatedAt string          `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

type ActivityPage struct {
	Activity  []Activity `json:"activity"`
	NextAfter ID         `json:"next_after,omitempty"`
}

type Error struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	CurrentVersion int64  `json:"current_version,omitempty"`
}

func (e *Error) Error() string      { return e.Message }
func invalid(message string) *Error { return &Error{Code: "validation", Message: message} }
func missing() *Error               { return &Error{Code: "not_found", Message: "record not found"} }
func conflict(version int64) *Error {
	return &Error{Code: "conflict", Message: "item changed; reload before retrying", CurrentVersion: version}
}
