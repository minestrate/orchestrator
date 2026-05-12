package api

type CreateServerRequest struct {
	Game    string `json:"game"`
	Players int    `json:"players"`
}
