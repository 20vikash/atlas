package network

import (
	"fmt"

	"github.com/cilium/ebpf"
)

// bpfActivityLoader creates the real kernel objects with cilium/ebpf. It holds
// no state, so one instance serves every VM.
type bpfActivityLoader struct{}

// createMap creates the shared activity hash map with the given capacity.
func (bpfActivityLoader) createMap(capacity uint32) (activityMap, error) {
	spec, err := loadActivity()
	if err != nil {
		return nil, err
	}

	mapSpec := spec.Maps["activity_by_user_id"]
	mapSpec.MaxEntries = capacity
	kernelMap, err := ebpf.NewMap(mapSpec)
	if err != nil {
		return nil, err
	}

	return &bpfActivityMap{kernelMap: kernelMap}, nil
}

// loadProgram loads one program instance. It rewrites the read-only user ID
// constant and shares the one activity map instead of a per-program map.
func (bpfActivityLoader) loadProgram(userID uint32, shared activityMap) (activityProgram, error) {
	sharedMap, ok := shared.(*bpfActivityMap)
	if !ok {
		return nil, fmt.Errorf("shared map has unexpected type %T", shared)
	}

	spec, err := loadActivity()
	if err != nil {
		return nil, err
	}
	if err := spec.Variables["virtual_machine_user_id"].Set(userID); err != nil {
		return nil, fmt.Errorf("set user id constant: %w", err)
	}

	var programs activityPrograms
	options := &ebpf.CollectionOptions{
		MapReplacements: map[string]*ebpf.Map{"activity_by_user_id": sharedMap.kernelMap},
	}
	if err := spec.LoadAndAssign(&programs, options); err != nil {
		return nil, err
	}

	return &bpfActivityProgram{program: programs.RecordActivity}, nil
}

// bpfActivityMap wraps the shared kernel map.
type bpfActivityMap struct {
	kernelMap *ebpf.Map
}

// Close releases the shared kernel map.
func (handle *bpfActivityMap) Close() error { return handle.kernelMap.Close() }

// bpfActivityProgram wraps one loaded program instance. Later commits add its
// TCX links.
type bpfActivityProgram struct {
	program *ebpf.Program
}

// Close releases the program instance.
func (handle *bpfActivityProgram) Close() error { return handle.program.Close() }
