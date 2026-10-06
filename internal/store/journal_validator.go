package store

import (
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

// References are reconstructed for each complete candidate replay.
type journalValidator struct {
	baseLen         int64
	produced, begun map[int64]bool
	terminal        bool
}

func newValidator(n int) *journalValidator {
	return &journalValidator{baseLen: int64(n), produced: map[int64]bool{}, begun: map[int64]bool{}}
}
func (v *journalValidator) add(e apitype.JournalEntry) error {
	if e.Version != 1 || e.Kind < 0 || e.Kind > 7 || e.SequenceID <= 0 || e.OperationID < 0 {
		return Fail(422, "Unsupported journal entry")
	}
	if v.terminal {
		return Fail(400, "Journal already rebuilt")
	}
	for _, i := range []*int64{e.RemoveOld, e.DeleteOld, e.PendingReplacementOld} {
		if i != nil && (*i < 0 || *i >= v.baseLen) {
			return Fail(400, "Invalid old resource reference")
		}
	}
	for _, i := range []*int64{e.RemoveNew, e.DeleteNew, e.PendingReplacementNew} {
		if i != nil && !v.produced[*i] {
			return Fail(400, "Invalid new resource reference")
		}
	}
	switch e.Kind {
	case 0:
		if v.begun[e.OperationID] {
			return Fail(400, "Operation already begun")
		}
		v.begun[e.OperationID] = true
	case 1, 2, 3:
		if !v.begun[e.OperationID] {
			return Fail(400, "Completion requires begin")
		}
		delete(v.begun, e.OperationID)
		if e.Kind == 1 && e.State != nil {
			if v.produced[e.OperationID] {
				return Fail(400, "Duplicate resource operation")
			}
			v.produced[e.OperationID] = true
		}
	case 4:
		if e.State == nil {
			return Fail(400, "Outputs requires state")
		}
	case 5:
		if e.NewSnapshot == nil {
			return Fail(400, "Write requires snapshot")
		}
		if len(v.begun) > 0 || len(v.produced) > 0 {
			return Fail(400, "Write must precede operations")
		}
		v.baseLen = int64(len(e.NewSnapshot.Resources))
	case 6:
		if e.SecretsProvider == nil {
			return Fail(400, "Missing secrets provider")
		}
	case 7:
		if len(v.begun) > 0 {
			return Fail(400, "Rebuild with pending operation")
		}
		v.terminal = len(v.produced) > 0
	}
	return nil
}
