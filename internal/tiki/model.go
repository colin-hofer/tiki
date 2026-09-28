// Package tiki owns the task model, validation, and transactional SQLite store.
// It is independent of HTTP and CLI concerns. Callers are trusted; network
// authentication and authorization are enforced by internal/httpapi.
package tiki

import (
	"encoding/json"
	"strconv"
)

const (
	// DefaultPageSize is used when an item filter omits its limit.
	DefaultPageSize = 50
	// MaxPageSize bounds all paginated reads.
	MaxPageSize = 200
	// MaxDescriptionBytes bounds a ticket's UTF-8 description.
	MaxDescriptionBytes = 256 << 10
)

// ID uses strings on the wire to preserve int64 precision in JavaScript.
type ID int64

// String returns the decimal representation of id.
func (id ID) String() string { return strconv.FormatInt(int64(id), 10) }

// MarshalJSON encodes an ID as a decimal string.
func (id ID) MarshalJSON() ([]byte, error) {
	return append(strconv.AppendInt([]byte{'"'}, int64(id), 10), '"'), nil
}

// UnmarshalJSON accepts only quoted positive decimal IDs.
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

// ParseID accepts positive decimal IDs, including leading zeroes.
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

// Status is a ticket's workflow state.
type Status string

const (
	StatusBacklog    Status = "backlog"
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusCodeReview Status = "code_review"
	StatusBlocked    Status = "blocked"
	StatusComplete   Status = "complete"
	StatusVoid       Status = "void"
)

// Valid reports whether s is a supported workflow state.
func (s Status) Valid() bool {
	switch s {
	case StatusBacklog, StatusTodo, StatusInProgress, StatusCodeReview, StatusBlocked, StatusComplete, StatusVoid:
		return true
	}
	return false
}

// ItemType describes the kind of work a ticket tracks.
type ItemType string

const (
	ItemTypeBug     ItemType = "bug"
	ItemTypeFeature ItemType = "feature"
	ItemTypeTask    ItemType = "task"
)

// Valid reports whether t is a supported ticket type.
func (t ItemType) Valid() bool {
	return t == ItemTypeBug || t == ItemTypeFeature || t == ItemTypeTask
}

// Role controls a user's workspace permissions.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

// Valid reports whether r is a supported workspace role.
func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleMember || r == RoleViewer
}

// User is a workspace identity. Removed users retain their history and assignments.
type User struct {
	ID        ID     `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email,omitempty"`
	Role      Role   `json:"role"`
	RemovedAt int64  `json:"removed_at,omitempty"`
}

// UserPage lists identities by ID, including removed users.
type UserPage struct {
	Users     []User `json:"users"`
	NextAfter ID     `json:"next_after,omitempty"`
}

// TagPage lists tags by name, optionally including ticket counts.
type TagPage struct {
	Tags      []string         `json:"tags"`
	Usage     map[string]int64 `json:"usage,omitempty"`
	NextAfter string           `json:"next_after,omitempty"`
}

// Session contains a bearer token and its expiry as Unix seconds.
type Session struct {
	User      User   `json:"user"`
	Token     string `json:"session_token"`
	ExpiresAt int64  `json:"expires_at"`
}

// Item is a versioned ticket. Lists omit Description and carry Preview instead.
type Item struct {
	ID          ID       `json:"id"`
	Type        ItemType `json:"type"`
	Status      Status   `json:"status"`
	Priority    float64  `json:"priority"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url,omitempty"`
	Preview     string   `json:"preview,omitempty"`
	CreatedBy   ID       `json:"created_by"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	Version     int64    `json:"version"`
	Assignees   []ID     `json:"assignees"`
	Tags        []string `json:"tags"`
}

// CreateItem defaults to task, backlog, and the end of the priority order.
type CreateItem struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	URL         string   `json:"url,omitempty"`
	Type        ItemType `json:"type"`
	Status      Status   `json:"status"`
	Priority    *float64 `json:"priority,omitempty"`
	Assignees   []ID     `json:"assignees"`
	Tags        []string `json:"tags"`
}

// UpdateItem applies a version-checked patch. Nil fields are unchanged;
// non-nil fields replace values, including an empty description or URL.
// Membership edits are set operations.
type UpdateItem struct {
	Version         int64     `json:"version"`
	Title           *string   `json:"title,omitempty"`
	Description     *string   `json:"description,omitempty"`
	URL             *string   `json:"url,omitempty"`
	Type            *ItemType `json:"type,omitempty"`
	Status          *Status   `json:"status,omitempty"`
	Priority        *float64  `json:"priority,omitempty"`
	AddAssignees    []ID      `json:"add_assignees,omitempty"`
	RemoveAssignees []ID      `json:"remove_assignees,omitempty"`
	AddTags         []string  `json:"add_tags,omitempty"`
	RemoveTags      []string  `json:"remove_tags,omitempty"`
}

// MoveItem places a ticket immediately before or after one other ticket,
// optionally changing status in the same version-checked transaction.
type MoveItem struct {
	Version int64   `json:"version"`
	Before  ID      `json:"before,omitempty"`
	After   ID      `json:"after,omitempty"`
	Status  *Status `json:"status,omitempty"`
}

// Filter selects tickets in ascending (priority, ID) order. All tags must match;
// a cursor belongs to its original filter. A zero limit uses DefaultPageSize.
type Filter struct {
	Tags       []string
	Assignee   ID
	Unassigned bool
	Status     Status
	Query      string
	Limit      int
	Cursor     string
}

// Page contains tickets in priority order and an opaque continuation cursor.
type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Activity is a committed timeline entry. A nil ItemID marks a workspace event.
type Activity struct {
	ID        ID              `json:"id"`
	ItemID    *ID             `json:"item_id,omitempty"`
	ActorID   ID              `json:"actor_id"`
	Kind      string          `json:"kind"`
	CreatedAt string          `json:"created_at"`
	Data      json.RawMessage `json:"data"`
	ClientID  string          `json:"client_id,omitempty"`
}

// ActivityPage contains chronological events, with a cursor for older or newer history.
type ActivityPage struct {
	Activity   []Activity `json:"activity"`
	NextAfter  ID         `json:"next_after,omitempty"`
	NextBefore ID         `json:"next_before,omitempty"`
}

// Error is a client-visible failure shared by the store, HTTP API, and CLI.
// Code is stable; Message is intended for people.
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
