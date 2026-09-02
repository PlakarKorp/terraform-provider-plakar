// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// ScheduleRule is one recurrence of a scheduled task. The id is
// server-assigned per rule.
type ScheduleRule struct {
	ID          *string `json:"id,omitempty"`
	Start       *string `json:"start,omitempty"`
	Periodicity *int64  `json:"periodicity,omitempty"`
	Jitter      *int64  `json:"jitter,omitempty"`
	Enabled     bool    `json:"enabled"`
}

// Schedule mirrors v1's ScheduleConfig.
type Schedule struct {
	Priority     *int64         `json:"priority"`
	Concurrency  *int64         `json:"concurrency"`
	MissStrategy string         `json:"miss_strategy,omitempty"`
	Enabled      bool           `json:"enabled"`
	Rules        []ScheduleRule `json:"rules"`
}

// TaskConfig is the flat per-type config the request carries: labels and
// ignores for backups, the retention buckets for prunes, group_by under
// locate. Unknown keys are silently dropped server-side, so callers validate
// before building one.
type TaskConfig struct {
	Labels    []string       `json:"labels,omitempty"`
	Ignores   []string       `json:"ignores,omitempty"`
	Retention map[string]int `json:"-"`
	GroupBy   string         `json:"-"`
}

// MarshalJSON folds the retention buckets and group_by into the wire shape.
func (c TaskConfig) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if len(c.Labels) > 0 {
		m["labels"] = c.Labels
	}
	if len(c.Ignores) > 0 {
		m["ignores"] = c.Ignores
	}
	for k, v := range c.Retention {
		m[k] = v
	}
	if c.GroupBy != "" {
		m["locate"] = map[string]any{"group_by": c.GroupBy}
	}
	return json.Marshal(m)
}

// TaskRequest is the create shape — and the edit shape, edit being a POST on
// the task id with a full body.
type TaskRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Type        string          `json:"type"`
	Origin      *map[string]any `json:"origin,omitempty"`
	Target      *map[string]any `json:"target,omitempty"`
	Schedule    Schedule        `json:"schedule"`
	Config      TaskConfig      `json:"config"`
}

func connectorRef(id string) *map[string]any {
	if id == "" {
		return nil
	}
	m := map[string]any{"id": id}
	return &m
}

// NewTaskRequest builds the request; target may be empty for task types that
// have none (check).
func NewTaskRequest(name, description, taskType, originID, targetID string, schedule Schedule, config TaskConfig) *TaskRequest {
	return &TaskRequest{
		Name:        name,
		Description: description,
		Type:        taskType,
		Origin:      connectorRef(originID),
		Target:      connectorRef(targetID),
		Schedule:    schedule,
		Config:      config,
	}
}

// Task is the decode-minimal view of a stored task: the response's origin and
// target are fully resolved connector objects and its config is polymorphic,
// so only the stable pieces are read.
type Task struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Origin *struct {
		ID string `json:"id"`
	} `json:"origin"`
	Target *struct {
		ID string `json:"id"`
	} `json:"target"`
	Schedule  Schedule        `json:"schedule"`
	RawConfig json.RawMessage `json:"config"`
}

// ParsedConfig decodes the pieces of a stored config this provider manages.
func (t *Task) ParsedConfig() (labels, ignores []string, retention map[string]int, groupBy string, err error) {
	var cfg struct {
		Labels  []string `json:"labels"`
		Ignores []string `json:"ignores"`
		Locate  struct {
			GroupBy *string `json:"group_by"`
		} `json:"locate"`
		Minute    *int `json:"minute"`
		PerMinute *int `json:"per_minute"`
		Hour      *int `json:"hour"`
		PerHour   *int `json:"per_hour"`
		Day       *int `json:"day"`
		PerDay    *int `json:"per_day"`
		Week      *int `json:"week"`
		PerWeek   *int `json:"per_week"`
		Month     *int `json:"month"`
		PerMonth  *int `json:"per_month"`
		Year      *int `json:"year"`
		PerYear   *int `json:"per_year"`
	}
	if len(t.RawConfig) == 0 {
		return nil, nil, nil, "", nil
	}
	if err := json.Unmarshal(t.RawConfig, &cfg); err != nil {
		return nil, nil, nil, "", err
	}
	retention = map[string]int{}
	for k, v := range map[string]*int{
		"minute": cfg.Minute, "per_minute": cfg.PerMinute,
		"hour": cfg.Hour, "per_hour": cfg.PerHour,
		"day": cfg.Day, "per_day": cfg.PerDay,
		"week": cfg.Week, "per_week": cfg.PerWeek,
		"month": cfg.Month, "per_month": cfg.PerMonth,
		"year": cfg.Year, "per_year": cfg.PerYear,
	} {
		if v != nil {
			retention[k] = *v
		}
	}
	if cfg.Locate.GroupBy != nil {
		groupBy = *cfg.Locate.GroupBy
	}
	return cfg.Labels, cfg.Ignores, retention, groupBy, nil
}

// Task mutations are serialized: two concurrent creations race the
// scheduler's revision bump server-side and the loser answers 404, so a
// terraform apply creating several schedules in parallel would fail
// spuriously. One writer at a time within this provider instance.

func (c *Client) CreateTask(req *TaskRequest) (*Task, error) {
	c.taskMu.Lock()
	defer c.taskMu.Unlock()
	var out Task
	if err := c.Do("POST", "/api/v1/scheduling/scheduler/tasks", req, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateTask(id string, req *TaskRequest) error {
	c.taskMu.Lock()
	defer c.taskMu.Unlock()
	// v1 edit is a POST on the task id with a full body.
	return c.Do("POST", "/api/v1/scheduling/scheduler/tasks/"+id, req, nil, nil)
}

func (c *Client) DeleteTask(id string) error {
	c.taskMu.Lock()
	defer c.taskMu.Unlock()
	return c.Do("DELETE", "/api/v1/scheduling/scheduler/tasks/"+id, nil, nil, nil)
}

// GetTask walks the paginated task list looking for one id: v1 has no
// single-task GET, only edit and delete hang off the id.
func (c *Client) GetTask(id string) (*Task, error) {
	offset := 0
	for {
		q := url.Values{}
		q.Set("limit", "50")
		q.Set("offset", fmt.Sprint(offset))
		var page struct {
			Total int    `json:"total"`
			Items []Task `json:"items"`
		}
		if err := c.Do("GET", "/api/v1/scheduling/scheduler/tasks", nil, q, &page); err != nil {
			return nil, err
		}
		for i := range page.Items {
			if page.Items[i].ID == id {
				return &page.Items[i], nil
			}
		}
		offset += len(page.Items)
		if len(page.Items) == 0 || offset >= page.Total {
			return nil, &Error{Method: "GET", Path: "/api/v1/scheduling/scheduler/tasks",
				Status: 404, Detail: fmt.Sprintf("task %s not found", id)}
		}
	}
}
