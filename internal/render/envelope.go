package render

import (
	"encoding/json"
	"io"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/plan"
)

type Meta struct {
	Site   string `json:"site"`
	Count  *int   `json:"count,omitempty"`
	DryRun bool   `json:"dry_run"`
}

type Envelope struct {
	SchemaVersion string     `json:"schema_version"`
	OK            bool       `json:"ok"`
	Resource      string     `json:"resource"`
	Action        string     `json:"action"`
	Data          any        `json:"data"`
	Meta          Meta       `json:"meta"`
	Error         *ErrorBody `json:"error,omitempty"`
	Plan          *plan.Plan `json:"plan,omitempty"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func Success(resource, action, site string, data any, dryRun bool) Envelope {
	env := Envelope{
		SchemaVersion: "1", OK: true, Resource: resource, Action: action,
		Data: data, Meta: Meta{Site: site, DryRun: dryRun},
	}
	switch t := data.(type) {
	case []any:
		n := len(t)
		env.Meta.Count = &n
	default:
		// domain list helpers should pass slices; count set by caller when needed
	}
	return env
}

func Fail(resource, action, site string, err error) Envelope {
	env := Envelope{
		SchemaVersion: "1", OK: false, Resource: resource, Action: action,
		Data: nil, Meta: Meta{Site: site},
	}
	if e := apperr.As(err); e != nil {
		env.Error = &ErrorBody{Code: string(e.Code), Message: e.Message, Hint: e.Hint}
	} else if err != nil {
		env.Error = &ErrorBody{Code: string(apperr.Internal), Message: err.Error()}
	}
	return env
}

func WriteJSON(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if apperr.Is(err, apperr.ValidationFailed) {
		return 2
	}
	return 1
}
