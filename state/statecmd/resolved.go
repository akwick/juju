package statecmd

import (
	"fmt"
	"launchpad.net/juju-core/state"
)

type ResolvedParams struct {
	UnitName string
	Retry    bool
}

type ResolvedResults struct {
	Service  string
	Charm    string
	Settings map[string]interface{}
}

// MarkResolved marks a unit as having had any previous state transition
// problems resolved, and informs the unit that it may attempt to reestablish
// normal workflow. The retryHooks parameter informs whether to attempt to
// reexecute previous failed hooks or to continue as if they had succeeded
// before.
func MarkResolved(unit *state.Unit, retryHooks bool) error {
	status, _, err := unit.Status()
	if err != nil {
		return err
	}
	if status != state.UnitError {
		return fmt.Errorf("unit %q is not in an error state", unit)
	}
	mode := state.ResolvedNoHooks
	if retryHooks {
		mode = state.ResolvedRetryHooks
	}
	return unit.SetResolved(mode)
}

func Resolved(st *state.State, p ResolvedParams) error {
	unit, err := st.Unit(p.UnitName)
	if err != nil {
		return err
	}
	return MarkResolved(unit, p.Retry)
}
