package api

import "github.com/mitsuakki/minestrate/internal/server"

type ServerResponse struct {
	ID      string             `json:"id"`
	Game    string             `json:"game"`
	Players int                `json:"players"`
	Address string             `json:"address"`
	Port    int                `json:"port"`
	State   server.ServerState `json:"state"`
}

func ToServerResponse(s *server.Server) ServerResponse {
	return ServerResponse{
		ID:      s.ID,
		Game:    s.Game,
		Players: s.Players,
		Address: s.Address,
		Port:    s.Port,
		State:   s.State(),
	}
}

func ToServerListResponse(servers []*server.Server) []ServerResponse {
	resp := make([]ServerResponse, len(servers))
	for i, s := range servers {
		resp[i] = ToServerResponse(s)
	}
	return resp
}
