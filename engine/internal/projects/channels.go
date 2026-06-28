package projects

import "strings"

// Channel is a project's link to an external channel (v1.3): which connector and
// target (e.g. a Telegram chat id) receives which events. Events is a comma list of
// event types, or "*" for all notable events. The connector's credential (bot
// token) is NOT here — it lives per-instance in settings.MCP[connector].
type Channel struct {
	ProjectID string `json:"project_id"`
	Connector string `json:"connector"`
	Target    string `json:"target"`
	Events    string `json:"events"`
	CreatedAt int64  `json:"created_at"`
}

// LinkChannel records (or updates) a channel link for a project. events defaults to
// "*". Upsert on (project_id, connector, target).
func (s *Store) LinkChannel(projectID, connector, target, events string) error {
	if events == "" {
		events = "*"
	}
	_, err := s.db.Exec(
		`INSERT INTO project_channels(project_id, connector, target, events, created_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(project_id, connector, target) DO UPDATE SET events=excluded.events`,
		projectID, connector, target, events, s.now())
	return err
}

// UnlinkChannel removes a channel link.
func (s *Store) UnlinkChannel(projectID, connector, target string) error {
	_, err := s.db.Exec(
		`DELETE FROM project_channels WHERE project_id=? AND connector=? AND target=?`,
		projectID, connector, target)
	return err
}

// Channels lists a project's channel links (oldest first).
func (s *Store) Channels(projectID string) ([]Channel, error) {
	return s.queryChannels(
		`SELECT project_id, connector, target, events, created_at FROM project_channels
		 WHERE project_id=? ORDER BY created_at ASC`, projectID)
}

// ChannelsForEvent returns a project's channels subscribed to eventType: those whose
// events list is "*" or contains the type. This is what the delivery layer calls per
// event to decide where to fan it out.
func (s *Store) ChannelsForEvent(projectID, eventType string) ([]Channel, error) {
	all, err := s.Channels(projectID)
	if err != nil {
		return nil, err
	}
	var out []Channel
	for _, c := range all {
		if subscribed(c.Events, eventType) {
			out = append(out, c)
		}
	}
	return out, nil
}

// subscribed reports whether an events spec ("*" or a comma list) includes type.
func subscribed(events, eventType string) bool {
	events = strings.TrimSpace(events)
	if events == "" || events == "*" {
		return true
	}
	for _, e := range strings.Split(events, ",") {
		if strings.TrimSpace(e) == eventType {
			return true
		}
	}
	return false
}

func (s *Store) queryChannels(q string, args ...any) ([]Channel, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ProjectID, &c.Connector, &c.Target, &c.Events, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
