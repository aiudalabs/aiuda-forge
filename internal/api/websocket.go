package api

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
)

// websocket streams live events to the client. Optional ?run=<id> filters to one
// run. This is the §B live-push transport; GET /runs/{id}/events is the replay
// counterpart. Both read the same persisted events — one source of truth.
func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()

	runID := r.URL.Query().Get("run")
	id, ch := s.Bus.Subscribe(runID)
	defer s.Bus.Unsubscribe(id)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			if err := c.Write(ctx, websocket.MessageText, b); err != nil {
				return
			}
		}
	}
}
