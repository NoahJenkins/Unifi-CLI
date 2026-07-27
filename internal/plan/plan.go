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

func Update(resource, id, name, summary string, before, after any) Plan {
	return Plan{
		Summary: summary,
		Changes: []Change{{Op: "update", Resource: resource, ID: id, Name: name, Before: before, After: after}},
	}
}

func Create(resource, name, summary string, after any) Plan {
	return Plan{
		Summary: summary,
		Changes: []Change{{Op: "create", Resource: resource, Name: name, After: after}},
	}
}

func Delete(resource, id, name, summary string, before any) Plan {
	return Plan{
		Summary: summary,
		Changes: []Change{{Op: "delete", Resource: resource, ID: id, Name: name, Before: before}},
	}
}
