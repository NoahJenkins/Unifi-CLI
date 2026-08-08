package domain

import (
	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/plan"
)

// requirePreparedTarget binds a mutation's final request preparation to the
// immutable target snapshot approved by the CLI. Callers must invoke it after
// their last controller read and immediately before the mutation request.
func requirePreparedTarget(target plan.Target, current any) error {
	matches, err := target.Matches(current)
	if err != nil {
		return apperr.WithCause(
			apperr.New(apperr.Internal, "could not compare prepared target state"), err)
	}
	if !matches {
		return apperr.WithHint(
			apperr.New(apperr.Conflict, "target changed after the mutation plan was prepared"),
			"rerun the command to inspect a fresh plan")
	}
	return nil
}
