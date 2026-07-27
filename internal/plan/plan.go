package plan

type Change struct {
	Op       string `json:"op"`
	Resource string `json:"resource"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Before   any    `json:"before,omitempty"`
	After    any    `json:"after,omitempty"`
}

type Plan struct {
	Summary string   `json:"summary"`
	Changes []Change `json:"changes"`
}
