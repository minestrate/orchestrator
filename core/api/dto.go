package api

import (
	"time"

	"github.com/mitsuakki/minestrate/core/domain"
)

type CreateServerRequest struct {
	Game        string            `json:"game"`
	Players     int               `json:"players"`
	NetworkName string            `json:"network_name,omitempty"`
	TTLSeconds  int               `json:"ttl_seconds,omitempty"`
	WebhookURL  string            `json:"webhook_url,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type ServerResponse struct {
	ID      string             `json:"id"`
	Game    string             `json:"game"`
	Players int                `json:"players"`
	Address string             `json:"address"`
	Port    int                `json:"port"`
	Created time.Time          `json:"created"`
	State   domain.ServerState `json:"state"`
	Labels  map[string]string  `json:"labels"`
}

func ToServerResponse(s *domain.Server) ServerResponse {
	labels := s.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	return ServerResponse{
		ID:      s.ID,
		Game:    s.Game,
		Players: s.Players,
		Address: s.Address,
		Port:    s.Port,
		Created: s.Created,
		State:   s.State(),
		Labels:  labels,
	}
}

func ToServerListResponse(servers []*domain.Server) []ServerResponse {
	resp := make([]ServerResponse, len(servers))
	for i, s := range servers {
		resp[i] = ToServerResponse(s)
	}
	return resp
}

// ServerListResponse wraps a paginated server list.
type ServerListResponse struct {
	Servers []ServerResponse `json:"servers"`
	Total   int              `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}
