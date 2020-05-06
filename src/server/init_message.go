package server

type InitMessage struct {
	Arguments string 				`json:"Arguments,omitempty"`
	AuthToken string 				`json:"AuthToken,omitempty"`
	Payload   map[string]string 	`json:"Payload,omitempty"`
}
